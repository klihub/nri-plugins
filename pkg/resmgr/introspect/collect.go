// Copyright The NRI Plugins Authors. All Rights Reserved.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package introspect

import (
	"slices"
	"sort"
	"strings"

	nri "github.com/containerd/nri/pkg/api"
	v1 "k8s.io/api/core/v1"

	"github.com/containers/nri-plugins/pkg/introspect/api"
	logger "github.com/containers/nri-plugins/pkg/log"
	"github.com/containers/nri-plugins/pkg/resmgr/cache"
	"github.com/containers/nri-plugins/pkg/topology"

	system "github.com/containers/nri-plugins/pkg/sysfs"
)

// Our logger instance.
var log = logger.Get("introspect")

// collectHardware describes the machine. It asks sysfs only; no policy and
// no cache are involved, so this section is always available and can never
// go stale with respect to a policy's participation.
func collectHardware(sys system.System) *api.Hardware {
	if sys == nil {
		return nil
	}

	hw := &api.Hardware{
		IsolatedCPUs: sys.Isolated().String(),
		OfflineCPUs:  sys.Offlined().String(),
	}

	for _, id := range sys.PackageIDs() {
		pkg := sys.Package(id)
		p := &api.Package{
			ID:   int(id),
			CPUs: pkg.CPUSet().String(),
		}

		for _, dieID := range pkg.DieIDs() {
			d := &api.Die{
				ID:   int(dieID),
				CPUs: pkg.DieCPUSet(dieID).String(),
			}
			for _, nodeID := range pkg.DieNodeIDs(dieID) {
				d.NUMANodes = append(d.NUMANodes, int(nodeID))
			}
			for _, clusterID := range pkg.DieClusterIDs(dieID) {
				d.Clusters = append(d.Clusters, &api.Cluster{
					ID:   int(clusterID),
					CPUs: pkg.DieClusterCPUSet(dieID, clusterID).String(),
				})
			}
			p.Dies = append(p.Dies, d)
		}

		hw.Packages = append(hw.Packages, p)
	}

	for _, id := range sys.NodeIDs() {
		node := sys.Node(id)
		n := &api.NUMANode{
			ID:           int(id),
			CPUs:         node.CPUSet().String(),
			MemoryType:   node.GetMemoryType().String(),
			NormalMemory: node.HasNormalMemory(),
			// Cloned: sysfs hands out its own slice, and a snapshot
			// must not alias discovered system state.
			Distance: slices.Clone(node.Distance()),
		}
		// A memory-only node belongs to no package or die. sysfs
		// leaves both IDs at zero for such a node, which is
		// indistinguishable from a real package 0, so association is
		// reported only for nodes that actually have CPUs.
		if !node.CPUSet().IsEmpty() {
			pkgID, dieID := int(node.PackageID()), int(node.DieID())
			n.Package, n.Die = &pkgID, &dieID
		}
		if info, err := node.MemoryInfo(); err != nil || info == nil {
			log.Warnf("no memory info for NUMA node %d: %v", id, err)
		} else {
			n.Capacity = int64(info.MemTotal)
		}
		hw.NUMANodes = append(hw.NUMANodes, n)
	}

	// Collect the core-kind sets first, so that each CPU can be tagged
	// with its own kind below and a consumer needs no reverse lookup.
	// The sets are used rather than sysfs.CPU.CoreKind() because the
	// latter's zero value is "P-core", which would label every CPU on
	// hardware that reports no core kinds at all.
	kindOfCPU := map[int]string{}
	for _, kind := range sys.CoreKinds() {
		cpus := sys.CoreKindCPUs(kind)
		if cpus.IsEmpty() {
			continue
		}
		if hw.CoreKinds == nil {
			hw.CoreKinds = map[string]string{}
		}
		hw.CoreKinds[kind.String()] = cpus.String()
		for _, cpu := range cpus.List() {
			kindOfCPU[cpu] = kind.String()
		}
	}

	for _, id := range sys.CPUIDs() {
		cpu := sys.CPU(id)
		freq := cpu.FrequencyRange()
		c := &api.CPU{
			ID:       int(id),
			Package:  int(cpu.PackageID()),
			Die:      int(cpu.DieID()),
			Cluster:  int(cpu.ClusterID()),
			NUMANode: int(cpu.NodeID()),
			Core:     int(cpu.CoreID()),
			Threads:  cpu.ThreadCPUSet().String(),
			CoreKind: kindOfCPU[int(id)],
			Online:   cpu.Online(),
			Isolated: cpu.Isolated(),
			BaseFreq: freq.Base,
			MinFreq:  freq.Min,
			MaxFreq:  freq.Max,
		}
		hw.CPUs = append(hw.CPUs, c)
	}

	return hw
}

// Cache is the part of the resource manager cache the collector needs.
// The interfaces here are narrow on purpose: cache.Cache, cache.Pod and
// cache.Container satisfy them structurally, and a test fake is a dozen
// methods rather than a hundred.
type Cache interface {
	GetPods() []Pod
	GetContainers() []Container
}

// Pod is the part of a cached pod the collector needs.
type Pod interface {
	GetID() string
	GetUID() string
	GetName() string
	GetNamespace() string
	GetQOSClass() v1.PodQOSClass
}

// Container is the part of a cached container the collector needs.
type Container interface {
	GetID() string
	GetPodID() string
	GetName() string
	GetState() nri.ContainerState
	GetQOSClass() v1.PodQOSClass
	GetResourceRequirements() v1.ResourceRequirements
	GetCpusetCpus() string
	GetCpusetMems() string
	GetCPUShares() int64
	GetCPUQuota() int64
	GetCPUPeriod() int64
	GetMemoryLimit() int64
	GetRDTClass() string
	GetBlockIOClass() string
	GetTopologyHints() topology.Hints
}

// Make sure the real cache types keep satisfying our interfaces.
var (
	_ Pod       = cache.Pod(nil)
	_ Container = cache.Container(nil)
)

// collectWorkloads describes the pods and containers on this node, as the
// cache knows them. Command line arguments are deliberately not exposed.
func collectWorkloads(c Cache) ([]*api.Pod, []*api.Container) {
	if c == nil {
		return nil, nil
	}

	var (
		pods       []*api.Pod
		containers []*api.Container
	)

	for _, p := range c.GetPods() {
		pods = append(pods, &api.Pod{
			ID:        p.GetID(),
			UID:       p.GetUID(),
			Name:      p.GetName(),
			Namespace: p.GetNamespace(),
			QOSClass:  string(p.GetQOSClass()),
		})
	}

	for _, ctr := range c.GetContainers() {
		container := &api.Container{
			ID:       ctr.GetID(),
			PodID:    ctr.GetPodID(),
			Name:     ctr.GetName(),
			State:    containerState(ctr.GetState()),
			QOSClass: string(ctr.GetQOSClass()),
			Applied: &api.Applied{
				CpusetCpus:  ctr.GetCpusetCpus(),
				CpusetMems:  ctr.GetCpusetMems(),
				CPUShares:   ctr.GetCPUShares(),
				CPUQuota:    ctr.GetCPUQuota(),
				CPUPeriod:   ctr.GetCPUPeriod(),
				MemoryLimit: ctr.GetMemoryLimit(),
			},
		}

		resources := ctr.GetResourceRequirements()
		if q, ok := resources.Requests[v1.ResourceCPU]; ok {
			container.CPURequest = q.MilliValue()
		}
		if q, ok := resources.Limits[v1.ResourceCPU]; ok {
			container.CPULimit = q.MilliValue()
		}
		if q, ok := resources.Requests[v1.ResourceMemory]; ok {
			container.MemoryRequest = q.Value()
		}
		if q, ok := resources.Limits[v1.ResourceMemory]; ok {
			container.MemoryLimit = q.Value()
		}

		for name, value := range map[string]string{
			"rdt":     ctr.GetRDTClass(),
			"blockio": ctr.GetBlockIOClass(),
		} {
			if value == "" {
				continue
			}
			if container.Classes == nil {
				container.Classes = map[string]string{}
			}
			container.Classes[name] = value
		}

		for _, h := range ctr.GetTopologyHints() {
			container.Hints = append(container.Hints, &api.Hint{
				Provider: h.Provider,
				CPUs:     h.CPUs,
				NUMAs:    h.NUMAs,
				Sockets:  h.Sockets,
				IRQs:     h.IRQs,
			})
		}
		sort.Slice(container.Hints, func(i, j int) bool {
			return container.Hints[i].Provider < container.Hints[j].Provider
		})

		containers = append(containers, container)
	}

	return pods, containers
}

// containerState turns an NRI container state into a short lower-case name.
func containerState(state nri.ContainerState) string {
	return strings.ToLower(strings.TrimPrefix(state.String(), "CONTAINER_"))
}

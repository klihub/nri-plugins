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
	"encoding/json"
	"reflect"
	"slices"
	"strings"
	"time"

	nri "github.com/containerd/nri/pkg/api"
	v1 "k8s.io/api/core/v1"

	cfgapi "github.com/containers/nri-plugins/pkg/apis/config/v1alpha1"
	"github.com/containers/nri-plugins/pkg/introspect/api"
	logger "github.com/containers/nri-plugins/pkg/log"
	"github.com/containers/nri-plugins/pkg/resmgr/cache"
	"github.com/containers/nri-plugins/pkg/topology"
	"github.com/containers/nri-plugins/pkg/utils/cpuset"

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

		setClass := func(name, value string) {
			if value == "" {
				return
			}
			if container.Classes == nil {
				container.Classes = map[string]string{}
			}
			container.Classes[name] = value
		}
		setClass("rdt", ctr.GetRDTClass())
		setClass("blockio", ctr.GetBlockIOClass())

		for _, h := range ctr.GetTopologyHints() {
			container.Hints = append(container.Hints, &api.Hint{
				Provider: h.Provider,
				CPUs:     h.CPUs,
				NUMAs:    h.NUMAs,
				Sockets:  h.Sockets,
				IRQs:     h.IRQs,
			})
		}
		slices.SortFunc(container.Hints, func(a, b *api.Hint) int {
			return strings.Compare(a.Provider, b.Provider)
		})

		containers = append(containers, container)
	}

	// The cache builds both of its slices by ranging over maps, and hints
	// come from a map too, so nothing here arrives in a stable order.
	// Sorting is what keeps two snapshots of an unchanged node identical,
	// which is what makes diffing them on a change notification useful.
	slices.SortFunc(pods, func(a, b *api.Pod) int {
		return strings.Compare(a.ID, b.ID)
	})
	slices.SortFunc(containers, func(a, b *api.Container) int {
		return strings.Compare(a.ID, b.ID)
	})

	return pods, containers
}

// containerState turns an NRI container state into a short lower-case name.
func containerState(state nri.ContainerState) string {
	return strings.ToLower(strings.TrimPrefix(state.String(), "CONTAINER_"))
}

// derive computes the values of the snapshot's derived section.
//
// A container's request is spread evenly over the CPUs, or memory nodes, it
// is allowed on, and the shares are summed. A container allowed on a single
// CPU and requesting four of them commits that CPU fourfold; the number is
// reported as it is rather than clamped, because oversubscription is
// precisely what a viewer wants to see.
//
// Shares truncate, so a per-CPU or per-node sum can fall a few units short
// of the request it came from. A consumer reconciling the two should expect
// that rather than treat it as a discrepancy.
//
// A set that does not parse, or is empty, contributes nothing. A set naming
// IDs the machine does not have still divides by its full size, and only the
// IDs the machine does have record anything: a container allowed on CPUs
// "0,99" really is spread over two CPUs, so CPU 0 receives half its request
// and the other half is simply not on this machine. An unparseable set means
// something upstream is already broken and is logged, not guessed at.
func derive(hw *api.Hardware, containers []*api.Container) *api.Derived {
	d := &api.Derived{
		CPUCommitment:    map[int]int64{},
		CPUSharers:       map[int]int{},
		MemoryCommitment: map[int]int64{},
	}

	if hw == nil {
		return d
	}

	// Every CPU and node gets an entry, so consumers need no fallback.
	for _, cpu := range hw.CPUs {
		d.CPUCommitment[cpu.ID] = 0
		d.CPUSharers[cpu.ID] = 0
	}
	for _, node := range hw.NUMANodes {
		d.MemoryCommitment[node.ID] = 0
	}

	for _, c := range containers {
		if c.Applied == nil {
			continue
		}

		// parseSet returns nil on failure, so a length check covers
		// both the nil and the empty case.
		if cpus := parseSet(c.Applied.CpusetCpus, "cpuset.cpus", c.ID); len(cpus) > 0 {
			share := c.CPURequest / int64(len(cpus))
			for _, id := range cpus {
				if _, ok := d.CPUCommitment[id]; !ok {
					continue
				}
				d.CPUCommitment[id] += share
				d.CPUSharers[id]++
			}
		}

		if mems := parseSet(c.Applied.CpusetMems, "cpuset.mems", c.ID); len(mems) > 0 {
			share := c.MemoryRequest / int64(len(mems))
			for _, id := range mems {
				if _, ok := d.MemoryCommitment[id]; !ok {
					continue
				}
				d.MemoryCommitment[id] += share
			}
		}
	}

	return d
}

// parseSet parses a Linux list-format set, returning nil if it cannot.
func parseSet(value, what, id string) []int {
	if value == "" {
		return nil
	}

	set, err := cpuset.Parse(value)
	if err != nil {
		// Debug rather than warn: a snapshot is collected per HTTP
		// request, and an unparseable set is a persistent condition,
		// so warning here would let a remote caller drive an unbounded
		// log stream.
		log.Debugf("container %s has unparseable %s %q: %v", id, what, value, err)
		return nil
	}

	return set.List()
}

// Input is everything Collect needs. The caller is responsible for making
// sure the state behind it is not being mutated concurrently; the resource
// manager does that by collecting under its read lock.
type Input struct {
	NodeName string
	Policy   string
	Version  string
	System   system.System
	Cache    Cache
	Config   cfgapi.ResmgrConfig
}

// Collect builds a snapshot. The Generation field is left at zero for the
// server to stamp, since only the server knows it.
func Collect(in *Input) *api.Snapshot {
	snap := &api.Snapshot{
		Version:   api.Version,
		Timestamp: time.Now().UTC(),
		Node: api.Node{
			Name:    in.NodeName,
			Policy:  in.Policy,
			Version: in.Version,
		},
		Config:   collectConfig(in.Config),
		Hardware: collectHardware(in.System),
	}

	snap.Pods, snap.Containers = collectWorkloads(in.Cache)
	snap.Derived = derive(snap.Hardware, snap.Containers)

	return snap
}

// collectConfig describes the effective configuration.
//
// Data is the configuration as received. Apart from its common section its
// keys are policy-specific, so it is carried verbatim rather than
// interpreted: a consumer that is not itself policy-specific should display
// it, not parse it.
func collectConfig(cfg cfgapi.ResmgrConfig) *api.Config {
	if isNil(cfg) {
		return nil
	}

	c := &api.Config{}

	meta := cfg.GetObjectMeta()
	c.Name = meta.GetName()
	c.Namespace = meta.GetNamespace()
	c.Generation = meta.GetGeneration()
	c.ResourceVersion = meta.GetResourceVersion()

	data, err := json.Marshal(cfg)
	if err != nil {
		log.Warnf("failed to marshal effective configuration: %v", err)
	} else {
		c.Data = json.RawMessage(data)
	}

	return c
}

// isNil reports whether a configuration interface is nil or holds a nil
// pointer.
//
// This is not pedantry. The concrete types satisfy ResmgrConfig's embedded
// ObjectMetaAccessor by promoting GetObjectMeta() from an embedded
// metav1.ObjectMeta, so calling it on a typed nil pointer dereferences that
// nil and panics — inside an HTTP handler. A plain cfg == nil does not catch
// a typed nil, so the invariant is enforced here rather than trusted of
// every future caller.
func isNil(cfg cfgapi.ResmgrConfig) bool {
	if cfg == nil {
		return true
	}

	v := reflect.ValueOf(cfg)

	return v.Kind() == reflect.Ptr && v.IsNil()
}

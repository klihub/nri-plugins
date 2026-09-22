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
	"os"
	"path"
	"strings"
	"testing"

	nri "github.com/containerd/nri/pkg/api"
	v1 "k8s.io/api/core/v1"

	"github.com/containers/nri-plugins/pkg/introspect/api"
	"github.com/containers/nri-plugins/pkg/sysfs"
	"github.com/containers/nri-plugins/pkg/topology"
	"github.com/containers/nri-plugins/pkg/utils/cpuset"
)

// discover returns the sysfs fixture with the given name. Run
// ./test-setup.sh first if testdata is missing.
func discover(t *testing.T, sample string) sysfs.System {
	t.Helper()

	cwd, err := os.Getwd()
	if err != nil {
		t.Fatalf("failed to get working directory: %v", err)
	}

	root := path.Join(cwd, "testdata", sample)
	if _, err := os.Stat(root); err != nil {
		t.Skipf("fixture %s missing, run ./test-setup.sh: %v", sample, err)
	}

	// The sysroot is process-global, and later tasks add tests to this
	// package that must not inherit it.
	t.Cleanup(func() { sysfs.SetSysRoot("") })
	sysfs.SetSysRoot(root)

	sys, err := sysfs.DiscoverSystem()
	if err != nil {
		t.Fatalf("failed to discover %s: %v", sample, err)
	}

	return sys
}

func TestCollectHardwareSample1(t *testing.T) {
	sys := discover(t, "sample1")
	hw := collectHardware(sys)

	if len(hw.CPUs) != 16 {
		t.Errorf("expected 16 CPUs, got %d", len(hw.CPUs))
	}
	if len(hw.Packages) != 1 {
		t.Errorf("expected 1 package, got %d", len(hw.Packages))
	}
	if len(hw.NUMANodes) != 1 {
		t.Errorf("expected 1 NUMA node, got %d", len(hw.NUMANodes))
	}
}

func TestCollectHardwareSample2(t *testing.T) {
	sys := discover(t, "sample2")
	hw := collectHardware(sys)

	if len(hw.CPUs) != 112 {
		t.Errorf("expected 112 CPUs, got %d", len(hw.CPUs))
	}
	if len(hw.Packages) != 2 {
		t.Errorf("expected 2 packages, got %d", len(hw.Packages))
	}
	if len(hw.NUMANodes) != 8 {
		t.Errorf("expected 8 NUMA nodes, got %d", len(hw.NUMANodes))
	}

	// Four of the eight nodes are memory-only. They must be present,
	// with a capacity and an empty cpuset.
	memoryOnly := 0
	for _, n := range hw.NUMANodes {
		if n.CPUs != "" {
			continue
		}
		memoryOnly++
		if n.Capacity <= 0 {
			t.Errorf("memory-only node %d has capacity %d", n.ID, n.Capacity)
		}
		// A node with no CPUs belongs to no package or die, and must
		// not claim package 0 just because sysfs leaves the ID unset.
		if n.Package != nil || n.Die != nil {
			t.Errorf("memory-only node %d claims package %v die %v",
				n.ID, n.Package, n.Die)
		}
	}
	if memoryOnly != 4 {
		t.Errorf("expected 4 memory-only NUMA nodes, got %d", memoryOnly)
	}
}

// TestCollectHardwareMapping checks every CPU against sysfs rather than
// against hardcoded fixture internals: the collector's whole job is the
// mapping, so that is what gets asserted.
func TestCollectHardwareMapping(t *testing.T) {
	sys := discover(t, "sample2")
	hw := collectHardware(sys)

	seen := map[int]bool{}
	for _, c := range hw.CPUs {
		if seen[c.ID] {
			t.Fatalf("CPU %d reported twice", c.ID)
		}
		seen[c.ID] = true

		cpu := sys.CPU(c.ID)
		if cpu == nil {
			t.Fatalf("CPU %d is not in sysfs at all", c.ID)
		}
		// Unlike a NUMA node, a CPU always belongs to a package, so
		// these stay plain ints.
		if c.Package != int(cpu.PackageID()) {
			t.Errorf("CPU %d: package %d, expected %d",
				c.ID, c.Package, cpu.PackageID())
		}
		if c.NUMANode != int(cpu.NodeID()) {
			t.Errorf("CPU %d: NUMA node %d, expected %d",
				c.ID, c.NUMANode, cpu.NodeID())
		}
		// Die and Cluster are asserted too: both are usually 0, so a
		// swapped assignment would be invisible without this.
		if c.Die != int(cpu.DieID()) {
			t.Errorf("CPU %d: die %d, expected %d",
				c.ID, c.Die, cpu.DieID())
		}
		if c.Cluster != int(cpu.ClusterID()) {
			t.Errorf("CPU %d: cluster %d, expected %d",
				c.ID, c.Cluster, cpu.ClusterID())
		}
		if c.Core != int(cpu.CoreID()) {
			t.Errorf("CPU %d: core %d, expected %d",
				c.ID, c.Core, cpu.CoreID())
		}
		if c.Threads != cpu.ThreadCPUSet().String() {
			t.Errorf("CPU %d: threads %q, expected %q",
				c.ID, c.Threads, cpu.ThreadCPUSet().String())
		}
		if c.Online != cpu.Online() {
			t.Errorf("CPU %d: online %v, expected %v",
				c.ID, c.Online, cpu.Online())
		}
	}

	if len(seen) != sys.CPUCount() {
		t.Errorf("collected %d CPUs, sysfs has %d", len(seen), sys.CPUCount())
	}

	if hw.IsolatedCPUs != sys.Isolated().String() {
		t.Errorf("isolated %q, expected %q",
			hw.IsolatedCPUs, sys.Isolated().String())
	}
	if hw.OfflineCPUs != sys.Offlined().String() {
		t.Errorf("offlined %q, expected %q",
			hw.OfflineCPUs, sys.Offlined().String())
	}

	// Every package's cpuset must be the union of its dies' cpusets.
	for _, p := range hw.Packages {
		union := cpuset.New()
		for _, d := range p.Dies {
			union = union.Union(cpuset.MustParse(d.CPUs))
		}
		if union.String() != p.CPUs {
			t.Errorf("package %d: dies cover %q, package has %q",
				p.ID, union.String(), p.CPUs)
		}
	}
}

// TestCollectHardwareNodeAssociation cross-checks the two independent
// encodings of the node-to-package relation: Die.NUMANodes, derived from the
// package side, against NUMANode.Package and NUMANode.Die, derived from the
// node side. Agreement is not automatic, and without this the whole
// association could go missing unnoticed.
func TestCollectHardwareNodeAssociation(t *testing.T) {
	sys := discover(t, "sample2")
	hw := collectHardware(sys)

	byID := map[int]*api.NUMANode{}
	for _, n := range hw.NUMANodes {
		byID[n.ID] = n
	}

	listed := map[int]bool{}
	for _, p := range hw.Packages {
		for _, d := range p.Dies {
			for _, id := range d.NUMANodes {
				listed[id] = true

				n, ok := byID[id]
				if !ok {
					t.Errorf("package %d die %d lists unknown NUMA node %d",
						p.ID, d.ID, id)
					continue
				}
				if n.Package == nil || n.Die == nil {
					t.Errorf("NUMA node %d is listed by package %d die %d "+
						"but claims no package or die", id, p.ID, d.ID)
					continue
				}
				if *n.Package != p.ID || *n.Die != d.ID {
					t.Errorf("NUMA node %d claims package %d die %d, "+
						"but is listed by package %d die %d",
						id, *n.Package, *n.Die, p.ID, d.ID)
				}
			}
		}
	}

	if len(listed) == 0 {
		t.Fatal("no package lists any NUMA node, so nothing was checked")
	}

	// Conversely, a node claiming a package must be listed by one.
	for _, n := range hw.NUMANodes {
		if n.Package != nil && !listed[n.ID] {
			t.Errorf("NUMA node %d claims package %d but no die lists it",
				n.ID, *n.Package)
		}
	}
}

// TestCollectHardwareCoreKinds checks the only derived, non-pass-through
// mapping in the collector. The sample1 fixture reports both a P-core and an
// E-core set, so this is not vacuous.
func TestCollectHardwareCoreKinds(t *testing.T) {
	sys := discover(t, "sample1")
	hw := collectHardware(sys)

	if len(hw.CoreKinds) < 2 {
		t.Fatalf("expected at least 2 core kinds in sample1, got %v",
			hw.CoreKinds)
	}

	tagged := map[int]string{}
	for kind, cpus := range hw.CoreKinds {
		for _, id := range cpuset.MustParse(cpus).List() {
			tagged[id] = kind
		}
	}

	for _, c := range hw.CPUs {
		if c.CoreKind != tagged[c.ID] {
			t.Errorf("CPU %d: core kind %q, expected %q",
				c.ID, c.CoreKind, tagged[c.ID])
		}
	}
}

func TestCollectWorkloads(t *testing.T) {
	c := &fakeCache{
		pods: []Pod{
			&fakePod{
				id: "pod0", uid: "uid0", name: "web", namespace: "default",
				qos: v1.PodQOSGuaranteed,
			},
		},
		containers: []Container{
			&fakeContainer{
				id: "ctr0", podID: "pod0", name: "nginx",
				state:        nri.ContainerState_CONTAINER_RUNNING,
				qos:          v1.PodQOSGuaranteed,
				requirements: requirements("4", "4", "2Gi", "2Gi"),
				cpusetCpus:   "0-3",
				cpusetMems:   "0",
				cpuShares:    4096,
				cpuQuota:     400000,
				cpuPeriod:    100000,
				memoryLimit:  2147483648,
				rdtClass:     "guaranteed",
				blockIOClass: "",
				hints: topology.Hints{
					"eth0": topology.Hint{
						Provider: "/sys/class/net/eth0",
						CPUs:     "0-3",
						NUMAs:    "0",
					},
				},
			},
		},
	}

	pods, containers := collectWorkloads(c)

	if len(pods) != 1 {
		t.Fatalf("expected 1 pod, got %d", len(pods))
	}
	if pods[0].ID != "pod0" || pods[0].UID != "uid0" ||
		pods[0].Name != "web" || pods[0].Namespace != "default" ||
		pods[0].QOSClass != "Guaranteed" {
		t.Errorf("unexpected pod: %+v", pods[0])
	}

	if len(containers) != 1 {
		t.Fatalf("expected 1 container, got %d", len(containers))
	}

	got := containers[0]
	if got.ID != "ctr0" || got.PodID != "pod0" || got.Name != "nginx" {
		t.Errorf("unexpected container identity: %+v", got)
	}
	if got.State != "running" {
		t.Errorf("state %q, expected \"running\"", got.State)
	}
	if got.CPURequest != 4000 || got.CPULimit != 4000 {
		t.Errorf("CPU request/limit %d/%d, expected 4000/4000",
			got.CPURequest, got.CPULimit)
	}
	if got.MemoryRequest != 2147483648 || got.MemoryLimit != 2147483648 {
		t.Errorf("memory request/limit %d/%d, expected 2147483648 twice",
			got.MemoryRequest, got.MemoryLimit)
	}
	if got.Applied == nil {
		t.Fatal("applied resources missing")
	}
	if got.Applied.CpusetCpus != "0-3" || got.Applied.CpusetMems != "0" {
		t.Errorf("unexpected applied cpuset: %+v", got.Applied)
	}
	if got.Applied.CPUShares != 4096 || got.Applied.CPUQuota != 400000 ||
		got.Applied.CPUPeriod != 100000 ||
		got.Applied.MemoryLimit != 2147483648 {
		t.Errorf("unexpected applied cgroup parameters: %+v", got.Applied)
	}
	if got.Classes["rdt"] != "guaranteed" {
		t.Errorf("rdt class %q, expected \"guaranteed\"", got.Classes["rdt"])
	}
	if _, ok := got.Classes["blockio"]; ok {
		t.Error("empty blockio class should be omitted")
	}
	if len(got.Hints) != 1 || got.Hints[0].Provider != "/sys/class/net/eth0" {
		t.Errorf("unexpected hints: %+v", got.Hints)
	}
}

func TestCollectWorkloadsNoArgs(t *testing.T) {
	// The removed introspection API exposed container command lines.
	// Make sure no field of that shape came back.
	if _, ok := any(&api.Container{}).(interface{ GetArgs() []string }); ok {
		t.Error("api.Container must not expose command line arguments")
	}
	data, err := json.Marshal(&api.Container{})
	if err != nil {
		t.Fatalf("failed to marshal: %v", err)
	}
	if strings.Contains(string(data), "args") {
		t.Errorf("container JSON mentions args: %s", data)
	}
}

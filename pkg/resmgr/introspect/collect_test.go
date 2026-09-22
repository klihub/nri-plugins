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
	"os"
	"path"
	"reflect"
	"regexp"
	"slices"
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
				state: nri.ContainerState_CONTAINER_RUNNING,
				qos:   v1.PodQOSBurstable,
				// Every one of the six numbers below is distinct, on
				// purpose. A self-consistent Guaranteed container makes
				// requests, limits and what was applied all equal, and
				// then a Requests/Limits swap — or reading the applied
				// memory limit from the requirements — passes the test.
				// Their disagreement is the design's whole point.
				requirements: requirements("2", "4", "1Gi", "2Gi"),
				cpusetCpus:   "0-1",
				cpusetMems:   "0",
				cpuShares:    2048,
				cpuQuota:     400000,
				cpuPeriod:    100000,
				// 1.5Gi, distinct from both the memory request (1Gi)
				// and the memory limit (2Gi), so that sourcing the
				// applied limit from either one fails a test.
				memoryLimit:  1610612736,
				rdtClass:     "burstable",
				blockIOClass: "",
				// Out of lexical order, so the collector's sort is
				// actually exercised.
				hints: topology.Hints{
					"eth1": topology.Hint{
						Provider: "/sys/class/net/eth1",
						CPUs:     "2-3",
						NUMAs:    "1",
						Sockets:  "1",
						IRQs:     []int{129},
					},
					"eth0": topology.Hint{
						Provider: "/sys/class/net/eth0",
						CPUs:     "0-1",
						NUMAs:    "0",
						Sockets:  "0",
						IRQs:     []int{128},
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
	if got.QOSClass != "Burstable" {
		t.Errorf("QoS class %q, expected \"Burstable\"", got.QOSClass)
	}

	// Requested: from the Kubernetes resource requirements.
	if got.CPURequest != 2000 {
		t.Errorf("CPU request %d, expected 2000", got.CPURequest)
	}
	if got.CPULimit != 4000 {
		t.Errorf("CPU limit %d, expected 4000", got.CPULimit)
	}
	if got.MemoryRequest != 1073741824 {
		t.Errorf("memory request %d, expected 1073741824", got.MemoryRequest)
	}
	if got.MemoryLimit != 2147483648 {
		t.Errorf("memory limit %d, expected 2147483648", got.MemoryLimit)
	}

	// Applied: from the container itself. Note the memory limit differs
	// from the requested one above.
	if got.Applied == nil {
		t.Fatal("applied resources missing")
	}
	if got.Applied.CpusetCpus != "0-1" || got.Applied.CpusetMems != "0" {
		t.Errorf("unexpected applied cpuset: %+v", got.Applied)
	}
	if got.Applied.CPUShares != 2048 || got.Applied.CPUQuota != 400000 ||
		got.Applied.CPUPeriod != 100000 {
		t.Errorf("unexpected applied cgroup parameters: %+v", got.Applied)
	}
	if got.Applied.MemoryLimit != 1610612736 {
		t.Errorf("applied memory limit %d, expected 1610612736",
			got.Applied.MemoryLimit)
	}

	if got.Classes["rdt"] != "burstable" {
		t.Errorf("rdt class %q, expected \"burstable\"", got.Classes["rdt"])
	}
	if _, ok := got.Classes["blockio"]; ok {
		t.Error("empty blockio class should be omitted")
	}

	// Hints come from a map, so the collector sorts them by provider.
	if len(got.Hints) != 2 {
		t.Fatalf("expected 2 hints, got %d: %+v", len(got.Hints), got.Hints)
	}
	if got.Hints[0].Provider != "/sys/class/net/eth0" ||
		got.Hints[1].Provider != "/sys/class/net/eth1" {
		t.Errorf("hints are not sorted by provider: %q then %q",
			got.Hints[0].Provider, got.Hints[1].Provider)
	}
	if got.Hints[0].CPUs != "0-1" || got.Hints[0].NUMAs != "0" ||
		got.Hints[0].Sockets != "0" ||
		!reflect.DeepEqual(got.Hints[0].IRQs, []int{128}) {
		t.Errorf("unexpected first hint: %+v", got.Hints[0])
	}
}

// TestContainerHasNoArguments enforces the prohibition on exposing
// container command lines: the removed introspection API published them,
// and command lines leak credentials.
//
// This reflects over the struct rather than marshalling one. Every optional
// field is omitempty, so an added Args field would be nil in a zero value
// and absent from the JSON — a marshal-and-grep test passes whether or not
// the field exists, which makes it worthless for the one prohibition the
// design calls security-critical.
func TestContainerHasNoArguments(t *testing.T) {
	forbidden := regexp.MustCompile(`(?i)arg|command|cmd|entrypoint`)

	typ := reflect.TypeOf(api.Container{})
	for i := 0; i < typ.NumField(); i++ {
		field := typ.Field(i)

		if forbidden.MatchString(field.Name) {
			t.Errorf("api.Container.%s looks like a command line", field.Name)
		}
		if tag := field.Tag.Get("json"); forbidden.MatchString(tag) {
			t.Errorf("api.Container.%s has JSON tag %q, which looks like a "+
				"command line", field.Name, tag)
		}
	}
}

// TestCollectWorkloadsIsOrdered pins the ordering of the two lists. The
// cache builds its own slices by ranging over maps, so without sorting two
// snapshots of an unchanged node differ, which makes diffing them — the
// thing the UI does on every change notification — useless.
func TestCollectWorkloadsIsOrdered(t *testing.T) {
	c := &fakeCache{
		pods: []Pod{
			&fakePod{id: "pod2", uid: "uid2", name: "c", namespace: "default"},
			&fakePod{id: "pod0", uid: "uid0", name: "a", namespace: "default"},
			&fakePod{id: "pod1", uid: "uid1", name: "b", namespace: "default"},
		},
		containers: []Container{
			&fakeContainer{id: "ctr2", podID: "pod2", name: "c"},
			&fakeContainer{id: "ctr0", podID: "pod0", name: "a"},
			&fakeContainer{id: "ctr1", podID: "pod1", name: "b"},
		},
	}

	pods, containers := collectWorkloads(c)

	// Guard against passing vacuously: IsSorted is true of an empty
	// slice, so a collector that returned nothing would look ordered.
	if len(pods) != 3 || len(containers) != 3 {
		t.Fatalf("expected 3 pods and 3 containers, got %d and %d",
			len(pods), len(containers))
	}

	podIDs := make([]string, 0, len(pods))
	for _, p := range pods {
		podIDs = append(podIDs, p.ID)
	}
	if !slices.IsSorted(podIDs) {
		t.Errorf("pods are not sorted by ID: %v", podIDs)
	}

	ctrIDs := make([]string, 0, len(containers))
	for _, ctr := range containers {
		ctrIDs = append(ctrIDs, ctr.ID)
	}
	if !slices.IsSorted(ctrIDs) {
		t.Errorf("containers are not sorted by ID: %v", ctrIDs)
	}
}

func TestDerive(t *testing.T) {
	hw := &api.Hardware{
		CPUs: []*api.CPU{
			{ID: 0}, {ID: 1}, {ID: 2}, {ID: 3},
		},
		NUMANodes: []*api.NUMANode{
			{ID: 0}, {ID: 1},
		},
	}

	for _, tc := range []struct {
		name          string
		containers    []*api.Container
		cpuCommitment map[int]int64
		cpuSharers    map[int]int
		memCommitment map[int]int64
	}{
		{
			name:          "no containers leaves every entry at zero",
			containers:    nil,
			cpuCommitment: map[int]int64{0: 0, 1: 0, 2: 0, 3: 0},
			cpuSharers:    map[int]int{0: 0, 1: 0, 2: 0, 3: 0},
			memCommitment: map[int]int64{0: 0, 1: 0},
		},
		{
			name: "exclusive container fully commits its CPUs",
			containers: []*api.Container{
				{
					CPURequest:    2000,
					MemoryRequest: 1000,
					Applied:       &api.Applied{CpusetCpus: "0-1", CpusetMems: "0"},
				},
			},
			cpuCommitment: map[int]int64{0: 1000, 1: 1000, 2: 0, 3: 0},
			cpuSharers:    map[int]int{0: 1, 1: 1, 2: 0, 3: 0},
			memCommitment: map[int]int64{0: 1000, 1: 0},
		},
		{
			name: "shared request spreads and sharers accumulate",
			containers: []*api.Container{
				{
					CPURequest: 500,
					Applied:    &api.Applied{CpusetCpus: "2-3"},
				},
				{
					CPURequest: 500,
					Applied:    &api.Applied{CpusetCpus: "2-3"},
				},
			},
			cpuCommitment: map[int]int64{0: 0, 1: 0, 2: 500, 3: 500},
			cpuSharers:    map[int]int{0: 0, 1: 0, 2: 2, 3: 2},
			memCommitment: map[int]int64{0: 0, 1: 0},
		},
		{
			name: "oversubscription is reported, not clamped",
			containers: []*api.Container{
				{
					CPURequest: 4000,
					Applied:    &api.Applied{CpusetCpus: "0"},
				},
			},
			cpuCommitment: map[int]int64{0: 4000, 1: 0, 2: 0, 3: 0},
			cpuSharers:    map[int]int{0: 1, 1: 0, 2: 0, 3: 0},
			memCommitment: map[int]int64{0: 0, 1: 0},
		},
		{
			name: "empty cpuset contributes nothing",
			containers: []*api.Container{
				{
					CPURequest: 4000,
					Applied:    &api.Applied{CpusetCpus: ""},
				},
			},
			cpuCommitment: map[int]int64{0: 0, 1: 0, 2: 0, 3: 0},
			cpuSharers:    map[int]int{0: 0, 1: 0, 2: 0, 3: 0},
			memCommitment: map[int]int64{0: 0, 1: 0},
		},
		{
			name: "missing applied section contributes nothing",
			containers: []*api.Container{
				{CPURequest: 4000},
			},
			cpuCommitment: map[int]int64{0: 0, 1: 0, 2: 0, 3: 0},
			cpuSharers:    map[int]int{0: 0, 1: 0, 2: 0, 3: 0},
			memCommitment: map[int]int64{0: 0, 1: 0},
		},
		{
			name: "cpuset outside the machine is ignored",
			containers: []*api.Container{
				{
					CPURequest: 1000,
					Applied:    &api.Applied{CpusetCpus: "99"},
				},
			},
			cpuCommitment: map[int]int64{0: 0, 1: 0, 2: 0, 3: 0},
			cpuSharers:    map[int]int{0: 0, 1: 0, 2: 0, 3: 0},
			memCommitment: map[int]int64{0: 0, 1: 0},
		},
		{
			name: "unparseable cpuset is ignored",
			containers: []*api.Container{
				{
					CPURequest: 1000,
					Applied:    &api.Applied{CpusetCpus: "not-a-cpuset"},
				},
			},
			cpuCommitment: map[int]int64{0: 0, 1: 0, 2: 0, 3: 0},
			cpuSharers:    map[int]int{0: 0, 1: 0, 2: 0, 3: 0},
			memCommitment: map[int]int64{0: 0, 1: 0},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			d := derive(hw, tc.containers)

			if !reflect.DeepEqual(d.CPUCommitment, tc.cpuCommitment) {
				t.Errorf("CPU commitment\n  got:      %v\n  expected: %v",
					d.CPUCommitment, tc.cpuCommitment)
			}
			if !reflect.DeepEqual(d.CPUSharers, tc.cpuSharers) {
				t.Errorf("CPU sharers\n  got:      %v\n  expected: %v",
					d.CPUSharers, tc.cpuSharers)
			}
			if !reflect.DeepEqual(d.MemoryCommitment, tc.memCommitment) {
				t.Errorf("memory commitment\n  got:      %v\n  expected: %v",
					d.MemoryCommitment, tc.memCommitment)
			}
		})
	}
}

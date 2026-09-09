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

package hardware

import (
	"os"
	"path/filepath"
	"testing"
	"testing/fstest"
)

// recordedTrees are the pkg/sysfs test trees, which test-setup.sh unpacks
// into pkg/sysfs/testdata.
var recordedTrees = []string{"sample1", "sample2"}

// openRecorded returns a Machine discovered from one recorded tree, skipping the
// test if the trees have not been unpacked.
func openRecorded(t *testing.T, name string) *Machine {
	t.Helper()

	root, err := filepath.Abs(filepath.Join("..", "..", "sysfs", "testdata", name))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(root, "sys")); err != nil {
		t.Skipf("recorded tree %s is not unpacked: run pkg/sysfs/test-setup.sh", name)
	}

	m, err := Discover(WithRoot(root))
	if err != nil {
		t.Fatalf("Discover(%s): %v", name, err)
	}

	return m
}

// TestDiscoverRecorded checks the machine invariants on the recorded trees.
// It asserts no particular numbers.
func TestDiscoverRecorded(t *testing.T) {
	for _, name := range recordedTrees {
		t.Run(name, func(t *testing.T) {
			m := openRecorded(t, name)
			checkMachineInvariants(t, m)

			t.Logf("%s: %d CPUs (%s), %d nodes, levels %v",
				name, m.PresentCPUs().Size(), m.PresentCPUs(),
				len(m.MemoryNodes()), m.Levels())
			for _, level := range m.Levels() {
				zones := m.Zones(level)
				same := ""
				for _, other := range m.Levels() {
					if other != level && m.SameZones(level, other) {
						same += " =" + other.String()
					}
				}
				t.Logf("  %-8s %3d zones%s", level, len(zones), same)
			}
		})
	}
}

// checkMachineInvariants asserts what must be true of any discovered machine,
// whatever the hardware.
func checkMachineInvariants(t *testing.T, m *Machine) {
	t.Helper()

	if m.PresentCPUs().IsEmpty() {
		t.Fatal("no CPUs present")
	}

	// the machine-wide sets must nest
	if !m.OnlineCPUs().IsSubsetOf(m.PresentCPUs()) {
		t.Errorf("online %s is not within present %s", m.OnlineCPUs(), m.PresentCPUs())
	}
	if !m.PresentCPUs().IsSubsetOf(m.PossibleCPUs()) {
		t.Errorf("present %s is not within possible %s",
			m.PresentCPUs(), m.PossibleCPUs())
	}
	if got := m.OnlineCPUs().Union(m.OfflineCPUs()); !got.Equals(m.PresentCPUs()) {
		t.Errorf("online+offline %s != present %s", got, m.PresentCPUs())
	}
	if m.OnlineCPUs().Intersects(m.OfflineCPUs()) {
		t.Error("a CPU is both online and offline")
	}

	// every set handed out must be sealed
	for _, tc := range []struct {
		what string
		cpus interface{ Set(...int) }
	}{
		{"PossibleCPUs", m.PossibleCPUs()},
		{"PresentCPUs", m.PresentCPUs()},
		{"OnlineCPUs", m.OnlineCPUs()},
		{"IsolatedCPUs", m.IsolatedCPUs()},
		{"OfflineCPUs", m.OfflineCPUs()},
	} {
		assertSealedNamed(t, tc.what, tc.cpus)
	}

	// an absent CPU answers rather than panicking
	absent := m.CPU(1 << 20)
	if absent == nil {
		t.Fatal("CPU() returned nil")
	}
	if absent.Valid() {
		t.Error("a CPU which is not there reports Valid()")
	}
	_, _, _ = absent.PackageID(), absent.Threads(), absent.String()
	if absent.Zone(LevelPackage).Valid() {
		t.Error("an absent CPU has a valid package zone")
	}
	if absent.MemoryNode().Valid() {
		t.Error("an absent CPU has a valid memory node")
	}

	checkZones(t, m)
	checkContainment(t, m)
	checkCPUs(t, m)
	checkMemoryNodes(t, m)
}

// checkZones asserts that the zones of every level are valid and sealed, hold
// only CPUs the machine has, and do not overlap within a level.
func checkZones(t *testing.T, m *Machine) {
	t.Helper()

	for _, level := range m.Levels() {
		zones := m.Zones(level)
		if len(zones) == 0 {
			t.Errorf("level %s is listed but has no zones", level)
			continue
		}

		seen := emptyCPUs.Union()
		seenID := map[ID]bool{}
		for _, z := range zones {
			if !z.Valid() {
				t.Errorf("level %s has an invalid zone", level)
				continue
			}
			if z.Level() != level {
				t.Errorf("zone %s is listed at %s but reports %s",
					z.Name(), level, z.Level())
			}
			assertSealedNamed(t, "zone "+z.Name(), z.CPUs())

			if !z.CPUs().IsSubsetOf(m.PresentCPUs()) {
				t.Errorf("zone %s holds CPUs the machine does not have: %s",
					z.Name(), z.CPUs().Difference(m.PresentCPUs()))
			}
			if z.CPUs().Intersects(seen) {
				t.Errorf("zone %s overlaps another zone at level %s: %s",
					z.Name(), level, z.CPUs().Intersection(seen))
			}
			seen = seen.Union(z.CPUs())

			// ids are unique per machine only for packages and NUMA nodes; dies,
			// clusters and cores repeat them per package
			if z.Level() == LevelPackage || z.Level() == LevelNUMANode {
				if seenID[z.ID()] {
					t.Errorf("level %s has two zones with id %d", level, z.ID())
				}
				seenID[z.ID()] = true
			}
		}
	}

	// a level the machine does not have answers empty rather than panicking
	for _, level := range allLevels {
		if len(m.Zones(level)) > 0 {
			continue
		}
		for _, other := range allLevels {
			if m.SameZones(level, other) {
				t.Errorf("empty level %s reports the same zones as %s", level, other)
			}
		}
	}

	// SameZones must agree with the zones themselves
	for _, a := range m.Levels() {
		if !m.SameZones(a, a) {
			t.Errorf("SameZones(%s, %s) is false", a, a)
		}
		for _, b := range m.Levels() {
			if m.SameZones(a, b) != m.SameZones(b, a) {
				t.Errorf("SameZones is not symmetric for %s and %s", a, b)
			}
		}
	}
}

// checkContainment exercises the containment queries against the zones.
func checkContainment(t *testing.T, m *Machine) {
	t.Helper()

	for _, level := range m.Levels() {
		for _, z := range m.Zones(level) {
			// a zone is within itself and overlaps itself
			within := ZonesWithin(m, level, z.CPUs())
			if !containsZone(within, z) {
				t.Errorf("ZonesWithin(%s, %s) does not include %s",
					level, z.CPUs(), z.Name())
			}
			over := ZonesOverlapping(m, level, z.CPUs())
			if !containsZone(over, z) {
				t.Errorf("ZonesOverlapping(%s, %s) does not include %s",
					level, z.CPUs(), z.Name())
			}
			// overlapping is the weaker predicate, so it can only be larger
			if len(over) < len(within) {
				t.Errorf("%s: overlapping (%d) is smaller than within (%d)",
					z.Name(), len(over), len(within))
			}
			if got := ZoneOf(m, level, z.CPUs()); got != z && got.CPUs().Size() > z.CPUs().Size() {
				t.Errorf("ZoneOf(%s, %s) = %s, want %s or smaller",
					level, z.CPUs(), got.Name(), z.Name())
			}
		}

		// nothing is within an empty set, and nothing overlaps it
		if got := ZonesWithin(m, level, emptyCPUs); len(got) != 0 {
			t.Errorf("ZonesWithin(%s, empty) returned %d zones", level, len(got))
		}
		if got := ZonesOverlapping(m, level, emptyCPUs); len(got) != 0 {
			t.Errorf("ZonesOverlapping(%s, empty) returned %d zones", level, len(got))
		}
		// everything is within the whole machine
		if got, want := len(ZonesWithin(m, level, m.PresentCPUs())), len(m.Zones(level)); got != want {
			t.Errorf("ZonesWithin(%s, all) returned %d zones, want %d", level, got, want)
		}
	}
}

func containsZone(zones []*Zone, want *Zone) bool {
	for _, z := range zones {
		if z == want {
			return true
		}
	}
	return false
}

// checkCPUs asserts what must be true of every CPU.
func checkCPUs(t *testing.T, m *Machine) {
	t.Helper()

	kinds := emptyCPUs.Union()
	for _, kind := range m.CoreKinds() {
		cpus := m.CoreKindCPUs(kind)
		if cpus.IsEmpty() {
			t.Errorf("core kind %s is listed but has no CPUs", kind)
		}
		if kinds.Intersects(cpus) {
			t.Errorf("core kind %s overlaps another", kind)
		}
		kinds = kinds.Union(cpus)
	}
	if missing := m.OnlineCPUs().Difference(kinds); !missing.IsEmpty() {
		t.Errorf("online CPUs %s are of no core kind", missing)
	}

	for _, id := range m.CPUIDs() {
		c := m.CPU(id)
		if !c.Valid() {
			t.Errorf("cpu%d is listed but not valid", id)
			continue
		}
		if c.ID() != id {
			t.Errorf("cpu%d reports id %d", id, c.ID())
		}
		if c.Online() != m.OnlineCPUs().Contains(id) {
			t.Errorf("cpu%d Online()=%v disagrees with the online set", id, c.Online())
		}
		if c.Isolated() != m.IsolatedCPUs().Contains(id) {
			t.Errorf("cpu%d Isolated()=%v disagrees with the isolated set", id,
				c.Isolated())
		}

		if !c.Online() {
			// an offline CPU has no topology
			continue
		}

		if !c.Threads().Contains(id) {
			t.Errorf("cpu%d is not among its own threads %s", id, c.Threads())
		}
		assertSealedNamed(t, c.String()+" threads", c.Threads())

		// its zones must contain it and agree with its ids
		for _, level := range m.Levels() {
			z := c.Zone(level)
			if !z.Valid() {
				t.Errorf("cpu%d has no zone at level %s", id, level)
				continue
			}
			if !z.CPUs().Contains(id) {
				t.Errorf("cpu%d is not in its own %s zone %s", id, level, z.Name())
			}
		}
		if pkg := c.Zone(LevelPackage); pkg.Valid() && pkg.ID() != c.PackageID() {
			t.Errorf("cpu%d package zone is #%d but PackageID() is %d",
				id, pkg.ID(), c.PackageID())
		}

		if node := c.MemoryNode(); node.Valid() && node.ID() != c.NodeID() {
			t.Errorf("cpu%d node is #%d but NodeID() is %d",
				id, node.ID(), c.NodeID())
		}

		// caches come out lowest level first, and each holds this CPU
		last := 0
		for _, cache := range c.Caches() {
			if cache.Level() < last {
				t.Errorf("cpu%d caches are out of order: %d after %d",
					id, cache.Level(), last)
			}
			last = cache.Level()
			if !cache.CPUs().Contains(id) {
				t.Errorf("cpu%d is not among the CPUs of its own cache %s",
					id, cache.Key())
			}
		}
	}
}

// checkMemoryNodes asserts what must be true of every NUMA node.
func checkMemoryNodes(t *testing.T, m *Machine) {
	t.Helper()

	nodes := m.MemoryNodes()
	if len(nodes) == 0 {
		t.Fatal("no NUMA nodes")
	}

	for _, n := range nodes {
		if !n.Valid() {
			t.Errorf("node#%d is listed but not valid", n.ID())
			continue
		}
		assertSealedNamed(t, n.String()+" CPUs", n.CPUs())

		// the distance vector covers every node and is shortest to itself
		for _, other := range nodes {
			d := n.Distance(other.ID())
			if d < 0 {
				t.Errorf("node#%d has no distance to node#%d", n.ID(), other.ID())
				continue
			}
			if other.ID() == n.ID() {
				continue
			}
			if self := n.Distance(n.ID()); d < self {
				t.Errorf("node#%d is closer to node#%d (%d) than to itself (%d)",
					n.ID(), other.ID(), d, self)
			}
			// symmetrized during discovery
			if back := other.Distance(n.ID()); d != back {
				t.Errorf("distance node#%d->node#%d is %d but back is %d",
					n.ID(), other.ID(), d, back)
			}
		}

		if n.Distance(1<<20) != unknownID {
			t.Errorf("node#%d gave a distance to a node which does not exist", n.ID())
		}

		// its CPUs agree with the CPUs' own view
		n.CPUs().ForEachCpu(func(id int) bool {
			if got := m.CPU(id).NodeID(); got != n.ID() {
				t.Errorf("cpu%d is in node#%d's CPU list but reports node#%d",
					id, n.ID(), got)
			}
			return true
		})

		if n.HasMemory() != (n.Capacity() > 0) {
			t.Errorf("node#%d HasMemory()=%v with capacity %d",
				n.ID(), n.HasMemory(), n.Capacity())
		}
	}

	// an absent node answers rather than panicking
	absent := m.MemoryNode(1 << 20)
	if absent.Valid() {
		t.Error("a node which is not there reports Valid()")
	}
	_, _, _ = absent.Capacity(), absent.CPUs(), absent.String()
}

// TestDiscoverSynthetic runs discovery over hand-built topologies, for the
// shapes the recorded trees lack.
func TestDiscoverSynthetic(t *testing.T) {
	t.Run("no-numa", func(t *testing.T) {
		// Without NUMA there are no node directories. One node must hold every
		// CPU.
		m, err := Discover(WithFS(syntheticFS(2, false)))
		if err != nil {
			t.Fatalf("Discover: %v", err)
		}
		checkMachineInvariants(t, m)

		if got := len(m.MemoryNodes()); got != 1 {
			t.Fatalf("got %d nodes, want 1", got)
		}
		node := m.MemoryNode(0)
		if !node.CPUs().Equals(m.OnlineCPUs()) {
			t.Errorf("the single node holds %s, want %s", node.CPUs(), m.OnlineCPUs())
		}

		// ...and all of the memory, read from the machine's own meminfo. A node
		// left at zero would make an allocator refuse everything.
		if got, want := node.Capacity(), int64(1048576*1024); got != want {
			t.Errorf("the single node has capacity %d, want %d", got, want)
		}
		if !node.HasMemory() {
			t.Error("the single node reports no memory")
		}
		if !node.HasNormalMemory() {
			t.Error("the single node reports no normal memory")
		}

		// and Usage, which reads the same file live, agrees with it
		usage, err := node.Usage()
		if err != nil {
			t.Fatalf("Usage: %v", err)
		}
		if usage.Total != node.Capacity() {
			t.Errorf("Usage().Total = %d but Capacity() = %d",
				usage.Total, node.Capacity())
		}
	})

	t.Run("no-numa-no-meminfo", func(t *testing.T) {
		// Without node directories meminfo is the only capacity source, so
		// failing to read it must fail discovery.
		fsys := syntheticFS(2, false)
		delete(fsys, "proc/meminfo")

		if _, err := Discover(WithFS(fsys)); err == nil {
			t.Fatal("discovering a NUMA-less machine with no meminfo succeeded")
		}
	})

	t.Run("with-numa", func(t *testing.T) {
		m, err := Discover(WithFS(syntheticFS(4, true)))
		if err != nil {
			t.Fatalf("Discover: %v", err)
		}
		checkMachineInvariants(t, m)
	})

	t.Run("no-cpus", func(t *testing.T) {
		// Nothing to describe: this must fail rather than return an empty
		// machine.
		_, err := Discover(WithFS(fstest.MapFS{"proc/meminfo": file("MemTotal: 1 kB\n")}))
		if err == nil {
			t.Fatal("discovering a machine with no CPUs succeeded")
		}
	})

	t.Run("nil-fs", func(t *testing.T) {
		if _, err := Discover(WithFS(nil)); err == nil {
			t.Fatal("WithFS(nil) was accepted")
		}
	})
}

// syntheticFS builds a minimal single-package machine with n CPUs, no
// hyperthreads and one L2 cache each, optionally with a NUMA node.
func syntheticFS(n int, numa bool) fstest.MapFS {
	fsys := fstest.MapFS{
		"proc/meminfo": file("MemTotal:       1048576 kB\nMemFree: 524288 kB\n"),
	}

	last := itoa(n - 1)
	fsys["sys/devices/system/cpu/possible"] = file("0-" + last + "\n")
	fsys["sys/devices/system/cpu/present"] = file("0-" + last + "\n")
	fsys["sys/devices/system/cpu/online"] = file("0-" + last + "\n")
	fsys["sys/devices/system/cpu/isolated"] = file("\n")

	for i := 0; i < n; i++ {
		dir := "sys/devices/system/cpu/cpu" + itoa(i)
		fsys[dir+"/topology/physical_package_id"] = file("0\n")
		fsys[dir+"/topology/core_id"] = file(itoa(i) + "\n")
		fsys[dir+"/topology/core_cpus_list"] = file(itoa(i) + "\n")
		fsys[dir+"/cache/index0/level"] = file("2\n")
		fsys[dir+"/cache/index0/type"] = file("Unified\n")
		fsys[dir+"/cache/index0/id"] = file(itoa(i) + "\n")
		fsys[dir+"/cache/index0/shared_cpu_list"] = file(itoa(i) + "\n")
		fsys[dir+"/cache/index0/size"] = file("1024K\n")
		if numa {
			fsys[dir+"/node0/x"] = file("")
		}
	}

	if numa {
		fsys["sys/devices/system/node/has_normal_memory"] = file("0\n")
		fsys["sys/devices/system/node/node0/cpulist"] = file("0-" + last + "\n")
		fsys["sys/devices/system/node/node0/distance"] = file("10\n")
		fsys["sys/devices/system/node/node0/meminfo"] =
			file("Node 0 MemTotal:       1048576 kB\nNode 0 MemFree: 524288 kB\n")
	}

	return fsys
}

//
// helpers
//

func itoa(i int) string {
	if i == 0 {
		return "0"
	}
	digits := ""
	for i > 0 {
		digits = string(rune('0'+i%10)) + digits
		i /= 10
	}
	return digits
}

// assertSealedNamed checks that a set cannot be modified, saying which set it is.
func assertSealedNamed(t *testing.T, what string, cpus interface{ Set(...int) }) {
	t.Helper()
	defer func() {
		if recover() == nil {
			t.Errorf("%s is not sealed", what)
		}
	}()
	cpus.Set(0)
}

// TestUnreportedDieAndCluster pins that a missing die_id or cluster_id reads as
// -1, while one die per package and one cluster per die still form zones.
func TestUnreportedDieAndCluster(t *testing.T) {
	m, err := Discover(WithFS(syntheticFS(2, false)))
	if err != nil {
		t.Fatalf("Discover: %v", err)
	}
	checkMachineInvariants(t, m)

	c := m.CPU(0)
	if c.DieID() != -1 || c.ClusterID() != -1 {
		t.Errorf("unreported die and cluster read %d and %d, want -1",
			c.DieID(), c.ClusterID())
	}

	dies := m.Zones(LevelDie)
	if len(dies) != 1 || dies[0].DieID() != (DieID{Package: 0, Die: -1}) {
		t.Fatalf("die zones %v, want one with id package#0/die#-1", dies)
	}
	clusters := m.Zones(LevelCluster)
	want := ClusterID{Package: 0, Die: -1, Cluster: -1}
	if len(clusters) != 1 || clusters[0].ClusterID() != want {
		t.Fatalf("cluster zones %v, want one with id %s", clusters, want)
	}
	if !m.SameZones(LevelDie, LevelPackage) || !m.SameZones(LevelCluster, LevelDie) {
		t.Error("an unreported die and cluster should cut like the package")
	}

	x := m.TopologyIndex()
	if got := x.DieIDs(0); len(got) != 1 || got[0] != (DieID{Package: 0, Die: -1}) {
		t.Errorf("DieIDs(0) = %v, want [package#0/die#-1]", got)
	}
	if got := x.DieCPUs(DieID{Package: 0, Die: -1}); !got.Equals(m.OnlineCPUs()) {
		t.Errorf("DieCPUs = %s, want %s", got, m.OnlineCPUs())
	}
	if got := LogicalClusters(m, 0, -1, MergeSingleCoreClusters); len(got) != 1 {
		t.Errorf("LogicalClusters = %v, want one cluster", got)
	}
}

// TestClustersWithoutDies pins the arm64 shape: cluster_id is reported, die_id
// is not. Clusters of different packages must stay apart.
func TestClustersWithoutDies(t *testing.T) {
	// 2 packages x 2 clusters x 2 cores.
	fsys := syntheticFS(8, false)
	for i := 0; i < 8; i++ {
		dir := "sys/devices/system/cpu/cpu" + itoa(i) + "/topology/"
		fsys[dir+"physical_package_id"] = file(itoa(i/4) + "\n")
		fsys[dir+"cluster_id"] = file(itoa(i/2%2) + "\n")
	}

	m, err := Discover(WithFS(fsys))
	if err != nil {
		t.Fatalf("Discover: %v", err)
	}
	checkMachineInvariants(t, m)

	clusters := m.Zones(LevelCluster)
	if len(clusters) != 4 {
		t.Fatalf("got %d cluster zones, want 4: %v", len(clusters), clusters)
	}
	ids := map[ClusterID]bool{}
	names := map[string]bool{}
	for _, z := range clusters {
		ids[z.ClusterID()] = true
		names[z.Name()] = true
	}
	for pkg := 0; pkg < 2; pkg++ {
		for cl := 0; cl < 2; cl++ {
			if id := (ClusterID{Package: pkg, Die: -1, Cluster: cl}); !ids[id] {
				t.Errorf("no cluster zone %s among %v", id, ids)
			}
		}
	}
	if len(names) != 4 {
		t.Errorf("cluster zone names not unique: %v", names)
	}

	if got := LogicalClusters(m, 1, -1, OmitSingleCoreClusters); len(got) != 2 {
		t.Errorf("LogicalClusters(package 1) = %v, want 2 clusters", got)
	}
	if got := m.TopologyIndex().ClusterIDs(DieID{Package: 1, Die: -1}); len(got) != 2 {
		t.Errorf("ClusterIDs(package 1) = %v, want 2", got)
	}
}

// TestDieAndClusterIDErrors pins that only a missing die_id or cluster_id is
// tolerated, and that a kernel-reported -1 means unknown.
func TestDieAndClusterIDErrors(t *testing.T) {
	for _, name := range []string{"die_id", "cluster_id"} {
		t.Run(name+" unparsable", func(t *testing.T) {
			fsys := syntheticFS(2, false)
			fsys["sys/devices/system/cpu/cpu0/topology/"+name] = file("bogus\n")
			if _, err := Discover(WithFS(fsys)); err == nil {
				t.Fatalf("an unparsable %s was accepted", name)
			}
		})
		t.Run(name+" -1", func(t *testing.T) {
			fsys := syntheticFS(2, false)
			for i := 0; i < 2; i++ {
				fsys["sys/devices/system/cpu/cpu"+itoa(i)+"/topology/"+name] =
					file("-1\n")
			}
			m, err := Discover(WithFS(fsys))
			if err != nil {
				t.Fatalf("Discover: %v", err)
			}
			if m.CPU(0).DieID() != -1 || m.CPU(0).ClusterID() != -1 {
				t.Errorf("a reported -1 reads as die %d, cluster %d",
					m.CPU(0).DieID(), m.CPU(0).ClusterID())
			}
		})
	}
}

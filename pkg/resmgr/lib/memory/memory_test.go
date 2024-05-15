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

package libmem_test

import (
	"sort"
	"strconv"

	"github.com/containers/nri-plugins/pkg/sysfs"
	"github.com/containers/nri-plugins/pkg/utils/cpuset"

	. "github.com/containers/nri-plugins/pkg/resmgr/lib/memory"

	"testing"

	"github.com/stretchr/testify/require"
)

func TestGetClosestNodes(t *testing.T) {
	type testCase struct {
		name   string
		node   ID
		kinds  []Kind
		expect []ID
		fail   bool
	}

	var (
		sysRoot = "./testdata/sample2"
		sys     sysfs.System
		err     error
		a       *Allocator
	)

	sys, err = sysfs.DiscoverSystemAt(sysRoot + "/sys")
	require.Nil(t, err)
	require.NotNil(t, sys)

	a, err = NewAllocator(WithSystemNodes(sys))
	require.Nil(t, err)
	require.NotNil(t, a)

	for _, tc := range []*testCase{
		{
			name:   "closest DRAM nodes to node #0",
			node:   0,
			kinds:  []Kind{KindDRAM},
			expect: []ID{2},
		},
		{
			name:   "closest PMEM nodes to node #0",
			node:   0,
			kinds:  []Kind{KindPMEM},
			expect: []ID{4},
		},
		{
			name:   "closest DRAM+PMEM nodes to node #0",
			node:   0,
			kinds:  []Kind{KindDRAM, KindPMEM},
			expect: []ID{2, 4},
		},
		{
			name:   "closest DRAM nodes to node #1",
			node:   1,
			kinds:  []Kind{KindDRAM},
			expect: []ID{3},
		},
		{
			name:   "closest PMEM nodes to node #1",
			node:   1,
			kinds:  []Kind{KindPMEM},
			expect: []ID{6},
		},
		{
			name:   "closest DRAM+PMEM nodes to node #1",
			node:   1,
			kinds:  []Kind{KindDRAM, KindPMEM},
			expect: []ID{3, 6},
		},
	} {
		nodes, err := a.GetClosestNodes(tc.node, MaskForKinds(tc.kinds...))
		require.Nil(t, err)
		require.NotNil(t, nodes)
		require.Equal(t, tc.expect, nodes)
	}
}

func TestGetClosestNodesForCPUs(t *testing.T) {
	type testCase struct {
		name   string
		cpus   string
		kinds  []Kind
		expect []ID
		fail   bool
	}

	var (
		sysRoot = "./testdata/sample2"
		sys     sysfs.System
		err     error
		a       *Allocator
	)

	sys, err = sysfs.DiscoverSystemAt(sysRoot + "/sys")
	require.Nil(t, err)
	require.NotNil(t, sys)

	a, err = NewAllocator(WithSystemNodes(sys))
	require.Nil(t, err)
	require.NotNil(t, a)

	for _, tc := range []*testCase{
		{
			name:   "DRAM for CPU #0",
			cpus:   "0",
			kinds:  []Kind{KindDRAM},
			expect: []ID{0},
		},
		{
			name:   "DRAM for CPU #108",
			cpus:   "108",
			kinds:  []Kind{KindDRAM},
			expect: []ID{0},
		},
		{
			name:   "DRAM for CPU #1",
			cpus:   "1",
			kinds:  []Kind{KindDRAM},
			expect: []ID{1},
		},
		{
			name:   "DRAM for CPU #109",
			cpus:   "109",
			kinds:  []Kind{KindDRAM},
			expect: []ID{1},
		},
		{
			name:   "DRAM for CPU #2",
			cpus:   "2",
			kinds:  []Kind{KindDRAM},
			expect: []ID{2},
		},
		{
			name:   "DRAM for CPU #110",
			cpus:   "110",
			kinds:  []Kind{KindDRAM},
			expect: []ID{2},
		},
		{
			name:   "DRAM for CPU #3",
			cpus:   "3",
			kinds:  []Kind{KindDRAM},
			expect: []ID{3},
		},
		{
			name:   "DRAM for CPU #111",
			cpus:   "111",
			kinds:  []Kind{KindDRAM},
			expect: []ID{3},
		},

		{
			name:   "DRAM for CPU #0-1",
			cpus:   "0-1",
			kinds:  []Kind{KindDRAM},
			expect: []ID{0, 1},
		},
		{
			name:   "DRAM for CPU #2-3",
			cpus:   "2-3",
			kinds:  []Kind{KindDRAM},
			expect: []ID{2, 3},
		},
		{
			name:   "DRAM for CPU #0-2",
			cpus:   "0-2",
			kinds:  []Kind{KindDRAM},
			expect: []ID{0, 1, 2},
		},
		{
			name:   "DRAM for CPU #0,1,3",
			cpus:   "0-1,3",
			kinds:  []Kind{KindDRAM},
			expect: []ID{0, 1, 3},
		},
		{
			name:   "DRAM for CPU #0-111",
			cpus:   "0-111",
			kinds:  []Kind{KindDRAM},
			expect: []ID{0, 1, 2, 3},
		},

		{
			name:   "PMEM for CPU #0",
			cpus:   "0",
			kinds:  []Kind{KindPMEM},
			expect: []ID{4},
		},
		{
			name:   "PMEM for CPU #1",
			cpus:   "1",
			kinds:  []Kind{KindPMEM},
			expect: []ID{6},
		},
		{
			name:   "PMEM for CPU #2",
			cpus:   "2",
			kinds:  []Kind{KindPMEM},
			expect: []ID{5},
		},
		{
			name:   "PMEM for CPU #3",
			cpus:   "3",
			kinds:  []Kind{KindPMEM},
			expect: []ID{7},
		},

		{
			name:   "PMEM for CPU #0,1",
			cpus:   "0,1",
			kinds:  []Kind{KindPMEM},
			expect: []ID{4, 6},
		},
		{
			name:   "PMEM for CPU #2,3",
			cpus:   "2,3",
			kinds:  []Kind{KindPMEM},
			expect: []ID{5, 7},
		},
		{
			name:   "PMEM for CPU #108-111",
			cpus:   "108-111",
			kinds:  []Kind{KindPMEM},
			expect: []ID{4, 5, 6, 7},
		},

		{
			name:   "DRAM+PMEM for CPU #0",
			cpus:   "0",
			kinds:  []Kind{KindDRAM, KindPMEM},
			expect: []ID{0, 4},
		},
		{
			name:   "DRAM+PMEM for CPU #1",
			cpus:   "1",
			kinds:  []Kind{KindDRAM, KindPMEM},
			expect: []ID{1, 6},
		},
		{
			name:   "DRAM+PMEM for CPU #2",
			cpus:   "2",
			kinds:  []Kind{KindDRAM, KindPMEM},
			expect: []ID{2, 5},
		},
		{
			name:   "DRAM+PMEM for CPU #3",
			cpus:   "3",
			kinds:  []Kind{KindDRAM, KindPMEM},
			expect: []ID{3, 7},
		},
		{
			name:   "DRAM+PMEM for CPU #0-1",
			cpus:   "0-1",
			kinds:  []Kind{KindDRAM, KindPMEM},
			expect: []ID{0, 1, 4, 6},
		},
		{
			name:   "DRAM+PMEM for CPU #2-3",
			cpus:   "2-3",
			kinds:  []Kind{KindDRAM, KindPMEM},
			expect: []ID{2, 3, 5, 7},
		},
		{
			name:   "DRAM+PMEM for CPU #0-3",
			cpus:   "0-3",
			kinds:  []Kind{KindDRAM, KindPMEM},
			expect: []ID{0, 1, 2, 3, 4, 5, 6, 7},
		},

		{
			name:   "HBMEM for CPU #0",
			cpus:   "0",
			kinds:  []Kind{KindHBM},
			expect: nil,
			fail:   true,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cset, err := cpuset.Parse(tc.cpus)
			require.Nil(t, err)

			result, err := a.GetClosestNodesForCPUs(cset, MaskForKinds(tc.kinds...))

			if tc.fail {
				require.Nil(t, result)
				require.NotNil(t, err)
			} else {
				require.Nil(t, err)
				require.Equal(t, tc.expect, result)
			}
		})
	}
}

func TestGetAvailableKinds(t *testing.T) {
	var (
		sysRoot = "./testdata/sample2"
		sys     sysfs.System
		err     error
		a       *Allocator
	)

	sys, err = sysfs.DiscoverSystemAt(sysRoot + "/sys")
	require.Nil(t, err)
	require.NotNil(t, sys)

	a, err = NewAllocator(WithSystemNodes(sys))
	require.Nil(t, err)
	require.NotNil(t, a)

	kinds := a.GetAvailableKinds()
	require.Equal(t, []Kind{KindDRAM, KindPMEM}, kinds.Slice())
}

func TestExpand(t *testing.T) {
	type testCase struct {
		name  string
		start []ID
		allow []Kind
		nodes []ID
		kinds []Kind
	}

	var (
		sysRoot = "./testdata/sample2"
		sys     sysfs.System
		err     error
		a       *Allocator
	)

	sys, err = sysfs.DiscoverSystemAt(sysRoot + "/sys")
	require.Nil(t, err)
	require.NotNil(t, sys)

	a, err = NewAllocator(WithSystemNodes(sys))
	require.Nil(t, err)
	require.NotNil(t, a)

	for _, tc := range []*testCase{
		{
			name:  "node #0, DRAM expansion #1",
			start: []ID{0},
			allow: []Kind{KindDRAM},
			nodes: []ID{2},
			kinds: []Kind{KindDRAM},
		},
		{
			name:  "node #0, DRAM expansion #2",
			start: []ID{0, 2},
			allow: []Kind{KindDRAM},
			nodes: []ID{1, 3},
			kinds: []Kind{KindDRAM},
		},
		{
			name:  "node #0, PMEM expansion #1",
			start: []ID{0},
			allow: []Kind{KindPMEM},
			nodes: []ID{4},
			kinds: []Kind{KindPMEM},
		},
		{
			name:  "node #0, PMEM expansion #2",
			start: []ID{0, 4},
			allow: []Kind{KindPMEM},
			nodes: []ID{5, 6, 7},
			kinds: []Kind{KindPMEM},
		},
		{
			name:  "node #0, DRAM+PMEM expansion #1",
			start: []ID{0},
			allow: []Kind{KindDRAM, KindPMEM},
			nodes: []ID{2, 4},
			kinds: []Kind{KindDRAM, KindPMEM},
		},
		{
			name:  "node #0, DRAM+PMEM expansion #2",
			start: []ID{0, 2, 4},
			allow: []Kind{KindDRAM, KindPMEM},
			nodes: []ID{1, 3, 5},
			kinds: []Kind{KindDRAM, KindPMEM},
		},
		{
			name:  "node #0, DRAM+PMEM expansion #3",
			start: []ID{0, 1, 2, 3, 4, 5},
			allow: []Kind{KindDRAM, KindPMEM},
			nodes: []ID{6, 7},
			kinds: []Kind{KindPMEM},
		},
	} {
		nodes, kinds := a.Expand(tc.start, MaskForKinds(tc.allow...))
		require.Equal(t, tc.nodes, nodes)
		require.Equal(t, MaskForKinds(tc.kinds...), kinds)
	}
}

func TestNodeMaskCreate(t *testing.T) {
	type testForIDs struct {
		ids  []ID
		mask NodeMask
	}
	for _, tc := range []*testForIDs{
		{
			ids:  []ID{0},
			mask: NodeMask(1 << 0),
		},
		{
			ids:  []ID{0, 1, 5},
			mask: NodeMask(1<<0 | 1<<1 | 1<<5),
		},
		{
			ids:  []ID{0, 31, 32, 33, 63},
			mask: NodeMask(1<<0 | 1<<31 | 1<<32 | 1<<33 | 1<<63),
		},
	} {
		m := NodeMaskForIDs(tc.ids...)
		require.Equal(t, tc.mask, m)
		m = NodeMaskForIDSet(NewIDSet(tc.ids...))
		require.Equal(t, tc.mask, m)
	}
}

func TestNodeMaskToIDs(t *testing.T) {
	type testForIDs struct {
		ids []ID
	}
	for _, tc := range []*testForIDs{
		{
			ids: []ID{0},
		},
		{
			ids: []ID{1, 3},
		},
		{
			ids: []ID{0, 1, 5},
		},
		{
			ids: []ID{0, 31, 32, 33, 63},
		},
	} {
		m := NodeMaskForIDs(tc.ids...)
		require.Equal(t, tc.ids, m.IDs())
		m = NodeMaskForIDSet(NewIDSet(tc.ids...))
		require.Equal(t, tc.ids, m.IDs())
	}
}

func TestAllocate(t *testing.T) {
	const (
		normal  = false
		movable = true
	)

	type testSetup struct {
		name        string
		description string
		nodes       []*Node
	}

	var (
		dist0 = [][]int{
			{10, 11, 20, 20},
			{11, 10, 20, 20},
			{20, 20, 10, 11},
			{20, 20, 11, 10},
		}
		dist1 = [][]int{
			{10, 21, 11, 21, 17, 28, 28, 28},
			{21, 10, 21, 11, 28, 28, 17, 28},
			{11, 21, 10, 21, 28, 17, 28, 28},
			{21, 11, 21, 10, 28, 28, 28, 17},
			{17, 28, 28, 28, 10, 28, 28, 28},
			{28, 28, 17, 28, 28, 10, 28, 28},
			{28, 17, 28, 28, 28, 28, 10, 28},
			{28, 28, 28, 17, 28, 28, 28, 10},
		}
		cpus0 = []cpuset.CPUSet{
			cpuset.New(0),
			cpuset.New(1),
			cpuset.New(2),
			cpuset.New(3),
		}
		cpus1 = []cpuset.CPUSet{
			cpuset.New(0, 1),
			cpuset.New(2, 3),
			cpuset.New(4, 5),
			cpuset.New(6, 7),
			cpuset.New(8, 9),
			cpuset.New(10, 11),
			cpuset.New(12, 13),
			cpuset.New(14, 15),
		}

		setups = map[string]*testSetup{
			"setup0": {
				description: "2 sockets, 2 NUMA nodes, 4 bytes, 1 close CPU",
				nodes: []*Node{
					NewNode(0, KindDRAM, 4, normal, dist0[0], cpus0[0]),
					NewNode(1, KindDRAM, 4, normal, dist0[1], cpus0[1]),
					NewNode(2, KindDRAM, 4, normal, dist0[2], cpus0[2]),
					NewNode(3, KindDRAM, 4, normal, dist0[3], cpus0[2]),
				},
			},
			"setup1": {
				description: "2 sockets, 4 NUMA nodes, 4 bytes, 2 close CPUs",
				nodes: []*Node{
					NewNode(0, KindDRAM, 4, normal, dist1[0], cpus1[0]),
					NewNode(1, KindDRAM, 4, normal, dist1[1], cpus1[1]),
					NewNode(2, KindDRAM, 4, normal, dist1[2], cpus1[2]),
					NewNode(3, KindDRAM, 4, normal, dist1[3], cpus1[3]),
					NewNode(4, KindPMEM, 4, normal, dist1[4], cpus1[4]),
					NewNode(5, KindPMEM, 4, normal, dist1[5], cpus1[5]),
					NewNode(6, KindPMEM, 4, normal, dist1[6], cpus1[6]),
					NewNode(7, KindPMEM, 4, normal, dist1[7], cpus1[7]),
				},
			},
		}

		allocDRAM = []Kind{KindDRAM}
		//allocDRAMAndPMEM = []Kind{KindDRAM, KindPMEM}

		a     *Allocator
		err   error
		wlCnt int
		wlID  string
	)

	a, err = NewAllocator(WithNodes(setups["setup1"].nodes))
	require.Nil(t, err)
	require.NotNil(t, a)

	type testCase struct {
		name    string
		from    []ID
		amount  int64
		kinds   []Kind
		nodes   []ID
		updates []string
		fail    bool
	}

	for _, tc := range []*testCase{
		{
			name:    "fitting first single node allocation #1",
			from:    []ID{0},
			amount:  2,
			kinds:   allocDRAM,
			nodes:   []ID{0},
			updates: nil,
		},
		{
			name:    "fitting first single node allocation #2",
			from:    []ID{2},
			amount:  2,
			kinds:   allocDRAM,
			nodes:   []ID{0},
			updates: nil,
		},

		{
			name:    "fitting first single node allocation #3",
			from:    []ID{0, 2},
			amount:  4,
			kinds:   allocDRAM,
			nodes:   []ID{0, 2},
			updates: nil,
		},

		{
			name:    "fitting first single node allocation #4",
			from:    []ID{0},
			amount:  1,
			kinds:   allocDRAM,
			nodes:   []ID{0},
			updates: nil,
		},
		{
			name:    "fitting first single node allocation #5",
			from:    []ID{1},
			amount:  2,
			kinds:   allocDRAM,
			nodes:   []ID{1},
			updates: nil,
		},
		{
			name:    "fitting first single node allocation #6",
			from:    []ID{3},
			amount:  2,
			kinds:   allocDRAM,
			nodes:   []ID{3},
			updates: nil,
		},
		{
			name:    "non-fitting allocation #7",
			from:    []ID{1, 3},
			amount:  4,
			kinds:   allocDRAM,
			nodes:   []ID{1, 3},
			updates: nil,
			fail:    true,
		},
		{
			name:    "non-fitting allocation #8",
			from:    []ID{1},
			amount:  1,
			kinds:   allocDRAM,
			nodes:   []ID{1},
			updates: nil,
		},
	} {
		wlID = strconv.Itoa(wlCnt)
		wlCnt++

		nodes, updates, err := a.Allocate(&Request{
			Workload: wlID,
			Amount:   tc.amount,
			Kinds:    MaskForKinds(tc.kinds...),
			Nodes:    NodeMaskForIDs(tc.from...),
		})

		if tc.fail {
			require.NotNil(t, err, tc.name)
			require.Nil(t, nodes, tc.name)
			require.Nil(t, updates, tc.name)
		} else {
			t.Logf("=> allocated nodes %v, updated workloads %v", nodes, updates)
			require.Nil(t, err, tc.name)
			require.NotNil(t, nodes, tc.name)
		}
	}
}

type testHW struct {
	description string
	distance    [][]int
	cpuset      [][]int
	capacity    []int64
	kinds       []Kind
	movable     []bool
}

func (hw *testHW) Node(id ID) *Node {
	return NewNode(
		id,
		hw.kinds[id],
		hw.capacity[id],
		hw.movable[id],
		hw.distance[id],
		cpuset.New(hw.cpuset[id]...),
	)
}

func (hw *testHW) Nodes() []*Node {
	var nodes []*Node
	for id := range hw.capacity {
		nodes = append(nodes, hw.Node(id))
	}
	return nodes
}

func (hw *testHW) Allocator() (*Allocator, error) {
	return NewAllocator(WithNodes(hw.Nodes()))
}

func TestMoreOfAllocate(t *testing.T) {
	var (
		testSetup = map[string]testHW{
			"4DRAM": testHW{
				description: "4 DRAM NUMA nodes, 1 close CPU per node",
				distance: [][]int{
					{10, 20, 11, 20},
					{20, 10, 20, 11},
					{11, 20, 10, 20},
					{20, 11, 20, 10},
				},
				cpuset:   [][]int{{0}, {1}, {2}, {3}},
				capacity: []int64{4, 4, 4, 4},
				kinds:    []Kind{KindDRAM, KindDRAM, KindDRAM, KindDRAM},
				movable:  []bool{false, false, false, false},
			},
			"4DRAM+4PMEM": testHW{
				description: "4 DRAM + 4 PMEM NUMA nodes, 2 close CPUs per node",
				distance: [][]int{
					{10, 11, 20, 20, 17, 28, 28, 28},
					{11, 10, 20, 20, 28, 28, 17, 28},
					{20, 20, 10, 11, 28, 17, 28, 28},
					{20, 20, 11, 10, 28, 28, 28, 17},
					{17, 28, 28, 28, 10, 28, 28, 28},
					{28, 28, 17, 28, 28, 10, 28, 28},
					{28, 17, 28, 28, 28, 28, 10, 28},
					{28, 28, 28, 17, 28, 28, 28, 10},
				},
				cpuset: [][]int{
					{0, 2}, {1, 3}, {4, 6}, {5, 7},
					{8, 10}, {9, 11}, {12, 14}, {13, 15},
				},
				capacity: []int64{
					4, 4, 4, 4,
					4, 4, 4, 4,
				},
				kinds: []Kind{
					KindDRAM, KindDRAM, KindDRAM, KindDRAM,
					KindPMEM, KindPMEM, KindPMEM, KindPMEM,
				},
				movable: []bool{
					false, false, false, false,
					false, false, false, false,
				},
			},
		}
	)

	type testCase struct {
		name    string
		amount  int64
		kinds   []Kind
		from    []ID
		nodes   []ID
		updates map[string][]ID
		fail    bool
		release []string
	}

	var (
		allocDRAM  = []Kind{KindDRAM}
		allocators = map[string]*Allocator{}
		hwName     string
		a          *Allocator
		err        error
	)

	for n, hw := range testSetup {
		name := "allocator for test setup " + n
		allocators[n], err = hw.Allocator()
		require.Nil(t, err, name)
		require.NotNil(t, allocators[n], name)
	}

	hwName = "4DRAM"
	a = allocators[hwName]
	for round := range []int{0, 1} {
		allocated := []string{}
		for idx, tc := range []*testCase{
			{
				name:    "fitting allocation from NUMA node #0",
				amount:  2,
				kinds:   allocDRAM,
				from:    []ID{0},
				nodes:   []ID{0},
				updates: nil,
			},
			{
				name:    "fitting allocation from NUMA node #1",
				amount:  2,
				kinds:   allocDRAM,
				from:    []ID{1},
				nodes:   []ID{1},
				updates: nil,
			},
			{
				name:    "fitting allocation from NUMA nodes #0,2",
				amount:  4,
				kinds:   allocDRAM,
				from:    []ID{0, 2},
				nodes:   []ID{0, 2},
				updates: nil,
			},
			{
				name:    "fitting allocation from NUMA nodes #1,3",
				amount:  4,
				kinds:   allocDRAM,
				from:    []ID{1, 3},
				nodes:   []ID{1, 3},
				updates: nil,
			},
			{
				name:    "fitting allocation from NUMA node #0",
				amount:  1,
				kinds:   allocDRAM,
				from:    []ID{0},
				nodes:   []ID{0},
				updates: nil,
			},
			{
				name:    "fitting allocation from NUMA node #1",
				amount:  1,
				kinds:   allocDRAM,
				from:    []ID{1},
				nodes:   []ID{1},
				updates: nil,
			},
			{
				name:    "fitting allocation from NUMA node #0",
				amount:  1,
				kinds:   allocDRAM,
				from:    []ID{0},
				nodes:   []ID{0},
				updates: nil,
			},
			{
				name:    "overflowing allocation from NUMA node #1",
				amount:  1,
				kinds:   allocDRAM,
				from:    []ID{1},
				nodes:   []ID{1},
				updates: nil,
			},
		} {
			var (
				testName            = hwName + "/" + tc.name
				wlID                = strconv.Itoa(idx)
				nodes, updates, err = a.Allocate(&Request{
					Workload: wlID,
					Amount:   tc.amount,
					Kinds:    MaskForKinds(tc.kinds...),
					Nodes:    NodeMaskForIDs(tc.from...),
				})
			)

			if tc.fail {
				require.NotNil(t, err, testName)
				require.Nil(t, nodes, testName)
				require.Nil(t, updates, testName)
				continue
			}

			t.Logf("allocated nodes %v, updated workloads %v", nodes, updates)
			require.Nil(t, err, testName)
			require.Equal(t, tc.nodes, nodes, testName)
			if tc.updates != nil {
				workloads := []string{}
				for wl := range tc.updates {
					workloads = append(workloads, wl)
				}
				sort.Strings(updates)
				sort.Strings(workloads)
				require.Equal(t, workloads, updates, testName)
			}

			allocated = append(allocated, wlID)
		}
		if round == 0 {
			for _, wl := range allocated {
				err = a.Release(wl)
				require.Nil(t, err)
			}
		}
	}
}

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
		require.True(t, m.ContainsAll(tc.ids...))
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

func TestNodeMaskOperations(t *testing.T) {
	type testForIDs struct {
		m1 []ID
		m2 []ID
		ru []ID
		ri []ID
		rd []ID
	}
	for _, tc := range []*testForIDs{
		{
			m1: []ID{0, 2, 4, 6},
			m2: []ID{1, 3, 5, 7},
			ru: []ID{0, 1, 2, 3, 4, 5, 6, 7},
			ri: nil,
			rd: []ID{0, 2, 4, 6},
		},
		{
			m1: []ID{0, 1, 2, 8, 10, 12},
			m2: []ID{0, 3, 8, 10, 14},
			ru: []ID{0, 1, 2, 3, 8, 10, 12, 14},
			ri: []ID{0, 8, 10},
			rd: []ID{1, 2, 12},
		},
	} {
		m1 := NodeMaskForIDs(tc.m1...)
		m2 := NodeMaskForIDs(tc.m2...)
		ru := m1.Union(m2)
		require.Equal(t, tc.ru, ru.IDs(), "incorrect union")
		ri := m1.Intersection(m2)
		require.Equal(t, tc.ri, ri.IDs(), "incorrect intersection")
		rd := m1.Diff(m2)
		require.Equal(t, tc.rd, rd.IDs(), "incorrect diff")
		if ri.Size() > 0 {
			require.True(t, ri.ContainsAny(tc.m1...) || ri.ContainsAny(tc.m2...))
		}
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

		a     *Allocator
		err   error
		wlCnt int
		wlID  string
	)

	a, err = NewAllocator(WithNodes(setups["setup0"].nodes))
	require.Nil(t, err)
	require.NotNil(t, a)

	type testCase struct {
		name    string
		from    []ID
		amount  int64
		kinds   []Kind
		nodes   []ID
		updates []string
		failure error
	}

	for _, tc := range []*testCase{
		{
			name:    "fitting first single node allocation",
			from:    []ID{0},
			amount:  1,
			kinds:   allocDRAM,
			nodes:   []ID{0},
			updates: nil,
			failure: nil,
		},
		{
			name:    "fitting single node allocation",
			from:    []ID{0},
			amount:  2,
			kinds:   allocDRAM,
			nodes:   []ID{0},
			updates: nil,
			failure: nil,
		},
		{
			name:    "non-ftting allocation from single node",
			from:    []ID{0},
			amount:  2,
			kinds:   allocDRAM,
			nodes:   []ID{0, 2},
			updates: nil,
			failure: nil,
		},
	} {
		wlID = strconv.Itoa(wlCnt)
		wlCnt++

		nodes, updates, err := a.Allocate(&Request{
			Workload: wlID,
			Amount:   tc.amount,
			Kinds:    MaskForKinds(tc.kinds...),
			Nodes:    tc.from,
		})

		if tc.failure == nil {
			require.Nil(t, err, tc.name)
			require.Equal(t, tc.nodes, nodes, tc.name)
			require.Equal(t, tc.updates, updates, tc.name)
		}
	}
}

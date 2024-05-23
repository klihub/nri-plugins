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
	"fmt"
	"strconv"
	"testing"

	"github.com/stretchr/testify/require"

	. "github.com/containers/nri-plugins/pkg/resmgr/lib/memory"
	"github.com/containers/nri-plugins/pkg/sysfs"
	"github.com/containers/nri-plugins/pkg/utils/cpuset"
)

func TestNewAllocatorWithSystemNodes(t *testing.T) {
	var (
		sysRoot = "./testdata/sample2"
		sys     sysfs.System
		err     error
		a       *Allocator
	)

	sys, err = sysfs.DiscoverSystemAt(sysRoot + "/sys")
	require.Nil(t, err, "sysfs discovery error for "+sysRoot)
	require.NotNil(t, sys, "sysfs discovery for "+sysRoot)

	a, err = NewAllocator(WithSystemNodes(sys))
	require.Nil(t, err, "allocator creation error")
	require.NotNil(t, a, "created allocator")
}

func TestOffer(t *testing.T) {
	var (
		setup = &testSetup{
			description: "test setup",
			types: []Type{
				TypeDRAM, TypeDRAM,
			},
			capacities: []int64{
				4, 4,
			},
			movability: []bool{
				normal, normal,
			},
			closeCPUs: [][]int{
				{0, 1}, {2, 3},
			},
			distances: [][]int{
				{10, 21},
				{21, 10},
			},
		}
	)

	a, err := NewAllocator(WithNodes(setup.Nodes(t)))
	require.Nil(t, err, "unexpected NewAllocator() error")
	require.NotNil(t, a, "unexpected nil allocator")

	o1, err := a.GetOffer(Container("id1", "test", 1, "burstable", NewNodeMask(0), TypeMaskDRAM))
	require.Nil(t, err, "unexpected GetOffer() error")
	require.NotNil(t, o1, "unexpected nil offer")

	o2, err := a.GetOffer(Container("id2", "test", 1, "burstable", NewNodeMask(1), TypeMaskDRAM))
	require.Nil(t, err, "unexpected GetOffer() error")
	require.NotNil(t, o2, "unexpected nil offer")

	n, _, err := o1.Commit()
	require.Nil(t, err, "unexpected Offer.Commit() error")
	require.NotEqual(t, n, NodeMask(0), "unexpected Offer.Commit() failure")

	n, _, err = o2.Commit()
	t.Logf("got error %v", err)
	require.NotNil(t, err, "unexpected success, offer should have expired")
	require.Equal(t, n, NodeMask(0), "failed commit should return 0 NodeMask")
}

func TestAllocate(t *testing.T) {
	var (
		setup = &testSetup{
			description: "4 DRAM+4 PMEM NUMA nodes, 4 bytes per node, 2 close CPUs",
			types: []Type{
				TypeDRAM, TypeDRAM, TypeDRAM, TypeDRAM,
				TypePMEM, TypePMEM, TypePMEM, TypePMEM,
			},
			capacities: []int64{
				4, 4, 4, 4,
				4, 4, 4, 4,
			},
			movability: []bool{
				normal, normal, normal, normal,
				normal, normal, normal, normal,
			},
			closeCPUs: [][]int{
				{0, 1}, {2, 3}, {4, 5}, {6, 7},
				{8, 9}, {10, 11}, {12, 13}, {14, 15},
			},
			distances: [][]int{
				{10, 21, 11, 21, 17, 28, 28, 28},
				{21, 10, 21, 11, 28, 28, 17, 28},
				{11, 21, 10, 21, 28, 17, 28, 28},
				{21, 11, 21, 10, 28, 28, 28, 17},
				{17, 28, 28, 28, 10, 28, 28, 28},
				{28, 28, 17, 28, 28, 10, 28, 28},
				{28, 17, 28, 28, 28, 28, 10, 28},
				{28, 28, 28, 17, 28, 28, 28, 10},
			},
		}
	)

	a, err := NewAllocator(WithNodes(setup.Nodes(t)))
	require.Nil(t, err)
	require.NotNil(t, a)

	type testCase struct {
		name     string
		id       string
		limit    int64
		types    TypeMask
		affinity NodeMask
		qosClass string
		result   NodeMask
		updates  map[string]NodeMask
		fail     bool
		reset    bool
		release  []string
	}

	for _, tc := range []*testCase{
		{
			name:     "too big allocation",
			id:       "1",
			affinity: NewNodeMask(0, 1, 2, 3),
			limit:    33,
			types:    TypeMaskDRAM,
			fail:     true,
		},
		{
			name:  "allocation with unavailable node",
			id:    "1",
			limit: 1,
			types: TypeMaskDRAM,
			fail:  true,
		},
		{
			name:  "allocation without affinity",
			id:    "1",
			limit: 1,
			types: TypeMaskDRAM,
			fail:  true,
		},
		{
			name:  "allocation with unavailable node type",
			id:    "1",
			limit: 1,
			types: TypeMaskHBM,
			fail:  true,
		},
		{
			name:     "2 bytes of DRAM from node #0",
			id:       "1",
			affinity: NewNodeMask(0),
			limit:    2,
			types:    TypeMaskDRAM,
			result:   NewNodeMask(0),
		},
		{
			name:  "allocation attempt with existing ID",
			id:    "1",
			limit: 1,
			types: TypeMaskDRAM,
			fail:  true,
		},
		{
			name:     "2 bytes of DRAM from node #2",
			id:       "2",
			affinity: NewNodeMask(2),
			limit:    2,
			types:    TypeMaskDRAM,
			result:   NewNodeMask(2),
		},
		{
			name:     "2 bytes of DRAM from node #0",
			id:       "3",
			affinity: NewNodeMask(0),
			limit:    2,
			types:    TypeMaskDRAM,
			result:   NewNodeMask(0),
		},
		{
			name:     "2 bytes of DRAM from node #2",
			id:       "4",
			affinity: NewNodeMask(2),
			limit:    2,
			types:    TypeMaskDRAM,
			result:   NewNodeMask(2),
		},
		{
			name:     "2 bytes of DRAM, guaranteed from node #0",
			id:       "5",
			affinity: NewNodeMask(0),
			limit:    2,
			types:    TypeMaskDRAM,
			qosClass: "guaranteed",
			result:   NewNodeMask(0),
			updates:  map[string]NodeMask{"1": NewNodeMask(0, 1, 2, 3)},
		},
		{
			name:     "2 bytes of DRAM, guaranteed from node #2",
			id:       "6",
			affinity: NewNodeMask(2),
			limit:    2,
			types:    TypeMaskDRAM,
			qosClass: "guaranteed",
			result:   NewNodeMask(2),
			updates:  map[string]NodeMask{"2": NewNodeMask(0, 1, 2, 3)},
		},
		{
			name:     "2 bytes of DRAM, guaranteed from node #0",
			id:       "7",
			affinity: NewNodeMask(0),
			limit:    2,
			types:    TypeMaskDRAM,
			qosClass: "guaranteed",
			result:   NewNodeMask(0),
			updates:  map[string]NodeMask{"3": NewNodeMask(0, 1, 2, 3)},
		},
		{
			name:     "2 bytes of DRAM, guaranteed from node #2",
			id:       "8",
			affinity: NewNodeMask(2),
			limit:    2,
			types:    TypeMaskDRAM,
			qosClass: "guaranteed",
			result:   NewNodeMask(2),
			updates:  map[string]NodeMask{"4": NewNodeMask(0, 1, 2, 3)},
		},
		{
			name:     "2 bytes of DRAM from node #1",
			id:       "9",
			affinity: NewNodeMask(1),
			limit:    2,
			types:    TypeMaskDRAM,
			result:   NewNodeMask(1),
			updates:  map[string]NodeMask{"1": NewNodeMask(0, 1, 2, 3, 4, 5, 6, 7)},
		},
		{
			name:     "2 bytes of DRAM from node #3",
			id:       "10",
			affinity: NewNodeMask(3),
			limit:    2,
			types:    TypeMaskDRAM,
			result:   NewNodeMask(3),
			updates:  map[string]NodeMask{"2": NewNodeMask(0, 1, 2, 3, 4, 5, 6, 7)},
		},
		{
			name:     "2 bytes of DRAM from node #1",
			id:       "11",
			affinity: NewNodeMask(1),
			limit:    2,
			types:    TypeMaskDRAM,
			result:   NewNodeMask(1),
			updates:  map[string]NodeMask{"3": NewNodeMask(0, 1, 2, 3, 4, 5, 6, 7)},
		},
		{
			name:     "2 bytes of DRAM from node #3",
			id:       "12",
			affinity: NewNodeMask(3),
			limit:    2,
			types:    TypeMaskDRAM,
			result:   NewNodeMask(3),
			updates:  map[string]NodeMask{"4": NewNodeMask(0, 1, 2, 3, 4, 5, 6, 7)},
		},
		{
			name:     "2 bytes of DRAM from node #1, then release all",
			id:       "13",
			affinity: NewNodeMask(1),
			limit:    2,
			types:    TypeMaskDRAM,
			result:   NewNodeMask(1),
			updates:  map[string]NodeMask{"11": NewNodeMask(0, 1, 2, 3, 4, 5, 6, 7)},
			release:  []string{"1", "2", "3", "4", "5", "6", "7", "8", "9", "10", "11", "12", "13"},
		},

		// ----- all allocations released -----

		{
			name:     "6 bytes of DRAM from nodes #0,2",
			id:       "14",
			affinity: NewNodeMask(0, 2),
			limit:    6,
			types:    TypeMaskDRAM,
			result:   NewNodeMask(0, 2),
		},
		{
			name:     "3 bytes of DRAM from node #0",
			id:       "15",
			affinity: NewNodeMask(0),
			limit:    3,
			types:    TypeMaskDRAM,
			result:   NewNodeMask(0),
			updates:  map[string]NodeMask{"14": NewNodeMask(0, 1, 2, 3)},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			qosClass := tc.qosClass
			if qosClass == "" {
				qosClass = "burstable"
			}
			nodes, updates, err := a.Allocate(
				Container(tc.id, tc.name, tc.limit, qosClass, tc.affinity, tc.types),
			)

			if tc.fail {
				require.NotNil(t, err, "unexpected allocation success")
				require.Equal(t, NodeMask(0), nodes, tc.name)
				require.Nil(t, updates, tc.name)
				t.Logf("* got error %v", err)
			} else {
				require.Nil(t, err, "unexpected allocation failure")
				require.Equal(t, tc.result, nodes, "allocated nodes")
				require.Equal(t, tc.updates, updates, "updated nodes")
			}

			if tc.reset {
				a.Reset()
			}

			if len(tc.release) > 0 {
				for _, id := range tc.release {
					err := a.Release(id)
					require.Nil(t, err, "release of ID #"+id)
				}
			}
		})
	}
}

type testSetup struct {
	description string
	types       []Type
	capacities  []int64
	movability  []bool
	closeCPUs   [][]int
	distances   [][]int
}

const (
	movable = true
	normal  = false
)

func (s *testSetup) Nodes(t *testing.T) []*Node {
	var nodes []*Node

	for id, memType := range s.types {
		var (
			capacity  = s.capacities[id]
			normal    = !s.movability[id]
			closeCPUs = cpuset.New(s.closeCPUs[id]...)
			distance  = s.distances[id]
		)

		phase := fmt.Sprintf("node #%d for test setup %s", id, s.description)
		n, err := NewNode(id, memType, capacity, normal, closeCPUs, distance)
		require.Nil(t, err, phase)
		require.NotNil(t, n, phase)

		nodes = append(nodes, n)
	}

	return nodes
}

var (
	nextID = 1
)

func newID() string {
	id := strconv.Itoa(nextID)
	nextID++
	return id
}

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
	"testing"

	"github.com/stretchr/testify/require"

	. "github.com/containers/nri-plugins/pkg/resmgr/lib/memory"
)

func TestMaskCache(t *testing.T) {
	var (
		setup = &testSetup{
			description: "4 DRAM+4 PMEM NUMA nodes, 8 bytes per node, 2 close CPUs",
			types: []Type{
				TypeDRAM, TypeDRAM, TypeDRAM, TypeDRAM,
				TypePMEM, TypePMEM, TypePMEM, TypePMEM,
			},
			capacities: []int64{
				4, 0, 4, 4,
				0, 4, 4, 4,
			},
			movability: []bool{
				normal, normal, normal, normal,
				movable, movable, movable, movable,
			},
			closeCPUs: [][]int{
				{0, 1}, {2, 3}, {4, 5}, {6, 7},
				{8, 9}, {}, {12, 13}, {},
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

		allTypes        = NewTypeMask(TypeDRAM, TypePMEM)
		allNodes        = NewNodeMask(0, 1, 2, 3, 4, 5, 6, 7)
		normalNodes     = NewNodeMask(0, 2, 3)
		movableNodes    = NewNodeMask(5, 6, 7)
		nodesWithMem    = NewNodeMask(0, 2, 3, 5, 6, 7)
		nodesWithoutMem = NewNodeMask(1, 4)
		nodesWithoutCPU = NewNodeMask(5, 7)
		nodesWithCPU    = NewNodeMask(0, 1, 2, 3, 4, 6)

		dramMask         = NewTypeMask(TypeDRAM)
		pmemMask         = NewTypeMask(TypePMEM)
		hbmMask          = NewTypeMask(TypeHBM)
		dramAndPmemMask  = NewTypeMask(TypeDRAM, TypePMEM)
		dramAndHbmMask   = NewTypeMask(TypeDRAM, TypeHBM)
		pmemAndHbmMask   = NewTypeMask(TypePMEM, TypeHBM)
		allTypesMask     = NewTypeMask(TypeDRAM, TypePMEM, TypeHBM)
		dramNodes        = NewNodeMask(0, 2, 3)
		pmemNodes        = NewNodeMask(5, 6, 7)
		dramAndPmemNodes = NewNodeMask(0, 2, 3, 5, 6, 7)
		emptyNodeMask    = NewNodeMask()
	)

	m := NewMaskCache()
	require.NotNil(t, m, "created masks")
	for _, n := range setup.Nodes(t) {
		m.AddNode(n)
	}

	require.Equal(t, allTypes, m.AvailableTypes(), "all types")
	require.Equal(t, allNodes, m.AvailableNodes(), "all nodes")
	require.Equal(t, normalNodes, m.NodesWithNormalMem(), "normal nodes")
	require.Equal(t, movableNodes, m.NodesWithMovableMem(), "movable nodes")
	require.Equal(t, nodesWithMem, m.NodesWithMem(), "nodes with memory")
	require.Equal(t, nodesWithoutMem, m.NodesWithoutMem(), "nodes without memory")
	require.Equal(t, nodesWithoutCPU, m.NodesWithoutCloseCPUs(), "nodes without close CPU")
	require.Equal(t, nodesWithCPU, m.NodesWithCloseCPUs(), "nodes with close CPU")
	require.Equal(t, dramNodes, m.NodesByTypes(dramMask), "DRAM nodes")
	require.Equal(t, pmemNodes, m.NodesByTypes(pmemMask), "PMEM nodes")
	require.Equal(t, emptyNodeMask, m.NodesByTypes(hbmMask), "HBM nodes")
	require.Equal(t, dramAndPmemNodes, m.NodesByTypes(dramAndPmemMask), "DRAM and PMEM nodes")
	require.Equal(t, emptyNodeMask, m.NodesByTypes(dramAndHbmMask), "DRAM and HBM nodes")
	require.Equal(t, emptyNodeMask, m.NodesByTypes(pmemAndHbmMask), "PMEM and HBM nodes")
	require.Equal(t, emptyNodeMask, m.NodesByTypes(allTypesMask), "DRAM and PMEM and HBM nodes")
}

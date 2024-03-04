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

package memory_test

import (
	libmem "github.com/containers/nri-plugins/pkg/resmgr/lib/memory"

	"testing"

	"github.com/stretchr/testify/require"
	_ "github.com/stretchr/testify/require"
)

type ID = libmem.ID

func DontTestNodeSetup(t *testing.T) {
}

func TestAllocation(t *testing.T) {
	type request struct {
		amount int64
		nodes  []ID
		kinds  libmem.KindMask
	}
	type testCase struct {
		name      string
		nodes     []*libmem.Node
		requests  []*request
		ids       [][]libmem.ID
		exhausted [][]ID
		failures  []bool
	}

	for _, tc := range []*testCase{
		{
			name: "1 DRAM nodes, a few allocations",
			nodes: []*libmem.Node{
				libmem.NewNode(0, libmem.KindDRAM, 256, []int{10}),
			},
			requests: []*request{
				{64, []ID{0}, 0},
				{64, []ID{0}, 0},
				{128, []ID{0}, 0},
				{1, []ID{0}, 0},
			},
			ids: [][]libmem.ID{
				{0},
				{0},
				{0},
				nil,
			},
			exhausted: [][]ID{
				nil,
				nil,
				[]ID{0},
				nil,
			},
			failures: []bool{
				false,
				false,
				false,
				true,
			},
		},
		{
			name: "4 DRAM nodes, a few allocations",
			nodes: []*libmem.Node{
				libmem.NewNode(0, libmem.KindDRAM, 64, []int{10, 20, 20, 20}),
				libmem.NewNode(1, libmem.KindDRAM, 64, []int{20, 10, 20, 20}),
				libmem.NewNode(2, libmem.KindDRAM, 32, []int{20, 20, 10, 20}),
				libmem.NewNode(3, libmem.KindDRAM, 16, []int{20, 20, 20, 10}),
			},
			requests: []*request{
				{64, []ID{0, 1, 2, 3}, 0},
				{64, []ID{0, 1, 2, 3}, 0},
				{48, []ID{0, 1, 2, 3}, 0},
				{16, []ID{0, 1, 2, 3}, 0},
			},
			ids: [][]libmem.ID{
				{0, 1, 2, 3},
				{0, 1, 2},
				{0, 1},
				nil,
			},
			exhausted: [][]ID{
				[]ID{3},
				[]ID{2},
				[]ID{0, 1},
				nil,
			},
			failures: []bool{
				false,
				false,
				false,
				true,
			},
		},
	} {
		for round := 0; round < 1; round++ {
			t.Run(tc.name, func(t *testing.T) {
				a := libmem.NewAllocator(tc.nodes...)
				results := []libmem.Reservation{}

				for i := 0; i < len(tc.requests); i++ {
					req := tc.requests[i]
					res, exhausted, err := a.AllocateKind(req.amount, req.nodes, req.kinds)

					if tc.failures[i] {
						require.NotNil(t, err)
						continue
					}

					require.Equal(t, tc.ids[i], res.IDs())
					require.Equal(t, tc.exhausted[i], exhausted)

					results = append(results, res)
				}

				for _, res := range results {
					a.Release(res)
				}
			})
		}
	}
}

func DontTestRelease(t *testing.T) {
}

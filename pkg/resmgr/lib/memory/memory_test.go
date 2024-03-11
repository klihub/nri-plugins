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
	"github.com/containers/nri-plugins/pkg/sysfs"
	"github.com/containers/nri-plugins/pkg/utils/cpuset"

	. "github.com/containers/nri-plugins/pkg/resmgr/lib/memory"

	"testing"

	"github.com/stretchr/testify/require"
)

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

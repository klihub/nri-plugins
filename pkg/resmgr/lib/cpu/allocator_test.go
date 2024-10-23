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

package libcpu_test

import (
	"strconv"
	"testing"

	"github.com/stretchr/testify/require"

	. "github.com/containers/nri-plugins/pkg/resmgr/lib/cpu"
	"github.com/containers/nri-plugins/pkg/sysfs"
)

func TestNewCpuAllocator(t *testing.T) {
	type testCase struct {
		name  string
		which string
		fail  bool
	}

	for _, tc := range []*testCase{
		{
			name:  "sample1",
			which: "sample1",
		},
		{
			name:  "sample2",
			which: "sample2",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, _ = getAllocatorForTestSysfs(t, tc.which)
		})
	}
}

func TestBasicAllocation(t *testing.T) {
	type testCase struct {
		name      string
		id        string
		pool      string
		exclusive int
		shared    int
		private   CpuMask
		common    CpuMask
		updates   map[string]CpuMask
		fail      bool
		release   []string
	}

	// use test sysfs sample2:
	//   2 sockets with HT-enabled cores:
	//     - socket #0: 0,2,4,6,...,110 (56 cores, isolated: 4,6,60,62)
	//         NUMA nodes:
	//           node #0, DRAM, cores 0,4,8,12,16,...,108 (28 cores, isolated: 4, 60)
	//           node #2, DRAM, cores 2,6,10,14,18,...,110 (28 cores, isolated: 6, 62)
	//     - socket #1: 1,3,5,7,...,111 (56 cores, isolated: 5,7,61,63)
	//         NUMA nodes:
	//           node #1: DRAM, cores 1,5,9,13,17,...,109 (28 cores, isolated: 5, 61)
	//           node #3: DRAM, cores 3,7,11,15,19...,111 (28 cores, 7, 63)

	var (
		a, sys   = getAllocatorForTestSysfs(t, "sample2")
		pools    = map[string]CpuMask{}
		isolated = map[string]CpuMask{}
	)

	pools["root"] = NewCpuMaskForCPUSet(sys.OnlineCPUs())
	isolated["root"] = NewCpuMaskForCPUSet(sys.IsolatedCPUs())

	for _, id := range sys.PackageIDs() {
		if cset := sys.Package(id).CPUSet(); cset.Size() > 0 {
			pools["socket#"+strconv.Itoa(id)] = NewCpuMaskForCPUSet(cset)
		}
	}

	for _, id := range sys.NodeIDs() {
		if cset := sys.Node(id).CPUSet(); cset.Size() > 0 {
			pools["node#"+strconv.Itoa(id)] = NewCpuMaskForCPUSet(cset)
		}
	}

	for pool, cpus := range pools {
		isolated[pool] = isolated["root"].Intersection(cpus)
	}

	for _, tc := range []*testCase{
		// first we consume all shared capacity of node #0
		{
			name:   "alloc 6500m shared CPU from node #0",
			id:     "1",
			pool:   "node#0",
			shared: 6500,
			common: pools["node#0"].Difference(isolated["node#0"]),
		},
		{
			name:   "alloc 7500m shared CPU from node #0",
			id:     "2",
			pool:   "node#0",
			shared: 7500,
			common: pools["node#0"].Difference(isolated["node#0"]),
		},
		{
			name:   "alloc 8500m shared CPU from node #0",
			id:     "3",
			pool:   "node#0",
			shared: 8500,
			common: pools["node#0"].Difference(isolated["node#0"]),
		},
		{
			name:   "alloc 3500m shared CPU from node #0",
			id:     "4",
			pool:   "node#0",
			shared: 3500,
			common: pools["node#0"].Difference(isolated["node#0"]),
		},
		// this should fail: we've exhausted the shared pool of node#0
		{
			name:   "fail to alloc 3500m shared CPU from node #0",
			id:     "5",
			pool:   "node#0",
			shared: 100,
			fail:   true,
		},
		// next we put some charge in socket #1, then consume all shared capacity of node #1
		{
			name:   "alloc 26500m shared CPU from socket #1",
			id:     "6",
			pool:   "socket#1",
			shared: 26500,
			common: pools["socket#1"].Difference(isolated["socket#1"]),
		},
		{
			name:   "alloc 8500m shared CPU from node #1",
			id:     "7",
			pool:   "node#1",
			shared: 8500,
			common: pools["node#1"].Difference(isolated["node#1"]),
		},
		{
			name:   "alloc 7500m shared CPU from node #1",
			id:     "8",
			pool:   "node#1",
			shared: 7500,
			common: pools["node#1"].Difference(isolated["node#1"]),
		},
		{
			name:   "alloc 6500m shared CPU from node #1",
			id:     "9",
			pool:   "node#1",
			shared: 6500,
			common: pools["node#1"].Difference(isolated["node#1"]),
		},
		// this should fail: 26500 from socket #1 consumes 500 from node #1, so we have 3000 left
		{
			name:   "fail to alloc 3500m shared CPU from node #1",
			id:     "10",
			pool:   "node#1",
			shared: 3500,
			fail:   true,
		},
		// but this should then succeed
		{
			name:   "alloc 3000m shared CPU from node #1",
			id:     "10",
			pool:   "node#1",
			shared: 3000,
			common: pools["node#1"].Difference(isolated["node#1"]),
		},
		// release allocation from socket #1
		{
			name:    "free 26500m shared CPU from socket #1 and 3000m from node #1",
			id:      "",
			release: []string{"6", "10"},
		},
		// now we should be able to fully consume node #1
		{
			name:   "alloc 3500m shared CPU from node #1",
			id:     "11",
			pool:   "node#1",
			shared: 3500,
			common: pools["node#1"].Difference(isolated["node#1"]),
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if tc.id != "" {
				req := NewRequest(tc.id, tc.name, pools[tc.pool], tc.exclusive, tc.shared, NoPriority)
				cpus, updates, err := a.Allocate(req)

				if tc.fail {
					require.Error(t, err, "allocation should fail")
					require.Nil(t, cpus, "failed allocation should return nil CPUs")
					require.Nil(t, updates, "failed allocation should return nil updates")
				} else {
					require.NoError(t, err, "allocation should succeed")
					require.Equal(t, tc.private.Union(tc.common), cpus, "allocated CPUs")
					require.Equal(t, tc.updates, updates, "update requests for allocation")
				}
			}

			for _, id := range tc.release {
				_, err := a.Release(id)
				require.NoError(t, err, "release should succeed")
			}
		})
	}
}

func TestAllocation(t *testing.T) {
	type testCase struct {
		name      string
		id        string
		pool      CpuMask
		exclusive int
		shared    int
		private   CpuMask
		common    CpuMask
		updates   map[string]CpuMask
		fail      bool
	}

	a, _ := getAllocatorForTestSysfs(t, "sample2")

	for _, tc := range []*testCase{
		{
			name:      "allocate 2 exclusive CPUs from pool 0-2,56-58",
			id:        "1",
			pool:      NewCpuMask(0, 1, 2, 56, 57, 58),
			exclusive: 2,
			shared:    0,
			fail:      false,
			private:   NewCpuMask(0, 56),
			common:    NewCpuMask(),
		},
		{
			name:      "allocate 2 exclusive+150m CPUs from pool 0-2,56-58",
			id:        "2",
			pool:      NewCpuMask(0, 1, 2, 56, 57, 58),
			exclusive: 2,
			shared:    150,
			fail:      false,
			private:   NewCpuMask(1, 57),
			common:    NewCpuMask(2, 58),
		},
		{
			name:    "allocate 850m CPUs from pool 0-2,56-58",
			id:      "3",
			pool:    NewCpuMask(0, 1, 2, 56, 57, 58),
			shared:  850,
			fail:    false,
			private: NewCpuMask(),
			common:  NewCpuMask(2, 58),
		},
		{
			name:   "allocate 1100m CPUs from pool 0-2,56-58",
			id:     "4",
			pool:   NewCpuMask(0, 1, 2, 56, 57, 58),
			shared: 1100,
			fail:   true,
		},
		{
			name:      "allocate 1100m CPUs from pool 0-2,56-58",
			id:        "5",
			pool:      NewCpuMask(0, 1, 2, 56, 57, 58),
			exclusive: 1,
			private:   NewCpuMask(2),
			common:    NewCpuMask(),
			updates: map[string]CpuMask{
				"2": NewCpuMask(58),
				"3": NewCpuMask(58),
			},
		},
		{
			name:   "best-effort CPUs from pool 0-2,56-58",
			id:     "5",
			pool:   NewCpuMask(0, 1, 2, 56, 57, 58),
			shared: 0,
			fail:   true,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			name := "test-" + tc.id
			req := NewRequest(tc.id, name, tc.pool, tc.exclusive, tc.shared, NoPriority)

			cpus, updates, err := a.Allocate(req)
			if tc.fail {
				require.Error(t, err, "unexpected allocation success")
				require.Nil(t, cpus, "unexpected non-nil CPUs")
				require.Nil(t, updates, "unexpected non-nil CPUs")
				return
			}

			require.NoError(t, err, "unexpected allocation failure")
			require.Equal(t, tc.private.Union(tc.common), cpus, "allocated CPUs")
			require.Equal(t, tc.updates, updates, "updated CPUs for existing allocations")
		})
	}
}

func getAllocatorForTestSysfs(t *testing.T, which string) (*Allocator, sysfs.System) {
	var (
		sysRoot = "./testdata/" + which + "/sys"
		sys     sysfs.System
		a       *Allocator
		err     error
	)

	sys, err = sysfs.DiscoverSystemAt(sysRoot)
	require.Nil(t, err, which+" sysfs discovery error")
	require.NotNil(t, sys, "discovered "+which+" sysfs is nil")

	a, err = NewAllocator(WithSystem(sys))
	require.NoError(t, err, "allocator creation error")
	require.NotNil(t, a, "created allocator is nil")

	return a, sys
}

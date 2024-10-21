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
	"testing"

	"github.com/stretchr/testify/require"

	. "github.com/containers/nri-plugins/pkg/resmgr/lib/cpu"
	"github.com/containers/nri-plugins/pkg/sysfs"
)

func TestNewCpuAllocator(t *testing.T) {
	var (
		sysRoot = "./testdata/sample2"
		sys     sysfs.System
		err     error
		a       *Allocator
	)

	sys, err = sysfs.DiscoverSystemAt(sysRoot + "/sys")
	require.Nil(t, err, "sysfs discovery error for "+sysRoot)
	require.NotNil(t, sys, "discovered sysfs for "+sysRoot)

	a, err = NewAllocator(WithSystem(sys))
	require.NoError(t, err, "allocator creation error")
	require.NotNil(t, a, "created allocator")
}

func TestAllocate(t *testing.T) {
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

	var (
		sysRoot = "./testdata/sample2"
		sys     sysfs.System
		err     error
		a       *Allocator
	)

	sys, err = sysfs.DiscoverSystemAt(sysRoot + "/sys")
	require.Nil(t, err, "sysfs discovery error for "+sysRoot)
	require.NotNil(t, sys, "discovered sysfs for "+sysRoot)

	a, err = NewAllocator(WithSystem(sys))
	require.NoError(t, err, "allocator creation error")
	require.NotNil(t, a, "created allocator")

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

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
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	. "github.com/containers/nri-plugins/pkg/resmgr/lib/cpu"
)

func TestParseCpuMask(t *testing.T) {
	type testCase struct {
		name string
		mask string
		fail bool
	}
	for _, tc := range []*testCase{
		{
			name: "empty mask",
			mask: "",
		},
		{
			name: "single cpu mask",
			mask: "0",
		},
		{
			name: "no ranges mask",
			mask: "0,2,4,6,8,11,17",
		},
		{
			name: "mask with single range",
			mask: "0-5",
		},
		{
			name: "mask with multiple ranges",
			mask: "0-5,30-35,62-64,124-127",
		},
		{
			name: "invalid mask, missing ID",
			mask: "0,2,4,6,8,11,17,",
			fail: true,
		},
		{
			name: "invalid mask, invalid ID",
			mask: "0,2,xyzzy,11",
			fail: true,
		},
		{
			name: "invalid mask, too large ID",
			mask: "0,2,4,6,8,11,17,128",
			fail: true,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			m, err := ParseCpuMask(tc.mask)
			if tc.fail {
				require.Error(t, err)
			} else {
				require.NoError(t, err)
				require.Equal(t, tc.mask, m.KernelString())
			}
		})
	}
}

func TestCpuMaskSlice(t *testing.T) {
	type testCase struct {
		name   string
		mask   string
		result []ID
		fail   bool
	}
	for _, tc := range []*testCase{
		{
			name: "empty mask",
			mask: "",
		},
		{
			name:   "single cpu mask",
			mask:   "0",
			result: []ID{0},
		},
		{
			name:   "no ranges mask",
			mask:   "0,2,4,6,8,11,17",
			result: []ID{0, 2, 4, 6, 8, 11, 17},
		},
		{
			name:   "mask with single range",
			mask:   "0-5",
			result: []ID{0, 1, 2, 3, 4, 5},
		},
		{
			name:   "mask with multiple ranges",
			mask:   "0-5,30-35,62-64,124-127",
			result: []ID{0, 1, 2, 3, 4, 5, 30, 31, 32, 33, 34, 35, 62, 63, 64, 124, 125, 126, 127},
		},
		{
			name: "invalid mask, missing ID",
			mask: "0,2,4,6,8,11,17,",
			fail: true,
		},
		{
			name: "invalid mask, invalid ID",
			mask: "0,2,xyzzy,11",
			fail: true,
		},
		{
			name: "invalid mask, too large ID",
			mask: "0,2,4,6,8,11,17,128",
			fail: true,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			m, err := ParseCpuMask(tc.mask)
			if tc.fail {
				require.Error(t, err)
			} else {
				require.NoError(t, err)
				require.Equal(t, tc.result, m.Slice(), m)
			}
		})
	}
}

func TestCpuMaskString(t *testing.T) {
	SetMaxCpuCount(142)

	type testCase struct {
		name   string
		ids    []ID
		result string
	}
	for _, tc := range []*testCase{
		{
			name:   "empty mask",
			ids:    []ID{},
			result: "cpumask{}",
		},
		{
			name:   "single cpu mask",
			ids:    []ID{0},
			result: "cpumask{0}",
		},
		{
			name:   "no ranges mask",
			ids:    []ID{0, 2, 4, 6, 8, 11, 17},
			result: "cpumask{0,2,4,6,8,11,17}",
		},
		{
			name:   "multiple cpus single range mask",
			ids:    []ID{0, 1, 2, 3, 4, 5, 6, 7},
			result: "cpumask{0-7}",
		},
		{
			name:   "single word, multiple ranges mask",
			ids:    []ID{0, 1, 2, 3, 4, 5, 6, 7, 10, 11, 12, 13, 14, 15},
			result: "cpumask{0-7,10-15}",
		},
		{
			name:   "double word, no ranges mask",
			ids:    []ID{0, 2, 4, 6, 8, 11, 18, 21, 23, 31, 33, 35, 39, 47, 111},
			result: "cpumask{0,2,4,6,8,11,18,21,23,31,33,35,39,47,111}",
		},
		{
			name:   "double word, single range each mask",
			ids:    []ID{0, 1, 2, 3, 4, 5, 6, 7, 64, 65, 66, 67, 68, 69, 70, 71},
			result: "cpumask{0-7,64-71}",
		},
		{
			name:   "double word, multiple ranges mask",
			ids:    []ID{0, 1, 2, 3, 5, 6, 7, 29, 30, 31, 64, 65, 66, 67, 81, 82, 83, 110, 111, 112},
			result: "cpumask{0-3,5-7,29-31,64-67,81-83,110-112}",
		},
		{
			name:   "triple word, multiple ranges mask",
			ids:    []ID{0, 1, 2, 3, 30, 31, 32, 96, 97, 98, 111, 112, 113, 129, 130, 131, 140, 141},
			result: "cpumask{0-3,30-32,96-98,111-113,129-131,140-141}",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			require.Equal(t, tc.result, NewCpuMask(tc.ids...).String())
		})
	}
}

func TestCpuMaskSetAndClear(t *testing.T) {
	type testCase struct {
		name   string
		ids    []ID
		result string
	}
	mask := NewCpuMask()
	for _, tc := range []*testCase{
		{
			name:   "set CPU 0",
			ids:    []ID{0},
			result: "cpumask{0}",
		},
		{
			name:   "set CPU 1",
			ids:    []ID{1},
			result: "cpumask{0-1}",
		},
		{
			name:   "set CPUs 3, 5, 7, 63, 64, 65, 110, 111, 112",
			ids:    []ID{3, 5, 7, 63, 64, 65, 110, 111, 112},
			result: "cpumask{0-1,3,5,7,63-65,110-112}",
		},
		{
			name:   "clear CPU 0",
			ids:    []ID{0},
			result: "cpumask{1,3,5,7,63-65,110-112}",
		},
		{
			name:   "clear CPUs 1, 2, 5, 64, 111",
			ids:    []ID{1, 2, 5, 64, 111},
			result: "cpumask{3,7,63,65,110,112}",
		},
		{
			name:   "clear CPU 3, 7, 110",
			ids:    []ID{3, 7, 110},
			result: "cpumask{63,65,112}",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			switch {
			case strings.HasPrefix(tc.name, "set"):
				mask.Set(tc.ids...)
			case strings.HasPrefix(tc.name, "clear"):
				mask.Clear(tc.ids...)
			default:
				panic("invalid test case")
			}
			require.Equal(t, tc.result, mask.String())
		})
	}
}

func TestCpuMaskContains(t *testing.T) {
	type testCase struct {
		name   string
		ids    []ID
		test   []ID
		result bool
	}
	for _, tc := range []*testCase{
		{
			name:   "empty mask, contains all",
			ids:    []ID{},
			test:   []ID{0},
			result: false,
		},
		{
			name:   "empty mask, contains any",
			ids:    []ID{},
			test:   []ID{0},
			result: false,
		},
		{
			name:   "single cpu mask, contains all",
			ids:    []ID{0},
			test:   []ID{0},
			result: true,
		},
		{
			name:   "single cpu mask, contains any",
			ids:    []ID{0},
			test:   []ID{0},
			result: true,
		},
		{
			name:   "multiple cpus mask, contains all",
			ids:    []ID{0, 1, 2, 3, 4, 5, 6, 7},
			test:   []ID{0, 3, 4, 7},
			result: true,
		},
		{
			name:   "multiple cpus mask, contains any",
			ids:    []ID{0, 1, 3, 4, 7},
			test:   []ID{2, 5, 6, 111, 112, 63, 42, 7},
			result: true,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var (
				mask   = NewCpuMask(tc.ids...)
				result bool
			)
			switch {
			case strings.Contains(tc.name, "all"):
				result = mask.Contains(tc.test...)
			case strings.Contains(tc.name, "any"):
				result = mask.ContainsAny(tc.test...)
			default:
				panic("invalid test case")
			}
			require.Equal(t, tc.result, result)
		})
	}
}

func TestCpuMaskAndOr(t *testing.T) {
	type testCase struct {
		name   string
		ids    []ID
		other  []ID
		result string
	}
	for _, tc := range []*testCase{
		{
			name:   "empty mask, AND empty",
			ids:    []ID{},
			other:  []ID{},
			result: "cpumask{}",
		},
		{
			name:   "empty mask, OR empty",
			ids:    []ID{},
			other:  []ID{},
			result: "cpumask{}",
		},
		{
			name:   "empty mask, AND single",
			ids:    []ID{},
			other:  []ID{0},
			result: "cpumask{}",
		},
		{
			name:   "nil mask, OR single",
			ids:    nil,
			other:  []ID{0},
			result: "cpumask{0}",
		},
		{
			name:   "empty mask, OR single",
			ids:    []ID{},
			other:  []ID{0},
			result: "cpumask{0}",
		},
		{
			name:   "single mask, AND empty",
			ids:    []ID{0},
			other:  []ID{},
			result: "cpumask{}",
		},
		{
			name:   "single mask, OR empty",
			ids:    []ID{0},
			other:  []ID{},
			result: "cpumask{0}",
		},
		{
			name:   "single mask, AND single",
			ids:    []ID{0},
			other:  []ID{0},
			result: "cpumask{0}",
		},
		{
			name:   "single mask, OR single",
			ids:    []ID{0},
			other:  []ID{0},
			result: "cpumask{0}",
		},
		{
			name:   "single mask, AND different single",
			ids:    []ID{0},
			other:  []ID{1},
			result: "cpumask{}",
		},
		{
			name:   "single mask, OR different single",
			ids:    []ID{0},
			other:  []ID{1},
			result: "cpumask{0-1}",
		},
		{
			name:   "single mask, AND different multiple",
			ids:    []ID{0},
			other:  []ID{1, 2, 3},
			result: "cpumask{}",
		},
		{
			name:   "single mask, OR different multiple",
			ids:    []ID{0},
			other:  []ID{1, 2, 3},
			result: "cpumask{0-3}",
		},
		{
			name:   "single mask, AND same multiple",
			ids:    []ID{0},
			other:  []ID{0, 1, 2, 3},
			result: "cpumask{0}",
		},
		{
			name:   "single mask, OR same multiple",
			ids:    []ID{0},
			other:  []ID{0, 1, 2, 3},
			result: "cpumask{0-3}",
		},
		{
			name:   "multiple words, AND multiple words",
			ids:    []ID{0, 1, 2, 3, 62, 63, 64, 100, 101, 102},
			other:  []ID{0, 2, 62, 63, 64, 102},
			result: "cpumask{0,2,62-64,102}",
		},
		{
			name:   "multiple words, OR multiple words",
			ids:    []ID{0, 3, 62, 101},
			other:  []ID{1, 2, 63, 64, 102, 103},
			result: "cpumask{0-3,62-64,101-103}",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var (
				mask  = NewCpuMask(tc.ids...)
				other = NewCpuMask(tc.other...)
			)
			switch {
			case strings.Contains(tc.name, "AND"):
				mask.And(other)
			case strings.Contains(tc.name, "OR"):
				mask.Or(other)
			default:
				panic("invalid test case")
			}
			require.Equal(t, tc.result, mask.String())
		})
	}
}

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

package libcpu

import "fmt"

// CpuUsage is a CPU usage a caller declares. The accounting copies it and
// never retains it. A nil Shared or Exclusive mask means the empty set.
//
// Three shapes are accepted: Exclusive alone, with no Charge; Shared alone,
// with a Charge of 0 or more; or both, with a Charge above 0. A Charge without
// Shared is refused. The package documentation explains the shapes.
type CpuUsage struct {
	ID        string
	Name      string
	Exclusive *CpuMask
	Shared    *CpuMask
	Charge    int
}

// Change is a CPU assignment the caller must put into effect: the CPUs a user
// should now be pinned to. It cannot be fed back into the accounting.
type Change struct {
	id   string
	name string
	cpus *CpuMask
}

func (c *Change) ID() string   { return c.id }
func (c *Change) Name() string { return c.name }

// Cpus returns a copy of the CPUs this user should be pinned to: what its pool
// has left, plus what it holds exclusively.
func (c *Change) Cpus() *CpuMask { return c.cpus.Clone() }

// CapacityError reports that a usage does not fit.
type CapacityError struct {
	Lacking int // milli-CPU lacking
}

func (e *CapacityError) Error() string {
	return fmt.Sprintf("insufficient CPU capacity, lacking %dm", e.Lacking)
}

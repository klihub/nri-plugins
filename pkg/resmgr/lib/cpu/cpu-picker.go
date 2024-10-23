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

import (
	"fmt"

	"github.com/containers/nri-plugins/pkg/cpuallocator"
	"github.com/containers/nri-plugins/pkg/sysfs"
)

// CpuPicker provides functions for picking CPUs from a source set.
type CpuPicker interface {
	// PickCpus picks the given number of CPUs from the source CPU set.
	// If commit is true, the picked CPUs are cleared from the source.
	PickCpus(src CpuMask, cnt int, commit bool) (CpuMask, error)
}

// WithCpuPicker returns an option to set up an allocator with a CPU picker.
func WithCpuPicker(p CpuPicker) AllocatorOption {
	return func(a *Allocator) error {
		a.picker = p
		return nil
	}
}

// WithDefaultCpuPicker returns an option to set up an allocator with the
// default CPU picker, which uses pkg/cpuallocator.CPUAllocator.
func WithDefaultCpuPicker(sys sysfs.System) AllocatorOption {
	return WithCpuPicker(NewDefaultCpuPicker(sys))
}

type defaultCpuPicker struct {
	cpuallocator.CPUAllocator
}

// NewDefaultCpuPicker returns the default CPU allocator as a CPU picker.
// This picker is HW topology aware and tries to pick CPUs topologically
// close to each other.
func NewDefaultCpuPicker(sys sysfs.System) CpuPicker {
	return defaultCpuPicker{
		CPUAllocator: cpuallocator.NewCPUAllocator(sys),
	}
}

func (p defaultCpuPicker) PickCpus(cpus CpuMask, cnt int, commit bool) (CpuMask, error) {
	from := cpus.CPUSet()
	cset, err := p.AllocateCpus(&from, cnt, cpuallocator.PriorityNone)

	if err != nil {
		return NewCpuMask(), fmt.Errorf("failed to pick %d CPUs from %s: %v",
			cnt, cpus.String(), err)
	}

	if commit {
		cpus.Clear(cset.UnsortedList()...)
	}

	return NewCpuMaskForCPUSet(cset), nil
}

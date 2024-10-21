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

// Request represents a CPU allocation request.
type Request struct {
	id        string  // unique ID for the request, typically a container ID
	name      string  // an optional, user provided name for the request
	bounding  CpuMask // bounding set to allocate CPUs from
	exclusive int     // number of exclusive CPUs to allocate
	shared    int     // amount of shared CPU to allocate, in milli-CPUs
	priority  Priority
	private   CpuMask // exclusively assigned CPUs
	common    CpuMask // shared pool of CPUs
}

type Priority int16

const (
	NoPriority  Priority = 0
	Preserved   Priority = (1 << 15) - 2
	Reservation Priority = (1 << 15) - 1
)

func NewRequest(id, name string, bounding CpuMask, exclusive, shared int, prio Priority) *Request {
	return &Request{
		id:        id,
		name:      name,
		bounding:  bounding,
		exclusive: exclusive,
		shared:    shared,
		priority:  prio,
	}
}

func PreservedContainer(id, name string, pool CpuMask, preserve int) *Request {
	return NewRequest(id, name, pool, 0, preserve, Preserved)
}

func ReservedCPUs(id, name string, exclusive CpuMask, shared int, pool CpuMask) *Request {
	return NewRequest(id, name, pool.Clone().Or(exclusive), exclusive.Size(), shared, Reservation)
}

func (r *Request) ID() string {
	return r.id
}

func (r *Request) Name() string {
	if r.name != "" {
		return r.name
	} else {
		return "ID:#" + r.id
	}
}

func (r *Request) Bounding() CpuMask {
	return r.bounding.Clone()
}

func (r *Request) Exclusive() int {
	return r.exclusive
}

func (r *Request) Shared() int {
	return r.shared
}

func (r *Request) Priority() Priority {
	return r.priority
}

func (r *Request) Private() CpuMask {
	return r.private.Clone()
}

func (r *Request) Common() CpuMask {
	return r.common.Clone()
}

func (r *Request) String() string {
	var (
		kind string
		cpus string
	)

	switch {
	case r.Exclusive() != 0 && r.Shared() != 0:
		kind = "mixed"
		cpus = fmt.Sprintf(", %d+%dm cores", r.Exclusive(), r.Shared())
	case r.Exclusive() != 0:
		kind = "exclusive"
		cpus = fmt.Sprintf(", %d cores", r.Exclusive())
	case r.Shared() != 0:
		kind = "shared"
		cpus = fmt.Sprintf(", %d cores", r.Shared())
	default:
		kind = "best-effort"
	}

	return kind + " CPU request<" + r.Name() + cpus + ">"
}

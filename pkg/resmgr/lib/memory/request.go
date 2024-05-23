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

package libmem

import (
	"fmt"
	"math"
	"strconv"
	"strings"
	"sync/atomic"
)

// Request represents a memory allocation request.
type Request struct {
	id       string   // unique ID for the request, typically a container ID
	name     string   // an optional, user provided name for the request
	limit    int64    // the amount of memory to allocate
	types    TypeMask // types of nodes to use for fulfilling the request
	affinity NodeMask // nodes to start allocating memory from
	inertia  Inertia  // larger inertia results in more reluctance to move a request
	zone     NodeMask // the nodes allocated for the request, ideally == affinity
}

// Inertia is used to tell the allocator how reluctantly it should move allocations
type Inertia int

const (
	NoInertia Inertia = iota
	Burstable
	Guaranteed
	Preserved
	reservation
	BestEffort = NoInertia
)

// RequestOption is an option to apply to a request.
type RequestOption func(*Request)

// Container is a convenience function to create a container memory allocation request.
func Container(id, name string, limit int64, qos string, affin NodeMask, types TypeMask) *Request {
	opts := []RequestOption{
		WithName(name),
		WithQosClass(qos),
		WithRequiredTypes(types),
	}
	return NewRequest(id, limit, affin, opts...)
}

// PreserveContainer is a convenience function to create a container with 'preserved'
// memory. Such a container has higher inertia than other containers. The allocator
// tries harder not to move such containers once they have been allocated.
func PreserveContainer(id, name string, limit int64, affin NodeMask, types TypeMask) *Request {
	opts := []RequestOption{
		WithName(name),
		WithInertia(Preserved),
		WithRequiredTypes(types),
	}
	return NewRequest(id, limit, affin, opts...)
}

// ReserveMemory is a convenience function to reserve memory from the allocator.
// Once memory has been succesfully reserved, it is never moved by the allocator.
// ReserveMemory can be used the inform the allocator about memory allocations
// which are beyond the control of the caller.
func ReserveMemory(limit int64, nodes NodeMask, options ...RequestOption) *Request {
	var (
		id   = NewID()
		name = "memory reservation #" + id
		opts = append([]RequestOption{WithName(name)}, options...)
	)
	return NewRequest(id, limit, nodes, append(opts, WithInertia(reservation))...)
}

// WithRequiredTypes returns an option to set the memory type of a request.
func WithRequiredTypes(types TypeMask) RequestOption {
	return func(r *Request) {
		r.types = types
	}
}

// WithInertia returns an option to set the inertia of a request.
func WithInertia(i Inertia) RequestOption {
	return func(r *Request) {
		r.inertia = i
	}
}

// WithQosClass returns an option to set the inertia of a request based on a container QoS class.
func WithQosClass(qosClass string) RequestOption {
	switch strings.ToLower(qosClass) {
	case "besteffort":
		return WithInertia(NoInertia)
	case "burstable":
		return WithInertia(Burstable)
	case "guaranteed":
		return WithInertia(Guaranteed)
	default:
		return WithInertia(NoInertia)
	}
}

// WithName returns an option for setting the name of a request.
func WithName(name string) RequestOption {
	return func(r *Request) {
		r.name = name
	}
}

// NewRequest returns a new request with the given parameters and options.
func NewRequest(id string, limit int64, affinity NodeMask, options ...RequestOption) *Request {
	r := &Request{
		id:       id,
		limit:    limit,
		affinity: affinity,
	}

	if limit > 0 {
		r.inertia = Burstable
	}

	for _, o := range options {
		o(r)
	}

	return r
}

// ID returns the ID of this request.
func (r *Request) ID() string {
	return r.id
}

// Name returns the name of this request.
func (r *Request) Name() string {
	if r.name != "" {
		return r.name
	} else {
		return "ID:#" + r.id
	}
}

// String returns a string representation of this request.
func (r *Request) String() string {
	var (
		kind string
		size = HumanReadableSize(r.Size())
		name = r.Name()
	)

	switch r.inertia {
	case NoInertia:
		kind = "besteffort workload"
	case Burstable:
		kind = "burstable workload"
	case Guaranteed:
		kind = "guaranteed workload"
	case Preserved:
		kind = "preserved workload"
	case reservation:
		kind = "memory reservation"
	}

	if size == "0" {
		size = ""
	} else {
		size = ", size " + size
	}

	return kind + "<" + name + size + ">"
}

// Size returns the allocation size of this request.
func (r *Request) Size() int64 {
	return r.limit
}

// Affinity returns the node affinity of this request. This is the set of nodes
// the allocator will start with to fulfill the allocation request.
func (r *Request) Affinity() NodeMask {
	return r.affinity
}

// Types returns the types of memory for this request.
func (r *Request) Types() TypeMask {
	return r.types
}

// Inertia returns the inertia for this request.
func (r *Request) Inertia() Inertia {
	return r.inertia
}

// Zone returns the allocated memory zone for this request. It is the set of nodes
// the allocator used to fulfill the allocation request.
func (r *Request) Zone() NodeMask {
	return r.zone
}

// String returns a string representation of this inertia.
func (i Inertia) String() string {
	switch i {
	case NoInertia:
		return "NoInertia"
	case Burstable:
		return "Burstable"
	case Guaranteed:
		return "Guaranteed"
	case Preserved:
		return "Preserved"
	case reservation:
		return "reservation"
	}
	return fmt.Sprintf("%%(libmem:BadInertia=%d)", i)
}

var (
	nextID atomic.Int64
)

// NewID returns a new internally unique ID. It is used for memory reservations.
func NewID() string {
	return "request-id-" + strconv.FormatInt(nextID.Add(1), 16)
}

// SortRequestsByInertiaAndSize is a helper to sort requests by increasing
// inertia, size, and request ID in this order.
func SortRequestsByInertiaAndSize(a, b *Request) int {
	if a.Inertia() < b.Inertia() {
		return -1
	}
	if b.Inertia() < a.Inertia() {
		return 1
	}

	if diff := b.Size() - a.Size(); diff < 0 {
		return -1
	} else if diff > 0 {
		return 1
	}

	// TODO(klihub): instead of using ID() add an internal index/age and use
	// that as the last resort for providing a stable sorting order.

	return strings.Compare(a.ID(), b.ID())
}

// HumanReadableSize returns the given size as a human-readable string.
func HumanReadableSize(size int64) string {
	if size >= 1024 {
		units := []string{"k", "M", "G", "T"}

		for i, d := 0, int64(1024); i < len(units); i, d = i+1, d<<10 {
			if val := size / d; 1 <= val && val < 1024 {
				if fval := float64(size) / float64(d); math.Floor(fval) != fval {
					return fmt.Sprintf("%.3g%s", fval, units[i])
				} else {
					return fmt.Sprintf("%d%s", val, units[i])
				}
			}
		}
	}

	return strconv.FormatInt(size, 10)
}

func prettySize(v int64) string {
	return HumanReadableSize(v)
}

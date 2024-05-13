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
	"slices"

	"github.com/containers/nri-plugins/pkg/utils/cpuset"
)

type distance struct {
	vector []int            // distance vector, distances by node ID
	sorted []int            // unique distances sorted in ascending order
	idsets map[int]NodeMask // nodes by distance
}

// Node represents allocatable memory in a NUMA node.
type Node struct {
	id        ID
	kind      Kind
	capacity  int64
	movable   bool
	distance  distance
	closeCPUs cpuset.CPUSet
}

func NewNode(id ID, kind Kind, capacity int64, movable bool, dist []int, closeCPUs cpuset.CPUSet) *Node {
	n := &Node{
		id:        id,
		kind:      kind,
		capacity:  capacity,
		closeCPUs: closeCPUs.Clone(),

		distance: distance{
			vector: slices.Clone(dist),
		},
	}

	return n
}

func (n *Node) ID() ID {
	return n.id
}

func (n *Node) Kind() Kind {
	return n.kind
}

func (n *Node) Capcity() int64 {
	return n.capacity
}

func (n *Node) CloseCPUs() cpuset.CPUSet {
	return n.closeCPUs
}

func (n *Node) IsMovable() bool {
	return n.movable
}

func (n *Node) Distance() []int {
	return slices.Clone(n.distance.vector)
}

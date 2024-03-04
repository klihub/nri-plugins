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

// Node represents allocatable memory in a NUMA node.
type Node struct {
	id         ID
	kind       Kind
	capacity   int64
	movable    bool
	distance   []int
	closeCPUs  cpuset.CPUSet
	byDistance map[Kind][][]ID
	order      []IDSet
	fallback   [][]ID
}

func NewNode(id ID, kind Kind, capacity int64, movable bool, dist []int, closeCPUs cpuset.CPUSet, fallback [][]ID) *Node {
	n := &Node{
		id:        id,
		kind:      kind,
		capacity:  capacity,
		distance:  slices.Clone(dist),
		closeCPUs: closeCPUs.Clone(),
		fallback:  slices.Clone(fallback),
	}
	for i, fbids := range n.fallback {
		n.fallback[i] = slices.Clone(fbids)
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
	return slices.Clone(n.distance)
}

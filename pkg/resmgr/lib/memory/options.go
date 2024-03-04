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
	"slices"

	"github.com/containers/nri-plugins/pkg/sysfs"
)

// AllocatorOption is an option for an allocator.
type AllocatorOption func(*Allocator) error

// WithNodes is an option to set the nodes an allocator can use.
func WithNodes(nodes []*Node) AllocatorOption {
	return func(a *Allocator) error {
		if len(a.nodes) != 0 {
			return fmt.Errorf("failed to set allocator nodes, already set")
		}

		nodeCnt := len(nodes)
		for _, n := range nodes {
			if _, ok := a.nodes[n.id]; ok {
				return fmt.Errorf("failed to set allocator nodes, duplicate node %v", n.id)
			}

			if distCnt := len(n.distance); distCnt < nodeCnt {
				return fmt.Errorf("%d distances set for node #%v, >= %d expected",
					distCnt, n.id, nodeCnt)
			}

			a.nodes[n.id] = n
			a.ids = append(a.ids, n.id)
		}

		return nil
	}
}

// WithSystemNodes is an option to let the allocator discover and pick nodes.
func WithSystemNodes(sys sysfs.System, pickers ...func(sysfs.Node) bool) AllocatorOption {
	return func(a *Allocator) error {
		if len(a.nodes) != 0 {
			return fmt.Errorf("failed to set allocator nodes, already set")
		}

		for _, id := range sys.NodeIDs() {
			sysNode := sys.Node(id)

			picked := len(pickers) == 0
			for _, p := range pickers {
				if p(sysNode) {
					picked = true
					break
				}
			}
			if !picked {
				log.Info("ignoring rejected node #%v...", id)
				continue
			}

			memInfo, err := sysNode.MemoryInfo()
			if err != nil {
				return fmt.Errorf("failed to discover system node #%v: %w", id, err)
			}

			n := &Node{
				id:        sysNode.ID(),
				kind:      TypeToKind(sysNode.GetMemoryType()),
				capacity:  int64(memInfo.MemTotal),
				movable:   !sysNode.HasNormalMemory(),
				distance:  slices.Clone(sysNode.Distance()),
				closeCPUs: sysNode.CPUSet().Clone(),
			}

			a.nodes[id] = n
			a.ids = append(a.ids, id)

			log.Info("discovered %s node %d, %d memory, close CPUs %s",
				n.kind, n.id, n.capacity, n.closeCPUs.String())
		}

		return nil
	}
}

// WithFallbackNodes is an option to manually set up/override fallback nodes for the allocator.
func WithFallbackNodes(fallback map[ID][][]ID) AllocatorOption {
	return func(a *Allocator) error {
		if len(fallback) < len(a.nodes) {
			return fmt.Errorf("failed to set fallback nodes, expected %d entries, got %d",
				len(a.nodes), len(fallback))
		}

		for id, nodeFallback := range fallback {
			n, ok := a.nodes[id]
			if !ok {
				return fmt.Errorf("failed to set fallback nodes, node #%v not found", id)
			}

			n.fallback = slices.Clone(nodeFallback)
			for i, fbids := range nodeFallback {
				n.fallback[i] = slices.Clone(fbids)
			}
		}
		return nil
	}
}

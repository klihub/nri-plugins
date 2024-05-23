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

import "maps"

type MaskCache struct {
	types TypeMask
	nodes struct {
		all       NodeMask
		normal    NodeMask
		movable   NodeMask
		hasMemory NodeMask
		noMemory  NodeMask
		hasCPU    NodeMask
		noCPU     NodeMask
		byTypes   map[TypeMask]NodeMask
	}
}

func NewMaskCache() *MaskCache {
	c := &MaskCache{}
	c.nodes.byTypes = make(map[TypeMask]NodeMask)
	return c
}

func (c *MaskCache) AddNode(n *Node) {
	typeMask := n.memType.Mask()
	nodeMask := n.Mask()

	c.types |= typeMask
	c.nodes.all |= nodeMask
	if n.HasMemory() {
		c.nodes.hasMemory |= nodeMask
		if n.IsNormal() {
			c.nodes.normal |= nodeMask
		} else {
			c.nodes.movable |= nodeMask
		}
		c.nodes.byTypes[typeMask] |= nodeMask
		for _, types := range []TypeMask{
			TypeMaskDRAM | TypeMaskPMEM,
			TypeMaskDRAM | TypeMaskHBM,
			TypeMaskPMEM | TypeMaskHBM,
			TypeMaskAll,
		} {
			if (types & c.types) == types {
				for _, t := range types.Slice() {
					c.nodes.byTypes[types] |= c.nodes.byTypes[t.Mask()]
				}
			}
		}
	} else {
		c.nodes.noMemory |= nodeMask
	}
	if n.HasCPUs() {
		c.nodes.hasCPU |= nodeMask
	} else {
		c.nodes.noCPU |= nodeMask
	}
}

func (c *MaskCache) AvailableTypes() TypeMask {
	return c.types
}

func (c *MaskCache) AvailableNodes() NodeMask {
	return c.nodes.all
}

func (c *MaskCache) NodesWithNormalMem() NodeMask {
	return c.nodes.normal
}

func (c *MaskCache) NodesWithMovableMem() NodeMask {
	return c.nodes.movable
}

func (c *MaskCache) NodesWithMem() NodeMask {
	return c.nodes.hasMemory
}

func (c *MaskCache) NodesWithoutMem() NodeMask {
	return c.nodes.noMemory
}

func (c *MaskCache) NodesByTypes(types TypeMask) NodeMask {
	return c.nodes.byTypes[types]
}

func (c *MaskCache) NodesWithCloseCPUs() NodeMask {
	return c.nodes.hasCPU
}

func (c *MaskCache) NodesWithoutCloseCPUs() NodeMask {
	return c.nodes.noCPU
}

func (c *MaskCache) Clone() *MaskCache {
	n := &MaskCache{
		types: c.types,
		nodes: c.nodes,
	}
	n.nodes.byTypes = maps.Clone(c.nodes.byTypes)
	return n
}

func (c *MaskCache) Log(prefix, header string) {
	c.LogWithLogger(prefix, header, log.Debug)
}

func (c *MaskCache) LogWithLogger(prefix, header string, logger func(string, ...interface{})) {
	logger("%s%s", header, prefix)
	logger("%s  - available types: %s", prefix, c.types)
	logger("%s  - available nodes: %s", prefix, c.nodes.all)
	logger("%s      has memory: %s", prefix, c.nodes.hasMemory)
	for types, nodes := range c.nodes.byTypes {
		logger("%s        %s: %s", prefix, types, nodes)
	}
	logger("%s      no memory: %s", prefix, c.nodes.noMemory)
	logger("%s     has close CPUs: %s", prefix, c.nodes.hasCPU)
	logger("%s      no close CPUs: %s", prefix, c.nodes.noCPU)
}

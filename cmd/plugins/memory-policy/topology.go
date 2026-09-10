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

package main

import (
	"github.com/containers/nri-plugins/pkg/lib/hardware"
	"github.com/containers/nri-plugins/pkg/utils/cpuset"
	idset "github.com/intel/goresctrl/pkg/utils"
)

// idsForCPUs returns the set of ids idOf gives for the given CPUs, skipping
// CPUs the machine does not have.
func idsForCPUs(cpus cpuset.CPUSet, idOf func(*hardware.CPU) idset.ID) idset.IDSet {
	ids := idset.NewIDSet()
	for _, id := range cpus.List() {
		if cpu := machine.CPU(id); cpu.Valid() {
			ids.Add(idOf(cpu))
		}
	}
	return ids
}

// packagesOfNode returns the CPU packages a memory node belongs to. A node
// with CPUs belongs to their package. A node without CPUs, such as HBM, CXL
// or PMEM, belongs to the package of the nearest node with CPUs, or to
// several if they are equally near.
func packagesOfNode(nodeId idset.ID) []idset.ID {
	node := machine.MemoryNode(nodeId)
	if !node.Valid() {
		return nil
	}

	if pkg := node.PackageID(); pkg >= 0 {
		return []idset.ID{pkg}
	}

	groups := hardware.ClosestMemoryNodes(machine, nodeId,
		func(n *hardware.MemoryNode) bool { return n.CPUs().Size() > 0 })
	if len(groups) == 0 {
		return nil
	}

	pkgs := idset.NewIDSet()
	for _, id := range groups[0].Nodes {
		if pkg := machine.MemoryNode(id).PackageID(); pkg >= 0 {
			pkgs.Add(pkg)
		}
	}

	return pkgs.SortedMembers()
}

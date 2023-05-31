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

package sysfs

import (
	"fmt"
	"path"

	"github.com/containers/nri-plugins/pkg/utils/cpuset"
)

// Nodes is an interface for querying availble system memory.
type Nodes interface {
	// System returns the System associated with these Nodes.
	System() System
	// HasCPU returns IDs of the nodes which hav 'local' or 'close' CPUs associated.
	HasCPU() []ID
	// HasMemory returns IDs of the nodes which have memory attached.
	HasMemory() []ID
	// HasMemory returns IDs of the nodes which have normal memory attached.
	HasNormalMemory() []ID
	// Online returns IDs of the nodes which are online.
	Online() []ID
	// Possible returns IDs of all possible nodes in the system.
	Possible() []ID
	// Node returns an interface for querying the node with the given ID.
	Node(ID) Node
	// ReadFile reads a file relative to the node subtree in sysfs.
	ReadFile(file string) ([]byte, error)
}

// Node is an interface for querying a single memory node in the system.
type Node interface {
	// System returns the System associated with this node.
	System() System
	// ID returns the ID of this node.
	ID() ID
	// CPUSet returns the cpuset of 'local' or 'close' CPUs in the system.
	CPUSet() cpuset.CPUSet
	// CPUList returns the CPU IDs of 'local' or 'close' CPUs in the system.
	CPUList() []ID
	// Distance returns the distances of this node from other system nodes.
	Distance() []int
	// MemInfo returns the sysfs meminfo for this node.
	MemInfo() *MemInfo
	// NUMAStat returns the sysfs numastat for this node.
	NUMAStat() *NUMAStat
	// ReadFile reads a file relative to this node in sysfs.
	ReadFile(file string) ([]byte, error)
}

// sysNodes is our implementation of Nodes.
type sysNodes struct {
	system          *system
	possible        IDSet
	online          IDSet
	hasCPU          IDSet
	hasMemory       IDSet
	hasNormalMemory IDSet
	nodes           map[ID]*sysNode
}

// sysNode is our implementation of Node.
type sysNode struct {
	system   *system
	id       ID
	dir      string
	cpus     cpuset.CPUSet
	distance []int
	memInfo  *MemInfo
	numaStat *NUMAStat
}

// MemInfo represents data read from a sysfs meminfo entry.
type MemInfo struct {
	MemTotal        uint64
	MemFree         uint64
	MemUsed         uint64
	SwapCached      uint64
	Active          uint64
	Inactive        uint64
	ActiveAnon      uint64
	InactiveAnon    uint64
	ActiveFile      uint64
	InactiveFile    uint64
	Unevictable     uint64
	Mlocked         uint64
	Dirty           uint64
	Writeback       uint64
	FilePages       uint64
	Mapped          uint64
	AnonPages       uint64
	Shmem           uint64
	KernelStack     uint64
	PageTables      uint64
	SecPageTables   uint64
	NFS_Unstable    uint64
	Bounce          uint64
	WritebackTmp    uint64
	KReclaimable    uint64
	Slab            uint64
	SReclaimable    uint64
	SUnreclaim      uint64
	AnonHugePages   uint64
	FileHugePages   uint64
	FilePmdMapped   uint64
	ShmemHugePages  uint64
	ShmemPmdMapped  uint64
	HugePages_Total uint64
	HugePages_Free  uint64
	HugePages_Surp  uint64
}

// NUMAStat represents data read from a sysfs numastat entry.
type NUMAStat struct {
	NUMAHit       uint64
	NUMAMiss      uint64
	NUMAForeign   uint64
	InterleaveHit uint64
	LocalNode     uint64
	OtherNode     uint64
}

func (s *system) readSysNodes() error {
	s.nodes = &sysNodes{
		system: s,
		nodes:  map[ID]*sysNode{},
	}
	return s.nodes.refresh()
}

func (s *sysNodes) System() System {
	return s.system
}

func (s *sysNodes) HasCPU() []ID {
	return s.hasCPU.SortedMembers()
}

func (s *sysNodes) HasMemory() []ID {
	return s.hasMemory.SortedMembers()
}

func (s *sysNodes) HasNormalMemory() []ID {
	return s.hasNormalMemory.SortedMembers()
}

func (s *sysNodes) Online() []ID {
	return s.online.SortedMembers()
}

func (s *sysNodes) Possible() []ID {
	return s.possible.SortedMembers()
}

func (s *sysNodes) Node(id ID) Node {
	if n, ok := s.nodes[id]; ok {
		return n
	}
	return &sysNode{
		system:   s.system,
		id:       UnknownID,
		memInfo:  &MemInfo{},
		numaStat: &NUMAStat{},
	}
}

func (n *sysNodes) ReadFile(file string) ([]byte, error) {
	return n.system.SysFS().ReadFile(path.Join("sys/devices/system/node", file))
}

func (s *sysNodes) refresh() error {
	const (
		possible        = "sys/devices/system/node/possible"
		online          = "sys/devices/system/node/online"
		hasCPU          = "sys/devices/system/node/has_cpu"
		hasMemory       = "sys/devices/system/node/has_memory"
		hasNormalMemory = "sys/devices/system/node/has_normal_memory"
	)

	log.Debugf("refreshing sysfs nodes...")

	for entry, isetp := range map[string]*IDSet{
		possible:        &s.possible,
		online:          &s.online,
		hasCPU:          &s.hasCPU,
		hasMemory:       &s.hasMemory,
		hasNormalMemory: &s.hasNormalMemory,
	} {
		err := s.system.fs.ParseLine(entry, idsetParser(isetp))
		if err != nil {
			return err
		}
	}

	for _, id := range s.possible.SortedMembers() {
		n, ok := s.nodes[id]
		if !ok {
			n = &sysNode{
				system:   s.system,
				id:       id,
				dir:      fmt.Sprintf("sys/devices/system/node/node%d", id),
				memInfo:  &MemInfo{},
				numaStat: &NUMAStat{},
			}
			s.nodes[id] = n
		}
		err := n.refresh()
		if err != nil {
			return err
		}
	}

	return nil
}

func (n *sysNode) System() System {
	return n.system
}

func (n *sysNode) ID() ID {
	return n.id
}

func (n *sysNode) CPUSet() cpuset.CPUSet {
	return n.cpus
}

func (n *sysNode) CPUList() []ID {
	return n.cpus.List()
}

func (n *sysNode) Distance() []int {
	return n.distance
}

func (n *sysNode) MemInfo() *MemInfo {
	memInfo := *n.memInfo
	return &memInfo
}

func (n *sysNode) NUMAStat() *NUMAStat {
	numaStat := *n.numaStat
	return &numaStat
}

func (n *sysNode) ReadFile(file string) ([]byte, error) {
	return n.system.SysFS().ReadFile(path.Join(n.dir, file))
}

func (n *sysNode) refresh() error {
	var err error

	log.Debug("refreshing sysfs node #%d...", n.id)

	if n.cpus.Size() == 0 {
		err = n.system.fs.ParseLine(n.dir+"/cpulist", cpusetParser(&n.cpus))
		if err != nil {
			return err
		}
	}

	if n.distance == nil {
		err = n.system.fs.ParseLine(n.dir+"/distance", intSliceParser(&n.distance, " "))
		if err != nil {
			return err
		}
	}

	err = n.system.fs.ParseText(n.dir+"/meminfo", meminfoParser(n.memInfo))
	if err != nil {
		return err
	}

	err = n.system.fs.ParseText(n.dir+"/numastat", numastatParser(n.numaStat))
	if err != nil {
		return err
	}

	return nil
}

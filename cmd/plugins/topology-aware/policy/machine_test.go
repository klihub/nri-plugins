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

package topologyaware

import (
	"fmt"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/containers/nri-plugins/pkg/lib/hardware"
	"github.com/containers/nri-plugins/pkg/utils/cpuset"
)

// synthNode describes one NUMA node of a test machine. A node with CPUs is
// DRAM. A node with memory and no CPUs gets its kind from its size against
// the DRAM nodes: larger is PMEM, smaller is HBM.
type synthNode struct {
	cpus     string // cpulist, empty for a node with no CPUs of its own
	memKB    int    // MemTotal in kB
	distance []int  // distance to every node, this one included
}

// synthMachine builds a machine from the given nodes by writing them out as
// sysfs and discovering it. All CPUs are single-threaded cores, with one
// package per node with CPUs.
func synthMachine(t *testing.T, nodes []synthNode) *hardware.Machine {
	t.Helper()

	file := func(s string) *fstest.MapFile { return &fstest.MapFile{Data: []byte(s)} }
	fsys := fstest.MapFS{}

	var (
		online = cpuset.New()
		normal = cpuset.New()
		pkg    = 0
	)
	for id, node := range nodes {
		dir := fmt.Sprintf("sys/devices/system/node/node%d", id)

		cpus := cpuset.New()
		if node.cpus != "" {
			var err error
			if cpus, err = cpuset.Parse(node.cpus); err != nil {
				t.Fatalf("node%d: bad cpulist %q: %v", id, node.cpus, err)
			}
		}
		online = online.Union(cpus)

		fsys[dir+"/cpulist"] = file(node.cpus + "\n")
		fsys[dir+"/meminfo"] = file(
			fmt.Sprintf("Node %d MemTotal: %d kB\n", id, node.memKB))

		if node.memKB > 0 {
			normal = normal.Union(cpuset.New(id))
		}

		dist := make([]string, 0, len(node.distance))
		for _, d := range node.distance {
			dist = append(dist, fmt.Sprintf("%d", d))
		}
		fsys[dir+"/distance"] = file(strings.Join(dist, " ") + "\n")

		if cpus.IsEmpty() {
			continue
		}
		for _, cpu := range cpus.List() {
			topo := fmt.Sprintf("sys/devices/system/cpu/cpu%d/topology", cpu)
			fsys[topo+"/physical_package_id"] = file(fmt.Sprintf("%d\n", pkg))
			fsys[topo+"/core_id"] = file(fmt.Sprintf("%d\n", cpu))
			fsys[topo+"/core_cpus_list"] = file(fmt.Sprintf("%d\n", cpu))
		}
		pkg++
	}

	all := online.String()

	// Mark every node with memory as having normal memory. Otherwise it
	// reads as movable-only, leaving an allocator no memory.
	fsys["sys/devices/system/node/has_normal_memory"] = file(normal.String() + "\n")

	fsys["proc/meminfo"] = file("MemTotal: 1048576 kB\n")
	fsys["sys/devices/system/cpu/online"] = file(all + "\n")
	fsys["sys/devices/system/cpu/present"] = file(all + "\n")
	fsys["sys/devices/system/cpu/possible"] = file(all + "\n")

	m, err := hardware.Discover(hardware.WithFS(fsys))
	if err != nil {
		t.Fatalf("failed to discover the test machine: %v", err)
	}
	return m
}

// oneCpuMachine is the smallest machine: one CPU, one node, one package.
func oneCpuMachine(t *testing.T) *hardware.Machine {
	t.Helper()
	return synthMachine(t, []synthNode{
		{cpus: "0", memKB: 1048576, distance: []int{10}},
	})
}

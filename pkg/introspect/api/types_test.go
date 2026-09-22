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

package api_test

import (
	"encoding/json"
	"reflect"
	"sort"
	"testing"
	"time"

	"github.com/containers/nri-plugins/pkg/introspect/api"
)

// populated returns a snapshot with every field set, so that a renamed
// or dropped JSON tag shows up as a key-set mismatch below.
//
// Three fields are left at their zero value on purpose, so that the
// expected key sets also prove omitempty works: CPU.Isolated,
// Hardware.OfflineCPUs and Assignment.SharedCPUs and its neighbours.
func populated() *api.Snapshot {
	return &api.Snapshot{
		Version:    api.Version,
		Generation: 42,
		Timestamp:  time.Date(2026, 9, 22, 12, 0, 0, 0, time.UTC),
		Node: api.Node{
			Name:    "worker-1",
			Policy:  "topology-aware",
			Version: "v0.10.0",
		},
		Config: &api.Config{
			Name:            "node.worker-1",
			Namespace:       "kube-system",
			Generation:      7,
			ResourceVersion: "12345",
			Data:            json.RawMessage(`{"pinCPU":true}`),
		},
		Hardware: &api.Hardware{
			Packages: []*api.Package{
				{
					ID:   0,
					CPUs: "0-7",
					Dies: []*api.Die{
						{
							ID:        0,
							CPUs:      "0-7",
							NUMANodes: []int{0},
							Clusters:  []*api.Cluster{{ID: 0, CPUs: "0-3"}},
						},
					},
				},
			},
			NUMANodes: []*api.NUMANode{
				{
					ID:           0,
					Package:      0,
					Die:          0,
					CPUs:         "0-7",
					MemoryType:   "DRAM",
					NormalMemory: true,
					Capacity:     34359738368,
					Distance:     []int{10, 21},
				},
			},
			CPUs: []*api.CPU{
				{
					ID:       0,
					Package:  0,
					Die:      0,
					Cluster:  0,
					NUMANode: 0,
					Core:     0,
					Threads:  "0,4",
					CoreKind: "P-core",
					Online:   true,
					Isolated: false,
					BaseFreq: 2400000,
					MinFreq:  800000,
					MaxFreq:  3600000,
				},
			},
			IsolatedCPUs: "6-7",
			OfflineCPUs:  "",
			CoreKinds:    map[string]string{"P-core": "0-7"},
		},
		Pods: []*api.Pod{
			{
				ID:        "pod-cri-id",
				UID:       "pod-uid",
				Name:      "web",
				Namespace: "default",
				QOSClass:  "Guaranteed",
			},
		},
		Containers: []*api.Container{
			{
				ID:            "ctr-cri-id",
				PodID:         "pod-cri-id",
				Name:          "nginx",
				State:         "running",
				QOSClass:      "Guaranteed",
				CPURequest:    4000,
				CPULimit:      4000,
				MemoryRequest: 2147483648,
				MemoryLimit:   2147483648,
				Applied: &api.Applied{
					CpusetCpus:  "0-3",
					CpusetMems:  "0",
					CPUShares:   4096,
					CPUQuota:    400000,
					CPUPeriod:   100000,
					MemoryLimit: 2147483648,
				},
				Classes: map[string]string{"rdt": "guaranteed"},
				Hints: []*api.Hint{
					{
						Provider: "/sys/class/net/eth0",
						CPUs:     "0-3",
						NUMAs:    "0",
						Sockets:  "0",
						IRQs:     []int{128},
					},
				},
			},
		},
		Derived: &api.Derived{
			CPUCommitment:    map[int]int64{0: 1000},
			CPUSharers:       map[int]int{0: 1},
			MemoryCommitment: map[int]int64{0: 2147483648},
		},
		Policy: &api.PolicyState{
			Name: "topology-aware",
			Zones: []*api.Zone{
				{
					Name:   "numa node #0",
					Type:   "numa node",
					Parent: "socket #0",
					CPUs:   "0-7",
					Memory: "0",
					Resources: []*api.ZoneResource{
						{
							Name:        "cpu",
							Unit:        "milliCPU",
							Capacity:    8000,
							Allocatable: 8000,
							Available:   4000,
						},
					},
					Attributes: map[string]string{"shared cpuset": "4-7"},
				},
			},
			Assignments: []*api.Assignment{
				{
					ContainerID:   "ctr-cri-id",
					Zone:          "numa node #0",
					ExclusiveCPUs: "0-3",
					SharedCPUs:    "",
					IsolatedCPUs:  "",
					ReservedCPUs:  "",
					CPUShare:      0,
					MemoryZone:    "0",
					MemoryTypes:   []string{"DRAM"},
					Attributes:    map[string]string{"cold start": "false"},
				},
			},
			Notices: []*api.Notice{
				{
					Severity: "info",
					Message:  "CPU pinning is disabled",
					Detail:   "pinCPU: false",
				},
			},
			Attributes: map[string]string{"allocations": "1"},
			Data:       json.RawMessage(`{"cpuTree":{}}`),
		},
	}
}

func keys(t *testing.T, v any) []string {
	t.Helper()

	data, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("failed to marshal: %v", err)
	}

	var m map[string]json.RawMessage
	if err := json.Unmarshal(data, &m); err != nil {
		t.Fatalf("failed to unmarshal into a map: %v", err)
	}

	k := make([]string, 0, len(m))
	for name := range m {
		k = append(k, name)
	}
	sort.Strings(k)

	return k
}

func TestSnapshotJSONKeys(t *testing.T) {
	snap := populated()

	for _, tc := range []struct {
		name     string
		value    any
		expected []string
	}{
		{
			name:  "snapshot",
			value: snap,
			expected: []string{
				"config", "containers", "derived", "generation", "hardware",
				"node", "pods", "policy", "timestamp", "version",
			},
		},
		{
			name:     "node",
			value:    snap.Node,
			expected: []string{"name", "policy", "version"},
		},
		{
			name:  "config",
			value: snap.Config,
			expected: []string{
				"data", "generation", "name", "namespace", "resourceVersion",
			},
		},
		{
			name:  "hardware",
			value: snap.Hardware,
			expected: []string{
				"coreKinds", "cpus", "isolatedCpus", "numaNodes", "packages",
			},
		},
		{
			name:     "package",
			value:    snap.Hardware.Packages[0],
			expected: []string{"cpus", "dies", "id"},
		},
		{
			name:     "die",
			value:    snap.Hardware.Packages[0].Dies[0],
			expected: []string{"clusters", "cpus", "id", "numaNodes"},
		},
		{
			name:  "numaNode",
			value: snap.Hardware.NUMANodes[0],
			expected: []string{
				"capacity", "cpus", "die", "distance", "id", "memoryType",
				"normalMemory", "package",
			},
		},
		{
			name:  "cpu",
			value: snap.Hardware.CPUs[0],
			expected: []string{
				"baseFreq", "cluster", "core", "coreKind", "die", "id",
				"maxFreq", "minFreq", "numaNode", "online", "package",
				"threads",
			},
		},
		{
			name:     "pod",
			value:    snap.Pods[0],
			expected: []string{"id", "name", "namespace", "qosClass", "uid"},
		},
		{
			name:  "container",
			value: snap.Containers[0],
			expected: []string{
				"applied", "classes", "cpuLimit", "cpuRequest", "hints", "id",
				"memoryLimit", "memoryRequest", "name", "podId", "qosClass",
				"state",
			},
		},
		{
			name:  "applied",
			value: snap.Containers[0].Applied,
			expected: []string{
				"cpuPeriod", "cpuQuota", "cpuShares", "cpusetCpus",
				"cpusetMems", "memoryLimit",
			},
		},
		{
			name:     "hint",
			value:    snap.Containers[0].Hints[0],
			expected: []string{"cpus", "irqs", "numas", "provider", "sockets"},
		},
		{
			name:     "derived",
			value:    snap.Derived,
			expected: []string{"cpuCommitment", "cpuSharers", "memoryCommitment"},
		},
		{
			name:  "policy",
			value: snap.Policy,
			expected: []string{
				"assignments", "attributes", "data", "name", "notices", "zones",
			},
		},
		{
			name:  "zone",
			value: snap.Policy.Zones[0],
			expected: []string{
				"attributes", "cpus", "memory", "name", "parent", "resources",
				"type",
			},
		},
		{
			name:  "zoneResource",
			value: snap.Policy.Zones[0].Resources[0],
			expected: []string{
				"allocatable", "available", "capacity", "name", "unit",
			},
		},
		{
			name:  "assignment",
			value: snap.Policy.Assignments[0],
			expected: []string{
				"attributes", "containerId", "exclusiveCpus", "memoryTypes",
				"memoryZone", "zone",
			},
		},
		{
			name:     "notice",
			value:    snap.Policy.Notices[0],
			expected: []string{"detail", "message", "severity"},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := keys(t, tc.value)
			if !reflect.DeepEqual(got, tc.expected) {
				t.Errorf("JSON keys mismatch\n  got:      %v\n  expected: %v",
					got, tc.expected)
			}
		})
	}
}

func TestSnapshotRoundTrip(t *testing.T) {
	snap := populated()

	data, err := json.Marshal(snap)
	if err != nil {
		t.Fatalf("failed to marshal: %v", err)
	}

	decoded := &api.Snapshot{}
	if err := json.Unmarshal(data, decoded); err != nil {
		t.Fatalf("failed to unmarshal: %v", err)
	}

	if !reflect.DeepEqual(snap, decoded) {
		t.Errorf("round trip changed the value\n  before: %+v\n  after:  %+v",
			snap, decoded)
	}
}

func TestVersion(t *testing.T) {
	if api.Version != "v1alpha1" {
		t.Errorf("unexpected version %q", api.Version)
	}
}

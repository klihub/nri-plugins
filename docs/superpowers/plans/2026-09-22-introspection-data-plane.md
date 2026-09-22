# Introspection Data Plane Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Serve a complete, policy-agnostic JSON snapshot of a node's resource placement state over the plugin's existing HTTP endpoint, with change notification, off by default.

**Architecture:** A dependency-free types package defines the wire format. A collector in `pkg/resmgr/introspect` builds a snapshot from `pkg/sysfs` and the resource manager cache, with no policy involvement. A small server caches the marshalled snapshot, invalidates it at the four points where placement can change, and pushes generation numbers to Server-Sent Events subscribers so clients refetch only when something moved.

**Tech Stack:** Go 1.26, the repo's own `pkg/http.ServeMux`, `pkg/sysfs`, `pkg/resmgr/cache`, ginkgo/`go test`, controller-gen for CRDs, Helm, the e2e VM framework.

**Spec:** `docs/superpowers/specs/2026-09-22-introspection-visualization-design.md`

**Scope:** This plan implements spec increments 1 and 2 only — the data plane. It ends with `curl :8891/introspect | jq` returning a full snapshot and an SSE stream that ticks when placement changes. The visualization (increment 3) and the policy-provided layer 2 (increments 4 and 5) each get their own plan. The `api` package does define the layer-2 types, because they are part of the published `v1alpha1` contract, but nothing in this plan populates them.

## Global Constraints

- Module path: `github.com/containers/nri-plugins`.
- Every new file starts with the current license header, copied verbatim from `pkg/resmgr/dra.go`: `// Copyright The NRI Plugins Authors. All Rights Reserved.` followed by the Apache 2.0 block.
- `pkg/introspect/api` uses the **standard library only**. No k8s apimachinery, no `resource.Quantity`, no repo-internal imports. Any import beyond `encoding/json` and `time` in that package is a defect.
- Units in the wire format are fixed: **CPU quantities are milli-CPU, memory quantities are bytes, CPU and memory node sets are Linux list-format strings** such as `"0-3,8"`.
- Introspection is **disabled by default**. With it disabled, no handler is registered at all.
- Container command lines are never exposed. Do not add an `Args` field.
- `testdata/` is gitignored repo-wide (see `.gitignore`), so test fixtures are either in-test literals or extracted from a committed tarball. Do not expect to commit a file under any `testdata/` directory.
- Commit messages: subject line, blank line, body wrapped at 60 columns, ending with `Signed-off-by: Krisztian Litkey <krisztian.litkey@intel.com>` and `Co-Authored-By: Claude Opus 5 (1M context) <noreply@anthropic.com>`.
- Run `gofmt -l` over touched files before each commit; the repo has no tolerance for unformatted Go.

## File Structure

| File | Responsibility |
|---|---|
| `pkg/introspect/api/types.go` | the wire format. Types and the `Version` constant, nothing else. |
| `pkg/introspect/api/types_test.go` | JSON key and round-trip tests that pin field names. |
| `pkg/resmgr/introspect/collect.go` | `Collect()` and the narrow `Cache`/`Pod`/`Container` interfaces it consumes. Hardware, workload, config and derived sections. |
| `pkg/resmgr/introspect/collect_test.go` | collector tests against the real sysfs fixtures and fake cache objects. |
| `pkg/resmgr/introspect/fake_test.go` | fake `Pod`/`Container`/`Cache` implementations used by the collector tests. |
| `pkg/resmgr/introspect/introspect.go` | the `Server`: enable/disable, snapshot caching, generation counter, `/introspect` handler. |
| `pkg/resmgr/introspect/events.go` | the SSE `/introspect/events` handler and subscriber bookkeeping. |
| `pkg/resmgr/introspect/introspect_test.go` | handler tests, including the disabled case and an SSE subscriber. |
| `pkg/resmgr/introspect/test-setup.sh`, `test-cleanup.sh` | extract the sysfs fixture shared with `pkg/sysfs`. |
| `pkg/apis/config/v1alpha1/instrumentation/config.go` | modify: add the `Introspection` config section. |
| `pkg/resmgr/resource-manager.go` | modify: create the server, configure it from config, collect under the read lock, `publishNodeState()`. |
| `pkg/resmgr/nri.go` | modify: call `publishNodeState()` at the three NRI invalidation sites. |
| `deployment/helm/*/values.yaml`, `deployment/helm/*/crds/*.yaml` | modify/regenerate: the new config field. |
| `test/e2e/lib/test.bash` | modify: `verify-introspect` helpers. |
| `test/e2e/policies.test-suite/topology-aware/helm-config.yaml.in` | modify: an `INTROSPECTION` knob. |
| `test/e2e/policies.test-suite/topology-aware/n4c16/test42-introspection/code.var.sh` | the e2e case. |

Two boundary decisions worth stating, because they are what make the rest testable:

1. The collector does not take `cache.Cache`. It takes narrow interfaces declared in `pkg/resmgr/introspect` that the real cache types satisfy structurally. A fake in the tests implements a dozen methods instead of the cache's hundred.
2. The server does not know about resmgr. It holds a `CollectFn func() *api.Snapshot`. resmgr supplies a closure that takes its read lock. Handler tests pass a stub.

## Deviations from the spec, and why

- The spec's config snippet shows both `enabled` and `ui`. This plan adds **only `enabled`**, because there is no UI yet and a knob that does nothing is worse than no knob. `ui` arrives with the UI plan.
- The spec's testing section asks for a golden JSON file under `testdata/`. `testdata/` is gitignored repo-wide, so the equivalent guarantee is provided by in-test key-set assertions (Task 1).
- The spec describes commitment as computed by the UI, while its testing section asks for Go-side table-driven tests of the arithmetic. This plan resolves that by putting it in a `derived` section of the snapshot: it is then testable in Go, available to `curl | jq` consumers, and the UI does not have to reimplement it. The section is documented as derived, and a consumer may recompute it.

---

### Task 1: The wire format

**Files:**
- Create: `pkg/introspect/api/types.go`
- Test: `pkg/introspect/api/types_test.go`

**Interfaces:**
- Consumes: nothing.
- Produces: `api.Version`, `api.Snapshot`, `api.Node`, `api.Config`, `api.Hardware`, `api.Package`, `api.Die`, `api.Cluster`, `api.NUMANode`, `api.CPU`, `api.Pod`, `api.Container`, `api.Applied`, `api.Hint`, `api.Derived`, `api.PolicyState`, `api.Zone`, `api.ZoneResource`, `api.Assignment`, `api.Notice`.

- [ ] **Step 1: Write the failing test**

Create `pkg/introspect/api/types_test.go`:

```go
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

// intp returns a pointer to an int, for the optional NUMA node fields.
func intp(i int) *int {
	return &i
}

// populated returns a snapshot with every field set, so that a renamed
// or dropped JSON tag shows up as a key-set mismatch below.
//
// Six fields are left at their zero value on purpose, so that the expected
// key sets also prove omitempty works: Hardware.OfflineCPUs, CPU.Isolated,
// and Assignment's SharedCPUs, IsolatedCPUs, ReservedCPUs and CPUShare.
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
					Package:      intp(0),
					Die:          intp(0),
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

// TestMemoryOnlyNUMANodeKeys pins the case the optional Package and Die
// fields exist for: a memory-only CXL or HBM node belongs to no package,
// and must not claim to belong to package 0.
func TestMemoryOnlyNUMANodeKeys(t *testing.T) {
	node := &api.NUMANode{
		ID:         8,
		CPUs:       "",
		MemoryType: "HBM",
		Capacity:   68719476736,
	}

	got := keys(t, node)
	expected := []string{"capacity", "cpus", "id", "memoryType", "normalMemory"}

	if !reflect.DeepEqual(got, expected) {
		t.Errorf("JSON keys mismatch\n  got:      %v\n  expected: %v",
			got, expected)
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
```

- [ ] **Step 2: Run the test to verify it fails**

Run: `go test ./pkg/introspect/api/... -v`

Expected: FAIL — the package does not compile, because `pkg/introspect/api` does
not exist yet.

- [ ] **Step 3: Write the types**

Create `pkg/introspect/api/types.go`. Field order matters only for readability;
the tests assert on key sets, not order.

```go
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

// Package api defines the wire format of the resource policy introspection
// interface.
//
// Units are fixed rather than encoded per value: CPU quantities are
// milli-CPU, memory quantities are bytes, and CPU and memory node sets are
// Linux list-format strings, for instance "0-3,8".
//
// The Hardware, Pods, Containers, Config and Derived sections are
// policy-agnostic: they are collected without any policy involvement and are
// always present. The Policy section is optional and additive. Its absence
// means the active policy has nothing to say, which is not an error, and no
// policy-agnostic field is ever derived from it.
//
// This package deliberately depends on the standard library only, so that
// external consumers can use it without pulling in the resource manager or
// Kubernetes.
package api

import (
	"encoding/json"
	"time"
)

// Version is the version of this wire format.
const Version = "v1alpha1"

// Snapshot is a complete description of one node's placement state.
type Snapshot struct {
	Version    string       `json:"version"`
	Generation uint64       `json:"generation"`
	Timestamp  time.Time    `json:"timestamp"`
	Node       Node         `json:"node"`
	Config     *Config      `json:"config,omitempty"`
	Hardware   *Hardware    `json:"hardware,omitempty"`
	Pods       []*Pod       `json:"pods,omitempty"`
	Containers []*Container `json:"containers,omitempty"`
	Derived    *Derived     `json:"derived,omitempty"`
	Policy     *PolicyState `json:"policy,omitempty"`
}

// Node identifies the node and the policy running on it.
type Node struct {
	Name    string `json:"name"`
	Policy  string `json:"policy"`
	Version string `json:"version,omitempty"` // plugin build version
}

// Config is the effective configuration the plugin is running with.
//
// Data is the configuration as received, verbatim. Apart from the common
// section, its keys are policy-specific; a consumer that is not
// policy-specific itself should display it rather than interpret it.
type Config struct {
	Name            string          `json:"name,omitempty"`
	Namespace       string          `json:"namespace,omitempty"`
	Generation      int64           `json:"generation,omitempty"`
	ResourceVersion string          `json:"resourceVersion,omitempty"`
	Data            json.RawMessage `json:"data,omitempty"`
}

// Hardware describes the machine.
type Hardware struct {
	Packages     []*Package        `json:"packages,omitempty"`
	NUMANodes    []*NUMANode       `json:"numaNodes,omitempty"`
	CPUs         []*CPU            `json:"cpus,omitempty"`
	IsolatedCPUs string            `json:"isolatedCpus,omitempty"`
	OfflineCPUs  string            `json:"offlineCpus,omitempty"`
	CoreKinds    map[string]string `json:"coreKinds,omitempty"` // kind -> cpuset
}

// Package is a physical CPU package.
type Package struct {
	ID   int    `json:"id"`
	CPUs string `json:"cpus"`
	Dies []*Die `json:"dies,omitempty"`
}

// Die is a die within a package.
type Die struct {
	ID        int        `json:"id"`
	CPUs      string     `json:"cpus"`
	NUMANodes []int      `json:"numaNodes,omitempty"`
	Clusters  []*Cluster `json:"clusters,omitempty"`
}

// Cluster is a CPU cluster within a die.
type Cluster struct {
	ID   int    `json:"id"`
	CPUs string `json:"cpus"`
}

// NUMANode is a NUMA node. A node with no CPUs is a memory-only node.
//
// Package and Die are absent for a node that belongs to neither, which is
// the normal case for memory-only CXL and HBM nodes. They are pointers
// rather than an int with a sentinel value because 0 is a valid package
// and die ID, and because absence is self-describing on the wire.
type NUMANode struct {
	ID           int    `json:"id"`
	Package      *int   `json:"package,omitempty"`
	Die          *int   `json:"die,omitempty"`
	CPUs         string `json:"cpus"`
	MemoryType   string `json:"memoryType"`
	NormalMemory bool   `json:"normalMemory"`
	Capacity     int64  `json:"capacity"` // bytes
	Distance     []int  `json:"distance,omitempty"`
}

// CPU is a single logical CPU.
type CPU struct {
	ID       int    `json:"id"`
	Package  int    `json:"package"`
	Die      int    `json:"die"`
	Cluster  int    `json:"cluster"`
	NUMANode int    `json:"numaNode"`
	Core     int    `json:"core"`
	Threads  string `json:"threads"` // sibling threads, self included
	CoreKind string `json:"coreKind,omitempty"`
	Online   bool   `json:"online"`
	Isolated bool   `json:"isolated,omitempty"`
	BaseFreq uint64 `json:"baseFreq,omitempty"` // kHz
	MinFreq  uint64 `json:"minFreq,omitempty"`  // kHz
	MaxFreq  uint64 `json:"maxFreq,omitempty"`  // kHz
}

// Pod is a pod with at least one container on this node.
type Pod struct {
	ID        string `json:"id"`  // CRI ID
	UID       string `json:"uid"` // Kubernetes UID
	Name      string `json:"name"`
	Namespace string `json:"namespace"`
	QOSClass  string `json:"qosClass,omitempty"`
}

// Container is a container on this node.
//
// The request and limit fields are what Kubernetes asked for. Applied is
// what actually reached the container. They can disagree, and that
// disagreement is usually the interesting part.
type Container struct {
	ID       string `json:"id"`
	PodID    string `json:"podId"`
	Name     string `json:"name"`
	State    string `json:"state,omitempty"`
	QOSClass string `json:"qosClass,omitempty"`

	CPURequest    int64 `json:"cpuRequest,omitempty"`    // milli-CPU
	CPULimit      int64 `json:"cpuLimit,omitempty"`      // milli-CPU
	MemoryRequest int64 `json:"memoryRequest,omitempty"` // bytes
	MemoryLimit   int64 `json:"memoryLimit,omitempty"`   // bytes

	Applied *Applied          `json:"applied,omitempty"`
	Classes map[string]string `json:"classes,omitempty"` // "rdt", "blockio"
	Hints   []*Hint           `json:"hints,omitempty"`
}

// Applied is what was actually applied to a container.
//
// The Cpuset field names mirror the cgroup filenames they come from, which
// is why they are not spelled with the CPU acronym like their neighbours.
type Applied struct {
	CpusetCpus  string `json:"cpusetCpus,omitempty"`
	CpusetMems  string `json:"cpusetMems,omitempty"`
	CPUShares   int64  `json:"cpuShares,omitempty"`
	CPUQuota    int64  `json:"cpuQuota,omitempty"`
	CPUPeriod   int64  `json:"cpuPeriod,omitempty"`
	MemoryLimit int64  `json:"memoryLimit,omitempty"` // bytes
}

// Hint is a topology hint collected for a container's devices.
type Hint struct {
	Provider string `json:"provider"` // sysfs path the hint came from
	CPUs     string `json:"cpus,omitempty"`
	NUMAs    string `json:"numas,omitempty"`
	Sockets  string `json:"sockets,omitempty"`
	IRQs     []int  `json:"irqs,omitempty"`
}

// Derived holds values computed from the sections above. A consumer may
// recompute them instead of trusting these.
//
// Commitment spreads each container's request evenly over the CPUs or memory
// nodes it is allowed on, and sums per CPU or node. Integer division
// truncates, so a commitment total can be up to one milli-CPU per CPU below
// the exact figure. Every CPU and NUMA node in Hardware has an entry, so a
// consumer needs no missing-key fallback.
type Derived struct {
	CPUCommitment    map[int]int64 `json:"cpuCommitment,omitempty"`    // cpu id -> milli-CPU
	CPUSharers       map[int]int   `json:"cpuSharers,omitempty"`       // cpu id -> containers allowed
	MemoryCommitment map[int]int64 `json:"memoryCommitment,omitempty"` // numa id -> bytes
}

// PolicyState is what the active policy chooses to say about itself. It is
// optional and additive: its absence means the policy has nothing to say.
type PolicyState struct {
	Name        string            `json:"name"`
	Zones       []*Zone           `json:"zones,omitempty"`
	Assignments []*Assignment     `json:"assignments,omitempty"`
	Notices     []*Notice         `json:"notices,omitempty"`
	Attributes  map[string]string `json:"attributes,omitempty"`
	Data        json.RawMessage   `json:"data,omitempty"` // policy-specific, opaque
}

// Zone is a policy pool, balloon or other resource zone.
type Zone struct {
	Name       string            `json:"name"`
	Type       string            `json:"type,omitempty"`
	Parent     string            `json:"parent,omitempty"`
	CPUs       string            `json:"cpus,omitempty"`
	Memory     string            `json:"memory,omitempty"`
	Resources  []*ZoneResource   `json:"resources,omitempty"`
	Attributes map[string]string `json:"attributes,omitempty"`
}

// ZoneResource is one resource of a Zone.
type ZoneResource struct {
	Name        string `json:"name"` // "cpu", "memory"
	Unit        string `json:"unit"` // "milliCPU", "bytes"
	Capacity    int64  `json:"capacity"`
	Allocatable int64  `json:"allocatable"`
	Available   int64  `json:"available"`
}

// Assignment is a policy's own account of one container's placement.
type Assignment struct {
	ContainerID   string            `json:"containerId"`
	Zone          string            `json:"zone,omitempty"`
	ExclusiveCPUs string            `json:"exclusiveCpus,omitempty"`
	SharedCPUs    string            `json:"sharedCpus,omitempty"`
	IsolatedCPUs  string            `json:"isolatedCpus,omitempty"`
	ReservedCPUs  string            `json:"reservedCpus,omitempty"`
	CPUShare      int64             `json:"cpuShare,omitempty"` // milli-CPU
	MemoryZone    string            `json:"memoryZone,omitempty"`
	MemoryTypes   []string          `json:"memoryTypes,omitempty"`
	Attributes    map[string]string `json:"attributes,omitempty"`
}

// Notice is a policy's explanation of something a viewer would otherwise
// have to guess at, for instance that its configuration disables pinning.
type Notice struct {
	Severity string `json:"severity"` // "info", "warning", "error"
	Message  string `json:"message"`
	Detail   string `json:"detail,omitempty"`
}
```

- [ ] **Step 4: Run the test to verify it passes**

Run: `go test ./pkg/introspect/api/... -v`

Expected: PASS, all subtests. A failing subtest means a JSON tag in
`types.go` does not match the expected key — fix `types.go`, not the test,
unless the test's expected list is provably wrong about `omitempty`.

- [ ] **Step 5: Verify the standard-library-only constraint**

Run: `go list -deps ./pkg/introspect/api | grep -v '^internal/' | grep -E '^(github.com|k8s.io|sigs.k8s.io|go.opentelemetry.io)' | grep -v '^github.com/containers/nri-plugins/pkg/introspect/api$'`

Expected: no output. Any line means a forbidden dependency crept in.

- [ ] **Step 6: Commit**

```bash
gofmt -l pkg/introspect/api/
git add pkg/introspect/api/
git commit -F - <<'EOF'
introspect: add wire format for introspection API.

Types for the node placement snapshot served over the
introspection endpoint. Standard library only, so that
external consumers can use them without pulling in the
resource manager or Kubernetes.

Units are fixed rather than tagged per value: CPU in
milli-CPU, memory in bytes, CPU and memory node sets as
Linux list-format strings.

Tests pin the JSON key set of every type, so a renamed
or dropped tag cannot slip through.

Signed-off-by: Krisztian Litkey <krisztian.litkey@intel.com>
Co-Authored-By: Claude Opus 5 (1M context) <noreply@anthropic.com>
EOF
```

---

### Task 2: Hardware collection

**Files:**
- Create: `pkg/resmgr/introspect/collect.go`
- Create: `pkg/resmgr/introspect/test-setup.sh`, `pkg/resmgr/introspect/test-cleanup.sh`
- Test: `pkg/resmgr/introspect/collect_test.go`

**Interfaces:**
- Consumes: `api.Hardware` and friends from Task 1.
- Produces: `func collectHardware(sys system.System) *api.Hardware` (package-private, used by Task 6).

The fixtures are the ones `pkg/sysfs` already ships. `sample1` is a single
package, 16 CPUs, one NUMA node. `sample2` is two packages, 112 CPUs and eight
NUMA nodes, of which **four have no CPUs at all** — memory-only nodes, which is
exactly the case a naive collector gets wrong.

- [ ] **Step 1: Add the fixture setup scripts**

The Makefile's test target runs any `test-setup.sh` it finds under the tree
(see `ginkgo-test-setup`), in the script's own directory. `testdata/` is
gitignored, so extracting into it is the established pattern.

Create `pkg/resmgr/introspect/test-setup.sh`, mode 0755:

```sh
#!/bin/sh

# Our collector tests run against the sysfs fixtures of pkg/sysfs.
# They are shared rather than duplicated; keep this in sync if those
# tarballs are ever renamed.

set -e

mkdir -p testdata
tar -C testdata -xJf ../../sysfs/test-data-sample1.tar.xz
tar -C testdata -xJf ../../sysfs/test-data-sample2.tar.xz
```

Create `pkg/resmgr/introspect/test-cleanup.sh`, mode 0755:

```sh
#!/bin/sh

rm -fr testdata
```

Run: `chmod 755 pkg/resmgr/introspect/test-setup.sh pkg/resmgr/introspect/test-cleanup.sh && (cd pkg/resmgr/introspect && ./test-setup.sh) && ls pkg/resmgr/introspect/testdata`

Expected: `sample1  sample2`

- [ ] **Step 2: Write the failing test**

Create `pkg/resmgr/introspect/collect_test.go` with the license header, then:

```go
package introspect

import (
	"os"
	"path"
	"testing"

	"github.com/containers/nri-plugins/pkg/sysfs"
	"github.com/containers/nri-plugins/pkg/utils/cpuset"
)

// discover returns the sysfs fixture with the given name. Run
// ./test-setup.sh first if testdata is missing.
func discover(t *testing.T, sample string) sysfs.System {
	t.Helper()

	cwd, err := os.Getwd()
	if err != nil {
		t.Fatalf("failed to get working directory: %v", err)
	}

	root := path.Join(cwd, "testdata", sample)
	if _, err := os.Stat(root); err != nil {
		t.Skipf("fixture %s missing, run ./test-setup.sh: %v", sample, err)
	}

	sysfs.SetSysRoot(root)

	sys, err := sysfs.DiscoverSystem()
	if err != nil {
		t.Fatalf("failed to discover %s: %v", sample, err)
	}

	return sys
}

func TestCollectHardwareSample1(t *testing.T) {
	sys := discover(t, "sample1")
	hw := collectHardware(sys)

	if len(hw.CPUs) != 16 {
		t.Errorf("expected 16 CPUs, got %d", len(hw.CPUs))
	}
	if len(hw.Packages) != 1 {
		t.Errorf("expected 1 package, got %d", len(hw.Packages))
	}
	if len(hw.NUMANodes) != 1 {
		t.Errorf("expected 1 NUMA node, got %d", len(hw.NUMANodes))
	}
}

func TestCollectHardwareSample2(t *testing.T) {
	sys := discover(t, "sample2")
	hw := collectHardware(sys)

	if len(hw.CPUs) != 112 {
		t.Errorf("expected 112 CPUs, got %d", len(hw.CPUs))
	}
	if len(hw.Packages) != 2 {
		t.Errorf("expected 2 packages, got %d", len(hw.Packages))
	}
	if len(hw.NUMANodes) != 8 {
		t.Errorf("expected 8 NUMA nodes, got %d", len(hw.NUMANodes))
	}

	// Four of the eight nodes are memory-only. They must be present,
	// with a capacity and an empty cpuset.
	memoryOnly := 0
	for _, n := range hw.NUMANodes {
		if n.CPUs != "" {
			continue
		}
		memoryOnly++
		if n.Capacity <= 0 {
			t.Errorf("memory-only node %d has capacity %d", n.ID, n.Capacity)
		}
		// A node with no CPUs belongs to no package or die, and must
		// not claim package 0 just because sysfs leaves the ID unset.
		if n.Package != nil || n.Die != nil {
			t.Errorf("memory-only node %d claims package %v die %v",
				n.ID, n.Package, n.Die)
		}
	}
	if memoryOnly != 4 {
		t.Errorf("expected 4 memory-only NUMA nodes, got %d", memoryOnly)
	}
}

// TestCollectHardwareMapping checks every CPU against sysfs rather than
// against hardcoded fixture internals: the collector's whole job is the
// mapping, so that is what gets asserted.
func TestCollectHardwareMapping(t *testing.T) {
	sys := discover(t, "sample2")
	hw := collectHardware(sys)

	seen := map[int]bool{}
	for _, c := range hw.CPUs {
		if seen[c.ID] {
			t.Fatalf("CPU %d reported twice", c.ID)
		}
		seen[c.ID] = true

		cpu := sys.CPU(c.ID)
		if cpu == nil {
			t.Fatalf("CPU %d is not in sysfs at all", c.ID)
		}
		// Unlike a NUMA node, a CPU always belongs to a package, so
		// these stay plain ints.
		if c.Package != int(cpu.PackageID()) {
			t.Errorf("CPU %d: package %d, expected %d",
				c.ID, c.Package, cpu.PackageID())
		}
		if c.NUMANode != int(cpu.NodeID()) {
			t.Errorf("CPU %d: NUMA node %d, expected %d",
				c.ID, c.NUMANode, cpu.NodeID())
		}
		if c.Core != int(cpu.CoreID()) {
			t.Errorf("CPU %d: core %d, expected %d",
				c.ID, c.Core, cpu.CoreID())
		}
		if c.Threads != cpu.ThreadCPUSet().String() {
			t.Errorf("CPU %d: threads %q, expected %q",
				c.ID, c.Threads, cpu.ThreadCPUSet().String())
		}
		if c.Online != cpu.Online() {
			t.Errorf("CPU %d: online %v, expected %v",
				c.ID, c.Online, cpu.Online())
		}
	}

	if len(seen) != sys.CPUCount() {
		t.Errorf("collected %d CPUs, sysfs has %d", len(seen), sys.CPUCount())
	}

	if hw.IsolatedCPUs != sys.Isolated().String() {
		t.Errorf("isolated %q, expected %q",
			hw.IsolatedCPUs, sys.Isolated().String())
	}
	if hw.OfflineCPUs != sys.Offlined().String() {
		t.Errorf("offlined %q, expected %q",
			hw.OfflineCPUs, sys.Offlined().String())
	}

	// Every package's cpuset must be the union of its dies' cpusets.
	for _, p := range hw.Packages {
		union := cpuset.New()
		for _, d := range p.Dies {
			union = union.Union(cpuset.MustParse(d.CPUs))
		}
		if union.String() != p.CPUs {
			t.Errorf("package %d: dies cover %q, package has %q",
				p.ID, union.String(), p.CPUs)
		}
	}
}
```

- [ ] **Step 3: Run the test to verify it fails**

Run: `go test ./pkg/resmgr/introspect/... -run TestCollectHardware -v`

Expected: FAIL — `undefined: collectHardware`.

- [ ] **Step 4: Write the implementation**

Create `pkg/resmgr/introspect/collect.go` with the license header, then:

```go
package introspect

import (
	"github.com/containers/nri-plugins/pkg/introspect/api"
	logger "github.com/containers/nri-plugins/pkg/log"
	"github.com/containers/nri-plugins/pkg/utils/cpuset"

	system "github.com/containers/nri-plugins/pkg/sysfs"
)

// Our logger instance.
var log = logger.Get("introspect")

// collectHardware describes the machine. It asks sysfs only; no policy and
// no cache are involved, so this section is always available and can never
// go stale with respect to a policy's participation.
func collectHardware(sys system.System) *api.Hardware {
	if sys == nil {
		return nil
	}

	hw := &api.Hardware{
		IsolatedCPUs: sys.Isolated().String(),
		OfflineCPUs:  sys.Offlined().String(),
	}

	for _, id := range sys.PackageIDs() {
		pkg := sys.Package(id)
		p := &api.Package{
			ID:   int(id),
			CPUs: pkg.CPUSet().String(),
		}

		for _, dieID := range pkg.DieIDs() {
			d := &api.Die{
				ID:   int(dieID),
				CPUs: pkg.DieCPUSet(dieID).String(),
			}
			for _, nodeID := range pkg.DieNodeIDs(dieID) {
				d.NUMANodes = append(d.NUMANodes, int(nodeID))
			}
			for _, clusterID := range pkg.DieClusterIDs(dieID) {
				d.Clusters = append(d.Clusters, &api.Cluster{
					ID:   int(clusterID),
					CPUs: pkg.DieClusterCPUSet(dieID, clusterID).String(),
				})
			}
			p.Dies = append(p.Dies, d)
		}

		hw.Packages = append(hw.Packages, p)
	}

	for _, id := range sys.NodeIDs() {
		node := sys.Node(id)
		n := &api.NUMANode{
			ID:           int(id),
			CPUs:         node.CPUSet().String(),
			MemoryType:   node.GetMemoryType().String(),
			NormalMemory: node.HasNormalMemory(),
			Distance:     node.Distance(),
		}
		// A memory-only node belongs to no package or die. sysfs
		// leaves both IDs at zero for such a node, which is
		// indistinguishable from a real package 0, so association is
		// reported only for nodes that actually have CPUs.
		if !node.CPUSet().IsEmpty() {
			pkgID, dieID := int(node.PackageID()), int(node.DieID())
			n.Package, n.Die = &pkgID, &dieID
		}
		if info, err := node.MemoryInfo(); err == nil && info != nil {
			n.Capacity = int64(info.MemTotal)
		} else if err != nil {
			log.Warnf("failed to get memory info for NUMA node %d: %v", id, err)
		}
		hw.NUMANodes = append(hw.NUMANodes, n)
	}

	for _, id := range sys.CPUIDs() {
		cpu := sys.CPU(id)
		freq := cpu.FrequencyRange()
		c := &api.CPU{
			ID:       int(id),
			Package:  int(cpu.PackageID()),
			Die:      int(cpu.DieID()),
			Cluster:  int(cpu.ClusterID()),
			NUMANode: int(cpu.NodeID()),
			Core:     int(cpu.CoreID()),
			Threads:  cpu.ThreadCPUSet().String(),
			Online:   cpu.Online(),
			Isolated: cpu.Isolated(),
			BaseFreq: freq.Base,
			MinFreq:  freq.Min,
			MaxFreq:  freq.Max,
		}
		hw.CPUs = append(hw.CPUs, c)
	}

	for _, kind := range sys.CoreKinds() {
		cpus := sys.CoreKindCPUs(kind)
		if cpus.IsEmpty() {
			continue
		}
		if hw.CoreKinds == nil {
			hw.CoreKinds = map[string]string{}
		}
		hw.CoreKinds[kind.String()] = cpus.String()
	}

	// Tag each CPU with its core kind, so a consumer needs no reverse
	// lookup through CoreKinds.
	for kind, cpus := range hw.CoreKinds {
		set := cpuset.MustParse(cpus)
		for _, c := range hw.CPUs {
			if set.Contains(c.ID) {
				c.CoreKind = kind
			}
		}
	}

	return hw
}
```

`pkg/sysfs` is imported once, under the `system` alias, matching how
`pkg/resmgr/policy/policy.go` refers to it.

Check that `sysfs.MemoryType` and `sysfs.CoreKind` really have a `String()`
method before relying on it:

Run: `go doc github.com/containers/nri-plugins/pkg/sysfs MemoryType; go doc github.com/containers/nri-plugins/pkg/sysfs CoreKind`

If either lacks `String()`, map it with a small local switch instead of
inventing one on the sysfs type — `MemoryType` values are DRAM, HBM and PMEM;
`CoreKind` values are P-core and E-core.

- [ ] **Step 5: Run the tests to verify they pass**

Run: `go test ./pkg/resmgr/introspect/... -run TestCollectHardware -v`

Expected: PASS for all three tests.

- [ ] **Step 6: Commit**

```bash
gofmt -l pkg/resmgr/introspect/
git add pkg/resmgr/introspect/
git commit -F - <<'EOF'
introspect: collect hardware topology.

Describe packages, dies, clusters, NUMA nodes and every
logical CPU from sysfs. No policy or cache involvement,
so this part of a snapshot is always available.

Tests run against the sysfs fixtures pkg/sysfs already
ships. sample2 is the interesting one: two packages and
eight NUMA nodes, four of them memory-only.

Signed-off-by: Krisztian Litkey <krisztian.litkey@intel.com>
Co-Authored-By: Claude Opus 5 (1M context) <noreply@anthropic.com>
EOF
```

---

### Task 3: Pod and container collection

**Files:**
- Modify: `pkg/resmgr/introspect/collect.go`
- Create: `pkg/resmgr/introspect/fake_test.go`
- Test: `pkg/resmgr/introspect/collect_test.go` (append)

**Interfaces:**
- Consumes: `collectHardware()` from Task 2.
- Produces: the `Cache`, `Pod` and `Container` interfaces, and
  `func collectWorkloads(c Cache) ([]*api.Pod, []*api.Container)`.

- [ ] **Step 1: Write the failing test**

Create `pkg/resmgr/introspect/fake_test.go` with the license header, then:

```go
package introspect

import (
	v1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"

	nri "github.com/containerd/nri/pkg/api"
	"github.com/containers/nri-plugins/pkg/topology"
)

type fakePod struct {
	id, uid, name, namespace string
	qos                      v1.PodQOSClass
}

func (p *fakePod) GetID() string               { return p.id }
func (p *fakePod) GetUID() string              { return p.uid }
func (p *fakePod) GetName() string             { return p.name }
func (p *fakePod) GetNamespace() string        { return p.namespace }
func (p *fakePod) GetQOSClass() v1.PodQOSClass { return p.qos }

type fakeContainer struct {
	id, podID, name string
	state           nri.ContainerState
	qos             v1.PodQOSClass
	requirements    v1.ResourceRequirements
	cpusetCpus      string
	cpusetMems      string
	cpuShares       int64
	cpuQuota        int64
	cpuPeriod       int64
	memoryLimit     int64
	rdtClass        string
	blockIOClass    string
	hints           topology.Hints
}

func (c *fakeContainer) GetID() string                                { return c.id }
func (c *fakeContainer) GetPodID() string                             { return c.podID }
func (c *fakeContainer) GetName() string                              { return c.name }
func (c *fakeContainer) GetState() nri.ContainerState                 { return c.state }
func (c *fakeContainer) GetQOSClass() v1.PodQOSClass                  { return c.qos }
func (c *fakeContainer) GetResourceRequirements() v1.ResourceRequirements {
	return c.requirements
}
func (c *fakeContainer) GetCpusetCpus() string        { return c.cpusetCpus }
func (c *fakeContainer) GetCpusetMems() string        { return c.cpusetMems }
func (c *fakeContainer) GetCPUShares() int64          { return c.cpuShares }
func (c *fakeContainer) GetCPUQuota() int64           { return c.cpuQuota }
func (c *fakeContainer) GetCPUPeriod() int64          { return c.cpuPeriod }
func (c *fakeContainer) GetMemoryLimit() int64        { return c.memoryLimit }
func (c *fakeContainer) GetRDTClass() string          { return c.rdtClass }
func (c *fakeContainer) GetBlockIOClass() string      { return c.blockIOClass }
func (c *fakeContainer) GetTopologyHints() topology.Hints { return c.hints }

type fakeCache struct {
	pods       []Pod
	containers []Container
}

func (c *fakeCache) GetPods() []Pod             { return c.pods }
func (c *fakeCache) GetContainers() []Container { return c.containers }

// requirements builds a ResourceRequirements the way Kubernetes would.
func requirements(cpuReq, cpuLim string, memReq, memLim string) v1.ResourceRequirements {
	r := v1.ResourceRequirements{
		Requests: v1.ResourceList{},
		Limits:   v1.ResourceList{},
	}
	if cpuReq != "" {
		r.Requests[v1.ResourceCPU] = resource.MustParse(cpuReq)
	}
	if cpuLim != "" {
		r.Limits[v1.ResourceCPU] = resource.MustParse(cpuLim)
	}
	if memReq != "" {
		r.Requests[v1.ResourceMemory] = resource.MustParse(memReq)
	}
	if memLim != "" {
		r.Limits[v1.ResourceMemory] = resource.MustParse(memLim)
	}
	return r
}
```

Append to `pkg/resmgr/introspect/collect_test.go`:

```go
func TestCollectWorkloads(t *testing.T) {
	c := &fakeCache{
		pods: []Pod{
			&fakePod{
				id: "pod0", uid: "uid0", name: "web", namespace: "default",
				qos: v1.PodQOSGuaranteed,
			},
		},
		containers: []Container{
			&fakeContainer{
				id: "ctr0", podID: "pod0", name: "nginx",
				state:        nri.ContainerState_CONTAINER_RUNNING,
				qos:          v1.PodQOSGuaranteed,
				requirements: requirements("4", "4", "2Gi", "2Gi"),
				cpusetCpus:   "0-3",
				cpusetMems:   "0",
				cpuShares:    4096,
				cpuQuota:     400000,
				cpuPeriod:    100000,
				memoryLimit:  2147483648,
				rdtClass:     "guaranteed",
				blockIOClass: "",
				hints: topology.Hints{
					"eth0": topology.Hint{
						Provider: "/sys/class/net/eth0",
						CPUs:     "0-3",
						NUMAs:    "0",
					},
				},
			},
		},
	}

	pods, containers := collectWorkloads(c)

	if len(pods) != 1 {
		t.Fatalf("expected 1 pod, got %d", len(pods))
	}
	if pods[0].ID != "pod0" || pods[0].UID != "uid0" ||
		pods[0].Name != "web" || pods[0].Namespace != "default" ||
		pods[0].QOSClass != "Guaranteed" {
		t.Errorf("unexpected pod: %+v", pods[0])
	}

	if len(containers) != 1 {
		t.Fatalf("expected 1 container, got %d", len(containers))
	}

	got := containers[0]
	if got.ID != "ctr0" || got.PodID != "pod0" || got.Name != "nginx" {
		t.Errorf("unexpected container identity: %+v", got)
	}
	if got.State != "running" {
		t.Errorf("state %q, expected \"running\"", got.State)
	}
	if got.CPURequest != 4000 || got.CPULimit != 4000 {
		t.Errorf("CPU request/limit %d/%d, expected 4000/4000",
			got.CPURequest, got.CPULimit)
	}
	if got.MemoryRequest != 2147483648 || got.MemoryLimit != 2147483648 {
		t.Errorf("memory request/limit %d/%d, expected 2147483648 twice",
			got.MemoryRequest, got.MemoryLimit)
	}
	if got.Applied == nil {
		t.Fatal("applied resources missing")
	}
	if got.Applied.CpusetCpus != "0-3" || got.Applied.CpusetMems != "0" {
		t.Errorf("unexpected applied cpuset: %+v", got.Applied)
	}
	if got.Applied.CPUShares != 4096 || got.Applied.CPUQuota != 400000 ||
		got.Applied.CPUPeriod != 100000 ||
		got.Applied.MemoryLimit != 2147483648 {
		t.Errorf("unexpected applied cgroup parameters: %+v", got.Applied)
	}
	if got.Classes["rdt"] != "guaranteed" {
		t.Errorf("rdt class %q, expected \"guaranteed\"", got.Classes["rdt"])
	}
	if _, ok := got.Classes["blockio"]; ok {
		t.Error("empty blockio class should be omitted")
	}
	if len(got.Hints) != 1 || got.Hints[0].Provider != "/sys/class/net/eth0" {
		t.Errorf("unexpected hints: %+v", got.Hints)
	}
}

func TestCollectWorkloadsNoArgs(t *testing.T) {
	// The removed introspection API exposed container command lines.
	// Make sure no field of that shape came back.
	if _, ok := any(&api.Container{}).(interface{ GetArgs() []string }); ok {
		t.Error("api.Container must not expose command line arguments")
	}
	data, err := json.Marshal(&api.Container{})
	if err != nil {
		t.Fatalf("failed to marshal: %v", err)
	}
	if strings.Contains(string(data), "args") {
		t.Errorf("container JSON mentions args: %s", data)
	}
}
```

Add `encoding/json`, `strings`, `v1 "k8s.io/api/core/v1"`, `nri "github.com/containerd/nri/pkg/api"`,
`"github.com/containers/nri-plugins/pkg/introspect/api"` and
`"github.com/containers/nri-plugins/pkg/topology"` to the test file's imports.

- [ ] **Step 2: Run the test to verify it fails**

Run: `go test ./pkg/resmgr/introspect/... -run TestCollectWorkloads -v`

Expected: FAIL — `undefined: Pod`, `undefined: Container`, `undefined: Cache`,
`undefined: collectWorkloads`.

- [ ] **Step 3: Write the implementation**

Append to `pkg/resmgr/introspect/collect.go`:

```go
// Cache is the part of the resource manager cache the collector needs.
// The interfaces here are narrow on purpose: cache.Cache, cache.Pod and
// cache.Container satisfy them structurally, and a test fake is a dozen
// methods rather than a hundred.
type Cache interface {
	GetPods() []Pod
	GetContainers() []Container
}

// Pod is the part of a cached pod the collector needs.
type Pod interface {
	GetID() string
	GetUID() string
	GetName() string
	GetNamespace() string
	GetQOSClass() v1.PodQOSClass
}

// Container is the part of a cached container the collector needs.
type Container interface {
	GetID() string
	GetPodID() string
	GetName() string
	GetState() nri.ContainerState
	GetQOSClass() v1.PodQOSClass
	GetResourceRequirements() v1.ResourceRequirements
	GetCpusetCpus() string
	GetCpusetMems() string
	GetCPUShares() int64
	GetCPUQuota() int64
	GetCPUPeriod() int64
	GetMemoryLimit() int64
	GetRDTClass() string
	GetBlockIOClass() string
	GetTopologyHints() topology.Hints
}

// collectWorkloads describes the pods and containers on this node, as the
// cache knows them. Command line arguments are deliberately not exposed.
func collectWorkloads(c Cache) ([]*api.Pod, []*api.Container) {
	if c == nil {
		return nil, nil
	}

	var (
		pods       []*api.Pod
		containers []*api.Container
	)

	for _, p := range c.GetPods() {
		pods = append(pods, &api.Pod{
			ID:        p.GetID(),
			UID:       p.GetUID(),
			Name:      p.GetName(),
			Namespace: p.GetNamespace(),
			QOSClass:  string(p.GetQOSClass()),
		})
	}

	for _, ctr := range c.GetContainers() {
		container := &api.Container{
			ID:       ctr.GetID(),
			PodID:    ctr.GetPodID(),
			Name:     ctr.GetName(),
			State:    containerState(ctr.GetState()),
			QOSClass: string(ctr.GetQOSClass()),
			Applied: &api.Applied{
				CpusetCpus:  ctr.GetCpusetCpus(),
				CpusetMems:  ctr.GetCpusetMems(),
				CPUShares:   ctr.GetCPUShares(),
				CPUQuota:    ctr.GetCPUQuota(),
				CPUPeriod:   ctr.GetCPUPeriod(),
				MemoryLimit: ctr.GetMemoryLimit(),
			},
		}

		resources := ctr.GetResourceRequirements()
		if q, ok := resources.Requests[v1.ResourceCPU]; ok {
			container.CPURequest = q.MilliValue()
		}
		if q, ok := resources.Limits[v1.ResourceCPU]; ok {
			container.CPULimit = q.MilliValue()
		}
		if q, ok := resources.Requests[v1.ResourceMemory]; ok {
			container.MemoryRequest = q.Value()
		}
		if q, ok := resources.Limits[v1.ResourceMemory]; ok {
			container.MemoryLimit = q.Value()
		}

		for name, value := range map[string]string{
			"rdt":     ctr.GetRDTClass(),
			"blockio": ctr.GetBlockIOClass(),
		} {
			if value == "" {
				continue
			}
			if container.Classes == nil {
				container.Classes = map[string]string{}
			}
			container.Classes[name] = value
		}

		for _, h := range ctr.GetTopologyHints() {
			container.Hints = append(container.Hints, &api.Hint{
				Provider: h.Provider,
				CPUs:     h.CPUs,
				NUMAs:    h.NUMAs,
				Sockets:  h.Sockets,
				IRQs:     h.IRQs,
			})
		}
		sort.Slice(container.Hints, func(i, j int) bool {
			return container.Hints[i].Provider < container.Hints[j].Provider
		})

		containers = append(containers, container)
	}

	return pods, containers
}

// containerState turns an NRI container state into a short lower-case name.
func containerState(state nri.ContainerState) string {
	return strings.ToLower(strings.TrimPrefix(state.String(), "CONTAINER_"))
}
```

Add `sort`, `strings`, `v1 "k8s.io/api/core/v1"`,
`nri "github.com/containerd/nri/pkg/api"` and
`"github.com/containers/nri-plugins/pkg/topology"` to `collect.go`'s imports.

Hints come from a map, so they arrive in random order; the `sort.Slice` is what
keeps a snapshot from changing shape between two identical requests.

- [ ] **Step 4: Run the tests to verify they pass**

Run: `go test ./pkg/resmgr/introspect/... -v`

Expected: PASS, including the Task 2 tests.

- [ ] **Step 5: Verify the real cache types satisfy the interfaces**

This is the assertion that keeps the narrow interfaces honest. Add to
`collect.go`, after the interface declarations:

```go
// Make sure the real cache types keep satisfying our interfaces.
var (
	_ Pod       = cache.Pod(nil)
	_ Container = cache.Container(nil)
)
```

with `"github.com/containers/nri-plugins/pkg/resmgr/cache"` imported.

Run: `go build ./pkg/resmgr/introspect/...`

Expected: success. A compile error here means an interface method's name or
signature does not match the cache — fix the interface to match the cache, not
the other way round.

Note that `Cache` itself cannot be checked this way, because
`cache.Cache.GetPods()` returns `[]cache.Pod` rather than `[]Pod`. Task 6 bridges
that with an adapter.

- [ ] **Step 6: Commit**

```bash
gofmt -l pkg/resmgr/introspect/
git add pkg/resmgr/introspect/
git commit -F - <<'EOF'
introspect: collect pods and containers.

Report identity, QoS, requests and limits, topology
hints and class assignments, plus what was actually
applied to each container. Requested and applied are
both present: their disagreement is usually the
interesting part.

The collector consumes narrow interfaces the cache
types satisfy structurally, so tests need a dozen fake
methods instead of a hundred.

Container command lines stay unexposed. The removed
introspection API published them, and command lines
leak credentials.

Signed-off-by: Krisztian Litkey <krisztian.litkey@intel.com>
Co-Authored-By: Claude Opus 5 (1M context) <noreply@anthropic.com>
EOF
```

---

### Task 4: Derived commitment

**Files:**
- Modify: `pkg/resmgr/introspect/collect.go`
- Test: `pkg/resmgr/introspect/collect_test.go` (append)

**Interfaces:**
- Consumes: `api.Hardware`, `api.Container` from Task 1.
- Produces: `func derive(hw *api.Hardware, containers []*api.Container) *api.Derived`.

- [ ] **Step 1: Write the failing test**

Append to `pkg/resmgr/introspect/collect_test.go`:

```go
func TestDerive(t *testing.T) {
	hw := &api.Hardware{
		CPUs: []*api.CPU{
			{ID: 0}, {ID: 1}, {ID: 2}, {ID: 3},
		},
		NUMANodes: []*api.NUMANode{
			{ID: 0}, {ID: 1},
		},
	}

	for _, tc := range []struct {
		name          string
		containers    []*api.Container
		cpuCommitment map[int]int64
		cpuSharers    map[int]int
		memCommitment map[int]int64
	}{
		{
			name:          "no containers leaves every entry at zero",
			containers:    nil,
			cpuCommitment: map[int]int64{0: 0, 1: 0, 2: 0, 3: 0},
			cpuSharers:    map[int]int{0: 0, 1: 0, 2: 0, 3: 0},
			memCommitment: map[int]int64{0: 0, 1: 0},
		},
		{
			name: "exclusive container fully commits its CPUs",
			containers: []*api.Container{
				{
					CPURequest:    2000,
					MemoryRequest: 1000,
					Applied:       &api.Applied{CpusetCpus: "0-1", CpusetMems: "0"},
				},
			},
			cpuCommitment: map[int]int64{0: 1000, 1: 1000, 2: 0, 3: 0},
			cpuSharers:    map[int]int{0: 1, 1: 1, 2: 0, 3: 0},
			memCommitment: map[int]int64{0: 1000, 1: 0},
		},
		{
			name: "shared request spreads and sharers accumulate",
			containers: []*api.Container{
				{
					CPURequest: 500,
					Applied:    &api.Applied{CpusetCpus: "2-3"},
				},
				{
					CPURequest: 500,
					Applied:    &api.Applied{CpusetCpus: "2-3"},
				},
			},
			cpuCommitment: map[int]int64{0: 0, 1: 0, 2: 500, 3: 500},
			cpuSharers:    map[int]int{0: 0, 1: 0, 2: 2, 3: 2},
			memCommitment: map[int]int64{0: 0, 1: 0},
		},
		{
			name: "oversubscription is reported, not clamped",
			containers: []*api.Container{
				{
					CPURequest: 4000,
					Applied:    &api.Applied{CpusetCpus: "0"},
				},
			},
			cpuCommitment: map[int]int64{0: 4000, 1: 0, 2: 0, 3: 0},
			cpuSharers:    map[int]int{0: 1, 1: 0, 2: 0, 3: 0},
			memCommitment: map[int]int64{0: 0, 1: 0},
		},
		{
			name: "empty cpuset contributes nothing",
			containers: []*api.Container{
				{
					CPURequest: 4000,
					Applied:    &api.Applied{CpusetCpus: ""},
				},
			},
			cpuCommitment: map[int]int64{0: 0, 1: 0, 2: 0, 3: 0},
			cpuSharers:    map[int]int{0: 0, 1: 0, 2: 0, 3: 0},
			memCommitment: map[int]int64{0: 0, 1: 0},
		},
		{
			name: "missing applied section contributes nothing",
			containers: []*api.Container{
				{CPURequest: 4000},
			},
			cpuCommitment: map[int]int64{0: 0, 1: 0, 2: 0, 3: 0},
			cpuSharers:    map[int]int{0: 0, 1: 0, 2: 0, 3: 0},
			memCommitment: map[int]int64{0: 0, 1: 0},
		},
		{
			name: "cpuset outside the machine is ignored",
			containers: []*api.Container{
				{
					CPURequest: 1000,
					Applied:    &api.Applied{CpusetCpus: "99"},
				},
			},
			cpuCommitment: map[int]int64{0: 0, 1: 0, 2: 0, 3: 0},
			cpuSharers:    map[int]int{0: 0, 1: 0, 2: 0, 3: 0},
			memCommitment: map[int]int64{0: 0, 1: 0},
		},
		{
			name: "unparseable cpuset is ignored",
			containers: []*api.Container{
				{
					CPURequest: 1000,
					Applied:    &api.Applied{CpusetCpus: "not-a-cpuset"},
				},
			},
			cpuCommitment: map[int]int64{0: 0, 1: 0, 2: 0, 3: 0},
			cpuSharers:    map[int]int{0: 0, 1: 0, 2: 0, 3: 0},
			memCommitment: map[int]int64{0: 0, 1: 0},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			d := derive(hw, tc.containers)

			if !reflect.DeepEqual(d.CPUCommitment, tc.cpuCommitment) {
				t.Errorf("CPU commitment\n  got:      %v\n  expected: %v",
					d.CPUCommitment, tc.cpuCommitment)
			}
			if !reflect.DeepEqual(d.CPUSharers, tc.cpuSharers) {
				t.Errorf("CPU sharers\n  got:      %v\n  expected: %v",
					d.CPUSharers, tc.cpuSharers)
			}
			if !reflect.DeepEqual(d.MemoryCommitment, tc.memCommitment) {
				t.Errorf("memory commitment\n  got:      %v\n  expected: %v",
					d.MemoryCommitment, tc.memCommitment)
			}
		})
	}
}
```

Add `reflect` to the test file's imports.

- [ ] **Step 2: Run the test to verify it fails**

Run: `go test ./pkg/resmgr/introspect/... -run TestDerive -v`

Expected: FAIL — `undefined: derive`.

- [ ] **Step 3: Write the implementation**

Append to `pkg/resmgr/introspect/collect.go`:

```go
// derive computes the values of the snapshot's derived section.
//
// A container's request is spread evenly over the CPUs, or memory nodes, it
// is allowed on, and the shares are summed. A container allowed on a single
// CPU and requesting four of them commits that CPU fourfold; the number is
// reported as it is rather than clamped, because oversubscription is
// precisely what a viewer wants to see.
//
// A cpuset that does not parse, is empty, or names CPUs the machine does not
// have contributes nothing. An unparseable cpuset means something upstream
// is already broken and is logged, not guessed at.
func derive(hw *api.Hardware, containers []*api.Container) *api.Derived {
	d := &api.Derived{
		CPUCommitment:    map[int]int64{},
		CPUSharers:       map[int]int{},
		MemoryCommitment: map[int]int64{},
	}

	if hw == nil {
		return d
	}

	// Every CPU and node gets an entry, so consumers need no fallback.
	for _, cpu := range hw.CPUs {
		d.CPUCommitment[cpu.ID] = 0
		d.CPUSharers[cpu.ID] = 0
	}
	for _, node := range hw.NUMANodes {
		d.MemoryCommitment[node.ID] = 0
	}

	for _, c := range containers {
		if c.Applied == nil {
			continue
		}

		if cpus := parseSet(c.Applied.CpusetCpus, "cpuset.cpus", c.ID); cpus != nil {
			if n := int64(len(cpus)); n > 0 {
				share := c.CPURequest / n
				for _, id := range cpus {
					if _, ok := d.CPUCommitment[id]; !ok {
						continue
					}
					d.CPUCommitment[id] += share
					d.CPUSharers[id]++
				}
			}
		}

		if mems := parseSet(c.Applied.CpusetMems, "cpuset.mems", c.ID); mems != nil {
			if n := int64(len(mems)); n > 0 {
				share := c.MemoryRequest / n
				for _, id := range mems {
					if _, ok := d.MemoryCommitment[id]; !ok {
						continue
					}
					d.MemoryCommitment[id] += share
				}
			}
		}
	}

	return d
}

// parseSet parses a Linux list-format set, returning nil if it cannot.
func parseSet(value, what, id string) []int {
	if value == "" {
		return nil
	}

	set, err := cpuset.Parse(value)
	if err != nil {
		log.Warnf("container %s has unparseable %s %q: %v", id, what, value, err)
		return nil
	}

	return set.List()
}
```

`cpuset.Parse` and `.List()` come from
`github.com/containers/nri-plugins/pkg/utils/cpuset`, already imported in
Task 2. The memory node set uses the same list format, which is why the same
parser serves both.

- [ ] **Step 4: Run the tests to verify they pass**

Run: `go test ./pkg/resmgr/introspect/... -v`

Expected: PASS, all subtests.

- [ ] **Step 5: Commit**

```bash
gofmt -l pkg/resmgr/introspect/
git add pkg/resmgr/introspect/
git commit -F - <<'EOF'
introspect: derive CPU and memory commitment.

Spread each container's request over the CPUs and
memory nodes it is allowed on, and sum per CPU and per
node. This is what makes a heatmap of the machine
possible from policy-agnostic data alone, and it shows
oversubscription honestly rather than clamping it.

Every CPU and node gets an entry so that consumers need
no missing-key fallback. An empty, unparseable or
out-of-range set contributes nothing.

Signed-off-by: Krisztian Litkey <krisztian.litkey@intel.com>
Co-Authored-By: Claude Opus 5 (1M context) <noreply@anthropic.com>
EOF
```

---

### Task 5: Snapshot assembly

**Files:**
- Modify: `pkg/resmgr/introspect/collect.go`
- Test: `pkg/resmgr/introspect/collect_test.go` (append)

**Interfaces:**
- Consumes: `collectHardware()`, `collectWorkloads()`, `derive()`.
- Produces: `type Input struct{...}`, `func Collect(in *Input) *api.Snapshot`,
  `func collectConfig(cfg cfgapi.ResmgrConfig) *api.Config`.

- [ ] **Step 1: Write the failing test**

Append to `pkg/resmgr/introspect/collect_test.go`:

```go
// fakeConfig is a minimal cfgapi.ResmgrConfig. It is shaped like the real
// thing: the concrete configuration types are whole custom resources, such
// as *cfgapi.TopologyAwarePolicy, so a marshalled configuration has
// metadata and spec sections rather than bare policy fields.
type fakeConfig struct {
	metav1.ObjectMeta `json:"metadata,omitempty"`
	Spec              fakeSpec `json:"spec"`
}

type fakeSpec struct {
	Pin bool `json:"pinCPU"`
}

func (c *fakeConfig) GetObjectMeta() metav1.Object    { return &c.ObjectMeta }
func (c *fakeConfig) AgentConfig() *cfgapi.AgentConfig { return nil }
func (c *fakeConfig) CommonConfig() *cfgapi.CommonConfig {
	return &cfgapi.CommonConfig{}
}
func (c *fakeConfig) PolicyConfig() any { return nil }

func TestCollect(t *testing.T) {
	sys := discover(t, "sample1")

	in := &Input{
		NodeName: "worker-1",
		Policy:   "topology-aware",
		Version:  "v0.0.0-test",
		System:   sys,
		Cache: &fakeCache{
			pods: []Pod{
				&fakePod{id: "pod0", uid: "uid0", name: "web",
					namespace: "default", qos: v1.PodQOSGuaranteed},
			},
			containers: []Container{
				&fakeContainer{
					id: "ctr0", podID: "pod0", name: "nginx",
					state:        nri.ContainerState_CONTAINER_RUNNING,
					requirements: requirements("2", "2", "1Gi", "1Gi"),
					cpusetCpus:   "0-1",
					cpusetMems:   "0",
				},
			},
		},
		Config: &fakeConfig{
			ObjectMeta: metav1.ObjectMeta{
				Name:            "node.worker-1",
				Namespace:       "kube-system",
				Generation:      7,
				ResourceVersion: "12345",
			},
			Spec: fakeSpec{Pin: true},
		},
	}

	snap := Collect(in)

	if snap.Version != api.Version {
		t.Errorf("version %q, expected %q", snap.Version, api.Version)
	}
	if snap.Timestamp.IsZero() {
		t.Error("timestamp not set")
	}
	if snap.Generation != 0 {
		t.Errorf("generation %d, expected 0: the server stamps it",
			snap.Generation)
	}
	if snap.Node.Name != "worker-1" || snap.Node.Policy != "topology-aware" ||
		snap.Node.Version != "v0.0.0-test" {
		t.Errorf("unexpected node: %+v", snap.Node)
	}
	if snap.Hardware == nil || len(snap.Hardware.CPUs) != 16 {
		t.Errorf("unexpected hardware: %+v", snap.Hardware)
	}
	if len(snap.Pods) != 1 || len(snap.Containers) != 1 {
		t.Errorf("expected 1 pod and 1 container, got %d and %d",
			len(snap.Pods), len(snap.Containers))
	}
	if snap.Derived == nil || snap.Derived.CPUCommitment[0] != 1000 {
		t.Errorf("unexpected derived section: %+v", snap.Derived)
	}
	if snap.Policy != nil {
		t.Errorf("policy section must stay empty for now: %+v", snap.Policy)
	}

	if snap.Config == nil {
		t.Fatal("config section missing")
	}
	if snap.Config.Name != "node.worker-1" ||
		snap.Config.Namespace != "kube-system" ||
		snap.Config.Generation != 7 ||
		snap.Config.ResourceVersion != "12345" {
		t.Errorf("unexpected config metadata: %+v", snap.Config)
	}
	if !strings.Contains(string(snap.Config.Data), `"spec":{"pinCPU":true}`) {
		t.Errorf("config data does not carry the configuration: %s",
			snap.Config.Data)
	}

	// A snapshot must marshal, since that is all the server does with it.
	if _, err := json.Marshal(snap); err != nil {
		t.Errorf("failed to marshal snapshot: %v", err)
	}
}

func TestCollectWithoutConfig(t *testing.T) {
	snap := Collect(&Input{NodeName: "worker-1", Policy: "none"})

	if snap.Config != nil {
		t.Errorf("expected no config section, got %+v", snap.Config)
	}
	if snap.Hardware != nil {
		t.Errorf("expected no hardware section, got %+v", snap.Hardware)
	}
	if snap.Derived == nil {
		t.Error("derived section should still be present")
	}
}
```

Add `metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"` and
`cfgapi "github.com/containers/nri-plugins/pkg/apis/config/v1alpha1"` to the
test imports.

- [ ] **Step 2: Run the test to verify it fails**

Run: `go test ./pkg/resmgr/introspect/... -run TestCollect$ -v`

Expected: FAIL — `undefined: Input`, `undefined: Collect`.

- [ ] **Step 3: Write the implementation**

Append to `pkg/resmgr/introspect/collect.go`:

```go
// Input is everything Collect needs. The caller is responsible for making
// sure the state behind it is not being mutated concurrently; the resource
// manager does that by collecting under its read lock.
type Input struct {
	NodeName string
	Policy   string
	Version  string
	System   system.System
	Cache    Cache
	Config   cfgapi.ResmgrConfig
}

// Collect builds a snapshot. The Generation field is left at zero for the
// server to stamp, since only the server knows it.
func Collect(in *Input) *api.Snapshot {
	snap := &api.Snapshot{
		Version:   api.Version,
		Timestamp: time.Now(),
		Node: api.Node{
			Name:    in.NodeName,
			Policy:  in.Policy,
			Version: in.Version,
		},
		Config:   collectConfig(in.Config),
		Hardware: collectHardware(in.System),
	}

	snap.Pods, snap.Containers = collectWorkloads(in.Cache)
	snap.Derived = derive(snap.Hardware, snap.Containers)

	return snap
}

// collectConfig describes the effective configuration.
//
// Data is the configuration as received. Apart from its common section its
// keys are policy-specific, so it is carried verbatim rather than
// interpreted: a consumer that is not itself policy-specific should display
// it, not parse it.
func collectConfig(cfg cfgapi.ResmgrConfig) *api.Config {
	if cfg == nil {
		return nil
	}

	c := &api.Config{}

	if meta := cfg.GetObjectMeta(); meta != nil {
		c.Name = meta.GetName()
		c.Namespace = meta.GetNamespace()
		c.Generation = meta.GetGeneration()
		c.ResourceVersion = meta.GetResourceVersion()
	}

	data, err := json.Marshal(cfg)
	if err != nil {
		log.Warnf("failed to marshal effective configuration: %v", err)
	} else {
		c.Data = json.RawMessage(data)
	}

	return c
}
```

Add `encoding/json`, `time` and
`cfgapi "github.com/containers/nri-plugins/pkg/apis/config/v1alpha1"` to
`collect.go`'s imports.

The concrete types behind `cfgapi.ResmgrConfig` are whole custom resources —
`*cfgapi.TopologyAwarePolicy` and friends — so `Data` carries `metadata`, `spec`
and `status` sections, and a consumer looking for a policy setting finds it under
`spec`. The separate `Name`, `Namespace`, `Generation` and `ResourceVersion`
fields duplicate part of that metadata on purpose, so that the common case needs
no digging into an opaque blob.

Note `cfg == nil` alone is not enough if a typed nil pointer is passed in an
interface. If the test in Step 1 passes but the plugin later panics here,
that is the cause; the resmgr wiring in Task 7 only passes `m.cfg` once it is
set, so it does not arise there.

- [ ] **Step 4: Run the tests to verify they pass**

Run: `go test ./pkg/resmgr/introspect/... -v`

Expected: PASS.

- [ ] **Step 5: Commit**

```bash
gofmt -l pkg/resmgr/introspect/
git add pkg/resmgr/introspect/
git commit -F - <<'EOF'
introspect: assemble node state snapshots.

Collect() ties the hardware, workload, configuration and
derived sections into one snapshot. Generation is left
for the server to stamp, since only it knows the value.

The effective configuration travels verbatim as raw
JSON. Apart from the common section its keys are
policy-specific, so a consumer that is not itself
policy-specific should display it rather than parse it.

Signed-off-by: Krisztian Litkey <krisztian.litkey@intel.com>
Co-Authored-By: Claude Opus 5 (1M context) <noreply@anthropic.com>
EOF
```

---

### Task 6: Configuration knob

**Files:**
- Modify: `pkg/apis/config/v1alpha1/instrumentation/config.go`
- Modify: `deployment/helm/topology-aware/values.yaml`, `deployment/helm/balloons/values.yaml`, `deployment/helm/template/values.yaml`
- Regenerate: `pkg/apis/config/v1alpha1/instrumentation/zz_generated.deepcopy.go`, `deployment/helm/*/crds/*.yaml`

**Interfaces:**
- Consumes: nothing.
- Produces: `instrumentation.Config.Introspection *IntrospectionConfig` with an
  `Enabled bool` field, and `func (c *Config) IntrospectionEnabled() bool`.

Only `enabled` is added. The `ui` knob from the spec arrives with the UI, so
that no setting exists without an effect.

- [ ] **Step 1: Write the failing test**

Create `pkg/apis/config/v1alpha1/instrumentation/config_test.go`:

```go
package instrumentation

import "testing"

func TestIntrospectionEnabled(t *testing.T) {
	for _, tc := range []struct {
		name     string
		config   *Config
		expected bool
	}{
		{
			name:     "unset defaults to disabled",
			config:   &Config{},
			expected: false,
		},
		{
			name:     "present but disabled",
			config:   &Config{Introspection: &IntrospectionConfig{Enabled: false}},
			expected: false,
		},
		{
			name:     "enabled",
			config:   &Config{Introspection: &IntrospectionConfig{Enabled: true}},
			expected: true,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := tc.config.IntrospectionEnabled(); got != tc.expected {
				t.Errorf("got %v, expected %v", got, tc.expected)
			}
		})
	}
}

func TestIntrospectionEnabledNilConfig(t *testing.T) {
	var c *Config
	if c.IntrospectionEnabled() {
		t.Error("a nil config must not enable introspection")
	}
}
```

Add the license header to the file.

- [ ] **Step 2: Run the test to verify it fails**

Run: `go test ./pkg/apis/config/v1alpha1/instrumentation/... -v`

Expected: FAIL — `undefined: IntrospectionConfig`,
`c.IntrospectionEnabled undefined`.

- [ ] **Step 3: Add the config field**

In `pkg/apis/config/v1alpha1/instrumentation/config.go`, add to `Config`, after
the `PrometheusExport` field:

```go
	// Introspection controls the introspection interface, which serves a
	// JSON description of how the active policy has placed containers on
	// this node. It is disabled by default.
	// +optional
	Introspection *IntrospectionConfig `json:"introspection,omitempty"`
```

and after the `Config` type:

```go
// IntrospectionConfig provides runtime configuration for the introspection
// interface.
// +kubebuilder:object:generate=true
type IntrospectionConfig struct {
	// Enabled turns on the introspection endpoints on the HTTP server.
	// +optional
	Enabled bool `json:"enabled,omitempty"`
}

// IntrospectionEnabled returns whether the introspection interface is enabled.
func (c *Config) IntrospectionEnabled() bool {
	if c == nil || c.Introspection == nil {
		return false
	}
	return c.Introspection.Enabled
}
```

- [ ] **Step 4: Run the test to verify it passes**

Run: `go test ./pkg/apis/config/v1alpha1/instrumentation/... -v`

Expected: PASS.

- [ ] **Step 5: Regenerate deepcopy functions and CRDs**

Run: `make generate`

Expected: `pkg/apis/config/v1alpha1/instrumentation/zz_generated.deepcopy.go`
gains `DeepCopy` methods for `IntrospectionConfig` and a deep copy of the new
pointer field in `Config`, and each chart's CRD under
`deployment/helm/*/crds/` gains an `introspection` property with an `enabled`
boolean.

Run: `git status --short` and confirm the changed files are only generated
deepcopy code and CRD YAML.

If `make generate` fails because `controller-gen` is missing, run
`make controller-gen` first.

- [ ] **Step 6: Document the knob in the Helm values**

In each of `deployment/helm/topology-aware/values.yaml`,
`deployment/helm/balloons/values.yaml` and
`deployment/helm/template/values.yaml`, under the existing
`config.instrumentation` block that already has `httpEndpoint: ":8891"`, add:

```yaml
    introspection:
      enabled: false
```

Run: `grep -A 3 'httpEndpoint' deployment/helm/*/values.yaml`

Expected: all three charts show the new block.

- [ ] **Step 7: Verify the whole tree still builds**

Run: `go build ./... && go test ./pkg/apis/... ./pkg/introspect/... ./pkg/resmgr/introspect/...`

Expected: success.

- [ ] **Step 8: Commit**

```bash
gofmt -l pkg/apis/config/v1alpha1/instrumentation/
git add pkg/apis/config/v1alpha1/instrumentation/ deployment/helm/
git commit -F - <<'EOF'
config: add instrumentation.introspection.enabled.

Turn the introspection interface on or off. Disabled by
default: with it off, no handler is registered at all.

Only 'enabled' for now. A 'ui' knob follows with the
visualization, so that no setting exists without an
effect.

Signed-off-by: Krisztian Litkey <krisztian.litkey@intel.com>
Co-Authored-By: Claude Opus 5 (1M context) <noreply@anthropic.com>
EOF
```

---

### Task 7: The server and the snapshot endpoint

**Files:**
- Create: `pkg/resmgr/introspect/introspect.go`
- Test: `pkg/resmgr/introspect/introspect_test.go`
- Modify: `pkg/resmgr/resource-manager.go`

**Interfaces:**
- Consumes: `Collect()`, `Input` from Task 5; `IntrospectionEnabled()` from Task 6.
- Produces: `type CollectFn func() *api.Snapshot`, `func New(mux *xhttp.ServeMux, collect CollectFn) *Server`,
  `func (s *Server) Enable()`, `func (s *Server) Disable()`, `func (s *Server) Invalidate()`,
  and on the resmgr side `func (m *resmgr) setupIntrospection()`,
  `func (m *resmgr) configureIntrospection(cfg *instrumentation.Config)`,
  `func (m *resmgr) collectIntrospection() *api.Snapshot`.

**A deadlock you must not write.** The NRI handlers in `pkg/resmgr/nri.go` hold
the resource manager's write lock for their whole duration (see the `m.Lock()`
calls at lines 360, 382, 427, 467, 545, 591, 653, 692), and `Invalidate()` will
be called from inside them. `collectIntrospection()` takes the resource
manager's read lock. So if the server ever held its own mutex while calling
`collect()`, an HTTP request could hold the server mutex waiting for the resmgr
lock while an NRI handler holds the resmgr lock waiting for the server mutex.
**Never call `collect()` with the server mutex held.** The implementation below
reads the generation under the mutex, releases it, collects, then re-acquires to
store the result.

- [ ] **Step 1: Write the failing test**

Create `pkg/resmgr/introspect/introspect_test.go` with the license header, then:

```go
package introspect

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	xhttp "github.com/containers/nri-plugins/pkg/http"
	"github.com/containers/nri-plugins/pkg/introspect/api"
)

func testSnapshot(node string) *api.Snapshot {
	return &api.Snapshot{
		Version: api.Version,
		Node:    api.Node{Name: node, Policy: "test"},
	}
}

func TestServeSnapshot(t *testing.T) {
	mux := xhttp.NewServeMux()
	s := New(mux, func() *api.Snapshot { return testSnapshot("worker-1") })
	s.Enable()

	w := httptest.NewRecorder()
	mux.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/introspect", nil))

	if w.Code != http.StatusOK {
		t.Fatalf("status %d, expected %d", w.Code, http.StatusOK)
	}
	if ct := w.Header().Get("Content-Type"); ct != "application/json" {
		t.Errorf("content type %q, expected application/json", ct)
	}

	snap := &api.Snapshot{}
	if err := json.Unmarshal(w.Body.Bytes(), snap); err != nil {
		t.Fatalf("failed to unmarshal response: %v", err)
	}
	if snap.Node.Name != "worker-1" {
		t.Errorf("unexpected node name %q", snap.Node.Name)
	}
	if snap.Generation != 1 {
		t.Errorf("generation %d, expected 1", snap.Generation)
	}
}

func TestDisabledRegistersNothing(t *testing.T) {
	mux := xhttp.NewServeMux()
	New(mux, func() *api.Snapshot { return testSnapshot("worker-1") })

	w := httptest.NewRecorder()
	mux.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/introspect", nil))

	if w.Code != http.StatusNotFound {
		t.Errorf("status %d, expected %d for a server never enabled",
			w.Code, http.StatusNotFound)
	}
}

func TestDisableUnregisters(t *testing.T) {
	mux := xhttp.NewServeMux()
	s := New(mux, func() *api.Snapshot { return testSnapshot("worker-1") })

	s.Enable()
	s.Disable()

	w := httptest.NewRecorder()
	mux.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/introspect", nil))

	if w.Code != http.StatusNotFound {
		t.Errorf("status %d, expected %d after Disable", w.Code, http.StatusNotFound)
	}

	// Enable must work again afterwards.
	s.Enable()
	w = httptest.NewRecorder()
	mux.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/introspect", nil))
	if w.Code != http.StatusOK {
		t.Errorf("status %d, expected %d after re-enabling", w.Code, http.StatusOK)
	}
}

func TestSnapshotIsCachedUntilInvalidated(t *testing.T) {
	var collected atomic.Int64

	mux := xhttp.NewServeMux()
	s := New(mux, func() *api.Snapshot {
		collected.Add(1)
		return testSnapshot("worker-1")
	})
	s.Enable()

	get := func() *api.Snapshot {
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/introspect", nil))
		snap := &api.Snapshot{}
		if err := json.Unmarshal(w.Body.Bytes(), snap); err != nil {
			t.Fatalf("failed to unmarshal response: %v", err)
		}
		return snap
	}

	first := get()
	second := get()

	if n := collected.Load(); n != 1 {
		t.Errorf("collected %d times for two requests, expected 1", n)
	}
	if first.Generation != second.Generation {
		t.Errorf("generation changed without an invalidation: %d then %d",
			first.Generation, second.Generation)
	}

	s.Invalidate()
	third := get()

	if n := collected.Load(); n != 2 {
		t.Errorf("collected %d times after invalidation, expected 2", n)
	}
	if third.Generation <= second.Generation {
		t.Errorf("generation %d did not advance past %d",
			third.Generation, second.Generation)
	}
}

func TestNothingCollectedWithoutRequests(t *testing.T) {
	var collected atomic.Int64

	mux := xhttp.NewServeMux()
	s := New(mux, func() *api.Snapshot {
		collected.Add(1)
		return testSnapshot("worker-1")
	})
	s.Enable()

	for i := 0; i < 10; i++ {
		s.Invalidate()
	}

	if n := collected.Load(); n != 0 {
		t.Errorf("collected %d times with no request pending, expected 0", n)
	}
}

func TestInvalidateDoesNotBlockOnCollect(t *testing.T) {
	// Invalidate() is called from NRI handlers that hold the resource
	// manager's write lock, while collect() takes its read lock. If
	// Invalidate() could block behind a collect(), that is a deadlock.
	release := make(chan struct{})
	entered := make(chan struct{})

	mux := xhttp.NewServeMux()
	s := New(mux, func() *api.Snapshot {
		close(entered)
		<-release
		return testSnapshot("worker-1")
	})
	s.Enable()

	go func() {
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/introspect", nil))
	}()

	<-entered // a collect() is now in flight

	done := make(chan struct{})
	go func() {
		s.Invalidate()
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("Invalidate() blocked while a collect() was in flight")
	}

	close(release)
}
```

Add `time` to the test imports.

- [ ] **Step 2: Run the test to verify it fails**

Run: `go test ./pkg/resmgr/introspect/... -run 'TestServe|TestDisabled|TestDisable|TestSnapshotIs|TestNothing|TestInvalidate' -v`

Expected: FAIL — `undefined: New`.

- [ ] **Step 3: Write the implementation**

Create `pkg/resmgr/introspect/introspect.go` with the license header, then:

```go
package introspect

import (
	"encoding/json"
	"net/http"
	"sync"

	xhttp "github.com/containers/nri-plugins/pkg/http"
	"github.com/containers/nri-plugins/pkg/introspect/api"
)

const (
	// SnapshotPath serves the node state snapshot.
	SnapshotPath = "/introspect"
	// EventsPath serves change notifications.
	EventsPath = "/introspect/events"
)

// CollectFn returns a fresh snapshot. It must be safe to call from an HTTP
// handler goroutine; the resource manager's implementation takes its read
// lock.
type CollectFn func() *api.Snapshot

// Server serves the introspection interface.
//
// The snapshot is built lazily and cached per generation, so with nobody
// asking, nothing is ever collected or marshalled. Invalidate() only bumps
// a counter and wakes subscribers.
type Server struct {
	mu         sync.Mutex
	mux        *xhttp.ServeMux
	collect    CollectFn
	enabled    bool
	generation uint64
	cachedGen  uint64
	cached     []byte
	subs       map[chan uint64]struct{}
}

// New creates a server. It registers no handlers until Enable is called.
func New(mux *xhttp.ServeMux, collect CollectFn) *Server {
	return &Server{
		mux:        mux,
		collect:    collect,
		generation: 1,
		subs:       map[chan uint64]struct{}{},
	}
}

// Enable starts serving the introspection endpoints. It is idempotent.
func (s *Server) Enable() {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.enabled {
		return
	}

	log.Infof("enabling introspection interface...")

	s.mux.HandleFunc(SnapshotPath, s.serveSnapshot)
	s.mux.HandleFunc(EventsPath, s.serveEvents)
	s.enabled = true
}

// Disable stops serving and drops any subscribers. It is idempotent.
func (s *Server) Disable() {
	s.mu.Lock()
	defer s.mu.Unlock()

	if !s.enabled {
		return
	}

	log.Infof("disabling introspection interface...")

	s.mux.Unregister(SnapshotPath)
	s.mux.Unregister(EventsPath)
	s.enabled = false
	s.cached, s.cachedGen = nil, 0

	for ch := range s.subs {
		close(ch)
		delete(s.subs, ch)
	}
}

// Invalidate marks the cached snapshot stale and wakes subscribers.
//
// This is called from NRI event handlers while they hold the resource
// manager's write lock, so it must never block on anything that needs that
// lock. It takes this server's mutex briefly and sends without blocking.
func (s *Server) Invalidate() {
	s.mu.Lock()

	s.generation++
	gen := s.generation

	subs := make([]chan uint64, 0, len(s.subs))
	for ch := range s.subs {
		subs = append(subs, ch)
	}

	s.mu.Unlock()

	for _, ch := range subs {
		notify(ch, gen)
	}
}

// notify delivers a generation without ever blocking. The channel is
// buffered for one value: if a notification is already pending the
// subscriber has not read it yet, and since it refetches the current
// snapshot anyway, an older pending value loses nothing.
func notify(ch chan uint64, gen uint64) {
	select {
	case ch <- gen:
	default:
	}
}

// snapshot returns the marshalled snapshot for the current generation,
// collecting it if the cache is stale.
//
// collect() is deliberately called with the mutex released: it takes the
// resource manager's read lock, and Invalidate() runs while that lock is
// held for writing. Holding this mutex across collect() would deadlock.
func (s *Server) snapshot() ([]byte, error) {
	s.mu.Lock()
	gen := s.generation
	if s.cached != nil && s.cachedGen == gen {
		data := s.cached
		s.mu.Unlock()
		return data, nil
	}
	collect := s.collect
	s.mu.Unlock()

	snap := collect()
	if snap == nil {
		snap = &api.Snapshot{Version: api.Version}
	}
	snap.Generation = gen

	data, err := json.Marshal(snap)
	if err != nil {
		return nil, err
	}

	s.mu.Lock()
	// Another request may have finished a newer collection meanwhile;
	// keep whichever is newer.
	if s.cached == nil || s.cachedGen <= gen {
		s.cached, s.cachedGen = data, gen
	}
	s.mu.Unlock()

	return data, nil
}

// serveSnapshot serves the node state snapshot.
func (s *Server) serveSnapshot(w http.ResponseWriter, req *http.Request) {
	data, err := s.snapshot()
	if err != nil {
		log.Errorf("failed to produce snapshot: %v", err)
		http.Error(w, "failed to produce snapshot", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	if _, err := w.Write(data); err != nil {
		log.Debugf("failed to write snapshot response: %v", err)
	}
}
```

`serveEvents` lives in `events.go` and arrives in Task 8, so for this task
**omit the two `EventsPath` lines**: register only `SnapshotPath` in `Enable()`
and unregister only it in `Disable()`. Task 8 adds both lines back. Everything
else above — including the `subs` map and the subscriber wake-up in
`Invalidate()` — stays exactly as written; with no subscribers the map is empty
and the loop does nothing, which keeps Task 8 to an addition rather than a
rewrite.

- [ ] **Step 4: Run the tests to verify they pass**

Run: `go test ./pkg/resmgr/introspect/... -race -v`

Expected: PASS. `-race` matters here: these tests exercise concurrent
collection and invalidation.

- [ ] **Step 5: Wire it into the resource manager**

In `pkg/resmgr/resource-manager.go`:

Add to the `resmgr` struct, after the `dra` field:

```go
	introspect *introspect.Server // introspection interface
```

Add the imports:

```go
	"github.com/containers/nri-plugins/pkg/introspect/api"
	"github.com/containers/nri-plugins/pkg/resmgr/introspect"
	cfgintr "github.com/containers/nri-plugins/pkg/apis/config/v1alpha1/instrumentation"
```

Add these methods next to `setupHealthCheck()`:

```go
// setupIntrospection prepares the introspection interface. It is created
// disabled; configuration decides whether it is enabled.
func (m *resmgr) setupIntrospection() {
	mux := instrumentation.HTTPServer().GetMux()
	m.introspect = introspect.New(mux, m.collectIntrospection)
}

// configureIntrospection enables or disables introspection per config.
func (m *resmgr) configureIntrospection(cfg *cfgintr.Config) {
	if m.introspect == nil {
		return
	}
	if cfg.IntrospectionEnabled() {
		m.introspect.Enable()
	} else {
		m.introspect.Disable()
	}
}

// collectIntrospection collects a snapshot of our state.
//
// This runs on an HTTP handler goroutine, so it takes the read lock to
// avoid collecting halfway through an allocation. A request can therefore
// block behind a long allocation, which is acceptable for a read-only
// inspection interface.
func (m *resmgr) collectIntrospection() *api.Snapshot {
	m.RLock()
	defer m.RUnlock()

	policyName := ""
	if m.policy != nil {
		policyName = m.policy.ActivePolicy()
	}

	return introspect.Collect(&introspect.Input{
		NodeName: m.agent.NodeName(),
		Policy:   policyName,
		Version:  version.Version,
		System:   m.cache.GetSystem(),
		Cache:    introspect.CacheOf(m.cache),
		Config:   m.cfg,
	})
}
```

Call `m.setupIntrospection()` in `NewResourceManager()`, right after
`m.setupHealthCheck()`.

Call `m.configureIntrospection(&mCfg.Instrumentation)` in **both** places that
call `instrumentation.Reconfigure(&mCfg.Instrumentation)`: in `start()` and in
the `apply` closure inside `reconfigure()`. Place it immediately after the
`instrumentation.Reconfigure` call, so that the HTTP server is configured before
handlers are registered on it.

Two unknowns to resolve while writing this, rather than guessing:

1. `m.cache.GetSystem()` — check the real accessor with
   `grep -n 'System()' pkg/resmgr/cache/cache.go`. If the cache does not expose
   the discovered system, get it the way `setupPolicy()` does and store it on
   `resmgr` during `NewResourceManager()`.
2. `version.Version` — confirm the symbol with
   `go doc github.com/containers/nri-plugins/pkg/version`, and import it.

- [ ] **Step 6: Add the cache adapter**

`cache.Cache.GetPods()` returns `[]cache.Pod`, not `[]Pod`, so it cannot satisfy
the `Cache` interface directly. Append to `pkg/resmgr/introspect/collect.go`:

```go
// cacheAdapter adapts the resource manager cache to our Cache interface.
// Go does not convert []cache.Pod to []Pod implicitly, so the slices are
// rebuilt. It is a handful of pointers per request on a read-only path.
type cacheAdapter struct {
	cache cache.Cache
}

// CacheOf adapts a resource manager cache for collection.
func CacheOf(c cache.Cache) Cache {
	if c == nil {
		return nil
	}
	return &cacheAdapter{cache: c}
}

func (a *cacheAdapter) GetPods() []Pod {
	pods := a.cache.GetPods()
	out := make([]Pod, 0, len(pods))
	for _, p := range pods {
		out = append(out, p)
	}
	return out
}

func (a *cacheAdapter) GetContainers() []Container {
	containers := a.cache.GetContainers()
	out := make([]Container, 0, len(containers))
	for _, c := range containers {
		out = append(out, c)
	}
	return out
}
```

- [ ] **Step 7: Verify the plugin builds and behaves**

Run: `go build ./... && go test ./pkg/resmgr/... ./pkg/introspect/... -race`

Expected: success.

- [ ] **Step 8: Commit**

```bash
gofmt -l pkg/resmgr/
git add pkg/resmgr/
git commit -F - <<'EOF'
introspect: serve node state over HTTP.

Register /introspect on the instrumentation HTTP server
when configuration enables it, and nothing at all when
it does not.

The snapshot is built lazily and cached per generation,
so an idle node collects and marshals nothing. The
removed introspection API re-marshalled its whole state
on every container start whether or not anyone was
listening.

collect() takes the resource manager's read lock and is
never called with the server's own mutex held: NRI
handlers invalidate while holding that lock for writing,
so the other order deadlocks. A test pins this.

Signed-off-by: Krisztian Litkey <krisztian.litkey@intel.com>
Co-Authored-By: Claude Opus 5 (1M context) <noreply@anthropic.com>
EOF
```

---

### Task 8: Change notification over SSE

**Files:**
- Create: `pkg/resmgr/introspect/events.go`
- Test: `pkg/resmgr/introspect/introspect_test.go` (append)
- Modify: `pkg/resmgr/introspect/introspect.go` (restore `EventsPath` registration and subscriber wake-up if Task 7 left them out)

**Interfaces:**
- Consumes: `Server`, `notify()`, `EventsPath` from Task 7.
- Produces: `func (s *Server) serveEvents(w http.ResponseWriter, req *http.Request)`,
  `func (s *Server) subscribe() (chan uint64, bool)`, `func (s *Server) unsubscribe(ch chan uint64)`.

- [ ] **Step 1: Write the failing test**

Append to `pkg/resmgr/introspect/introspect_test.go`:

```go
func TestServeEvents(t *testing.T) {
	mux := xhttp.NewServeMux()
	s := New(mux, func() *api.Snapshot { return testSnapshot("worker-1") })
	s.Enable()

	req := httptest.NewRequest(http.MethodGet, "/introspect/events", nil)
	ctx, cancel := context.WithCancel(req.Context())
	defer cancel()
	req = req.WithContext(ctx)

	w := httptest.NewRecorder()

	served := make(chan struct{})
	go func() {
		mux.ServeHTTP(w, req)
		close(served)
	}()

	// Wait until the subscriber is registered, then invalidate twice.
	for i := 0; i < 100; i++ {
		if s.subscribers() > 0 {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if s.subscribers() != 1 {
		t.Fatalf("expected 1 subscriber, got %d", s.subscribers())
	}

	s.Invalidate()
	s.Invalidate()

	// Give the handler a moment to write, then shut it down.
	time.Sleep(100 * time.Millisecond)
	cancel()

	select {
	case <-served:
	case <-time.After(2 * time.Second):
		t.Fatal("handler did not return after the request was cancelled")
	}

	if ct := w.Header().Get("Content-Type"); ct != "text/event-stream" {
		t.Errorf("content type %q, expected text/event-stream", ct)
	}

	body := w.Body.String()
	if !strings.Contains(body, "data: {\"generation\":") {
		t.Errorf("no generation event in the stream:\n%s", body)
	}

	if s.subscribers() != 0 {
		t.Errorf("subscriber not cleaned up: %d remain", s.subscribers())
	}
}

func TestEventSubscriberLimit(t *testing.T) {
	mux := xhttp.NewServeMux()
	s := New(mux, func() *api.Snapshot { return testSnapshot("worker-1") })
	s.Enable()

	// Fill the subscriber table directly; the HTTP path is covered above.
	var channels []chan uint64
	for i := 0; i < maxSubscribers; i++ {
		ch, ok := s.subscribe()
		if !ok {
			t.Fatalf("subscriber %d rejected below the limit", i)
		}
		channels = append(channels, ch)
	}

	if _, ok := s.subscribe(); ok {
		t.Errorf("subscriber %d accepted above the limit of %d",
			maxSubscribers+1, maxSubscribers)
	}

	s.unsubscribe(channels[0])

	if _, ok := s.subscribe(); !ok {
		t.Error("subscribing failed after a slot was freed")
	}
}

func TestEventsRejectedAboveLimit(t *testing.T) {
	mux := xhttp.NewServeMux()
	s := New(mux, func() *api.Snapshot { return testSnapshot("worker-1") })
	s.Enable()

	for i := 0; i < maxSubscribers; i++ {
		if _, ok := s.subscribe(); !ok {
			t.Fatalf("subscriber %d rejected below the limit", i)
		}
	}

	w := httptest.NewRecorder()
	mux.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/introspect/events", nil))

	if w.Code != http.StatusServiceUnavailable {
		t.Errorf("status %d, expected %d when full",
			w.Code, http.StatusServiceUnavailable)
	}
}
```

Add `context` and `strings` to the test imports.

- [ ] **Step 2: Run the test to verify it fails**

Run: `go test ./pkg/resmgr/introspect/... -run 'TestServeEvents|TestEvent' -v`

Expected: FAIL — `undefined: maxSubscribers`, `s.subscribers undefined`,
`s.subscribe undefined`, `s.unsubscribe undefined`.

- [ ] **Step 3: Write the implementation**

Create `pkg/resmgr/introspect/events.go` with the license header, then:

```go
package introspect

import (
	"fmt"
	"net/http"
	"time"
)

const (
	// maxSubscribers caps concurrent event subscribers, so that the
	// endpoint cannot be used to pin an unbounded number of goroutines
	// and buffers.
	maxSubscribers = 16
	// heartbeatInterval is how often a comment is written to an idle
	// stream. Without it, kubectl port-forward and intervening proxies
	// eventually reap the connection.
	heartbeatInterval = 30 * time.Second
)

// subscribe registers an event subscriber, returning false if we are full.
func (s *Server) subscribe() (chan uint64, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if len(s.subs) >= maxSubscribers {
		return nil, false
	}

	// Buffered for one value: notify() never blocks, and a subscriber
	// refetches the current snapshot anyway, so a coalesced older
	// generation loses nothing.
	ch := make(chan uint64, 1)
	s.subs[ch] = struct{}{}

	return ch, true
}

// unsubscribe removes an event subscriber.
func (s *Server) unsubscribe(ch chan uint64) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if _, ok := s.subs[ch]; ok {
		delete(s.subs, ch)
	}
}

// subscribers returns the number of current event subscribers.
func (s *Server) subscribers() int {
	s.mu.Lock()
	defer s.mu.Unlock()

	return len(s.subs)
}

// serveEvents streams change notifications as Server-Sent Events.
//
// Each event carries only a generation number; the client refetches the
// snapshot. That way the server marshals nothing until someone asks, and a
// slow client cannot make a producer wait.
func (s *Server) serveEvents(w http.ResponseWriter, req *http.Request) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming unsupported", http.StatusInternalServerError)
		return
	}

	ch, ok := s.subscribe()
	if !ok {
		http.Error(w, "too many subscribers", http.StatusServiceUnavailable)
		return
	}
	defer s.unsubscribe(ch)

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Connection", "keep-alive")
	w.WriteHeader(http.StatusOK)
	flusher.Flush()

	heartbeat := time.NewTicker(heartbeatInterval)
	defer heartbeat.Stop()

	for {
		select {
		case <-req.Context().Done():
			return

		case gen, ok := <-ch:
			if !ok {
				// Disable() closed us.
				return
			}
			if _, err := fmt.Fprintf(w, "data: {\"generation\":%d}\n\n", gen); err != nil {
				log.Debugf("failed to write event: %v", err)
				return
			}
			flusher.Flush()

		case <-heartbeat.C:
			if _, err := fmt.Fprintf(w, ": heartbeat\n\n"); err != nil {
				log.Debugf("failed to write heartbeat: %v", err)
				return
			}
			flusher.Flush()
		}
	}
}
```

Now add the two lines Task 7 left out. In `Enable()`:

```go
	s.mux.HandleFunc(EventsPath, s.serveEvents)
```

and in `Disable()`:

```go
	s.mux.Unregister(EventsPath)
```

`Invalidate()` needs no change: Task 7 already wrote its subscriber wake-up.

Note the `defer s.unsubscribe(ch)` combined with `Disable()` closing channels:
`unsubscribe` checks membership before deleting, so a channel `Disable()`
already removed is not deleted twice, and the handler returns on the closed
channel.

- [ ] **Step 4: Run the tests to verify they pass**

Run: `go test ./pkg/resmgr/introspect/... -race -v`

Expected: PASS, all tests including Tasks 1 through 7.

- [ ] **Step 5: Commit**

```bash
gofmt -l pkg/resmgr/introspect/
git add pkg/resmgr/introspect/
git commit -F - <<'EOF'
introspect: push change notifications over SSE.

/introspect/events streams a generation number whenever
placement changes, and the client refetches. Plain HTTP,
no library, and EventSource reconnects by itself when
the plugin pod restarts.

Only the number travels, so nothing is marshalled until
someone asks for the snapshot. Notifications coalesce
per subscriber, so a slow client never holds up a
producer. A comment heartbeat keeps port-forwards and
proxies from reaping idle streams, and subscribers are
capped.

Signed-off-by: Krisztian Litkey <krisztian.litkey@intel.com>
Co-Authored-By: Claude Opus 5 (1M context) <noreply@anthropic.com>
EOF
```

---

### Task 9: Invalidate where placement changes

**Files:**
- Modify: `pkg/resmgr/resource-manager.go`
- Modify: `pkg/resmgr/nri.go:334,516,668`

**Interfaces:**
- Consumes: `(*Server).Invalidate()` from Task 7.
- Produces: `func (m *resmgr) publishNodeState()`.

`updateTopologyZones()` is called from exactly four places, all of them points
where placement can change, and every one of them already calls
`updateNodeExtendedResources()` immediately afterwards:

| Site | Context |
|---|---|
| `resource-manager.go`, in `updateConfig()` | configuration update |
| `nri.go:334` | `nriPlugin.Synchronize()` |
| `nri.go:516` | `nriPlugin.CreateContainer()` |
| `nri.go:668` | `nriPlugin.StopContainer()` |

Introspection invalidation is a third such call, so the three collapse into one
helper. That is a tidy-up of the code being touched, and it keeps a future
publisher from being forgotten at one of the four.

- [ ] **Step 1: Add the helper**

In `pkg/resmgr/resource-manager.go`, next to `updateTopologyZones()`:

```go
// publishNodeState publishes our state to everything that consumes it.
//
// Call this wherever resource assignments may have changed. Keeping the
// publishers together means a new one cannot be forgotten at one of the
// call sites.
func (m *resmgr) publishNodeState() {
	m.updateTopologyZones()
	m.updateNodeExtendedResources()

	if m.introspect != nil {
		m.introspect.Invalidate()
	}
}
```

`Invalidate()` is safe to call with the resource manager's write lock held: it
takes only the server's own mutex, briefly, and never collects. See the comment
on `Invalidate()` and `TestInvalidateDoesNotBlockOnCollect`.

- [ ] **Step 2: Replace the four call sites**

At each of the four sites, replace the pair

```go
	m.updateTopologyZones()
	m.updateNodeExtendedResources()
```

with

```go
	m.publishNodeState()
```

Run: `grep -rn 'updateTopologyZones()\|updateNodeExtendedResources()' pkg/resmgr/`

Expected: each appears exactly twice — once as its own declaration and once
inside `publishNodeState()`. Anything else is a site you missed.

- [ ] **Step 3: Verify it builds and the suite still passes**

Run: `go build ./... && go test ./pkg/resmgr/... -race`

Expected: success.

- [ ] **Step 4: Commit**

```bash
gofmt -l pkg/resmgr/
git add pkg/resmgr/
git commit -F - <<'EOF'
resmgr: collect publishers into publishNodeState().

The four places where assignments can change each
published topology zones and extended resources back to
back, and now also invalidate introspection. Fold the
three calls into one helper so a new publisher cannot be
forgotten at one of the sites.

Invalidating is safe under the resource manager's write
lock: it takes only the introspection server's own mutex
and never collects.

Signed-off-by: Krisztian Litkey <krisztian.litkey@intel.com>
Co-Authored-By: Claude Opus 5 (1M context) <noreply@anthropic.com>
EOF
```

---

### Task 10: End-to-end verification

**Files:**
- Modify: `test/e2e/lib/test.bash`
- Modify: `test/e2e/policies.test-suite/topology-aware/helm-config.yaml.in`
- Create: `test/e2e/policies.test-suite/topology-aware/n4c16/test42-introspection/code.var.sh`

**Interfaces:**
- Consumes: the running endpoint from Tasks 7 through 9.
- Produces: `verify-introspect-jq` and `verify-introspect-disabled` shell
  helpers.

`helm-launch` already enables a port-forward of 8891 (see
`vm-port-forward-enable` in `test/e2e/lib/vm.bash`, called from
`test/e2e/run.sh`), which is how the existing `verify-metrics-has-line` helper
reaches the plugin. The new helpers use the same route.

- [ ] **Step 1: Add the shell helpers**

In `test/e2e/lib/test.bash`, in the "Metrics and instrumentation" section, after
`verify-metrics-has-no-line()`, add:

```bash
verify_introspect_url="$instrumentation_url/introspect"

verify-introspect-jq() { # script API
    # Usage: verify-introspect-jq JQ-FILTER [TIMEOUT]
    #
    # Wait until the introspection snapshot of the plugin satisfies
    # JQ-FILTER, that is, until the filter outputs "true". Fail the test on
    # timeout, 10s by default.
    local filter="$1"
    vm-run-until --timeout "${2:-10}" "echo 'waiting for introspection: $filter' >&2; curl --silent --noproxy localhost $verify_introspect_url | jq -e '$filter' >/dev/null" || {
        vm-command "curl --silent --noproxy localhost $verify_introspect_url | jq ."
        command-error "introspection snapshot does not satisfy '$filter'"
    }
}

verify-introspect-disabled() { # script API
    # Usage: verify-introspect-disabled
    #
    # Verify that the introspection endpoint is not served at all.
    vm-command "curl --silent --show-error --output /dev/null --write-out '%{http_code}' --noproxy localhost $verify_introspect_url" || true
    if [ "$COMMAND_OUTPUT" != "404" ]; then
        command-error "expected HTTP 404 from $verify_introspect_url, got '$COMMAND_OUTPUT'"
    fi
}
```

Confirm the variable holding a command's output really is `COMMAND_OUTPUT`:

Run: `grep -n 'COMMAND_OUTPUT' test/e2e/lib/*.bash | head`

If the framework names it differently, use that name. Do not invent one.

Also confirm `jq` is available in the test VM:

Run: `grep -rn 'jq' test/e2e/lib/vm.bash | head`

If it is not installed, add the installation to the VM provisioning the same way
other tools are added there, in this task.

- [ ] **Step 2: Add the configuration knob to the e2e template**

In `test/e2e/policies.test-suite/topology-aware/helm-config.yaml.in`, replace

```yaml
  instrumentation:
    httpEndpoint: ":8891"
```

with

```yaml
  instrumentation:
    httpEndpoint: ":8891"
    introspection:
      enabled: ${INTROSPECTION:-false}
```

- [ ] **Step 3: Write the test case**

Create `test/e2e/policies.test-suite/topology-aware/n4c16/test42-introspection/code.var.sh`:

```bash
# Test that the introspection interface describes the node and the
# placement of containers, and that it is not served unless enabled.

helm-terminate

# With introspection disabled, the endpoint must not exist at all.
helm_config=$(instantiate helm-config.yaml) helm-launch topology-aware
verify-introspect-disabled
helm-terminate

helm_config=$(INTROSPECTION=true instantiate helm-config.yaml) helm-launch topology-aware

# The snapshot describes the machine before any workload exists.
verify-introspect-jq '.version == "v1alpha1"'
verify-introspect-jq '.node.policy == "topology-aware"'
verify-introspect-jq '(.hardware.cpus | length) == 16'
verify-introspect-jq '(.hardware.numaNodes | length) == 4'
verify-introspect-jq '(.hardware.cpus | map(.id) | unique | length) == 16'

# The effective configuration travels with it. The concrete configuration
# type is the whole custom resource, so its spec is one level down.
verify-introspect-jq '.config.data.spec.pinCPU == true'
verify-introspect-jq '.config.name != ""'

# A guaranteed pod shows up with its applied cpuset, and the CPUs it got
# are fully committed.
CPU=2 MEM=100M create guaranteed
report allowed

verify-introspect-jq '[.containers[] | select(.name == "pod0c0")] | length == 1'
verify-introspect-jq '[.containers[] | select(.name == "pod0c0")][0].cpuRequest == 2000'
verify-introspect-jq '[.containers[] | select(.name == "pod0c0")][0].applied.cpusetCpus != ""'
verify-introspect-jq '
    [.derived.cpuCommitment | to_entries[] | select(.value == 1000)] | length == 2
    ' 15

# The pod's container is gone from the snapshot once it is deleted.
kubectl delete pods pod0 --now --ignore-not-found
verify-introspect-jq '[.containers[] | select(.name == "pod0c0")] | length == 0'

# Commitment is reported honestly when a container is oversubscribed on
# the CPUs it is allowed to use.
CPU=1 MEM=100M CPUREQ=1 CPULIM=1 create burstable
report allowed
verify-introspect-jq '(.derived.cpuCommitment | to_entries | map(.value) | add) > 0'

helm-terminate
```

The commitment assertion checks the property rather than the identity of the
CPUs: a guaranteed pod asking for two CPUs should leave exactly two CPUs fully
committed at 1000 milli-CPU each, whichever two the policy picked. Tying it to
specific CPU ids would duplicate what `report allowed` and the other
topology-aware tests already assert, and would break whenever pool selection
changes.

- [ ] **Step 4: Run the e2e case**

Run: `cd test/e2e && ./run_tests.sh policies.test-suite/topology-aware/n4c16/test42-introspection`

Expected: PASS. Check the exact invocation the suite expects first with
`sed -n '1,40p' test/e2e/run_tests.sh`; the e2e framework needs a VM and takes
several minutes on a cold start.

If a VM is unavailable in this environment, say so explicitly rather than
marking this step done, and verify by hand instead: build the plugin, run it
against a local configuration with introspection enabled, and
`curl localhost:8891/introspect | jq .` to confirm the four `hardware`
assertions and the `config.data` assertion by eye.

- [ ] **Step 5: Commit**

```bash
git add test/e2e/
git commit -F - <<'EOF'
e2e: verify the introspection interface.

Check that the snapshot describes the machine, carries
the effective configuration, reports a guaranteed pod's
applied cpuset and its commitment, and drops the
container when the pod goes away.

Also check that nothing is served when introspection is
disabled, which is the default.

Signed-off-by: Krisztian Litkey <krisztian.litkey@intel.com>
Co-Authored-By: Claude Opus 5 (1M context) <noreply@anthropic.com>
EOF
```

---

## Done criteria

- `go test ./pkg/introspect/... ./pkg/resmgr/... -race` passes.
- `go list -deps ./pkg/introspect/api` shows no non-standard-library dependency.
- With `instrumentation.introspection.enabled: false`, `curl :8891/introspect`
  returns 404.
- With it enabled, `curl :8891/introspect | jq .` returns a snapshot whose
  `hardware`, `pods`, `containers`, `config` and `derived` sections are
  populated, and whose `policy` section is absent.
- `curl -N :8891/introspect/events` prints a `data: {"generation":N}` line
  within a second of a pod being created or deleted, and a `: heartbeat`
  comment on an idle node.
- `make generate` produces no further diff.

## Follow-up plans

- **Visualization** (spec increment 3): the machine map, commitment ramp,
  pinning, table view, configuration panel and delta pulses, plus the `ui`
  config knob and a checked-in sample snapshot. Depends on this plan's wire
  format being frozen.
- **Policy layer** (spec increments 4 and 5): the `Introspector` interface,
  topology-aware and balloons implementations with their pinning notices, the
  missing `cpuset` zone attribute for topology-aware, and rendering zones,
  assignment columns and notices in the UI.
- **Documentation** (spec increment 6).

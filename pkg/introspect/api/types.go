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
type NUMANode struct {
	ID           int    `json:"id"`
	Package      int    `json:"package"`
	Die          int    `json:"die"`
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

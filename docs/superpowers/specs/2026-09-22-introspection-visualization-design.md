# Introspection API and placement visualization

Design document. 2026-09-22.

## Problem

An operator running one of the resource policies has no way to see what the
policy did to a node. The plugin logs its decisions and publishes a
`NodeResourceTopology` CR, but neither answers the questions people actually
ask: which CPUs did my container get, is this node packed, are two workloads
sharing CPUs they should not be, did memory land where the policy said it
would.

Two removed pieces of code attacked this before:

- `pkg/resmgr/introspect` (removed in 3a00470112) served a single JSON blob at
  `/introspect` describing pools, pods, containers and assignments. Only the
  topology-aware policy ever implemented its backend half; balloons and
  template returned nothing. The whole state was re-marshalled on every update
  whether or not anyone was listening.
- `pkg/visualizer` plus its `bubbles` UI (removed in fb14c57756) served
  embedded and filesystem-discovered UIs under `/ui`. The one UI was a d3 v4
  circle-packing prototype: pool tree into `d3.pack()`, circle area by CPU
  request, an inner red circle for the limit/request ratio, click to zoom. It
  showed neither the machine nor the pools legibly, could not represent
  balloons, and never updated.

Both are inspiration only. The mechanism — an optionally enabled HTTP endpoint
serving JSON — is what we keep.

## Goals

- Serve a complete, self-describing JSON description of a node's placement
  state over an optional HTTP endpoint.
- Ship a visualization that answers "where did my container land" at a glance,
  for any policy.
- Make the data useful without the UI: `curl | jq` is a first-class consumer,
  and the snapshot is what someone attaches to a bug report.
- Never let the interface rot when a policy does not participate.

## Non-goals

- Cluster-wide aggregation. One plugin instance describes its own node. The
  schema must not make aggregation harder later, but no aggregator is built.
- Any write or control path. Everything here is read-only.
- Runtime resource *usage*. The plugin knows allocations and requests, not
  actual consumption. Usage belongs to metrics and existing monitoring.
- Historical state or a timeline scrubber.

## Audience and deployment shape

The primary user is a cluster operator or plugin user, not a policy developer.
The plugin is a DaemonSet, so each instance serves both the JSON and the UI for
its own node on its existing instrumentation HTTP endpoint. Reaching it is a
`kubectl port-forward` away — the same path e2e already uses for `/metrics`.
No new component, no new listener, no cluster-wide auth story.

## Architecture

### Two layers of data

The central constraint: **the policy-agnostic layer is complete and sufficient
on its own, and everything a policy contributes is strictly additive.**

**Layer 1 — policy-agnostic.** Gathered from `pkg/sysfs` and the resource
manager cache, with no policy involvement, so it can neither go stale nor be
missing:

- hardware: packages, dies, clusters, NUMA nodes, per-CPU detail, isolated and
  offline CPUs, core kinds;
- pods and containers: identity, QoS, requests and limits, topology hints, and
  what was *actually applied* to the container (`cpuset.cpus`, `cpuset.mems`,
  CPU shares/quota/period, memory limit, class assignments);
- the effective configuration the plugin is running with, as received.

This drives a genuinely useful machine view, because the interesting relations
are derivable from it alone:

- CPU cell occupancy: which containers' applied `cpuset.cpus` cover a CPU.
- Exclusive versus shared: the number of containers whose cpuset covers a CPU.
  One means effectively exclusive, several means shared — and names them.
- NUMA node occupancy: from applied `cpuset.mems` against sysfs capacity.
- Commitment per CPU: see "Commitment" below.

**Layer 2 — policy-provided.** An optional `policy` section in two tiers:

- *Generically renderable*: `zones` (from the existing `GetTopologyZones()`),
  `assignments` (per container: zone, exclusive/shared/isolated cpusets, CPU
  share, memory zone and types, free-form attributes), and `notices` — the
  policy's own explanations of its configuration's effect, as severity-tagged
  strings. Any visualizer can render these mechanically — a tree with bars, a
  key/value table, outlines over the CPU grid, a list of messages — without
  knowing which policy produced them.
- *Escape hatch*: an opaque `data` field of raw JSON, for structure only a
  policy-aware visualizer understands (the balloons CPU tree, for example).
  Generic visualizers ignore it by contract.

Rules that make "optional" real:

1. The absence of `policy` is indistinguishable from a policy having nothing to
   say. No error field, no empty scaffolding for a consumer to special-case.
2. No layer-1 field is ever derived from layer 2.
3. A consumer reading only layer 1 is correct, just less detailed.

The first visualizer we build consumes **layer 1 only**.

### Packages

| Package | Contents |
|---|---|
| `pkg/introspect/api` | the JSON types. Standard library only — no apimachinery, no `resource.Quantity`. This is the published contract. |
| `pkg/resmgr/introspect` | the collector (cache + sysfs + policy to snapshot) and the HTTP handlers. |
| `pkg/resmgr/introspect/ui` | UI assets embedded with `go:embed`, registered separately from the data endpoints. |

Keeping the types dependency-free means an external consumer — a future
aggregator, a test helper, someone's own tool — can import them without
pulling in resmgr or Kubernetes.

## Wire format

`pkg/introspect/api`, version string `v1alpha1` carried in the document. Units
are fixed and documented rather than encoded per value: **CPU quantities are
milli-CPU, memory quantities are bytes, CPU sets and memory sets are Linux
list-format strings** (`"0-3,8"`). Resource entries additionally carry a `unit`
string so the document stays self-describing.

```go
type Snapshot struct {
    Version    string       `json:"version"`
    Generation uint64       `json:"generation"`
    Timestamp  time.Time    `json:"timestamp"`
    Node       Node         `json:"node"`
    Config     *Config      `json:"config,omitempty"`
    Hardware   *Hardware    `json:"hardware"`
    Pods       []*Pod       `json:"pods"`
    Containers []*Container `json:"containers"`
    Policy     *PolicyState `json:"policy,omitempty"`
}

type Node struct {
    Name    string `json:"name"`
    Policy  string `json:"policy"`            // active policy name
    Version string `json:"version,omitempty"` // plugin build version
}
```

### Effective configuration

```go
type Config struct {
    Name            string          `json:"name,omitempty"`      // CR name
    Namespace       string          `json:"namespace,omitempty"`
    Generation      int64           `json:"generation,omitempty"`
    ResourceVersion string          `json:"resourceVersion,omitempty"`
    Data            json.RawMessage `json:"data,omitempty"`      // the config itself
}
```

The resource manager already retains the effective configuration as
`m.cfg cfgapi.ResmgrConfig`, and that interface embeds
`metav1.ObjectMetaAccessor`, so name, namespace, generation and resource version
are available without new plumbing. `Data` is the configuration marshalled as
received — `json.RawMessage` rather than typed fields, which keeps
`pkg/introspect/api` dependency-free and means a new policy configuration field
shows up in the snapshot with no schema work at all.

**A generic consumer does not interpret this blob.** Apart from `CommonConfig`,
every key in it is policy-specific: `pinCPU` and `pinMemory` live in
`topologyaware.Config` and in `balloons.Config` — and in the latter they are
`*bool` per *balloon definition*, not a single node-wide switch.
`reservedResources` and `availableResources` are per-policy too. A generic
visualizer that keyed off such names would be encoding policy knowledge in the
layer that is supposed to have none, and would be wrong for the next policy or
even for the other current one. So layer 1 renders the configuration as an
opaque read-only tree and reads nothing out of it.

The explanatory value is real, though, and it is preserved from both sides:

- **Generic observation.** Statements about the collected data need no
  configuration at all. "No container on this node has an applied cpuset" is an
  observation the UI can make and state plainly — policy-agnostic, and true
  whenever it is true, whatever the reason.
- **Policy-supplied explanation.** The *why* comes from the policy, which is the
  only thing that can read its own configuration correctly. See `Notices` under
  "Policy state".

No field is redacted. Nothing in the current configuration types carries a
credential, but if one is ever added, redaction belongs here.

### Hardware

```go
type Hardware struct {
    Packages     []*Package          `json:"packages"`
    NUMANodes    []*NUMANode         `json:"numaNodes"`
    CPUs         []*CPU              `json:"cpus"`
    IsolatedCPUs string              `json:"isolatedCpus,omitempty"`
    OfflineCPUs  string              `json:"offlineCpus,omitempty"`
    CoreKinds    map[string]string   `json:"coreKinds,omitempty"` // "P-core" -> cpuset
}

type Package struct {
    ID   int    `json:"id"`
    CPUs string `json:"cpus"`
    Dies []*Die `json:"dies,omitempty"`
}

type Die struct {
    ID        int        `json:"id"`
    CPUs      string     `json:"cpus"`
    NUMANodes []int      `json:"numaNodes,omitempty"`
    Clusters  []*Cluster `json:"clusters,omitempty"`
}

type Cluster struct {
    ID   int    `json:"id"`
    CPUs string `json:"cpus"`
}

type NUMANode struct {
    ID           int    `json:"id"`
    Package      int    `json:"package"`
    Die          int    `json:"die"`
    CPUs         string `json:"cpus"`
    MemoryType   string `json:"memoryType"`   // DRAM, HBM, PMEM
    NormalMemory bool   `json:"normalMemory"` // kernel-allocatable zone present
    Capacity     int64  `json:"capacity"`     // bytes
    Distance     []int  `json:"distance,omitempty"`
}

type CPU struct {
    ID       int    `json:"id"`
    Package  int    `json:"package"`
    Die      int    `json:"die"`
    Cluster  int    `json:"cluster"`
    NUMANode int    `json:"numaNode"`
    Core     int    `json:"core"`
    Threads  string `json:"threads"`            // sibling threads, self included
    CoreKind string `json:"coreKind,omitempty"` // "P-core", "E-core"
    Online   bool   `json:"online"`
    Isolated bool   `json:"isolated"`
    BaseFreq uint64 `json:"baseFreq,omitempty"` // kHz
    MinFreq  uint64 `json:"minFreq,omitempty"`
    MaxFreq  uint64 `json:"maxFreq,omitempty"`
}
```

Every field maps to an accessor that exists today on `sysfs.System`,
`sysfs.CPUPackage`, `sysfs.Node` and `sysfs.CPU`. One object per CPU is a few
kilobytes on a 128-CPU node and is what makes a machine-shaped view drawable at
all; everything coarser stays a cpuset string.

### Pods and containers

Flat lists with ID references rather than nesting, which keeps both UI lookups
and later concatenation of several nodes' snapshots simple.

```go
type Pod struct {
    ID        string `json:"id"`  // CRI ID
    UID       string `json:"uid"` // Kubernetes UID
    Name      string `json:"name"`
    Namespace string `json:"namespace"`
    QOSClass  string `json:"qosClass"`
}

type Container struct {
    ID       string `json:"id"`
    PodID    string `json:"podId"`
    Name     string `json:"name"`
    State    string `json:"state"`
    QOSClass string `json:"qosClass"`

    // requested and limited
    CPURequest    int64 `json:"cpuRequest,omitempty"`    // milli-CPU
    CPULimit      int64 `json:"cpuLimit,omitempty"`      // milli-CPU
    MemoryRequest int64 `json:"memoryRequest,omitempty"` // bytes
    MemoryLimit   int64 `json:"memoryLimit,omitempty"`   // bytes

    Applied *Applied          `json:"applied,omitempty"`
    Classes map[string]string `json:"classes,omitempty"` // rdt, blockio, cpu
    Hints   []*Hint           `json:"hints,omitempty"`
}

// Applied is what actually reached the container.
type Applied struct {
    CpusetCpus  string `json:"cpusetCpus,omitempty"`
    CpusetMems  string `json:"cpusetMems,omitempty"`
    CPUShares   int64  `json:"cpuShares,omitempty"`
    CPUQuota    int64  `json:"cpuQuota,omitempty"`
    CPUPeriod   int64  `json:"cpuPeriod,omitempty"`
    MemoryLimit int64  `json:"memoryLimit,omitempty"` // bytes
}

type Hint struct {
    Provider string `json:"provider"` // sysfs path the hint came from
    CPUs     string `json:"cpus,omitempty"`
    NUMAs    string `json:"numas,omitempty"`
    Sockets  string `json:"sockets,omitempty"`
    IRQs     []int  `json:"irqs,omitempty"`
}
```

The requested/limited values stay flat on `Container` while everything applied
sits in a nested `applied` object. Flattening both would put the requested
`memoryLimit` beside an applied memory limit under two near-identical names,
which is a trap for consumers. `topology.Hint` also carries a `PCIeChain`; the
view has no use for it and the snapshot omits it.

Requested/limited and applied are both present on purpose. Divergence between
what a policy decided and what reached the container is exactly the failure an
operator hits, and the applied side costs nothing to collect.

**Container arguments are deliberately absent.** The removed schema exposed
`Args`; command lines leak credentials, and nothing in the view needs them.

### Policy state

```go
type PolicyState struct {
    Name        string            `json:"name"`
    Zones       []*Zone           `json:"zones,omitempty"`
    Assignments []*Assignment     `json:"assignments,omitempty"`
    Notices     []*Notice         `json:"notices,omitempty"`
    Attributes  map[string]string `json:"attributes,omitempty"`
    Data        json.RawMessage   `json:"data,omitempty"` // policy-specific, opaque
}

// Notice is a policy's own explanation of something a viewer would
// otherwise have to guess at.
type Notice struct {
    Severity string `json:"severity"` // info, warning, error
    Message  string `json:"message"`
    Detail   string `json:"detail,omitempty"`
}

type Zone struct {
    Name       string            `json:"name"`
    Type       string            `json:"type"`
    Parent     string            `json:"parent,omitempty"`
    CPUs       string            `json:"cpus,omitempty"`
    Memory     string            `json:"memory,omitempty"`
    Resources  []*ZoneResource   `json:"resources,omitempty"`
    Attributes map[string]string `json:"attributes,omitempty"`
}

type ZoneResource struct {
    Name        string `json:"name"` // "cpu", "memory"
    Unit        string `json:"unit"` // "milliCPU", "bytes"
    Capacity    int64  `json:"capacity"`
    Allocatable int64  `json:"allocatable"`
    Available   int64  `json:"available"`
}

type Assignment struct {
    ContainerID   string            `json:"containerId"`
    Zone          string            `json:"zone,omitempty"` // matches a Zone.Name
    ExclusiveCPUs string            `json:"exclusiveCpus,omitempty"`
    SharedCPUs    string            `json:"sharedCpus,omitempty"`
    IsolatedCPUs  string            `json:"isolatedCpus,omitempty"`
    ReservedCPUs  string            `json:"reservedCpus,omitempty"`
    CPUShare      int64             `json:"cpuShare,omitempty"` // milli-CPU of shared
    MemoryZone    string            `json:"memoryZone,omitempty"`
    MemoryTypes   []string          `json:"memoryTypes,omitempty"`
    Attributes    map[string]string `json:"attributes,omitempty"`
}
```

`Notices` is how a policy explains its own configuration's effect on what the
viewer is seeing, without any consumer having to parse policy-specific
configuration keys: "CPU pinning is disabled (pinCPU: false)", or for balloons,
"CPU pinning is disabled for balloon type `noisy` (pinCPU: false)" — a
distinction a generic reader of the configuration blob could not even locate,
since there the setting is per balloon definition. It is deliberately a list of
severity-tagged strings rather than structured facts: any visualizer can render
it without policy knowledge, and it maps onto the status palette, which is
always drawn with an icon and a label and never colour alone.

`Zone` is populated by converting the existing `policy.TopologyZone`:
`resource.Quantity` becomes milli-CPU for `cpu` and bytes for everything else.
Reusing `GetTopologyZones()` avoids a second pool export to keep in sync, at
the cost of inheriting its NRT-shaped semantics (balloons reports whole-system
CPU as a zone's `Capacity`, and `Available` as a fits-this-container figure).
The UI therefore labels zone resources with the policy's own wording and does
not compute derived percentages from them.

`Attributes` on both types is a `map[string]string` rather than the existing
`[]*ZoneAttribute` slice, so consumers can look a key up directly; conversion
is trivial and order carries no meaning.

## Endpoints

All read-only `GET`, registered on the existing instrumentation `ServeMux`, and
only when enabled:

| Path | Response |
|---|---|
| `/introspect` | the snapshot, `application/json` |
| `/introspect/events` | change notifications, `text/event-stream` |
| `/ui/` | the embedded UI (only when the UI is enabled) |

The version lives in the document rather than the path. Should a breaking
change ever be needed, a `/introspect/v2` path can be added alongside.

A redirect from `/` to `/ui/` is registered **only** when the UI is enabled, so
that port-forwarding the endpoint and opening the root lands somewhere useful
without otherwise claiming the shared mux's catch-all pattern.

### Change notification

`/introspect/events` is Server-Sent Events: a normal `GET` whose response body
stays open, `Content-Type: text/event-stream`, one record per change, flushed
as it is written.

```
data: {"generation":42}

: heartbeat

data: {"generation":43}

```

Chosen over WebSockets because nothing flows upstream — SSE needs no library,
no upgrade handshake and no ping/pong bookkeeping, and `EventSource` reconnects
by itself when the DaemonSet pod restarts. Chosen over client polling because
an idle node then costs nothing and a busy one is not seen seconds late.

The record carries only the generation number. The server keeps a counter and a
dirty flag; the client sees the bump and re-`GET`s `/introspect`. With no
subscriber connected, **no snapshot is ever built or marshalled** — the
opposite of the removed code, which re-marshalled its entire state on every
container start regardless.

Implementation notes:

- a comment heartbeat roughly every 30 seconds keeps `kubectl port-forward` and
  intervening proxies from reaping an idle stream;
- per-subscriber notifications coalesce — only the newest generation matters, so
  a slow client never blocks a fast producer;
- concurrent subscribers are capped (16) so the endpoint cannot be used to pin
  goroutines and buffers;
- each subscriber's goroutine exits when the request context is cancelled.

### Consistency and locking

The snapshot must not be built while an allocation is in flight, so the
collector runs under the resource manager's read lock. Building lazily on
request means an HTTP request can block behind a long allocation; that is
acceptable for a read-only debug and inspection interface, and is noted rather
than worked around.

## Configuration

Disabled by default, in the section that already owns the HTTP server:

```yaml
instrumentation:
  httpEndpoint: ":8891"
  introspection:
    enabled: true   # default false: no /introspect, no /ui
    ui: true        # default true when enabled
```

```go
// in pkg/apis/config/v1alpha1/instrumentation
type IntrospectionConfig struct {
    // Enabled turns on the introspection endpoints.
    // +optional
    Enabled bool `json:"enabled,omitempty"`
    // UI additionally serves the built-in visualization. Defaults to true
    // when introspection is enabled.
    // +optional
    UI *bool `json:"ui,omitempty"`
}
```

`UI` is a pointer so "unset" is distinguishable from "false". Enabling
introspection without wanting the UI is the unusual case; an operator who turns
this on generally turns it on in order to look at the node. Adding the field
means regenerating the CRDs and updating the Helm values of all three charts.

## Policy API

An optional interface the collector type-asserts, rather than a method every
backend must stub:

```go
// Introspector is optionally implemented by policy backends that can
// describe their own view of the node's state.
type Introspector interface {
    GetIntrospection() *api.PolicyState
}
```

A backend that does not implement it contributes nothing, and the template
policy stays untouched. This is what keeps the interface from rotting the way
the removed `Backend.Introspect()` did — there, a policy that skipped the
method silently produced an empty view, and two of three policies skipped it.

Zones keep coming from the existing `GetTopologyZones()`, converted by the
collector; `GetIntrospection()` supplies assignments, policy-level attributes
and the opaque `data` blob.

One gap to close on the way: topology-aware's zones export `memset`, `shared
cpuset`, `reserved cpuset` and `isolated cpuset`, but not the zone's full
cpuset, which is what pool outlines over the CPU grid need. Balloons already
exports `cpuset`. Adding it to topology-aware improves the
`NodeResourceTopology` CRs too.

### Invalidation points

`updateTopologyZones()` is called from exactly four places, all of which are
points where placement can change: the configuration-update path in
`resource-manager.go`, and `nriPlugin.Synchronize()`,
`nriPlugin.CreateContainer()` and `nriPlugin.StopContainer()` in `nri.go`. All
four already call both `updateTopologyZones()` and
`updateNodeExtendedResources()` back to back; introspection invalidation is a
third such call. Collapsing the three into one `m.publishNodeState()` helper
used at all four sites is a small tidy-up of the code being touched, and keeps a
future publisher from being forgotten at one of them.

## Visualization

### Form, and the constraint that fixes it

A CPU grid is an *all-pairs* context — any two cells can end up adjacent — and
the validated palette's all-pairs cap is three hues:

```
3 slots #2a78d6,#eb6834,#1baf7a  all pairs light:
  CVD ΔE 9.2 PASS · normal-vision 24.0 PASS · contrast WARN (#1baf7a 2.74:1, relief required)
3 slots #3987e5,#d95926,#199e70  all pairs dark:
  CVD ΔE 9.4 PASS · normal-vision 20.9 PASS · contrast PASS
```

A node runs dozens of containers, so colour-per-container is structurally
wrong. That was the removed prototype's central mistake. **Identity comes off
the hue channel entirely.**

### Base layer: commitment heatmap

Each CPU cell's fill encodes one magnitude, computed from layer-1 data alone.
For every container, spread its CPU request evenly across the CPUs its applied
`cpuset.cpus` allows, then sum per CPU:

```
commitment(cpu) = Σ over containers allowed on cpu of (cpuRequest / |cpusetCpus|)
```

An exclusive four-CPU container requesting 4000m contributes 1000m to each of
its cells; a 500m container loose on sixteen shared CPUs contributes 31m to
each. Oversubscription becomes visible honestly, with no policy input. The same
spread rule over `cpuset.mems` and memory requests gives per-NUMA-node
commitment.

Four ordinal steps, validated:

```
light #86b6ef → #5598e7 → #2a78d6 → #1c5cab
  monotone PASS · adjacent ΔL PASS · light-end 2.06:1 PASS · single hue PASS
dark  #184f95 → #256abf → #3987e5 → #86b6ef
  monotone PASS · adjacent ΔL PASS · light-end 2.15:1 PASS · single hue PASS
```

Idle CPUs sit at the surface with a hairline border. Commitment above 100% is
status-critical *with* an icon and label, never colour alone.

### Everything structural uses a non-colour channel

So that nothing competes with the ramp:

- packages, dies and NUMA nodes are nested hairline boxes;
- hyperthread siblings stack in one column, so a core reads vertically;
- isolated CPUs take a dashed ring, offline CPUs a hatch;
- every cell carries its CPU id as a direct label — which is also the relief
  channel the light-mode contrast WARN obligates.

### Identity by pinning, capped at three

Clicking a container pins it; each pinned container takes categorical slot 1, 2
or 3 as a 2px ring plus a 2px surface gap on its cells, while unpinned fills
recede. A fourth pin displaces the oldest. The cap is the palette's all-pairs
cap, not an arbitrary product decision, and it covers the real question: where
is this pod, and does it overlap that one.

### Layout

```
worker-1 · topology-aware · 128 cpu · 4 numa            ◐ light/dark   ● live gen 42
colour by: [ commitment ▾ ]   pinned: ● web/nginx  ● db/sql            [ map | table ]

┌ package 0 ─────────────────────────────────────────────────────────────────┐
│ NUMA 0  DRAM  mem ████████████▏░░░░░░ 24.1/32 GiB committed                 │
│    cpu  0    1    2    3    4    5    6    7   ⋯                            │
│   ht0  ▓●   ▓●   ▓▓   ▓▓   ░░   ░░   ▒▒   ▒▒                                │
│   ht1  ▓●   ▓●   ▓▓   ▓▓   ░░   ░░   ▒▒   ▒▒    ⌇ isolated  ▨ offline       │
│ NUMA 1  DRAM  mem ███▏░░░░░░░░░░░░░░░  9.4/32 GiB                           │
│    cpu 16   17   18   19   20   21   22   23   ⋯                            │
│   ht0  ░░   ░░   ⌇⌇   ⌇⌇   ▒▒   ▒▒   ▨▨   ▨▨                                │
└─────────────────────────────────────────────────────────────────────────────┘
commitment  ░ idle  ▒ ≤50%  ▓ ≤100%  █ full  ⚠ over        ● pinned container
```

Memory is a horizontal bar per NUMA node: a capacity track with a single-hue
committed fill, 4px rounded data-end anchored to the baseline, memory type as a
text chip rather than a hue. A legend is always present.

### Table view

Always available, both as the required relief channel and as a feature in its
own right: one row per container with pod, name, QoS, requests, limits, applied
cpuset and mems, shares and quota, derived exclusivity, hints and classes.
Sortable, filterable, `tabular-nums`. This is what gets pasted into a bug
report.

### Interaction

- Per-cell hover tooltip: CPU id, package/die/cluster/NUMA, core kind, thread
  sibling, commitment, and which containers are allowed there.
- Hovering a table row cross-highlights that container's cells, and vice versa.
- On a generation bump, cells whose occupancy changed pulse a ring briefly and
  new table rows flash — ring and opacity, so no hue is spent on change.
- Filters and the map/table switch sit in one row above the view.

### Configuration panel

A collapsible panel shows the effective configuration as a read-only formatted
tree, with the CR name and generation in the header so it is obvious which
configuration produced what is on screen. The panel does not interpret any key —
it is a viewer, not a parser.

Where the view would otherwise be inexplicable, the layer-1 UI states what it
observes — "no container on this node has an applied cpuset, so no CPU placement
is shown" — which needs no policy knowledge and ships in increment 3. The reason
behind it arrives with layer 2, as policy `Notices` rendered verbatim beside the
observation, and needs no consumer-side policy knowledge either.

### Rendering untrusted strings

Pod names, namespaces, container names, annotations, zone attributes and the
whole configuration blob are user-authored. The UI renders every value as text
(`textContent`, or SVG text nodes) and never assembles markup from snapshot
data. No `innerHTML` with anything that came off the wire.

### Accessibility and scale

- Dark mode is a *selected* set of steps validated against the dark surface,
  declared under both the `prefers-color-scheme` media query and an explicit
  `data-theme` scope, with the toggle winning both ways. Not an inverted flip.
- Identity is never colour-alone: cell labels, the legend, and the table.
- Adaptive cell size down to about 14px so 256 CPUs still fit; labels drop below
  18px while tooltips remain.
- Keyboard navigation across cells and rows.

### Layer 2 in the UI

Pool and balloon boundaries draw as labelled strokes over the grid; policy
assignment adds columns to the table; notices render as a list of
severity-tagged messages with the status palette's icon-and-label pairing.
Structure and text, not colour — so layer 2 cannot collide with the commitment
ramp, and a snapshot without a `policy` section simply renders without outlines,
without the extra columns and without messages.

### Stack

Hand-written HTML, CSS and ES-module JavaScript rendering SVG, embedded with
`go:embed`. No npm, no bundler, nothing minified in git, no new CI step; every
line is reviewable like the rest of the repo. A CPU-grid layout is arithmetic,
not a job for a layout library. The removed UI vendored `d3.v4.min.js` into git
and generated a Go asset file with `vfsgen`; `go:embed` and plain SVG replace
both.

## Testing

- **Schema stability**: a golden-JSON test in `pkg/introspect/api` marshalling a
  fully populated snapshot against `testdata/snapshot.golden.json`, so a field
  rename cannot slip through unnoticed.
- **Collector**: unit tests using `sysfs.SetSysRoot` with the existing
  `pkg/sysfs/testdata/sample1` and `sample2` trees for the hardware half, and a
  populated cache for the container half. Commitment arithmetic gets its own
  table-driven test, including the oversubscribed and empty-cpuset cases.
- **Endpoints**: handler tests for the snapshot, for the disabled case (no
  handlers registered at all), and for an SSE subscriber receiving a coalesced
  generation bump.
- **e2e**: a case enabling introspection in `helm-config.yaml`, curling
  `/introspect` through the port-forward the framework already sets up, and
  `jq`-asserting a container's applied cpuset — a `verify-introspect` helper
  alongside the existing `verify-metrics-has-line`.
- **UI**: data-to-layout stays in pure functions, and a checked-in sample
  snapshot lets the UI be developed, reviewed and screenshotted from `file://`
  with no cluster. That sample doubles as the source for docs screenshots. No
  JavaScript test harness.

## Increments

Each step is independently useful and independently reviewable.

1. `pkg/introspect/api` types, the layer-1 collector including the effective
   configuration, `GET /introspect`, the config knob, CRD and Helm updates,
   golden and collector tests, and the e2e case. Useful immediately via
   `curl | jq`.
2. SSE `/introspect/events`, generation and dirty-flag bookkeeping, and the
   `publishNodeState()` tidy-up at the four invalidation sites.
3. The layer-1 UI: machine map, commitment ramp, pinning, table view,
   configuration panel, hover, dark mode, delta pulses, sample-snapshot
   development mode.
4. Layer 2 on the Go side: the `Introspector` interface, implementations for
   topology-aware and balloons including their pinning notices, and the missing
   `cpuset` zone attribute for topology-aware.
5. Layer 2 in the UI: zone outlines over the grid, assignment columns in the
   table, and the notices list.
6. Documentation page under `docs/resource-policy/` with screenshots generated
   from the sample snapshot.

## Open questions

- Whether hugepages deserve first-class treatment in `NUMANode` and container
  requests. The policies handle them; deliberately left out of this design.
- This document lives under `docs/superpowers/` and is internal to how the work
  was designed. It can be moved or dropped before the branch is proposed
  upstream.

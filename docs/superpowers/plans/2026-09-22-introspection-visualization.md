# Introspection Visualization Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Serve a browser view of how the active resource policy placed containers on a node — a machine-shaped map with a commitment heatmap, a container table, and live updates — embedded in the plugin binary with no build step.

**Architecture:** The plugin already serves a policy-agnostic JSON snapshot at `/introspect` and a change-notification stream at `/introspect/events`. This plan adds a `ui` package whose assets are embedded with `go:embed` and served at `/ui/` when enabled, plus hand-written HTML, CSS and ES modules that render the snapshot as SVG. No npm, no bundler, nothing minified, no framework.

**Tech Stack:** Go 1.26 (`go:embed`, `http.FileServerFS`), plain ES modules, SVG, CSS custom properties. Verification uses headless Chrome screenshots, which are already confirmed working in this environment.

**Spec:** `docs/superpowers/specs/2026-09-22-introspection-visualization-design.md` — read its "Visualization" section before starting. This plan implements **spec increment 3** (the layer-1 UI). Increments 4–6 (the policy-provided layer 2, and its rendering) are out of scope and get their own plan.

**Predecessor:** `docs/superpowers/plans/2026-09-22-introspection-data-plane.md` implemented increments 1–2. It is complete, reviewed and merged into the branch this plan continues. Its wire format is frozen and is the contract this UI consumes.

---

## Notes for the executor

This section is the handoff. Read it once before Task 1; it carries what was learned running the predecessor plan and is not repeated in the task steps.

### Model availability in this environment

**Only `haiku` and `opus` are invocable.** `sonnet` and `fable` both fail with HTTP 403 — an explicit IAM deny on `bedrock:InvokeModelWithResponseStream` for this account. A dispatch naming either burns an agent launch and dies in seconds. There is no middle tier, so:

- **Implementers: `haiku`.** It handled 8 of 10 tasks in the predecessor plan, including fix rounds, whenever the plan contained the complete code. Every task here does.
- **Reviewers and any judgement-heavy implementation: `opus`.**

### Verification is by screenshot, not by review

A reviewer reading CSS or SVG-building code from a diff cannot tell you the machine map is unreadable, the ramp is inverted, or the layout overflows. In the predecessor plan, the single worst cluster of defects — eight in one file — was in the one artefact nobody could execute.

So the gate for presentation work here is **render it and look at it**, and the controller does that directly:

```bash
cd pkg/resmgr/introspect/ui/assets
(python3 -m http.server 8799 >/dev/null 2>&1 &) ; sleep 2
google-chrome --headless --disable-gpu --no-sandbox --hide-scrollbars \
  --virtual-time-budget=3000 --window-size=1400,900 \
  --screenshot=/tmp/ui.png 'http://localhost:8799/index.html?snapshot=./sample-snapshot.json'
pkill -f "http.server 8799"
```

Then read `/tmp/ui.png`. This was verified working in this environment, including that an in-page `fetch()` completes before the screenshot fires. `--virtual-time-budget=3000` is belt-and-braces for animation and SSE.

Serve over `http://localhost` rather than opening `file://`: a `fetch()` of a local JSON file from a `file://` origin is blocked, and working around it with Chrome flags diverges from production.

**Per task, the plan states which gate applies** — a full `opus` review, or a screenshot plus a scoped spot-check. Do not pay for a full review of a stylesheet.

### One bug this loop already caught

While proving the pipeline, a probe page rendered `worker-1 Â· topology-aware` — a UTF-8 middot decoded as Latin-1, because the page had no `<meta charset="utf-8">`. Invisible in a diff; obvious in a render. `index.html` declares the charset for this reason.

### The wire format's traps

These come from the predecessor's final review and are the things most likely to produce a UI that is quietly wrong. **Every one is a field where the obvious JS reading is incorrect.**

- **`pods` and `containers` can be absent entirely**, not empty arrays — a node with no pods has neither key. Same for `hardware`, `config`, `derived`, and every map. Treat absence as empty; never `snapshot.pods.length` without a guard.
- **`numaNodes[].package` and `.die` are optional.** Absent means "belongs to no package or die" — a memory-only CXL or HBM node. It does **not** mean package 0. The `sample-snapshot.json` this plan generates contains four such nodes precisely so this is visible.
- **`cpus[].coreKind` absent means unknown**, not P-core. Do not render a default kind.
- **`cpus[].baseFreq`/`minFreq`/`maxFreq` absent means the kernel did not report them**, not 0 kHz. Render "not reported" or omit the row.
- **`cpus[].isolated` is `omitempty`** — absent means false. `online` is not omitempty and is always present.
- **`containers[].applied` can be absent**, and so can any field inside it. A container with no `applied.cpusetCpus` is not pinned, which is a normal state (CPU pinning can be disabled in policy configuration) and the reason for the observation line in Task 7.
- **`derived.cpuCommitment` and `cpuSharers` are keyed by CPU id, `memoryCommitment` by NUMA node id**, as JSON object keys — so strings. JS coerces `obj[5]` to `obj["5"]`, so indexing by number works, but `Object.keys()` gives strings. Every hardware CPU and NUMA node has an entry, so no missing-key fallback is needed for ids that exist.
- **Units: CPU is milli-CPU, memory is bytes, cpusets are Linux list strings** like `"0-3,8"`.
- **`state`** is one of `unknown creating created paused running stopped stale`, and the set can grow — keep a default branch.

The authoritative statement of all of this is the package comment in `pkg/introspect/api/types.go`. Read it; it was corrected three times to be accurate.

### Security rule, non-negotiable

Pod names, namespaces, container names, class names and the whole configuration blob are user-authored. **Render every wire value with `textContent` or an SVG text node. Never `innerHTML`, never template-string HTML assembly with snapshot data.** The configuration panel is the sharpest case: it renders arbitrary nested JSON.

### Process lessons worth carrying

- Most findings in the predecessor plan were defects in the *plan text*, not implementer error. If a step looks wrong, say so and stop rather than implementing it — three implementers did exactly that and each was right.
- A test that cannot fail is worse than no test. Before claiming a test covers something, break the code and confirm the test goes red.
- Reports must paste verbatim command output. Prose summaries and filtered transcripts were sent back twice.

---

## Global Constraints

- Module path: `github.com/containers/nri-plugins`.
- Every new Go file starts with the current licence header, copied verbatim from `pkg/resmgr/dra.go`: `// Copyright The NRI Plugins Authors. All Rights Reserved.` followed by the Apache 2.0 block. Assets (`.html`, `.css`, `.js`) carry no licence header — the repo does not put them on web assets.
- **No build step, no npm, no bundler, nothing minified, no vendored framework.** Plain ES modules loaded with `<script type="module">`.
- **No JSON tag or field change in `pkg/introspect/api`.** The wire format is frozen; this plan is a consumer.
- The UI is served only when introspection **and** the UI are enabled. With either off, nothing is registered on the mux.
- `gofmt -l` clean on every Go file touched.
- **Never discard changes you did not make.** Do not run `git checkout --`, `git restore`, `git stash`, `git clean`, or anything that reverts the working tree, and never `git add` a path outside your task's file list. Note `test/e2e/n4c16-fedora-43-containerd/` is 736 MB of untracked VM workspace that must stay untracked and undeleted. Stage your files by name.
- Commit messages: subject line, blank line, body wrapped at 60 columns, ending with `Signed-off-by: Krisztian Litkey <krisztian.litkey@intel.com>` and then exactly `Co-Authored-By: Claude Opus 5 (1M context) <noreply@anthropic.com>`. That co-author line is a fixed string for this repository regardless of which model does the work — do not substitute your own name; it had to be corrected twice in the predecessor plan.

## The palette, already validated

Do not pick colours. These are the values from the spec, and the validator results that justify them. `scripts/validate_palette.js` in the `dataviz` skill is what produced them.

**Commitment ramp (ordinal, one hue, four steps).** Light `#86b6ef → #5598e7 → #2a78d6 → #1c5cab`; dark `#184f95 → #256abf → #3987e5 → #86b6ef`. Both: monotone lightness PASS, adjacent ΔL PASS, light-end contrast 2.06:1 / 2.15:1 PASS, single hue PASS.

**Pinned-container hues (categorical, capped at three).** Light `#2a78d6 #eb6834 #1baf7a`; dark `#3987e5 #d95926 #199e70`. All-pairs CVD ΔE 9.2 light / 9.4 dark PASS; normal-vision 24.0 / 20.9 PASS. Light-mode aqua is 2.74:1 against the surface — a contrast WARN, which is why every cell carries its CPU id as a direct label and the table view exists. **Three is the cap**: a CPU grid is an all-pairs context and the palette does not clear the floors beyond three hues. A fourth pin displaces the oldest.

**Status (fixed, never themed, always with icon and label).** good `#0ca30c`, warning `#fab219`, serious `#ec835a`, critical `#d03b3b`. Only over-100% commitment uses one.

**Surfaces and ink.**

| Role | Light | Dark |
|---|---|---|
| chart surface | `#fcfcfb` | `#1a1a19` |
| page plane | `#f9f9f7` | `#0d0d0d` |
| primary ink | `#0b0b0b` | `#ffffff` |
| secondary ink | `#52514e` | `#c3c2b7` |
| muted | `#898781` | `#898781` |
| gridline | `#e1e0d9` | `#2c2c2a` |
| baseline | `#c3c2b7` | `#383835` |
| border | `rgba(11,11,11,0.10)` | `rgba(255,255,255,0.10)` |

Typeface everywhere: `system-ui, -apple-system, "Segoe UI", sans-serif`. `font-variant-numeric: tabular-nums` only in the table and on axis-like numbers.

## Deviations from the spec, and why

The predecessor plan's final review flagged an undeclared deviation as a defect
in its own right, so these are stated up front.

1. **The CPU id is not drawn inside every cell.** The spec says every cell
   carries its id as a direct label, doubling as the relief channel the
   light-mode contrast WARN obligates. At a 20px cell a three-digit id is
   cramped, and 112 of them is visual noise competing with the ramp. Instead
   the id appears on a per-column ruler, in the hover tooltip, in each cell's
   `aria-label`, and in the table's cpuset column. **The relief channel is
   therefore the table view**, which the colour rules accept explicitly as an
   alternative to visible direct labels. If a render shows the ruler is not
   enough to locate a CPU, revisit this — it is a judgement call, not a
   constraint.
2. **The ramp uses three of its four validated steps.** There are three fill
   buckets (`≤50%`, `≤100%`, `full`) plus idle at the surface and over-full in
   status-critical, so the widest three steps are used and the fourth is left
   out. All four were validated together; using a subset weakens nothing.
3. **The `ui` config knob is added here**, having been deliberately deferred by
   the predecessor plan so that no setting existed without an effect. That is
   this plan delivering on that deferral, not a divergence.

## File Structure

| File | Responsibility |
|---|---|
| `pkg/resmgr/introspect/ui/ui.go` | the embedded asset filesystem and its `http.Handler`. Nothing else. |
| `pkg/resmgr/introspect/ui/ui_test.go` | that the handler serves `index.html` and rejects traversal. |
| `pkg/resmgr/introspect/ui/sample_test.go` | regenerates `assets/sample-snapshot.json` under `-update`. |
| `pkg/resmgr/introspect/ui/test-setup.sh`, `test-cleanup.sh` | extract the shared sysfs fixture the generator needs. |
| `pkg/resmgr/introspect/ui/assets/index.html` | the document: charset, roles, module entry point. |
| `pkg/resmgr/introspect/ui/assets/ui.css` | every colour and every dimension, as custom properties plus rules. |
| `pkg/resmgr/introspect/ui/assets/sample-snapshot.json` | committed dev fixture: 112 CPUs, 8 NUMA nodes, 4 of them memory-only, containers covering every visual state. |
| `pkg/resmgr/introspect/ui/assets/js/wire.js` | omitempty-safe accessors over a snapshot. The only file that knows the wire format's traps. |
| `pkg/resmgr/introspect/ui/assets/js/model.js` | pure derivation: commitment buckets, per-CPU occupancy, formatting. No DOM. |
| `pkg/resmgr/introspect/ui/assets/js/machine.js` | layout arithmetic and the SVG machine map. |
| `pkg/resmgr/introspect/ui/assets/js/table.js` | the container table. |
| `pkg/resmgr/introspect/ui/assets/js/config.js` | the configuration panel. |
| `pkg/resmgr/introspect/ui/assets/js/app.js` | state, wiring, pinning, theme, SSE. |
| `pkg/resmgr/introspect/ui/assets/js/selftest.html` + `selftest.js` | in-browser assertions for `wire.js` and `model.js`, because there is no JS test runner. |
| `pkg/resmgr/introspect/introspect.go` | modify: register the UI paths from `Enable`/`Disable`. |
| `pkg/apis/config/v1alpha1/instrumentation/config.go` | modify: the `ui` knob. |
| `deployment/helm/*/values.yaml`, `*/crds/*.yaml` | modify/regenerate. |

Two boundaries worth stating, because they are what keep this reviewable:

1. **`wire.js` is the only module that knows a field can be absent.** Everything downstream consumes its normalised output. That is what stops the `omitempty` traps being rediscovered in five places.
2. **`model.js` has no DOM access at all**, so it is testable in the self-test page and reviewable as ordinary logic. The renderers take its output and produce nodes.

---

### Task 1: Serve the assets

**Files:**
- Create: `pkg/resmgr/introspect/ui/ui.go`, `pkg/resmgr/introspect/ui/ui_test.go`
- Create: `pkg/resmgr/introspect/ui/assets/index.html` (placeholder, replaced in Task 3)
- Modify: `pkg/resmgr/introspect/introspect.go`
- Modify: `pkg/apis/config/v1alpha1/instrumentation/config.go`
- Modify: `deployment/helm/{topology-aware,balloons,template}/values.yaml`
- Regenerate: CRDs and deepcopy

**Interfaces:**
- Consumes: `introspect.Server` from the predecessor plan.
- Produces: `ui.Handler() http.Handler`, `ui.Assets fs.FS`, `introspect.UIPath`, `(*Server).SetUI(bool)`, `(*instrumentation.Config).IntrospectionUIEnabled() bool`.

**Gate: full `opus` review.** This is Go, it touches the mux registration that had two deadlocks fixed in it, and it is the only task where a review can see everything that matters.

- [ ] **Step 1: The config knob**

Add to `IntrospectionConfig` in `pkg/apis/config/v1alpha1/instrumentation/config.go`:

```go
	// UI additionally serves the built-in visualization at /ui/. It
	// defaults to true when introspection is enabled: an operator who
	// turns introspection on generally turns it on in order to look at
	// the node.
	// +optional
	UI *bool `json:"ui,omitempty"`
```

and beside `IntrospectionEnabled()`:

```go
// IntrospectionUIEnabled returns whether the introspection UI is served.
// It is never true unless introspection itself is enabled.
func (c *Config) IntrospectionUIEnabled() bool {
	if !c.IntrospectionEnabled() {
		return false
	}
	if c.Introspection.UI == nil {
		return true
	}
	return *c.Introspection.UI
}
```

A pointer, so "unset" is distinguishable from "false".

- [ ] **Step 2: Write the failing config test**

Append to `pkg/apis/config/v1alpha1/instrumentation/config_test.go`:

```go
func TestIntrospectionUIEnabled(t *testing.T) {
	yes, no := true, false

	for _, tc := range []struct {
		name     string
		config   *Config
		expected bool
	}{
		{
			name:     "introspection off means no UI",
			config:   &Config{},
			expected: false,
		},
		{
			name: "introspection off, UI explicitly on, still no UI",
			config: &Config{
				Introspection: &IntrospectionConfig{Enabled: false, UI: &yes},
			},
			expected: false,
		},
		{
			name: "introspection on, UI unset, defaults on",
			config: &Config{
				Introspection: &IntrospectionConfig{Enabled: true},
			},
			expected: true,
		},
		{
			name: "introspection on, UI explicitly off",
			config: &Config{
				Introspection: &IntrospectionConfig{Enabled: true, UI: &no},
			},
			expected: false,
		},
		{
			name: "introspection on, UI explicitly on",
			config: &Config{
				Introspection: &IntrospectionConfig{Enabled: true, UI: &yes},
			},
			expected: true,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := tc.config.IntrospectionUIEnabled(); got != tc.expected {
				t.Errorf("got %v, expected %v", got, tc.expected)
			}
		})
	}
}

func TestIntrospectionUIEnabledNilConfig(t *testing.T) {
	var c *Config
	if c.IntrospectionUIEnabled() {
		t.Error("a nil config must not enable the UI")
	}
}
```

Run: `go test ./pkg/apis/config/v1alpha1/instrumentation/... -count=1 -v`

Expected: FAIL, `UI` and `IntrospectionUIEnabled` undefined. Then add the code from Step 1 and re-run: PASS.

- [ ] **Step 3: The asset package**

Create `pkg/resmgr/introspect/ui/assets/index.html` as a placeholder that proves serving works and will be replaced wholesale in Task 3:

```html
<!DOCTYPE html>
<html lang="en">
  <head>
    <meta charset="utf-8">
    <title>nri-plugins introspection</title>
  </head>
  <body>
    <p>Introspection UI placeholder.</p>
  </body>
</html>
```

Create `pkg/resmgr/introspect/ui/ui.go` with the licence header, then:

```go
// Package ui serves the built-in introspection visualization.
//
// The assets are embedded, hand-written, and have no build step: plain
// HTML, CSS and ES modules. Nothing here is generated, so what is in the
// repository is exactly what is served.
package ui

import (
	"embed"
	"io/fs"
	"net/http"
)

//go:embed assets
var embedded embed.FS

// Assets is the embedded asset filesystem, rooted at the asset directory.
var Assets = mustSub(embedded, "assets")

// Handler serves the assets. It is the caller's job to strip whatever path
// prefix it registered the handler under.
func Handler() http.Handler {
	return http.FileServerFS(Assets)
}

func mustSub(f fs.FS, dir string) fs.FS {
	sub, err := fs.Sub(f, dir)
	if err != nil {
		// Unreachable: the directory is embedded at compile time.
		panic(err)
	}
	return sub
}
```

- [ ] **Step 4: Write the failing handler test**

Create `pkg/resmgr/introspect/ui/ui_test.go` with the licence header, then:

```go
package ui

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestHandlerServesIndex(t *testing.T) {
	for _, path := range []string{"/", "/index.html"} {
		t.Run(path, func(t *testing.T) {
			w := httptest.NewRecorder()
			Handler().ServeHTTP(w, httptest.NewRequest(http.MethodGet, path, nil))

			if w.Code != http.StatusOK {
				t.Fatalf("status %d, expected %d", w.Code, http.StatusOK)
			}
			if ct := w.Header().Get("Content-Type"); !strings.Contains(ct, "text/html") {
				t.Errorf("content type %q, expected HTML", ct)
			}
			// The charset declaration is not decoration: without it a
			// UTF-8 document is decoded as Latin-1 and every non-ASCII
			// glyph renders as mojibake.
			if body := w.Body.String(); !strings.Contains(body, `charset="utf-8"`) {
				t.Errorf("index.html does not declare its charset:\n%s", body)
			}
		})
	}
}

func TestHandlerRejectsTraversal(t *testing.T) {
	for _, path := range []string{
		"/../ui.go",
		"/../../introspect.go",
		"/assets/../../ui.go",
	} {
		t.Run(path, func(t *testing.T) {
			w := httptest.NewRecorder()
			Handler().ServeHTTP(w, httptest.NewRequest(http.MethodGet, path, nil))

			if w.Code == http.StatusOK {
				t.Errorf("traversal to %q was served:\n%s", path, w.Body.String())
			}
		})
	}
}

func TestAssetsHoldNoGoSource(t *testing.T) {
	// go:embed takes the whole directory, so a stray file would ship.
	entries, err := Assets.(interface {
		ReadDir(string) ([]os.DirEntry, error)
	}).ReadDir(".")
	if err != nil {
		t.Fatalf("failed to read the asset root: %v", err)
	}
	for _, e := range entries {
		if strings.HasSuffix(e.Name(), ".go") {
			t.Errorf("Go source embedded as an asset: %s", e.Name())
		}
	}
}
```

Add `os` to the imports.

Run: `go test ./pkg/resmgr/introspect/ui/... -count=1 -v`

Expected: FAIL first (`Handler` undefined), then PASS once Step 3's code is in.

- [ ] **Step 5: Register the paths, and fix the duplication while you are in there**

`Enable()` and `Disable()` in `pkg/resmgr/introspect/introspect.go` currently name each path twice. Adding two more paths conditionally would make that worse, so replace the hardcoded lists with a remembered one. **Keep the existing lock discipline exactly**: decide state under `s.mu`, release it, then touch the mux. Two deadlocks were fixed to establish that and there is a regression test for each.

Add the constant beside the others:

```go
	// UIPath serves the built-in visualization.
	UIPath = "/ui/"
```

Add two fields to `Server`:

```go
	ui         bool     // serve the visualization too
	registered []string // mux patterns we registered, for Disable
```

Add the setter:

```go
// SetUI selects whether Enable also serves the visualization. It takes
// effect at the next Enable; configuration is applied before enabling.
func (s *Server) SetUI(on bool) {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.ui = on
}
```

Replace `Enable`'s registration tail and `Disable`'s unregistration tail:

```go
func (s *Server) Enable() {
	s.mu.Lock()
	if s.enabled {
		s.mu.Unlock()
		return
	}
	s.enabled = true

	handlers := map[string]http.HandlerFunc{
		SnapshotPath: s.serveSnapshot,
		EventsPath:   s.serveEvents,
	}
	if s.ui {
		ui := http.StripPrefix(UIPath, ui.Handler())
		handlers[UIPath] = ui.ServeHTTP
		// "/ui" without the slash, and the root, are conveniences: an
		// operator who port-forwards the endpoint and opens it should
		// land somewhere useful. The root pattern is registered only
		// when the UI is, so introspection never claims the shared
		// mux' catch-all on its own.
		handlers["/ui"] = redirectTo(UIPath)
		handlers["/"] = redirectTo(UIPath)
	}

	s.registered = make([]string, 0, len(handlers))
	for pattern := range handlers {
		s.registered = append(s.registered, pattern)
	}
	sort.Strings(s.registered) // deterministic registration order

	s.mu.Unlock()

	log.Infof("enabling introspection interface...")

	for _, pattern := range s.registered {
		s.mux.HandleFunc(pattern, handlers[pattern])
	}
}
```

```go
func (s *Server) Disable() {
	s.mu.Lock()
	if !s.enabled {
		s.mu.Unlock()
		return
	}

	log.Infof("disabling introspection interface...")

	s.enabled = false
	s.cached, s.cachedGen = nil, 0
	s.closeSubscribers()

	patterns := s.registered
	s.registered = nil

	s.mu.Unlock()

	for _, pattern := range patterns {
		s.mux.Unregister(pattern)
	}
}
```

and the helper:

```go
// redirectTo permanently sends a request to another path.
func redirectTo(path string) http.HandlerFunc {
	return func(w http.ResponseWriter, req *http.Request) {
		http.Redirect(w, req, path, http.StatusFound)
	}
}
```

Add `sort`, `net/http` (already there) and `"github.com/containers/nri-plugins/pkg/resmgr/introspect/ui"` to the imports. Note the local variable `ui` in `Enable` shadows the package name — rename the local to `assets` to avoid it.

There is a subtlety the existing tests will catch if you get it wrong: `handlers` is built while `s.mu` is held but registered after it is released, so the map must not be mutated afterwards. It is not.

- [ ] **Step 6: Wire the knob**

In `pkg/resmgr/resource-manager.go`'s `configureIntrospection`, call `SetUI` before `Enable`:

```go
	if cfg.IntrospectionEnabled() {
		if cfg.HTTPEndpoint == "" {
			log.Warnf("introspection is enabled but no HTTP endpoint is " +
				"configured; set instrumentation.httpEndpoint to serve it")
		}
		m.introspect.SetUI(cfg.IntrospectionUIEnabled())
		m.introspect.Enable()
	} else {
		m.introspect.Disable()
	}
```

Keep the existing warning; only the `SetUI` line is new. Ordering matters: `SetUI` must precede `Enable`, because `Enable` reads the flag to decide what to register.

- [ ] **Step 7: Add a test that the UI paths follow the knob**

Append to `pkg/resmgr/introspect/introspect_test.go`:

```go
func TestUIPathsFollowTheKnob(t *testing.T) {
	get := func(mux *xhttp.ServeMux, path string) int {
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, httptest.NewRequest(http.MethodGet, path, nil))
		return w.Code
	}

	t.Run("UI off serves no UI path", func(t *testing.T) {
		mux := xhttp.NewServeMux()
		s := New(mux, func() *api.Snapshot { return testSnapshot("worker-1") })
		s.Enable()

		if code := get(mux, "/ui/"); code != http.StatusNotFound {
			t.Errorf("/ui/ status %d with the UI off, expected 404", code)
		}
		if code := get(mux, "/"); code != http.StatusNotFound {
			t.Errorf("/ status %d with the UI off, expected 404", code)
		}
		if code := get(mux, SnapshotPath); code != http.StatusOK {
			t.Errorf("%s status %d, expected 200", SnapshotPath, code)
		}
	})

	t.Run("UI on serves the UI and the redirects", func(t *testing.T) {
		mux := xhttp.NewServeMux()
		s := New(mux, func() *api.Snapshot { return testSnapshot("worker-1") })
		s.SetUI(true)
		s.Enable()

		if code := get(mux, "/ui/"); code != http.StatusOK {
			t.Errorf("/ui/ status %d with the UI on, expected 200", code)
		}
		if code := get(mux, "/"); code != http.StatusFound {
			t.Errorf("/ status %d, expected a 302 redirect", code)
		}
		if code := get(mux, "/ui"); code != http.StatusFound {
			t.Errorf("/ui status %d, expected a 302 redirect", code)
		}
	})

	t.Run("Disable unregisters the UI paths too", func(t *testing.T) {
		mux := xhttp.NewServeMux()
		s := New(mux, func() *api.Snapshot { return testSnapshot("worker-1") })
		s.SetUI(true)
		s.Enable()
		s.Disable()

		for _, path := range []string{"/ui/", "/ui", "/", SnapshotPath, EventsPath} {
			if code := get(mux, path); code != http.StatusNotFound {
				t.Errorf("%s status %d after Disable, expected 404", path, code)
			}
		}
	})
}
```

The third subtest is the one that earns its keep: `Disable` now unregisters a remembered list rather than a hardcoded one, so a path registered and not remembered would leak past it.

- [ ] **Step 8: Regenerate and update the charts**

Run: `make generate`

Expected: the `ui` property appears in all six CRD copies and `IntrospectionConfig`'s deepcopy gains the pointer field. Confirm with `git status --short` that nothing unrelated was regenerated.

In each of the three `deployment/helm/*/values.yaml`, add `ui` under the existing `introspection` block, keeping alphabetical order:

```yaml
    introspection:
      enabled: false
      ui: true
```

- [ ] **Step 9: Verify**

Run each and paste the verbatim output:

```
go build ./...
go test ./pkg/apis/... ./pkg/resmgr/... ./pkg/introspect/... -count=1 -race
gofmt -l pkg/resmgr/introspect/ pkg/apis/config/v1alpha1/instrumentation/
git status --short
```

Expected: build clean, tests pass, `gofmt` silent, and no unexpected files.

- [ ] **Step 10: Commit**

```bash
git add pkg/resmgr/introspect/ pkg/apis/config/v1alpha1/instrumentation/ deployment/helm/ config/crd/
git commit -F - <<'EOF'
introspect: serve the visualization's assets.

Embed the UI assets and serve them at /ui/ when both
introspection and the ui knob are on, with /ui and the
root redirecting there so that port-forwarding the
endpoint and opening it lands somewhere useful. The root
pattern is registered only when the UI is, so
introspection never claims the shared mux' catch-all on
its own.

Enable and Disable now remember which patterns they
registered instead of naming each one twice, which is
what makes conditional registration safe to add. The
lock discipline is unchanged: state is decided under our
own mutex, the mux is touched with it released.

Signed-off-by: Krisztian Litkey <krisztian.litkey@intel.com>
Co-Authored-By: Claude Opus 5 (1M context) <noreply@anthropic.com>
EOF
```

---

### Task 2: The sample snapshot

**Files:**
- Create: `pkg/resmgr/introspect/ui/sample_test.go`
- Create: `pkg/resmgr/introspect/ui/test-setup.sh`, `pkg/resmgr/introspect/ui/test-cleanup.sh`
- Create (generated, committed): `pkg/resmgr/introspect/ui/assets/sample-snapshot.json`

**Interfaces:**
- Consumes: `introspect.Collect`, `introspect.Input`, `introspect.Pod`, `introspect.Container`, `introspect.Cache` — all exported by the predecessor plan, so this needs no access to package internals.
- Produces: `assets/sample-snapshot.json`, the fixture every later task is verified against.

**Gate: full `opus` review.** It is Go, and a wrong fixture would silently mislead every screenshot after it.

Why a committed fixture rather than a live plugin: the UI must be developable and screenshot-verifiable with no cluster, no VM and no running plugin. It ships embedded, which costs about 100 KB in the binary and gives an operator a reference snapshot to compare their node against.

The fixture is deliberately built from the `sample2` sysfs tree — 2 packages, 112 CPUs, 8 NUMA nodes of which **four have no CPUs at all**. Those four are the case the wire format's optional `package`/`die` fields exist for, and rendering them wrongly is the most likely visual defect in this plan.

- [ ] **Step 1: Fixture extraction scripts**

Create `pkg/resmgr/introspect/ui/test-setup.sh`, mode 0755:

```sh
#!/bin/sh

# The sample snapshot generator builds against pkg/sysfs' sample2 fixture:
# 2 packages, 112 CPUs, 8 NUMA nodes, four of them memory-only. Shared
# rather than duplicated; keep this in sync if those tarballs are renamed.

set -e

mkdir -p testdata
tar -C testdata -xJf ../../../sysfs/test-data-sample2.tar.xz
```

Create `pkg/resmgr/introspect/ui/test-cleanup.sh`, mode 0755:

```sh
#!/bin/sh

rm -fr testdata
```

Run: `chmod 755 pkg/resmgr/introspect/ui/test-*.sh && (cd pkg/resmgr/introspect/ui && ./test-setup.sh) && ls pkg/resmgr/introspect/ui/testdata`

Expected: `sample2`.

- [ ] **Step 2: The generator**

Create `pkg/resmgr/introspect/ui/sample_test.go` with the licence header, then:

```go
package ui_test

import (
	"encoding/json"
	"flag"
	"os"
	"path"
	"testing"

	nri "github.com/containerd/nri/pkg/api"
	v1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	cfgapi "github.com/containers/nri-plugins/pkg/apis/config/v1alpha1"
	"github.com/containers/nri-plugins/pkg/resmgr/introspect"
	"github.com/containers/nri-plugins/pkg/sysfs"
	"github.com/containers/nri-plugins/pkg/topology"
)

// update regenerates assets/sample-snapshot.json. It is a flag rather than
// an ordinary test because the file is a committed asset: regenerating it
// on every run would leave the tree dirty.
//
//	go test ./pkg/resmgr/introspect/ui/ -run TestSampleSnapshot -update
var update = flag.Bool("update", false, "regenerate assets/sample-snapshot.json")

const samplePath = "assets/sample-snapshot.json"

// TestSampleSnapshot regenerates the committed sample snapshot under
// -update, and otherwise checks that the committed one still parses and
// still covers every visual state the UI has to render.
func TestSampleSnapshot(t *testing.T) {
	if *update {
		generate(t)
	}

	data, err := os.ReadFile(samplePath)
	if err != nil {
		t.Fatalf("failed to read %s: %v", samplePath, err)
	}

	var snap map[string]any
	if err := json.Unmarshal(data, &snap); err != nil {
		t.Fatalf("%s is not valid JSON: %v", samplePath, err)
	}

	hw, ok := snap["hardware"].(map[string]any)
	if !ok {
		t.Fatal("no hardware section")
	}

	cpus, _ := hw["cpus"].([]any)
	if len(cpus) != 112 {
		t.Errorf("expected 112 CPUs, got %d", len(cpus))
	}

	nodes, _ := hw["numaNodes"].([]any)
	if len(nodes) != 8 {
		t.Errorf("expected 8 NUMA nodes, got %d", len(nodes))
	}

	// Four memory-only nodes, which is why the UI must treat an absent
	// package or die as "belongs to none" rather than as package 0.
	memoryOnly := 0
	for _, n := range nodes {
		node, _ := n.(map[string]any)
		if node["cpus"] != "" {
			continue
		}
		memoryOnly++
		if _, claims := node["package"]; claims {
			t.Errorf("memory-only node %v claims a package", node["id"])
		}
	}
	if memoryOnly != 4 {
		t.Errorf("expected 4 memory-only NUMA nodes, got %d", memoryOnly)
	}

	containers, _ := snap["containers"].([]any)
	if len(containers) < 6 {
		t.Errorf("expected at least 6 containers, got %d", len(containers))
	}

	// The fixture has to contain each state the map renders differently,
	// or a screenshot proves nothing about the ones it lacks.
	var (
		unpinned   bool
		overcommit bool
		shared     bool
	)
	derived, _ := snap["derived"].(map[string]any)
	commitment, _ := derived["cpuCommitment"].(map[string]any)
	sharers, _ := derived["cpuSharers"].(map[string]any)

	for _, c := range containers {
		ctr, _ := c.(map[string]any)
		applied, has := ctr["applied"].(map[string]any)
		if !has || applied["cpusetCpus"] == nil || applied["cpusetCpus"] == "" {
			unpinned = true
		}
	}
	for _, v := range commitment {
		if n, _ := v.(float64); n > 1000 {
			overcommit = true
		}
	}
	for _, v := range sharers {
		if n, _ := v.(float64); n > 1 {
			shared = true
		}
	}

	if !unpinned {
		t.Error("no unpinned container: the not-pinned path is unexercised")
	}
	if !overcommit {
		t.Error("no CPU over 1000m: the oversubscribed state is unexercised")
	}
	if !shared {
		t.Error("no CPU with more than one sharer: sharing is unexercised")
	}
}

func generate(t *testing.T) {
	t.Helper()

	cwd, err := os.Getwd()
	if err != nil {
		t.Fatalf("failed to get the working directory: %v", err)
	}

	root := path.Join(cwd, "testdata", "sample2")
	if _, err := os.Stat(root); err != nil {
		t.Fatalf("fixture missing, run ./test-setup.sh: %v", err)
	}

	t.Cleanup(func() { sysfs.SetSysRoot("") })
	sysfs.SetSysRoot(root)

	sys, err := sysfs.DiscoverSystem()
	if err != nil {
		t.Fatalf("failed to discover the fixture: %v", err)
	}

	snap := introspect.Collect(&introspect.Input{
		NodeName: "worker-1",
		Policy:   "topology-aware",
		Version:  "v0.0.0-sample",
		System:   sys,
		Cache:    sampleCache(),
		Config:   sampleConfig(),
	})

	// The generation the server would stamp. Any non-zero value is fine;
	// a round one reads better in a screenshot.
	snap.Generation = 42

	data, err := json.MarshalIndent(snap, "", "  ")
	if err != nil {
		t.Fatalf("failed to marshal the snapshot: %v", err)
	}

	if err := os.WriteFile(samplePath, append(data, '\n'), 0644); err != nil {
		t.Fatalf("failed to write %s: %v", samplePath, err)
	}

	t.Logf("wrote %s (%d bytes)", samplePath, len(data))
}

func sampleConfig() cfgapi.ResmgrConfig {
	return &cfgapi.TopologyAwarePolicy{
		TypeMeta: metav1.TypeMeta{
			APIVersion: "config.nri/v1alpha1",
			Kind:       "TopologyAwarePolicy",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:            "default",
			Namespace:       "kube-system",
			Generation:      7,
			ResourceVersion: "123456",
		},
	}
}

// sampleCache is the set of workloads the fixture describes. Between them
// they cover every state the machine map renders differently: exclusively
// pinned and fully committed, several containers sharing a pool, a
// container oversubscribed on a single CPU, one consuming a memory-only
// NUMA node, and one with no cpuset at all.
func sampleCache() introspect.Cache {
	pods := []*samplePod{
		{id: "pod-web", uid: "uid-web", name: "web-0", namespace: "default", qos: v1.PodQOSGuaranteed},
		{id: "pod-db", uid: "uid-db", name: "db-0", namespace: "default", qos: v1.PodQOSGuaranteed},
		{id: "pod-cache", uid: "uid-cache", name: "cache-0", namespace: "default", qos: v1.PodQOSBurstable},
		{id: "pod-dns", uid: "uid-dns", name: "coredns-7d4", namespace: "kube-system", qos: v1.PodQOSBurstable},
		{id: "pod-hpc", uid: "uid-hpc", name: "solver-0", namespace: "hpc", qos: v1.PodQOSGuaranteed},
		{id: "pod-mem", uid: "uid-mem", name: "bigmem-0", namespace: "hpc", qos: v1.PodQOSGuaranteed},
		{id: "pod-idle", uid: "uid-idle", name: "unpinned-0", namespace: "default", qos: v1.PodQOSBestEffort},
	}

	containers := []*sampleContainer{
		{
			// Exclusive: four CPUs, 4000m, so 1000m each — full.
			id: "ctr-web", podID: "pod-web", name: "nginx",
			cpuReq: "4", cpuLim: "4", memReq: "2Gi", memLim: "2Gi",
			cpus: "0-3", mems: "0", shares: 4096, quota: 400000, period: 100000,
			rdt: "guaranteed",
			hints: topology.Hints{"eth0": topology.Hint{
				Provider: "/sys/class/net/eth0", CPUs: "0-3", NUMAs: "0",
			}},
		},
		{
			// Exclusive, eight CPUs.
			id: "ctr-db", podID: "pod-db", name: "postgres",
			cpuReq: "8", cpuLim: "8", memReq: "16Gi", memLim: "16Gi",
			cpus: "4-11", mems: "0", shares: 8192, quota: 800000, period: 100000,
			rdt: "guaranteed",
		},
		{
			// Shared pool: 2000m over 16 CPUs is 125m each.
			id: "ctr-cache", podID: "pod-cache", name: "redis",
			cpuReq: "2", cpuLim: "4", memReq: "4Gi", memLim: "8Gi",
			cpus: "12-27", mems: "0", shares: 2048, quota: 400000, period: 100000,
			rdt: "burstable",
		},
		{
			// Same pool, so those CPUs have more than one sharer.
			id: "ctr-dns", podID: "pod-dns", name: "coredns",
			cpuReq: "100m", cpuLim: "200m", memReq: "70Mi", memLim: "170Mi",
			cpus: "12-27", mems: "0", shares: 102, quota: 20000, period: 100000,
		},
		{
			// Oversubscribed: 8000m allowed on one CPU.
			id: "ctr-hpc", podID: "pod-hpc", name: "solver",
			cpuReq: "8", cpuLim: "8", memReq: "8Gi", memLim: "8Gi",
			cpus: "28", mems: "1", shares: 8192,
		},
		{
			// Memory on a CPU-less node, CPUs on another package.
			id: "ctr-mem", podID: "pod-mem", name: "bigmem",
			cpuReq: "4", cpuLim: "4", memReq: "64Gi", memLim: "64Gi",
			cpus: "56-59", mems: "4", shares: 4096,
		},
		{
			// No cpuset at all: the not-pinned path.
			id: "ctr-idle", podID: "pod-idle", name: "sidecar",
			mems: "",
		},
	}

	c := &sampleCacheImpl{}
	for _, p := range pods {
		c.pods = append(c.pods, p)
	}
	for _, ctr := range containers {
		c.containers = append(c.containers, ctr)
	}
	return c
}

type sampleCacheImpl struct {
	pods       []introspect.Pod
	containers []introspect.Container
}

func (c *sampleCacheImpl) GetPods() []introspect.Pod             { return c.pods }
func (c *sampleCacheImpl) GetContainers() []introspect.Container { return c.containers }

type samplePod struct {
	id, uid, name, namespace string
	qos                      v1.PodQOSClass
}

func (p *samplePod) GetID() string               { return p.id }
func (p *samplePod) GetUID() string              { return p.uid }
func (p *samplePod) GetName() string             { return p.name }
func (p *samplePod) GetNamespace() string        { return p.namespace }
func (p *samplePod) GetQOSClass() v1.PodQOSClass { return p.qos }

type sampleContainer struct {
	id, podID, name              string
	cpuReq, cpuLim               string
	memReq, memLim               string
	cpus, mems                   string
	shares, quota, period        int64
	memLimit                     int64
	rdt, blockio                 string
	hints                        topology.Hints
}

func (c *sampleContainer) GetID() string                 { return c.id }
func (c *sampleContainer) GetPodID() string              { return c.podID }
func (c *sampleContainer) GetName() string               { return c.name }
func (c *sampleContainer) GetState() nri.ContainerState  { return nri.ContainerState_CONTAINER_RUNNING }
func (c *sampleContainer) GetQOSClass() v1.PodQOSClass   { return v1.PodQOSGuaranteed }
func (c *sampleContainer) GetCpusetCpus() string         { return c.cpus }
func (c *sampleContainer) GetCpusetMems() string         { return c.mems }
func (c *sampleContainer) GetCPUShares() int64           { return c.shares }
func (c *sampleContainer) GetCPUQuota() int64            { return c.quota }
func (c *sampleContainer) GetCPUPeriod() int64           { return c.period }
func (c *sampleContainer) GetMemoryLimit() int64         { return c.memLimit }
func (c *sampleContainer) GetRDTClass() string           { return c.rdt }
func (c *sampleContainer) GetBlockIOClass() string       { return c.blockio }
func (c *sampleContainer) GetTopologyHints() topology.Hints { return c.hints }

func (c *sampleContainer) GetResourceRequirements() v1.ResourceRequirements {
	r := v1.ResourceRequirements{Requests: v1.ResourceList{}, Limits: v1.ResourceList{}}
	for value, list := range map[string]v1.ResourceList{
		c.cpuReq: r.Requests, c.cpuLim: r.Limits,
	} {
		if value != "" {
			list[v1.ResourceCPU] = resource.MustParse(value)
		}
	}
	for value, list := range map[string]v1.ResourceList{
		c.memReq: r.Requests, c.memLim: r.Limits,
	} {
		if value != "" {
			list[v1.ResourceMemory] = resource.MustParse(value)
		}
	}
	return r
}
```

Two things to check while writing this rather than after:

1. The `GetResourceRequirements` map trick collapses if two of the four strings are equal — a map keyed by value loses duplicates. If `cpuReq == cpuLim` (which is true for the guaranteed containers), one entry is lost. **Write it as four plain `if` statements instead**, one per field. This is the kind of clever-looking construction that silently drops data.
2. `sampleContainer.GetQOSClass` returns a constant; give it a `qos` field and return that, so the table's QoS column shows more than one value.

- [ ] **Step 3: Generate and inspect**

Run:

```
(cd pkg/resmgr/introspect/ui && ./test-setup.sh)
go test ./pkg/resmgr/introspect/ui/ -run TestSampleSnapshot -update -count=1 -v
```

Expected: the file is written, then the assertions in the same run pass against it.

Then read it with human eyes before trusting it:

```
jq '{version, node, generation,
     cpus: (.hardware.cpus|length),
     nodes: (.hardware.numaNodes|length),
     memoryOnly: [.hardware.numaNodes[]|select(.cpus=="")|.id],
     containers: [.containers[]|{name, cpuRequest, cpus: .applied.cpusetCpus}],
     fullCPUs: [.derived.cpuCommitment|to_entries[]|select(.value==1000)|.key]|length,
     overCPUs: [.derived.cpuCommitment|to_entries[]|select(.value>1000)|.key]}' \
  pkg/resmgr/introspect/ui/assets/sample-snapshot.json
```

Expected: 112 CPUs, 8 nodes, `memoryOnly` listing four ids, twelve CPUs at exactly 1000m (web's four plus db's eight), and CPU 28 over.

Paste that output in your report — it is the evidence that the fixture covers what later screenshots claim to show.

- [ ] **Step 4: Verify and commit**

Run: `go test ./pkg/resmgr/introspect/... -count=1 && gofmt -l pkg/resmgr/introspect/ui/`

The sample is a committed asset, so stage it explicitly — `testdata/` is gitignored but `assets/` is not.

```bash
chmod 755 pkg/resmgr/introspect/ui/test-setup.sh pkg/resmgr/introspect/ui/test-cleanup.sh
git add pkg/resmgr/introspect/ui/
git commit -F - <<'EOF'
introspect/ui: add a sample snapshot to develop against.

The UI has to be developable and verifiable with no
cluster, no VM and no running plugin, so it ships a
snapshot built from pkg/sysfs' sample2 fixture: two
packages, 112 CPUs, eight NUMA nodes, four of them
memory-only.

Those four are the reason the wire format's package and
die fields are optional, and getting them wrong is the
most likely visual defect in this work — so the fixture
puts them on screen from the first render.

The workloads cover every state the map draws
differently: exclusively pinned and fully committed,
several containers sharing a pool, one oversubscribed on
a single CPU, one consuming a memory-only node, and one
with no cpuset at all.

Signed-off-by: Krisztian Litkey <krisztian.litkey@intel.com>
Co-Authored-By: Claude Opus 5 (1M context) <noreply@anthropic.com>
EOF
```

---

### Task 3: The wire layer and the model

**Files:**
- Create: `pkg/resmgr/introspect/ui/assets/js/wire.js`
- Create: `pkg/resmgr/introspect/ui/assets/js/model.js`
- Create: `pkg/resmgr/introspect/ui/assets/selftest.html`, `pkg/resmgr/introspect/ui/assets/js/selftest.js`

**Interfaces:**
- Consumes: `assets/sample-snapshot.json` from Task 2.
- Produces: `normalize()`, `parseCpuset()` from `wire.js`; `commitmentBucket()`, `BUCKETS`, `cpuIndex()`, `numaGroups()`, `coreColumns()`, `observations()`, `formatMilliCPU()`, `formatBytes()` from `model.js`.

**Gate: full `opus` review, plus a screenshot of `selftest.html`.** This is pure logic with no visual component, it is where every wire-format trap is handled, and everything downstream trusts it. It is the one JS task worth a full review seat.

There is no JS test runner and this plan does not add one — that would mean npm, which the spec rules out. Instead `selftest.html` runs assertions in the browser and prints them, so the same screenshot loop that verifies the visuals also verifies the logic.

- [ ] **Step 1: Write the self-test page first**

Create `pkg/resmgr/introspect/ui/assets/selftest.html`:

```html
<!DOCTYPE html>
<html lang="en">
  <head>
    <meta charset="utf-8">
    <title>introspection UI self-test</title>
    <style>
      body { background: #fcfcfb; color: #0b0b0b; margin: 24px;
             font: 14px/1.5 system-ui, -apple-system, "Segoe UI", sans-serif; }
      h1 { font-size: 16px; margin: 0 0 12px; }
      #summary { font-weight: 600; margin-bottom: 12px; }
      .pass { color: #006300; }
      .fail { color: #d03b3b; font-weight: 600; }
      ol { margin: 0; padding-left: 20px; }
      li { font-variant-numeric: tabular-nums; }
    </style>
  </head>
  <body>
    <h1>introspection UI self-test</h1>
    <div id="summary">running…</div>
    <ol id="results"></ol>
    <script type="module" src="./js/selftest.js"></script>
  </body>
</html>
```

Create `pkg/resmgr/introspect/ui/assets/js/selftest.js`:

```js
// A browser-run assertion page. There is no JS test runner here by design —
// that would mean a toolchain the spec rules out — so these run in the same
// headless Chrome the visual verification uses, and a screenshot of this page
// is the evidence.
import { normalize, parseCpuset } from './wire.js';
import {
  BUCKETS, commitmentBucket, cpuIndex, numaGroups, coreColumns,
  observations, formatMilliCPU, formatBytes,
} from './model.js';

const results = [];

// Async so that a check may await, which the fixture check does. Every
// call site awaits it, so the results stay in source order.
async function check(name, fn) {
  try {
    const problem = await fn();
    results.push({ name, ok: !problem, detail: problem || '' });
  } catch (e) {
    results.push({ name, ok: false, detail: `threw: ${e && e.message}` });
  }
}

function eq(got, want, what) {
  if (got !== want) return `${what}: got ${JSON.stringify(got)}, want ${JSON.stringify(want)}`;
  return null;
}

// --- wire.js: the absence traps ---------------------------------------

check('normalize tolerates an empty document', () => {
  const s = normalize({});
  return eq(s.pods.length, 0, 'pods')
    || eq(s.containers.length, 0, 'containers')
    || eq(s.hardware.cpus.length, 0, 'cpus')
    || eq(s.config, null, 'config')
    || eq(typeof s.derived.cpuCommitment, 'object', 'cpuCommitment');
});

check('an absent NUMA package is null, not zero', () => {
  const s = normalize({ hardware: { numaNodes: [
    { id: 4, cpus: '', memoryType: 'HBM', capacity: 68719476736 },
    { id: 0, cpus: '0-3', package: 0, die: 0, memoryType: 'DRAM', capacity: 1 },
  ] } });
  return eq(s.hardware.numaNodes[0].package, null, 'memory-only package')
    || eq(s.hardware.numaNodes[0].die, null, 'memory-only die')
    || eq(s.hardware.numaNodes[1].package, 0, 'real package 0');
});

check('an absent coreKind is null, not a default kind', () => {
  const s = normalize({ hardware: { cpus: [{ id: 0, online: true }] } });
  return eq(s.hardware.cpus[0].coreKind, null, 'coreKind');
});

check('absent frequencies are null, not 0 kHz', () => {
  const s = normalize({ hardware: { cpus: [{ id: 0, online: true }] } });
  const cpu = s.hardware.cpus[0];
  return eq(cpu.baseFreq, null, 'baseFreq')
    || eq(cpu.minFreq, null, 'minFreq')
    || eq(cpu.maxFreq, null, 'maxFreq');
});

check('an absent isolated flag is false', () => {
  const s = normalize({ hardware: { cpus: [{ id: 0, online: true }] } });
  return eq(s.hardware.cpus[0].isolated, false, 'isolated');
});

check('a container with no applied section reads as unpinned', () => {
  const s = normalize({ containers: [{ id: 'c', podId: 'p', name: 'n' }] });
  const c = s.containers[0];
  return eq(c.applied.cpusetCpus, '', 'cpusetCpus')
    || eq(c.pinned, false, 'pinned');
});

check('a container with a cpuset reads as pinned', () => {
  const s = normalize({ containers: [
    { id: 'c', podId: 'p', name: 'n', applied: { cpusetCpus: '0-1' } },
  ] });
  return eq(s.containers[0].pinned, true, 'pinned');
});

check('parseCpuset handles ranges, lists and empties', () => {
  return eq(JSON.stringify(parseCpuset('0-3,8')), '[0,1,2,3,8]', 'ranges and lists')
    || eq(JSON.stringify(parseCpuset('')), '[]', 'empty')
    || eq(JSON.stringify(parseCpuset(null)), '[]', 'null')
    || eq(JSON.stringify(parseCpuset('5')), '[5]', 'single')
    || eq(JSON.stringify(parseCpuset('not a cpuset')), '[]', 'garbage');
});

// --- model.js: buckets and indices ------------------------------------

check('commitment buckets split at the documented thresholds', () => {
  return eq(commitmentBucket(0), 0, 'idle')
    || eq(commitmentBucket(1), 1, 'just above idle')
    || eq(commitmentBucket(500), 1, 'half')
    || eq(commitmentBucket(501), 2, 'just above half')
    || eq(commitmentBucket(999), 2, 'just under full')
    || eq(commitmentBucket(1000), 3, 'full')
    || eq(commitmentBucket(1001), 4, 'over')
    || eq(BUCKETS.length, 5, 'bucket count');
});

check('cpuIndex joins hardware, derived and containers', () => {
  const s = normalize({
    hardware: { cpus: [{ id: 0, online: true }, { id: 1, online: true }] },
    containers: [
      { id: 'a', podId: 'p', name: 'a', applied: { cpusetCpus: '0-1' } },
      { id: 'b', podId: 'p', name: 'b', applied: { cpusetCpus: '1' } },
    ],
    derived: { cpuCommitment: { 0: 250, 1: 1200 }, cpuSharers: { 0: 1, 1: 2 } },
  });
  const idx = cpuIndex(s);
  return eq(idx.get(0).commitment, 250, 'cpu 0 commitment')
    || eq(idx.get(0).bucket, 1, 'cpu 0 bucket')
    || eq(idx.get(1).bucket, 4, 'cpu 1 bucket')
    || eq(idx.get(1).sharers, 2, 'cpu 1 sharers')
    || eq(idx.get(1).containers.length, 2, 'cpu 1 container count')
    || eq(idx.get(0).containers.length, 1, 'cpu 0 container count');
});

check('numaGroups keeps memory-only nodes out of a package', () => {
  const s = normalize({ hardware: {
    packages: [{ id: 0, cpus: '0-1' }],
    numaNodes: [
      { id: 0, cpus: '0-1', package: 0, die: 0, memoryType: 'DRAM', capacity: 1 },
      { id: 4, cpus: '', memoryType: 'HBM', capacity: 2 },
    ],
    cpus: [{ id: 0, online: true, numaNode: 0 }, { id: 1, online: true, numaNode: 0 }],
  } });
  const groups = numaGroups(s);
  const packaged = groups.filter((g) => g.package !== null);
  const orphans = groups.filter((g) => g.package === null);
  return eq(packaged.length, 1, 'packaged group count')
    || eq(orphans.length, 1, 'unassociated group count')
    || eq(orphans[0].nodes.length, 1, 'memory-only node count')
    || eq(orphans[0].nodes[0].cpus.length, 0, 'memory-only node CPU count');
});

check('coreColumns stacks hyperthread siblings in one column', () => {
  const cpus = [
    { id: 0, core: 0 }, { id: 8, core: 0 },
    { id: 1, core: 1 }, { id: 9, core: 1 },
  ];
  const cols = coreColumns(cpus);
  return eq(cols.length, 2, 'column count')
    || eq(cols[0].length, 2, 'threads in the first column')
    || eq(cols[0][0].id, 0, 'first thread')
    || eq(cols[0][1].id, 8, 'second thread');
});

check('observations name an unpinned node', () => {
  const none = normalize({
    hardware: { cpus: [{ id: 0, online: true }] },
    containers: [{ id: 'a', podId: 'p', name: 'a' }],
  });
  const some = normalize({
    hardware: { cpus: [{ id: 0, online: true }] },
    containers: [{ id: 'a', podId: 'p', name: 'a', applied: { cpusetCpus: '0' } }],
  });
  const said = observations(none).join(' ');
  if (!said.includes('cpuset')) return `expected an observation about cpusets, got ${JSON.stringify(said)}`;
  return eq(observations(some).length, 0, 'observations when pinned');
});

check('formatters are readable and unit-correct', () => {
  return eq(formatMilliCPU(1000), '1', 'a whole CPU')
    || eq(formatMilliCPU(2500), '2.5', 'a fractional CPU')
    || eq(formatMilliCPU(31), '31m', 'a small share')
    || eq(formatMilliCPU(0), '0', 'zero')
    || eq(formatBytes(0), '0', 'no bytes')
    || eq(formatBytes(1024), '1.0 KiB', 'kibibytes')
    || eq(formatBytes(34359738368), '32.0 GiB', 'gibibytes');
});

// --- the committed fixture -------------------------------------------

check('the sample snapshot normalizes and covers every state', async () => {
  const raw = await (await fetch('../sample-snapshot.json')).json();
  const s = normalize(raw);
  const idx = cpuIndex(s);

  const buckets = new Set([...idx.values()].map((e) => e.bucket));
  const missing = [0, 1, 3, 4].filter((b) => !buckets.has(b));
  if (missing.length) return `fixture never reaches buckets ${missing.join(',')}`;

  const orphans = numaGroups(s).filter((g) => g.package === null);
  return eq(s.hardware.cpus.length, 112, 'fixture CPU count')
    || eq(orphans.length, 1, 'unassociated group')
    || eq(orphans[0].nodes.length, 4, 'memory-only node count');
});

// --- report ----------------------------------------------------------

const failed = results.filter((r) => !r.ok);
const summary = document.getElementById('summary');
summary.textContent = failed.length
  ? `${failed.length} of ${results.length} FAILED`
  : `all ${results.length} checks passed`;
summary.className = failed.length ? 'fail' : 'pass';

const list = document.getElementById('results');
for (const r of results) {
  const li = document.createElement('li');
  li.className = r.ok ? 'pass' : 'fail';
  li.textContent = `${r.ok ? 'PASS' : 'FAIL'}  ${r.name}${r.detail ? ' — ' + r.detail : ''}`;
  list.appendChild(li);
}
```

**Every `check(...)` call must be `await`ed** — the helper is async so that the fixture check can `await fetch`. Prefix each call with `await`; they then run in source order and the results list reads top to bottom.

- [ ] **Step 2: Run it and watch it fail**

```
cd pkg/resmgr/introspect/ui/assets
(python3 -m http.server 8799 >/dev/null 2>&1 &) ; sleep 2
google-chrome --headless --disable-gpu --no-sandbox --hide-scrollbars \
  --virtual-time-budget=3000 --window-size=900,700 \
  --screenshot=/tmp/selftest-red.png http://localhost:8799/selftest.html
pkill -f "http.server 8799"
```

Expected: a page whose summary is a failure, because `wire.js` and `model.js` do not exist and the module import fails. Keep `/tmp/selftest-red.png` and describe what it shows in your report — that is this task's RED evidence.

- [ ] **Step 3: `wire.js`**

Create `pkg/resmgr/introspect/ui/assets/js/wire.js`:

```js
// The only module that knows the wire format's optional fields.
//
// Every field in the snapshot that can be absent is normalised here, once,
// so that nothing downstream has to remember which ones those are. The
// rules come from the package comment on pkg/introspect/api: absence means
// empty for aggregates, and for a handful of scalars it means "not known"
// rather than the zero value.

const num = (v, fallback = 0) => (typeof v === 'number' ? v : fallback);
const str = (v) => (typeof v === 'string' ? v : '');
const bool = (v) => v === true;
const arr = (v) => (Array.isArray(v) ? v : []);
const obj = (v) => (v && typeof v === 'object' && !Array.isArray(v) ? v : {});

// maybe returns null rather than a zero value, for the fields where absence
// means "not known". Rendering 0 for these would be a lie: an absent
// frequency is a kernel that did not report one, not a 0 kHz CPU.
const maybeNum = (v) => (typeof v === 'number' ? v : null);
const maybeStr = (v) => (typeof v === 'string' && v !== '' ? v : null);

/**
 * parseCpuset parses a Linux list-format set — "0-3,8" — into ids.
 * Anything unparseable yields an empty list rather than throwing: a
 * malformed cpuset means something upstream is broken, and the view's job
 * is to keep rendering.
 */
export function parseCpuset(s) {
  if (typeof s !== 'string' || s === '') return [];

  const ids = [];
  for (const part of s.split(',')) {
    const piece = part.trim();
    if (piece === '') continue;

    const dash = piece.indexOf('-');
    if (dash < 0) {
      const n = Number(piece);
      if (!Number.isInteger(n)) return [];
      ids.push(n);
      continue;
    }

    const lo = Number(piece.slice(0, dash));
    const hi = Number(piece.slice(dash + 1));
    if (!Number.isInteger(lo) || !Number.isInteger(hi) || hi < lo) return [];
    for (let i = lo; i <= hi; i++) ids.push(i);
  }
  return ids;
}

function normalizeCPU(raw) {
  const c = obj(raw);
  return {
    id: num(c.id),
    package: num(c.package),
    die: num(c.die),
    cluster: num(c.cluster),
    numaNode: num(c.numaNode),
    core: num(c.core),
    threads: str(c.threads),
    // Absent means the machine reports no core kinds. It does not mean
    // P-core, which is what the zero value of the underlying type claims.
    coreKind: maybeStr(c.coreKind),
    online: bool(c.online),
    isolated: bool(c.isolated),
    // Absent means the kernel did not report a frequency.
    baseFreq: maybeNum(c.baseFreq),
    minFreq: maybeNum(c.minFreq),
    maxFreq: maybeNum(c.maxFreq),
  };
}

function normalizeNUMANode(raw) {
  const n = obj(raw);
  return {
    id: num(n.id),
    // Absent means the node belongs to no package or die at all — a
    // memory-only CXL or HBM node. Null, never 0.
    package: maybeNum(n.package),
    die: maybeNum(n.die),
    cpus: str(n.cpus),
    memoryType: str(n.memoryType) || 'unknown',
    normalMemory: bool(n.normalMemory),
    capacity: num(n.capacity),
    distance: arr(n.distance).map((d) => num(d)),
  };
}

function normalizeContainer(raw) {
  const c = obj(raw);
  const applied = obj(c.applied);
  const cpusetCpus = str(applied.cpusetCpus);

  return {
    id: str(c.id),
    podId: str(c.podId),
    name: str(c.name),
    state: str(c.state) || 'unknown',
    qosClass: str(c.qosClass),
    cpuRequest: num(c.cpuRequest),
    cpuLimit: num(c.cpuLimit),
    memoryRequest: num(c.memoryRequest),
    memoryLimit: num(c.memoryLimit),
    applied: {
      cpusetCpus,
      cpusetMems: str(applied.cpusetMems),
      cpuShares: num(applied.cpuShares),
      cpuQuota: num(applied.cpuQuota),
      cpuPeriod: num(applied.cpuPeriod),
      memoryLimit: num(applied.memoryLimit),
    },
    // Derived, and the distinction the whole map rests on: a container
    // with no cpuset is not placed on any CPU, which is a normal state.
    pinned: cpusetCpus !== '',
    cpus: parseCpuset(cpusetCpus),
    mems: parseCpuset(str(applied.cpusetMems)),
    classes: obj(c.classes),
    hints: arr(c.hints).map((h) => ({
      provider: str(obj(h).provider),
      cpus: str(obj(h).cpus),
      numas: str(obj(h).numas),
      sockets: str(obj(h).sockets),
      irqs: arr(obj(h).irqs).map((i) => num(i)),
    })),
  };
}

/**
 * normalize turns a raw snapshot into one with no absent fields, so that
 * every consumer downstream can read it without guards. It never throws.
 */
export function normalize(raw) {
  const s = obj(raw);
  const hw = obj(s.hardware);
  const derived = obj(s.derived);
  const node = obj(s.node);
  const config = s.config ? obj(s.config) : null;

  return {
    version: str(s.version),
    generation: num(s.generation),
    timestamp: s.timestamp ? new Date(s.timestamp) : null,
    node: {
      name: str(node.name) || 'unknown',
      policy: str(node.policy) || 'none',
      version: str(node.version),
    },
    config: config && {
      name: str(config.name),
      namespace: str(config.namespace),
      generation: num(config.generation),
      resourceVersion: str(config.resourceVersion),
      data: config.data === undefined ? null : config.data,
    },
    hardware: {
      packages: arr(hw.packages).map((p) => ({
        id: num(obj(p).id),
        cpus: str(obj(p).cpus),
        dies: arr(obj(p).dies).map((d) => ({
          id: num(obj(d).id),
          cpus: str(obj(d).cpus),
          numaNodes: arr(obj(d).numaNodes).map((n) => num(n)),
          clusters: arr(obj(d).clusters).map((cl) => ({
            id: num(obj(cl).id),
            cpus: str(obj(cl).cpus),
          })),
        })),
      })),
      numaNodes: arr(hw.numaNodes).map(normalizeNUMANode),
      cpus: arr(hw.cpus).map(normalizeCPU),
      isolatedCpus: str(hw.isolatedCpus),
      offlineCpus: str(hw.offlineCpus),
      coreKinds: obj(hw.coreKinds),
    },
    pods: arr(s.pods).map((p) => ({
      id: str(obj(p).id),
      uid: str(obj(p).uid),
      name: str(obj(p).name),
      namespace: str(obj(p).namespace),
      qosClass: str(obj(p).qosClass),
    })),
    containers: arr(s.containers).map(normalizeContainer),
    derived: {
      cpuCommitment: obj(derived.cpuCommitment),
      cpuSharers: obj(derived.cpuSharers),
      memoryCommitment: obj(derived.memoryCommitment),
    },
    // Layer 2. Nothing in this plan populates or renders it; its absence
    // means the policy has nothing to say, which is not an error.
    policy: s.policy ? obj(s.policy) : null,
  };
}
```

- [ ] **Step 4: `model.js`**

Create `pkg/resmgr/introspect/ui/assets/js/model.js`:

```js
// Pure derivation over a normalised snapshot. No DOM access anywhere in
// this file, so it is testable in selftest.html and reviewable as ordinary
// logic.
import { parseCpuset } from './wire.js';

// A CPU is "full" at 1000 milli-CPU — one whole CPU's worth of requests
// spread onto it. The buckets are ordinal, and the ramp that renders them
// is a single hue stepped light to dark; over-full is the one state that
// gets a status colour, and it gets an icon and a label with it.
export const BUCKETS = [
  { key: 'idle', label: 'idle' },
  { key: 'low', label: '≤50%' },
  { key: 'mid', label: '≤100%' },
  { key: 'full', label: 'full' },
  { key: 'over', label: 'over' },
];

/** commitmentBucket maps milli-CPU of commitment to a BUCKETS index. */
export function commitmentBucket(mcpu) {
  if (!(mcpu > 0)) return 0;
  if (mcpu > 1000) return 4;
  if (mcpu >= 1000) return 3;
  if (mcpu > 500) return 2;
  return 1;
}

/**
 * cpuIndex joins the three independent views of a CPU — the hardware
 * description, the derived commitment, and which containers are allowed on
 * it — into one lookup keyed by CPU id.
 */
export function cpuIndex(snapshot) {
  const index = new Map();

  for (const cpu of snapshot.hardware.cpus) {
    const commitment = Number(snapshot.derived.cpuCommitment[cpu.id]) || 0;
    index.set(cpu.id, {
      cpu,
      commitment,
      bucket: commitmentBucket(commitment),
      sharers: Number(snapshot.derived.cpuSharers[cpu.id]) || 0,
      containers: [],
    });
  }

  for (const c of snapshot.containers) {
    for (const id of c.cpus) {
      const entry = index.get(id);
      // A cpuset can name a CPU this machine does not have; the collector
      // ignores those and so do we.
      if (entry) entry.containers.push(c);
    }
  }

  return index;
}

/**
 * numaGroups groups NUMA nodes under their package, with the nodes that
 * belong to no package in a trailing group whose `package` is null.
 *
 * That trailing group is not a rounding error: a memory-only CXL or HBM
 * node genuinely has no package, and putting it under package 0 — which is
 * what reading an absent field as 0 would do — would be a lie about the
 * machine.
 */
export function numaGroups(snapshot) {
  const byNode = new Map();
  for (const cpu of snapshot.hardware.cpus) {
    if (!byNode.has(cpu.numaNode)) byNode.set(cpu.numaNode, []);
    byNode.get(cpu.numaNode).push(cpu);
  }

  const groups = new Map();
  const orphans = [];

  for (const node of snapshot.hardware.numaNodes) {
    const entry = {
      node,
      cpus: (byNode.get(node.id) || []).slice().sort((a, b) => a.id - b.id),
      memoryCommitment: Number(snapshot.derived.memoryCommitment[node.id]) || 0,
    };

    if (node.package === null) {
      orphans.push(entry);
      continue;
    }
    if (!groups.has(node.package)) groups.set(node.package, []);
    groups.get(node.package).push(entry);
  }

  const out = [...groups.keys()].sort((a, b) => a - b).map((pkg) => ({
    package: pkg,
    nodes: groups.get(pkg).sort((a, b) => a.node.id - b.node.id),
  }));

  if (orphans.length) {
    out.push({
      package: null,
      nodes: orphans.sort((a, b) => a.node.id - b.node.id),
    });
  }

  return out;
}

/**
 * coreColumns groups a node's CPUs into one column per core, so that
 * hyperthread siblings stack vertically and a core reads as a unit.
 */
export function coreColumns(cpus) {
  const byCore = new Map();
  for (const cpu of cpus) {
    if (!byCore.has(cpu.core)) byCore.set(cpu.core, []);
    byCore.get(cpu.core).push(cpu);
  }
  return [...byCore.keys()]
    .sort((a, b) => a - b)
    .map((core) => byCore.get(core).sort((a, b) => a.id - b.id));
}

/**
 * observations states what the view can see but cannot explain. The reason
 * behind an observation is policy knowledge and arrives with layer 2; the
 * observation itself needs none, which is why it ships now.
 */
export function observations(snapshot) {
  const said = [];

  if (snapshot.containers.length > 0 && !snapshot.containers.some((c) => c.pinned)) {
    said.push(
      'No container on this node has an applied CPU set, so no CPU placement '
      + 'is shown. The policy may not be pinning CPUs.',
    );
  }

  if (snapshot.hardware.cpus.length === 0) {
    said.push('This snapshot carries no hardware description.');
  }

  return said;
}

/** formatMilliCPU renders milli-CPU the way an operator reads it. */
export function formatMilliCPU(mcpu) {
  if (!mcpu) return '0';
  if (mcpu < 1000) return `${mcpu}m`;
  const cpus = mcpu / 1000;
  return Number.isInteger(cpus) ? String(cpus) : cpus.toFixed(1);
}

const UNITS = ['B', 'KiB', 'MiB', 'GiB', 'TiB', 'PiB'];

/** formatBytes renders a byte count in binary units. */
export function formatBytes(bytes) {
  if (!bytes) return '0';
  let n = bytes;
  let unit = 0;
  while (n >= 1024 && unit < UNITS.length - 1) {
    n /= 1024;
    unit++;
  }
  return unit === 0 ? `${n} B` : `${n.toFixed(1)} ${UNITS[unit]}`;
}

/** cpusetOf re-exports the parser, so renderers need only this module. */
export { parseCpuset as cpusetOf };

/**
 * pinSlot returns 1, 2 or 3 for a pinned container, or 0 if it is not
 * pinned. Three is the palette's all-pairs cap, not a product choice: a
 * CPU grid is a context where any two cells can be neighbours, and the
 * validated categorical palette does not clear its floors past three hues.
 *
 * This lives here rather than in app.js so that the renderers depend on the
 * model and not on the application shell — importing it from app.js while
 * app.js imports the renderers is a cycle that works only by hoisting.
 */
export function pinSlot(pinned, containerID) {
  const at = pinned.indexOf(containerID);
  return at < 0 ? 0 : at + 1;
}

/**
 * containerLabel is "namespace/pod:container", degrading to the container
 * name when the pod is not in the snapshot.
 */
export function containerLabel(snapshot, container) {
  const pod = snapshot.pods.find((p) => p.id === container.podId);
  if (!pod) return container.name;
  return `${pod.namespace}/${pod.name}:${container.name}`;
}
```

- [ ] **Step 5: Run the self-test green**

Re-run the Step 2 command, writing to `/tmp/selftest-green.png`, and read it.

Expected: `all N checks passed`, in green, with every line `PASS`. If any line fails, fix the code — not the assertion — unless the assertion is provably wrong about the wire format, in which case say so and stop.

Paste the summary line in your report, and confirm that the `formatBytes(1024)` and `formatMilliCPU` expectations match what the code produces rather than adjusting the expectations to match a wrong implementation.

- [ ] **Step 6: Prove the self-test can fail**

A page that prints PASS is worthless if it cannot print FAIL. Temporarily change `commitmentBucket` so `1001` returns 3 instead of 4, re-run, and confirm the summary turns red and names the bucket check. Then revert.

Report both screenshots' summary lines. This step is the whole reason the self-test is trustworthy.

- [ ] **Step 7: Commit**

```bash
gofmt -l pkg/resmgr/introspect/ui/
git add pkg/resmgr/introspect/ui/assets/
git commit -F - <<'EOF'
introspect/ui: normalise the wire format, derive the model.

wire.js is the only module that knows a field can be
absent. Every optional field is normalised once, so
nothing downstream has to remember which ones mean
"empty" and which mean "not known" — an absent NUMA
package is no package rather than package 0, an absent
core kind is unknown rather than P-core, and an absent
frequency is unreported rather than 0 kHz.

model.js derives the view's numbers with no DOM access
at all: commitment buckets, the per-CPU join of
hardware, commitment and containers, the NUMA grouping
that keeps memory-only nodes out of a package, and the
observations the view can state without policy
knowledge.

selftest.html runs the assertions in the browser, since
a JS test runner would mean a toolchain the design rules
out. It was confirmed able to fail before being trusted.

Signed-off-by: Krisztian Litkey <krisztian.litkey@intel.com>
Co-Authored-By: Claude Opus 5 (1M context) <noreply@anthropic.com>
EOF
```

---

### Task 4: The shell

**Files:**
- Replace: `pkg/resmgr/introspect/ui/assets/index.html`
- Create: `pkg/resmgr/introspect/ui/assets/ui.css`
- Create: `pkg/resmgr/introspect/ui/assets/js/app.js`

**Interfaces:**
- Consumes: `normalize()` from `wire.js`; `observations()`, `formatMilliCPU()` from `model.js`.
- Produces: the document skeleton and its CSS roles; `app.js`'s `state` object and `render()` dispatch, which Tasks 5–8 extend.

**Gate: screenshot, plus a scoped `opus` spot-check of `app.js` only.** The CSS is verified by looking at it; `app.js` holds the state machine every later task hooks into, so it gets read.

This task ends with a page that shows the header, the theme toggle, the legend and any observations — no map yet. That is deliberate: it makes the next task's screenshot a clean comparison.

- [ ] **Step 1: `index.html`**

```html
<!DOCTYPE html>
<html lang="en">
  <head>
    <meta charset="utf-8">
    <meta name="viewport" content="width=device-width, initial-scale=1">
    <title>nri-plugins · node introspection</title>
    <link rel="stylesheet" href="./ui.css">
  </head>
  <body>
    <div class="viz-root">
      <header class="topbar">
        <h1 id="node-title">loading…</h1>
        <div class="topbar-right">
          <span id="live" class="live" title="snapshot generation">–</span>
          <button id="theme" type="button" class="ghost" aria-label="toggle theme">◐</button>
        </div>
      </header>

      <nav class="toolbar" aria-label="view controls">
        <div class="pins" id="pins" aria-live="polite"></div>
        <div class="views" role="tablist">
          <button id="view-map" type="button" class="tab" role="tab" aria-selected="true">map</button>
          <button id="view-table" type="button" class="tab" role="tab" aria-selected="false">table</button>
        </div>
      </nav>

      <p id="observations" class="observations" hidden></p>

      <main>
        <section id="map" class="panel" role="tabpanel" aria-label="machine map"></section>
        <section id="table" class="panel" role="tabpanel" aria-label="container table" hidden></section>
      </main>

      <footer class="legend" id="legend"></footer>

      <details class="config" id="config">
        <summary>effective configuration</summary>
        <div id="config-body"></div>
      </details>

      <div id="tooltip" class="tooltip" role="tooltip" hidden></div>
    </div>
    <script type="module" src="./js/app.js"></script>
  </body>
</html>
```

The `charset` line is load-bearing — see "One bug this loop already caught" in the executor notes.

- [ ] **Step 2: `ui.css`**

Every colour here comes from the validated palette in this plan's header. Do not add a colour that is not in that table.

```css
/* Roles first, rules second, so a theme change is one block rather than a
   hunt through the file. Dark mode is a selected set of steps validated
   against the dark surface, declared under both the OS preference and an
   explicit toggle, with the toggle winning either way. */

.viz-root {
  color-scheme: light;

  --surface-1: #fcfcfb;
  --plane: #f9f9f7;
  --ink: #0b0b0b;
  --ink-2: #52514e;
  --muted: #898781;
  --gridline: #e1e0d9;
  --baseline: #c3c2b7;
  --border: rgba(11, 11, 11, 0.10);

  /* Commitment ramp: one hue, light to dark, three fill steps for the
     three fill buckets. The validated ramp has four steps; the widest
     three are used so that "full" is unmistakable at cell size. */
  --ramp-low: #86b6ef;
  --ramp-mid: #2a78d6;
  --ramp-full: #1c5cab;

  /* Pinned-container identity, capped at three by the palette's
     all-pairs limit. A fourth pin displaces the oldest. */
  --pin-1: #2a78d6;
  --pin-2: #eb6834;
  --pin-3: #1baf7a;

  /* Status is reserved and never themed. Only over-commitment uses it,
     and never without its icon and label. */
  --critical: #d03b3b;

  --cell: 20px;
  --cell-gap: 2px;
  --radius: 4px;
}

@media (prefers-color-scheme: dark) {
  :root:where(:not([data-theme="light"])) .viz-root {
    color-scheme: dark;
    --surface-1: #1a1a19;
    --plane: #0d0d0d;
    --ink: #ffffff;
    --ink-2: #c3c2b7;
    --muted: #898781;
    --gridline: #2c2c2a;
    --baseline: #383835;
    --border: rgba(255, 255, 255, 0.10);
    --ramp-low: #184f95;
    --ramp-mid: #3987e5;
    --ramp-full: #86b6ef;
    --pin-1: #3987e5;
    --pin-2: #d95926;
    --pin-3: #199e70;
  }
}

:root[data-theme="dark"] .viz-root {
  color-scheme: dark;
  --surface-1: #1a1a19;
  --plane: #0d0d0d;
  --ink: #ffffff;
  --ink-2: #c3c2b7;
  --muted: #898781;
  --gridline: #2c2c2a;
  --baseline: #383835;
  --border: rgba(255, 255, 255, 0.10);
  --ramp-low: #184f95;
  --ramp-mid: #3987e5;
  --ramp-full: #86b6ef;
  --pin-1: #3987e5;
  --pin-2: #d95926;
  --pin-3: #199e70;
}

* { box-sizing: border-box; }

body {
  margin: 0;
  background: var(--plane);
  color: var(--ink);
  font: 14px/1.5 system-ui, -apple-system, "Segoe UI", sans-serif;
}

.viz-root { padding: 16px 20px 28px; }

.topbar {
  display: flex; align-items: baseline; justify-content: space-between;
  gap: 16px; margin-bottom: 4px;
}
.topbar h1 { font-size: 15px; font-weight: 600; margin: 0; }
.topbar-right { display: flex; align-items: center; gap: 12px; }
.live { color: var(--ink-2); font-variant-numeric: tabular-nums; font-size: 12px; }
.live.pulse { color: var(--ink); }

.ghost {
  background: none; border: 1px solid var(--border); color: var(--ink-2);
  border-radius: var(--radius); padding: 2px 8px; cursor: pointer; font: inherit;
}
.ghost:hover { color: var(--ink); }

.toolbar {
  display: flex; align-items: center; justify-content: space-between;
  gap: 16px; margin: 8px 0 14px;
}
.pins { display: flex; align-items: center; gap: 10px; min-height: 22px; flex-wrap: wrap; }
.pin { display: inline-flex; align-items: center; gap: 6px; font-size: 12px; color: var(--ink-2); }
.pin button { background: none; border: 0; color: var(--muted); cursor: pointer; font: inherit; }
.swatch { width: 10px; height: 10px; border-radius: 3px; display: inline-block; }
.swatch.s1 { background: var(--pin-1); }
.swatch.s2 { background: var(--pin-2); }
.swatch.s3 { background: var(--pin-3); }

.views { display: flex; gap: 4px; }
.tab {
  background: none; border: 1px solid var(--border); color: var(--ink-2);
  border-radius: var(--radius); padding: 3px 12px; cursor: pointer; font: inherit;
}
.tab[aria-selected="true"] { color: var(--ink); border-color: var(--baseline); }

.observations {
  margin: 0 0 14px; padding: 8px 12px;
  border: 1px solid var(--border); border-radius: var(--radius);
  background: var(--surface-1); color: var(--ink-2); font-size: 13px;
}

.panel { background: var(--surface-1); border: 1px solid var(--border);
         border-radius: 6px; padding: 14px; overflow-x: auto; }

.legend {
  display: flex; flex-wrap: wrap; gap: 18px;
  margin-top: 14px; color: var(--ink-2); font-size: 12px;
}
.legend-group { display: flex; align-items: center; gap: 8px; }
.legend-item { display: inline-flex; align-items: center; gap: 5px; }
.legend-key {
  width: 13px; height: 13px; border-radius: 3px; display: inline-block;
  border: 1px solid var(--gridline);
}
.legend-key.idle { background: var(--surface-1); }
.legend-key.low { background: var(--ramp-low); border-color: transparent; }
.legend-key.mid { background: var(--ramp-mid); border-color: transparent; }
.legend-key.full { background: var(--ramp-full); border-color: transparent; }
.legend-key.over { background: var(--critical); border-color: transparent; }
.legend-key.isolated { background: var(--surface-1); border-style: dashed;
                       border-color: var(--baseline); }
.legend-key.offline { background: var(--surface-1);
  background-image: repeating-linear-gradient(45deg, var(--gridline) 0 2px,
                                              transparent 2px 4px); }

.config { margin-top: 18px; color: var(--ink-2); font-size: 13px; }
.config summary { cursor: pointer; }

.tooltip {
  position: fixed; z-index: 10; pointer-events: none;
  background: var(--surface-1); color: var(--ink);
  border: 1px solid var(--baseline); border-radius: var(--radius);
  padding: 8px 10px; font-size: 12px; max-width: 320px;
  box-shadow: 0 2px 8px rgba(0, 0, 0, 0.14);
}
.tooltip dl { display: grid; grid-template-columns: auto auto; gap: 2px 10px; margin: 0; }
.tooltip dt { color: var(--muted); }
.tooltip dd { margin: 0; font-variant-numeric: tabular-nums; }
```

- [ ] **Step 3: `app.js`**

```js
// State, wiring and the render dispatch. Every later module is given a
// container element and the state, and returns nothing: the DOM is built
// fresh on each render, which is affordable at this size and removes a
// whole class of stale-node bug.
import { normalize } from './wire.js';
import { observations, pinSlot, containerLabel } from './model.js';

const SNAPSHOT_PATH = '/introspect';
const EVENTS_PATH = '/introspect/events';

export const state = {
  snapshot: null,
  view: 'map',          // 'map' | 'table'
  pinned: [],           // container ids, most recent last, at most three
  hover: null,          // { kind: 'cpu' | 'container', id }
  lastGeneration: 0,
  changedCPUs: new Set(),
};

/** snapshotURL honours a ?snapshot= override, which is how the committed
 *  sample is developed and screenshotted against with no plugin running. */
function snapshotURL() {
  const override = new URLSearchParams(location.search).get('snapshot');
  return override || SNAPSHOT_PATH;
}

/** slotOf and labelOf bind the model's helpers to the current state, so
 *  the shell's own rendering reads as tersely as the renderers'. */
const slotOf = (id) => pinSlot(state.pinned, id);
const labelOf = (c) => containerLabel(state.snapshot, c);

export function togglePin(containerID) {
  const at = state.pinned.indexOf(containerID);
  if (at >= 0) {
    state.pinned.splice(at, 1);
  } else {
    state.pinned.push(containerID);
    if (state.pinned.length > 3) state.pinned.shift();
  }
  render();
}

function renderHeader() {
  const s = state.snapshot;
  const hw = s.hardware;
  const title = document.getElementById('node-title');

  // textContent, never innerHTML: the node name and policy come off the
  // wire and are user-authored.
  title.textContent =
    `${s.node.name} · ${s.node.policy} · ${hw.cpus.length} cpu · `
    + `${hw.numaNodes.length} numa`;

  const live = document.getElementById('live');
  live.textContent = `gen ${s.generation}`;
}

function renderObservations() {
  const el = document.getElementById('observations');
  const said = observations(state.snapshot);

  el.textContent = said.join(' ');
  el.hidden = said.length === 0;
}

function renderLegend() {
  const el = document.getElementById('legend');
  el.replaceChildren();

  const group = (label, items) => {
    const g = document.createElement('div');
    g.className = 'legend-group';

    const name = document.createElement('span');
    name.textContent = label;
    g.appendChild(name);

    for (const [cls, text] of items) {
      const item = document.createElement('span');
      item.className = 'legend-item';

      const key = document.createElement('span');
      key.className = `legend-key ${cls}`;
      item.appendChild(key);

      const caption = document.createElement('span');
      caption.textContent = text;
      item.appendChild(caption);

      g.appendChild(item);
    }
    el.appendChild(g);
  };

  group('commitment', [
    ['idle', 'idle'], ['low', '≤50%'], ['mid', '≤100%'],
    ['full', 'full'], ['over', 'over ⚠'],
  ]);
  group('cpu', [['isolated', 'isolated'], ['offline', 'offline']]);
}

function renderPins() {
  const el = document.getElementById('pins');
  el.replaceChildren();

  if (!state.pinned.length) {
    const hint = document.createElement('span');
    hint.className = 'pin';
    hint.textContent = 'click a container to pin it (up to three)';
    el.appendChild(hint);
    return;
  }

  for (const id of state.pinned) {
    const c = state.snapshot.containers.find((x) => x.id === id);
    if (!c) continue;

    const pin = document.createElement('span');
    pin.className = 'pin';

    const swatch = document.createElement('span');
    swatch.className = `swatch s${slotOf(id)}`;
    pin.appendChild(swatch);

    const name = document.createElement('span');
    name.textContent = labelOf(c);
    pin.appendChild(name);

    const drop = document.createElement('button');
    drop.type = 'button';
    drop.textContent = '×';
    drop.setAttribute('aria-label', `unpin ${labelOf(c)}`);
    drop.addEventListener('click', () => togglePin(id));
    pin.appendChild(drop);

    el.appendChild(pin);
  }
}

/** render rebuilds everything that depends on state. Later tasks add the
 *  map and table renderers here; this task leaves those panels empty. */
export function render() {
  if (!state.snapshot) return;

  renderHeader();
  renderObservations();
  renderPins();
  renderLegend();

  document.getElementById('map').hidden = state.view !== 'map';
  document.getElementById('table').hidden = state.view !== 'table';
  document.getElementById('view-map')
    .setAttribute('aria-selected', String(state.view === 'map'));
  document.getElementById('view-table')
    .setAttribute('aria-selected', String(state.view === 'table'));
}

async function load() {
  const response = await fetch(snapshotURL(), { cache: 'no-store' });
  if (!response.ok) throw new Error(`${response.status} ${response.statusText}`);

  const next = normalize(await response.json());
  state.snapshot = next;
  state.lastGeneration = next.generation;
  render();
}

function wireControls() {
  document.getElementById('view-map').addEventListener('click', () => {
    state.view = 'map';
    render();
  });
  document.getElementById('view-table').addEventListener('click', () => {
    state.view = 'table';
    render();
  });
  document.getElementById('theme').addEventListener('click', () => {
    const root = document.documentElement;
    const now = root.getAttribute('data-theme');
    // Cycle light → dark → follow the OS.
    root.setAttribute('data-theme', now === 'dark' ? 'light' : 'dark');
  });
}

function fail(err) {
  const title = document.getElementById('node-title');
  title.textContent = `could not load the snapshot: ${err && err.message}`;
}

wireControls();
load().catch(fail);
```

- [ ] **Step 4: Screenshot it**

Run the loop from the executor notes, writing `/tmp/ui-shell.png`, and read it.

Expected, against the sample snapshot: the header reading `worker-1 · topology-aware · 112 cpu · 8 numa` — **with a real middot, not `Â·`** — `gen 42` on the right, the pin hint, map/table tabs with map selected, the two legend groups, an empty bordered panel, and the collapsed configuration summary. No observation line, because the sample has pinned containers.

For the dark screenshot, `app.js` honours a `?theme=` parameter — add this to
`wireControls()`, before the listeners:

```js
  // An explicit ?theme=dark|light makes the theme reproducible from a URL,
  // which is how the dark rendering gets screenshotted and how someone
  // shares a view with a colleague.
  const wanted = new URLSearchParams(location.search).get('theme');
  if (wanted === 'dark' || wanted === 'light') {
    document.documentElement.setAttribute('data-theme', wanted);
  }
```

Then screenshot `...&theme=dark` to `/tmp/ui-shell-dark.png` and read that too.

Expected in dark: the surface near-black, ink white, the legend keys clearly
distinct steps of one hue, and nothing illegible. Report both images.

- [ ] **Step 5: Commit**

```bash
git add pkg/resmgr/introspect/ui/assets/
git commit -F - <<'EOF'
introspect/ui: the document, the palette and the shell.

The header, the view tabs, the pin row, the legend and
the observation line, with every colour taken from the
palette the design validated rather than chosen here.

Dark mode is a selected set of steps stated under both
the OS preference and an explicit toggle, with the
toggle winning either way — not an inverted flip.

Every wire value reaches the DOM through textContent.
Pod and container names are user-authored and this view
never assembles markup from them.

Signed-off-by: Krisztian Litkey <krisztian.litkey@intel.com>
Co-Authored-By: Claude Opus 5 (1M context) <noreply@anthropic.com>
EOF
```

---

### Task 5: The machine map

**Files:**
- Create: `pkg/resmgr/introspect/ui/assets/js/machine.js`
- Modify: `pkg/resmgr/introspect/ui/assets/js/app.js` (call the renderer)
- Modify: `pkg/resmgr/introspect/ui/assets/ui.css` (append the map rules)

**Interfaces:**
- Consumes: `cpuIndex()`, `numaGroups()`, `coreColumns()`, `formatBytes()`, `formatMilliCPU()` from `model.js`; `pinSlot()` from `app.js`.
- Produces: `renderMachine(container, state)`.

**Gate: screenshot, and this is the task the screenshot exists for.** No review seat. Judge it by looking: is the machine legible, do the packages nest, do hyperthread siblings stack, are the four memory-only nodes visibly outside any package, is the ramp monotone, is the oversubscribed CPU marked.

The layout is arithmetic, not a library. One SVG per package group, one row per NUMA node, one column per core, one cell per CPU.

- [ ] **Step 1: `machine.js`**

```js
// The machine map: packages and NUMA nodes as nested boxes, one cell per
// CPU, filled by commitment. Structure uses no colour at all — boxes,
// dashed rings, hatching and labels — so nothing competes with the ramp.
import {
  cpuIndex, numaGroups, coreColumns, formatBytes, formatMilliCPU, pinSlot,
} from './model.js';

const SVG = 'http://www.w3.org/2000/svg';

// Geometry. These are the only magic numbers in the file and they are all
// here: cell size adapts in Task 6, everything else is fixed.
const GEOM = {
  cell: 20,
  gap: 2,
  radius: 3,
  nodeLabel: 128,     // width reserved for the NUMA node label and bar
  nodePadTop: 22,     // room above a node's cells for the cpu-id ruler
  nodeGap: 14,
  pkgPad: 12,
  pkgLabel: 20,
};

function el(name, attrs = {}) {
  const node = document.createElementNS(SVG, name);
  for (const [k, v] of Object.entries(attrs)) {
    if (v !== null && v !== undefined) node.setAttribute(k, String(v));
  }
  return node;
}

function text(x, y, value, cls) {
  const t = el('text', { x, y, class: cls });
  // Wire values reach the DOM as text nodes, never as markup.
  t.textContent = value;
  return t;
}

const BUCKET_CLASS = ['idle', 'low', 'mid', 'full', 'over'];

/** cellClasses builds the class list for one CPU cell. Everything except
 *  the commitment fill is a non-colour channel. */
function cellClasses(entry, slot) {
  const classes = ['cell', BUCKET_CLASS[entry.bucket]];
  if (!entry.cpu.online) classes.push('offline');
  if (entry.cpu.isolated) classes.push('isolated');
  if (slot) classes.push(`pinned p${slot}`);
  return classes.join(' ');
}

/** highestPinSlot returns the lowest-numbered pin slot among a CPU's
 *  containers, so that a CPU shared by two pinned containers shows the
 *  first one's ring rather than flickering between them. */
function highestPinSlot(entry, pinned) {
  let best = 0;
  for (const c of entry.containers) {
    const slot = pinSlot(pinned, c.id);
    if (slot && (best === 0 || slot < best)) best = slot;
  }
  return best;
}

function renderNode(group, entry, index, y, pinned, onCell) {
  const g = el('g', { class: 'numa' });
  const columns = coreColumns(entry.cpus);

  const label = `NUMA ${entry.node.id}`;
  g.appendChild(text(0, y + 14, label, 'numa-label'));

  const type = el('text', { x: 0, y: y + 30, class: 'numa-type' });
  type.textContent = entry.node.memoryType;
  g.appendChild(type);

  // Memory: a capacity track with a committed fill. Single hue, so the
  // memory type is a text chip rather than another colour.
  const barX = 0;
  const barY = y + 38;
  const barW = GEOM.nodeLabel - 12;
  const used = entry.node.capacity > 0
    ? Math.min(1, entry.memoryCommitment / entry.node.capacity)
    : 0;

  g.appendChild(el('rect', {
    x: barX, y: barY, width: barW, height: 6, rx: 3, class: 'mem-track',
  }));
  if (used > 0) {
    g.appendChild(el('rect', {
      x: barX, y: barY, width: Math.max(4, barW * used), height: 6, rx: 3,
      class: 'mem-fill',
    }));
  }
  g.appendChild(text(
    barX, barY + 18,
    `${formatBytes(entry.memoryCommitment)} / ${formatBytes(entry.node.capacity)}`,
    'mem-caption',
  ));

  // A node with no CPUs is memory only. Say so, rather than drawing an
  // empty grid that looks like a rendering failure.
  if (!entry.cpus.length) {
    g.appendChild(text(GEOM.nodeLabel, y + 20, 'memory only — no CPUs', 'numa-empty'));
    return { g, height: 60 };
  }

  const threads = Math.max(...columns.map((c) => c.length));
  const step = GEOM.cell + GEOM.gap;

  columns.forEach((column, col) => {
    const x = GEOM.nodeLabel + col * step;

    // The cpu-id ruler: every cell carries its own id too, but the ruler
    // makes a column scannable. Thinned when cells are dense.
    if (col % 2 === 0 || columns.length <= 16) {
      g.appendChild(text(x, y + 12, String(column[0].id), 'cpu-ruler'));
    }

    column.forEach((cpu, row) => {
      const entryForCPU = index.get(cpu.id);
      const cy = y + GEOM.nodePadTop + row * step;
      const slot = highestPinSlot(entryForCPU, pinned);

      const cell = el('rect', {
        x, y: cy, width: GEOM.cell, height: GEOM.cell, rx: GEOM.radius,
        class: cellClasses(entryForCPU, slot),
        tabindex: 0,
        role: 'img',
        'aria-label': `cpu ${cpu.id}, ${formatMilliCPU(entryForCPU.commitment)} committed`,
        'data-cpu': cpu.id,
      });
      g.appendChild(cell);
      onCell(cell, entryForCPU);

      // The over-commitment marker: a status colour never carries meaning
      // on its own, so the cell gets a glyph as well as a fill.
      if (entryForCPU.bucket === 4) {
        const warn = text(x + GEOM.cell / 2, cy + GEOM.cell - 5, '⚠', 'cell-warn');
        warn.setAttribute('text-anchor', 'middle');
        g.appendChild(warn);
      }
    });
  });

  const height = GEOM.nodePadTop + threads * step;
  return { g, height: Math.max(height, 60) };
}

/**
 * renderMachine draws the whole machine into container. It rebuilds from
 * scratch: at a few hundred cells that is cheaper than diffing and cannot
 * leave a stale node behind.
 *
 * onCell is called with each cell element and its index entry, so that
 * interaction is wired by the caller rather than by this module.
 */
export function renderMachine(container, state, onCell = () => {}) {
  container.replaceChildren();

  const snapshot = state.snapshot;
  const index = cpuIndex(snapshot);
  const groups = numaGroups(snapshot);

  if (!snapshot.hardware.cpus.length) {
    const empty = document.createElement('p');
    empty.className = 'numa-empty';
    empty.textContent = 'This snapshot carries no hardware description.';
    container.appendChild(empty);
    return;
  }

  for (const group of groups) {
    const box = document.createElement('section');
    box.className = 'pkg';

    const heading = document.createElement('h2');
    heading.className = 'pkg-label';
    // The trailing group is the one whose nodes belong to no package —
    // memory-only CXL or HBM. Naming it that way is the point: reading an
    // absent package as 0 would file them under package 0 and lie.
    heading.textContent = group.package === null
      ? 'memory-only nodes (no package)'
      : `package ${group.package}`;
    box.appendChild(heading);

    const svg = el('svg', { class: 'machine' });
    let y = 0;
    let widest = 0;

    for (const entry of group.nodes) {
      const { g, height } = renderNode(
        group, entry, index, y, state.pinned, onCell,
      );
      svg.appendChild(g);
      y += height + GEOM.nodeGap;

      const columns = coreColumns(entry.cpus).length;
      widest = Math.max(widest, GEOM.nodeLabel + columns * (GEOM.cell + GEOM.gap));
    }

    svg.setAttribute('width', Math.max(widest, 320));
    svg.setAttribute('height', Math.max(y, 40));
    box.appendChild(svg);
    container.appendChild(box);
  }
}
```

- [ ] **Step 2: The map's CSS**

Append to `ui.css`:

```css
.pkg { margin-bottom: 18px; }
.pkg:last-child { margin-bottom: 0; }
.pkg-label {
  font-size: 12px; font-weight: 600; color: var(--ink-2);
  margin: 0 0 6px; letter-spacing: 0.02em;
}
.machine { display: block; overflow: visible; }

.numa-label { font-size: 11px; font-weight: 600; fill: var(--ink); }
.numa-type { font-size: 10px; fill: var(--muted); }
.numa-empty { font-size: 11px; fill: var(--muted); color: var(--muted); }
.mem-track { fill: var(--gridline); }
.mem-fill { fill: var(--ramp-mid); }
.mem-caption { font-size: 10px; fill: var(--ink-2);
               font-variant-numeric: tabular-nums; }
.cpu-ruler { font-size: 9px; fill: var(--muted);
             font-variant-numeric: tabular-nums; }

.cell { stroke: transparent; stroke-width: 1; }
.cell.idle { fill: var(--surface-1); stroke: var(--gridline); }
.cell.low { fill: var(--ramp-low); }
.cell.mid { fill: var(--ramp-mid); }
.cell.full { fill: var(--ramp-full); }
.cell.over { fill: var(--critical); }
.cell.isolated { stroke: var(--baseline); stroke-dasharray: 2 2; }
.cell.offline { fill: var(--surface-1); stroke: var(--gridline); opacity: 0.55; }
.cell:focus { outline: 2px solid var(--ink); outline-offset: 1px; }
.cell-warn { font-size: 11px; fill: #ffffff; }

/* A pinned container's cells take a ring in its slot hue, plus a surface
   gap so two adjacent pinned cells stay distinguishable. Identity is never
   carried by fill: the fill is the commitment ramp. */
.cell.pinned { stroke-width: 2; }
.cell.pinned.p1 { stroke: var(--pin-1); }
.cell.pinned.p2 { stroke: var(--pin-2); }
.cell.pinned.p3 { stroke: var(--pin-3); }

/* Everything recedes while something is pinned, so the pinned cells read
   as the figure rather than competing with the rest of the ground. */
.machine.has-pins .cell:not(.pinned) { opacity: 0.45; }
```

- [ ] **Step 3: Call it from `app.js`**

Add the import and the call in `render()`:

```js
import { renderMachine } from './machine.js';
```

```js
  if (state.view === 'map') {
    const map = document.getElementById('map');
    renderMachine(map, state);
    map.querySelectorAll('svg.machine').forEach((svg) => {
      svg.classList.toggle('has-pins', state.pinned.length > 0);
    });
  }
```

Place it after the header and observation rendering, before the panel visibility lines.

- [ ] **Step 4: Screenshot and judge it**

Run the loop, `/tmp/ui-map.png`, window `1500x1000`, and read it. Then the same with `&theme=dark`.

Judge these specifically, and say in your report what you saw for each:

1. **Two package sections plus a third headed "memory-only nodes (no package)"** containing four nodes, each saying "memory only — no CPUs". If those four appear inside package 0, the absent-package trap was not handled.
2. **Hyperthread siblings stacked**: each column is two cells tall on this fixture, and the two cells in a column are the same core.
3. **A monotone ramp**: web's CPUs 0–3 and db's 4–11 at the darkest step (light mode), the shared pool 12–27 at the lightest, and most of the machine idle.
4. **CPU 28 in red with a ⚠ glyph**, and nothing else red.
5. **Memory bars** filled proportionally, with a byte caption, including on the memory-only nodes where the bar has a fill but the grid does not exist.
6. **No overflow**: the widest package fits or scrolls horizontally within its panel rather than spilling out of it.
7. **In dark mode**, the ramp runs the other way — lightest for most-committed — and every step is distinguishable from the near-black surface.

If any of those is wrong, fix it and screenshot again before reporting. Include the final images' filenames in your report and describe each of the seven.

- [ ] **Step 5: Commit**

```bash
git add pkg/resmgr/introspect/ui/assets/
git commit -F - <<'EOF'
introspect/ui: draw the machine.

Packages and NUMA nodes as nested boxes, one column per
core so hyperthread siblings stack, one cell per CPU
filled by how much of it is committed. Structure uses no
colour at all — boxes, a dashed ring for isolated,
hatching for offline, and a label on every cell — so
nothing competes with the ramp.

Nodes that belong to no package get their own section
rather than being filed under package 0. That is the
whole reason the wire format made those fields optional,
and the sample snapshot has four of them so the case is
on screen from the first render.

Over-commitment is the one state that takes a status
colour, and it takes a glyph with it: a status colour
never carries meaning alone.

Signed-off-by: Krisztian Litkey <krisztian.litkey@intel.com>
Co-Authored-By: Claude Opus 5 (1M context) <noreply@anthropic.com>
EOF
```

---

### Task 6: Interaction

**Files:**
- Create: `pkg/resmgr/introspect/ui/assets/js/tooltip.js`
- Modify: `pkg/resmgr/introspect/ui/assets/js/app.js`
- Modify: `pkg/resmgr/introspect/ui/assets/ui.css`

**Interfaces:**
- Consumes: `renderMachine`'s `onCell` hook from Task 5; `togglePin`, `pinSlot`, `containerLabel` from `app.js`.
- Produces: `showTooltip(anchorEvent, rows)`, `hideTooltip()`.

**Gate: screenshot with the pointer simulated, plus a scoped `opus` spot-check of the keyboard handling.** A tooltip cannot be screenshotted by hovering, so the plan drives it from script.

- [ ] **Step 1: `tooltip.js`**

```js
// A single reusable tooltip node. Rows arrive as [label, value] pairs and
// are appended as text, never as markup: several of these values are
// user-authored container names.
const PAD = 12;

export function showTooltip(event, rows) {
  const el = document.getElementById('tooltip');
  el.replaceChildren();

  const dl = document.createElement('dl');
  for (const [label, value] of rows) {
    const dt = document.createElement('dt');
    dt.textContent = label;
    const dd = document.createElement('dd');
    dd.textContent = value;
    dl.append(dt, dd);
  }
  el.appendChild(dl);
  el.hidden = false;

  // Place it near the anchor, flipped when it would leave the viewport.
  const box = el.getBoundingClientRect();
  const x = event.clientX + PAD;
  const y = event.clientY + PAD;
  el.style.left = `${x + box.width > window.innerWidth ? x - box.width - 2 * PAD : x}px`;
  el.style.top = `${y + box.height > window.innerHeight ? y - box.height - 2 * PAD : y}px`;
}

export function hideTooltip() {
  const el = document.getElementById('tooltip');
  el.hidden = true;
  el.replaceChildren();
}
```

- [ ] **Step 2: Wire the cells in `app.js`**

Add the imports, then replace the map-rendering block in `render()`:

```js
import { showTooltip, hideTooltip } from './tooltip.js';
import { formatMilliCPU } from './model.js';
```

```js
  if (state.view === 'map') {
    const map = document.getElementById('map');
    renderMachine(map, state, wireCell);
    map.querySelectorAll('svg.machine').forEach((svg) => {
      svg.classList.toggle('has-pins', state.pinned.length > 0);
    });
  }
```

and add the handler:

```js
/** cpuTooltipRows describes one CPU. Absent values say so rather than
 *  rendering a zero: an unreported frequency is not 0 kHz, and an unknown
 *  core kind is not a P-core. */
function cpuTooltipRows(entry) {
  const cpu = entry.cpu;
  const rows = [
    ['cpu', String(cpu.id)],
    ['package / die', `${cpu.package} / ${cpu.die}`],
    ['numa node', String(cpu.numaNode)],
    ['core / threads', `${cpu.core} · ${cpu.threads}`],
    ['core kind', cpu.coreKind || 'not reported'],
    ['committed', `${formatMilliCPU(entry.commitment)} of 1`],
    ['containers allowed', String(entry.sharers)],
  ];

  if (cpu.maxFreq !== null) {
    rows.push(['max frequency', `${(cpu.maxFreq / 1000).toFixed(0)} MHz`]);
  }
  if (!cpu.online) rows.push(['state', 'offline']);
  if (cpu.isolated) rows.push(['state', 'isolated']);

  for (const c of entry.containers) {
    rows.push(['·', labelOf(c)]);
  }
  return rows;
}

function wireCell(cell, entry) {
  cell.addEventListener('mouseenter', (e) => showTooltip(e, cpuTooltipRows(entry)));
  cell.addEventListener('mousemove', (e) => showTooltip(e, cpuTooltipRows(entry)));
  cell.addEventListener('mouseleave', hideTooltip);
  cell.addEventListener('focus', (e) => showTooltip(e, cpuTooltipRows(entry)));
  cell.addEventListener('blur', hideTooltip);

  // Clicking a CPU pins the first container on it, which is the gesture an
  // operator reaches for: "what else is on this CPU".
  cell.addEventListener('click', () => {
    if (entry.containers.length) togglePin(entry.containers[0].id);
  });

  cell.addEventListener('keydown', (e) => {
    if (e.key === 'Enter' || e.key === ' ') {
      e.preventDefault();
      if (entry.containers.length) togglePin(entry.containers[0].id);
      return;
    }
    // Arrow keys walk the grid: cells are focusable in document order,
    // which is column-major, so Up and Down move between hyperthread
    // siblings and Left and Right between cores.
    const step = { ArrowRight: 1, ArrowDown: 1, ArrowLeft: -1, ArrowUp: -1 }[e.key];
    if (!step) return;
    e.preventDefault();

    const cells = [...document.querySelectorAll('.cell')];
    const at = cells.indexOf(cell);
    const next = cells[at + step];
    if (next) next.focus();
  });
}
```

Note the arrow-key mapping is a simplification: document order is column-major within a NUMA node, so Left/Right and Up/Down both walk the same linear sequence with different strides. State that limitation in a comment rather than pretending the grid is navigated two-dimensionally — a two-dimensional walk needs a coordinate map, which is not worth it until someone asks.

- [ ] **Step 3: Highlight on hover from the pin row**

Add to `renderPins()`, inside the loop after `name` is appended:

```js
    pin.addEventListener('mouseenter', () => highlightContainer(id, true));
    pin.addEventListener('mouseleave', () => highlightContainer(id, false));
```

and the helper:

```js
/** highlightContainer rings a container's cells without re-rendering, so
 *  hovering stays cheap. */
function highlightContainer(containerID, on) {
  const c = state.snapshot.containers.find((x) => x.id === containerID);
  if (!c) return;

  const wanted = new Set(c.cpus);
  for (const cell of document.querySelectorAll('.cell')) {
    const id = Number(cell.getAttribute('data-cpu'));
    cell.classList.toggle('highlight', on && wanted.has(id));
  }
}
```

- [ ] **Step 4: CSS**

```css
.cell.highlight { stroke: var(--ink); stroke-width: 2; }
```

- [ ] **Step 5: Screenshot the tooltip**

A headless screenshot cannot hover, so drive it. Create `/tmp/tooltip-probe.js` and run Chrome with `--virtual-time-budget`, having appended a temporary line to `app.js` that fires a synthetic hover when `?probe=tooltip` is present:

```js
  // Development aid: ?probe=tooltip opens the tooltip on a known cell so
  // that the headless screenshot can capture it. Remove before committing.
  if (new URLSearchParams(location.search).get('probe') === 'tooltip') {
    const cell = document.querySelector('.cell[data-cpu="0"]');
    if (cell) cell.dispatchEvent(new MouseEvent('mouseenter',
      { clientX: 420, clientY: 260, bubbles: true }));
  }
```

Put that at the end of `render()`, screenshot `...&probe=tooltip` to `/tmp/ui-tooltip.png`, read it, **then delete those lines and screenshot once more to confirm the page is unchanged without them.** Do not commit the probe.

Expected in the tooltip: cpu 0, its package/die/node/core, `core kind` reading a real kind or "not reported", `committed 1 of 1`, one container allowed, and `default/web-0:nginx` beneath. Check specifically that no row shows a bare `0` for a value that is actually absent.

Also screenshot with two containers pinned by appending `&probe=pins` handled the same temporary way, or simply by clicking through in a non-headless run if one is available. Expected: two slot hues as rings, the rest of the machine at reduced opacity, and the pin row naming both.

- [ ] **Step 6: Commit** (message: `introspect/ui: hover, pin and keyboard interaction.`, body describing that identity is carried by ring and opacity rather than fill, and that a status colour and a pin hue never collide because they occupy different channels)

---

### Task 7: The table and the configuration panel

**Files:**
- Create: `pkg/resmgr/introspect/ui/assets/js/table.js`, `js/config.js`
- Modify: `pkg/resmgr/introspect/ui/assets/js/app.js`, `ui.css`

**Interfaces:**
- Consumes: `formatMilliCPU`, `formatBytes` from `model.js`; `togglePin`, `pinSlot`, `containerLabel` from `app.js`.
- Produces: `renderTable(container, state)`, `renderConfig(container, state)`.

**Gate: screenshot of both, plus a scoped `opus` spot-check of `config.js`.** The configuration panel renders arbitrary user-authored nested JSON and is the sharpest injection surface in the UI, so its construction gets read.

- [ ] **Step 1: `table.js`**

```js
// One row per container. This is the relief channel the light-mode contrast
// warning obligates, and it is the thing an operator pastes into a bug
// report, so every value is present as text rather than encoded in a glyph.
import {
  formatMilliCPU, formatBytes, pinSlot, containerLabel,
} from './model.js';

const COLUMNS = [
  { key: 'name', label: 'container', get: (c, ctx) => containerLabel(ctx.snapshot, c) },
  { key: 'qos', label: 'QoS', get: (c) => c.qosClass || '—' },
  { key: 'state', label: 'state', get: (c) => c.state },
  { key: 'cpuReq', label: 'cpu req', get: (c) => formatMilliCPU(c.cpuRequest), num: true },
  { key: 'cpuLim', label: 'cpu lim', get: (c) => formatMilliCPU(c.cpuLimit), num: true },
  { key: 'memReq', label: 'mem req', get: (c) => formatBytes(c.memoryRequest), num: true },
  { key: 'cpuset', label: 'cpuset', get: (c) => c.applied.cpusetCpus || 'not pinned' },
  { key: 'mems', label: 'mems', get: (c) => c.applied.cpusetMems || '—' },
  { key: 'shares', label: 'shares', get: (c) => String(c.applied.cpuShares || 0), num: true },
  {
    key: 'excl', label: 'exclusive',
    // Exclusivity is derivable without policy input: a CPU with exactly
    // one container allowed on it is effectively exclusive to it.
    get: (c, ctx) => {
      if (!c.pinned) return '—';
      const sharers = ctx.snapshot.derived.cpuSharers;
      const alone = c.cpus.every((id) => (Number(sharers[id]) || 0) <= 1);
      return alone ? 'yes' : 'shared';
    },
  },
  { key: 'classes', label: 'classes', get: (c) => Object.entries(c.classes)
      .map(([k, v]) => `${k}=${v}`).join(' ') || '—' },
];

let sortKey = 'name';
let sortDesc = false;
let filter = '';

export function renderTable(container, state, onActivate = () => {}) {
  container.replaceChildren();

  const controls = document.createElement('div');
  controls.className = 'table-controls';

  const box = document.createElement('input');
  box.type = 'search';
  box.placeholder = 'filter containers';
  box.value = filter;
  box.addEventListener('input', () => {
    filter = box.value.toLowerCase();
    renderTable(container, state, onActivate);
    box.focus();
  });
  controls.appendChild(box);
  container.appendChild(controls);

  const rows = state.snapshot.containers.filter((c) => {
    if (!filter) return true;
    const label = containerLabel(state.snapshot, c);
    return (label + ' ' + c.applied.cpusetCpus).toLowerCase().includes(filter);
  });

  const column = COLUMNS.find((c) => c.key === sortKey) || COLUMNS[0];
  rows.sort((a, b) => {
    const av = column.get(a, state);
    const bv = column.get(b, state);
    const cmp = column.num
      ? (parseFloat(av) || 0) - (parseFloat(bv) || 0)
      : String(av).localeCompare(String(bv));
    return sortDesc ? -cmp : cmp;
  });

  const table = document.createElement('table');
  table.className = 'containers';

  const head = document.createElement('tr');
  for (const col of COLUMNS) {
    const th = document.createElement('th');
    th.textContent = col.label + (sortKey === col.key ? (sortDesc ? ' ↓' : ' ↑') : '');
    th.tabIndex = 0;
    const sort = () => {
      if (sortKey === col.key) sortDesc = !sortDesc;
      else { sortKey = col.key; sortDesc = false; }
      renderTable(container, state, onActivate);
    };
    th.addEventListener('click', sort);
    th.addEventListener('keydown', (e) => {
      if (e.key === 'Enter' || e.key === ' ') { e.preventDefault(); sort(); }
    });
    head.appendChild(th);
  }
  table.appendChild(head);

  for (const c of rows) {
    const tr = document.createElement('tr');
    const slot = pinSlot(state.pinned, c.id);
    if (slot) tr.className = `pinned p${slot}`;
    tr.tabIndex = 0;
    tr.addEventListener('click', () => onActivate(c.id));
    tr.addEventListener('keydown', (e) => {
      if (e.key === 'Enter' || e.key === ' ') { e.preventDefault(); onActivate(c.id); }
    });

    for (const col of COLUMNS) {
      const td = document.createElement('td');
      if (col.num) td.className = 'num';
      // textContent: container names, cpusets and class names are all
      // user-authored.
      td.textContent = String(col.get(c, state));
      tr.appendChild(td);
    }
    table.appendChild(tr);
  }

  if (!rows.length) {
    const empty = document.createElement('p');
    empty.className = 'numa-empty';
    empty.textContent = state.snapshot.containers.length
      ? 'No container matches the filter.'
      : 'No containers on this node.';
    container.appendChild(empty);
    return;
  }

  container.appendChild(table);
}
```

- [ ] **Step 2: `config.js`**

```js
// The configuration panel is a viewer, not a parser. It does not interpret
// a single key: apart from the common section every key in this blob is
// policy-specific, and a consumer that keyed off names would be encoding
// policy knowledge in the one layer defined to have none.
//
// It renders arbitrary user-authored nested JSON, which makes it the
// sharpest injection surface in this UI. Every key and value below is
// appended as a text node.

function renderValue(parent, value) {
  if (value === null || typeof value !== 'object') {
    const span = document.createElement('span');
    span.className = 'cfg-scalar';
    span.textContent = value === null ? 'null' : String(value);
    parent.appendChild(span);
    return;
  }

  const list = document.createElement('ul');
  list.className = 'cfg-tree';

  for (const [key, child] of Object.entries(value)) {
    const item = document.createElement('li');

    const name = document.createElement('span');
    name.className = 'cfg-key';
    name.textContent = Array.isArray(value) ? `[${key}]` : key;
    item.appendChild(name);

    renderValue(item, child);
    list.appendChild(item);
  }

  parent.appendChild(list);
}

export function renderConfig(container, state) {
  container.replaceChildren();

  const config = state.snapshot.config;
  if (!config) {
    const none = document.createElement('p');
    none.className = 'numa-empty';
    none.textContent = 'No effective configuration reported.';
    container.appendChild(none);
    return;
  }

  const head = document.createElement('p');
  head.className = 'cfg-head';
  head.textContent =
    `${config.namespace}/${config.name} · generation ${config.generation}`;
  container.appendChild(head);

  if (config.data === null) {
    const note = document.createElement('p');
    note.className = 'numa-empty';
    // An absent data field means the configuration could not be
    // serialized, not that it was empty.
    note.textContent = 'The configuration could not be serialized.';
    container.appendChild(note);
    return;
  }

  renderValue(container, config.data);
}
```

- [ ] **Step 3: CSS and wiring**

```css
.table-controls { margin-bottom: 10px; }
.table-controls input {
  font: inherit; padding: 3px 8px; border-radius: var(--radius);
  border: 1px solid var(--border); background: var(--plane); color: var(--ink);
  min-width: 220px;
}
table.containers { border-collapse: collapse; font-size: 12px; width: 100%; }
table.containers th {
  text-align: left; font-weight: 600; color: var(--ink-2);
  border-bottom: 1px solid var(--baseline); padding: 4px 10px 4px 0;
  cursor: pointer; white-space: nowrap;
}
table.containers td {
  padding: 3px 10px 3px 0; border-bottom: 1px solid var(--gridline);
  color: var(--ink); white-space: nowrap;
}
table.containers td.num { font-variant-numeric: tabular-nums; text-align: right;
                          padding-right: 18px; }
table.containers tr.pinned td:first-child { border-left: 3px solid transparent; padding-left: 6px; }
table.containers tr.pinned.p1 td:first-child { border-left-color: var(--pin-1); }
table.containers tr.pinned.p2 td:first-child { border-left-color: var(--pin-2); }
table.containers tr.pinned.p3 td:first-child { border-left-color: var(--pin-3); }
table.containers tr:focus { outline: 2px solid var(--ink); outline-offset: -2px; }

.cfg-head { color: var(--ink); font-size: 12px; margin: 8px 0; }
ul.cfg-tree { list-style: none; margin: 0 0 0 14px; padding: 0;
              border-left: 1px solid var(--gridline); }
ul.cfg-tree li { padding: 1px 0 1px 10px; }
.cfg-key { color: var(--ink-2); margin-right: 8px; }
.cfg-scalar { color: var(--ink); font-variant-numeric: tabular-nums; }
```

In `app.js`, import both and call them from `render()`:

```js
  if (state.view === 'table') {
    renderTable(document.getElementById('table'), state, togglePin);
  }
  renderConfig(document.getElementById('config-body'), state);
```

- [ ] **Step 4: Screenshot both**

`/tmp/ui-table.png`: screenshot with the table tab selected. Since clicking is not available headless, add `?view=table` handling in `wireControls()` the same way `?theme=` was added — that one stays, it is genuinely useful:

```js
  const view = new URLSearchParams(location.search).get('view');
  if (view === 'table' || view === 'map') state.view = view;
```

Expected: eleven columns, seven rows, `not pinned` in the cpuset column for the sidecar, `shared` in the exclusive column for the two containers on 12–27 and `yes` for web and db, right-aligned tabular numbers.

`/tmp/ui-config.png`: the `<details>` is collapsed by default, so add `open` to it temporarily, screenshot, and revert. Expected: `kube-system/default · generation 7` and a nested tree with `metadata`, `spec` and `status` branches, indented with a hairline guide.

- [ ] **Step 5: Commit** (message: `introspect/ui: the container table and the configuration viewer.`, body noting that the table is the contrast-relief channel and the bug-report artefact, that exclusivity is derived from sharer counts with no policy input, and that the configuration panel interprets no key)

---

### Task 8: Live updates

**Files:**
- Modify: `pkg/resmgr/introspect/ui/assets/js/app.js`, `ui.css`
- Modify: `docs/resource-policy/policy/balloons.md`, `topology-aware.md`

**Interfaces:**
- Consumes: `/introspect/events` from the predecessor plan; `state.changedCPUs`.
- Produces: nothing new; this closes the loop.

**Gate: full `opus` review.** It is the only task with a long-lived connection and a reconnect path, and the class of bug it can carry — a leaked `EventSource`, an unbounded refetch loop — does not show in a screenshot.

- [ ] **Step 1: Subscribe**

Add to `app.js`:

```js
/** subscribe follows the change stream. Each event carries only a
 *  generation number, so a bump means "refetch", not "here is the data" —
 *  which is why an idle node costs the server nothing.
 *
 *  EventSource reconnects by itself, which matters because the plugin is a
 *  DaemonSet and its pod can restart under us. There is deliberately no
 *  manual retry loop: adding one on top would double up.
 */
function subscribe() {
  // A ?snapshot= override means we are looking at a static file, so there
  // is nothing to subscribe to.
  if (new URLSearchParams(location.search).get('snapshot')) return;

  const events = new EventSource(EVENTS_PATH);

  events.addEventListener('message', async (e) => {
    let generation = 0;
    try {
      generation = Number(JSON.parse(e.data).generation) || 0;
    } catch {
      return; // A malformed record is not worth a refetch.
    }
    if (generation <= state.lastGeneration) return;

    try {
      await load();
    } catch (err) {
      // A failed refetch is not fatal: the stream stays open and the next
      // bump tries again.
      document.getElementById('live').textContent = 'refetch failed';
    }
  });

  events.addEventListener('error', () => {
    // EventSource is already retrying; just say so.
    const live = document.getElementById('live');
    live.textContent = `gen ${state.snapshot ? state.snapshot.generation : '–'} · reconnecting`;
  });
}
```

and call it after the first successful load:

```js
load().then(subscribe).catch(fail);
```

- [ ] **Step 2: Mark what changed**

In `load()`, before assigning the new snapshot, diff the commitment map:

```js
  const previous = state.snapshot;
  const next = normalize(await response.json());

  // Which CPUs actually moved. Ring and opacity carry change, so no hue is
  // spent on it and the commitment ramp keeps its meaning.
  state.changedCPUs = new Set();
  if (previous) {
    for (const cpu of next.hardware.cpus) {
      const before = Number(previous.derived.cpuCommitment[cpu.id]) || 0;
      const after = Number(next.derived.cpuCommitment[cpu.id]) || 0;
      if (before !== after) state.changedCPUs.add(cpu.id);
    }
  }

  state.snapshot = next;
  state.lastGeneration = next.generation;
  render();
```

In `wireCell`, add the pulse class when the cell is one that changed:

```js
  if (state.changedCPUs.has(entry.cpu.id)) {
    cell.classList.add('changed');
  }
```

and clear the set after a render settles, at the end of `render()`:

```js
  if (state.changedCPUs.size) {
    // One animation per change, then forget it: the set is a per-update
    // signal, not part of the node's state.
    setTimeout(() => { state.changedCPUs.clear(); }, 1200);
  }
```

- [ ] **Step 3: CSS**

```css
@keyframes cell-changed {
  0%   { stroke: var(--ink); stroke-width: 2; }
  100% { stroke: transparent; stroke-width: 1; }
}
.cell.changed { animation: cell-changed 1s ease-out 1; }

@media (prefers-reduced-motion: reduce) {
  .cell.changed { animation: none; stroke: var(--ink); }
}
```

The reduced-motion branch is not decoration: a pulsing grid is exactly what that preference exists for, and a static ring conveys the same thing.

- [ ] **Step 4: Verify the stream against a real plugin, or say you could not**

The honest check needs a running plugin with introspection enabled. If one is not available here, do **not** fake it — report that the SSE path is unverified end to end and that only the static rendering was checked. That is what happened to the e2e case in the predecessor plan and saying so plainly was the right call.

What you can verify without a cluster: that no `EventSource` is created when `?snapshot=` is present (add a temporary `console.log`, screenshot the console via `--enable-logging` or simply reason it through and state it), and that `load()` is idempotent when called twice.

- [ ] **Step 5: Document the UI**

Both `docs/resource-policy/policy/balloons.md` and `topology-aware.md` already describe `introspection.enabled` and mention the two endpoints. Add the UI to each, in that file's existing style: that `ui` defaults to true when introspection is enabled, that it serves a browser view at `/ui/` showing how the policy placed containers on the node, and that `/` redirects there. Keep it to two or three sentences; no screenshots in the docs yet.

Run: `grep -n 'ui\b\|/ui/' docs/resource-policy/policy/*.md`

Expected: a hit in each of the two files.

- [ ] **Step 6: Final verification and commit**

```
go build ./...
go test ./pkg/... ./cmd/... -count=1
go test ./pkg/resmgr/... ./pkg/http/... ./pkg/introspect/... -count=1 -race
gofmt -l pkg/resmgr/introspect/
```

Plus a final screenshot of the map and the table in both themes, and the self-test page, all green.

Commit message: `introspect/ui: follow the change stream.` Body: each event carries only a generation so a bump means refetch rather than here-is-the-data, which is what keeps an idle node free; EventSource reconnects by itself because the plugin is a DaemonSet whose pod restarts; change is carried by ring and opacity so no hue is spent on it; reduced-motion is honoured.

---

## Done criteria

- `go build ./...` clean, `go test ./pkg/... ./cmd/...` zero failures, race-clean on `pkg/resmgr/...`, `pkg/http/...`, `pkg/introspect/...`.
- With `instrumentation.introspection.enabled: false`, `/ui/` and `/` both 404.
- With introspection enabled and `ui: false`, `/introspect` serves and `/ui/` 404s.
- With both enabled, `/ui/` serves the map, `/` redirects to it.
- `selftest.html` reports every check passing, and has been shown able to fail.
- The machine map renders the sample snapshot with: two packages plus a clearly separate memory-only group of four nodes, hyperthread siblings stacked, a monotone ramp, exactly one over-committed CPU carrying a ⚠, and memory bars on every node including the CPU-less ones.
- Both themes legible; the table lists every container with `not pinned` where that is true.
- Both operator docs mention the UI.

## Follow-ups this plan deliberately leaves

- **Spec increments 4–5, the policy layer**: zone and balloon outlines over the grid, assignment columns in the table, and the notices list. The spec already says these use structure and text rather than colour, so they cannot collide with the commitment ramp.
- **Two-dimensional keyboard navigation** of the grid, which needs a coordinate map rather than document order.
- **An e2e case for `/ui/`**, once the predecessor's `test42-introspection` has been run once on real hardware. Asserting a 200 and a `<title>` is cheap; asserting anything visual is not.
- **Screenshots in the operator docs**, which want a stable fixture and a rendering step in CI rather than a hand-pasted PNG.

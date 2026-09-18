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
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// publishRun puts a run under root with a report of its own, complete enough
// that nothing would report on it again unasked.
func publishRun(t *testing.T, root, name string) string {
	t.Helper()

	dir := filepath.Join(root, name)
	if err := os.Rename(newRun(t), dir); err != nil {
		t.Fatal(err)
	}

	// A report with a test in it. The one newRun leaves has none, and a run
	// with nothing in its report is reported on again by anybody.
	reported := `{"name":"` + name + `","verdict":"PASS",` +
		`"counts":{"PASS":1,"total":1},"tests":[{"name":"test01",` +
		`"path":"vm/` + suiteDir + `/balloons/test01","verdict":"PASS",` +
		`"links":{"artifacts":"once-upon-a-time.tar.xz"}}]}`
	if err := os.WriteFile(filepath.Join(dir, resultsJSON), []byte(reported), 0o644); err != nil {
		t.Fatal(err)
	}

	return dir
}

func mustRead(t *testing.T, path string) string {
	t.Helper()

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}

	return string(data)
}

// TestIndexLeavesReportsAlone checks that indexing a root does not report on a
// run which already has a report of its own, which is what it has always done
// and what the nightly relies on.
func TestIndexLeavesReportsAlone(t *testing.T) {
	root := t.TempDir()
	run := publishRun(t, root, "test-2026-09-15-2113")
	was := mustRead(t, filepath.Join(run, resultsJSON))

	if err := reportIndex(root, false); err != nil {
		t.Fatal(err)
	}

	if now := mustRead(t, filepath.Join(run, resultsJSON)); now != was {
		t.Errorf("the report of a run was built again without being asked for")
	}
}

// TestIndexRefreshesReports checks that --refresh builds the report of every
// unpacked run again, which is how a run published by an older runner comes to
// be rendered the way one published today is.
func TestIndexRefreshesReports(t *testing.T) {
	root := t.TempDir()
	loose := publishRun(t, root, "test-2026-09-15-2113")
	packed := publishRun(t, root, "test-2026-09-16-0910")

	wasPacked := mustRead(t, filepath.Join(packed, resultsJSON))
	if err := packRun(packed); err != nil {
		t.Fatal(err)
	}

	if err := reportIndex(root, true); err != nil {
		t.Fatal(err)
	}

	// The stale report named an artifact which was never there; a report built
	// again names what the run actually collected.
	if now := mustRead(t, filepath.Join(loose, resultsJSON)); strings.Contains(now, "once-upon-a-time") {
		t.Errorf("the report of an unpacked run was not built again")
	}

	// A packed run keeps the report it was packed with: its tests are inside
	// the archive, so building it again could only throw them away.
	if now := mustRead(t, filepath.Join(packed, resultsJSON)); now != wasPacked {
		t.Errorf("the report a packed run was packed with was replaced")
	}
}

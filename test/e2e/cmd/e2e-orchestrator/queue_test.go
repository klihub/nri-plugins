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
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// testConfig is a Config on a temporary root, with the directories a queue needs.
func testConfig(t *testing.T) *Config {
	t.Helper()

	cfg := &Config{Root: t.TempDir(), MaxJobs: 1, DoneKeep: 10, HookTimeout: 30 * time.Second}
	cfg.Hooks = filepath.Join(cfg.Root, "hooks.d")
	for _, dir := range []string{cfg.queueDir(), cfg.jobsDir(), cfg.doneDir(), cfg.stateDir()} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
	}

	return cfg
}

// mustRequest parses a request a test means to be valid.
func mustRequest(t *testing.T, body string) *Request {
	t.Helper()

	req, err := parseRequest([]byte(body))
	if err != nil {
		t.Fatal(err)
	}

	return req
}

// mustReadFile reads a file and errors if it cannot.
func mustReadFile(t *testing.T, path string) []byte {
	t.Helper()

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}

	return data
}

// TestQueuedSkipsPartialAndSubmitLeavesNoLitter checks the two halves a test can
// see: a request still being written is invisible to a reader, and a finished
// submit leaves one request under a plain name in the queue directory with no
// temporary file beside it.
//
// It deliberately does NOT pin that submit writes atomically. See the comment at
// the rename in submit: that property is not observable after the fact, since
// every assertion here also holds for a plain write straight to the final name.
func TestQueuedSkipsPartialAndSubmitLeavesNoLitter(t *testing.T) {
	cfg := testConfig(t)

	// The reader half: a request still being written is invisible.
	if err := os.WriteFile(filepath.Join(cfg.queueDir(), ".half-written"), []byte("branch=m"), 0o644); err != nil {
		t.Fatal(err)
	}
	paths, err := queued(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if len(paths) != 0 {
		t.Errorf("a dotted name was queued: %v", paths)
	}

	// The writer half: what submit leaves behind is a finished request under a
	// plain name, in the queue directory it reported, with no temporary file
	// beside it.
	//
	// That the rename cannot cross a filesystem is true by construction inside
	// renameIn, which resolves both names against one directory descriptor. No
	// assertion here establishes it.
	path, err := submit(cfg, mustRequest(t, "branch=main\n"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.HasPrefix(filepath.Base(path), ".") {
		t.Errorf("submit left the request under a dotted name: %q", path)
	}
	if filepath.Dir(path) != cfg.queueDir() {
		t.Errorf("submit wrote outside the queue directory: %q", path)
	}
	entries, err := os.ReadDir(cfg.queueDir())
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		if entry.Name() != ".half-written" && strings.HasPrefix(entry.Name(), ".") {
			t.Errorf("a temporary file was left behind: %q", entry.Name())
		}
	}

	// The mode matters as much as the content: a request the orchestrator cannot
	// read is worse than one it never received. This regressed silently once.
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o644 {
		t.Errorf("mode: got %o, want 644", info.Mode().Perm())
	}
}

// TestSubmitThenQueued checks a request through the queue, and that queued gives
// the oldest first so that waiting work is not overtaken by new work.
func TestSubmitThenQueued(t *testing.T) {
	cfg := testConfig(t)

	first, err := submit(cfg, mustRequest(t, "branch=one\n"))
	if err != nil {
		t.Fatal(err)
	}
	second, err := submit(cfg, mustRequest(t, "branch=two\n"))
	if err != nil {
		t.Fatal(err)
	}
	if first == "" || second == "" || first == second {
		t.Fatalf("submit gave %q and %q", first, second)
	}

	paths, err := queued(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if len(paths) != 2 {
		t.Fatalf("got %d queued, want 2", len(paths))
	}

	req, err := parseRequest(mustReadFile(t, paths[0]))
	if err != nil {
		t.Fatal(err)
	}
	if value, _ := req.Get("branch"); value != "one" {
		t.Errorf("oldest first: got %q, want one", value)
	}
}

// TestSubmitDropsADuplicateKey checks that a key already queued is not queued
// again. This is what lets a tick hook submit on every tick without remembering
// what it submitted, and it is what replaces the runner's single global record of
// the last thing tried.
func TestSubmitDropsADuplicateKey(t *testing.T) {
	cfg := testConfig(t)

	if _, err := submit(cfg, mustRequest(t, "key=origin@main\nbranch=main\n")); err != nil {
		t.Fatal(err)
	}
	path, err := submit(cfg, mustRequest(t, "key=origin@main\nbranch=main\n"))
	if err != nil {
		t.Fatal(err)
	}
	if path != "" {
		t.Errorf("a duplicate key was queued at %q", path)
	}

	paths, _ := queued(cfg)
	if len(paths) != 1 {
		t.Errorf("got %d queued, want 1", len(paths))
	}
}

// TestSubmitKeepsDifferentKeys checks that dedup is by key and not by content, so
// two different pieces of work queue even when they look alike.
func TestSubmitKeepsDifferentKeys(t *testing.T) {
	cfg := testConfig(t)

	for _, key := range []string{"origin@main", "origin@devel"} {
		if _, err := submit(cfg, mustRequest(t, "key="+key+"\nbranch=x\n")); err != nil {
			t.Fatal(err)
		}
	}

	paths, _ := queued(cfg)
	if len(paths) != 2 {
		t.Errorf("got %d queued, want 2", len(paths))
	}
}

// TestSubmitWithoutAKeyNeverDeduplicates checks that a request with no key is
// always queued. Without a key there is nothing to compare, and guessing that two
// such requests are the same work would drop somebody's job.
func TestSubmitWithoutAKeyNeverDeduplicates(t *testing.T) {
	cfg := testConfig(t)

	for range 3 {
		if _, err := submit(cfg, mustRequest(t, "branch=main\n")); err != nil {
			t.Fatal(err)
		}
	}

	paths, _ := queued(cfg)
	if len(paths) != 3 {
		t.Errorf("got %d queued, want 3", len(paths))
	}
}

// captureStdout runs f with os.Stdout replaced by a pipe and returns what it wrote.
func captureStdout(t *testing.T, f func()) string {
	t.Helper()

	read, write, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	saved := os.Stdout
	os.Stdout = write
	defer func() { os.Stdout = saved }()

	f()

	if err := write.Close(); err != nil {
		t.Fatal(err)
	}
	out, err := io.ReadAll(read)
	if err != nil {
		t.Fatal(err)
	}

	return string(out)
}

// TestRequestCommand checks that the CLI command rejects empty arguments,
// writes nothing to stdout on a duplicate drop, and reports the queue id
// on success so that a producer can capture it.
func TestRequestCommand(t *testing.T) {
	cfg := testConfig(t)

	// Empty arguments must error.
	if err := requestCommand(cfg, []string{}); err == nil {
		t.Errorf("empty args should error")
	}

	// A successful submit must write the id to stdout.
	out := captureStdout(t, func() {
		if err := requestCommand(cfg, []string{"key=first", "branch=test"}); err != nil {
			t.Fatal(err)
		}
	})

	paths, _ := queued(cfg)
	if len(paths) != 1 {
		t.Fatalf("wanted 1 queued, got %d", len(paths))
	}
	if want := filepath.Base(paths[0]) + "\n"; out != want {
		t.Errorf("stdout: got %q, want %q", out, want)
	}

	// A duplicate drop must write nothing to stdout.
	out = captureStdout(t, func() {
		if err := requestCommand(cfg, []string{"key=first", "branch=test"}); err != nil {
			t.Fatal(err)
		}
	})
	if out != "" {
		t.Errorf("duplicate drop wrote to stdout: %q", out)
	}
}

// TestActiveKeysIgnoresUnparseableFiles checks that a queue file which cannot
// be parsed as a request does not crash activeKeys and does not count toward
// dedup. A crash after the rename can leave a visible zero-length file that
// parses to no key, and activeKeys must not treat that as "no keys active".
func TestActiveKeysIgnoresUnparseableFiles(t *testing.T) {
	cfg := testConfig(t)

	// Plant an unparseable file in the queue.
	if err := os.WriteFile(filepath.Join(cfg.queueDir(), "9999999999999999999-9999"), []byte("garbage"), 0o644); err != nil {
		t.Fatal(err)
	}

	// activeKeys must not crash on it and must not consider it a key.
	keys, err := activeKeys(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if len(keys) != 0 {
		t.Errorf("unparseable file counted as a key: %v", keys)
	}

	// A keyed submit must still work; the unparseable file does not block it.
	path, err := submit(cfg, mustRequest(t, "key=test\nbranch=main\n"))
	if err != nil {
		t.Fatal(err)
	}
	if path == "" {
		t.Errorf("submit dropped a request that should have been queued")
	}
}

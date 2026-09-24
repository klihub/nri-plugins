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
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// submit puts a request in the queue, or drops it as work already asked for.
//
// Returns the path written, or empty when the request carried a key which is
// already queued or running. Dedup is best-effort: two producers can both pass
// the check before either renames, but a key is enough to prevent most repeats.
// Dropping is not an error: a producer is meant to be able to ask on every tick
// without keeping track of what it asked for.
func submit(cfg *Config, req *Request) (string, error) {
	if key := req.Key(); key != "" {
		active, err := activeKeys(cfg)
		if err != nil {
			return "", err
		}
		if active[key] {
			return "", nil
		}
	}

	if err := os.MkdirAll(cfg.queueDir(), 0o755); err != nil {
		return "", err
	}

	// Named for when it was asked for, so the queue reads in order, with the pid
	// to keep two producers landing in the same nanosecond apart.
	name := fmt.Sprintf("%d-%d", time.Now().UnixNano(), os.Getpid())
	tempFile, err := os.CreateTemp(cfg.queueDir(), "."+name+"-")
	if err != nil {
		return "", err
	}
	if _, err := tempFile.Write(req.Bytes()); err != nil {
		_ = tempFile.Close()
		_ = os.Remove(tempFile.Name())
		return "", err
	}
	if err := tempFile.Chmod(0o644); err != nil {
		_ = tempFile.Close()
		_ = os.Remove(tempFile.Name())
		return "", err
	}
	if err := tempFile.Close(); err != nil {
		_ = os.Remove(tempFile.Name())
		return "", err
	}

	// Written to a temporary name in this same directory and renamed into place.
	// queued skips dotted names and a rename within one directory is atomic, so no
	// reader ever sees a request which is still being written. Same directory also
	// means the rename cannot cross a filesystem, where it would stop being atomic
	// and become a copy.
	//
	// Do not "simplify" this to a single write at the final path. No test can catch
	// that regression: every observable property afterwards is identical, which is
	// why the test named for this only claims the halves it can actually see.
	//
	// The rename now also refuses to replace: plain rename(2) overwrites silently,
	// so two producers that agreed on a name would lose a request. RENAME_NOREPLACE
	// fails with EEXIST instead.
	path := filepath.Join(cfg.queueDir(), name)
	if err := renameIn(cfg.queueDir(), filepath.Base(tempFile.Name()), name, false); err != nil {
		_ = os.Remove(tempFile.Name())
		return "", err
	}

	return path, nil
}

// queued is every request waiting, oldest first.
//
// Oldest first so that waiting work is not overtaken. Names are timestamps, so
// sorting them sorts by when they were asked for. An NTP step backwards could
// let a new request sort ahead of waiting ones, but that is acceptable.
func queued(cfg *Config) ([]string, error) {
	entries, err := os.ReadDir(cfg.queueDir())
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}

	var names []string
	for _, entry := range entries {
		if entry.IsDir() || ignoredName(entry.Name()) {
			continue
		}
		names = append(names, entry.Name())
	}
	sort.Strings(names)

	paths := make([]string, 0, len(names))
	for _, name := range names {
		paths = append(paths, filepath.Join(cfg.queueDir(), name))
	}

	return paths, nil
}

// activeKeys are the keys of everything queued or running.
//
// Running counts as active: the point of a key is that the same work is not
// started twice, and work in progress is the case that matters most.
func activeKeys(cfg *Config) (map[string]bool, error) {
	keys := map[string]bool{}

	paths, err := queued(cfg)
	if err != nil {
		return nil, err
	}
	for _, path := range paths {
		data, err := os.ReadFile(path)
		if err != nil {
			// Claimed between the listing and here, which is normal. Anything
			// else is an anomaly and must not be read as "no key": a key
			// silently dropped lets the same work be queued twice.
			if os.IsNotExist(err) {
				continue
			}
			return nil, err
		}

		req, err := parseRequest(data)
		if err != nil {
			fmt.Fprintf(os.Stderr, "e2e-orchestrator: %s: %v\n", filepath.Base(path), err)
			continue
		}
		if key := req.Key(); key != "" {
			keys[key] = true
		}
	}

	jobs, err := liveJobs(cfg)
	if err != nil {
		return nil, err
	}
	for _, job := range jobs {
		if key := job.Request.Key(); key != "" {
			keys[key] = true
		}
	}

	return keys, nil
}

// requestCommand is the request subcommand: submit what the arguments say.
func requestCommand(cfg *Config, args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("a request with no fields asks for nothing")
	}

	req, err := parseRequest([]byte(strings.Join(args, "\n") + "\n"))
	if err != nil {
		return err
	}

	path, err := submit(cfg, req)
	if err != nil {
		return err
	}
	if path == "" {
		fmt.Fprintf(os.Stderr, "e2e-orchestrator: %s is already asked for, nothing to do\n", req.Key())
		return nil
	}

	fmt.Printf("%s\n", filepath.Base(path))

	return nil
}

// Both of these are placeholders so this task compiles. Task 4 deletes them
// together and defines the real Job in job.go. Delete BOTH or neither: leaving
// one behind is a duplicate declaration or an undefined symbol.
func liveJobs(cfg *Config) ([]*Job, error) { return nil, nil }

type Job struct{ Request *Request }

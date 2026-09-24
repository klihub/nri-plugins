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
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"testing"
)

// TestRenameInRefusesToReplace checks that a rename which would clobber fails
// instead. The queue generates unique names, so a collision means two producers
// agreed on one, and replacing would lose a request silently.
func TestRenameInRefusesToReplace(t *testing.T) {
	dir := t.TempDir()
	for _, name := range []string{"a", "b"} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(name), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	err := renameIn(dir, "a", "b", false)
	if err == nil {
		t.Fatal("renaming onto an existing name was allowed")
	}
	if !errors.Is(err, fs.ErrExist) {
		t.Errorf("want an exists error, got %v", err)
	}
	// os.IsExist does not unwrap, which is the trap the doc comment warns about.
	if os.IsExist(err) {
		t.Error("os.IsExist now unwraps; the doc comment needs correcting")
	}
	if got := string(mustReadFile(t, filepath.Join(dir, "b"))); got != "b" {
		t.Errorf("the target was modified: %q", got)
	}

	if err := renameIn(dir, "a", "c", false); err != nil {
		t.Errorf("renaming onto a free name failed: %v", err)
	}
	if err := renameIn(dir, "c", "b", true); err != nil {
		t.Errorf("overwrite was refused: %v", err)
	}
	if got := string(mustReadFile(t, filepath.Join(dir, "b"))); got != "a" {
		t.Errorf("overwrite did not replace: %q", got)
	}
}

// TestLinkInBehavesLikeTheRename checks the fallback on its own terms.
//
// It is tested directly rather than by forcing renameIn down this path, because
// making a local filesystem refuse RENAME_NOREPLACE is not something a test can
// arrange. What matters is that a caller cannot tell the two apart: the same
// refusal, the same error predicate, the same end state.
func TestLinkInBehavesLikeTheRename(t *testing.T) {
	dir := t.TempDir()
	for _, name := range []string{"a", "b"} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(name), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	err := linkIn(dir, "a", "b")
	if !errors.Is(err, fs.ErrExist) {
		t.Errorf("linking onto an existing name: want an exists error, got %v", err)
	}
	if got := string(mustReadFile(t, filepath.Join(dir, "b"))); got != "b" {
		t.Errorf("the target was modified: %q", got)
	}

	if err := linkIn(dir, "a", "c"); err != nil {
		t.Fatalf("linking onto a free name failed: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "a")); !os.IsNotExist(err) {
		t.Error("the source survived the move")
	}
	if got := string(mustReadFile(t, filepath.Join(dir, "c"))); got != "a" {
		t.Errorf("moved content: got %q, want a", got)
	}
	// The mode has to survive too: a request the orchestrator cannot read is
	// worse than one it never received.
	info, err := os.Stat(filepath.Join(dir, "c"))
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o644 {
		t.Errorf("mode: got %o, want 644", info.Mode().Perm())
	}
}

// TestRenameAndLinkAgree runs one contract over both the rename and its fallback.
// The fallback is only safe because a caller cannot tell them apart, and that is
// not established by testing each with a different set of assertions.
func TestRenameAndLinkAgree(t *testing.T) {
	for name, move := range map[string]func(dir, src, dst string) error{
		"renameIn": func(dir, src, dst string) error { return renameIn(dir, src, dst, false) },
		"linkIn":   linkIn,
	} {
		t.Run(name, func(t *testing.T) {
			dir := t.TempDir()
			for _, n := range []string{"a", "b"} {
				if err := os.WriteFile(filepath.Join(dir, n), []byte(n), 0o644); err != nil {
					t.Fatal(err)
				}
			}

			err := move(dir, "a", "b")
			if !errors.Is(err, fs.ErrExist) {
				t.Errorf("onto an existing name: want an exists error, got %v", err)
			}
			if os.IsExist(err) {
				t.Error("os.IsExist now unwraps; the doc comment needs correcting")
			}
			if got := string(mustReadFile(t, filepath.Join(dir, "b"))); got != "b" {
				t.Errorf("the target was modified: %q", got)
			}

			if err := move(dir, "a", "c"); err != nil {
				t.Fatalf("onto a free name: %v", err)
			}
			if _, err := os.Stat(filepath.Join(dir, "a")); !os.IsNotExist(err) {
				t.Error("the source survived the move")
			}
			if got := string(mustReadFile(t, filepath.Join(dir, "c"))); got != "a" {
				t.Errorf("moved content: got %q, want a", got)
			}
			info, err := os.Stat(filepath.Join(dir, "c"))
			if err != nil {
				t.Fatal(err)
			}
			if info.Mode().Perm() != 0o644 {
				t.Errorf("mode: got %o, want 644", info.Mode().Perm())
			}
		})
	}
}

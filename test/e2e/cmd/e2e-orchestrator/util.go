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
	"fmt"
	"os"
	"path/filepath"

	"golang.org/x/sys/unix"
)

// renameIn renames within one directory, refusing to replace unless told to.
//
// Plain rename(2) overwrites its target silently, so two producers agreeing on a
// name would lose a request without anybody hearing about it, which is the worst
// way to lose one. RENAME_NOREPLACE fails with EEXIST instead.
//
// Both names are resolved against the same directory descriptor, which is what
// makes "this rename cannot cross a filesystem" true by construction rather than
// by inspection.
//
// A filesystem which does not implement the flag falls back to linkIn, which gets
// the same refusal from link(2) on any POSIX filesystem.
//
// Note for callers: the returned error is wrapped, and os.IsExist does not unwrap.
// Use errors.Is(err, fs.ErrExist) to tell a collision from any other failure. Both
// paths answer that identically, so a caller cannot tell which one ran.
func renameIn(dir, src, dst string, overwrite bool) error {
	var flags uint

	dirf, err := os.Open(dir)
	if err != nil {
		return fmt.Errorf("rename %s to %s in %s: %w", src, dst, dir, err)
	}
	defer func() {
		_ = dirf.Close()
	}()

	if !overwrite {
		flags = unix.RENAME_NOREPLACE
	}

	dirFd := int(dirf.Fd())
	err = unix.Renameat2(dirFd, src, dirFd, dst, flags)

	// Only the no-replace case can be refused for want of flag support, so only
	// it falls back. A kernel without renameat2 at all would fail either way, which
	// no caller in this tree can reach.
	if !overwrite && (errors.Is(err, unix.EINVAL) ||
		errors.Is(err, unix.ENOSYS) || errors.Is(err, unix.EOPNOTSUPP)) {
		return linkIn(dir, src, dst)
	}
	if err != nil {
		return fmt.Errorf("rename %s to %s in %s: %w", src, dst, dir, err)
	}

	return nil
}

// linkIn moves within one directory by linking and then removing the source.
//
// The stand-in for RENAME_NOREPLACE where the kernel or the filesystem has not got
// it. link(2) refuses an existing target with EEXIST atomically everywhere POSIX,
// which is the whole guarantee wanted, and a maildir has always worked this way.
//
// What it costs against a rename is a wider crash window: a process killed between
// the link and the remove leaves the temporary behind. That is a leak and not a
// lost request, and a leaked temporary is already a condition of writing under a
// temporary name at all.
//
// Note: link(2) refuses directories with EPERM, so this is not equivalent to
// renameIn for directories. Future callers moving directories would work on most
// filesystems and fail on a fallback one.
func linkIn(dir, src, dst string) error {
	from, to := filepath.Join(dir, src), filepath.Join(dir, dst)

	if err := os.Link(from, to); err != nil {
		return fmt.Errorf("link %s to %s in %s: %w", src, dst, dir, err)
	}
	if err := os.Remove(from); err != nil {
		return fmt.Errorf("remove %s in %s after linking: %w", src, dir, err)
	}

	return nil
}

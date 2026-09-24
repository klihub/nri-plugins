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

import "strings"

// ignoredName tells whether a name in the queue or in a hook directory is one we
// never treat as ours.
//
// A request name is otherwise free-form: requiring a shape would break the point
// of a directory, that anything able to write a file and rename it can create
// work. So this is a list of what to ignore rather than a pattern to match.
//
// The hook case is why the editor artifacts are here rather than only the dot.
// Emacs keeps the mode of a backup, so editing an executable hook leaves an
// executable copy of it next door, and a rule which only skipped names containing
// a dot would run both the hook and a superseded version of it. Hooks are
// conventionally named without a dot, so that rule would be no protection at all.
func ignoredName(name string) bool {
	switch {
	case name == "":
		return true
	// A request still being written, a vi swap file (.req.swp), or the emacs lock
	// (.#req), which is a dangling symlink rather than a file.
	case strings.HasPrefix(name, "."):
		return true
	// An emacs or vi backup, including an emacs numbered backup (req.~3~).
	case strings.HasSuffix(name, "~"):
		return true
	// An emacs auto-save, which is where unsaved changes live.
	case strings.HasPrefix(name, "#"):
		return true
	}

	return false
}

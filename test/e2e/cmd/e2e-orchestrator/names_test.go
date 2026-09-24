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

import "testing"

// TestIgnoredName checks which names are never ours. The editor cases are not
// hypothetical: a backup of a hook keeps the hook's mode under emacs, so a name
// ending in ~ that got run would be superseded logic running as policy.
func TestIgnoredName(t *testing.T) {
	for _, name := range []string{
		"",
		".half-written",   // a request mid-submit
		".req.swp",        // vi swap
		".#req",           // emacs lock, a dangling symlink
		"req~",            // emacs or vi backup
		"10-poll-branch~", // the dangerous one: executable, no dot
		"req.~3~",         // emacs numbered backup
		"#req#",           // emacs auto-save
	} {
		if !ignoredName(name) {
			t.Errorf("%q should be ignored", name)
		}
	}

	for _, name := range []string{
		"1790247584181679245-2345687", // what submit generates
		"poll-1758700000-4242",        // what a shell hook writes
		"new",
		"10-poll-branch", // the hook itself
	} {
		if ignoredName(name) {
			t.Errorf("%q should not be ignored", name)
		}
	}
}

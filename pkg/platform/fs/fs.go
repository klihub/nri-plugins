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

package fs

import (
	"io/fs"

	logger "github.com/containers/nri-plugins/pkg/log"
	"github.com/containers/nri-plugins/pkg/platform/parser"
)

// FS provides filesystem-like access to a real filesystem or other data.
//
// The primary use for FS is to provide transparent access to the sysfs
// and procfs pseudo-filesystems when they are not mounted under their
// standard locations. This is implemented by DirFS which is much like
// os.DirFS but allows using absolute names. It also implements a few
// additional interfaces.
type FS interface {
	fs.FS
	fs.GlobFS
	fs.ReadFileFS
	fs.ReadDirFS
	fs.StatFS
	fs.SubFS

	// WriteFile writes data to a file.
	WriteFile(string, []byte, fs.FileMode) error

	// Root returns the root directory for this FS.
	Root() string

	// WalkDir walks the file tree rooted at root, calling fn for each
	// file or directory in the tree, including root.
	WalkDir(dir string, fn fs.WalkDirFunc) error

	// EvalSymlinks evaluates any symbolic links in the source and
	// returns the resulting path name.
	EvalSymlinks(source string) (string, error)

	// Parse the given file with the given parser.
	Parse(string, func([]string) error, ...parser.SimpleParserOption) error
}

var (
	// Our logger instance.
	log = logger.Get("fs")
)

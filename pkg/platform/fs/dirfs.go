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
	"fmt"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"strings"
	"syscall"

	"github.com/containers/nri-plugins/pkg/platform/parser"
)

var (
	// Default root directory.
	sysRoot = ""
)

// SetSysRoot sets the global default sysroot. DirFS without any
// options uses this as its root.
func SetSysRoot(root string) {
	sysRoot = root
}

// DirFSOption is an option which can be applied to an DirFS.
type DirFSOption func(*dirFS) error

// WithSysRoot sets the directory DirFS should prefix all
// pathes with.
func WithRoot(root string) DirFSOption {
	return func(d *dirFS) error {
		if root == "" {
			root = "/"
		}
		d.root = root
		return nil
	}
}

// dirFS implements an FS which prefixes all pathes with a common
// sysroot.
type dirFS struct {
	root string
	fs   fs.FS
}

// DirFS returns an FS which resolves filesystem paths relative
// to a DirFS-specific root directory. This directory is either
// supplied with the WithSysRoot() option or, if such an option
// is not given, taken from the effective default sysroot which
// can be set using SetSysRoot(). DirFS should never be used as
// a replacement for a proper chroot jail.
func DirFS(options ...DirFSOption) (FS, error) {
	d := &dirFS{
		root: sysRoot,
	}

	for _, opt := range options {
		if err := opt(d); err != nil {
			return nil, fmt.Errorf("failed to apply DirFS option: %w", err)
		}
	}

	d.fs = os.DirFS(d.root)

	return d, nil
}

// Open opens the given file under DirFS.
func (d *dirFS) Open(name string) (fs.File, error) {
	rela, err := ensureValid(name)
	if err != nil {
		return nil, fmt.Errorf("failed to open %s: %w", name, err)
	}
	return d.fs.Open(rela)
}

// Glob returns the names of all files matching pattern.
func (d *dirFS) Glob(pattern string) ([]string, error) {
	rela, err := ensureValid(pattern)
	if err != nil {
		return nil, err
	}
	return fs.Glob(d.fs, rela)
}

// ReadFile reads the named file and returns its contents.
// A successful call returns a nil error, not io.EOF.
func (d *dirFS) ReadFile(name string) ([]byte, error) {
	rela, err := ensureValid(name)
	if err != nil {
		return nil, err
	}
	return fs.ReadFile(d.fs, rela)
}

// WriteFile writes the given data to the given file.
// A successful call returns a nil error, not io.EOF.
func (d *dirFS) WriteFile(name string, data []byte, perm fs.FileMode) error {
	rela, err := ensureValid(name)
	if err != nil {
		return err
	}

	f, err := os.OpenFile(path.Join(d.root, rela), os.O_CREATE|os.O_WRONLY, perm)
	if err != nil {
		return err
	}
	defer f.Close()

	_, err = f.Write(data)
	return err
}

// ReadDir reads the named directory and returns a list of directory
// entries sorted by filename.
func (d *dirFS) ReadDir(dir string) ([]fs.DirEntry, error) {
	rela, err := ensureValid(dir)
	if err != nil {
		return nil, err
	}
	return fs.ReadDir(d.fs, rela)
}

// Stat returns a FileInfo describing the named file.
func (d *dirFS) Stat(name string) (fs.FileInfo, error) {
	rela, err := ensureValid(name)
	if err != nil {
		return nil, err
	}
	return fs.Stat(d.fs, rela)
}

// Sub returns an FS corresponding to the subtree rooted at dir.
func (d *dirFS) Sub(dir string) (fs.FS, error) {
	rela, err := ensureValid(dir)
	if err != nil {
		return nil, err
	}
	return os.DirFS(path.Join(d.root, rela)), nil
}

// Root returns the effective root for this DirFS.
func (d *dirFS) Root() string {
	return d.root
}

// WalkDir walks the file tree rooted at root, calling fn for each file
// or directory in the tree, including root.
func (d *dirFS) WalkDir(name string, fn fs.WalkDirFunc) error {
	rela, err := ensureValid(name)
	if err != nil {
		return err
	}
	return fs.WalkDir(d.fs, rela, fn)
}

// EvalSymlinks returns the path name after the evaluation of any symbolic
// links. The implementation is not necessarily as generic as the the one
// provided by path/filepath, but it should be adequate for dealing with
// sysfs and procfs.
func (d *dirFS) EvalSymlinks(source string) (string, error) {
	const (
		maxLinks = 256
	)

	orig := source

	for {
		dirs := strings.Split(filepath.Clean(source), "/")
		curr := d.root
		cnt := 0

		for idx, part := range dirs {
			curr = filepath.Join(curr, part)

			fi, err := os.Lstat(curr)
			if err != nil {
				return "", fmt.Errorf("failed to eval symlinks for %s: %w", orig, err)
			}

			// not a symlink, appending it was enough
			if (fi.Mode() & fs.ModeSymlink) == 0 {
				if idx < len(dirs)-1 {
					continue
				}

				cnt++
				if cnt > maxLinks {
					return "", fmt.Errorf("failed to eval symlinks for %s: %w", orig, syscall.ELOOP)
				}

				source, err := filepath.Rel(d.root, curr)
				if err != nil {
					return "", fmt.Errorf("failed to eval symlinks for %s: %w", orig, err)
				}

				return "/" + source, nil
			}

			link, err := os.Readlink(curr)
			if err != nil {
				return "", fmt.Errorf("failed to eval symlinks for %s: %w", orig, err)
			}

			// absolute symlink, re-evaluate everything with new source
			if filepath.IsAbs(link) {
				source = filepath.Join(append([]string{link}, dirs[idx+1:]...)...)
				break
			}

			// relative symlink, construct new source, then re-evaluate everything
			if strings.HasPrefix(link, "../") {
				curr = filepath.Dir(curr)
			}

			curr = filepath.Join(append([]string{curr, link}, dirs[idx+1:]...)...)
			source, err = filepath.Rel(d.root, curr)
			if err != nil {
				return "", fmt.Errorf("failed to eval symlinks for %s: %w", orig, err)
			}
			break
		}
	}
}

// Parse the given file using the given function and options.
func (d *dirFS) Parse(file string, parseFn func([]string) error, options ...parser.SimpleParserOption) error {
	rela, err := ensureValid(file)
	if err != nil {
		return err
	}

	return parser.NewSimpleParser(
		parseFn,
		append(
			[]parser.SimpleParserOption{
				parser.WithSourceReader(d.ReadFile),
			},
			options...,
		)...,
	).Parse(rela)
}

// Make sure that a given name is valid for os.DirFS.
func ensureValid(name string) (string, error) {
	name = path.Clean(name)
	if name[0] == '/' {
		return name[1:], nil
	}
	if strings.HasPrefix(name, "../") {
		return name, fs.ErrInvalid
	}
	return name, nil
}

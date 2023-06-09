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

package emufs

import (
	"fmt"
	"io"
	"io/fs"
	"path"
	"sort"
	"strings"
	"syscall"
	"time"
)

// Dir is an emulated directory.
//
// It is a collection of other directories, files and symlinks.
// All other emulated metadata (permissions, modification time,
// etc. are hardcoded by the emulation.
type Dir struct {
	name  string
	Dirs  map[string]*Dir
	Files map[string]File
	Links map[string]Link
}

// File is an emulated file. It is the actual file content.
type File string

// Link is an emulated symbolic link. It is the path the link points to.
type Link string

// FS is an emulated filesystem.
type FS struct {
	root *Dir
}

var (
	statTime = time.Now()
)

// EmuFS creates an emulated filesystem with the given root.
func EmuFS(root *Dir) (*FS, error) {
	e := &FS{
		root: root,
	}

	err := e.chkAndPopulate("/", root, root)
	if err != nil {
		return nil, err
	}

	return e, nil
}

func (e *FS) chkAndPopulate(prefix string, dir *Dir, parent *Dir) error {
	fmt.Printf("checking/populating dir %s (%p) (dirs: %+v, files: %+v)...\n",
		prefix, dir, dir.Dirs, dir.Files)
	dir.name = prefix
	for n, _ := range dir.Files {
		name := path.Join(prefix, n)
		if !isValidEntry(n) {
			return &fs.PathError{Op: "mount", Path: name, Err: fs.ErrInvalid}
		}
		_, ok := dir.Links[n]
		if ok {
			return &fs.PathError{Op: "mount", Path: name, Err: fs.ErrExist}
		}
		_, ok = dir.Dirs[n]
		if ok {
			return &fs.PathError{Op: "mount", Path: name, Err: fs.ErrExist}
		}
	}

	for n, _ := range dir.Links {
		name := path.Join(prefix, n)
		/*if !isValidEntry(n) {
			return fmt.Errorf("invalid link '%s': %w", name, fs.ErrInvalid)
		}*/
		_, ok := dir.Dirs[n]
		if ok {
			return &fs.PathError{Op: "mount", Path: name, Err: fs.ErrExist}
		}
	}

	for n, d := range dir.Dirs {
		name := path.Join(prefix, n)
		if !isValidEntry(n) {
			return &fs.PathError{Op: "mount", Path: name, Err: fs.ErrInvalid}
		}
		err := e.chkAndPopulate(name, d, dir)
		if err != nil {
			return err
		}
	}

	if dir.Dirs == nil {
		dir.Dirs = map[string]*Dir{}
	}
	dir.Dirs[".."] = parent
	dir.Dirs["."] = dir

	return nil
}

func isValidEntry(name string) bool {
	return fs.ValidPath(name)
}

func (e *FS) clean(name string) (string, error) {
	name = path.Clean(name)
	if strings.HasPrefix(name, "../") {
		return "", fs.ErrInvalid
	}

	if len(name) > 0 && name[0] == '/' {
		name = name[1:]
	}

	return name, nil
}

func (e *FS) Open(name string) (fs.File, error) {
	fmt.Printf("opening '%s'...\n", name)

	if !isValidEntry(name) {
		return nil, &fs.PathError{Op: "open", Path: name, Err: fs.ErrInvalid}
	}

	resolved, dir, err := e.resolve(name)
	if err != nil {
		return nil, err
	}

	base := path.Base(resolved)

	d, ok := dir.Dirs[base]
	if ok {
		info := &emuFileInfo{
			name: base,
			size: 0,
			mode: 0755 | fs.ModeDir,
			mod:  statTime,
		}
		return &emuDir{
			name:    base,
			info:    info,
			dir:     d,
			entries: d.entries(),
		}, nil
	}

	f, ok := dir.Files[base]
	if !ok {
		return nil, &fs.PathError{Op: "open", Path: name, Err: fs.ErrNotExist}
	}

	return &emuFile{
		name:    base,
		content: []byte(string(f)),
	}, nil
}

func (e *FS) ReadDir(name string) ([]fs.DirEntry, error) {
	resolved, dir, err := e.resolve(name)
	if err != nil {
		return nil, err
	}

	base := path.Base(resolved)
	d, ok := dir.Dirs[base]
	if !ok {
		return nil, syscall.ENOTDIR
	}

	return d.entries(), nil
}

func (d *Dir) entries() []fs.DirEntry {
	entries := []fs.DirEntry{}
	for n, _ := range d.Dirs {
		if n == "." || n == ".." {
			continue
		}
		entries = append(entries,
			&emuDirEntry{
				name: n,
				info: &emuFileInfo{
					name: n,
					size: 0,
					mode: 0755 | fs.ModeDir,
					mod:  statTime,
				},
			},
		)
	}
	for n, e := range d.Files {
		entries = append(entries,
			&emuDirEntry{
				name: n,
				info: &emuFileInfo{
					name: n,
					size: int64(len(e)),
					mode: 0644,
					mod:  statTime,
				},
			},
		)
	}
	for n, e := range d.Links {
		entries = append(entries,
			&emuDirEntry{
				name: n,
				info: &emuFileInfo{
					name: n,
					size: int64(len(e)),
					mode: 0777 | fs.ModeSymlink,
					mod:  statTime,
				},
			},
		)
	}

	sort.Slice(entries, func(i, j int) bool {
		return entries[i].Name() < entries[j].Name()
	})

	return entries
}

func (e *FS) EvalSymlinks(name string) (string, error) {
	resolved, _, err := e.resolve(name)
	return resolved, err
}

func (e *FS) resolve(name string) (result string, resDir *Dir, retErr error) {
	defer func() {
		if result != "" {
			fmt.Printf(" - '%s' resolved => '%s' in dir %s (%p)\n",
				name, result, resDir.name, resDir)
		} else {
			fmt.Printf(" - '%s' failed to resolve: %v'\n", name, retErr)
		}
	}()

	const maxAllowedLinks = 32
	nlink := 0
	source := name
resolve:
	for {
		var err error

		source, err = e.clean(source)
		if err != nil {
			return "", nil, err
		}

		cwd := ""
		dir := e.root
		dirs := strings.Split(source, "/")
		for i := 0; i < len(dirs); i++ {
			fmt.Printf(" - resolve looking for '%s' in '%s'...\n", dirs[i], cwd)
			d, ok := dir.Dirs[dirs[i]]
			if ok {
				fmt.Printf("   => a dir\n")
				cwd = path.Join(cwd, dirs[i])
				if i == len(dirs)-1 {
					return cwd, dir, nil
				}
				dir = d
				continue
			}

			_, ok = dir.Files[dirs[i]]
			if ok && i == len(dirs)-1 {
				fmt.Printf("   => the file\n")
				return path.Join(cwd, dirs[i]), dir, nil
			}
			if ok {
				return "", nil, syscall.ENOTDIR
			}

			l, ok := dir.Links[dirs[i]]
			if !ok {
				return "", nil, fs.ErrNotExist
			}
			fmt.Printf("   => a link (%s)\n", l)
			nlink++
			if nlink >= maxAllowedLinks {
				return "", nil, syscall.ELOOP
			}

			/*
				cl, err := e.clean(string(l))
				if err != nil {
					return "", nil, err
				}*/

			if len(l) > 0 && l[0] == '/' {
				source = path.Join(append([]string{string(l)}, dirs[i+1:]...)...)
			} else {
				source = path.Join(append([]string{cwd, string(l)}, dirs[i+1:]...)...)
			}

			fmt.Printf("- continue resolution with '%s'...\n", source)

			continue resolve
		}
	}
}

// emuFile is a file opened by Open().
type emuFile struct {
	name    string
	content []byte
	offset  int
	closed  bool
}

func (f *emuFile) Stat() (fs.FileInfo, error) {
	return &emuFileInfo{
		name: f.name,
		size: int64(len(f.content)),
		mode: 0644,
		mod:  statTime,
	}, nil
}

func (f *emuFile) Read(buf []byte) (retCnt int, retErr error) {
	var err error

	/*
		defer func() {
			if retErr == nil {
				fmt.Printf("Read %s %d@%d: %d %s\n", f.name, len(buf), f.offset, retCnt, string(buf))
			} else {
				fmt.Printf("Read %s %d@%d %d %s %v\n", f.name, len(buf), f.offset, retCnt, string(buf), retErr)
			}
		}()*/

	if f.offset >= len(f.content) {
		return 0, io.EOF
	}

	beg := f.offset
	end := f.offset + len(buf)
	if end > len(f.content) {
		end = len(f.content)
		err = io.EOF
	}

	cnt := copy(buf, f.content[beg:end])
	f.offset += cnt
	return cnt, err
}

func (f *emuFile) Close() error {
	if f.closed {
		return fs.ErrClosed
	}
	f.closed = true
	return nil
}

// emuFileInfo is returned by Stat().
type emuFileInfo struct {
	name string
	size int64
	mode fs.FileMode
	mod  time.Time
	sys  any
}

func (i *emuFileInfo) Name() string       { return i.name }
func (i *emuFileInfo) Size() int64        { return i.size }
func (i *emuFileInfo) Mode() fs.FileMode  { return i.mode }
func (i *emuFileInfo) ModTime() time.Time { return i.mod }
func (i *emuFileInfo) IsDir() bool {
	return (i.mode & fs.ModeDir) != 0
}
func (i *emuFileInfo) Sys() any { return i.sys }

// emuDir is returned by Open for directories.
type emuDir struct {
	name    string
	info    *emuFileInfo
	dir     *Dir
	entries []fs.DirEntry
	offset  int
}

func (d *emuDir) Stat() (fs.FileInfo, error) {
	return d.info, nil
}

func (d *emuDir) Read([]byte) (int, error) {
	return 0, &fs.PathError{Op: "read", Path: d.name, Err: syscall.EISDIR}
}

func (d *emuDir) Close() error {
	return nil
}

func (d *emuDir) ReadDir(cnt int) ([]fs.DirEntry, error) {
	entries := d.entries

	/*
		fmt.Printf("ReadDir(%d from %s) @ offset %d (%d entries)...\n",
			cnt, d.name, d.offset, len(d.entries))
	*/

	n := len(entries)
	if d.offset >= n {
		if cnt <= 0 {
			return nil, nil
		}
		return nil, io.EOF
	}

	n -= d.offset
	if cnt > 0 && n > cnt {
		n = cnt
	}

	beg := d.offset
	end := beg + n
	d.offset += n

	return entries[beg:end], nil
}

// emuDirEntry is returned by ReadDir().
type emuDirEntry struct {
	name string
	info *emuFileInfo
}

func (d *emuDirEntry) Name() string { return d.name }
func (d *emuDirEntry) IsDir() bool {
	return (d.info.mode & fs.ModeDir) != 0
}
func (d *emuDirEntry) Type() fs.FileMode { return d.info.mode & fs.ModeType }
func (d *emuDirEntry) Info() (fs.FileInfo, error) {
	return d.info, nil
}

/*
// file is a filed opened by Open.
type file struct {
	name    string
	content string
	seek    int
	closed  bool
}

func (f *file) Stat() (fs.FileInfo, error) {
	return &fileInfo{
		name:  f.name,
		size:  int64(len(f.content)),
		mode:  fs.FileMode(0644),
		time:  time.Now(),
		isDir: false,
	}, nil
}

func (f *file) Read(buf []byte) (int, error) {
	if f.closed {
		return 0, io.EOF
	}
	if f.seek >= len(f.content) {
		return 0, io.EOF
	}

	var err error

	beg, end := f.seek, f.seek+cap(buf)
	if end > len(f.content) {
		end = len(f.content)
		err = io.EOF
	}

	return copy(buf, f.content[beg:end]), err
}

func (f *file) Close() error {
	f.closed = true
	return nil
}

// fileInfo is returned by Stat() or ReadDir()
type fileInfo struct {
	name  string
	size  int64
	mode  fs.FileMode
	time  time.Time
	isDir bool
}

var _ fs.FileInfo = &fileInfo{}

func (i *fileInfo) Name() string {
	return i.name
}

func (i *fileInfo) Size() int64 {
	return i.size
}

func (i *fileInfo) Mode() fs.FileMode {
	return i.mode
}

func (i *fileInfo) ModTime() time.Time {
	return i.time
}

func (i *fileInfo) IsDir() bool {
	return i.isDir
}

func (i *fileInfo) Sys() any {
	return nil
}

type dirFile struct {
	dir *Dir
}

func (d *dirFile) ReadDir(n int) ([]fs.DirEntry, error) {
	return nil, nil
}

// dirEntry is returned by ReadDir()
type dirEntry struct {
	name  string
	isDir bool
	kind  fs.FileMode
	info  *fileInfo
}

var _ fs.DirEntry = &dirEntry{}

func (d *dirEntry) Name() string {
	return d.name
}

func (d *dirEntry) IsDir() bool {
	return d.isDir
}

func (d *dirEntry) Type() fs.FileMode {
	return d.kind
}

func (d *dirEntry) Info() (fs.FileInfo, error) {
	return d.info, nil
}
*/

/////////////////////////////////
/////////////////////////////////
/////////////////////////////////

/*

func (e *emuFS) Open(name string) (fs.File, error) {
	orig := name

resolveName:
	for {
		name = path.Clean(name)
		curr := ""
		if len(name) > 0 && name[0] == '/' {
			name = name[1:]
		}

		dirs := strings.Split(name, "/")
		if len(names) == 0 {
			return nil, fmt.Errorf("failed to open file %s: %w", orig, fs.ErrInvalid)
		}

		dir := e.root
		for idx, part := range names {
			d, ok := dir.Dirs[part]
			if ok {
				dir = d
				continue
			}

			l, ok := dir.Links[part]
			if ok {
				if path.IsAbs(l) {
					name = path.Join(append([]string{l}, dirs[idx+1:]...)...)
					continue resolveName
				}

				if strings.HasPrefix(link, "../") {
					curr = path.Dir(curr)
				}

				curr = filepath.Join(append([]string{curr, l}, dirs[idx+1]...)...)
				continue resolveName
			}

			f, ok := dir.Files[part]
			if ok {
				if idx < len(names)-1 {
					return nil, fmt.Errorf("failed to open file %s: %w", syscall.ENOTDIR)
				}

				return &file{
					name:    part,
					content: f,
				}, nil
			}

			return nil, fmt.Errorf("failed to open file %s: %w", fs.ErrNotExist)
		}
	}
}

func (e *emuFS) ReadDir(name string) ([]DirEntry, error) {

}

func (e *emuFS) EvalSymlinks(name string) (string, error) {
	orig := name

resolveName:
	for {
		name = path.Clean(name)
		curr := ""
		if len(name) > 0 && name[0] == '/' {
			name = name[1:]
		}

		dirs := strings.Split(name, "/")
		if len(names) == 0 {
			return "", fmt.Errorf("failed to eval symlinks for  %s: %w", orig, fs.ErrInvalid)
		}

		dir := e.root
		for idx, part := range names {
			d, ok := dir.Dirs[part]
			if ok {
				dir = d
				curr = path.Join(curr, part)
				continue
			}

			l, ok := dir.Links[part]
			if ok {
				if path.IsAbs(l) {
					name = path.Join(append([]string{l}, dirs[idx+1:]...)...)
					continue resolveName
				}

				if strings.HasPrefix(link, "../") {
					curr = path.Dir(curr)
				}

				curr = filepath.Join(append([]string{curr, l}, dirs[idx+1]...)...)
				continue resolveName
			}

			f, ok := dir.Files[part]
			if ok {
				if idx < len(names)-1 {
					return "", fmt.Errorf("failed to eval symlinks for %s: %w", syscall.ENOTDIR)
				}

				return curr, nil
			}

			return "", fmt.Errorf("failed to eval symlinks for %s: %w", fs.ErrNotExist)
		}
	}
}

func (e *emuFS) validateDir(parent string, dir *Dir) error {
	for n, f := range dir.Files {
		name := path.Join(parent, n)
		if strings.Index(n, '/') != -1 {
			return fmt.Errorf("invalid file '%s': %w", name, fs.ErrInvalid)
		}
		_, ok := dir.Links[n]
		if ok {
			return fmt.Errorf("invalid file '%s': link name clash", name)
		}
		_, ok := dir.Dirs[n]
		if ok {
			return fmt.Errorf("invalid file '%s': dir name clash", name)
		}
	}

	for n, f := range dir.Links {
		name := path.Join(parent, n)
		if strings.Index(n, '/') != -1 {
			return fmt.Errorf("invalid link '%s': %w", name, fs.ErrInvalid)
		}
		_, ok := dir.Dirs[n]
		if ok {
			return fmt.Errorf("invalid file '%s': dir name clash", name)
		}
	}
	for n, d := range dir.Dirs {
		name := path.Join(parent, n)
		if strings.Index(n, '/') != -1 {
			return fmt.Errorf("invalid directory '%s': %w", name, fs.ErrInvalid)
		}
		err := e.validateDir(name, d)
		if err != nil {
			return err
		}
	}

	return nil
}
*/

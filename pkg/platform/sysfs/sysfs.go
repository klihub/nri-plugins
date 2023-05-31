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

package sysfs

import (
	logger "github.com/containers/nri-plugins/pkg/log"
	pfs "github.com/containers/nri-plugins/pkg/platform/fs"
	"github.com/containers/nri-plugins/pkg/platform/parser"
)

// SysFS provides access to the Linux sysfs pseudofilesystem.
type SysFS interface {
	pfs.FS

	// GetSystem returns system information gathered from sys/devices/system.
	GetSystem() (System, error)

	ParseText(file string, parseFn func([]string) error) error
	ParseLine(file string, parseFn func([]string) error) error
}

var (
	WithSysRoot = pfs.WithRoot
)

type (
	SysFSOption = pfs.DirFSOption
)

var (
	// Our logger instance.
	log = logger.Get("sysfs")
)

// sysfs is our implementation of SysFS.
type sysFS struct {
	pfs.FS
	system *system
}

// Get an interface for interacting with sysfs.
func Get(options ...SysFSOption) (SysFS, error) {
	fs, err := pfs.DirFS(options...)
	if err != nil {
		return nil, err
	}

	s := &sysFS{
		FS: fs,
	}

	return s, nil
}

var (
	TextFileOptions = []parser.SimpleParserOption{
		parser.WithPreprocessor(parser.TrimSpace),
		parser.WithEntrySplitter(parser.SplitByLines),
		parser.WithFieldSplitter(parser.SplitBySpaces),
	}

	OnelinerOptions = []parser.SimpleParserOption{
		parser.WithPreprocessor(parser.TrimSpace),
		parser.WithFieldSplitter(parser.SplitBySpaces),
	}
)

func (s *sysFS) ParseText(file string, parseFn func([]string) error) error {
	return s.Parse(file, parseFn, TextFileOptions...)
}

func (s *sysFS) ParseLine(file string, parseFn func([]string) error) error {
	return s.Parse(file, parseFn, OnelinerOptions...)
}

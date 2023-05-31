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
	"sync"

	"github.com/containers/nri-plugins/pkg/utils/idset"
)

type (
	ID    = idset.ID
	IDSet = idset.IDSet
)

const (
	UnknownID = idset.UnknownID
)

// System provides interfaces for querying information about the underlying
// system.
//
// All information is gathered from sysfs. Interfaces are provided for
// querying memory nodes and CPUs available in the system.
type System interface {
	// SysFS returns the sysfs associated with this System.
	SysFS() SysFS
	// Nodes returns an interface for querying available system memory.
	Nodes() Nodes
	// CPUs returns an interface for querying availbe system CPUs.
	CPUs() CPUs
}

// system is our implementation of System.
type system struct {
	sync.RWMutex
	fs    *sysFS
	nodes *sysNodes
	cpus  *sysCPUs
}

// GetSystem returns the System interface for this sysfs.
func (s *sysFS) GetSystem() (System, error) {
	if s.system == nil {
		sys := &system{
			fs: s,
		}

		err := sys.refresh()
		if err != nil {
			return nil, err
		}

		s.system = sys
	}

	return s.system, nil
}

func (s *system) refresh() error {
	err := s.readSysNodes()
	if err != nil {
		return err
	}

	err = s.readSysCPUs()
	if err != nil {
		return err
	}

	return nil
}

func (s *system) SysFS() SysFS {
	return s.fs
}

func (s *system) Nodes() Nodes {
	return s.nodes
}

func (s *system) CPUs() CPUs {
	return s.cpus
}

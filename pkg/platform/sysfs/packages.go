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
	"fmt"

	"github.com/containers/nri-plugins/pkg/utils/cpuset"
)

// Packages is an interface for querying available physical packages
// (a.k.a 'sockets') in the system.
type Packages interface {
	// System returns the System associated with these Packages.
	System() System
	// ID returns the IDs of all packages in the system.
	IDs() []ID
	// Package returns an interface for querying the package with the given ID.
	Package(ID) Package
}

// Package is an interface for querying a single physical package of a System.
type Package interface {
	// System returns the System associated with this Package.
	System() System
	// ID returns the ID of this package.
	ID() ID
	// DieIDs returns the die IDs for this package.
	DieIDs() []ID
	// Die returns the die with the given ID in this package.
	Die(ID) Die
	// CPUSet returns the set of CPUs in the package.
	CPUSet() cpuset.CPUSet
}

// Die is an interface for querying a single die in a physical package.
type Die interface {
	// Package returns the Package for this Die.
	Package() Package
	// ID returns the ID of this die
	ID() ID
	// CPUSet returns the set of CPUs in this die.
	CPUSet() cpuset.CPUSet
	// UncoreInfo returns the uncore info for this die.
	UncoreInfo() *UncoreInfo
}

// UncoreInfo contains uncore info for a package/die.
type UncoreInfo struct {
	CurrentFreq    int64
	InitialMinFreq int64
	InitialMaxFreq int64
	MinFreq        int64
	MaxFreq        int64
}

const (
	uncoreDir = "sys/devices/system/cpu/intel_uncore_frequency"
)

// sysPackages is our implementation of Packages
type sysPackages struct {
	system *system
	pkgIDs IDSet
	pkgs   map[ID]*sysPackage
}

// sysPackage is our implementation of Package.
type sysPackage struct {
	system *system
	id     ID
	dieIDs IDSet
	cpus   cpuset.CPUSet
	dies   map[ID]*sysDie
}

// sysDie is our implementation of Die.
type sysDie struct {
	pkg        *sysPackage
	id         ID
	cpus       cpuset.CPUSet
	uncoreDir  string
	uncoreInfo *UncoreInfo
}

func (s *system) collectSysPackages() error {
	s.pkgs = &sysPackages{
		system: s,
		pkgIDs: NewIDSet(),
		pkgs:   map[ID]*sysPackage{},
	}

	for _, cpuID := range s.CPUs().Online().List() {
		c := s.CPUs().CPU(cpuID)
		pkgID := c.PackageID()
		dieID := c.DieID()
		cset := cpuset.New(cpuID)
		pkg, ok := s.pkgs.pkgs[pkgID]
		if !ok {
			fmt.Printf("*** discovering package #%d...\n", pkgID)
			pkg = &sysPackage{
				system: s,
				id:     pkgID,
				dieIDs: NewIDSet(),
				dies:   map[ID]*sysDie{},
				cpus:   cset,
			}
			s.pkgs.pkgs[pkgID] = pkg
			s.pkgs.pkgIDs.Add(pkgID)
		} else {
			pkg.cpus = pkg.cpus.Union(cset)
		}
		die, ok := pkg.dies[dieID]
		if !ok {
			fmt.Printf("*** discovering die #%d/%d...\n", pkgID, dieID)
			die = &sysDie{
				pkg:       pkg,
				id:        dieID,
				cpus:      cset,
				uncoreDir: fmt.Sprintf(uncoreDir+"/package_%2.2d_die_%2.2d", pkgID, dieID),
			}
			pkg.dies[dieID] = die
			pkg.dieIDs.Add(dieID)
			fmt.Printf("uncoreDir: %s\n", die.uncoreDir)
		} else {
			die.cpus = die.cpus.Union(cset)
		}
	}

	for _, id := range s.Packages().IDs() {
		pkg := s.Packages().Package(id)
		fmt.Printf("*** package #%d:\n", id)
		fmt.Printf("    - id: %d\n", pkg.ID())
		fmt.Printf("    - dies: %v\n", pkg.DieIDs())
		fmt.Printf("    - cpus: %s\n", pkg.CPUSet())
		for _, dieID := range pkg.DieIDs() {
			die := pkg.Die(dieID)
			fmt.Printf("    * die #%d/%d:\n", id, dieID)
			fmt.Printf("      - package: #%d\n", die.Package().ID())
			fmt.Printf("      - cpus: %s\n", die.CPUSet())
		}
	}

	return nil
}

func (s *sysPackages) System() System {
	return s.system
}

func (s *sysPackages) IDs() []ID {
	return s.pkgIDs.SortedMembers()
}

func (s *sysPackages) Package(id ID) Package {
	p, ok := s.pkgs[id]
	if ok {
		return p
	}
	return &sysPackage{
		system: s.system,
		id:     id,
	}
}

func (p *sysPackage) System() System {
	return p.system
}

func (p *sysPackage) ID() ID {
	return p.id
}

func (p *sysPackage) DieIDs() []ID {
	return p.dieIDs.SortedMembers()
}

func (p *sysPackage) Die(id ID) Die {
	d, ok := p.dies[id]
	if ok {
		return d
	}
	return &sysDie{
		pkg:        p,
		id:         id,
		uncoreInfo: &UncoreInfo{},
	}
}

func (p *sysPackage) CPUSet() cpuset.CPUSet {
	return p.cpus
}

func (d *sysDie) Package() Package {
	return d.pkg
}

func (d *sysDie) ID() ID {
	return d.id
}

func (d *sysDie) CPUSet() cpuset.CPUSet {
	return d.cpus
}

func (d *sysDie) UncoreInfo() *UncoreInfo {
	return d.uncoreInfo
}

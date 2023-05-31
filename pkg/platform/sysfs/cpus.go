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
	"errors"
	"fmt"
	"io/fs"
	"path"
	"strconv"

	"github.com/containers/nri-plugins/pkg/platform/epp"
	"github.com/containers/nri-plugins/pkg/utils/cpuset"
)

// CPUs is an interface for querying available system CPUs.
type CPUs interface {
	// System returns the System associated with these CPUs.
	System() System
	// Online returns the cpuset of online CPUs in the system.
	Online() cpuset.CPUSet
	// Offline returns the cpuset of offline CPUs in the system.
	Offline() cpuset.CPUSet
	// Isolated returns the cpuset of kernel-isolated CPUs in the system.
	Isolated() cpuset.CPUSet
	// Present returns the cpuset of CPUs present in the system.
	Present() cpuset.CPUSet
	// CPU returns an interface for querying the CPU with the given ID.
	CPU(ID) CPU
	// ReadFile reads a file relative to the cpu subtree of sysfs.
	ReadFile(string) ([]byte, error)
	// WriteFile writes a file relative to this CPU in sysfs.
	WriteFile(string, []byte) error
}

// sysCPUs is our implementation of CPUs.
type sysCPUs struct {
	system   *system
	present  cpuset.CPUSet
	online   cpuset.CPUSet
	offline  cpuset.CPUSet
	isolated cpuset.CPUSet
	cpus     map[ID]*sysCPU
}

// CPU is an interface for querying a single CPU in the system.
type CPU interface {
	// System returns the System associated with this CPU.
	System() System
	// ID returns the ID for this CPU.
	ID() ID
	// PackageID returns the physical package ID of this CPU.
	PackageID() ID
	// DieID returns the die ID of this CPU.
	DieID() ID
	// ClusterID returns the cluster ID of this CPU.
	ClusterID() ID
	// CoreID returns the core ID of this CPU.
	CoreID() ID
	// PakcageCPUs returns the cpuset of all CPUs in the same physical package.
	PackageCPUs() cpuset.CPUSet
	// DieCPUs returns the cpuset of all CPUs on the same die.
	DieCPUs() cpuset.CPUSet
	// CoreSiblings returns the cpuset of all CPUs in the same core.
	CoreSiblings() cpuset.CPUSet
	// ThreadSiblings returns the cpuset of this CPU's hyperthreads.
	ThreadSiblings() cpuset.CPUSet
	// CPUFreq returns cpufreq information for this CPU.
	CPUFreq() *CPUFreq
	// ReadFile reads a file relative to this cpu in sysfs.
	ReadFile(string) ([]byte, error)
	// WriteFile writes data to a file relative to this CPU in sysfs.
	WriteFile(string, []byte) error
}

// sysCPU is our implementation of CPU.
type sysCPU struct {
	system         *system
	id             ID
	dir            string
	packageID      ID
	dieID          ID
	clusterID      ID
	coreID         ID
	packageCPUs    cpuset.CPUSet
	dieCPUs        cpuset.CPUSet
	clusterCPUs    cpuset.CPUSet
	coreSiblings   cpuset.CPUSet
	threadSiblings cpuset.CPUSet
	cpuFreq        *CPUFreq
}

// CPUFreq contains data collected from sys/devices/system/cpu/cpu$ID/cpufreq.
type CPUFreq struct {
	AffectedCPUs   cpuset.CPUSet
	BaseFreq       uint64
	CPUInfoMinFreq uint64
	CPUInfoMaxFreq uint64
	ScalingMinFreq uint64
	ScalingMaxFreq uint64
	ScalingCurFreq uint64
	AvailableEPPs  []epp.Preference
	CurrentEPP     epp.Preference
}

const (
	cpuSubtree = "sys/devices/system/cpu"
)

func (s *system) readSysCPUs() error {
	s.cpus = &sysCPUs{
		system: s,
		cpus:   map[ID]*sysCPU{},
	}
	return s.cpus.refresh()
}

func (s *sysCPUs) refresh() error {
	const (
		present  = cpuSubtree + "/present"
		online   = cpuSubtree + "/online"
		offline  = cpuSubtree + "/offline"
		isolated = cpuSubtree + "/isolated"
	)

	log.Debugf("refreshing CPU data from sysfs...")

	for entry, csetp := range map[string]*cpuset.CPUSet{
		present:  &s.present,
		online:   &s.online,
		offline:  &s.offline,
		isolated: &s.isolated,
	} {
		err := s.system.fs.ParseLine(entry, cpusetParser(csetp))
		if err != nil {
			return err
		}
	}

	for _, id := range s.present.List() {
		c, ok := s.cpus[id]
		if !ok {
			c = &sysCPU{
				system:  s.system,
				id:      id,
				dir:     path.Join(cpuSubtree, "cpu"+strconv.Itoa(id)),
				cpuFreq: &CPUFreq{},
			}
			s.cpus[id] = c
		}
		err := c.refresh()
		if err != nil {
			return err
		}
	}

	return nil
}

func (s *sysCPUs) System() System {
	return s.system
}

func (s *sysCPUs) Online() cpuset.CPUSet {
	return s.online
}

func (s *sysCPUs) Offline() cpuset.CPUSet {
	return s.offline
}

func (s *sysCPUs) Isolated() cpuset.CPUSet {
	return s.isolated
}

func (s *sysCPUs) Present() cpuset.CPUSet {
	return s.present
}

func (s *sysCPUs) CPU(id ID) CPU {
	if c, ok := s.cpus[id]; ok {
		return c
	}
	return &sysCPU{
		system:  s.system,
		id:      UnknownID,
		cpuFreq: &CPUFreq{},
	}
}

func (s *sysCPUs) ReadFile(file string) ([]byte, error) {
	return s.system.SysFS().ReadFile(path.Join(cpuSubtree, file))
}

func (s *sysCPUs) WriteFile(file string, data []byte) error {
	return s.system.SysFS().WriteFile(path.Join(cpuSubtree, file), data, 0644)
}

func (c *sysCPU) refresh() error {
	log.Debug("refreshing sysfs CPU #%d...", c.id)

	for entry, idp := range map[string]*ID{
		c.dir + "/topology/physical_package_id": &c.packageID,
		c.dir + "/topology/die_id":              &c.dieID,
		c.dir + "/topology/cluster_id":          &c.clusterID,
		c.dir + "/topology/core_id":             &c.coreID,
	} {
		err := c.system.fs.ParseLine(entry, idParser(idp))
		if err != nil {
			if errors.Is(err, fs.ErrNotExist) {
				log.Warnf("failed to read CPU #%d entry %s: %v", c.id, entry, err)
				*idp = UnknownID
				continue
			}
			return fmt.Errorf("failed to read CPU #%d entry %s: %w", c.id, entry, err)
		}
	}

	for entry, csetp := range map[string]*cpuset.CPUSet{
		c.dir + "/topology/package_cpus_list":    &c.packageCPUs,
		c.dir + "/topology/die_cpus_list":        &c.dieCPUs,
		c.dir + "/topology/cluster_cpus_list":    &c.clusterCPUs,
		c.dir + "/topology/core_siblings_list":   &c.coreSiblings,
		c.dir + "/topology/thread_siblings_list": &c.threadSiblings,
		c.dir + "/cpufreq/affected_cpus":         &c.cpuFreq.AffectedCPUs,
	} {
		err := c.system.fs.ParseLine(entry, cpusetParser(csetp))
		if err != nil {
			if errors.Is(err, fs.ErrNotExist) {
				log.Warnf("failed to read CPU #%d entry %s: %v", c.id, entry, err)
				*csetp = cpuset.New()
				continue
			}
			return fmt.Errorf("failed to read CPU #%d entry %s: %w", c.id, entry, err)
		}
	}

	for entry, csetp := range map[string]*cpuset.CPUSet{
		c.dir + "/topology/package_cpus_list":    &c.packageCPUs,
		c.dir + "/topology/die_cpus_list":        &c.dieCPUs,
		c.dir + "/topology/cluster_cpus_list":    &c.clusterCPUs,
		c.dir + "/topology/core_siblings_list":   &c.coreSiblings,
		c.dir + "/topology/thread_siblings_list": &c.threadSiblings,
		c.dir + "/cpufreq/affected_cpus":         &c.cpuFreq.AffectedCPUs,
	} {
		err := c.system.fs.ParseLine(entry, cpusetParser(csetp))
		if err != nil {
			if errors.Is(err, fs.ErrNotExist) {
				log.Warnf("failed to read CPU #%d entry %s: %v", c.id, entry, err)
				*csetp = cpuset.New()
				continue
			}
			return fmt.Errorf("failed to read CPU #%d entry %s: %w", c.id, entry, err)
		}
	}

	for entry, uintp := range map[string]*uint64{
		c.dir + "/cpufreq/base_frequency":   &c.cpuFreq.BaseFreq,
		c.dir + "/cpufreq/cpuinfo_min_freq": &c.cpuFreq.CPUInfoMinFreq,
		c.dir + "/cpufreq/cpuinfo_max_freq": &c.cpuFreq.CPUInfoMaxFreq,
		c.dir + "/cpufreq/scaling_min_freq": &c.cpuFreq.ScalingMinFreq,
		c.dir + "/cpufreq/scaling_max_freq": &c.cpuFreq.ScalingMaxFreq,
		c.dir + "/cpufreq/scaling_cur_freq": &c.cpuFreq.ScalingCurFreq,
	} {
		err := c.system.fs.ParseLine(entry, uint64Parser(uintp))
		if err != nil {
			if errors.Is(err, fs.ErrNotExist) {
				log.Warnf("failed to read CPU #%d entry %s: %v", c.id, entry, err)
				continue
			}
			return fmt.Errorf("failed to read CPU #%d entry %s: %w", c.id, entry, err)
		}
	}

	entry := c.dir + "/cpufreq/energy_performance_available_preferences"
	err := c.system.fs.ParseLine(
		entry,
		eppSliceParser(&c.cpuFreq.AvailableEPPs),
	)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			log.Warnf("no sysfs CPU #%d entry %s", c.id, entry)
		} else {
			return fmt.Errorf("failed to read #%d entry %s: %w", c.id, entry, err)
		}
	}

	entry = c.dir + "/cpufreq/energy_performance_preference"
	err = c.system.fs.ParseLine(
		entry,
		eppParser(&c.cpuFreq.CurrentEPP),
	)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			log.Warnf("no CPU #%d entry %s", c.id, entry)
		} else {
			return fmt.Errorf("failed to read #%d entry %s: %w", c.id, entry, err)
		}
	}

	if c.clusterID == UnknownID {
		c.clusterID = 0
	}

	return nil
}

func (c *sysCPU) System() System {
	return c.system
}

func (c *sysCPU) ID() ID {
	return c.id
}

func (c *sysCPU) PackageID() ID {
	return c.packageID
}

func (c *sysCPU) DieID() ID {
	return c.dieID
}

func (c *sysCPU) ClusterID() ID {
	return c.clusterID
}

func (c *sysCPU) CoreID() ID {
	return c.coreID
}

func (c *sysCPU) PackageCPUs() cpuset.CPUSet {
	return c.packageCPUs
}

func (c *sysCPU) DieCPUs() cpuset.CPUSet {
	return c.dieCPUs
}

func (c *sysCPU) CoreSiblings() cpuset.CPUSet {
	return c.coreSiblings
}

func (c *sysCPU) ThreadSiblings() cpuset.CPUSet {
	return c.threadSiblings
}

func (c *sysCPU) CPUFreq() *CPUFreq {
	cpufreq := *c.cpuFreq
	return &cpufreq
}

func (c *sysCPU) ReadFile(file string) ([]byte, error) {
	return c.system.SysFS().ReadFile(path.Join(c.dir, file))
}

func (c *sysCPU) WriteFile(file string, data []byte) error {
	return c.system.SysFS().WriteFile(path.Join(c.dir, file), data, 0644)
}

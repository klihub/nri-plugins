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
	"strconv"
	"strings"

	"github.com/containers/nri-plugins/pkg/platform/epp"
	"github.com/containers/nri-plugins/pkg/utils/cpuset"
	"github.com/containers/nri-plugins/pkg/utils/idset"
)

func idParser(p *ID) func([]string) error {
	return func(fields []string) error {
		if len(fields) == 0 || len(fields[0]) == 0 {
			*p = UnknownID
			return nil
		}
		i, err := strconv.ParseInt(strings.TrimSpace(fields[0]), 10, 64)
		if err != nil {
			*p = UnknownID
			return err
		}
		*p = ID(i)
		return nil
	}
}

func idsetParser(p *IDSet) func([]string) error {
	return func(fields []string) error {
		if len(fields) == 0 || len(fields[0]) == 0 {
			*p = idset.NewIDSet()
			return nil
		}
		iset, err := idset.ParseIDSet(strings.TrimSpace(fields[0]))
		if err != nil {
			*p = idset.NewIDSet()
			return err
		}
		*p = iset
		return nil
	}
}

func cpusetParser(p *cpuset.CPUSet) func([]string) error {
	return func(fields []string) error {
		if len(fields) == 0 || len(fields[0]) == 0 {
			*p = cpuset.New()
			return nil
		}
		cset, err := cpuset.Parse(strings.TrimSpace(fields[0]))
		if err != nil {
			*p = cpuset.New()
			return err
		}
		*p = cset
		return nil
	}
}

func uint64Parser(p *uint64) func([]string) error {
	return func(fields []string) error {
		if len(fields) == 0 || len(fields[0]) == 0 {
			*p = 0
			return nil
		}
		i, err := strconv.ParseInt(strings.TrimSpace(fields[0]), 10, 64)
		if err != nil {
			*p = 0
			return err
		}
		*p = uint64(i)
		return err
	}
}

func eppParser(p *epp.Preference) func([]string) error {
	return func(fields []string) error {
		if len(fields) == 0 || len(fields[0]) == 0 {
			*p = epp.Default
			return nil
		}
		pref, err := epp.Parse(fields[0])
		if err != nil {
			return err
		}
		*p = pref
		return nil
	}
}

func eppSliceParser(p *[]epp.Preference) func([]string) error {
	return func(fields []string) error {
		if len(fields) == 0 {
			*p = []epp.Preference{epp.Default}
			return nil
		}
		*p = []epp.Preference{}
		for _, name := range fields {
			pref, err := epp.Parse(name)
			if err != nil {
				return err
			}
			*p = append(*p, pref)
		}
		return nil
	}
}

func intSliceParser(p *[]int, separator string) func([]string) error {
	return func(fields []string) error {
		var slice []int
		if len(fields) == 0 {
			*p = slice
			return nil
		}

		for _, str := range fields {
			i, err := strconv.Atoi(str)
			if err != nil {
				return err
			}
			slice = append(slice, i)
		}

		*p = slice
		return nil
	}
}

func meminfoParser(mip *MemInfo) func([]string) error {
	return func(fields []string) error {
		var err error

		if len(fields) < 4 {
			return fmt.Errorf("invalid MemInfo entry \"%s\"", strings.Join(fields, " "))
		}
		if fields[0] == "Node" {
			fields = fields[2:]
		}

		ptrMap := map[string]*uint64{
			"MemTotal:":        &mip.MemTotal,
			"MemFree:":         &mip.MemFree,
			"MemUsed:":         &mip.MemUsed,
			"SwapCached:":      &mip.SwapCached,
			"Active:":          &mip.Active,
			"Inactive:":        &mip.Inactive,
			"Active(anon):":    &mip.ActiveAnon,
			"Inactive(anon):":  &mip.InactiveAnon,
			"Active(file):":    &mip.ActiveFile,
			"Inactive(file):":  &mip.InactiveFile,
			"Unevictable:":     &mip.Unevictable,
			"Mlocked:":         &mip.Mlocked,
			"Dirty:":           &mip.Dirty,
			"Writeback:":       &mip.Writeback,
			"FilePages:":       &mip.FilePages,
			"Mapped:":          &mip.Mapped,
			"AnonPages:":       &mip.AnonPages,
			"Shmem:":           &mip.Shmem,
			"KernelStack:":     &mip.KernelStack,
			"PageTables:":      &mip.PageTables,
			"SecPageTables:":   &mip.SecPageTables,
			"NFS_Unstable:":    &mip.NFS_Unstable,
			"Bounce:":          &mip.Bounce,
			"WritebackTmp:":    &mip.WritebackTmp,
			"KReclaimable:":    &mip.KReclaimable,
			"Slab:":            &mip.Slab,
			"SReclaimable:":    &mip.SReclaimable,
			"SUnreclaim:":      &mip.SUnreclaim,
			"AnonHugePages:":   &mip.AnonHugePages,
			"ShmemHugePages:":  &mip.ShmemHugePages,
			"ShmemPmdMapped:":  &mip.ShmemPmdMapped,
			"FileHugePages:":   &mip.FileHugePages,
			"FilePmdMapped:":   &mip.FilePmdMapped,
			"HugePages_Total:": &mip.HugePages_Total,
			"HugePages_Free:":  &mip.HugePages_Free,
			"HugePages_Surp:":  &mip.HugePages_Surp,
		}

		p, ok := ptrMap[fields[0]]
		if !ok {
			log.Warnf("unknown field in MemInfo entry \"%s\"", strings.Join(fields, " "))
			return nil
		}

		*p, err = strconv.ParseUint(fields[1], 10, 64)
		if err != nil {
			return fmt.Errorf("failed to parse MemInfo entry \"%s\": %w",
				strings.Join(fields, " "), err)
		}

		if len(fields) > 2 {
			switch fields[2] {
			case "kB":
				*p *= 1024
			default:
				return fmt.Errorf("unknown unit in MemInfo entry \"%s\"",
					strings.Join(fields, " "))
			}
		}

		return nil
	}
}

func numastatParser(nsp *NUMAStat) func([]string) error {
	return func(fields []string) error {
		var err error

		if len(fields) != 2 {
			return fmt.Errorf("invalid NUMAStat entry \"%s\"", strings.Join(fields, " "))
		}

		ptrMap := map[string]*uint64{
			"numa_hit":       &nsp.NUMAHit,
			"numa_miss":      &nsp.NUMAMiss,
			"numa_foreign":   &nsp.NUMAForeign,
			"interleave_hit": &nsp.InterleaveHit,
			"local_node":     &nsp.LocalNode,
			"other_node":     &nsp.OtherNode,
		}

		p, ok := ptrMap[fields[0]]
		if !ok {
			log.Warnf("unknown NUMAStat field in %s", strings.Join(fields, " "))
			return nil
		}

		*p, err = strconv.ParseUint(fields[1], 10, 64)
		if err != nil {
			return fmt.Errorf("failed to parse NUMAStat \"%s\": %w", strings.Join(fields, " "), err)
		}

		return nil
	}
}

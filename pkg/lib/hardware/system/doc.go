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

// Package system is pkg/sysfs reimplemented on top of
// [github.com/containers/nri-plugins/pkg/lib/hardware].
//
// It exports everything pkg/sysfs exports, with the same names and
// signatures. A consumer moves over by changing one import line. Most
// consumers import pkg/sysfs aliased to "system", so even the alias stays.
//
// # Why it exists
//
// It was a migration step, and it is a proof. Nothing in this repository
// uses it any more. pkg/sysfs stays in the tree beside it, so
// equivalence_test.go can run every method of both against the same
// recorded sysfs trees and compare the answers. This package goes when
// pkg/sysfs goes.
//
// Do not build anything new on this package. New code should use
// [github.com/containers/nri-plugins/pkg/lib/hardware] directly.
//
// # Compatibility
//
// The intent is bug-for-bug compatibility with pkg/sysfs. Notable points:
//
//   - There is one *Cache per cache, so a *Cache works as a map key.
//   - MemoryInfo reads the machine on every call, because callers use it to
//     read current usage.
//   - A NUMA node with no CPUs reports package 0 and die 0, and an
//     unreported die or cluster reads as 0, as in pkg/sysfs. The hardware
//     package reports -1 for all of these.
//   - Sst, SstInfo and SstClos expose Intel Speed Select through goresctrl
//     types. The hardware package does not know about SST, so its discovery
//     lives here.
//   - SetCpusOnline, SetCPUFrequencyLimits and CPU.SetFrequencyLimits write
//     to sysfs through the [hardware.WriterFS] from [hardware.Machine.FS].
//     A write goes to the tree the topology was read from.
//   - Discover returns nil: the constructors already discovered everything.
//   - SetSysRoot sets a package global, read when a System is constructed.
//
// # Cost
//
// Each call which returns a set converts it: hardware works in
// [libcpu.CpuMask], these interfaces in cpuset.CPUSet and idset.IDSet.
package system

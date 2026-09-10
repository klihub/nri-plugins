// Copyright 2020 Intel Corporation. All Rights Reserved.
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

// Package sysfs discovers the CPU and memory topology of a machine, and the
// details of its caches, CPU frequencies and Intel Speed Select state.
//
// Deprecated: use [github.com/containers/nri-plugins/pkg/lib/hardware] instead.
// Nothing in this repository uses this package, and it will be removed in a
// later release.
//
// # Moving over
//
// The hardware package describes the same hardware with a smaller interface:
//
//   - A machine is discovered once, with everything read up front, and does
//     not change. There is no Discover on an existing one and no discovery
//     flags.
//   - Lookups return concrete handles, never nil. A CPU or a node the machine
//     does not have reports Valid() == false.
//   - Packages, dies, clusters, cores and caches are all zones, addressed by
//     full coordinates, since the kernel numbers dies and cores within their
//     package. hardware.TopologyIndex is the lookup table for those.
//   - CPU sets are [github.com/containers/nri-plugins/pkg/lib/cpu] masks, not
//     k8s.io/utils/cpuset sets. The ones a machine hands out are sealed.
//   - Discovery reads through an io/fs.FS rooted at the host root, instead of
//     a package global sys root. A test passes a recorded or synthetic tree
//     this way.
//   - Intel Speed Select is not included. Code that needs it probes for it.
//
// [github.com/containers/nri-plugins/pkg/lib/hardware/system] implements this
// package's interface on top of hardware, for a migration in two steps. It is
// removed together with this package.
//
// ParseFileEntries and GetMemoryCapacity only forward to
// [github.com/containers/nri-plugins/pkg/utils/parse.FileEntries] and
// [github.com/containers/nri-plugins/pkg/utils.GetMemoryCapacity].
package sysfs

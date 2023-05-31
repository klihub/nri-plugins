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

package sysfs_test

import (
	"path/filepath"
	"strconv"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/containers/nri-plugins/pkg/platform/sysfs"
)

var _ = Describe("System discovery should,", func() {
	DescribeTable("correctly discover system nodes",
		func(sampleIdx int) {
			var (
				root = filepath.Join(testRoot, strconv.Itoa(sampleIdx))
				fs   sysfs.SysFS
				sys  sysfs.System
				err  error
			)

			fs, err = sysfs.Get(sysfs.WithSysRoot(root))
			Expect(err).To(BeNil())
			Expect(fs).ToNot(BeNil())

			sys, err = fs.GetSystem()
			Expect(err).To(BeNil())
			Expect(sys).ToNot(BeNil())

			chk := testSystems[sampleIdx]
			Expect(chk).ToNot(BeNil())

			nodes := sys.Nodes()
			Expect(nodes).ToNot(BeNil())
			Expect(nodes.HasCPU()).To(Equal(chk.nodes.hasCPU))
			Expect(nodes.HasMemory()).To(Equal(chk.nodes.hasMemory))
			Expect(nodes.HasNormalMemory()).To(Equal(chk.nodes.hasNormalMemory))

			cpus := sys.CPUs()
			Expect(cpus).ToNot(BeNil())
			Expect(cpus.Present().String()).To(Equal(chk.cpus.present))
			Expect(cpus.Online().String()).To(Equal(chk.cpus.online))
			Expect(cpus.Offline().String()).To(Equal(chk.cpus.offline))
			Expect(cpus.Isolated().String()).To(Equal(chk.cpus.isolated))
		},

		Entry("with sample sysfs "+testSystems[0].tarball, 0),
		Entry("with sample sysfs "+testSystems[1].tarball, 1),
		Entry("with sample sysfs "+testSystems[2].tarball, 2),
	)

	DescribeTable("correctly discover each system node",
		func(sampleIdx int) {
			var (
				root = filepath.Join(testRoot, strconv.Itoa(sampleIdx))
				fs   sysfs.SysFS
				sys  sysfs.System
				err  error
			)

			fs, err = sysfs.Get(sysfs.WithSysRoot(root))
			Expect(err).To(BeNil())
			Expect(fs).ToNot(BeNil())

			sys, err = fs.GetSystem()
			Expect(err).To(BeNil())
			Expect(sys).ToNot(BeNil())

			chk := testSystems[sampleIdx]
			nodes := sys.Nodes()

			for _, id := range nodes.Online() {
				n := nodes.Node(id)
				Expect(n).ToNot(BeNil())
				Expect(n.CPUSet().String()).To(Equal(chk.nodes.nodes[id].cpuset))
				Expect(n.Distance()).To(Equal(chk.nodes.nodes[id].distance))
			}
		},

		Entry("with sample sysfs "+testSystems[0].tarball, 0),
		Entry("with sample sysfs "+testSystems[1].tarball, 1),
		Entry("with sample sysfs "+testSystems[2].tarball, 2),
	)

	DescribeTable("correctly detect each CPU",
		func(sampleIdx int) {
			var (
				root = filepath.Join(testRoot, strconv.Itoa(sampleIdx))
				fs   sysfs.SysFS
				sys  sysfs.System
				err  error
			)

			fs, err = sysfs.Get(sysfs.WithSysRoot(root))
			Expect(err).To(BeNil())
			Expect(fs).ToNot(BeNil())

			sys, err = fs.GetSystem()
			Expect(err).To(BeNil())
			Expect(sys).ToNot(BeNil())

			chk := testSystems[sampleIdx]
			cpus := sys.CPUs()

			for _, id := range cpus.Online().List() {
				c := cpus.CPU(id)
				Expect(c).ToNot(BeNil())
				Expect(c.ID()).To(Equal(chk.cpus.cpus[id].id))
				Expect(c.PackageID()).To(Equal(chk.cpus.cpus[id].packageID))
				Expect(c.DieID()).To(Equal(chk.cpus.cpus[id].dieID))
				Expect(c.ClusterID()).To(Equal(chk.cpus.cpus[id].clusterID))
				Expect(c.CoreID()).To(Equal(chk.cpus.cpus[id].coreID))
				Expect(c.PackageCPUs().String()).To(Equal(chk.cpus.cpus[id].packageCPUs))
				Expect(c.DieCPUs().String()).To(Equal(chk.cpus.cpus[id].dieCPUs))
				Expect(c.CoreSiblings().String()).To(Equal(chk.cpus.cpus[id].coreSiblings))
				Expect(c.ThreadSiblings().String()).To(Equal(chk.cpus.cpus[id].threadSiblings))
			}
		},

		Entry("with sample sysfs "+testSystems[0].tarball, 0),
		Entry("with sample sysfs "+testSystems[1].tarball, 1),
		Entry("with sample sysfs "+testSystems[2].tarball, 2),
	)
})

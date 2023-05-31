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

var _ = Describe("SysFS should", func() {
	DescribeTable("read correct data content",
		func(sampleIdx int) {
			var (
				root    = filepath.Join(testRoot, strconv.Itoa(sampleIdx))
				fs      sysfs.SysFS
				content []byte
				err     error
			)

			fs, err = sysfs.Get(sysfs.WithSysRoot(root))
			Expect(err).To(BeNil())
			Expect(fs).ToNot(BeNil())

			for file, expected := range testContent[sampleIdx].content {
				content, err = fs.ReadFile(file)
				Expect(err).To(BeNil())
				Expect(content).Should(Equal([]byte(expected)))
			}
		},

		Entry("with sample sysfs "+testContent[0].tarball, 0),
		Entry("with sample sysfs "+testContent[1].tarball, 1),
		Entry("with sample sysfs "+testContent[2].tarball, 2),
	)

	DescribeTable("correctly resolve symlinks",
		func(sampleIdx int) {
			var (
				root = filepath.Join(testRoot, strconv.Itoa(sampleIdx))
				fs   sysfs.SysFS
				err  error
			)

			fs, err = sysfs.Get(sysfs.WithSysRoot(root))
			Expect(err).To(BeNil())
			Expect(fs).ToNot(BeNil())

			for src, chk := range testContent[sampleIdx].symlinks {
				dst, err := fs.EvalSymlinks(src)
				Expect(err).To(BeNil())
				Expect(dst).To(Equal(chk))
			}
		},

		Entry("with sample sysfs "+testContent[0].tarball, 0),
		Entry("with sample sysfs "+testContent[1].tarball, 1),
		Entry("with sample sysfs "+testContent[2].tarball, 2),
	)
})

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
	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

func TestSysfs(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "Sysfs Suite")
}

var _ = BeforeSuite(func() {
	testRoot = GinkgoT().TempDir()
	for idx, content := range testContent {
		dir := filepath.Join(testRoot, strconv.Itoa(idx))
		GinkgoT().Logf("extracting test content #%d (%s)...", idx, content.tarball)
		Expect(ExtractTarArchive(content.tarball, dir)).To(Succeed())
	}
})

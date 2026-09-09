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

package resmgr

import (
	"os"
	"path/filepath"
	"testing"
	"testing/fstest"

	"github.com/containers/nri-plugins/pkg/lib/hardware"
	"github.com/containers/nri-plugins/pkg/resmgr/cache"
	"github.com/containers/nri-plugins/pkg/resmgr/policy"
)

// testBackend is a policy backend which only has a name. Embedding the
// interface leaves the rest nil, which is fine as long as nothing calls them.
type testBackend struct {
	policy.Backend
	name string
}

func (b testBackend) Name() string { return b.name }

// oneCpuMachine is the smallest machine discovery accepts: one
// single-threaded CPU in one package, in one node with memory.
func oneCpuMachine(t *testing.T) *hardware.Machine {
	t.Helper()

	file := func(s string) *fstest.MapFile { return &fstest.MapFile{Data: []byte(s)} }
	m, err := hardware.Discover(hardware.WithFS(fstest.MapFS{
		"proc/meminfo":                                             file("MemTotal: 1048576 kB\n"),
		"sys/devices/system/cpu/online":                            file("0\n"),
		"sys/devices/system/cpu/present":                           file("0\n"),
		"sys/devices/system/cpu/possible":                          file("0\n"),
		"sys/devices/system/cpu/cpu0/topology/physical_package_id": file("0\n"),
		"sys/devices/system/cpu/cpu0/topology/core_id":             file("0\n"),
		"sys/devices/system/cpu/cpu0/topology/core_cpus_list":      file("0\n"),
		"sys/devices/system/node/has_normal_memory":                file("0\n"),
		"sys/devices/system/node/node0/cpulist":                    file("0\n"),
		"sys/devices/system/node/node0/meminfo":                    file("Node 0 MemTotal: 1048576 kB\n"),
		"sys/devices/system/node/node0/distance":                   file("10\n"),
	}))
	if err != nil {
		t.Fatalf("failed to discover the test machine: %v", err)
	}
	return m
}

// TestSetupPolicyResetsOnSwitch verifies that setting up a policy after a
// restart clears the data of another policy, but keeps the data of the same
// one, for the policy to decide about.
func TestSetupPolicyResetsOnSwitch(t *testing.T) {
	for _, tc := range []struct {
		name   string
		policy string
		kept   bool
	}{
		{name: "same policy", policy: "test", kept: true},
		{name: "policy switch", policy: "other", kept: false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			// The cache refuses a directory others may write to, which is
			// what a temporary directory is here.
			dir := filepath.Join(t.TempDir(), "cache")
			if err := os.Mkdir(dir, 0700); err != nil {
				t.Fatalf("failed to create cache directory: %v", err)
			}

			cch, err := cache.NewCache(cache.Options{CacheDir: dir})
			if err != nil {
				t.Fatalf("failed to create cache: %v", err)
			}
			if err := cch.SetActivePolicy("test"); err != nil {
				t.Fatalf("failed to set active policy: %v", err)
			}
			cch.SetPolicyEntry("key", "value")
			if err := cch.Save(); err != nil {
				t.Fatalf("failed to save cache: %v", err)
			}

			cch, err = cache.NewCache(cache.Options{CacheDir: dir})
			if err != nil {
				t.Fatalf("failed to restore cache: %v", err)
			}
			m := &resmgr{cache: cch, machine: oneCpuMachine(t)}
			if err := m.setupPolicy(testBackend{name: tc.policy}); err != nil {
				t.Fatalf("failed to set up policy: %v", err)
			}

			var value string
			if kept := m.cache.GetPolicyEntry("key", &value); kept != tc.kept {
				t.Errorf("policy entry kept: got %v, want %v", kept, tc.kept)
			}
			if got := m.cache.GetActivePolicy(); got != tc.policy {
				t.Errorf("active policy: got %q, want %q", got, tc.policy)
			}
		})
	}
}

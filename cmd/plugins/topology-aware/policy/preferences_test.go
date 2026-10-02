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

package topologyaware

import (
	"fmt"
	"testing"

	nri "github.com/containerd/nri/pkg/api"
	"github.com/stretchr/testify/require"

	cfgapi "github.com/containers/nri-plugins/pkg/apis/config/v1alpha1/resmgr/policy/topologyaware"
	"github.com/containers/nri-plugins/pkg/lib/hardware"
	"github.com/containers/nri-plugins/pkg/resmgr/cache"
	policyapi "github.com/containers/nri-plugins/pkg/resmgr/policy"
)

type machineFn func(*testing.T) *hardware.Machine

func setupTest(t *testing.T, mach machineFn, cfg *cfgapi.Config) *policy {
	t.Helper()

	cch, err := cache.NewCache(cache.Options{CacheDir: t.TempDir()})
	require.NoError(t, err, "failed to create test cache")

	opts := &policyapi.BackendOptions{
		Machine: mach(t),
		Cache:   cch,
		SendEvent: func(e any) error {
			return fmt.Errorf("test policy does not handle sending %v", e)
		},
		Config: cfg,
	}

	p := New()
	require.NoError(t, p.Setup(opts), "failed to set up test policy")

	return p.(*policy)
}

type testPodOption func(*nri.PodSandbox) error

func WithPodId(id string) testPodOption {
	return func(pod *nri.PodSandbox) error {
		pod.Id = id
		return nil
	}
}

func WithNamespace(ns string) testPodOption {
	return func(pod *nri.PodSandbox) error {
		if ns == "" {
			ns = "default"
		}
		pod.Namespace = ns
		return nil
	}
}

func WithQoSClass(cpuReq, cpuLim, memReq, memLim int) testPodOption {
	return func(pod *nri.PodSandbox) error {
		if pod.Linux == nil {
			pod.Linux = &nri.LinuxPodSandbox{}
		}
		qos := ""
		switch {
		case cpuReq == 0 && cpuLim == 0 && memReq == 0 && memLim == 0:
			qos = "besteffort"
		case cpuReq == cpuLim && memReq == memLim:
			qos = "guaranteed"
		default:
			qos = "burstable"
		}
		pod.Linux.CgroupParent = "foo/" + qos + "/bar"
		return nil
	}
}

func WithPodAnnotations(annotations map[string]string) testPodOption {
	return func(pod *nri.PodSandbox) error {
		pod.Annotations = annotations
		return nil
	}
}

var (
	nextPodId int = 1
	nextCtrId int = 1
)

func createTestPod(t *testing.T, cch cache.Cache, options ...testPodOption) cache.Pod {
	t.Helper()

	nriPod := &nri.PodSandbox{}
	for _, o := range options {
		require.NoError(t, o(nriPod), "failed to apply pod option")
	}

	if nriPod.Id == "" {
		nriPod.Id = fmt.Sprintf("pod%d", nextPodId)
		nextPodId++
	}

	if nriPod.Name == "" {
		nriPod.Name = nriPod.Id
	}
	cch.InsertPod(nriPod, nil)

	pod, ok := cch.LookupPod(nriPod.Id)
	require.True(t, ok, "failed to look up test pod in cache")

	return pod
}

type testContainerOption func(*nri.Container) error

func WithContainerId(id string) testContainerOption {
	return func(ctr *nri.Container) error {
		ctr.Id = id
		return nil
	}
}

func WithContainerPod(pod cache.Pod) testContainerOption {
	return func(ctr *nri.Container) error {
		ctr.PodSandboxId = pod.GetID()
		return nil
	}
}

func WithContainerName(name string) testContainerOption {
	return func(ctr *nri.Container) error {
		ctr.Name = name
		return nil
	}
}

func WithContainerResources(cpuReq, cpuLim, memReq, memLim int) testContainerOption {
	return func(ctr *nri.Container) error {
		if ctr.Linux == nil {
			ctr.Linux = &nri.LinuxContainer{}
		}
		if ctr.Linux.Resources == nil {
			ctr.Linux.Resources = &nri.LinuxResources{}
		}

		shares := cache.MilliCPUToShares(int64(cpuReq))
		quota, period := cache.MilliCPUToQuota(int64(cpuLim))

		ctr.Linux.Resources.Cpu = &nri.LinuxCPU{
			Shares: nri.UInt64(shares),
			Quota:  nri.Int64(quota),
			Period: nri.UInt64(period),
		}
		ctr.Linux.Resources.Memory = &nri.LinuxMemory{
			//Request: nri.Int64(memReq),
			Limit: nri.Int64(memLim),
		}

		return nil
	}
}

func createTestContainer(t *testing.T, cch cache.Cache, options ...testContainerOption) cache.Container {
	t.Helper()

	nriCtr := &nri.Container{}
	for _, o := range options {
		require.NoError(t, o(nriCtr), fmt.Errorf("failed to apply container option"))
	}

	if nriCtr.Id == "" {
		nriCtr.Id = fmt.Sprintf("ctr%d", nextCtrId)
		nextCtrId++
	}

	if nriCtr.Name == "" {
		nriCtr.Name = nriCtr.Id
	}

	ctr, err := cch.InsertContainer(nriCtr)
	require.NoError(t, err, "failed to insert test container into cache")

	return ctr

}

func TestGetContainerPreferences(t *testing.T) {
	boolPtr := func(v bool) *bool { return &v }

	type testCase struct {
		name           string
		namespace      string
		cpuReq, cpuLim int
		memReq, memLim int
		annotations    map[string]string
		expect         *Preferences
		expectError    bool
	}

	type testBatch struct {
		name  string
		cfg   *cfgapi.Config
		tests []*testCase
	}

	batches := []*testBatch{
		{
			name: "typical default config",
			cfg: &cfgapi.Config{
				ReservedResources: cfgapi.Constraints{
					"cpu": "750m",
				},
			},
			tests: []*testCase{
				{
					name:   "unannotated best-effort container",
					expect: &Preferences{},
				},
				{
					name:   "unannotated burstable container",
					cpuReq: 100,
					cpuLim: 200,
					memReq: 100,
					memLim: 200,
					expect: &Preferences{
						CpuRequest:    100,
						CpuLimit:      200,
						MemoryRequest: 0,
						MemoryLimit:   200,
						SharedCpu:     100,
					},
				},
				{
					name:   "unannotated guaranteed container",
					cpuReq: 100,
					cpuLim: 100,
					memReq: 200,
					memLim: 200,
					expect: &Preferences{
						CpuRequest:    100,
						CpuLimit:      100,
						MemoryRequest: 200,
						MemoryLimit:   200,
						SharedCpu:     100,
					},
				},
				{
					name:   "unannotated guaranteed exclusive container",
					cpuReq: 1000,
					cpuLim: 1000,
					memReq: 200,
					memLim: 200,
					expect: &Preferences{
						CpuRequest:    1000,
						CpuLimit:      1000,
						MemoryRequest: 200,
						MemoryLimit:   200,
						ExclusiveCpu:  1,
						IsolatedCpus:  Implied(BoolPreference(true)),
					},
				},
				{
					name:      "unannotated best-effort reserved container",
					namespace: "kube-system",
					expect: &Preferences{
						ReservedCpus: Implied(BoolPreference(true)),
					},
				},
				{
					name:      "unannotated burstable reserved container",
					namespace: "kube-system",
					cpuReq:    100,
					cpuLim:    200,
					memReq:    100,
					memLim:    200,
					expect: &Preferences{
						ReservedCpus: Implied(BoolPreference(true)),
						CpuRequest:   100,
						CpuLimit:     200,
						MemoryLimit:  200,
						SharedCpu:    100,
					},
				},
				{
					name:      "unannotated guaranteed reserved container",
					namespace: "kube-system",
					cpuReq:    100,
					cpuLim:    100,
					memReq:    200,
					memLim:    200,
					expect: &Preferences{
						ReservedCpus:  Implied(BoolPreference(true)),
						CpuRequest:    100,
						CpuLimit:      100,
						MemoryRequest: 200,
						MemoryLimit:   200,
						SharedCpu:     100,
					},
				},
				{
					name:      "unannotated guaranteed reserved container",
					namespace: "kube-system",
					cpuReq:    1000,
					cpuLim:    1000,
					memReq:    200,
					memLim:    200,
					expect: &Preferences{
						CpuRequest:    1000,
						CpuLimit:      1000,
						MemoryRequest: 200,
						MemoryLimit:   200,
						SharedCpu:     1000,
						ReservedCpus:  Implied(BoolPreference(true)),
					},
				},
			},
		},
		{
			name: "policy configuration with shared CPUs by default",
			cfg: &cfgapi.Config{
				ReservedResources: cfgapi.Constraints{
					"cpu": "750m",
				},
				PreferShared: boolPtr(true),
			},
			tests: []*testCase{
				{
					name:   "unannotated best-effort container",
					expect: &Preferences{},
				},
				{
					name:   "unannotated burstable container",
					cpuReq: 100,
					cpuLim: 200,
					memReq: 100,
					memLim: 200,
					expect: &Preferences{
						CpuRequest:    100,
						CpuLimit:      200,
						MemoryRequest: 0,
						MemoryLimit:   200,
						SharedCpu:     100,
					},
				},
				{
					name:   "unannotated guaranteed container",
					cpuReq: 100,
					cpuLim: 100,
					memReq: 200,
					memLim: 200,
					expect: &Preferences{
						CpuRequest:    100,
						CpuLimit:      100,
						MemoryRequest: 200,
						MemoryLimit:   200,
						SharedCpu:     100,
					},
				},
				{
					name:   "unannotated guaranteed exclusive container",
					cpuReq: 1000,
					cpuLim: 1000,
					memReq: 200,
					memLim: 200,
					expect: &Preferences{
						CpuRequest:    1000,
						CpuLimit:      1000,
						MemoryRequest: 200,
						MemoryLimit:   200,
						SharedCpu:     1000,
					},
				},
				{
					name:      "unannotated best-effort reserved container",
					namespace: "kube-system",
					expect: &Preferences{
						ReservedCpus: Implied(BoolPreference(true)),
					},
				},
				{
					name:      "unannotated burstable reserved container",
					namespace: "kube-system",
					cpuReq:    100,
					cpuLim:    200,
					memReq:    100,
					memLim:    200,
					expect: &Preferences{
						ReservedCpus: Implied(BoolPreference(true)),
						CpuRequest:   100,
						CpuLimit:     200,
						MemoryLimit:  200,
						SharedCpu:    100,
					},
				},
				{
					name:      "unannotated guaranteed reserved container",
					namespace: "kube-system",
					cpuReq:    100,
					cpuLim:    100,
					memReq:    200,
					memLim:    200,
					expect: &Preferences{
						ReservedCpus:  Implied(BoolPreference(true)),
						CpuRequest:    100,
						CpuLimit:      100,
						MemoryRequest: 200,
						MemoryLimit:   200,
						SharedCpu:     100,
					},
				},
				{
					name:      "unannotated guaranteed reserved container",
					namespace: "kube-system",
					cpuReq:    1000,
					cpuLim:    1000,
					memReq:    200,
					memLim:    200,
					expect: &Preferences{
						CpuRequest:    1000,
						CpuLimit:      1000,
						MemoryRequest: 200,
						MemoryLimit:   200,
						SharedCpu:     1000,
						ReservedCpus:  Implied(BoolPreference(true)),
					},
				},
			},
		},
	}

	for _, tb := range batches {
		p := setupTest(t, twoSocketMachine, tb.cfg)
		for _, tc := range tb.tests {
			t.Run(tb.name+"/"+tc.name, func(t *testing.T) {
				pod := createTestPod(t, p.cache,
					WithNamespace(tc.namespace),
					WithQoSClass(tc.cpuReq, tc.cpuLim, tc.memReq, tc.memLim),
					WithPodAnnotations(tc.annotations),
				)
				ctr := createTestContainer(t, p.cache,
					WithContainerPod(pod),
					WithContainerResources(tc.cpuReq, tc.cpuLim, tc.memReq, tc.memLim),
				)

				prefs, err := p.GetContainerPreferences(ctr)
				if tc.expectError {
					require.Error(t, err, "expected error but got none")
				} else {
					require.NoError(t, err, "unexpected error")
					require.Equal(t, tc.expect, prefs, "unexpected preferences")
				}
			})
		}
	}
}

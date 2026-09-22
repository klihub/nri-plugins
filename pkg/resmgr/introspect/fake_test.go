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

package introspect

import (
	v1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"

	nri "github.com/containerd/nri/pkg/api"
	"github.com/containers/nri-plugins/pkg/topology"
)

type fakePod struct {
	id, uid, name, namespace string
	qos                      v1.PodQOSClass
}

func (p *fakePod) GetID() string               { return p.id }
func (p *fakePod) GetUID() string              { return p.uid }
func (p *fakePod) GetName() string             { return p.name }
func (p *fakePod) GetNamespace() string        { return p.namespace }
func (p *fakePod) GetQOSClass() v1.PodQOSClass { return p.qos }

type fakeContainer struct {
	id, podID, name string
	state           nri.ContainerState
	qos             v1.PodQOSClass
	requirements    v1.ResourceRequirements
	cpusetCpus      string
	cpusetMems      string
	cpuShares       int64
	cpuQuota        int64
	cpuPeriod       int64
	memoryLimit     int64
	rdtClass        string
	blockIOClass    string
	hints           topology.Hints
}

func (c *fakeContainer) GetID() string                { return c.id }
func (c *fakeContainer) GetPodID() string             { return c.podID }
func (c *fakeContainer) GetName() string              { return c.name }
func (c *fakeContainer) GetState() nri.ContainerState { return c.state }
func (c *fakeContainer) GetQOSClass() v1.PodQOSClass  { return c.qos }
func (c *fakeContainer) GetResourceRequirements() v1.ResourceRequirements {
	return c.requirements
}
func (c *fakeContainer) GetCpusetCpus() string            { return c.cpusetCpus }
func (c *fakeContainer) GetCpusetMems() string            { return c.cpusetMems }
func (c *fakeContainer) GetCPUShares() int64              { return c.cpuShares }
func (c *fakeContainer) GetCPUQuota() int64               { return c.cpuQuota }
func (c *fakeContainer) GetCPUPeriod() int64              { return c.cpuPeriod }
func (c *fakeContainer) GetMemoryLimit() int64            { return c.memoryLimit }
func (c *fakeContainer) GetRDTClass() string              { return c.rdtClass }
func (c *fakeContainer) GetBlockIOClass() string          { return c.blockIOClass }
func (c *fakeContainer) GetTopologyHints() topology.Hints { return c.hints }

type fakeCache struct {
	pods       []Pod
	containers []Container
}

func (c *fakeCache) GetPods() []Pod             { return c.pods }
func (c *fakeCache) GetContainers() []Container { return c.containers }

// requirements builds a ResourceRequirements the way Kubernetes would.
func requirements(cpuReq, cpuLim string, memReq, memLim string) v1.ResourceRequirements {
	r := v1.ResourceRequirements{
		Requests: v1.ResourceList{},
		Limits:   v1.ResourceList{},
	}
	if cpuReq != "" {
		r.Requests[v1.ResourceCPU] = resource.MustParse(cpuReq)
	}
	if cpuLim != "" {
		r.Limits[v1.ResourceCPU] = resource.MustParse(cpuLim)
	}
	if memReq != "" {
		r.Requests[v1.ResourceMemory] = resource.MustParse(memReq)
	}
	if memLim != "" {
		r.Limits[v1.ResourceMemory] = resource.MustParse(memLim)
	}
	return r
}

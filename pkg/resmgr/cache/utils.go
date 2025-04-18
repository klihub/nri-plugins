// Copyright 2019 Intel Corporation. All Rights Reserved.
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

package cache

import (
	"os"
	"path"
	"strings"

	nri "github.com/containerd/nri/pkg/api"
	corev1 "k8s.io/api/core/v1"
	resapi "k8s.io/apimachinery/pkg/api/resource"

	"github.com/containers/nri-plugins/pkg/cgroups"
	"github.com/containers/nri-plugins/pkg/kubernetes"
)

var (
	SharesToMilliCPU = kubernetes.SharesToMilliCPU
	QuotaToMilliCPU  = kubernetes.QuotaToMilliCPU
	MilliCPUToShares = kubernetes.MilliCPUToShares
	MilliCPUToQuota  = kubernetes.MilliCPUToQuota
	OomAdjToMemReq   = kubernetes.OomAdjToMemReq
)

// Try to estimate CRI resource requirements from NRI resources.
func estimateResourceRequirements(r *nri.LinuxResources, qosClass corev1.PodQOSClass, oomAdj int64) corev1.ResourceRequirements {
	resources := corev1.ResourceRequirements{
		Requests: corev1.ResourceList{},
		Limits:   corev1.ResourceList{},
	}

	cpu := r.GetCpu()
	shares := int64(cpu.GetShares().GetValue())

	// calculate CPU request
	if value := SharesToMilliCPU(shares); value > 0 {
		qty := resapi.NewMilliQuantity(value, resapi.DecimalSI)
		resources.Requests[corev1.ResourceCPU] = *qty
	}

	// get memory limit
	memLimit := int64(0)
	if memLimit = r.GetMemory().GetLimit().GetValue(); memLimit > 0 {
		qty := resapi.NewQuantity(memLimit, resapi.DecimalSI)
		resources.Limits[corev1.ResourceMemory] = *qty
	}

	// calculate CPU limit, set memory request if known
	switch qosClass {
	case corev1.PodQOSGuaranteed:
		resources.Limits[corev1.ResourceCPU] = resources.Requests[corev1.ResourceCPU]
		resources.Requests[corev1.ResourceMemory] = resources.Limits[corev1.ResourceMemory]
	case corev1.PodQOSBurstable:
		if req := OomAdjToMemReq(oomAdj, memLimit); req != nil && *req != 0 {
			log.Info("estimated memory request: %d (%.2fM)", *req, float64(*req)/(1024*1024))
			qty := resapi.NewQuantity(*req, resapi.DecimalSI)
			resources.Requests[corev1.ResourceMemory] = *qty
		}
		fallthrough
	case corev1.PodQOSBestEffort:
		quota := cpu.GetQuota().GetValue()
		period := int64(cpu.GetPeriod().GetValue())
		if value := QuotaToMilliCPU(quota, period); value > 0 {
			qty := resapi.NewMilliQuantity(value, resapi.DecimalSI)
			resources.Limits[corev1.ResourceCPU] = *qty
		}
	}

	return resources
}

// IsPodQOSClassName returns true if the given class is one of the Pod QOS classes.
func IsPodQOSClassName(class string) bool {
	switch corev1.PodQOSClass(class) {
	case corev1.PodQOSBestEffort, corev1.PodQOSBurstable, corev1.PodQOSGuaranteed:
		return true
	}
	return false
}

// findContainerDir brute-force searches for a container cgroup dir.
func findContainerDir(podCgroupDir, podID, ID string) string {
	var dirs []string

	if podCgroupDir == "" {
		return ""
	}

	cpusetDir := cgroups.Cpuset.Path()

	dirs = []string{
		path.Join(cpusetDir, podCgroupDir, ID),
		// containerd, systemd
		path.Join(cpusetDir, podCgroupDir, "cri-containerd-"+ID+".scope"),
		// containerd, cgroupfs
		path.Join(cpusetDir, podCgroupDir, "cri-containerd-"+ID),
		// crio, systemd
		path.Join(cpusetDir, podCgroupDir, "crio-"+ID+".scope"),
		// crio, cgroupfs
		path.Join(cpusetDir, podCgroupDir, "crio-"+ID),
	}

	for _, dir := range dirs {
		if info, err := os.Stat(dir); err == nil {
			if info.Mode().IsDir() {
				return strings.TrimPrefix(dir, cpusetDir)
			}
		}
	}

	return ""
}

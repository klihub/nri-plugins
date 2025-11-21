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

package policy

import (
	"context"
	"strconv"

	"github.com/prometheus/client_golang/prometheus"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"
	v1 "k8s.io/api/core/v1"

	"github.com/containers/nri-plugins/pkg/metrics"
	"github.com/containers/nri-plugins/pkg/resmgr/cache"
	system "github.com/containers/nri-plugins/pkg/sysfs"
	"github.com/containers/nri-plugins/pkg/utils/cpuset"
)

const (
	nodeCapacity = iota
	nodeUsage
	nodeContainers
	cpuAllocation
	cpuContainers
	metricsCount
)

type (
	SystemCollector struct {
		cache   cache.Cache
		system  system.System
		Nodes   map[int]*NodeMetric
		Cpus    map[int]*CpuMetric
		Metrics []*prometheus.GaugeVec
		Meters  []metric.Float64Gauge
	}
	NodeMetric struct {
		Id             int
		IdLabel        string
		Type           string
		Capacity       int64
		Usage          int64
		ContainerCount int
	}
	CpuMetric struct {
		Id             int
		IdLabel        string
		Allocation     int
		ContainerCount int
	}
)

func (p *policy) newSystemCollector() *SystemCollector {
	s := &SystemCollector{
		cache:   p.cache,
		system:  p.system,
		Nodes:   map[int]*NodeMetric{},
		Cpus:    map[int]*CpuMetric{},
		Metrics: make([]*prometheus.GaugeVec, metricsCount),
		Meters:  make([]metric.Float64Gauge, metricsCount),
	}

	meter := metrics.Provider("policy").Meter("system", metrics.WithOmitSubsystem())

	s.Metrics[nodeCapacity] = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Name: "mem_node_capacity",
			Help: "Capacity of the memory node.",
		},
		[]string{
			"node_id",
		},
	)

	m, err := meter.Float64Gauge(
		"mem.node.capacity",
		metric.WithDescription("Capacity of the memory node."),
		metric.WithUnit("bytes"),
	)
	if err != nil {
		log.Errorf("failed to create mem_node_capacity meter: %v", err)
		panic(err)
	} else {
		s.Meters[nodeCapacity] = m
	}

	s.Metrics[nodeUsage] = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Name: "mem_node_usage",
			Help: "Usage of the memory node",
		},
		[]string{
			"node_id",
		},
	)

	m, err = meter.Float64Gauge(
		"mem.node.usage",
		metric.WithDescription("Usage of the memory node."),
		metric.WithUnit("bytes"),
	)
	if err != nil {
		log.Errorf("failed to create mem_node_usage meter: %v", err)
		panic(err)
	} else {
		s.Meters[nodeUsage] = m
	}

	s.Metrics[nodeContainers] = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Name: "mem_node_container_count",
			Help: "Number of containers assigned to the memory node.",
		},
		[]string{
			"node_id",
		},
	)

	m, err = meter.Float64Gauge(
		"mem.node.container.count",
		metric.WithDescription("Number of containers assigned to the memory node."),
	)
	if err != nil {
		log.Errorf("failed to create mem_node_container_count meter: %v", err)
	} else {
		s.Meters[nodeContainers] = m
	}

	s.Metrics[cpuAllocation] = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Name: "cpu_allocation",
			Help: "Total allocation of the CPU.",
		},
		[]string{
			"cpu_id",
		},
	)

	m, err = meter.Float64Gauge(
		"cpu.allocation",
		metric.WithDescription("Total allocation of the CPU."),
		metric.WithUnit("milli-cores"),
	)
	if err != nil {
		log.Errorf("failed to create cpu_allocation meter: %v", err)
	} else {
		s.Meters[cpuAllocation] = m
	}

	s.Metrics[cpuContainers] = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Name: "cpu_container_count",
			Help: "Number of containers assigned to the CPU.",
		},
		[]string{
			"cpu_id",
		},
	)

	m, err = meter.Float64Gauge(
		"cpu.container.count",
		metric.WithDescription("Number of containers assigned to the CPU."),
	)
	if err != nil {
		log.Errorf("failed to create cpu_container_count meter: %v", err)
	} else {
		s.Meters[cpuContainers] = m
	}

	for _, id := range s.system.NodeIDs() {
		var (
			sys        = s.system.Node(id)
			capa, used = s.getMemInfo(sys)
			node       = &NodeMetric{
				Id:       sys.ID(),
				IdLabel:  strconv.Itoa(sys.ID()),
				Type:     sys.GetMemoryType().String(),
				Capacity: capa,
				Usage:    used,
			}
		)
		s.Nodes[id] = node

		s.Metrics[nodeCapacity].WithLabelValues(node.IdLabel).Set(float64(node.Capacity))
		s.Meters[nodeCapacity].Record(
			context.Background(),
			float64(node.Capacity),
			metric.WithAttributes(
				attribute.String("node_id", node.IdLabel),
				attribute.String("node_type", node.Type),
			),
		)
	}

	for _, id := range s.system.CPUIDs() {
		cpu := &CpuMetric{
			Id:      id,
			IdLabel: strconv.Itoa(id),
		}
		s.Cpus[id] = cpu
	}

	s.Update()

	return s
}

func (s *SystemCollector) Update() {
	if s == nil {
		return
	}

	for _, n := range s.Nodes {
		sys := s.system.Node(n.Id)
		_, used := s.getMemInfo(sys)
		n.Usage = used
		n.ContainerCount = 0
	}

	for _, c := range s.Cpus {
		c.ContainerCount = 0
		c.Allocation = 0
	}

	for _, ctr := range s.cache.GetContainers() {
		switch ctr.GetState() {
		case cache.ContainerStateCreated:
		case cache.ContainerStateRunning:
		default:
			continue
		}

		var (
			cpu, mem = s.getCpuAndMemset(ctr)
			req, _   = s.getCpuResources(ctr)
		)

		for _, id := range mem.List() {
			if n, ok := s.Nodes[id]; ok {
				n.ContainerCount++
			}
		}

		for _, id := range cpu.List() {
			if c, ok := s.Cpus[id]; ok {
				c.ContainerCount++
				if cpu.Size() > 0 {
					c.Allocation += req / cpu.Size()
				}
			}
		}
	}

	for _, n := range s.Nodes {
		s.Metrics[nodeUsage].WithLabelValues(n.IdLabel).Set(float64(n.Usage))
		s.Meters[nodeUsage].Record(
			context.Background(),
			float64(n.Usage),
			metric.WithAttributes(
				attribute.String("node_id", n.IdLabel),
			),
		)
		s.Meters[nodeContainers].Record(
			context.Background(),
			float64(n.ContainerCount),
			metric.WithAttributes(
				attribute.String("node_id", n.IdLabel),
			),
		)
	}
	for _, c := range s.Cpus {
		s.Metrics[cpuAllocation].WithLabelValues(c.IdLabel).Set(float64(c.Allocation))
		s.Meters[cpuAllocation].Record(
			context.Background(),
			float64(c.Allocation),
			metric.WithAttributes(
				attribute.String("cpu_id", c.IdLabel),
			),
		)
		s.Metrics[cpuContainers].WithLabelValues(c.IdLabel).Set(float64(c.ContainerCount))
		s.Meters[cpuContainers].Record(
			context.Background(),
			float64(c.ContainerCount),
			metric.WithAttributes(
				attribute.String("cpu_id", c.IdLabel),
			),
		)
	}
}

func (s *SystemCollector) getMemInfo(n system.Node) (capacity, used int64) {
	if n != nil {
		if i, _ := n.MemoryInfo(); i != nil {
			return int64(i.MemTotal), int64(i.MemUsed)
		}
	}
	return 0, 0
}

func (s *SystemCollector) getCpuAndMemset(ctr cache.Container) (cpu, mem cpuset.CPUSet) {
	cset, _ := cpuset.Parse(ctr.GetCpusetCpus())
	mset, _ := cpuset.Parse(ctr.GetCpusetMems())
	return cset, mset
}

func (s *SystemCollector) getCpuResources(ctr cache.Container) (request, limit int) {
	res := ctr.GetResourceRequirements()
	if qty, ok := res.Requests[v1.ResourceCPU]; ok {
		request = int(qty.MilliValue())
	}
	if qty, ok := res.Limits[v1.ResourceCPU]; ok {
		limit = int(qty.MilliValue())
	}

	return request, limit
}

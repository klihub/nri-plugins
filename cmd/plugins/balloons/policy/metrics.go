// Copyright 2022 Intel Corporation. All Rights Reserved.
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

package balloons

import (
	"context"
	"sort"
	"strconv"
	"strings"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"

	"github.com/containers/nri-plugins/pkg/metrics"
	"github.com/containers/nri-plugins/pkg/resmgr/policy"
	"github.com/containers/nri-plugins/pkg/utils/cpuset"
	"github.com/prometheus/client_golang/prometheus"
)

// Prometheus Metric descriptor indices and descriptor table
const (
	balloonsDesc = iota
)

var descriptors = []*prometheus.Desc{
	balloonsDesc: prometheus.NewDesc(
		"balloons",
		"CPUs",
		[]string{
			"balloon_type",
			"cpu_class",
			"cpus_min",
			"cpus_max",
			"balloon",
			"groups",
			"cpus",
			"cpus_count",
			"numas",
			"numas_count",
			"dies",
			"dies_count",
			"packages",
			"packages_count",
			"sharedidlecpus",
			"sharedidlecpus_count",
			"cpus_allowed",
			"cpus_allowed_count",
			"mems",
			"containers",
			"tot_req_millicpu",
		}, nil,
	),
}

// Metrics defines the balloons-specific metrics from policy level.
type Metrics struct {
	p        *balloons
	Balloons []*BalloonMetrics
}

type Meters struct {
	p       *balloons
	meter   metric.Meter
	balloon metric.Int64ObservableGauge
	reg     metric.Registration
}

// BalloonMetrics define metrics of a balloon instance.
type BalloonMetrics struct {
	// Balloon type metrics
	DefName  string
	CpuClass string
	MinCpus  int
	MaxCpus  int
	// Balloon instance metrics
	PrettyName            string
	Groups                string
	Cpus                  cpuset.CPUSet
	CpusCount             int
	Numas                 []string
	NumasCount            int
	Dies                  []string
	DiesCount             int
	Packages              []string
	PackagesCount         int
	SharedIdleCpus        cpuset.CPUSet
	SharedIdleCpusCount   int
	CpusAllowed           cpuset.CPUSet
	CpusAllowedCount      int
	Mems                  string
	ContainerNames        string
	ContainerReqMilliCpus int
}

func (p *balloons) GetMetrics() policy.Metrics {
	policyMetrics := &Metrics{p: p}
	policyMetrics.Balloons = make([]*BalloonMetrics, len(p.balloons))
	for index, bln := range p.balloons {
		cpuLoc := p.cpuTree.CpuLocations(bln.Cpus)
		bm := &BalloonMetrics{}
		policyMetrics.Balloons[index] = bm
		bm.DefName = bln.Def.Name
		bm.CpuClass = bln.Def.CpuClass
		bm.MinCpus = bln.Def.MinCpus
		bm.MaxCpus = bln.Def.MaxCpus
		bm.PrettyName = bln.PrettyName()
		groups := []string{}
		for group, cCount := range bln.Groups {
			if cCount > 0 {
				groups = append(groups, group)
			}
		}
		sort.Strings(groups)
		bm.Groups = strings.Join(groups, ",")
		bm.Cpus = bln.Cpus
		bm.CpusCount = bm.Cpus.Size()
		if len(cpuLoc) > 3 {
			bm.Numas = cpuLoc[3]
			bm.NumasCount = len(bm.Numas)
			bm.Dies = cpuLoc[2]
			bm.DiesCount = len(bm.Dies)
			bm.Packages = cpuLoc[1]
			bm.PackagesCount = len(bm.Packages)
		}
		bm.SharedIdleCpus = bln.SharedIdleCpus
		bm.SharedIdleCpusCount = bm.SharedIdleCpus.Size()
		bm.CpusAllowed = bm.Cpus.Union(bm.SharedIdleCpus)
		bm.CpusAllowedCount = bm.CpusAllowed.Size()
		bm.Mems = bln.Mems.String()
		cNames := []string{}
		// Get container names and total requested milliCPUs.
		for _, containerIDs := range bln.PodIDs {
			for _, containerID := range containerIDs {
				if c, ok := p.cch.LookupContainer(containerID); ok {
					cNames = append(cNames, c.PrettyName())
					bm.ContainerReqMilliCpus += p.containerRequestedMilliCpus(containerID)
				}
			}
		}
		sort.Strings(cNames)
		bm.ContainerNames = strings.Join(cNames, ",")
	}

	return policyMetrics
}

func (b *balloons) NewMeters() {
	meter := metrics.Provider("policy").Meter("balloons", metrics.WithOmitSubsystem())

	m := &Meters{p: b}
	m.meter = meter

	var err error

	m.balloon, err = m.meter.Int64ObservableGauge(
		"balloons",
		metric.WithDescription("CPUs"),
		metric.WithUnit("cores"),
	)

	if err != nil {
		log.Errorf("failed to create balloons meter: %v", err)
		return
	}

	m.reg, err = m.meter.RegisterCallback(
		func(ctx context.Context, o metric.Observer) error {
			for _, bln := range b.balloons {
				select {
				case <-ctx.Done():
					log.Errorf("balloon metric collection cancelled: %v", ctx.Err())
				default:
					m.Observe(o, bln)
				}
			}
			return nil
		},
		m.balloon,
	)

	if err != nil {
		log.Errorf("failed to register balloons callback: %v", err)
	}

	b.meters = m
}

func (m *Meters) Observe(o metric.Observer, bln *Balloon) {
	var (
		defName               = bln.Def.Name
		prettyName            = bln.PrettyName()
		cpuClass              = bln.Def.CpuClass
		minCpus               = bln.Def.MinCpus
		maxCpus               = bln.Def.MaxCpus
		cpuLoc                = m.p.cpuTree.CpuLocations(bln.Cpus)
		cpus                  = bln.Cpus
		cpusCount             = cpus.Size()
		numas                 []string
		dies                  []string
		packages              []string
		numasCount            = 0
		diesCount             = 0
		packagesCount         = 0
		sharedIdleCpus        = bln.SharedIdleCpus
		sharedIdleCpusCount   = sharedIdleCpus.Size()
		cpusAllowed           = cpus.Union(sharedIdleCpus)
		cpusAllowedCount      = cpusAllowed.Size()
		mems                  = bln.Mems.String()
		containerNames        string
		containerReqMilliCpus = 0
		groups                strings.Builder
	)

	sep := ""
	for group, cCount := range bln.Groups {
		if cCount > 0 {
			groups.WriteString(sep)
			groups.WriteString(group)
			sep = ","
		}
	}

	if len(cpuLoc) > 3 {
		numas = cpuLoc[3]
		numasCount = len(numas)
		dies = cpuLoc[2]
		diesCount = len(dies)
		packages = cpuLoc[1]
		packagesCount = len(packages)
	}

	cNames := []string{}
	// Get container names and total requested milliCPUs.
	for _, containerIDs := range bln.PodIDs {
		for _, containerID := range containerIDs {
			if c, ok := m.p.cch.LookupContainer(containerID); ok {
				cNames = append(cNames, c.PrettyName())
				containerReqMilliCpus += m.p.containerRequestedMilliCpus(containerID)
			}
		}
	}
	sort.Strings(cNames)
	containerNames = strings.Join(cNames, ",")

	o.ObserveInt64(
		m.balloon,
		int64(cpus.Size()),
		metric.WithAttributes(
			attribute.String("balloon_type", defName),
			attribute.String("cpu_class", cpuClass),
			attribute.String("cpus_min", strconv.Itoa(minCpus)),
			attribute.String("cpus_max", strconv.Itoa(maxCpus)),
			attribute.String("balloon", prettyName),
			attribute.String("groups", groups.String()),
			attribute.String("cpus", cpus.String()),
			attribute.String("cpus_count", strconv.Itoa(cpusCount)),
			attribute.String("numas", strings.Join(numas, ",")),
			attribute.String("numas_count", strconv.Itoa(numasCount)),
			attribute.String("dies", strings.Join(dies, ",")),
			attribute.String("dies_count", strconv.Itoa(diesCount)),
			attribute.String("packages", strings.Join(packages, ",")),
			attribute.String("packages_count", strconv.Itoa(packagesCount)),
			attribute.String("sharedidlecpus", sharedIdleCpus.String()),
			attribute.String("sharedidlecpus_count", strconv.Itoa(sharedIdleCpusCount)),
			attribute.String("cpus_allowed", cpusAllowed.String()),
			attribute.String("cpus_allowed_count", strconv.Itoa(cpusAllowedCount)),
			attribute.String("mems", mems),
			attribute.String("containers", containerNames),
			attribute.String("tot_req_millicpu", strconv.Itoa(containerReqMilliCpus)),
		),
	)
}

func (m *Metrics) Describe(ch chan<- *prometheus.Desc) {
	for _, d := range descriptors {
		ch <- d
	}
}

func (m *Metrics) Collect(ch chan<- prometheus.Metric) {
	if m == nil {
		return
	}

	for _, bm := range m.Balloons {
		ch <- prometheus.MustNewConstMetric(
			descriptors[balloonsDesc],
			prometheus.GaugeValue,
			float64(bm.Cpus.Size()),
			bm.DefName,
			bm.CpuClass,
			strconv.Itoa(bm.MinCpus),
			strconv.Itoa(bm.MaxCpus),
			bm.PrettyName,
			bm.Groups,
			bm.Cpus.String(),
			strconv.Itoa(bm.CpusCount),
			strings.Join(bm.Numas, ","),
			strconv.Itoa(bm.NumasCount),
			strings.Join(bm.Dies, ","),
			strconv.Itoa(bm.DiesCount),
			strings.Join(bm.Packages, ","),
			strconv.Itoa(bm.PackagesCount),
			bm.SharedIdleCpus.String(),
			strconv.Itoa(bm.SharedIdleCpusCount),
			bm.CpusAllowed.String(),
			strconv.Itoa(bm.CpusAllowedCount),
			bm.Mems,
			bm.ContainerNames,
			strconv.Itoa(bm.ContainerReqMilliCpus))
	}
}

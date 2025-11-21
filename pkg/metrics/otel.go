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

package metrics

import (
	"context"
	"os"
	"path"
	"sync"
	"time"

	otlpt "github.com/prometheus/otlptranslator"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/exporters/prometheus"
	"go.opentelemetry.io/otel/exporters/stdout/stdoutmetric"
	otelmetric "go.opentelemetry.io/otel/metric"
	"go.opentelemetry.io/otel/metric/noop"
	"go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/resource"
	semconv "go.opentelemetry.io/otel/semconv/v1.37.0"

	"github.com/containers/nri-plugins/pkg/version"
)

var (
	provider *metric.MeterProvider
	exporter *prometheus.Exporter
	initOnce sync.Once
	enabled  []string
	nop      = noop.NewMeterProvider()
)

func Configure(enable []string) {
	enabled = enable
}

func setup() {
	resource, err := resource.Merge(
		resource.Default(),
		resource.NewWithAttributes(
			semconv.SchemaURL,
			semconv.ServiceName("nri-resource-policy"),
			semconv.ServiceVersion(version.Version),
		),
	)
	if err != nil {
		log.Errorf("failed to create OTEL resource: %v", err)
		return
	}

	prom, err := prometheus.New(
		prometheus.WithNamespace("nri"),
		prometheus.WithoutScopeInfo(),
		prometheus.WithTranslationStrategy(otlpt.UnderscoreEscapingWithoutSuffixes),
	)
	if err != nil {
		log.Errorf("failed to create OTEL prometheus exporter: %v", err)
		return
	}

	options := []metric.Option{
		metric.WithResource(resource),
		metric.WithReader(prom),
	}

	if period := os.Getenv("OTEL_STDOUT_METRICS"); period != "" {
		d, err := time.ParseDuration(period)
		if err != nil {
			log.Errorf("failed to parse OTEL_STDOUT_METRICS (%q) as time.Duration: %v",
				period, err)
		} else {
			stdo, err := stdoutmetric.New(
				stdoutmetric.WithPrettyPrint(),
				stdoutmetric.WithoutTimestamps(),
			)
			if err != nil {
				log.Errorf("failed to create OTEL stdout metric exporter: %v", err)
			} else {
				options = append(options,
					metric.WithReader(
						metric.NewPeriodicReader(stdo, metric.WithInterval(d)),
					),
				)
			}
		}
	}

	provider = metric.NewMeterProvider(options...)
	if provider != nil {
		otel.SetMeterProvider(provider)
	}

	exporter = prom
}

func shutdown() {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	wg := sync.WaitGroup{}
	wg.Go(func() {
		exporter.Shutdown(ctx)
	})
	wg.Go(func() {
		provider.Shutdown(ctx)
	})

	wg.Wait()
	cancel()
	provider = nil
	exporter = nil
	initOnce = sync.Once{}
}

type meterProvider struct {
	*metric.MeterProvider
	group string
}

type meterOption struct {
	otel  []otelmetric.MeterOption
	local func(*meter)
}

func WithOmitGroup() *meterOption {
	return &meterOption{
		local: func(m *meter) {
			m.omitGroup = true
		},
	}
}

func WithOmitSubsystem() *meterOption {
	return &meterOption{
		local: func(m *meter) {
			m.omitSubsys = true
		},
	}
}

func WithOtelMeterOptions(options ...otelmetric.MeterOption) *meterOption {
	return &meterOption{
		otel: options,
	}
}

func (mp *meterProvider) Shutdown() {
	if mp == nil {
		return
	}
	shutdown()
}

func (mp *meterProvider) Meter(subsys string, options ...*meterOption) otelmetric.Meter {
	var (
		otelopts []otelmetric.MeterOption
		m        = &meter{
			subsys: subsys,
			group:  mp.group,
		}
	)

	for _, opt := range options {
		if opt.local != nil {
			opt.local(m)
		}
		if opt.otel != nil {
			otelopts = append(otelopts, opt.otel...)
		}
	}

	if !m.isEnabled() {
		log.Infof("metric %s in group %s is disabled", m.subsys, m.group)
		m.Meter = nop.Meter(subsys, otelopts...)
	} else {
		log.Infof("metric %s in group %s is enabled", m.subsys, m.group)
		m.Meter = mp.MeterProvider.Meter(subsys, otelopts...)
	}

	return m
}

func Provider(group string) *meterProvider {
	initOnce.Do(setup)

	if provider == nil {
		log.Errorf("can't get metric provider, creation failed")
	}

	p := &meterProvider{
		MeterProvider: provider,
		group:         group,
	}

	return p
}

type meter struct {
	otelmetric.Meter
	group      string
	omitGroup  bool
	subsys     string
	omitSubsys bool
}

func (m *meter) isEnabled() bool {
	for _, glob := range enabled {
		if m.matches(glob) {
			return true
		}
	}
	return false
}

func (m *meter) matches(glob string) bool {
	if glob == m.group || glob == m.subsys {
		return true
	}
	name := m.group + "/" + m.subsys
	if glob == name {
		return true
	}

	ok, err := path.Match(glob, m.group)
	if err != nil {
		log.Warnf("invalid glob pattern %q: %v", glob, err)
		return false
	}
	if ok {
		return true
	}

	ok, err = path.Match(glob, m.subsys)
	if ok {
		return true
	}

	ok, err = path.Match(glob, name)
	if ok {
		return true
	}

	return false
}

func (m *meter) meterName(name string) string {
	n, sep := "", ""

	if !m.omitGroup && m.group != "" {
		n, sep = m.group, "."
	}
	if !m.omitSubsys && m.subsys != "" {
		n += sep + m.subsys
		sep = "."
	}
	return n + sep + name
}

func (m *meter) Int64Gauge(name string, options ...otelmetric.Int64GaugeOption) (otelmetric.Int64Gauge, error) {
	return m.Meter.Int64Gauge(m.meterName(name), options...)
}

func (m *meter) Int64ObservableGauge(name string, options ...otelmetric.Int64ObservableGaugeOption) (otelmetric.Int64ObservableGauge, error) {
	return m.Meter.Int64ObservableGauge(m.meterName(name), options...)
}

func (m *meter) Float64Gauge(name string, options ...otelmetric.Float64GaugeOption) (otelmetric.Float64Gauge, error) {
	return m.Meter.Float64Gauge(m.meterName(name), options...)
}

func Exporter() *prometheus.Exporter {
	initOnce.Do(setup)

	if exporter == nil {
		log.Errorf("can't get metric exporter, creation failed")
	}

	return exporter
}

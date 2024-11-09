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
	"fmt"
	"slices"
	"time"

	"github.com/prometheus/client_golang/prometheus/promhttp"

	"github.com/containers/nri-plugins/pkg/http"
	logger "github.com/containers/nri-plugins/pkg/log"
	"github.com/containers/nri-plugins/pkg/metrics"
	_ "github.com/containers/nri-plugins/pkg/metrics/collectors"
)

type (
	Option func() error
)

var (
	disabled     bool
	namespace    string
	enabled      []string
	polled       []string
	reportPeriod time.Duration
	mux          *http.ServeMux
	gatherer     *metrics.Gatherer
	log          = logger.Get("metrics")
)

func WithExporterDisabled(v bool) Option {
	return func() error {
		disabled = v
		return nil
	}
}

func WithNamespace(v string) Option {
	return func() error {
		namespace = v
		return nil
	}
}

func WithReportPeriod(v time.Duration) Option {
	return func() error {
		reportPeriod = v
		return nil
	}
}

func WithMetrics(enable []string, poll []string) Option {
	return func() error {
		enabled = slices.Clone(enable)
		polled = slices.Clone(poll)
		return nil
	}
}

func Start(m *http.ServeMux, options ...Option) error {
	Stop()

	for _, opt := range options {
		if err := opt(); err != nil {
			return err
		}
	}

	if m == nil {
		log.Info("no mux provided, metrics exporter disabled")
		return nil
	}

	if disabled {
		log.Info("metrics exporter disabled")
		return nil
	}

	log.Info("starting metrics exporter...")

	g, err := metrics.NewGatherer(
		metrics.WithNamespace("nri"),
		metrics.WithPollInterval(reportPeriod),
		metrics.WithMetrics(enabled, polled),
	)
	if err != nil {
		return fmt.Errorf("failed to create metrics gatherer: %v", err)
	}

	gatherer = g

	handlerOpts := promhttp.HandlerOpts{
		ErrorLog:      log,
		ErrorHandling: promhttp.ContinueOnError,
	}
	m.Handle("/metrics", promhttp.HandlerFor(g, handlerOpts))

	mux = m

	return nil
}

func Stop() {
	if mux == nil {
		return
	}

	mux.Unregister("/metrics")
	mux = nil
	gatherer.Stop()
	gatherer = nil
}

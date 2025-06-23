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
	"fmt"

	"github.com/containers/nri-plugins/pkg/apis/config/v1alpha1/resmgr/control/rdt"
	logger "github.com/containers/nri-plugins/pkg/log"
	"github.com/containers/nri-plugins/pkg/metrics"
	goresctrl "github.com/intel/goresctrl/pkg/rdt"
	"github.com/prometheus/client_golang/prometheus"
)

type rdtControl struct {
	resmgr    *resmgr
	hostRoot  string
	collector *rdtCollector
}

var (
	rdtlog = logger.Get("goresctrl")
)

func newRdtControl(resmgr *resmgr, hostRoot string) *rdtControl {
	rdt.SetLogger(rdtlog)

	if hostRoot != "" {
		rdt.SetPrefix(opt.HostRoot)
	}

	c, err := newRdtCollector()
	if err != nil {
		log.Error("failed to create RDT collector: %v", err)
	}

	return &rdtControl{
		resmgr:    resmgr,
		hostRoot:  hostRoot,
		collector: c,
	}
}

func (c *rdtControl) configure(cfg *rdt.Config) error {
	if cfg == nil {
		return nil
	}

	if cfg.Enable {
		nativeCfg, force, err := cfg.ToGoresctrl()
		if err != nil {
			return err
		}

		if err := rdt.Initialize(""); err != nil {
			return fmt.Errorf("failed to initialize goresctrl/rdt: %w", err)
		}
		log.Info("goresctrl/rdt initialized")

		if nativeCfg != nil {
			if err := rdt.SetConfig(nativeCfg, force); err != nil {
				return fmt.Errorf("failed to configure goresctrl/rdt: %w", err)
			}
			log.Info("goresctrl/rdt configuration updated")

			for _, c := range rdt.GetClasses() {
				if _, err := c.CreateMonGroup("test_group", nil); err != nil {
					log.Errorf("failed to create monitoring group for class %s: %v", c.Name, err)
				}
			}
		}
	}

	c.resmgr.cache.ConfigureRDTControl(cfg.Enable)

	return nil
}

type rdtCollector struct {
	prometheus.Collector
}

func newRdtCollector() (*rdtCollector, error) {
	c, err := goresctrl.NewCollector()
	if err != nil {
		return nil, fmt.Errorf("failed to create goresctrl/rdt metrics collector: %w", err)
	}

	options := []metrics.RegisterOption{
		metrics.WithGroup("policy"),
		metrics.WithCollectorOptions(
			metrics.WithoutNamespace(),
			metrics.WithoutSubsystem(),
		),
	}

	rdtc := &rdtCollector{c}

	if err := metrics.Register("rdt", rdtc, options...); err != nil {
		return nil, err
	}

	return rdtc, nil
}

func (c *rdtCollector) Describe(ch chan<- *prometheus.Desc) {
	rdtlog.Debug("describing RDT metrics...")
	c.Collector.Describe(ch)
}

func (c *rdtCollector) Collect(ch chan<- prometheus.Metric) {
	rdtlog.Debug("collecting RDT metrics...")
	c.Collector.Collect(ch)
}

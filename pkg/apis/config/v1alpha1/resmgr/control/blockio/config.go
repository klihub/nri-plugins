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

package blockio

import (
	"slices"

	"github.com/intel/goresctrl/pkg/blockio"
	grclog "github.com/intel/goresctrl/pkg/log"
	grcpath "github.com/intel/goresctrl/pkg/path"
)

var (
	// Expose goresctrl/blockio functions for configuration via this package.
	SetPrefix func(string)                      = grcpath.SetPrefix
	SetLogger func(grclog.Logger)               = blockio.SetLogger
	SetConfig func(*blockio.Config, bool) error = blockio.SetConfig
)

// Config provides runtime configuration for class based block I/O
// prioritization and throttling.
// +kubebuilder:object:generate=true
type Config struct {
	// Enable class based block I/O prioritization and throttling. When
	// enabled, policy implementations can adjust block I/O priority by
	// by assigning containers to block I/O priority classes.
	// +optional
	Enable bool `json:"enable,omitempty"`
	// usePodQoSAsDefaultClass controls whether a container's Pod QoS
	// class is used as its block I/O class, if this is otherwise unset.
	// +optional
	UsePodQoSAsDefaultClass bool `json:"usePodQoSAsDefaultClass,omitempty"`
	// Classes define weights and throttling parameters for sets of devices.
	// +optional
	Classes map[string][]DevicesParameters `json:"classes,omitempty"`
	// Force indicates if the configuration should be forced to goresctrl.
	Force bool `json:"force,omitempty"`
}

// +kubebuilder:object:generate=true
type DevicesParameters struct {
	Devices           []string `json:"devices,omitempty"`
	ThrottleReadBps   string   `json:"throttleReadBps,omitempty"`
	ThrottleWriteBps  string   `json:"throttleWriteBps,omitempty"`
	ThrottleReadIOPS  string   `json:"throttleReadIOPS,omitempty"`
	ThrottleWriteIOPS string   `json:"throttleWriteIOPS,omitempty"`
	Weight            string   `json:"weight,omitempty"`
}

func (c *Config) ToGoresctrl() (*blockio.Config, bool) {
	if c == nil {
		return nil, false
	}

	out := &blockio.Config{
		Classes: make(map[string][]blockio.DevicesParameters, len(c.Classes)),
	}

	for name, params := range c.Classes {
		out.Classes[name] = make([]blockio.DevicesParameters, len(params))
		for _, param := range params {
			out.Classes[name] = append(out.Classes[name],
				blockio.DevicesParameters{
					Devices:           slices.Clone(param.Devices),
					ThrottleReadBps:   param.ThrottleReadBps,
					ThrottleWriteBps:  param.ThrottleWriteBps,
					ThrottleReadIOPS:  param.ThrottleReadIOPS,
					ThrottleWriteIOPS: param.ThrottleWriteIOPS,
					Weight:            param.Weight,
				},
			)
		}
	}

	return out, c.Force
}

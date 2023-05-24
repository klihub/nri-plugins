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

package refactored

import (
	"github.com/containers/nri-plugins/pkg/config"
)

// PinOptions represents resource pinning preferences.
type PinOptions struct {
	CPU    bool // `json:"cpu"`
	Memory bool // `json:"memory"`
}

// CPUOptions represents CPU allocation preferences.
type CPUOptions struct {
	PreferIsolated bool // `json:"preferIsolated"`
	PreferShared   bool // `json:"preferShared"`
}

// ColocateOptions represents workload colocation preferences.
type ColocateOptions struct {
	Pods       bool // `json:"pods"`
	Namespaces bool // `json:"namespaces"`
}

// PoolOptions represents extra namespace or annotated pool preferences.
type PoolOptions struct {
	Namespaces  []string          // `json:"namespaces"`
	Annotations map[string]string // `json:"annotations"`
}

// Config represents runtime configuration for this policy.
type Config struct {
	Pin          PinOptions      // `json:"pin"`
	CPU          CPUOptions      // `json:"cpu"`
	Colocate     ColocateOptions // `json:"colocate"`
	ReservedPool PoolOptions     // `json:"reservedPool"`
}

// defaultConfig returns our default runtime configuration.
func defaultConfig() interface{} {
	// Our default configuration.
	//
	// By default we
	//   - pin CPU and memory
	//   - prefer isolated over sliced off shared CPUs for exclusive allocation
	//   - neither colocate pods nor namespaces
	//   - allocate no extra pods to the reserved pool by namespace of annotation
	//
	// These apply to all pods and containers unless they are annotated
	// otherwise (in those cases where we provide such an annotation).
	return &Config{
		Pin: PinOptions{
			CPU:    true,
			Memory: true,
		},
		CPU: CPUOptions{
			PreferIsolated: true,
			PreferShared:   false,
		},
		Colocate: ColocateOptions{
			Pods:       false,
			Namespaces: false,
		},
		ReservedPool: PoolOptions{
			Namespaces:  nil,
			Annotations: nil,
		},
	}
}

// Register us for configuration handling.
func init() {
	config.Register(PolicyPath, PolicyDescription, cfg, defaultConfig)
}

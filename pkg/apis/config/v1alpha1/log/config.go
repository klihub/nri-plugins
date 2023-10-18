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

package log

import (
	"github.com/containers/nri-plugins/pkg/apis/config/v1alpha1/log/klogcontrol"
)

// +k8s:deepcopy-gen=true
type Config struct {
	// Debug controls which log sources produce debug messages.
	// +optional
	Debug map[string]bool `json:"debug"`
	// LogSource controls if messages are prefixed with the logger source.
	// +optional
	LogSource bool `json:"logSource,omitempty"`
	// Klog configures klog-specific options.
	// +optional
	Klog klogcontrol.Config `json:"klog,omitempty"`
}

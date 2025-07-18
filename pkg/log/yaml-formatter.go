// Copyright 2019-2020 Intel Corporation. All Rights Reserved.
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
	"sigs.k8s.io/yaml"
)

func AsYaml(o any) *YamlFormatter {
	return &YamlFormatter{o: o}
}

type YamlFormatter struct {
	o any
}

func (f *YamlFormatter) String() string {
	if f == nil || f.o == nil {
		return ""
	}

	// Use a YAML marshaller to format the object.
	data, err := yaml.Marshal(f.o)
	if err != nil {
		return "<log YAML formatting error: " + err.Error() + ">"
	}

	return string(data)
}

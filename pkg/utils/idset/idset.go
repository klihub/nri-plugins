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

package idset

import (
	"fmt"

	"github.com/containers/nri-plugins/pkg/utils/cpuset"
	idset "github.com/intel/goresctrl/pkg/utils"
)

type (
	ID    = idset.ID
	IDSet = idset.IDSet
)

const (
	UnknownID = idset.Unknown
)

var (
	NewIDSet          = idset.NewIDSet
	NewIDSetFromSlice = idset.NewIDSetFromIntSlice
)

// ParseIDSet parses the given IDSet from a cpuset-compatible notation.
func ParseIDSet(s string) (IDSet, error) {
	cset, err := cpuset.Parse(s)
	if err != nil {
		return nil, fmt.Errorf("failed to parse IDSet %s: %w", s, err)
	}
	return NewIDSetFromSlice(cset.List()...), nil
}

// MustParseIDSet panics if parsing the given IDSet fails.
func MustParseIDSet(s string) IDSet {
	iset, err := ParseIDSet(s)
	if err != nil {
		panic(fmt.Errorf("failed to parse IDSet %s: %w", s, err))
	}
	return iset
}

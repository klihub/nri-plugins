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

// Package epp provides a simple represenation of energy/performance preferences.
package epp

import "fmt"

// Preference describes energy/performance preferences.
type Preference int

const (
	Unknown Preference = iota - 1
	Default
	Performance
	BalancePerformance
	BalancePower
	Power
)

var (
	Names = map[Preference]string{
		Unknown:            "<unknown EPP>",
		Default:            "default",
		Performance:        "performance",
		BalancePerformance: "balance_performance",
		BalancePower:       "balance_power",
		Power:              "power",
	}
	Preferences = map[string]Preference{
		Names[Default]:            Default,
		Names[Performance]:        Performance,
		Names[BalancePerformance]: BalancePerformance,
		Names[BalancePower]:       BalancePower,
		Names[Power]:              Power,
	}
)

func (p Preference) String() string {
	name, ok := Names[p]
	if ok {
		return name
	}
	return Names[Unknown]
}

func Parse(s string) (Preference, error) {
	if p, ok := Preferences[s]; ok {
		return p, nil
	}
	return Unknown, fmt.Errorf("unknown EPP \"%s\"", s)
}

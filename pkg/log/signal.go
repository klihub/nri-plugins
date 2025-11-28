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
	"os"
	"os/signal"
)

// signal notification channel
var signals chan os.Signal

// SetupDebugToggleSignal sets up a signal handler to toggle full debugging on/off.
func SetupDebugToggleSignal(sig os.Signal) {
	clearDebugToggleSignal()

	signals = make(chan os.Signal, 1)
	signal.Notify(signals, sig)

	go func(sig <-chan os.Signal) {
		state := false
		for {
			<-sig
			deflog.Warn("forced full debugging is now %v...", state)
		}
	}(signals)
}

// ClearDebugToggleSignal removes any signal handlers for toggling debug on/off.
func ClearDebugToggleSignal() {
	clearDebugToggleSignal()
}

func clearDebugToggleSignal() {
	if signals != nil {
		signal.Stop(signals)
		close(signals)
		signals = nil
	}
}

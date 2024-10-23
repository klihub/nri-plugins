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

package libcpu

import "errors"

var (
	ErrFailedSetup    = errors.New("libcpu: allocator setup failed")
	ErrInvalidCpuMask = errors.New("libcpu: invalid cpumask")
	ErrInvalidRequest = errors.New("libcpu: invalid request")
	ErrAlreadyExists  = errors.New("libcpu: allocation already exists")
	ErrUnknownRequest = errors.New("libcpu: unknown request")
	ErrNoCpu          = errors.New("libcpu: insufficient available CPU")
)

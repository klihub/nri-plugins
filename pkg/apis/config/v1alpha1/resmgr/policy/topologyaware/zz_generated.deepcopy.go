//go:build !ignore_autogenerated

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

// Code generated by controller-gen. DO NOT EDIT.

package topologyaware

import (
	"github.com/containers/nri-plugins/pkg/apis/config/v1alpha1/resmgr/policy"
)

// DeepCopyInto is an autogenerated deepcopy function, copying the receiver, writing into out. in must be non-nil.
func (in *Config) DeepCopyInto(out *Config) {
	*out = *in
	if in.PreferIsolated != nil {
		in, out := &in.PreferIsolated, &out.PreferIsolated
		*out = new(bool)
		**out = **in
	}
	if in.PreferShared != nil {
		in, out := &in.PreferShared, &out.PreferShared
		*out = new(bool)
		**out = **in
	}
	if in.ReservedPoolNamespaces != nil {
		in, out := &in.ReservedPoolNamespaces, &out.ReservedPoolNamespaces
		*out = make([]string, len(*in))
		copy(*out, *in)
	}
	if in.AvailableResources != nil {
		in, out := &in.AvailableResources, &out.AvailableResources
		*out = make(policy.Constraints, len(*in))
		for key, val := range *in {
			(*out)[key] = val
		}
	}
	if in.ReservedResources != nil {
		in, out := &in.ReservedResources, &out.ReservedResources
		*out = make(policy.Constraints, len(*in))
		for key, val := range *in {
			(*out)[key] = val
		}
	}
}

// DeepCopy is an autogenerated deepcopy function, copying the receiver, creating a new Config.
func (in *Config) DeepCopy() *Config {
	if in == nil {
		return nil
	}
	out := new(Config)
	in.DeepCopyInto(out)
	return out
}

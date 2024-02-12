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

package balloons

import (
	"github.com/containers/nri-plugins/pkg/apis/config/v1alpha1/resmgr/policy"
	v1alpha1 "github.com/containers/nri-plugins/pkg/apis/resmgr/v1alpha1"
)

// DeepCopyInto is an autogenerated deepcopy function, copying the receiver, writing into out. in must be non-nil.
func (in *BalloonDef) DeepCopyInto(out *BalloonDef) {
	*out = *in
	if in.Namespaces != nil {
		in, out := &in.Namespaces, &out.Namespaces
		*out = make([]string, len(*in))
		copy(*out, *in)
	}
	if in.MatchContainers != nil {
		in, out := &in.MatchContainers, &out.MatchContainers
		*out = make([]v1alpha1.Expression, len(*in))
		for i := range *in {
			(*in)[i].DeepCopyInto(&(*out)[i])
		}
	}
	if in.PreferSpreadOnPhysicalCores != nil {
		in, out := &in.PreferSpreadOnPhysicalCores, &out.PreferSpreadOnPhysicalCores
		*out = new(bool)
		**out = **in
	}
	if in.AllocatorTopologyBalancing != nil {
		in, out := &in.AllocatorTopologyBalancing, &out.AllocatorTopologyBalancing
		*out = new(bool)
		**out = **in
	}
	if in.PreferCloseToDevices != nil {
		in, out := &in.PreferCloseToDevices, &out.PreferCloseToDevices
		*out = make([]string, len(*in))
		copy(*out, *in)
	}
	if in.PreferFarFromDevices != nil {
		in, out := &in.PreferFarFromDevices, &out.PreferFarFromDevices
		*out = make([]string, len(*in))
		copy(*out, *in)
	}
}

// DeepCopy is an autogenerated deepcopy function, copying the receiver, creating a new BalloonDef.
func (in *BalloonDef) DeepCopy() *BalloonDef {
	if in == nil {
		return nil
	}
	out := new(BalloonDef)
	in.DeepCopyInto(out)
	return out
}

// DeepCopyInto is an autogenerated deepcopy function, copying the receiver, writing into out. in must be non-nil.
func (in *Config) DeepCopyInto(out *Config) {
	*out = *in
	if in.PinCPU != nil {
		in, out := &in.PinCPU, &out.PinCPU
		*out = new(bool)
		**out = **in
	}
	if in.PinMemory != nil {
		in, out := &in.PinMemory, &out.PinMemory
		*out = new(bool)
		**out = **in
	}
	if in.ReservedPoolNamespaces != nil {
		in, out := &in.ReservedPoolNamespaces, &out.ReservedPoolNamespaces
		*out = make([]string, len(*in))
		copy(*out, *in)
	}
	if in.BalloonDefs != nil {
		in, out := &in.BalloonDefs, &out.BalloonDefs
		*out = make([]*BalloonDef, len(*in))
		for i := range *in {
			if (*in)[i] != nil {
				in, out := &(*in)[i], &(*out)[i]
				*out = new(BalloonDef)
				(*in).DeepCopyInto(*out)
			}
		}
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

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

package v1alpha1

import (
	runtime "k8s.io/apimachinery/pkg/runtime"
)

// DeepCopyInto is an autogenerated deepcopy function, copying the receiver, writing into out. in must be non-nil.
func (in *BalloonsPolicy) DeepCopyInto(out *BalloonsPolicy) {
	*out = *in
	out.TypeMeta = in.TypeMeta
	in.ObjectMeta.DeepCopyInto(&out.ObjectMeta)
	in.Spec.DeepCopyInto(&out.Spec)
	in.Status.DeepCopyInto(&out.Status)
}

// DeepCopy is an autogenerated deepcopy function, copying the receiver, creating a new BalloonsPolicy.
func (in *BalloonsPolicy) DeepCopy() *BalloonsPolicy {
	if in == nil {
		return nil
	}
	out := new(BalloonsPolicy)
	in.DeepCopyInto(out)
	return out
}

// DeepCopyObject is an autogenerated deepcopy function, copying the receiver, creating a new runtime.Object.
func (in *BalloonsPolicy) DeepCopyObject() runtime.Object {
	if c := in.DeepCopy(); c != nil {
		return c
	}
	return nil
}

// DeepCopyInto is an autogenerated deepcopy function, copying the receiver, writing into out. in must be non-nil.
func (in *BalloonsPolicyList) DeepCopyInto(out *BalloonsPolicyList) {
	*out = *in
	out.TypeMeta = in.TypeMeta
	in.ListMeta.DeepCopyInto(&out.ListMeta)
	if in.Items != nil {
		in, out := &in.Items, &out.Items
		*out = make([]BalloonsPolicy, len(*in))
		for i := range *in {
			(*in)[i].DeepCopyInto(&(*out)[i])
		}
	}
}

// DeepCopy is an autogenerated deepcopy function, copying the receiver, creating a new BalloonsPolicyList.
func (in *BalloonsPolicyList) DeepCopy() *BalloonsPolicyList {
	if in == nil {
		return nil
	}
	out := new(BalloonsPolicyList)
	in.DeepCopyInto(out)
	return out
}

// DeepCopyObject is an autogenerated deepcopy function, copying the receiver, creating a new runtime.Object.
func (in *BalloonsPolicyList) DeepCopyObject() runtime.Object {
	if c := in.DeepCopy(); c != nil {
		return c
	}
	return nil
}

// DeepCopyInto is an autogenerated deepcopy function, copying the receiver, writing into out. in must be non-nil.
func (in *BalloonsPolicySpec) DeepCopyInto(out *BalloonsPolicySpec) {
	*out = *in
	in.Config.DeepCopyInto(&out.Config)
	in.Control.DeepCopyInto(&out.Control)
	in.Log.DeepCopyInto(&out.Log)
	out.Instrumentation = in.Instrumentation
}

// DeepCopy is an autogenerated deepcopy function, copying the receiver, creating a new BalloonsPolicySpec.
func (in *BalloonsPolicySpec) DeepCopy() *BalloonsPolicySpec {
	if in == nil {
		return nil
	}
	out := new(BalloonsPolicySpec)
	in.DeepCopyInto(out)
	return out
}

// DeepCopyInto is an autogenerated deepcopy function, copying the receiver, writing into out. in must be non-nil.
func (in *CommonConfig) DeepCopyInto(out *CommonConfig) {
	*out = *in
	in.Control.DeepCopyInto(&out.Control)
	in.Log.DeepCopyInto(&out.Log)
	out.Instrumentation = in.Instrumentation
}

// DeepCopy is an autogenerated deepcopy function, copying the receiver, creating a new CommonConfig.
func (in *CommonConfig) DeepCopy() *CommonConfig {
	if in == nil {
		return nil
	}
	out := new(CommonConfig)
	in.DeepCopyInto(out)
	return out
}

// DeepCopyInto is an autogenerated deepcopy function, copying the receiver, writing into out. in must be non-nil.
func (in *ConfigStatus) DeepCopyInto(out *ConfigStatus) {
	*out = *in
	if in.Nodes != nil {
		in, out := &in.Nodes, &out.Nodes
		*out = make(map[string]NodeStatus, len(*in))
		for key, val := range *in {
			(*out)[key] = *val.DeepCopy()
		}
	}
}

// DeepCopy is an autogenerated deepcopy function, copying the receiver, creating a new ConfigStatus.
func (in *ConfigStatus) DeepCopy() *ConfigStatus {
	if in == nil {
		return nil
	}
	out := new(ConfigStatus)
	in.DeepCopyInto(out)
	return out
}

// DeepCopyInto is an autogenerated deepcopy function, copying the receiver, writing into out. in must be non-nil.
func (in *GenericPolicy) DeepCopyInto(out *GenericPolicy) {
	*out = *in
	out.TypeMeta = in.TypeMeta
	in.ObjectMeta.DeepCopyInto(&out.ObjectMeta)
	in.Spec.DeepCopyInto(&out.Spec)
	in.Status.DeepCopyInto(&out.Status)
}

// DeepCopy is an autogenerated deepcopy function, copying the receiver, creating a new GenericPolicy.
func (in *GenericPolicy) DeepCopy() *GenericPolicy {
	if in == nil {
		return nil
	}
	out := new(GenericPolicy)
	in.DeepCopyInto(out)
	return out
}

// DeepCopyObject is an autogenerated deepcopy function, copying the receiver, creating a new runtime.Object.
func (in *GenericPolicy) DeepCopyObject() runtime.Object {
	if c := in.DeepCopy(); c != nil {
		return c
	}
	return nil
}

// DeepCopyInto is an autogenerated deepcopy function, copying the receiver, writing into out. in must be non-nil.
func (in *GenericPolicyList) DeepCopyInto(out *GenericPolicyList) {
	*out = *in
	out.TypeMeta = in.TypeMeta
	in.ListMeta.DeepCopyInto(&out.ListMeta)
	if in.Items != nil {
		in, out := &in.Items, &out.Items
		*out = make([]GenericPolicy, len(*in))
		for i := range *in {
			(*in)[i].DeepCopyInto(&(*out)[i])
		}
	}
}

// DeepCopy is an autogenerated deepcopy function, copying the receiver, creating a new GenericPolicyList.
func (in *GenericPolicyList) DeepCopy() *GenericPolicyList {
	if in == nil {
		return nil
	}
	out := new(GenericPolicyList)
	in.DeepCopyInto(out)
	return out
}

// DeepCopyObject is an autogenerated deepcopy function, copying the receiver, creating a new runtime.Object.
func (in *GenericPolicyList) DeepCopyObject() runtime.Object {
	if c := in.DeepCopy(); c != nil {
		return c
	}
	return nil
}

// DeepCopyInto is an autogenerated deepcopy function, copying the receiver, writing into out. in must be non-nil.
func (in *GenericPolicySpec) DeepCopyInto(out *GenericPolicySpec) {
	*out = *in
	in.Config.DeepCopyInto(&out.Config)
	in.Control.DeepCopyInto(&out.Control)
	in.Log.DeepCopyInto(&out.Log)
	out.Instrumentation = in.Instrumentation
}

// DeepCopy is an autogenerated deepcopy function, copying the receiver, creating a new GenericPolicySpec.
func (in *GenericPolicySpec) DeepCopy() *GenericPolicySpec {
	if in == nil {
		return nil
	}
	out := new(GenericPolicySpec)
	in.DeepCopyInto(out)
	return out
}

// DeepCopyInto is an autogenerated deepcopy function, copying the receiver, writing into out. in must be non-nil.
func (in *NodeStatus) DeepCopyInto(out *NodeStatus) {
	*out = *in
	if in.Error != nil {
		in, out := &in.Error, &out.Error
		*out = new(string)
		**out = **in
	}
	in.Timestamp.DeepCopyInto(&out.Timestamp)
}

// DeepCopy is an autogenerated deepcopy function, copying the receiver, creating a new NodeStatus.
func (in *NodeStatus) DeepCopy() *NodeStatus {
	if in == nil {
		return nil
	}
	out := new(NodeStatus)
	in.DeepCopyInto(out)
	return out
}

// DeepCopyInto is an autogenerated deepcopy function, copying the receiver, writing into out. in must be non-nil.
func (in *TemplatePolicy) DeepCopyInto(out *TemplatePolicy) {
	*out = *in
	out.TypeMeta = in.TypeMeta
	in.ObjectMeta.DeepCopyInto(&out.ObjectMeta)
	in.Spec.DeepCopyInto(&out.Spec)
	in.Status.DeepCopyInto(&out.Status)
}

// DeepCopy is an autogenerated deepcopy function, copying the receiver, creating a new TemplatePolicy.
func (in *TemplatePolicy) DeepCopy() *TemplatePolicy {
	if in == nil {
		return nil
	}
	out := new(TemplatePolicy)
	in.DeepCopyInto(out)
	return out
}

// DeepCopyObject is an autogenerated deepcopy function, copying the receiver, creating a new runtime.Object.
func (in *TemplatePolicy) DeepCopyObject() runtime.Object {
	if c := in.DeepCopy(); c != nil {
		return c
	}
	return nil
}

// DeepCopyInto is an autogenerated deepcopy function, copying the receiver, writing into out. in must be non-nil.
func (in *TemplatePolicyList) DeepCopyInto(out *TemplatePolicyList) {
	*out = *in
	out.TypeMeta = in.TypeMeta
	in.ListMeta.DeepCopyInto(&out.ListMeta)
	if in.Items != nil {
		in, out := &in.Items, &out.Items
		*out = make([]TemplatePolicy, len(*in))
		for i := range *in {
			(*in)[i].DeepCopyInto(&(*out)[i])
		}
	}
}

// DeepCopy is an autogenerated deepcopy function, copying the receiver, creating a new TemplatePolicyList.
func (in *TemplatePolicyList) DeepCopy() *TemplatePolicyList {
	if in == nil {
		return nil
	}
	out := new(TemplatePolicyList)
	in.DeepCopyInto(out)
	return out
}

// DeepCopyObject is an autogenerated deepcopy function, copying the receiver, creating a new runtime.Object.
func (in *TemplatePolicyList) DeepCopyObject() runtime.Object {
	if c := in.DeepCopy(); c != nil {
		return c
	}
	return nil
}

// DeepCopyInto is an autogenerated deepcopy function, copying the receiver, writing into out. in must be non-nil.
func (in *TemplatePolicySpec) DeepCopyInto(out *TemplatePolicySpec) {
	*out = *in
	in.Config.DeepCopyInto(&out.Config)
	in.Control.DeepCopyInto(&out.Control)
	in.Log.DeepCopyInto(&out.Log)
	out.Instrumentation = in.Instrumentation
}

// DeepCopy is an autogenerated deepcopy function, copying the receiver, creating a new TemplatePolicySpec.
func (in *TemplatePolicySpec) DeepCopy() *TemplatePolicySpec {
	if in == nil {
		return nil
	}
	out := new(TemplatePolicySpec)
	in.DeepCopyInto(out)
	return out
}

// DeepCopyInto is an autogenerated deepcopy function, copying the receiver, writing into out. in must be non-nil.
func (in *TopologyAwarePolicy) DeepCopyInto(out *TopologyAwarePolicy) {
	*out = *in
	out.TypeMeta = in.TypeMeta
	in.ObjectMeta.DeepCopyInto(&out.ObjectMeta)
	in.Spec.DeepCopyInto(&out.Spec)
	in.Status.DeepCopyInto(&out.Status)
}

// DeepCopy is an autogenerated deepcopy function, copying the receiver, creating a new TopologyAwarePolicy.
func (in *TopologyAwarePolicy) DeepCopy() *TopologyAwarePolicy {
	if in == nil {
		return nil
	}
	out := new(TopologyAwarePolicy)
	in.DeepCopyInto(out)
	return out
}

// DeepCopyObject is an autogenerated deepcopy function, copying the receiver, creating a new runtime.Object.
func (in *TopologyAwarePolicy) DeepCopyObject() runtime.Object {
	if c := in.DeepCopy(); c != nil {
		return c
	}
	return nil
}

// DeepCopyInto is an autogenerated deepcopy function, copying the receiver, writing into out. in must be non-nil.
func (in *TopologyAwarePolicyList) DeepCopyInto(out *TopologyAwarePolicyList) {
	*out = *in
	out.TypeMeta = in.TypeMeta
	in.ListMeta.DeepCopyInto(&out.ListMeta)
	if in.Items != nil {
		in, out := &in.Items, &out.Items
		*out = make([]TopologyAwarePolicy, len(*in))
		for i := range *in {
			(*in)[i].DeepCopyInto(&(*out)[i])
		}
	}
}

// DeepCopy is an autogenerated deepcopy function, copying the receiver, creating a new TopologyAwarePolicyList.
func (in *TopologyAwarePolicyList) DeepCopy() *TopologyAwarePolicyList {
	if in == nil {
		return nil
	}
	out := new(TopologyAwarePolicyList)
	in.DeepCopyInto(out)
	return out
}

// DeepCopyObject is an autogenerated deepcopy function, copying the receiver, creating a new runtime.Object.
func (in *TopologyAwarePolicyList) DeepCopyObject() runtime.Object {
	if c := in.DeepCopy(); c != nil {
		return c
	}
	return nil
}

// DeepCopyInto is an autogenerated deepcopy function, copying the receiver, writing into out. in must be non-nil.
func (in *TopologyAwarePolicySpec) DeepCopyInto(out *TopologyAwarePolicySpec) {
	*out = *in
	in.Config.DeepCopyInto(&out.Config)
	in.Control.DeepCopyInto(&out.Control)
	in.Log.DeepCopyInto(&out.Log)
	out.Instrumentation = in.Instrumentation
}

// DeepCopy is an autogenerated deepcopy function, copying the receiver, creating a new TopologyAwarePolicySpec.
func (in *TopologyAwarePolicySpec) DeepCopy() *TopologyAwarePolicySpec {
	if in == nil {
		return nil
	}
	out := new(TopologyAwarePolicySpec)
	in.DeepCopyInto(out)
	return out
}
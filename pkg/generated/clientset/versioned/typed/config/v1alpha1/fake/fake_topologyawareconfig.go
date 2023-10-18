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

// Code generated by client-gen. DO NOT EDIT.

package fake

import (
	"context"

	v1alpha1 "github.com/containers/nri-plugins/pkg/apis/config/v1alpha1"
	v1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	labels "k8s.io/apimachinery/pkg/labels"
	types "k8s.io/apimachinery/pkg/types"
	watch "k8s.io/apimachinery/pkg/watch"
	testing "k8s.io/client-go/testing"
)

// FakeTopologyAwareConfigs implements TopologyAwareConfigInterface
type FakeTopologyAwareConfigs struct {
	Fake *FakeConfigV1alpha1
	ns   string
}

var topologyawareconfigsResource = v1alpha1.SchemeGroupVersion.WithResource("topologyawareconfigs")

var topologyawareconfigsKind = v1alpha1.SchemeGroupVersion.WithKind("TopologyAwareConfig")

// Get takes name of the topologyAwareConfig, and returns the corresponding topologyAwareConfig object, and an error if there is any.
func (c *FakeTopologyAwareConfigs) Get(ctx context.Context, name string, options v1.GetOptions) (result *v1alpha1.TopologyAwareConfig, err error) {
	obj, err := c.Fake.
		Invokes(testing.NewGetAction(topologyawareconfigsResource, c.ns, name), &v1alpha1.TopologyAwareConfig{})

	if obj == nil {
		return nil, err
	}
	return obj.(*v1alpha1.TopologyAwareConfig), err
}

// List takes label and field selectors, and returns the list of TopologyAwareConfigs that match those selectors.
func (c *FakeTopologyAwareConfigs) List(ctx context.Context, opts v1.ListOptions) (result *v1alpha1.TopologyAwareConfigList, err error) {
	obj, err := c.Fake.
		Invokes(testing.NewListAction(topologyawareconfigsResource, topologyawareconfigsKind, c.ns, opts), &v1alpha1.TopologyAwareConfigList{})

	if obj == nil {
		return nil, err
	}

	label, _, _ := testing.ExtractFromListOptions(opts)
	if label == nil {
		label = labels.Everything()
	}
	list := &v1alpha1.TopologyAwareConfigList{ListMeta: obj.(*v1alpha1.TopologyAwareConfigList).ListMeta}
	for _, item := range obj.(*v1alpha1.TopologyAwareConfigList).Items {
		if label.Matches(labels.Set(item.Labels)) {
			list.Items = append(list.Items, item)
		}
	}
	return list, err
}

// Watch returns a watch.Interface that watches the requested topologyAwareConfigs.
func (c *FakeTopologyAwareConfigs) Watch(ctx context.Context, opts v1.ListOptions) (watch.Interface, error) {
	return c.Fake.
		InvokesWatch(testing.NewWatchAction(topologyawareconfigsResource, c.ns, opts))

}

// Create takes the representation of a topologyAwareConfig and creates it.  Returns the server's representation of the topologyAwareConfig, and an error, if there is any.
func (c *FakeTopologyAwareConfigs) Create(ctx context.Context, topologyAwareConfig *v1alpha1.TopologyAwareConfig, opts v1.CreateOptions) (result *v1alpha1.TopologyAwareConfig, err error) {
	obj, err := c.Fake.
		Invokes(testing.NewCreateAction(topologyawareconfigsResource, c.ns, topologyAwareConfig), &v1alpha1.TopologyAwareConfig{})

	if obj == nil {
		return nil, err
	}
	return obj.(*v1alpha1.TopologyAwareConfig), err
}

// Update takes the representation of a topologyAwareConfig and updates it. Returns the server's representation of the topologyAwareConfig, and an error, if there is any.
func (c *FakeTopologyAwareConfigs) Update(ctx context.Context, topologyAwareConfig *v1alpha1.TopologyAwareConfig, opts v1.UpdateOptions) (result *v1alpha1.TopologyAwareConfig, err error) {
	obj, err := c.Fake.
		Invokes(testing.NewUpdateAction(topologyawareconfigsResource, c.ns, topologyAwareConfig), &v1alpha1.TopologyAwareConfig{})

	if obj == nil {
		return nil, err
	}
	return obj.(*v1alpha1.TopologyAwareConfig), err
}

// UpdateStatus was generated because the type contains a Status member.
// Add a +genclient:noStatus comment above the type to avoid generating UpdateStatus().
func (c *FakeTopologyAwareConfigs) UpdateStatus(ctx context.Context, topologyAwareConfig *v1alpha1.TopologyAwareConfig, opts v1.UpdateOptions) (*v1alpha1.TopologyAwareConfig, error) {
	obj, err := c.Fake.
		Invokes(testing.NewUpdateSubresourceAction(topologyawareconfigsResource, "status", c.ns, topologyAwareConfig), &v1alpha1.TopologyAwareConfig{})

	if obj == nil {
		return nil, err
	}
	return obj.(*v1alpha1.TopologyAwareConfig), err
}

// Delete takes name of the topologyAwareConfig and deletes it. Returns an error if one occurs.
func (c *FakeTopologyAwareConfigs) Delete(ctx context.Context, name string, opts v1.DeleteOptions) error {
	_, err := c.Fake.
		Invokes(testing.NewDeleteActionWithOptions(topologyawareconfigsResource, c.ns, name, opts), &v1alpha1.TopologyAwareConfig{})

	return err
}

// DeleteCollection deletes a collection of objects.
func (c *FakeTopologyAwareConfigs) DeleteCollection(ctx context.Context, opts v1.DeleteOptions, listOpts v1.ListOptions) error {
	action := testing.NewDeleteCollectionAction(topologyawareconfigsResource, c.ns, listOpts)

	_, err := c.Fake.Invokes(action, &v1alpha1.TopologyAwareConfigList{})
	return err
}

// Patch applies the patch and returns the patched topologyAwareConfig.
func (c *FakeTopologyAwareConfigs) Patch(ctx context.Context, name string, pt types.PatchType, data []byte, opts v1.PatchOptions, subresources ...string) (result *v1alpha1.TopologyAwareConfig, err error) {
	obj, err := c.Fake.
		Invokes(testing.NewPatchSubresourceAction(topologyawareconfigsResource, c.ns, name, pt, data, subresources...), &v1alpha1.TopologyAwareConfig{})

	if obj == nil {
		return nil, err
	}
	return obj.(*v1alpha1.TopologyAwareConfig), err
}

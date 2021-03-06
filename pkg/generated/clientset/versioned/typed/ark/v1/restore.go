/*
Copyright 2017 the Heptio Ark contributors.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/
package v1

import (
	v1 "github.com/heptio/ark/pkg/apis/ark/v1"
	scheme "github.com/heptio/ark/pkg/generated/clientset/versioned/scheme"
	meta_v1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	types "k8s.io/apimachinery/pkg/types"
	watch "k8s.io/apimachinery/pkg/watch"
	rest "k8s.io/client-go/rest"
)

// RestoresGetter has a method to return a RestoreInterface.
// A group's client should implement this interface.
type RestoresGetter interface {
	Restores(namespace string) RestoreInterface
}

// RestoreInterface has methods to work with Restore resources.
type RestoreInterface interface {
	Create(*v1.Restore) (*v1.Restore, error)
	Update(*v1.Restore) (*v1.Restore, error)
	UpdateStatus(*v1.Restore) (*v1.Restore, error)
	Delete(name string, options *meta_v1.DeleteOptions) error
	DeleteCollection(options *meta_v1.DeleteOptions, listOptions meta_v1.ListOptions) error
	Get(name string, options meta_v1.GetOptions) (*v1.Restore, error)
	List(opts meta_v1.ListOptions) (*v1.RestoreList, error)
	Watch(opts meta_v1.ListOptions) (watch.Interface, error)
	Patch(name string, pt types.PatchType, data []byte, subresources ...string) (result *v1.Restore, err error)
	RestoreExpansion
}

// restores implements RestoreInterface
type restores struct {
	client rest.Interface
	ns     string
}

// newRestores returns a Restores
func newRestores(c *ArkV1Client, namespace string) *restores {
	return &restores{
		client: c.RESTClient(),
		ns:     namespace,
	}
}

// Get takes name of the restore, and returns the corresponding restore object, and an error if there is any.
func (c *restores) Get(name string, options meta_v1.GetOptions) (result *v1.Restore, err error) {
	result = &v1.Restore{}
	err = c.client.Get().
		Namespace(c.ns).
		Resource("restores").
		Name(name).
		VersionedParams(&options, scheme.ParameterCodec).
		Do().
		Into(result)
	return
}

// List takes label and field selectors, and returns the list of Restores that match those selectors.
func (c *restores) List(opts meta_v1.ListOptions) (result *v1.RestoreList, err error) {
	result = &v1.RestoreList{}
	err = c.client.Get().
		Namespace(c.ns).
		Resource("restores").
		VersionedParams(&opts, scheme.ParameterCodec).
		Do().
		Into(result)
	return
}

// Watch returns a watch.Interface that watches the requested restores.
func (c *restores) Watch(opts meta_v1.ListOptions) (watch.Interface, error) {
	opts.Watch = true
	return c.client.Get().
		Namespace(c.ns).
		Resource("restores").
		VersionedParams(&opts, scheme.ParameterCodec).
		Watch()
}

// Create takes the representation of a restore and creates it.  Returns the server's representation of the restore, and an error, if there is any.
func (c *restores) Create(restore *v1.Restore) (result *v1.Restore, err error) {
	result = &v1.Restore{}
	err = c.client.Post().
		Namespace(c.ns).
		Resource("restores").
		Body(restore).
		Do().
		Into(result)
	return
}

// Update takes the representation of a restore and updates it. Returns the server's representation of the restore, and an error, if there is any.
func (c *restores) Update(restore *v1.Restore) (result *v1.Restore, err error) {
	result = &v1.Restore{}
	err = c.client.Put().
		Namespace(c.ns).
		Resource("restores").
		Name(restore.Name).
		Body(restore).
		Do().
		Into(result)
	return
}

// UpdateStatus was generated because the type contains a Status member.
// Add a +genclient:noStatus comment above the type to avoid generating UpdateStatus().

func (c *restores) UpdateStatus(restore *v1.Restore) (result *v1.Restore, err error) {
	result = &v1.Restore{}
	err = c.client.Put().
		Namespace(c.ns).
		Resource("restores").
		Name(restore.Name).
		SubResource("status").
		Body(restore).
		Do().
		Into(result)
	return
}

// Delete takes name of the restore and deletes it. Returns an error if one occurs.
func (c *restores) Delete(name string, options *meta_v1.DeleteOptions) error {
	return c.client.Delete().
		Namespace(c.ns).
		Resource("restores").
		Name(name).
		Body(options).
		Do().
		Error()
}

// DeleteCollection deletes a collection of objects.
func (c *restores) DeleteCollection(options *meta_v1.DeleteOptions, listOptions meta_v1.ListOptions) error {
	return c.client.Delete().
		Namespace(c.ns).
		Resource("restores").
		VersionedParams(&listOptions, scheme.ParameterCodec).
		Body(options).
		Do().
		Error()
}

// Patch applies the patch and returns the patched restore.
func (c *restores) Patch(name string, pt types.PatchType, data []byte, subresources ...string) (result *v1.Restore, err error) {
	result = &v1.Restore{}
	err = c.client.Patch(pt).
		Namespace(c.ns).
		Resource("restores").
		SubResource(subresources...).
		Name(name).
		Body(data).
		Do().
		Into(result)
	return
}

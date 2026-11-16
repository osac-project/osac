/*
Copyright 2026.

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

// Package directclient provides a Kubernetes client that reads straight from
// the API server instead of the controller-runtime cache.
//
// Some operations must observe current cluster state rather than whatever an
// informer has seen so far: confirming that a deletion took effect, and
// reading or updating Secrets without paying for a cluster-wide Secret
// informer. Those operations need a client that is guaranteed uncached, which
// a plain client.Client argument cannot express — a cached client satisfies
// the same interface.
//
// Client seals that guarantee at compile time: its marker method is
// unexported, so only this package can implement it.
package directclient

import (
	"errors"

	"k8s.io/apimachinery/pkg/api/meta"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/rest"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// Client is a Kubernetes client whose reads go directly to the API server and
// never to the controller-runtime cache.
//
// The interface is sealed: a cache-backed client such as mgr.GetClient()
// cannot be passed where a Client is expected.
type Client interface {
	client.Client

	// direct is unexported on purpose. It stops any other package from
	// satisfying Client with a cache-backed implementation.
	direct()
}

// directClient marks a client.Client as direct. It adds no behavior of its
// own; the guarantee comes from how the embedded client was built.
type directClient struct {
	client.Client
}

func (directClient) direct() {}

// New builds a direct client from a REST configuration. The returned client
// reads from the API server and never starts or consults a cache.
//
// The signature deliberately omits client.Options: controller-runtime's
// client.New returns a cache-backed client when Options.Cache.Reader is set
// ("If both Options.Cache and Options.Cache.Reader are non-nil, the client
// reads from a local cache"), so accepting caller-supplied options would let a
// cache back in through the sealed type. Only the fields needed to talk to the
// API server are accepted, and the cache option is not reachable.
func New(cfg *rest.Config, scheme *runtime.Scheme, mapper meta.RESTMapper) (Client, error) {
	if cfg == nil {
		return nil, errors.New("rest config is required to build a direct client")
	}
	if scheme == nil {
		return nil, errors.New("scheme is required to build a direct client")
	}
	c, err := client.New(cfg, client.Options{Scheme: scheme, Mapper: mapper})
	if err != nil {
		return nil, err
	}
	return directClient{Client: c}, nil
}

// WrapForTest marks an existing client as direct. It exists so tests in other
// packages can inject a fake client; production code must use New.
//
// Callers must only pass a client known to read through to the API server: a
// value built with client.New, an envtest client, or a fake client. Never pass
// mgr.GetClient(), whose reads are served by the cache.
func WrapForTest(c client.Client) (Client, error) {
	if c == nil {
		return nil, errors.New("direct client is required")
	}
	return directClient{Client: c}, nil
}

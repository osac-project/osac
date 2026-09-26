/*
Copyright (c) 2026 Red Hat Inc.

Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except in compliance with the
License. You may obtain a copy of the License at

  http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on an
"AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the specific
language governing permissions and limitations under the License.
*/

package controllers

import (
	"context"
	"errors"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"google.golang.org/grpc"
	"google.golang.org/protobuf/proto"

	privatev1 "github.com/osac-project/osac/proto/gen/osac/private/v1"
)

type fakeNetworkClassesClient struct {
	objects      []*privatev1.NetworkClass
	listRequests []*privatev1.NetworkClassesListRequest
	updates      []*privatev1.NetworkClassesUpdateRequest
	listCalls    int
	updateErr    error
	updateObject *privatev1.NetworkClass
}

func (f *fakeNetworkClassesClient) List(
	_ context.Context,
	request *privatev1.NetworkClassesListRequest,
	_ ...grpc.CallOption,
) (*privatev1.NetworkClassesListResponse, error) {
	f.listCalls++
	f.listRequests = append(f.listRequests, proto.Clone(request).(*privatev1.NetworkClassesListRequest))
	return privatev1.NetworkClassesListResponse_builder{
		Items: f.objects,
		Size:  int32(len(f.objects)),
		Total: int32(len(f.objects)),
	}.Build(), nil
}

func (f *fakeNetworkClassesClient) Update(
	_ context.Context,
	request *privatev1.NetworkClassesUpdateRequest,
	_ ...grpc.CallOption,
) (*privatev1.NetworkClassesUpdateResponse, error) {
	f.updates = append(f.updates, proto.Clone(request).(*privatev1.NetworkClassesUpdateRequest))
	if f.updateErr != nil {
		return nil, f.updateErr
	}
	object := request.GetObject()
	if f.updateObject != nil {
		object = f.updateObject
	}
	return privatev1.NetworkClassesUpdateResponse_builder{Object: object}.Build(), nil
}

type fakeHubsListClient struct {
	items        []*privatev1.Hub
	listRequests []*privatev1.HubsListRequest
	listErr      error
	listCall     int
}

func (f *fakeHubsListClient) List(
	_ context.Context,
	request *privatev1.HubsListRequest,
	_ ...grpc.CallOption,
) (*privatev1.HubsListResponse, error) {
	f.listCall++
	f.listRequests = append(f.listRequests, proto.Clone(request).(*privatev1.HubsListRequest))
	if f.listErr != nil {
		return nil, f.listErr
	}
	return privatev1.HubsListResponse_builder{
		Items: f.items,
		Size:  int32(len(f.items)),
		Total: int32(len(f.items)),
	}.Build(), nil
}

type fakeNetworkingHubCache struct {
	entries map[string]*HubEntry
	errors  map[string]error
	calls   []string
}

func (f *fakeNetworkingHubCache) Get(_ context.Context, id string) (*HubEntry, error) {
	f.calls = append(f.calls, id)
	if err := f.errors[id]; err != nil {
		return nil, err
	}
	return f.entries[id], nil
}

var _ = Describe("NetworkingHubResolver", func() {
	var ctx context.Context

	BeforeEach(func() {
		ctx = context.Background()
	})

	It("persists and returns the only Hub when the NetworkClass has no canonical reference", func() {
		networkClass := testNetworkClass("nc-a", "", privatev1.NetworkClassState_NETWORK_CLASS_STATE_PENDING, "")
		networkClasses := &fakeNetworkClassesClient{objects: []*privatev1.NetworkClass{networkClass}}
		hubs := &fakeHubsListClient{items: []*privatev1.Hub{testHub("hub-a")}}
		cache := &fakeNetworkingHubCache{entries: map[string]*HubEntry{
			"hub-a": {Namespace: "networking", Client: nil},
		}}

		resolver := mustBuildNetworkingHubResolver(networkClasses, hubs, cache)

		result, err := resolver.Resolve(ctx)

		Expect(err).ToNot(HaveOccurred())
		Expect(result.ID).To(Equal("hub-a"))
		Expect(result.Namespace).To(Equal("networking"))
		Expect(hubs.listCall).To(Equal(1))
		Expect(networkClasses.listRequests[0].GetFilter()).To(Equal(activeResourceFilter))
		Expect(networkClasses.listRequests[0].GetLimit()).To(Equal(int32(activeResourceLimit)))
		Expect(hubs.listRequests[0].GetFilter()).To(Equal(activeResourceFilter))
		Expect(hubs.listRequests[0].GetLimit()).To(Equal(int32(activeResourceLimit)))
		Expect(cache.calls).To(Equal([]string{"hub-a"}))
		Expect(result.State).To(Equal(privatev1.NetworkClassState_NETWORK_CLASS_STATE_READY))
		Expect(result.Message).To(BeEmpty())
		Expect(networkClasses.updates).To(BeEmpty())
	})

	It("reports pending when there are no Hubs and does not call the cache", func() {
		networkClass := testNetworkClass("nc-a", "", privatev1.NetworkClassState_NETWORK_CLASS_STATE_READY, "old message")
		networkClasses := &fakeNetworkClassesClient{objects: []*privatev1.NetworkClass{networkClass}}
		hubs := &fakeHubsListClient{}
		cache := &fakeNetworkingHubCache{}

		resolver := mustBuildNetworkingHubResolver(networkClasses, hubs, cache)

		result, err := resolver.Resolve(ctx)

		Expect(errors.Is(err, ErrNoNetworkingHubs)).To(BeTrue())
		Expect(result.State).To(Equal(privatev1.NetworkClassState_NETWORK_CLASS_STATE_PENDING))
		Expect(result.Message).To(Equal("expected exactly one active networking hub, found none"))
		Expect(networkClasses.updates).To(BeEmpty())
		Expect(cache.calls).To(BeEmpty())
	})

	It("reports pending when multiple Hubs exist and does not choose one", func() {
		networkClass := testNetworkClass("nc-a", "", privatev1.NetworkClassState_NETWORK_CLASS_STATE_READY, "")
		networkClasses := &fakeNetworkClassesClient{objects: []*privatev1.NetworkClass{networkClass}}
		hubs := &fakeHubsListClient{items: []*privatev1.Hub{testHub("hub-a"), testHub("hub-b")}}
		cache := &fakeNetworkingHubCache{}

		resolver := mustBuildNetworkingHubResolver(networkClasses, hubs, cache)

		result, err := resolver.Resolve(ctx)

		Expect(errors.Is(err, ErrMultipleNetworkingHubs)).To(BeTrue())
		Expect(result.State).To(Equal(privatev1.NetworkClassState_NETWORK_CLASS_STATE_PENDING))
		Expect(result.Message).To(Equal("expected exactly one active networking hub, found multiple"))
		Expect(networkClasses.updates).To(BeEmpty())
		Expect(cache.calls).To(BeEmpty())
	})

	It("uses the persisted canonical reference without listing Hubs", func() {
		networkClass := testNetworkClass("nc-a", "hub-a", privatev1.NetworkClassState_NETWORK_CLASS_STATE_PENDING, "old message")
		networkClasses := &fakeNetworkClassesClient{objects: []*privatev1.NetworkClass{networkClass}}
		hubs := &fakeHubsListClient{items: []*privatev1.Hub{testHub("hub-b")}}
		cache := &fakeNetworkingHubCache{entries: map[string]*HubEntry{
			"hub-a": {Namespace: "canonical", Client: nil},
		}}

		resolver := mustBuildNetworkingHubResolver(networkClasses, hubs, cache)

		result, err := resolver.Resolve(ctx)

		Expect(err).ToNot(HaveOccurred())
		Expect(result.ID).To(Equal("hub-a"))
		Expect(result.Namespace).To(Equal("canonical"))
		Expect(hubs.listCall).To(Equal(0))
		Expect(result.State).To(Equal(privatev1.NetworkClassState_NETWORK_CLASS_STATE_READY))
		Expect(result.Message).To(BeEmpty())
		Expect(networkClasses.updates).To(BeEmpty())
	})

	It("does not rewrite an already healthy canonical status", func() {
		networkClass := testNetworkClass("nc-a", "hub-a", privatev1.NetworkClassState_NETWORK_CLASS_STATE_READY, "")
		networkClasses := &fakeNetworkClassesClient{objects: []*privatev1.NetworkClass{networkClass}}
		hubs := &fakeHubsListClient{items: []*privatev1.Hub{testHub("hub-b")}}
		cache := &fakeNetworkingHubCache{entries: map[string]*HubEntry{
			"hub-a": {Namespace: "canonical", Client: nil},
		}}

		resolver := mustBuildNetworkingHubResolver(networkClasses, hubs, cache)

		result, err := resolver.Resolve(ctx)

		Expect(err).ToNot(HaveOccurred())
		Expect(result.ID).To(Equal("hub-a"))
		Expect(result.State).To(Equal(privatev1.NetworkClassState_NETWORK_CLASS_STATE_READY))
		Expect(networkClasses.updates).To(BeEmpty())
	})

	It("re-evaluates active Hubs for each writable resolution", func() {
		networkClass := testNetworkClass("nc-a", "", privatev1.NetworkClassState_NETWORK_CLASS_STATE_READY, "")
		networkClasses := &fakeNetworkClassesClient{objects: []*privatev1.NetworkClass{networkClass}}
		hubs := &fakeHubsListClient{}
		cache := &fakeNetworkingHubCache{entries: map[string]*HubEntry{
			"hub-a": {Namespace: "canonical", Client: nil},
			"hub-b": {Namespace: "canonical", Client: nil},
		}}

		resolver := mustBuildNetworkingHubResolver(networkClasses, hubs, cache)

		hubs.items = []*privatev1.Hub{testHub("hub-a")}
		first, err := resolver.Resolve(ctx)
		Expect(err).ToNot(HaveOccurred())
		hubs.items = []*privatev1.Hub{testHub("hub-b")}
		networkClass.GetStatus().SetHub("")
		second, err := resolver.Resolve(ctx)
		Expect(err).ToNot(HaveOccurred())

		Expect(first.ID).To(Equal("hub-a"))
		Expect(second.ID).To(Equal("hub-b"))
		Expect(networkClasses.listCalls).To(Equal(2))
		Expect(hubs.listCall).To(Equal(2))
		Expect(cache.calls).To(Equal([]string{"hub-a", "hub-b"}))
	})

	It("re-evaluates a recent resolution failure for each writable resolution", func() {
		networkClass := testNetworkClass("nc-a", "", privatev1.NetworkClassState_NETWORK_CLASS_STATE_READY, "")
		networkClasses := &fakeNetworkClassesClient{objects: []*privatev1.NetworkClass{networkClass}}
		hubs := &fakeHubsListClient{}
		cache := &fakeNetworkingHubCache{}

		resolver := mustBuildNetworkingHubResolver(networkClasses, hubs, cache)

		_, firstErr := resolver.Resolve(ctx)
		_, secondErr := resolver.Resolve(ctx)

		Expect(errors.Is(firstErr, ErrNoNetworkingHubs)).To(BeTrue())
		Expect(errors.Is(secondErr, ErrNoNetworkingHubs)).To(BeTrue())
		Expect(networkClasses.listCalls).To(Equal(2))
		Expect(hubs.listCall).To(Equal(2))
		Expect(networkClasses.updates).To(BeEmpty())
	})

	It("reports an invalid canonical reference without falling back", func() {
		networkClass := testNetworkClass("nc-a", "missing", privatev1.NetworkClassState_NETWORK_CLASS_STATE_PENDING, "")
		networkClasses := &fakeNetworkClassesClient{objects: []*privatev1.NetworkClass{networkClass}}
		hubs := &fakeHubsListClient{items: []*privatev1.Hub{testHub("hub-a")}}
		cache := &fakeNetworkingHubCache{errors: map[string]error{
			"missing": ErrHubNotFound,
		}}

		resolver := mustBuildNetworkingHubResolver(networkClasses, hubs, cache)

		result, err := resolver.Resolve(ctx)

		Expect(errors.Is(err, ErrCanonicalHubNotFound)).To(BeTrue())
		Expect(hubs.listCall).To(Equal(0))
		Expect(cache.calls).To(Equal([]string{"missing"}))
		Expect(result.HubID).To(Equal("missing"))
		Expect(result.State).To(Equal(privatev1.NetworkClassState_NETWORK_CLASS_STATE_FAILED))
		Expect(networkClasses.updates).To(BeEmpty())
	})

	It("reports an unavailable canonical reference without falling back", func() {
		networkClass := testNetworkClass("nc-a", "hub-a", privatev1.NetworkClassState_NETWORK_CLASS_STATE_READY, "")
		networkClasses := &fakeNetworkClassesClient{objects: []*privatev1.NetworkClass{networkClass}}
		hubs := &fakeHubsListClient{items: []*privatev1.Hub{testHub("hub-b")}}
		cache := &fakeNetworkingHubCache{errors: map[string]error{
			"hub-a": errors.New("kubeconfig endpoint unavailable"),
		}}

		resolver := mustBuildNetworkingHubResolver(networkClasses, hubs, cache)

		result, err := resolver.Resolve(ctx)

		Expect(errors.Is(err, ErrCanonicalHubUnavailable)).To(BeTrue())
		Expect(hubs.listCall).To(Equal(0))
		Expect(cache.calls).To(Equal([]string{"hub-a"}))
		Expect(result.HubID).To(Equal("hub-a"))
		Expect(result.State).To(Equal(privatev1.NetworkClassState_NETWORK_CLASS_STATE_PENDING))
		Expect(networkClasses.updates).To(BeEmpty())
	})

	It("reports no NetworkClass when the provider singleton is absent", func() {
		networkClasses := &fakeNetworkClassesClient{}
		hubs := &fakeHubsListClient{items: []*privatev1.Hub{testHub("hub-a")}}
		cache := &fakeNetworkingHubCache{}

		resolver := mustBuildNetworkingHubResolver(networkClasses, hubs, cache)

		_, err := resolver.Resolve(ctx)

		Expect(errors.Is(err, ErrNoNetworkClass)).To(BeTrue())
		Expect(hubs.listCall).To(Equal(0))
		Expect(cache.calls).To(BeEmpty())
	})

	It("reports multiple NetworkClasses without selecting one", func() {
		networkClasses := &fakeNetworkClassesClient{objects: []*privatev1.NetworkClass{
			testNetworkClass("nc-a", "", privatev1.NetworkClassState_NETWORK_CLASS_STATE_READY, ""),
			testNetworkClass("nc-b", "", privatev1.NetworkClassState_NETWORK_CLASS_STATE_READY, ""),
		}}
		hubs := &fakeHubsListClient{items: []*privatev1.Hub{testHub("hub-a")}}
		cache := &fakeNetworkingHubCache{}

		resolver := mustBuildNetworkingHubResolver(networkClasses, hubs, cache)

		_, err := resolver.Resolve(ctx)

		Expect(errors.Is(err, ErrMultipleNetworkClasses)).To(BeTrue())
		Expect(hubs.listCall).To(Equal(0))
		Expect(cache.calls).To(BeEmpty())
	})
})

var _ = Describe("NetworkingHubReader", func() {
	It("reads the persisted NetworkClass Hub without updating NetworkClass status", func() {
		networkClass := testNetworkClass("nc-a", "hub-a", privatev1.NetworkClassState_NETWORK_CLASS_STATE_READY, "")
		networkClasses := &fakeNetworkClassesClient{objects: []*privatev1.NetworkClass{networkClass}}
		hubs := &fakeHubsListClient{items: []*privatev1.Hub{testHub("hub-a")}}
		cache := &fakeNetworkingHubCache{entries: map[string]*HubEntry{
			"hub-a": {Namespace: "networking", Client: nil},
		}}

		reader := mustBuildNetworkingHubReader(networkClasses, cache)

		result, err := reader.Resolve(context.Background())

		Expect(err).ToNot(HaveOccurred())
		Expect(result.ID).To(Equal("hub-a"))
		Expect(result.Namespace).To(Equal("networking"))
		Expect(networkClasses.updates).To(BeEmpty())
		Expect(hubs.listCall).To(Equal(0))
		Expect(cache.calls).To(Equal([]string{"hub-a"}))
	})

	It("does not discover or mutate a NetworkClass whose canonical Hub is not ready", func() {
		networkClass := testNetworkClass("nc-a", "", privatev1.NetworkClassState_NETWORK_CLASS_STATE_PENDING, "")
		networkClasses := &fakeNetworkClassesClient{objects: []*privatev1.NetworkClass{networkClass}}
		cache := &fakeNetworkingHubCache{}

		reader := mustBuildNetworkingHubReader(networkClasses, cache)

		_, err := reader.Resolve(context.Background())

		Expect(errors.Is(err, ErrCanonicalHubNotReady)).To(BeTrue())
		Expect(networkClasses.updates).To(BeEmpty())
		Expect(cache.calls).To(BeEmpty())
	})
})

func mustBuildNetworkingHubResolver(
	networkClasses *fakeNetworkClassesClient,
	hubs *fakeHubsListClient,
	cache *fakeNetworkingHubCache,
) NetworkClassHubResolver {
	resolver, err := NewNetworkingHubResolver().
		SetNetworkClassesClient(networkClasses).
		SetHubsClient(hubs).
		SetHubCache(cache).
		Build()
	if err != nil {
		panic(err)
	}
	return resolver
}

func mustBuildNetworkingHubReader(
	networkClasses *fakeNetworkClassesClient,
	cache *fakeNetworkingHubCache,
) NetworkingHubReader {
	reader, err := NewNetworkingHubReader().
		SetNetworkClassesClient(networkClasses).
		SetHubCache(cache).
		Build()
	Expect(err).ToNot(HaveOccurred())
	return reader
}

func testNetworkClass(id, hub string, state privatev1.NetworkClassState, message string) *privatev1.NetworkClass {
	status := privatev1.NetworkClassStatus_builder{State: state, Hub: hub}
	if message != "" {
		status.Message = &message
	}
	return privatev1.NetworkClass_builder{
		Id:     id,
		Status: status.Build(),
	}.Build()
}

func testHub(id string) *privatev1.Hub {
	return privatev1.Hub_builder{Id: id}.Build()
}

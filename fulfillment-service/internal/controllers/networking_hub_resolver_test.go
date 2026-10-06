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
	"fmt"

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

type fixedNetworkClassHubResolver struct {
	resolution NetworkingHubResolution
	err        error
	calls      int
}

func (f *fixedNetworkClassHubResolver) Resolve(context.Context) (NetworkingHubResolution, error) {
	f.calls++
	return f.resolution, f.err
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

	It("observes a changed persisted NetworkClass Hub on the next resolution", func() {
		networkClass := testNetworkClass("nc-a", "hub-a", privatev1.NetworkClassState_NETWORK_CLASS_STATE_READY, "")
		networkClasses := &fakeNetworkClassesClient{objects: []*privatev1.NetworkClass{networkClass}}
		cache := &fakeNetworkingHubCache{entries: map[string]*HubEntry{
			"hub-a": {Namespace: "networking-a", Client: nil},
			"hub-b": {Namespace: "networking-b", Client: nil},
		}}

		reader := mustBuildNetworkingHubReader(networkClasses, cache)

		first, err := reader.Resolve(context.Background())
		Expect(err).ToNot(HaveOccurred())
		Expect(first.ID).To(Equal("hub-a"))

		networkClass.GetStatus().SetHub("hub-b")
		second, err := reader.Resolve(context.Background())

		Expect(err).ToNot(HaveOccurred())
		Expect(second.ID).To(Equal("hub-b"))
		Expect(second.Namespace).To(Equal("networking-b"))
		Expect(networkClasses.listRequests).To(HaveLen(2))
		Expect(networkClasses.updates).To(BeEmpty())
		Expect(cache.calls).To(Equal([]string{"hub-a", "hub-b"}))
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

var _ = Describe("ResolveResourceNetworkingHub", func() {
	It("uses the canonical Hub when the resource has no assignment", func() {
		resolver := &fixedNetworkClassHubResolver{resolution: NetworkingHubResolution{
			NetworkingHub: NetworkingHub{ID: "hub-a", Namespace: "networking"},
			HubID:         "hub-a",
			State:         privatev1.NetworkClassState_NETWORK_CLASS_STATE_READY,
		}}

		result, err := ResolveResourceNetworkingHub(context.Background(), resolver, "")

		Expect(err).ToNot(HaveOccurred())
		Expect(result.HubID).To(Equal("hub-a"))
		Expect(result.ID).To(Equal("hub-a"))
		Expect(result.Namespace).To(Equal("networking"))
		Expect(resolver.calls).To(Equal(1))
	})

	It("retains an assignment that matches the canonical Hub", func() {
		resolver := &fixedNetworkClassHubResolver{resolution: NetworkingHubResolution{
			NetworkingHub: NetworkingHub{ID: "hub-a", Namespace: "networking"},
			HubID:         "hub-a",
			State:         privatev1.NetworkClassState_NETWORK_CLASS_STATE_READY,
		}}

		result, err := ResolveResourceNetworkingHub(context.Background(), resolver, "hub-a")

		Expect(err).ToNot(HaveOccurred())
		Expect(result.ID).To(Equal("hub-a"))
		Expect(result.Namespace).To(Equal("networking"))
		Expect(resolver.calls).To(Equal(1))
	})

	It("returns a deterministic conflict when the assignment differs from the canonical Hub", func() {
		resolver := &fixedNetworkClassHubResolver{resolution: NetworkingHubResolution{
			NetworkingHub: NetworkingHub{ID: "hub-a", Namespace: "networking"},
			HubID:         "hub-a",
			State:         privatev1.NetworkClassState_NETWORK_CLASS_STATE_READY,
		}}

		result, err := ResolveResourceNetworkingHub(context.Background(), resolver, "hub-b")

		Expect(errors.Is(err, ErrResourceHubConflict)).To(BeTrue())
		Expect(err.Error()).To(Equal(`resource Hub assignment conflicts with canonical networking Hub: assigned "hub-b", canonical "hub-a"`))
		Expect(result.HubID).To(Equal("hub-a"))
		Expect(result.State).To(Equal(privatev1.NetworkClassState_NETWORK_CLASS_STATE_READY))
		Expect(result.Message).To(BeEmpty())
	})

	It("preserves a pending resolver result when the canonical Hub is unavailable", func() {
		resolverErr := fmt.Errorf("canonical Hub: %w", ErrCanonicalHubUnavailable)
		resolver := &fixedNetworkClassHubResolver{
			resolution: NetworkingHubResolution{
				HubID:   "hub-a",
				State:   privatev1.NetworkClassState_NETWORK_CLASS_STATE_PENDING,
				Message: "canonical networking hub \"hub-a\" is unavailable",
			},
			err: resolverErr,
		}

		result, err := ResolveResourceNetworkingHub(context.Background(), resolver, "hub-b")

		Expect(errors.Is(err, ErrCanonicalHubUnavailable)).To(BeTrue())
		Expect(err).To(MatchError(resolverErr))
		Expect(result.State).To(Equal(privatev1.NetworkClassState_NETWORK_CLASS_STATE_PENDING))
		Expect(result.Message).To(Equal("canonical networking hub \"hub-a\" is unavailable"))
	})

	It("preserves a non-ready resolver result when no error is returned", func() {
		resolver := &fixedNetworkClassHubResolver{resolution: NetworkingHubResolution{
			HubID:   "hub-a",
			State:   privatev1.NetworkClassState_NETWORK_CLASS_STATE_PENDING,
			Message: "canonical Hub is still initializing",
		}}

		result, err := ResolveResourceNetworkingHub(context.Background(), resolver, "")

		Expect(errors.Is(err, ErrCanonicalHubNotReady)).To(BeTrue())
		Expect(result).To(Equal(resolver.resolution))
	})

	It("rejects a ready result with an incomplete or inconsistent Hub identity", func() {
		for _, resolution := range []NetworkingHubResolution{
			{
				HubID: "hub-a",
				State: privatev1.NetworkClassState_NETWORK_CLASS_STATE_READY,
			},
			{
				NetworkingHub: NetworkingHub{ID: "hub-b"},
				HubID:         "hub-a",
				State:         privatev1.NetworkClassState_NETWORK_CLASS_STATE_READY,
			},
		} {
			resolver := &fixedNetworkClassHubResolver{resolution: resolution}

			result, err := ResolveResourceNetworkingHub(context.Background(), resolver, "")

			Expect(errors.Is(err, ErrCanonicalHubNotReady)).To(BeTrue())
			Expect(result).To(Equal(resolution))
		}
	})

	It("resolves the canonical Hub on every call", func() {
		resolver := &fixedNetworkClassHubResolver{resolution: NetworkingHubResolution{
			NetworkingHub: NetworkingHub{ID: "hub-a", Namespace: "networking"},
			HubID:         "hub-a",
			State:         privatev1.NetworkClassState_NETWORK_CLASS_STATE_READY,
		}}

		_, firstErr := ResolveResourceNetworkingHub(context.Background(), resolver, "")
		_, secondErr := ResolveResourceNetworkingHub(context.Background(), resolver, "hub-a")

		Expect(firstErr).ToNot(HaveOccurred())
		Expect(secondErr).ToNot(HaveOccurred())
		Expect(resolver.calls).To(Equal(2))
	})
})

var _ = Describe("HandleResourceNetworkingHubResolutionError", func() {
	It("maps conflicting and permanently missing Hub assignments to resource failure", func() {
		for _, resolutionErr := range []error{ErrResourceHubConflict, ErrCanonicalHubNotFound} {
			var pendingErr, failedErr error

			handled, retry := HandleResourceNetworkingHubResolutionError(
				resolutionErr,
				func(err error) { pendingErr = err },
				func(err error) { failedErr = err },
			)

			Expect(handled).To(BeTrue())
			Expect(retry).To(BeFalse())
			Expect(pendingErr).ToNot(HaveOccurred())
			Expect(errors.Is(failedErr, resolutionErr)).To(BeTrue())
		}
	})

	It("maps unresolved or unavailable canonical state to resource pending", func() {
		for _, resolutionErr := range []error{
			ErrCanonicalHubNotReady,
			ErrCanonicalHubUnavailable,
			ErrNoNetworkClass,
			ErrMultipleNetworkClasses,
			ErrNoNetworkingHubs,
			ErrMultipleNetworkingHubs,
		} {
			var pendingErr, failedErr error

			handled, retry := HandleResourceNetworkingHubResolutionError(
				resolutionErr,
				func(err error) { pendingErr = err },
				func(err error) { failedErr = err },
			)

			Expect(handled).To(BeTrue())
			Expect(retry).To(BeTrue())
			Expect(errors.Is(pendingErr, resolutionErr)).To(BeTrue())
			Expect(failedErr).ToNot(HaveOccurred())
		}
	})

	It("keeps internal Hub resolution details out of resource status messages", func() {
		resolutionErr := fmt.Errorf("%w: kubeconfig secret id=%s", ErrCanonicalHubUnavailable, "sensitive-secret-id")
		var pendingErr error

		handled, retry := HandleResourceNetworkingHubResolutionError(
			resolutionErr,
			func(err error) { pendingErr = err },
			func(error) {},
		)

		Expect(handled).To(BeTrue())
		Expect(retry).To(BeTrue())
		Expect(pendingErr).To(Equal(ErrCanonicalHubUnavailable))
		Expect(pendingErr.Error()).ToNot(ContainSubstring("sensitive-secret-id"))
	})

	It("leaves unrelated errors for the controller retry path", func() {
		resolutionErr := errors.New("database request failed")
		called := false

		handled, retry := HandleResourceNetworkingHubResolutionError(
			resolutionErr,
			func(error) { called = true },
			func(error) { called = true },
		)

		Expect(handled).To(BeFalse())
		Expect(retry).To(BeFalse())
		Expect(called).To(BeFalse())
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

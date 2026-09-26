/*
Copyright (c) 2026 Red Hat Inc.

Licensed under the Apache License, Version 2.0 (the "License"); you may not use a copy of this file except in compliance with the
License. You may obtain a copy of the License at

  http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on an
"AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the specific
language governing permissions and limitations under the License.
*/

package networkclass

import (
	"context"
	"errors"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"google.golang.org/grpc"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"

	"github.com/osac-project/osac/fulfillment-service/internal/controllers"
	privatev1 "github.com/osac-project/osac/proto/gen/osac/private/v1"
)

type fakeNetworkClassHubResolver struct {
	calls  int
	result controllers.NetworkingHubResolution
	err    error
}

func (f *fakeNetworkClassHubResolver) Resolve(context.Context) (controllers.NetworkingHubResolution, error) {
	f.calls++
	if f.result.State == privatev1.NetworkClassState_NETWORK_CLASS_STATE_UNSPECIFIED {
		f.result = controllers.NetworkingHubResolution{
			NetworkingHub: controllers.NetworkingHub{ID: "hub-a"},
			HubID:         "hub-a",
			State:         privatev1.NetworkClassState_NETWORK_CLASS_STATE_READY,
		}
	}
	return f.result, f.err
}

type fakeNetworkClassStatusClient struct {
	updates []*privatev1.NetworkClassesUpdateRequest
	err     error
}

func (f *fakeNetworkClassStatusClient) Update(
	_ context.Context,
	request *privatev1.NetworkClassesUpdateRequest,
	_ ...grpc.CallOption,
) (*privatev1.NetworkClassesUpdateResponse, error) {
	f.updates = append(f.updates, proto.Clone(request).(*privatev1.NetworkClassesUpdateRequest))
	if f.err != nil {
		return nil, f.err
	}
	return privatev1.NetworkClassesUpdateResponse_builder{Object: request.GetObject()}.Build(), nil
}

var _ = Describe("NetworkClass reconciler", func() {
	It("delegates canonical Hub resolution to the NetworkClass reconciler", func() {
		resolver := &fakeNetworkClassHubResolver{}
		client := &fakeNetworkClassStatusClient{}
		reconcile, err := NewFunction().
			SetLogger(logger).
			SetResolver(resolver).
			SetNetworkClassesClient(client).
			Build()
		Expect(err).ToNot(HaveOccurred())

		networkClass := privatev1.NetworkClass_builder{
			Id: "nc-a",
			Status: privatev1.NetworkClassStatus_builder{
				State: privatev1.NetworkClassState_NETWORK_CLASS_STATE_PENDING,
			}.Build(),
		}.Build()

		Expect(reconcile(context.Background(), networkClass)).To(Succeed())
		Expect(resolver.calls).To(Equal(1))
		Expect(client.updates).To(HaveLen(1))
		Expect(client.updates[0].GetObject().GetStatus().GetHub()).To(Equal("hub-a"))
		Expect(client.updates[0].GetObject().GetStatus().GetState()).To(
			Equal(privatev1.NetworkClassState_NETWORK_CLASS_STATE_READY))
		Expect(client.updates[0].GetUpdateMask().GetPaths()).To(ConsistOf(
			"status.hub", "status.state", "status.message"))
		Expect(client.updates[0].GetLock()).To(BeTrue())
	})

	It("does not write status when the resolution is already persisted", func() {
		resolver := &fakeNetworkClassHubResolver{
			result: controllers.NetworkingHubResolution{
				NetworkingHub: controllers.NetworkingHub{ID: "hub-a"},
				HubID:         "hub-a",
				State:         privatev1.NetworkClassState_NETWORK_CLASS_STATE_READY,
			},
		}
		client := &fakeNetworkClassStatusClient{}
		reconcile, err := NewFunction().
			SetLogger(logger).
			SetResolver(resolver).
			SetNetworkClassesClient(client).
			Build()
		Expect(err).ToNot(HaveOccurred())

		networkClass := privatev1.NetworkClass_builder{
			Id: "nc-a",
			Status: privatev1.NetworkClassStatus_builder{
				Hub:   "hub-a",
				State: privatev1.NetworkClassState_NETWORK_CLASS_STATE_READY,
			}.Build(),
		}.Build()

		Expect(reconcile(context.Background(), networkClass)).To(Succeed())
		Expect(client.updates).To(BeEmpty())
	})

	It("does not resolve a deleted NetworkClass", func() {
		resolver := &fakeNetworkClassHubResolver{}
		client := &fakeNetworkClassStatusClient{}
		reconcile, err := NewFunction().
			SetLogger(logger).
			SetResolver(resolver).
			SetNetworkClassesClient(client).
			Build()
		Expect(err).ToNot(HaveOccurred())

		networkClass := privatev1.NetworkClass_builder{
			Id: "nc-a",
			Metadata: privatev1.Metadata_builder{
				DeletionTimestamp: timestamppb.Now(),
			}.Build(),
		}.Build()

		Expect(reconcile(context.Background(), networkClass)).To(Succeed())
		Expect(resolver.calls).To(Equal(0))
	})

	It("clears a stale Hub when discovery has no candidates", func() {
		resolver := &fakeNetworkClassHubResolver{
			result: controllers.NetworkingHubResolution{
				State:   privatev1.NetworkClassState_NETWORK_CLASS_STATE_PENDING,
				Message: "expected exactly one active networking hub, found none",
			},
			err: controllers.ErrNoNetworkingHubs,
		}
		client := &fakeNetworkClassStatusClient{}
		reconcile, err := NewFunction().
			SetLogger(logger).
			SetResolver(resolver).
			SetNetworkClassesClient(client).
			Build()
		Expect(err).ToNot(HaveOccurred())

		networkClass := privatev1.NetworkClass_builder{
			Id: "nc-a",
			Status: privatev1.NetworkClassStatus_builder{
				Hub:   "previous-hub",
				State: privatev1.NetworkClassState_NETWORK_CLASS_STATE_READY,
			}.Build(),
		}.Build()

		Expect(reconcile(context.Background(), networkClass)).To(MatchError(controllers.ErrNoNetworkingHubs))
		Expect(client.updates).To(HaveLen(1))
		Expect(client.updates[0].GetObject().GetStatus().GetState()).To(
			Equal(privatev1.NetworkClassState_NETWORK_CLASS_STATE_PENDING))
		Expect(client.updates[0].GetObject().GetStatus().GetHub()).To(BeEmpty())
		Expect(client.updates[0].GetObject().GetStatus().GetMessage()).To(
			Equal("expected exactly one active networking hub, found none"))
	})

	It("preserves a persisted Hub and marks the NetworkClass failed when it is not registered", func() {
		resolver := &fakeNetworkClassHubResolver{
			result: controllers.NetworkingHubResolution{
				HubID:   "missing",
				State:   privatev1.NetworkClassState_NETWORK_CLASS_STATE_FAILED,
				Message: "canonical networking hub \"missing\" is not registered",
			},
			err: errors.Join(controllers.ErrCanonicalHubNotFound, errors.New("missing")),
		}
		client := &fakeNetworkClassStatusClient{}
		reconcile, err := NewFunction().
			SetLogger(logger).
			SetResolver(resolver).
			SetNetworkClassesClient(client).
			Build()
		Expect(err).ToNot(HaveOccurred())

		networkClass := privatev1.NetworkClass_builder{
			Id: "nc-a",
			Status: privatev1.NetworkClassStatus_builder{
				Hub:   "missing",
				State: privatev1.NetworkClassState_NETWORK_CLASS_STATE_PENDING,
			}.Build(),
		}.Build()

		Expect(reconcile(context.Background(), networkClass)).To(HaveOccurred())
		Expect(client.updates).To(HaveLen(1))
		Expect(client.updates[0].GetObject().GetStatus().GetHub()).To(Equal("missing"))
		Expect(client.updates[0].GetObject().GetStatus().GetState()).To(
			Equal(privatev1.NetworkClassState_NETWORK_CLASS_STATE_FAILED))
	})

	It("returns the status update error without hiding the resolution error", func() {
		resolver := &fakeNetworkClassHubResolver{
			result: controllers.NetworkingHubResolution{
				State:   privatev1.NetworkClassState_NETWORK_CLASS_STATE_PENDING,
				Message: "expected exactly one active networking hub, found none",
			},
			err: controllers.ErrNoNetworkingHubs,
		}
		client := &fakeNetworkClassStatusClient{err: errors.New("optimistic lock failed")}
		reconcile, err := NewFunction().
			SetLogger(logger).
			SetResolver(resolver).
			SetNetworkClassesClient(client).
			Build()
		Expect(err).ToNot(HaveOccurred())

		networkClass := privatev1.NetworkClass_builder{Id: "nc-a"}.Build()

		err = reconcile(context.Background(), networkClass)
		Expect(errors.Is(err, controllers.ErrNoNetworkingHubs)).To(BeTrue())
		Expect(err.Error()).To(ContainSubstring("failed to update network class status"))
	})
})

/*
Copyright (c) 2026 Red Hat Inc.

Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except in compliance with the
License. You may obtain a copy of the License at

  http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on an
"AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the specific
language governing permissions and limitations under the License.
*/

package servers

import (
	"context"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"go.uber.org/mock/gomock"
	"google.golang.org/grpc/codes"
	grpcmetadata "google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"

	privatev1 "github.com/osac-project/fulfillment-service/internal/api/osac/private/v1"
	"github.com/osac-project/fulfillment-service/internal/auth"
	"github.com/osac-project/fulfillment-service/internal/events"
)

var _ = Describe("Generic server", func() {
	var ctrl *gomock.Controller

	BeforeEach(func() {
		ctrl = gomock.NewController(GinkgoT())
		DeferCleanup(ctrl.Finish)
	})

	It("Sets the payload via reflection for a simple object type", func() {
		// Create a mock notifier that captures the event:
		var event *privatev1.Event
		notifier := events.NewMockNotifier(ctrl)
		notifier.EXPECT().
			Notify(gomock.Any(), gomock.Any()).
			DoAndReturn(
				func(ctx context.Context, payload proto.Message) error {
					event = payload.(*privatev1.Event)
					return nil
				},
			)

		// Create the server:
		server, err := NewGenericServer[*privatev1.HostType]().
			SetLogger(logger).
			SetService(privatev1.HostTypes_ServiceDesc.ServiceName).
			SetAttributionLogic(attribution).
			SetTenancyLogic(tenancy).
			SetNotifier(notifier).
			Build()
		Expect(err).ToNot(HaveOccurred())

		// Create the object:
		response := &privatev1.HostTypesCreateResponse{}
		err = server.Create(
			ctx,
			privatev1.HostTypesCreateRequest_builder{
				Object: privatev1.HostType_builder{
					Metadata: privatev1.Metadata_builder{
						Name: "my-object",
					}.Build(),
				}.Build(),
			}.Build(),
			&response,
		)
		Expect(err).ToNot(HaveOccurred())

		// Verify the event:
		Expect(event).ToNot(BeNil())
		Expect(event.GetType()).To(Equal(privatev1.EventType_EVENT_TYPE_OBJECT_CREATED))
		object := event.GetHostType()
		Expect(object).ToNot(BeNil())
		metadata := object.GetMetadata()
		Expect(metadata).ToNot(BeNil())
		Expect(metadata.GetName()).To(Equal("my-object"))
	})

	It("Redacts the payload", func() {
		// Create a mock notifier that captures the event:
		var event *privatev1.Event
		notifier := events.NewMockNotifier(ctrl)
		notifier.EXPECT().
			Notify(gomock.Any(), gomock.Any()).
			DoAndReturn(
				func(ctx context.Context, payload proto.Message) error {
					event = payload.(*privatev1.Event)
					return nil
				},
			)

		// Create the server with a redact function that clears some field from the object:
		server, err := NewGenericServer[*privatev1.HostType]().
			SetLogger(logger).
			SetService(privatev1.HostTypes_ServiceDesc.ServiceName).
			SetAttributionLogic(attribution).
			SetTenancyLogic(tenancy).
			SetNotifier(notifier).
			SetRedactFunc(
				func(object *privatev1.HostType) *privatev1.HostType {
					object.SetDescription("***")
					return object
				},
			).
			Build()
		Expect(err).ToNot(HaveOccurred())

		// Create the object:
		object := privatev1.HostType_builder{
			Metadata: privatev1.Metadata_builder{
				Name: "my-object",
			}.Build(),
			Description: "My description.",
		}.Build()
		request := privatev1.HostTypesCreateRequest_builder{
			Object: object,
		}.Build()
		response := &privatev1.HostTypesCreateResponse{}
		err = server.Create(ctx, request, &response)
		Expect(err).ToNot(HaveOccurred())

		// Verify the the payload has been redacted:
		Expect(event).ToNot(BeNil())
		Expect(event.GetType()).To(Equal(privatev1.EventType_EVENT_TYPE_OBJECT_CREATED))
		Expect(event.GetHostType()).ToNot(BeNil())
		Expect(event.GetHostType().GetDescription()).To(Equal("***"))

		// Verify that the original object has not been modified:
		Expect(object.GetDescription()).To(Equal("My description."))
	})
})

var _ = Describe("Generic server dry run", func() {
	var server *GenericServer[*privatev1.HostType]

	BeforeEach(func() {
		var err error
		notifier := events.NewMockNotifier(gomock.NewController(GinkgoT()))
		server, err = NewGenericServer[*privatev1.HostType]().
			SetLogger(logger).
			SetService(privatev1.HostTypes_ServiceDesc.ServiceName).
			SetAttributionLogic(attribution).
			SetTenancyLogic(tenancy).
			SetNotifier(notifier).
			Build()
		Expect(err).ToNot(HaveOccurred())
	})

	It("Assigns creator and tenant to the object", func() {
		response := &privatev1.HostTypesCreateResponse{}
		err := server.Create(
			dryRunCtx(),
			privatev1.HostTypesCreateRequest_builder{
				Object: privatev1.HostType_builder{
					Metadata: privatev1.Metadata_builder{
						Name: "my-dry-run-object",
					}.Build(),
				}.Build(),
			}.Build(),
			&response,
		)
		Expect(err).ToNot(HaveOccurred())
		Expect(response.GetObject()).ToNot(BeNil())
		Expect(response.GetObject().GetMetadata().GetName()).To(Equal("my-dry-run-object"))
		Expect(response.GetObject().GetMetadata().GetCreator()).To(Equal("system"))
		Expect(response.GetObject().GetMetadata().GetTenant()).To(Equal(auth.SystemTenant))
	})

	It("Validates metadata and rejects invalid labels", func() {
		response := &privatev1.HostTypesCreateResponse{}
		err := server.Create(
			dryRunCtx(),
			privatev1.HostTypesCreateRequest_builder{
				Object: privatev1.HostType_builder{
					Metadata: privatev1.Metadata_builder{
						Name:   "valid-name",
						Labels: map[string]string{"!!!invalid": "value"},
					}.Build(),
				}.Build(),
			}.Build(),
			&response,
		)
		Expect(err).To(HaveOccurred())
		Expect(status.Code(err)).To(Equal(codes.InvalidArgument))
	})

	It("Does not persist the object", func() {
		response := &privatev1.HostTypesCreateResponse{}
		err := server.Create(
			dryRunCtx(),
			privatev1.HostTypesCreateRequest_builder{
				Object: privatev1.HostType_builder{
					Metadata: privatev1.Metadata_builder{
						Name: "dry-run-no-persist",
					}.Build(),
				}.Build(),
			}.Build(),
			&response,
		)
		Expect(err).ToNot(HaveOccurred())

		listResponse := &privatev1.HostTypesListResponse{}
		err = server.List(ctx,
			privatev1.HostTypesListRequest_builder{}.Build(),
			&listResponse,
		)
		Expect(err).ToNot(HaveOccurred())
		Expect(listResponse.GetTotal()).To(Equal(int32(0)))
	})

	It("Does not emit events", func() {
		response := &privatev1.HostTypesCreateResponse{}
		err := server.Create(
			dryRunCtx(),
			privatev1.HostTypesCreateRequest_builder{
				Object: privatev1.HostType_builder{
					Metadata: privatev1.Metadata_builder{
						Name: "dry-run-no-events",
					}.Build(),
				}.Build(),
			}.Build(),
			&response,
		)
		Expect(err).ToNot(HaveOccurred())
	})

	It("Persists normally when header value is false", func() {
		ctrl := gomock.NewController(GinkgoT())
		notifier := events.NewMockNotifier(ctrl)
		notifier.EXPECT().Notify(gomock.Any(), gomock.Any()).Return(nil)
		srv, err := NewGenericServer[*privatev1.HostType]().
			SetLogger(logger).
			SetService(privatev1.HostTypes_ServiceDesc.ServiceName).
			SetAttributionLogic(attribution).
			SetTenancyLogic(tenancy).
			SetNotifier(notifier).
			Build()
		Expect(err).ToNot(HaveOccurred())

		falseCtx := grpcmetadata.NewIncomingContext(ctx,
			grpcmetadata.Pairs(DryRunMetadataKey, "false"))

		response := &privatev1.HostTypesCreateResponse{}
		err = srv.Create(
			falseCtx,
			privatev1.HostTypesCreateRequest_builder{
				Object: privatev1.HostType_builder{
					Metadata: privatev1.Metadata_builder{
						Name: "not-a-dry-run",
					}.Build(),
				}.Build(),
			}.Build(),
			&response,
		)
		Expect(err).ToNot(HaveOccurred())

		listResponse := &privatev1.HostTypesListResponse{}
		err = srv.List(ctx,
			privatev1.HostTypesListRequest_builder{}.Build(),
			&listResponse,
		)
		Expect(err).ToNot(HaveOccurred())
		Expect(listResponse.GetTotal()).To(Equal(int32(1)))
	})
})

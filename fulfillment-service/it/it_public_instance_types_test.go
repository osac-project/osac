/*
Copyright (c) 2025 Red Hat Inc.

Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except in compliance with the
License. You may obtain a copy of the License at

  http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on an
"AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the specific
language governing permissions and limitations under the License.
*/

package it

import (
	"context"
	"fmt"

	. "github.com/onsi/ginkgo/v2/dsl/core"
	. "github.com/onsi/gomega"
	grpccodes "google.golang.org/grpc/codes"
	grpcstatus "google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/fieldmaskpb"

	privatev1 "github.com/osac-project/fulfillment-service/internal/api/osac/private/v1"
	publicv1 "github.com/osac-project/fulfillment-service/internal/api/osac/public/v1"
	"github.com/osac-project/fulfillment-service/internal/uuid"
)

var _ = Describe("Public instance types", func() {
	var (
		ctx           context.Context
		publicClient  publicv1.InstanceTypesClient
		privateClient privatev1.InstanceTypesClient
	)

	BeforeEach(func() {
		ctx = context.Background()
		publicClient = publicv1.NewInstanceTypesClient(tool.ExternalView().UserConn())
		privateClient = privatev1.NewInstanceTypesClient(tool.InternalView().AdminConn())
	})

	// createViaPrivate creates an instance type through the private API and returns its ID.
	// Registers a DeferCleanup to delete the instance type when the test completes.
	createViaPrivate := func(suffix string, cores int32, memoryGib int32) string {
		name := fmt.Sprintf("it-pub-%s-%s", suffix, uuid.New())
		_, err := privateClient.Create(ctx, privatev1.InstanceTypesCreateRequest_builder{
			Object: privatev1.InstanceType_builder{
				Metadata: privatev1.Metadata_builder{
					Name: name,
				}.Build(),
				Spec: privatev1.InstanceTypeSpec_builder{
					Cores:       cores,
					MemoryGib:   memoryGib,
					Description: "Public IT test type.",
				}.Build(),
			}.Build(),
		}.Build())
		Expect(err).ToNot(HaveOccurred())
		DeferCleanup(func() {
			_, _ = privateClient.Delete(ctx, privatev1.InstanceTypesDeleteRequest_builder{
				Id: name,
			}.Build())
		})
		return name
	}

	It("Can list instance types", func() {
		createViaPrivate("list", 2, 8)

		response, err := publicClient.List(ctx, publicv1.InstanceTypesListRequest_builder{}.Build())
		Expect(err).ToNot(HaveOccurred())
		Expect(response).ToNot(BeNil())
		Expect(response.GetItems()).ToNot(BeEmpty())
	})

	It("Can get an instance type", func() {
		id := createViaPrivate("get", 4, 16)

		response, err := publicClient.Get(ctx, publicv1.InstanceTypesGetRequest_builder{
			Id: id,
		}.Build())
		Expect(err).ToNot(HaveOccurred())
		Expect(response).ToNot(BeNil())
		object := response.GetObject()
		Expect(object).ToNot(BeNil())
		Expect(object.GetId()).To(Equal(id))
		Expect(object.GetSpec().GetCores()).To(Equal(int32(4)))
		Expect(object.GetSpec().GetMemoryGib()).To(Equal(int32(16)))
	})

	It("Can get an OBSOLETE instance type", func() {
		id := createViaPrivate("get-obs", 2, 4)

		// Transition to OBSOLETE via private API:
		_, err := privateClient.Update(ctx, privatev1.InstanceTypesUpdateRequest_builder{
			Object: privatev1.InstanceType_builder{
				Id: id,
				Spec: privatev1.InstanceTypeSpec_builder{
					State: privatev1.InstanceTypeState_INSTANCE_TYPE_STATE_DEPRECATED,
				}.Build(),
			}.Build(),
			UpdateMask: &fieldmaskpb.FieldMask{Paths: []string{"spec.state"}},
		}.Build())
		Expect(err).ToNot(HaveOccurred())

		_, err = privateClient.Update(ctx, privatev1.InstanceTypesUpdateRequest_builder{
			Object: privatev1.InstanceType_builder{
				Id: id,
				Spec: privatev1.InstanceTypeSpec_builder{
					State: privatev1.InstanceTypeState_INSTANCE_TYPE_STATE_OBSOLETE,
				}.Build(),
			}.Build(),
			UpdateMask: &fieldmaskpb.FieldMask{Paths: []string{"spec.state"}},
		}.Build())
		Expect(err).ToNot(HaveOccurred())

		// Public Get should still return OBSOLETE types:
		response, err := publicClient.Get(ctx, publicv1.InstanceTypesGetRequest_builder{
			Id: id,
		}.Build())
		Expect(err).ToNot(HaveOccurred())
		Expect(response.GetObject().GetSpec().GetState()).To(Equal(
			publicv1.InstanceTypeState_INSTANCE_TYPE_STATE_OBSOLETE))
	})

	It("Default list excludes OBSOLETE", func() {
		// Create 3 instance types:
		names := make([]string, 3)
		for i := range 3 {
			names[i] = createViaPrivate(fmt.Sprintf("filter-%d", i), 2, 4)
		}

		// Transition the third to OBSOLETE:
		_, err := privateClient.Update(ctx, privatev1.InstanceTypesUpdateRequest_builder{
			Object: privatev1.InstanceType_builder{
				Id: names[2],
				Spec: privatev1.InstanceTypeSpec_builder{
					State: privatev1.InstanceTypeState_INSTANCE_TYPE_STATE_DEPRECATED,
				}.Build(),
			}.Build(),
			UpdateMask: &fieldmaskpb.FieldMask{Paths: []string{"spec.state"}},
		}.Build())
		Expect(err).ToNot(HaveOccurred())

		_, err = privateClient.Update(ctx, privatev1.InstanceTypesUpdateRequest_builder{
			Object: privatev1.InstanceType_builder{
				Id: names[2],
				Spec: privatev1.InstanceTypeSpec_builder{
					State: privatev1.InstanceTypeState_INSTANCE_TYPE_STATE_OBSOLETE,
				}.Build(),
			}.Build(),
			UpdateMask: &fieldmaskpb.FieldMask{Paths: []string{"spec.state"}},
		}.Build())
		Expect(err).ToNot(HaveOccurred())

		// Public list scoped to our 3 names -- should return only 2 (OBSOLETE hidden):
		nameFilter := fmt.Sprintf(
			`this.metadata.name == %q || this.metadata.name == %q || this.metadata.name == %q`,
			names[0], names[1], names[2],
		)
		response, err := publicClient.List(ctx, publicv1.InstanceTypesListRequest_builder{
			Filter: &nameFilter,
		}.Build())
		Expect(err).ToNot(HaveOccurred())
		Expect(response.GetItems()).To(HaveLen(2))
	})

	It("List with explicit state filter includes OBSOLETE", func() {
		// Create an instance type and transition to OBSOLETE:
		id := createViaPrivate("filter-explicit", 2, 4)

		_, err := privateClient.Update(ctx, privatev1.InstanceTypesUpdateRequest_builder{
			Object: privatev1.InstanceType_builder{
				Id: id,
				Spec: privatev1.InstanceTypeSpec_builder{
					State: privatev1.InstanceTypeState_INSTANCE_TYPE_STATE_DEPRECATED,
				}.Build(),
			}.Build(),
			UpdateMask: &fieldmaskpb.FieldMask{Paths: []string{"spec.state"}},
		}.Build())
		Expect(err).ToNot(HaveOccurred())

		_, err = privateClient.Update(ctx, privatev1.InstanceTypesUpdateRequest_builder{
			Object: privatev1.InstanceType_builder{
				Id: id,
				Spec: privatev1.InstanceTypeSpec_builder{
					State: privatev1.InstanceTypeState_INSTANCE_TYPE_STATE_OBSOLETE,
				}.Build(),
			}.Build(),
			UpdateMask: &fieldmaskpb.FieldMask{Paths: []string{"spec.state"}},
		}.Build())
		Expect(err).ToNot(HaveOccurred())

		// Public list with explicit state filter should include it.
		// CEL types proto enums as int, so use the numeric value (3 = OBSOLETE):
		filter := fmt.Sprintf(
			`this.spec.state == %d && this.metadata.name == %q`,
			int32(publicv1.InstanceTypeState_INSTANCE_TYPE_STATE_OBSOLETE),
			id,
		)
		response, err := publicClient.List(ctx, publicv1.InstanceTypesListRequest_builder{
			Filter: &filter,
		}.Build())
		Expect(err).ToNot(HaveOccurred())
		Expect(response.GetItems()).To(HaveLen(1))
		Expect(response.GetItems()[0].GetId()).To(Equal(id))
	})

	It("Returns not found error when getting instance type that doesn't exist", func() {
		_, err := publicClient.Get(ctx, publicv1.InstanceTypesGetRequest_builder{
			Id: "does-not-exist",
		}.Build())
		Expect(err).To(HaveOccurred())
		status, ok := grpcstatus.FromError(err)
		Expect(ok).To(BeTrue())
		Expect(status.Code()).To(Equal(grpccodes.NotFound))
	})
})

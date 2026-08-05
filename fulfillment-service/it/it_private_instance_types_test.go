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

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	grpccodes "google.golang.org/grpc/codes"
	grpcstatus "google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/fieldmaskpb"

	privatev1 "github.com/osac-project/osac/fulfillment-service/internal/api/osac/private/v1"
	"github.com/osac-project/osac/fulfillment-service/internal/uuid"
)

var _ = Describe("Private instance types", func() {
	var (
		ctx    context.Context
		client privatev1.InstanceTypesClient
	)

	BeforeEach(func() {
		ctx = context.Background()
		client = privatev1.NewInstanceTypesClient(tool.InternalView().AdminConn())
	})

	// Happy path lifecycle (D-04)

	It("Can create an instance type", func() {
		name := fmt.Sprintf("it-type-%s", uuid.New())
		response, err := client.Create(ctx, privatev1.InstanceTypesCreateRequest_builder{
			Object: privatev1.InstanceType_builder{
				Metadata: privatev1.Metadata_builder{
					Name: name,
				}.Build(),
				Spec: privatev1.InstanceTypeSpec_builder{
					Cores:       4,
					MemoryGib:   16,
					Description: "Integration test type.",
				}.Build(),
			}.Build(),
		}.Build())
		Expect(err).ToNot(HaveOccurred())
		DeferCleanup(func() {
			_, _ = client.Delete(ctx, privatev1.InstanceTypesDeleteRequest_builder{
				Id: name,
			}.Build())
		})
		Expect(response).ToNot(BeNil())
		object := response.GetObject()
		Expect(object).ToNot(BeNil())
		Expect(object.GetId()).To(Equal(name))
		Expect(object.GetSpec().GetCores()).To(Equal(int32(4)))
		Expect(object.GetSpec().GetMemoryGib()).To(Equal(int32(16)))
		Expect(object.GetSpec().GetState()).To(Equal(
			privatev1.InstanceTypeState_INSTANCE_TYPE_STATE_ACTIVE))
		metadata := object.GetMetadata()
		Expect(metadata).ToNot(BeNil())
		Expect(metadata.HasCreationTimestamp()).To(BeTrue())
		Expect(metadata.HasDeletionTimestamp()).To(BeFalse())
	})

	It("Can list instance types", func() {
		// Create 3 instance types with unique names:
		for i := range 3 {
			name := fmt.Sprintf("it-list-%d-%s", i, uuid.New())
			_, err := client.Create(ctx, privatev1.InstanceTypesCreateRequest_builder{
				Object: privatev1.InstanceType_builder{
					Metadata: privatev1.Metadata_builder{
						Name: name,
					}.Build(),
					Spec: privatev1.InstanceTypeSpec_builder{
						Cores:       2,
						MemoryGib:   8,
						Description: fmt.Sprintf("List test type %d.", i),
					}.Build(),
				}.Build(),
			}.Build())
			Expect(err).ToNot(HaveOccurred())
			DeferCleanup(func() {
				_, _ = client.Delete(ctx, privatev1.InstanceTypesDeleteRequest_builder{
					Id: name,
				}.Build())
			})
		}

		// List all instance types:
		response, err := client.List(ctx, privatev1.InstanceTypesListRequest_builder{}.Build())
		Expect(err).ToNot(HaveOccurred())
		Expect(response).ToNot(BeNil())
		items := response.GetItems()
		Expect(len(items)).To(BeNumerically(">=", 3))
	})

	It("Can get an instance type", func() {
		name := fmt.Sprintf("it-get-%s", uuid.New())
		createResponse, err := client.Create(ctx, privatev1.InstanceTypesCreateRequest_builder{
			Object: privatev1.InstanceType_builder{
				Metadata: privatev1.Metadata_builder{
					Name: name,
				}.Build(),
				Spec: privatev1.InstanceTypeSpec_builder{
					Cores:       8,
					MemoryGib:   32,
					Description: "Get test type.",
				}.Build(),
			}.Build(),
		}.Build())
		Expect(err).ToNot(HaveOccurred())
		DeferCleanup(func() {
			_, _ = client.Delete(ctx, privatev1.InstanceTypesDeleteRequest_builder{
				Id: name,
			}.Build())
		})

		// Get the instance type and verify fields match:
		getResponse, err := client.Get(ctx, privatev1.InstanceTypesGetRequest_builder{
			Id: name,
		}.Build())
		Expect(err).ToNot(HaveOccurred())
		Expect(getResponse).ToNot(BeNil())
		object := getResponse.GetObject()
		Expect(object).ToNot(BeNil())
		Expect(object.GetId()).To(Equal(name))
		Expect(object.GetSpec().GetCores()).To(Equal(createResponse.GetObject().GetSpec().GetCores()))
		Expect(object.GetSpec().GetMemoryGib()).To(Equal(createResponse.GetObject().GetSpec().GetMemoryGib()))
		Expect(object.GetSpec().GetDescription()).To(Equal("Get test type."))
	})

	It("Can update an instance type", func() {
		name := fmt.Sprintf("it-update-%s", uuid.New())
		_, err := client.Create(ctx, privatev1.InstanceTypesCreateRequest_builder{
			Object: privatev1.InstanceType_builder{
				Metadata: privatev1.Metadata_builder{
					Name: name,
				}.Build(),
				Spec: privatev1.InstanceTypeSpec_builder{
					Cores:       4,
					MemoryGib:   16,
					Description: "Original description.",
				}.Build(),
			}.Build(),
		}.Build())
		Expect(err).ToNot(HaveOccurred())
		DeferCleanup(func() {
			_, _ = client.Delete(ctx, privatev1.InstanceTypesDeleteRequest_builder{
				Id: name,
			}.Build())
		})

		// Update the description:
		updateResponse, err := client.Update(ctx, privatev1.InstanceTypesUpdateRequest_builder{
			Object: privatev1.InstanceType_builder{
				Id: name,
				Spec: privatev1.InstanceTypeSpec_builder{
					Description: "Updated description.",
				}.Build(),
			}.Build(),
			UpdateMask: &fieldmaskpb.FieldMask{Paths: []string{"spec.description"}},
		}.Build())
		Expect(err).ToNot(HaveOccurred())
		Expect(updateResponse).ToNot(BeNil())
		Expect(updateResponse.GetObject().GetSpec().GetDescription()).To(Equal("Updated description."))

		// Get and verify persisted:
		getResponse, err := client.Get(ctx, privatev1.InstanceTypesGetRequest_builder{
			Id: name,
		}.Build())
		Expect(err).ToNot(HaveOccurred())
		Expect(getResponse.GetObject().GetSpec().GetDescription()).To(Equal("Updated description."))
	})

	It("Can delete an instance type", func() {
		name := fmt.Sprintf("it-delete-%s", uuid.New())
		_, err := client.Create(ctx, privatev1.InstanceTypesCreateRequest_builder{
			Object: privatev1.InstanceType_builder{
				Metadata: privatev1.Metadata_builder{
					Name:       name,
					Finalizers: []string{"a"},
				}.Build(),
				Spec: privatev1.InstanceTypeSpec_builder{
					Cores:       2,
					MemoryGib:   4,
					Description: "Delete test type.",
				}.Build(),
			}.Build(),
		}.Build())
		Expect(err).ToNot(HaveOccurred())
		DeferCleanup(func() {
			_, _ = client.Delete(ctx, privatev1.InstanceTypesDeleteRequest_builder{
				Id: name,
			}.Build())
		})

		// Delete it:
		deleteResponse, err := client.Delete(ctx, privatev1.InstanceTypesDeleteRequest_builder{
			Id: name,
		}.Build())
		Expect(err).ToNot(HaveOccurred())
		Expect(deleteResponse).ToNot(BeNil())

		// Get and verify deletion timestamp:
		getResponse, err := client.Get(ctx, privatev1.InstanceTypesGetRequest_builder{
			Id: name,
		}.Build())
		Expect(err).ToNot(HaveOccurred())
		Expect(getResponse).ToNot(BeNil())
		object := getResponse.GetObject()
		Expect(object).ToNot(BeNil())
		metadata := object.GetMetadata()
		Expect(metadata).ToNot(BeNil())
		Expect(metadata.HasDeletionTimestamp()).To(BeTrue())
	})

	// State transitions (D-04)

	It("Can transition state from ACTIVE to DEPRECATED", func() {
		name := fmt.Sprintf("it-dep-%s", uuid.New())
		_, err := client.Create(ctx, privatev1.InstanceTypesCreateRequest_builder{
			Object: privatev1.InstanceType_builder{
				Metadata: privatev1.Metadata_builder{
					Name: name,
				}.Build(),
				Spec: privatev1.InstanceTypeSpec_builder{
					Cores:       4,
					MemoryGib:   16,
					Description: "Deprecation test type.",
				}.Build(),
			}.Build(),
		}.Build())
		Expect(err).ToNot(HaveOccurred())
		DeferCleanup(func() {
			_, _ = client.Delete(ctx, privatev1.InstanceTypesDeleteRequest_builder{
				Id: name,
			}.Build())
		})

		// Transition to DEPRECATED:
		_, err = client.Update(ctx, privatev1.InstanceTypesUpdateRequest_builder{
			Object: privatev1.InstanceType_builder{
				Id: name,
				Spec: privatev1.InstanceTypeSpec_builder{
					State: privatev1.InstanceTypeState_INSTANCE_TYPE_STATE_DEPRECATED,
				}.Build(),
			}.Build(),
			UpdateMask: &fieldmaskpb.FieldMask{Paths: []string{"spec.state"}},
		}.Build())
		Expect(err).ToNot(HaveOccurred())

		// Get and verify:
		getResponse, err := client.Get(ctx, privatev1.InstanceTypesGetRequest_builder{
			Id: name,
		}.Build())
		Expect(err).ToNot(HaveOccurred())
		spec := getResponse.GetObject().GetSpec()
		Expect(spec.GetState()).To(Equal(
			privatev1.InstanceTypeState_INSTANCE_TYPE_STATE_DEPRECATED))
		dep := spec.GetDeprecation()
		Expect(dep).ToNot(BeNil())
		Expect(dep.HasDeprecationTimestamp()).To(BeTrue())
		Expect(dep.HasObsolescenceTimestamp()).To(BeFalse())
	})

	It("Can transition state from DEPRECATED to OBSOLETE", func() {
		name := fmt.Sprintf("it-obs-%s", uuid.New())
		_, err := client.Create(ctx, privatev1.InstanceTypesCreateRequest_builder{
			Object: privatev1.InstanceType_builder{
				Metadata: privatev1.Metadata_builder{
					Name: name,
				}.Build(),
				Spec: privatev1.InstanceTypeSpec_builder{
					Cores:       4,
					MemoryGib:   16,
					Description: "Obsolescence test type.",
				}.Build(),
			}.Build(),
		}.Build())
		Expect(err).ToNot(HaveOccurred())
		DeferCleanup(func() {
			_, _ = client.Delete(ctx, privatev1.InstanceTypesDeleteRequest_builder{
				Id: name,
			}.Build())
		})

		// Transition ACTIVE -> DEPRECATED:
		_, err = client.Update(ctx, privatev1.InstanceTypesUpdateRequest_builder{
			Object: privatev1.InstanceType_builder{
				Id: name,
				Spec: privatev1.InstanceTypeSpec_builder{
					State: privatev1.InstanceTypeState_INSTANCE_TYPE_STATE_DEPRECATED,
				}.Build(),
			}.Build(),
			UpdateMask: &fieldmaskpb.FieldMask{Paths: []string{"spec.state"}},
		}.Build())
		Expect(err).ToNot(HaveOccurred())

		// Transition DEPRECATED -> OBSOLETE:
		_, err = client.Update(ctx, privatev1.InstanceTypesUpdateRequest_builder{
			Object: privatev1.InstanceType_builder{
				Id: name,
				Spec: privatev1.InstanceTypeSpec_builder{
					State: privatev1.InstanceTypeState_INSTANCE_TYPE_STATE_OBSOLETE,
				}.Build(),
			}.Build(),
			UpdateMask: &fieldmaskpb.FieldMask{Paths: []string{"spec.state"}},
		}.Build())
		Expect(err).ToNot(HaveOccurred())

		// Get and verify:
		getResponse, err := client.Get(ctx, privatev1.InstanceTypesGetRequest_builder{
			Id: name,
		}.Build())
		Expect(err).ToNot(HaveOccurred())
		spec := getResponse.GetObject().GetSpec()
		Expect(spec.GetState()).To(Equal(
			privatev1.InstanceTypeState_INSTANCE_TYPE_STATE_OBSOLETE))
		dep := spec.GetDeprecation()
		Expect(dep).ToNot(BeNil())
		Expect(dep.HasDeprecationTimestamp()).To(BeTrue())
		Expect(dep.HasObsolescenceTimestamp()).To(BeTrue())
	})

	It("Can reactivate from DEPRECATED", func() {
		name := fmt.Sprintf("it-react-%s", uuid.New())
		_, err := client.Create(ctx, privatev1.InstanceTypesCreateRequest_builder{
			Object: privatev1.InstanceType_builder{
				Metadata: privatev1.Metadata_builder{
					Name: name,
				}.Build(),
				Spec: privatev1.InstanceTypeSpec_builder{
					Cores:       4,
					MemoryGib:   16,
					Description: "Reactivation test type.",
				}.Build(),
			}.Build(),
		}.Build())
		Expect(err).ToNot(HaveOccurred())
		DeferCleanup(func() {
			_, _ = client.Delete(ctx, privatev1.InstanceTypesDeleteRequest_builder{
				Id: name,
			}.Build())
		})

		// Transition to DEPRECATED:
		_, err = client.Update(ctx, privatev1.InstanceTypesUpdateRequest_builder{
			Object: privatev1.InstanceType_builder{
				Id: name,
				Spec: privatev1.InstanceTypeSpec_builder{
					State: privatev1.InstanceTypeState_INSTANCE_TYPE_STATE_DEPRECATED,
				}.Build(),
			}.Build(),
			UpdateMask: &fieldmaskpb.FieldMask{Paths: []string{"spec.state"}},
		}.Build())
		Expect(err).ToNot(HaveOccurred())

		// Reactivate back to ACTIVE:
		_, err = client.Update(ctx, privatev1.InstanceTypesUpdateRequest_builder{
			Object: privatev1.InstanceType_builder{
				Id: name,
				Spec: privatev1.InstanceTypeSpec_builder{
					State: privatev1.InstanceTypeState_INSTANCE_TYPE_STATE_ACTIVE,
				}.Build(),
			}.Build(),
			UpdateMask: &fieldmaskpb.FieldMask{Paths: []string{"spec.state"}},
		}.Build())
		Expect(err).ToNot(HaveOccurred())

		// Get and verify: state is ACTIVE, deprecation timestamp retained (D-04):
		getResponse, err := client.Get(ctx, privatev1.InstanceTypesGetRequest_builder{
			Id: name,
		}.Build())
		Expect(err).ToNot(HaveOccurred())
		spec := getResponse.GetObject().GetSpec()
		Expect(spec.GetState()).To(Equal(
			privatev1.InstanceTypeState_INSTANCE_TYPE_STATE_ACTIVE))
		dep := spec.GetDeprecation()
		Expect(dep).ToNot(BeNil())
		Expect(dep.HasDeprecationTimestamp()).To(BeTrue())
	})

	// Error scenarios (D-04)

	It("Rejects update of immutable field cores", func() {
		name := fmt.Sprintf("it-immut-cores-%s", uuid.New())
		_, err := client.Create(ctx, privatev1.InstanceTypesCreateRequest_builder{
			Object: privatev1.InstanceType_builder{
				Metadata: privatev1.Metadata_builder{
					Name: name,
				}.Build(),
				Spec: privatev1.InstanceTypeSpec_builder{
					Cores:       4,
					MemoryGib:   16,
					Description: "Immutability test type.",
				}.Build(),
			}.Build(),
		}.Build())
		Expect(err).ToNot(HaveOccurred())
		DeferCleanup(func() {
			_, _ = client.Delete(ctx, privatev1.InstanceTypesDeleteRequest_builder{
				Id: name,
			}.Build())
		})

		// Attempt to change cores:
		_, err = client.Update(ctx, privatev1.InstanceTypesUpdateRequest_builder{
			Object: privatev1.InstanceType_builder{
				Id: name,
				Spec: privatev1.InstanceTypeSpec_builder{
					Cores: 8,
				}.Build(),
			}.Build(),
		}.Build())
		Expect(err).To(HaveOccurred())
		status, ok := grpcstatus.FromError(err)
		Expect(ok).To(BeTrue())
		Expect(status.Code()).To(Equal(grpccodes.InvalidArgument))
		Expect(status.Message()).To(ContainSubstring("spec.cores"))
		Expect(status.Message()).To(ContainSubstring("immutable"))
	})

	It("Rejects update of immutable field memory_gib", func() {
		name := fmt.Sprintf("it-immut-mem-%s", uuid.New())
		_, err := client.Create(ctx, privatev1.InstanceTypesCreateRequest_builder{
			Object: privatev1.InstanceType_builder{
				Metadata: privatev1.Metadata_builder{
					Name: name,
				}.Build(),
				Spec: privatev1.InstanceTypeSpec_builder{
					Cores:       4,
					MemoryGib:   16,
					Description: "Immutability test type.",
				}.Build(),
			}.Build(),
		}.Build())
		Expect(err).ToNot(HaveOccurred())
		DeferCleanup(func() {
			_, _ = client.Delete(ctx, privatev1.InstanceTypesDeleteRequest_builder{
				Id: name,
			}.Build())
		})

		// Attempt to change memory_gib:
		_, err = client.Update(ctx, privatev1.InstanceTypesUpdateRequest_builder{
			Object: privatev1.InstanceType_builder{
				Id: name,
				Spec: privatev1.InstanceTypeSpec_builder{
					MemoryGib: 32,
				}.Build(),
			}.Build(),
		}.Build())
		Expect(err).To(HaveOccurred())
		status, ok := grpcstatus.FromError(err)
		Expect(ok).To(BeTrue())
		Expect(status.Code()).To(Equal(grpccodes.InvalidArgument))
		Expect(status.Message()).To(ContainSubstring("spec.memory_gib"))
		Expect(status.Message()).To(ContainSubstring("immutable"))
	})

	DescribeTable(
		"Rejects create with invalid GPU fields",
		func(gpu *privatev1.GpuSpec) {
			_, err := client.Create(ctx, privatev1.InstanceTypesCreateRequest_builder{
				Object: privatev1.InstanceType_builder{
					Metadata: privatev1.Metadata_builder{
						Name: fmt.Sprintf("it-val-%s", uuid.New()),
					}.Build(),
					Spec: privatev1.InstanceTypeSpec_builder{
						Cores:     4,
						MemoryGib: 16,
						Gpu:       gpu,
					}.Build(),
				}.Build(),
			}.Build())
			Expect(err).To(HaveOccurred())
			status, ok := grpcstatus.FromError(err)
			Expect(ok).To(BeTrue())
			Expect(status.Code()).To(Equal(grpccodes.InvalidArgument))
		},
		Entry("Empty pci_device_selector", privatev1.GpuSpec_builder{
			PciDeviceSelector: "", ResourceName: "nvidia.com/A100", Count: 1,
		}.Build()),
		Entry("Empty resource_name", privatev1.GpuSpec_builder{
			PciDeviceSelector: "10DE:20B0", ResourceName: "", Count: 1,
		}.Build()),
		Entry("Count less than 1", privatev1.GpuSpec_builder{
			PciDeviceSelector: "10DE:20B0", ResourceName: "nvidia.com/A100", Count: 0,
		}.Build()),
		Entry("Count greater than 16", privatev1.GpuSpec_builder{
			PciDeviceSelector: "10DE:20B0", ResourceName: "nvidia.com/A100", Count: 17,
		}.Build()),
	)

	// Deletion protection: ComputeInstance does not have spec.instance_type field until Phase 4 (COMP-01).
	// The reference checker query matches zero rows because the field does not exist in the JSONB data.
	// As a result, deletion succeeds. After Phase 4 adds the instance_type field to ComputeInstance
	// proto, this test should be updated to verify that deletion is blocked when references exist.
	It("Allows deletion when no compute instances reference the type (pre-Phase 4)", func() {
		name := fmt.Sprintf("it-delprotect-%s", uuid.New())
		_, err := client.Create(ctx, privatev1.InstanceTypesCreateRequest_builder{
			Object: privatev1.InstanceType_builder{
				Metadata: privatev1.Metadata_builder{
					Name: name,
				}.Build(),
				Spec: privatev1.InstanceTypeSpec_builder{
					Cores:       4,
					MemoryGib:   16,
					Description: "Deletion protection test type.",
				}.Build(),
			}.Build(),
		}.Build())
		Expect(err).ToNot(HaveOccurred())
		DeferCleanup(func() {
			_, _ = client.Delete(ctx, privatev1.InstanceTypesDeleteRequest_builder{
				Id: name,
			}.Build())
		})

		// Deletion succeeds because the reference checker finds no matches (pre-Phase 4):
		_, err = client.Delete(ctx, privatev1.InstanceTypesDeleteRequest_builder{
			Id: name,
		}.Build())
		Expect(err).ToNot(HaveOccurred())
	})

	// OBSOLETE default filtering (D-04)
	// The private API has no default state filter -- it returns all states including OBSOLETE.
	// This test verifies that the private API returns all 3 instance types regardless of state.
	It("Private list includes OBSOLETE instance types", func() {
		// Create 3 instance types with unique names:
		names := make([]string, 3)
		for i := range 3 {
			names[i] = fmt.Sprintf("it-filter-%d-%s", i, uuid.New())
			_, err := client.Create(ctx, privatev1.InstanceTypesCreateRequest_builder{
				Object: privatev1.InstanceType_builder{
					Metadata: privatev1.Metadata_builder{
						Name: names[i],
					}.Build(),
					Spec: privatev1.InstanceTypeSpec_builder{
						Cores:       2,
						MemoryGib:   4,
						Description: fmt.Sprintf("Filter test type %d.", i),
					}.Build(),
				}.Build(),
			}.Build())
			Expect(err).ToNot(HaveOccurred())
			DeferCleanup(func() {
				_, _ = client.Delete(ctx, privatev1.InstanceTypesDeleteRequest_builder{
					Id: names[i],
				}.Build())
			})
		}

		// Transition the third instance type to OBSOLETE (ACTIVE -> DEPRECATED -> OBSOLETE):
		_, err := client.Update(ctx, privatev1.InstanceTypesUpdateRequest_builder{
			Object: privatev1.InstanceType_builder{
				Id: names[2],
				Spec: privatev1.InstanceTypeSpec_builder{
					State: privatev1.InstanceTypeState_INSTANCE_TYPE_STATE_DEPRECATED,
				}.Build(),
			}.Build(),
			UpdateMask: &fieldmaskpb.FieldMask{Paths: []string{"spec.state"}},
		}.Build())
		Expect(err).ToNot(HaveOccurred())

		_, err = client.Update(ctx, privatev1.InstanceTypesUpdateRequest_builder{
			Object: privatev1.InstanceType_builder{
				Id: names[2],
				Spec: privatev1.InstanceTypeSpec_builder{
					State: privatev1.InstanceTypeState_INSTANCE_TYPE_STATE_OBSOLETE,
				}.Build(),
			}.Build(),
			UpdateMask: &fieldmaskpb.FieldMask{Paths: []string{"spec.state"}},
		}.Build())
		Expect(err).ToNot(HaveOccurred())

		// Private API list scoped to our 3 names -- should return all 3 including OBSOLETE:
		nameFilter := fmt.Sprintf(
			`this.metadata.name == %q || this.metadata.name == %q || this.metadata.name == %q`,
			names[0], names[1], names[2],
		)
		response, err := client.List(ctx, privatev1.InstanceTypesListRequest_builder{
			Filter: new(nameFilter),
		}.Build())
		Expect(err).ToNot(HaveOccurred())
		Expect(response.GetItems()).To(HaveLen(3))

		// Private API list with explicit OBSOLETE filter scoped to our names.
		// CEL types proto enums as int, so use the numeric value (3 = OBSOLETE):
		obsoleteFilter := fmt.Sprintf(
			`this.spec.state == %d && (%s)`,
			int32(privatev1.InstanceTypeState_INSTANCE_TYPE_STATE_OBSOLETE),
			nameFilter,
		)
		obsoleteResponse, err := client.List(ctx, privatev1.InstanceTypesListRequest_builder{
			Filter: new(obsoleteFilter),
		}.Build())
		Expect(err).ToNot(HaveOccurred())
		Expect(obsoleteResponse.GetItems()).To(HaveLen(1))
		Expect(obsoleteResponse.GetItems()[0].GetId()).To(Equal(names[2]))
	})
})

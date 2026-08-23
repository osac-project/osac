/*
Copyright (c) 2025 Red Hat Inc.

Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except in compliance with the
License. You may obtain a copy of the License at

  http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on an
"AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the specific
language governing permissions and limitations under the License.
*/

package servers

import (
	"encoding/json"
	"fmt"
	"time"

	"buf.build/go/protovalidate"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	grpccodes "google.golang.org/grpc/codes"
	grpcstatus "google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/fieldmaskpb"

	privatev1 "github.com/osac-project/osac/fulfillment-service/internal/api/osac/private/v1"
	publicv1 "github.com/osac-project/osac/fulfillment-service/internal/api/osac/public/v1"
	"github.com/osac-project/osac/fulfillment-service/internal/database"
)

var _ = Describe("Private instance types server", func() {
	Describe("Creation", func() {
		It("Can be built if all the required parameters are set", func() {
			server, err := NewPrivateInstanceTypesServer().
				SetLogger(logger).
				SetAttributionLogic(attribution).
				SetTenancyLogic(tenancy).
				Build()
			Expect(err).ToNot(HaveOccurred())
			Expect(server).ToNot(BeNil())
		})

		It("Fails if logger is not set", func() {
			server, err := NewPrivateInstanceTypesServer().
				SetAttributionLogic(attribution).
				SetTenancyLogic(tenancy).
				Build()
			Expect(err).To(MatchError("logger is mandatory"))
			Expect(server).To(BeNil())
		})

		It("Fails if tenancy logic is not set", func() {
			server, err := NewPrivateInstanceTypesServer().
				SetLogger(logger).
				SetAttributionLogic(attribution).
				Build()
			Expect(err).To(MatchError("tenancy logic is mandatory"))
			Expect(server).To(BeNil())
		})
	})

	Describe("Behaviour", func() {
		var server *PrivateInstanceTypesServer

		BeforeEach(func() {
			var err error

			// Create the server:
			server, err = NewPrivateInstanceTypesServer().
				SetLogger(logger).
				SetAttributionLogic(attribution).
				SetTenancyLogic(tenancy).
				Build()
			Expect(err).ToNot(HaveOccurred())
		})

		It("Creates object without GPU", func() {
			response, err := server.Create(ctx, privatev1.InstanceTypesCreateRequest_builder{
				Object: privatev1.InstanceType_builder{
					Metadata: privatev1.Metadata_builder{
						Name: "standard-4-16",
					}.Build(),
					Spec: privatev1.InstanceTypeSpec_builder{
						Cores:       4,
						MemoryGib:   16,
						Description: "Standard 4 cores, 16 GiB RAM.",
					}.Build(),
				}.Build(),
			}.Build())
			Expect(err).ToNot(HaveOccurred())
			Expect(response).ToNot(BeNil())
			object := response.GetObject()
			Expect(object).ToNot(BeNil())
			Expect(object.GetId()).To(Equal("standard-4-16"))
			Expect(object.GetSpec().GetState()).To(Equal(
				privatev1.InstanceTypeState_INSTANCE_TYPE_STATE_ACTIVE))
			Expect(object.GetSpec().GetCores()).To(Equal(int32(4)))
			Expect(object.GetSpec().GetMemoryGib()).To(Equal(int32(16)))
		})

		It("Rejects create with non-ACTIVE state", func() {
			_, err := server.Create(ctx, privatev1.InstanceTypesCreateRequest_builder{
				Object: privatev1.InstanceType_builder{
					Metadata: privatev1.Metadata_builder{
						Name: "deprecated-on-create",
					}.Build(),
					Spec: privatev1.InstanceTypeSpec_builder{
						Cores:     4,
						MemoryGib: 16,
						State:     privatev1.InstanceTypeState_INSTANCE_TYPE_STATE_DEPRECATED,
					}.Build(),
				}.Build(),
			}.Build())
			Expect(err).To(HaveOccurred())
			status, ok := grpcstatus.FromError(err)
			Expect(ok).To(BeTrue())
			Expect(status.Code()).To(Equal(grpccodes.InvalidArgument))
			Expect(status.Message()).To(ContainSubstring("ACTIVE"))
		})

		It("Creates object with GPU", func() {
			response, err := server.Create(ctx, privatev1.InstanceTypesCreateRequest_builder{
				Object: privatev1.InstanceType_builder{
					Metadata: privatev1.Metadata_builder{
						Name: "gpu-a100-8core",
					}.Build(),
					Spec: privatev1.InstanceTypeSpec_builder{
						Cores:       8,
						MemoryGib:   64,
						Description: "8 vCPU, 64 GiB, 1x A100 GPU.",
						Gpu: privatev1.GpuSpec_builder{
							PciDeviceSelector: "10DE:20B0",
							ResourceName:      "nvidia.com/A100",
							Count:             1,
						}.Build(),
					}.Build(),
				}.Build(),
			}.Build())
			Expect(err).ToNot(HaveOccurred())
			Expect(response).ToNot(BeNil())
			gpu := response.GetObject().GetSpec().GetGpu()
			Expect(gpu).ToNot(BeNil())
			Expect(gpu.GetPciDeviceSelector()).To(Equal("10DE:20B0"))
			Expect(gpu.GetResourceName()).To(Equal("nvidia.com/A100"))
			Expect(gpu.GetCount()).To(Equal(int32(1)))
		})

		It("List objects", func() {
			// Create a few objects, first with GPU:
			const count = 10
			for i := range count {
				spec := privatev1.InstanceTypeSpec_builder{
					Cores:       4,
					MemoryGib:   16,
					Description: fmt.Sprintf("Type %d.", i),
				}
				if i == 0 {
					spec.Gpu = privatev1.GpuSpec_builder{
						PciDeviceSelector: "10DE:20B0",
						ResourceName:      "nvidia.com/A100",
						Count:             1,
					}.Build()
				}
				_, err := server.Create(ctx, privatev1.InstanceTypesCreateRequest_builder{
					Object: privatev1.InstanceType_builder{
						Metadata: privatev1.Metadata_builder{
							Name: fmt.Sprintf("type-%d", i),
						}.Build(),
						Spec: spec.Build(),
					}.Build(),
				}.Build())
				Expect(err).ToNot(HaveOccurred())
			}

			// List the objects:
			response, err := server.List(ctx, privatev1.InstanceTypesListRequest_builder{}.Build())
			Expect(err).ToNot(HaveOccurred())
			Expect(response).ToNot(BeNil())
			items := response.GetItems()
			Expect(items).To(HaveLen(count))
		})

		It("Get object", func() {
			// Create the object:
			createResponse, err := server.Create(ctx, privatev1.InstanceTypesCreateRequest_builder{
				Object: privatev1.InstanceType_builder{
					Metadata: privatev1.Metadata_builder{
						Name: "standard-4-16",
					}.Build(),
					Spec: privatev1.InstanceTypeSpec_builder{
						Cores:       4,
						MemoryGib:   16,
						Description: "Standard 4 cores, 16 GiB RAM.",
					}.Build(),
				}.Build(),
			}.Build())
			Expect(err).ToNot(HaveOccurred())

			// Get it:
			getResponse, err := server.Get(ctx, privatev1.InstanceTypesGetRequest_builder{
				Id: createResponse.GetObject().GetId(),
			}.Build())
			Expect(err).ToNot(HaveOccurred())
			Expect(proto.Equal(createResponse.GetObject(), getResponse.GetObject())).To(BeTrue())
		})

		It("Update object", func() {
			// Create the object:
			createResponse, err := server.Create(ctx, privatev1.InstanceTypesCreateRequest_builder{
				Object: privatev1.InstanceType_builder{
					Metadata: privatev1.Metadata_builder{
						Name: "standard-4-16",
					}.Build(),
					Spec: privatev1.InstanceTypeSpec_builder{
						Cores:       4,
						MemoryGib:   16,
						Description: "Standard 4 cores, 16 GiB RAM.",
						Gpu: privatev1.GpuSpec_builder{
							PciDeviceSelector: "10DE:20B0",
							ResourceName:      "nvidia.com/A100",
							Count:             1,
						}.Build(),
					}.Build(),
				}.Build(),
			}.Build())
			Expect(err).ToNot(HaveOccurred())
			object := createResponse.GetObject()

			// Update the description using a field mask:
			updateResponse, err := server.Update(ctx, privatev1.InstanceTypesUpdateRequest_builder{
				Object: privatev1.InstanceType_builder{
					Id: object.GetId(),
					Spec: privatev1.InstanceTypeSpec_builder{
						Description: "Updated description.",
					}.Build(),
				}.Build(),
				UpdateMask: &fieldmaskpb.FieldMask{Paths: []string{"spec.description"}},
			}.Build())
			Expect(err).ToNot(HaveOccurred())
			Expect(updateResponse.GetObject().GetSpec().GetDescription()).To(Equal("Updated description."))

			// Get and verify:
			getResponse, err := server.Get(ctx, privatev1.InstanceTypesGetRequest_builder{
				Id: object.GetId(),
			}.Build())
			Expect(err).ToNot(HaveOccurred())
			Expect(getResponse.GetObject().GetSpec().GetDescription()).To(Equal("Updated description."))
		})

		It("Delete object", func() {
			// Create the object with a finalizer:
			createResponse, err := server.Create(ctx, privatev1.InstanceTypesCreateRequest_builder{
				Object: privatev1.InstanceType_builder{
					Metadata: privatev1.Metadata_builder{
						Name:       "standard-4-16",
						Finalizers: []string{"a"},
					}.Build(),
					Spec: privatev1.InstanceTypeSpec_builder{
						Cores:       4,
						MemoryGib:   16,
						Description: "Standard 4 cores, 16 GiB RAM.",
						Gpu: privatev1.GpuSpec_builder{
							PciDeviceSelector: "10DE:20B0",
							ResourceName:      "nvidia.com/A100",
							Count:             1,
						}.Build(),
					}.Build(),
				}.Build(),
			}.Build())
			Expect(err).ToNot(HaveOccurred())
			object := createResponse.GetObject()

			// Delete the object:
			_, err = server.Delete(ctx, privatev1.InstanceTypesDeleteRequest_builder{
				Id: object.GetId(),
			}.Build())
			Expect(err).ToNot(HaveOccurred())

			// Get and verify:
			getResponse, err := server.Get(ctx, privatev1.InstanceTypesGetRequest_builder{
				Id: object.GetId(),
			}.Build())
			Expect(err).ToNot(HaveOccurred())
			Expect(getResponse.GetObject().GetMetadata().GetDeletionTimestamp()).ToNot(BeNil())
		})

		DescribeTable(
			"Rejects invalid labels on create and update",
			func(key string, value string, expected string) {
				_, err := server.Create(ctx, privatev1.InstanceTypesCreateRequest_builder{
					Object: privatev1.InstanceType_builder{
						Metadata: privatev1.Metadata_builder{
							Name: "label-test",
							Labels: map[string]string{
								key: value,
							},
						}.Build(),
						Spec: privatev1.InstanceTypeSpec_builder{
							Cores:       4,
							MemoryGib:   16,
							Description: "Label test.",
						}.Build(),
					}.Build(),
				}.Build())
				Expect(err).To(HaveOccurred())
				status, ok := grpcstatus.FromError(err)
				Expect(ok).To(BeTrue())
				Expect(status.Code()).To(Equal(grpccodes.InvalidArgument))
				Expect(status.Message()).To(Equal(expected))

				createResponse, err := server.Create(ctx, privatev1.InstanceTypesCreateRequest_builder{
					Object: privatev1.InstanceType_builder{
						Metadata: privatev1.Metadata_builder{
							Name: "label-test",
						}.Build(),
						Spec: privatev1.InstanceTypeSpec_builder{
							Cores:       4,
							MemoryGib:   16,
							Description: "Label test.",
						}.Build(),
					}.Build(),
				}.Build())
				Expect(err).ToNot(HaveOccurred())
				object := createResponse.GetObject()

				_, err = server.Update(ctx, privatev1.InstanceTypesUpdateRequest_builder{
					Object: privatev1.InstanceType_builder{
						Id: object.GetId(),
						Metadata: privatev1.Metadata_builder{
							Name: "label-test",
							Labels: map[string]string{
								key: value,
							},
						}.Build(),
						Spec: privatev1.InstanceTypeSpec_builder{
							Cores:       4,
							MemoryGib:   16,
							Description: "Label test.",
						}.Build(),
					}.Build(),
				}.Build())
				Expect(err).To(HaveOccurred())
				status, ok = grpcstatus.FromError(err)
				Expect(ok).To(BeTrue())
				Expect(status.Code()).To(Equal(grpccodes.InvalidArgument))
				Expect(status.Message()).To(Equal(expected))
			},
			Entry(
				"Invalid label name character",
				"bad^name",
				"value",
				"field 'metadata.labels' key 'bad^name' name must only contain lowercase letters (a-z), "+
					"digits (0-9), hyphens (-), underscores (_) or dots (.), but contains '^' at position 3",
			),
			Entry(
				"Invalid label prefix character",
				"bad_prefix/name",
				"value",
				"field 'metadata.labels' key 'bad_prefix/name' prefix segment must only contain lowercase "+
					"letters (a-z), digits (0-9) and hyphens (-), but contains '_' at position 3",
			),
			Entry(
				"Invalid label value character",
				"good",
				"bad@value",
				"field 'metadata.labels' key 'good' value must only contain lowercase letters (a-z), "+
					"digits (0-9), hyphens (-), underscores (_) or dots (.), but contains '@' at position 3",
			),
		)

		DescribeTable(
			"Rejects invalid annotations on create and update",
			func(key string, value string, expected string) {
				_, err := server.Create(ctx, privatev1.InstanceTypesCreateRequest_builder{
					Object: privatev1.InstanceType_builder{
						Metadata: privatev1.Metadata_builder{
							Name: "annotation-test",
							Annotations: map[string]string{
								key: value,
							},
						}.Build(),
						Spec: privatev1.InstanceTypeSpec_builder{
							Cores:       4,
							MemoryGib:   16,
							Description: "Annotation test.",
						}.Build(),
					}.Build(),
				}.Build())
				Expect(err).To(HaveOccurred())
				status, ok := grpcstatus.FromError(err)
				Expect(ok).To(BeTrue())
				Expect(status.Code()).To(Equal(grpccodes.InvalidArgument))
				Expect(status.Message()).To(Equal(expected))

				createResponse, err := server.Create(ctx, privatev1.InstanceTypesCreateRequest_builder{
					Object: privatev1.InstanceType_builder{
						Metadata: privatev1.Metadata_builder{
							Name: "annotation-test",
						}.Build(),
						Spec: privatev1.InstanceTypeSpec_builder{
							Cores:       4,
							MemoryGib:   16,
							Description: "Annotation test.",
						}.Build(),
					}.Build(),
				}.Build())
				Expect(err).ToNot(HaveOccurred())
				object := createResponse.GetObject()

				_, err = server.Update(ctx, privatev1.InstanceTypesUpdateRequest_builder{
					Object: privatev1.InstanceType_builder{
						Id: object.GetId(),
						Metadata: privatev1.Metadata_builder{
							Name: "annotation-test",
							Annotations: map[string]string{
								key: value,
							},
						}.Build(),
						Spec: privatev1.InstanceTypeSpec_builder{
							Cores:       4,
							MemoryGib:   16,
							Description: "Annotation test.",
						}.Build(),
					}.Build(),
				}.Build())
				Expect(err).To(HaveOccurred())
				status, ok = grpcstatus.FromError(err)
				Expect(ok).To(BeTrue())
				Expect(status.Code()).To(Equal(grpccodes.InvalidArgument))
				Expect(status.Message()).To(Equal(expected))
			},
			Entry(
				"Invalid annotation name character",
				"bad^annotation",
				"value",
				"field 'metadata.annotations' key 'bad^annotation' name must only contain lowercase letters "+
					"(a-z), digits (0-9), hyphens (-), underscores (_) or dots (.), but contains '^' at position 3",
			),
			Entry(
				"Invalid annotation prefix character",
				"bad_prefix/annotation",
				"value",
				"field 'metadata.annotations' key 'bad_prefix/annotation' prefix segment must only contain "+
					"lowercase letters (a-z), digits (0-9) and hyphens (-), but contains '_' at position 3",
			),
		)

		// State transition tests (TEST-02)
		Describe("State transitions", func() {
			// Helper to create an instance type and transition it to the given starting state.
			createWithState := func(name string, state privatev1.InstanceTypeState) *privatev1.InstanceType {
				createResponse, err := server.Create(ctx, privatev1.InstanceTypesCreateRequest_builder{
					Object: privatev1.InstanceType_builder{
						Metadata: privatev1.Metadata_builder{
							Name: name,
						}.Build(),
						Spec: privatev1.InstanceTypeSpec_builder{
							Cores:       4,
							MemoryGib:   16,
							Description: "State test.",
						}.Build(),
					}.Build(),
				}.Build())
				Expect(err).ToNot(HaveOccurred())

				if state == privatev1.InstanceTypeState_INSTANCE_TYPE_STATE_ACTIVE {
					return createResponse.GetObject()
				}

				// Transition to desired starting state:
				updateResponse, err := server.Update(ctx, privatev1.InstanceTypesUpdateRequest_builder{
					Object: privatev1.InstanceType_builder{
						Id: createResponse.GetObject().GetId(),
						Spec: privatev1.InstanceTypeSpec_builder{
							State: state,
						}.Build(),
					}.Build(),
					UpdateMask: &fieldmaskpb.FieldMask{Paths: []string{"spec.state"}},
				}.Build())
				Expect(err).ToNot(HaveOccurred())
				return updateResponse.GetObject()
			}

			It("Transitions ACTIVE to DEPRECATED", func() {
				object := createWithState("active-to-deprecated",
					privatev1.InstanceTypeState_INSTANCE_TYPE_STATE_ACTIVE)

				updateResponse, err := server.Update(ctx, privatev1.InstanceTypesUpdateRequest_builder{
					Object: privatev1.InstanceType_builder{
						Id: object.GetId(),
						Spec: privatev1.InstanceTypeSpec_builder{
							State: privatev1.InstanceTypeState_INSTANCE_TYPE_STATE_DEPRECATED,
						}.Build(),
					}.Build(),
					UpdateMask: &fieldmaskpb.FieldMask{Paths: []string{"spec.state"}},
				}.Build())
				Expect(err).ToNot(HaveOccurred())
				spec := updateResponse.GetObject().GetSpec()
				Expect(spec.GetState()).To(Equal(
					privatev1.InstanceTypeState_INSTANCE_TYPE_STATE_DEPRECATED))
				dep := spec.GetDeprecation()
				Expect(dep).ToNot(BeNil())
				Expect(dep.GetDeprecationTimestamp()).ToNot(BeNil())
				Expect(dep.GetDeprecationTimestamp().AsTime()).To(
					BeTemporally("~", time.Now(), time.Second))
				Expect(dep.GetObsolescenceTimestamp()).To(BeNil())
			})

			It("Transitions ACTIVE to OBSOLETE", func() {
				object := createWithState("active-to-obsolete",
					privatev1.InstanceTypeState_INSTANCE_TYPE_STATE_ACTIVE)

				updateResponse, err := server.Update(ctx, privatev1.InstanceTypesUpdateRequest_builder{
					Object: privatev1.InstanceType_builder{
						Id: object.GetId(),
						Spec: privatev1.InstanceTypeSpec_builder{
							State: privatev1.InstanceTypeState_INSTANCE_TYPE_STATE_OBSOLETE,
						}.Build(),
					}.Build(),
					UpdateMask: &fieldmaskpb.FieldMask{Paths: []string{"spec.state"}},
				}.Build())
				Expect(err).ToNot(HaveOccurred())
				spec := updateResponse.GetObject().GetSpec()
				Expect(spec.GetState()).To(Equal(
					privatev1.InstanceTypeState_INSTANCE_TYPE_STATE_OBSOLETE))
				dep := spec.GetDeprecation()
				Expect(dep).ToNot(BeNil())
				Expect(dep.GetObsolescenceTimestamp()).ToNot(BeNil())
				Expect(dep.GetObsolescenceTimestamp().AsTime()).To(
					BeTemporally("~", time.Now(), time.Second))
				// D-03: Only target state timestamp is set.
				Expect(dep.GetDeprecationTimestamp()).To(BeNil())
			})

			It("Transitions DEPRECATED to ACTIVE", func() {
				object := createWithState("deprecated-to-active",
					privatev1.InstanceTypeState_INSTANCE_TYPE_STATE_DEPRECATED)
				originalDepTimestamp := object.GetSpec().GetDeprecation().GetDeprecationTimestamp()

				updateResponse, err := server.Update(ctx, privatev1.InstanceTypesUpdateRequest_builder{
					Object: privatev1.InstanceType_builder{
						Id: object.GetId(),
						Spec: privatev1.InstanceTypeSpec_builder{
							State: privatev1.InstanceTypeState_INSTANCE_TYPE_STATE_ACTIVE,
						}.Build(),
					}.Build(),
					UpdateMask: &fieldmaskpb.FieldMask{Paths: []string{"spec.state"}},
				}.Build())
				Expect(err).ToNot(HaveOccurred())
				spec := updateResponse.GetObject().GetSpec()
				Expect(spec.GetState()).To(Equal(
					privatev1.InstanceTypeState_INSTANCE_TYPE_STATE_ACTIVE))
				// D-04: Backward transitions retain existing timestamps as historical record.
				dep := spec.GetDeprecation()
				Expect(dep).ToNot(BeNil())
				Expect(dep.GetDeprecationTimestamp()).ToNot(BeNil())
				Expect(proto.Equal(dep.GetDeprecationTimestamp(), originalDepTimestamp)).To(BeTrue())
			})

			It("Transitions DEPRECATED to OBSOLETE", func() {
				object := createWithState("deprecated-to-obsolete",
					privatev1.InstanceTypeState_INSTANCE_TYPE_STATE_DEPRECATED)
				originalDepTimestamp := object.GetSpec().GetDeprecation().GetDeprecationTimestamp()

				updateResponse, err := server.Update(ctx, privatev1.InstanceTypesUpdateRequest_builder{
					Object: privatev1.InstanceType_builder{
						Id: object.GetId(),
						Spec: privatev1.InstanceTypeSpec_builder{
							State: privatev1.InstanceTypeState_INSTANCE_TYPE_STATE_OBSOLETE,
						}.Build(),
					}.Build(),
					UpdateMask: &fieldmaskpb.FieldMask{Paths: []string{"spec.state"}},
				}.Build())
				Expect(err).ToNot(HaveOccurred())
				spec := updateResponse.GetObject().GetSpec()
				Expect(spec.GetState()).To(Equal(
					privatev1.InstanceTypeState_INSTANCE_TYPE_STATE_OBSOLETE))
				dep := spec.GetDeprecation()
				Expect(dep).ToNot(BeNil())
				// Both timestamps should be present: deprecation retained from prior,
				// obsolescence newly set.
				Expect(dep.GetDeprecationTimestamp()).ToNot(BeNil())
				Expect(proto.Equal(dep.GetDeprecationTimestamp(), originalDepTimestamp)).To(BeTrue())
				Expect(dep.GetObsolescenceTimestamp()).ToNot(BeNil())
				Expect(dep.GetObsolescenceTimestamp().AsTime()).To(
					BeTemporally("~", time.Now(), time.Second))
			})

			It("Transitions OBSOLETE to DEPRECATED", func() {
				object := createWithState("obsolete-to-deprecated",
					privatev1.InstanceTypeState_INSTANCE_TYPE_STATE_OBSOLETE)
				originalObsTimestamp := object.GetSpec().GetDeprecation().GetObsolescenceTimestamp()

				updateResponse, err := server.Update(ctx, privatev1.InstanceTypesUpdateRequest_builder{
					Object: privatev1.InstanceType_builder{
						Id: object.GetId(),
						Spec: privatev1.InstanceTypeSpec_builder{
							State: privatev1.InstanceTypeState_INSTANCE_TYPE_STATE_DEPRECATED,
						}.Build(),
					}.Build(),
					UpdateMask: &fieldmaskpb.FieldMask{Paths: []string{"spec.state"}},
				}.Build())
				Expect(err).ToNot(HaveOccurred())
				spec := updateResponse.GetObject().GetSpec()
				Expect(spec.GetState()).To(Equal(
					privatev1.InstanceTypeState_INSTANCE_TYPE_STATE_DEPRECATED))
				// D-04: Backward transitions retain existing timestamps.
				dep := spec.GetDeprecation()
				Expect(dep).ToNot(BeNil())
				Expect(dep.GetObsolescenceTimestamp()).ToNot(BeNil())
				Expect(proto.Equal(dep.GetObsolescenceTimestamp(), originalObsTimestamp)).To(BeTrue())
				// Deprecation timestamp is newly set because DEPRECATED is the target state.
				Expect(dep.GetDeprecationTimestamp()).ToNot(BeNil())
				Expect(dep.GetDeprecationTimestamp().AsTime()).To(
					BeTemporally("~", time.Now(), time.Second))
			})

			It("Transitions OBSOLETE to ACTIVE", func() {
				object := createWithState("obsolete-to-active",
					privatev1.InstanceTypeState_INSTANCE_TYPE_STATE_OBSOLETE)
				originalObsTimestamp := object.GetSpec().GetDeprecation().GetObsolescenceTimestamp()

				updateResponse, err := server.Update(ctx, privatev1.InstanceTypesUpdateRequest_builder{
					Object: privatev1.InstanceType_builder{
						Id: object.GetId(),
						Spec: privatev1.InstanceTypeSpec_builder{
							State: privatev1.InstanceTypeState_INSTANCE_TYPE_STATE_ACTIVE,
						}.Build(),
					}.Build(),
					UpdateMask: &fieldmaskpb.FieldMask{Paths: []string{"spec.state"}},
				}.Build())
				Expect(err).ToNot(HaveOccurred())
				spec := updateResponse.GetObject().GetSpec()
				Expect(spec.GetState()).To(Equal(
					privatev1.InstanceTypeState_INSTANCE_TYPE_STATE_ACTIVE))
				// D-04: Backward transitions retain existing timestamps as historical record.
				dep := spec.GetDeprecation()
				Expect(dep).ToNot(BeNil())
				Expect(dep.GetObsolescenceTimestamp()).ToNot(BeNil())
				Expect(proto.Equal(dep.GetObsolescenceTimestamp(), originalObsTimestamp)).To(BeTrue())
			})

			It("Same-state update is no-op", func() {
				// D-02: Same-state update is silent no-op without error or timestamp changes.
				object := createWithState("same-state-noop",
					privatev1.InstanceTypeState_INSTANCE_TYPE_STATE_DEPRECATED)
				originalDep := object.GetSpec().GetDeprecation()

				updateResponse, err := server.Update(ctx, privatev1.InstanceTypesUpdateRequest_builder{
					Object: privatev1.InstanceType_builder{
						Id: object.GetId(),
						Spec: privatev1.InstanceTypeSpec_builder{
							State: privatev1.InstanceTypeState_INSTANCE_TYPE_STATE_DEPRECATED,
						}.Build(),
					}.Build(),
					UpdateMask: &fieldmaskpb.FieldMask{Paths: []string{"spec.state"}},
				}.Build())
				Expect(err).ToNot(HaveOccurred())
				// Verify no timestamp changes:
				updatedDep := updateResponse.GetObject().GetSpec().GetDeprecation()
				Expect(proto.Equal(updatedDep.GetDeprecationTimestamp(),
					originalDep.GetDeprecationTimestamp())).To(BeTrue())
			})

			It("Re-entry updates timestamp", func() {
				// D-05: Re-entering a state updates the corresponding timestamp to now.
				object := createWithState("re-entry-timestamp",
					privatev1.InstanceTypeState_INSTANCE_TYPE_STATE_DEPRECATED)
				t1 := object.GetSpec().GetDeprecation().GetDeprecationTimestamp()

				// Transition DEPRECATED -> ACTIVE:
				_, err := server.Update(ctx, privatev1.InstanceTypesUpdateRequest_builder{
					Object: privatev1.InstanceType_builder{
						Id: object.GetId(),
						Spec: privatev1.InstanceTypeSpec_builder{
							State: privatev1.InstanceTypeState_INSTANCE_TYPE_STATE_ACTIVE,
						}.Build(),
					}.Build(),
					UpdateMask: &fieldmaskpb.FieldMask{Paths: []string{"spec.state"}},
				}.Build())
				Expect(err).ToNot(HaveOccurred())

				// Wait to ensure distinct timestamps:
				time.Sleep(10 * time.Millisecond)

				// Transition ACTIVE -> DEPRECATED again:
				updateResponse, err := server.Update(ctx, privatev1.InstanceTypesUpdateRequest_builder{
					Object: privatev1.InstanceType_builder{
						Id: object.GetId(),
						Spec: privatev1.InstanceTypeSpec_builder{
							State: privatev1.InstanceTypeState_INSTANCE_TYPE_STATE_DEPRECATED,
						}.Build(),
					}.Build(),
					UpdateMask: &fieldmaskpb.FieldMask{Paths: []string{"spec.state"}},
				}.Build())
				Expect(err).ToNot(HaveOccurred())

				// Verify new deprecation timestamp differs from T1:
				t2 := updateResponse.GetObject().GetSpec().GetDeprecation().GetDeprecationTimestamp()
				Expect(t2).ToNot(BeNil())
				Expect(proto.Equal(t1, t2)).To(BeFalse())
			})
		})

		// Immutability tests (TEST-02)
		Describe("Immutability", func() {
			It("Rejects update of cores", func() {
				createResponse, err := server.Create(ctx, privatev1.InstanceTypesCreateRequest_builder{
					Object: privatev1.InstanceType_builder{
						Metadata: privatev1.Metadata_builder{
							Name: "immutable-cores",
						}.Build(),
						Spec: privatev1.InstanceTypeSpec_builder{
							Cores:       4,
							MemoryGib:   16,
							Description: "Immutability test.",
						}.Build(),
					}.Build(),
				}.Build())
				Expect(err).ToNot(HaveOccurred())

				_, err = server.Update(ctx, privatev1.InstanceTypesUpdateRequest_builder{
					Object: privatev1.InstanceType_builder{
						Id: createResponse.GetObject().GetId(),
						Spec: privatev1.InstanceTypeSpec_builder{
							Cores:     8,
							MemoryGib: 16,
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

			It("Rejects update of memory_gib", func() {
				createResponse, err := server.Create(ctx, privatev1.InstanceTypesCreateRequest_builder{
					Object: privatev1.InstanceType_builder{
						Metadata: privatev1.Metadata_builder{
							Name: "immutable-memory",
						}.Build(),
						Spec: privatev1.InstanceTypeSpec_builder{
							Cores:       4,
							MemoryGib:   16,
							Description: "Immutability test.",
						}.Build(),
					}.Build(),
				}.Build())
				Expect(err).ToNot(HaveOccurred())

				_, err = server.Update(ctx, privatev1.InstanceTypesUpdateRequest_builder{
					Object: privatev1.InstanceType_builder{
						Id: createResponse.GetObject().GetId(),
						Spec: privatev1.InstanceTypeSpec_builder{
							Cores:     4,
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

			It("Rejects update of name", func() {
				createResponse, err := server.Create(ctx, privatev1.InstanceTypesCreateRequest_builder{
					Object: privatev1.InstanceType_builder{
						Metadata: privatev1.Metadata_builder{
							Name: "original",
						}.Build(),
						Spec: privatev1.InstanceTypeSpec_builder{
							Cores:       4,
							MemoryGib:   16,
							Description: "Immutability test.",
						}.Build(),
					}.Build(),
				}.Build())
				Expect(err).ToNot(HaveOccurred())

				_, err = server.Update(ctx, privatev1.InstanceTypesUpdateRequest_builder{
					Object: privatev1.InstanceType_builder{
						Id: createResponse.GetObject().GetId(),
						Metadata: privatev1.Metadata_builder{
							Name: "renamed",
						}.Build(),
						Spec: privatev1.InstanceTypeSpec_builder{
							Cores:     4,
							MemoryGib: 16,
						}.Build(),
					}.Build(),
				}.Build())
				Expect(err).To(HaveOccurred())
				status, ok := grpcstatus.FromError(err)
				Expect(ok).To(BeTrue())
				Expect(status.Code()).To(Equal(grpccodes.InvalidArgument))
				Expect(status.Message()).To(ContainSubstring("name"))
				Expect(status.Message()).To(ContainSubstring("immutable"))
			})

			It("Rejects update of gpu", func() {
				createResponse, err := server.Create(ctx, privatev1.InstanceTypesCreateRequest_builder{
					Object: privatev1.InstanceType_builder{
						Metadata: privatev1.Metadata_builder{
							Name: "immutable-gpu",
						}.Build(),
						Spec: privatev1.InstanceTypeSpec_builder{
							Cores:     8,
							MemoryGib: 64,
							Gpu: privatev1.GpuSpec_builder{
								PciDeviceSelector: "10DE:20B0",
								ResourceName:      "nvidia.com/A100",
								Count:             1,
							}.Build(),
						}.Build(),
					}.Build(),
				}.Build())
				Expect(err).ToNot(HaveOccurred())

				_, err = server.Update(ctx, privatev1.InstanceTypesUpdateRequest_builder{
					Object: privatev1.InstanceType_builder{
						Id: createResponse.GetObject().GetId(),
						Spec: privatev1.InstanceTypeSpec_builder{
							Cores:     8,
							MemoryGib: 64,
							Gpu: privatev1.GpuSpec_builder{
								PciDeviceSelector: "10DE:20B0",
								ResourceName:      "nvidia.com/A100",
								Count:             2,
							}.Build(),
						}.Build(),
					}.Build(),
				}.Build())
				Expect(err).To(HaveOccurred())
				status, ok := grpcstatus.FromError(err)
				Expect(ok).To(BeTrue())
				Expect(status.Code()).To(Equal(grpccodes.InvalidArgument))
				Expect(status.Message()).To(ContainSubstring("spec.gpu"))
				Expect(status.Message()).To(ContainSubstring("immutable"))
			})
		})

		// Deletion protection tests (TEST-02)
		Describe("Deletion protection", func() {
			It("Blocks delete when referenced by compute instance", func() {
				createResponse, err := server.Create(ctx, privatev1.InstanceTypesCreateRequest_builder{
					Object: privatev1.InstanceType_builder{
						Metadata: privatev1.Metadata_builder{
							Name:       "referenced-type",
							Finalizers: []string{"a"},
						}.Build(),
						Spec: privatev1.InstanceTypeSpec_builder{
							Cores:       4,
							MemoryGib:   16,
							Description: "Referenced instance type.",
						}.Build(),
					}.Build(),
				}.Build())
				Expect(err).ToNot(HaveOccurred())
				instanceType := createResponse.GetObject()

				// Insert a compute instance row with spec.instance_type via raw SQL,
				// since the instance_type field is not yet in the ComputeInstance proto.
				tx, err := database.TxFromContext(ctx)
				Expect(err).ToNot(HaveOccurred())
				_, err = tx.Exec(ctx,
					`INSERT INTO compute_instances (id, name, tenant, data)
					VALUES ($1, $2, $3, $4)`,
					"test-vm",
					"test-vm",
					"system",
					func() string {
						data, err := json.Marshal(map[string]any{
							"spec": map[string]any{
								"instance_type": map[string]any{
									"id": instanceType.GetId(),
								},
							},
						})
						Expect(err).ToNot(HaveOccurred())
						return string(data)
					}(),
				)
				Expect(err).ToNot(HaveOccurred())

				// Attempt to delete the instance type — the DB trigger should reject it:
				_, err = server.Delete(ctx, privatev1.InstanceTypesDeleteRequest_builder{
					Id: instanceType.GetId(),
				}.Build())
				Expect(err).To(HaveOccurred())
				status, ok := grpcstatus.FromError(err)
				Expect(ok).To(BeTrue())
				Expect(status.Code()).To(Equal(grpccodes.FailedPrecondition))
				Expect(status.Message()).To(ContainSubstring("cannot delete instance type"))
				Expect(status.Message()).ToNot(ContainSubstring("test-vm"))
			})

			It("Allows delete when no compute instances reference it", func() {
				createResponse, err := server.Create(ctx, privatev1.InstanceTypesCreateRequest_builder{
					Object: privatev1.InstanceType_builder{
						Metadata: privatev1.Metadata_builder{
							Name:       "unreferenced-type",
							Finalizers: []string{"a"},
						}.Build(),
						Spec: privatev1.InstanceTypeSpec_builder{
							Cores:       4,
							MemoryGib:   16,
							Description: "Unreferenced instance type.",
						}.Build(),
					}.Build(),
				}.Build())
				Expect(err).ToNot(HaveOccurred())
				instanceType := createResponse.GetObject()

				// Delete without any references:
				_, err = server.Delete(ctx, privatev1.InstanceTypesDeleteRequest_builder{
					Id: instanceType.GetId(),
				}.Build())
				Expect(err).ToNot(HaveOccurred())

				// Verify deletion timestamp is set:
				getResponse, err := server.Get(ctx, privatev1.InstanceTypesGetRequest_builder{
					Id: instanceType.GetId(),
				}.Build())
				Expect(err).ToNot(HaveOccurred())
				Expect(getResponse.GetObject().GetMetadata().GetDeletionTimestamp()).ToNot(BeNil())
			})
		})

		Describe("Spec field validation via protovalidate", func() {
			var validator protovalidate.Validator

			BeforeEach(func() {
				var err error
				validator, err = protovalidate.New()
				Expect(err).ToNot(HaveOccurred())
			})

			It("Accepts valid cores and memory_gib", func() {
				request := privatev1.InstanceTypesCreateRequest_builder{
					Object: privatev1.InstanceType_builder{
						Metadata: privatev1.Metadata_builder{
							Name: "valid-spec",
						}.Build(),
						Spec: privatev1.InstanceTypeSpec_builder{
							Cores:     4,
							MemoryGib: 16,
						}.Build(),
					}.Build(),
				}.Build()
				err := validator.Validate(request)
				Expect(err).ToNot(HaveOccurred())
			})

			It("Rejects cores equal to zero", func() {
				request := privatev1.InstanceTypesCreateRequest_builder{
					Object: privatev1.InstanceType_builder{
						Metadata: privatev1.Metadata_builder{
							Name: "zero-cores",
						}.Build(),
						Spec: privatev1.InstanceTypeSpec_builder{
							Cores:     0,
							MemoryGib: 16,
						}.Build(),
					}.Build(),
				}.Build()
				err := validator.Validate(request)
				Expect(err).To(HaveOccurred())
				Expect(err.Error()).To(ContainSubstring("cores"))
			})

			It("Rejects memory_gib equal to zero", func() {
				request := privatev1.InstanceTypesCreateRequest_builder{
					Object: privatev1.InstanceType_builder{
						Metadata: privatev1.Metadata_builder{
							Name: "zero-memory",
						}.Build(),
						Spec: privatev1.InstanceTypeSpec_builder{
							Cores:     4,
							MemoryGib: 0,
						}.Build(),
					}.Build(),
				}.Build()
				err := validator.Validate(request)
				Expect(err).To(HaveOccurred())
				Expect(err.Error()).To(ContainSubstring("memory_gib"))
			})

			It("Rejects negative cores", func() {
				request := privatev1.InstanceTypesCreateRequest_builder{
					Object: privatev1.InstanceType_builder{
						Metadata: privatev1.Metadata_builder{
							Name: "negative-cores",
						}.Build(),
						Spec: privatev1.InstanceTypeSpec_builder{
							Cores:     -1,
							MemoryGib: 16,
						}.Build(),
					}.Build(),
				}.Build()
				err := validator.Validate(request)
				Expect(err).To(HaveOccurred())
				Expect(err.Error()).To(ContainSubstring("cores"))
			})

			It("Rejects negative memory_gib", func() {
				request := privatev1.InstanceTypesCreateRequest_builder{
					Object: privatev1.InstanceType_builder{
						Metadata: privatev1.Metadata_builder{
							Name: "negative-memory",
						}.Build(),
						Spec: privatev1.InstanceTypeSpec_builder{
							Cores:     4,
							MemoryGib: -1,
						}.Build(),
					}.Build(),
				}.Build()
				err := validator.Validate(request)
				Expect(err).To(HaveOccurred())
				Expect(err.Error()).To(ContainSubstring("memory_gib"))
			})
		})

		Describe("GPU CEL filters", func() {
			It("Filters GPU-enabled types with has(this.spec.gpu)", func() {
				_, err := server.Create(ctx, privatev1.InstanceTypesCreateRequest_builder{
					Object: privatev1.InstanceType_builder{
						Metadata: privatev1.Metadata_builder{
							Name: "with-gpu",
						}.Build(),
						Spec: privatev1.InstanceTypeSpec_builder{
							Cores:     8,
							MemoryGib: 64,
							Gpu: privatev1.GpuSpec_builder{
								PciDeviceSelector: "10DE:20B0",
								ResourceName:      "nvidia.com/A100",
								Count:             1,
							}.Build(),
						}.Build(),
					}.Build(),
				}.Build())
				Expect(err).ToNot(HaveOccurred())

				_, err = server.Create(ctx, privatev1.InstanceTypesCreateRequest_builder{
					Object: privatev1.InstanceType_builder{
						Metadata: privatev1.Metadata_builder{
							Name: "without-gpu",
						}.Build(),
						Spec: privatev1.InstanceTypeSpec_builder{
							Cores:     4,
							MemoryGib: 16,
						}.Build(),
					}.Build(),
				}.Build())
				Expect(err).ToNot(HaveOccurred())

				response, err := server.List(ctx, privatev1.InstanceTypesListRequest_builder{
					Filter: new("has(this.spec.gpu)"),
				}.Build())
				Expect(err).ToNot(HaveOccurred())
				Expect(response.GetItems()).To(HaveLen(1))
				Expect(response.GetItems()[0].GetId()).To(Equal("with-gpu"))
			})

			It("Filters by GPU resource name", func() {
				_, err := server.Create(ctx, privatev1.InstanceTypesCreateRequest_builder{
					Object: privatev1.InstanceType_builder{
						Metadata: privatev1.Metadata_builder{
							Name: "gpu-a100",
						}.Build(),
						Spec: privatev1.InstanceTypeSpec_builder{
							Cores:     8,
							MemoryGib: 64,
							Gpu: privatev1.GpuSpec_builder{
								PciDeviceSelector: "10DE:20B0",
								ResourceName:      "nvidia.com/A100",
								Count:             1,
							}.Build(),
						}.Build(),
					}.Build(),
				}.Build())
				Expect(err).ToNot(HaveOccurred())

				_, err = server.Create(ctx, privatev1.InstanceTypesCreateRequest_builder{
					Object: privatev1.InstanceType_builder{
						Metadata: privatev1.Metadata_builder{
							Name: "gpu-h100",
						}.Build(),
						Spec: privatev1.InstanceTypeSpec_builder{
							Cores:     16,
							MemoryGib: 128,
							Gpu: privatev1.GpuSpec_builder{
								PciDeviceSelector: "10DE:2330",
								ResourceName:      "nvidia.com/H100",
								Count:             2,
							}.Build(),
						}.Build(),
					}.Build(),
				}.Build())
				Expect(err).ToNot(HaveOccurred())

				response, err := server.List(ctx, privatev1.InstanceTypesListRequest_builder{
					Filter: new("this.spec.gpu.resource_name == 'nvidia.com/H100'"),
				}.Build())
				Expect(err).ToNot(HaveOccurred())
				Expect(response.GetItems()).To(HaveLen(1))
				Expect(response.GetItems()[0].GetId()).To(Equal("gpu-h100"))
			})
		})
	})
})

var _ = Describe("Public instance types server", func() {
	Describe("Behaviour", func() {
		var (
			publicServer  *InstanceTypesServer
			privateServer *PrivateInstanceTypesServer
		)

		BeforeEach(func() {
			var err error

			// Create the public server:
			publicServer, err = NewInstanceTypesServer().
				SetLogger(logger).
				SetAttributionLogic(attribution).
				SetTenancyLogic(tenancy).
				Build()
			Expect(err).ToNot(HaveOccurred())

			// Create a private server for test data setup:
			privateServer, err = NewPrivateInstanceTypesServer().
				SetLogger(logger).
				SetAttributionLogic(attribution).
				SetTenancyLogic(tenancy).
				Build()
			Expect(err).ToNot(HaveOccurred())
		})

		// Helper to create an instance type via the private server.
		createInstanceType := func(name string) *privatev1.InstanceType {
			response, err := privateServer.Create(ctx, privatev1.InstanceTypesCreateRequest_builder{
				Object: privatev1.InstanceType_builder{
					Metadata: privatev1.Metadata_builder{
						Name: name,
					}.Build(),
					Spec: privatev1.InstanceTypeSpec_builder{
						Cores:       4,
						MemoryGib:   16,
						Description: "Public server test.",
					}.Build(),
				}.Build(),
			}.Build())
			Expect(err).ToNot(HaveOccurred())
			return response.GetObject()
		}

		// Helper to transition an instance type to a given state via the private server.
		transitionTo := func(id string, state privatev1.InstanceTypeState) {
			_, err := privateServer.Update(ctx, privatev1.InstanceTypesUpdateRequest_builder{
				Object: privatev1.InstanceType_builder{
					Id: id,
					Spec: privatev1.InstanceTypeSpec_builder{
						State: state,
					}.Build(),
				}.Build(),
				UpdateMask: &fieldmaskpb.FieldMask{Paths: []string{"spec.state"}},
			}.Build())
			Expect(err).ToNot(HaveOccurred())
		}

		It("Lists ACTIVE and DEPRECATED by default", func() {
			// Create three instance types with different states:
			active := createInstanceType("pub-active")
			deprecated := createInstanceType("pub-deprecated")
			obsolete := createInstanceType("pub-obsolete")

			transitionTo(deprecated.GetId(),
				privatev1.InstanceTypeState_INSTANCE_TYPE_STATE_DEPRECATED)
			transitionTo(obsolete.GetId(),
				privatev1.InstanceTypeState_INSTANCE_TYPE_STATE_OBSOLETE)

			// Public List without filter should return only ACTIVE + DEPRECATED:
			response, err := publicServer.List(ctx,
				publicv1.InstanceTypesListRequest_builder{}.Build())
			Expect(err).ToNot(HaveOccurred())
			Expect(response.GetItems()).To(HaveLen(2))

			// Verify the returned items are ACTIVE and DEPRECATED:
			ids := make([]string, len(response.GetItems()))
			for i, item := range response.GetItems() {
				ids[i] = item.GetId()
			}
			Expect(ids).To(ContainElement(active.GetId()))
			Expect(ids).To(ContainElement(deprecated.GetId()))
			Expect(ids).ToNot(ContainElement(obsolete.GetId()))
		})

		It("Lists OBSOLETE when filter includes state", func() {
			// Create three instance types with different states:
			createInstanceType("filter-active")
			deprecated := createInstanceType("filter-deprecated")
			obsolete := createInstanceType("filter-obsolete")

			transitionTo(deprecated.GetId(),
				privatev1.InstanceTypeState_INSTANCE_TYPE_STATE_DEPRECATED)
			transitionTo(obsolete.GetId(),
				privatev1.InstanceTypeState_INSTANCE_TYPE_STATE_OBSOLETE)

			// Public List with state filter for OBSOLETE only:
			response, err := publicServer.List(ctx, publicv1.InstanceTypesListRequest_builder{
				Filter: new(fmt.Sprintf("this.spec.state == %d",
					int32(privatev1.InstanceTypeState_INSTANCE_TYPE_STATE_OBSOLETE))),
			}.Build())
			Expect(err).ToNot(HaveOccurred())
			Expect(response.GetItems()).To(HaveLen(1))
			Expect(response.GetItems()[0].GetId()).To(Equal(obsolete.GetId()))
		})

		It("Lists all when filter includes state", func() {
			// Create three instance types with different states:
			createInstanceType("all-active")
			deprecated := createInstanceType("all-deprecated")
			obsolete := createInstanceType("all-obsolete")

			transitionTo(deprecated.GetId(),
				privatev1.InstanceTypeState_INSTANCE_TYPE_STATE_DEPRECATED)
			transitionTo(obsolete.GetId(),
				privatev1.InstanceTypeState_INSTANCE_TYPE_STATE_OBSOLETE)

			// Public List with filter including all states:
			response, err := publicServer.List(ctx, publicv1.InstanceTypesListRequest_builder{
				Filter: new(fmt.Sprintf("this.spec.state == %d || this.spec.state == %d || this.spec.state == %d",
					int32(privatev1.InstanceTypeState_INSTANCE_TYPE_STATE_ACTIVE),
					int32(privatev1.InstanceTypeState_INSTANCE_TYPE_STATE_DEPRECATED),
					int32(privatev1.InstanceTypeState_INSTANCE_TYPE_STATE_OBSOLETE))),
			}.Build())
			Expect(err).ToNot(HaveOccurred())
			Expect(response.GetItems()).To(HaveLen(3))
		})

		It("Gets any instance type regardless of state", func() {
			// Create and transition to OBSOLETE:
			obsolete := createInstanceType("get-obsolete")
			transitionTo(obsolete.GetId(),
				privatev1.InstanceTypeState_INSTANCE_TYPE_STATE_OBSOLETE)

			// Public Get should return the OBSOLETE instance type:
			getResponse, err := publicServer.Get(ctx, publicv1.InstanceTypesGetRequest_builder{
				Id: obsolete.GetId(),
			}.Build())
			Expect(err).ToNot(HaveOccurred())
			Expect(getResponse.GetObject()).ToNot(BeNil())
			Expect(getResponse.GetObject().GetId()).To(Equal(obsolete.GetId()))
		})
	})
})

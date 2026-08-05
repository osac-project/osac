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
	"fmt"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	grpccodes "google.golang.org/grpc/codes"
	grpcstatus "google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/fieldmaskpb"

	privatev1 "github.com/osac-project/osac/fulfillment-service/internal/api/osac/private/v1"
)

var _ = Describe("Private bare metal instance types server", func() {
	Describe("Creation", func() {
		It("Can be built if all the required parameters are set", func() {
			server, err := NewPrivateBareMetalInstanceTypesServer().
				SetLogger(logger).
				SetAttributionLogic(attribution).
				SetTenancyLogic(tenancy).
				Build()
			Expect(err).ToNot(HaveOccurred())
			Expect(server).ToNot(BeNil())
		})

		It("Fails if logger is not set", func() {
			server, err := NewPrivateBareMetalInstanceTypesServer().
				SetAttributionLogic(attribution).
				SetTenancyLogic(tenancy).
				Build()
			Expect(err).To(MatchError("logger is mandatory"))
			Expect(server).To(BeNil())
		})

		It("Fails if tenancy logic is not set", func() {
			server, err := NewPrivateBareMetalInstanceTypesServer().
				SetLogger(logger).
				SetAttributionLogic(attribution).
				Build()
			Expect(err).To(MatchError("tenancy logic is mandatory"))
			Expect(server).To(BeNil())
		})
	})

	Describe("Behaviour", func() {
		var server *PrivateBareMetalInstanceTypesServer

		BeforeEach(func() {
			var err error

			// Create the server:
			server, err = NewPrivateBareMetalInstanceTypesServer().
				SetLogger(logger).
				SetAttributionLogic(attribution).
				SetTenancyLogic(tenancy).
				Build()
			Expect(err).ToNot(HaveOccurred())
		})

		It("Creates object", func() {
			response, err := server.Create(ctx, privatev1.BareMetalInstanceTypesCreateRequest_builder{
				Object: privatev1.BareMetalInstanceType_builder{
					Metadata: privatev1.Metadata_builder{
						Name: "compute-large",
					}.Build(),
					Spec: privatev1.BareMetalInstanceTypeSpec_builder{
						Hardware: privatev1.BareMetalHardwareSpec_builder{
							Cpu: privatev1.BareMetalCPUSpec_builder{
								Cores:          32,
								Architecture:   "x86_64",
								Model:          "Intel Xeon Gold",
								ThreadsPerCore: 2,
							}.Build(),
							Memory: privatev1.BareMetalMemorySpec_builder{
								TotalGb: 128,
								Type:    "DDR4",
							}.Build(),
						}.Build(),
						HostLabelSelector: privatev1.BareMetalLabelSelector_builder{
							MatchLabels: map[string]string{
								"hardware.profile": "compute-large",
							},
						}.Build(),
						Description: "Large compute instance type with 32 cores and 128GB RAM.",
					}.Build(),
				}.Build(),
			}.Build())
			Expect(err).ToNot(HaveOccurred())
			Expect(response).ToNot(BeNil())
			object := response.GetObject()
			Expect(object).ToNot(BeNil())
			Expect(object.GetId()).To(Equal("compute-large"))
			Expect(object.GetSpec().GetHardware().GetCpu().GetCores()).To(Equal(int32(32)))
			Expect(object.GetSpec().GetHardware().GetMemory().GetTotalGb()).To(Equal(int64(128)))
			Expect(object.GetSpec().GetHostLabelSelector().GetMatchLabels()).To(HaveKeyWithValue("hardware.profile", "compute-large"))
		})

		It("List objects", func() {
			// Create a few objects:
			const count = 5
			for i := range count {
				_, err := server.Create(ctx, privatev1.BareMetalInstanceTypesCreateRequest_builder{
					Object: privatev1.BareMetalInstanceType_builder{
						Metadata: privatev1.Metadata_builder{
							Name: fmt.Sprintf("type-%d", i),
						}.Build(),
						Spec: privatev1.BareMetalInstanceTypeSpec_builder{
							Hardware: privatev1.BareMetalHardwareSpec_builder{
								Cpu: privatev1.BareMetalCPUSpec_builder{
									Cores:          16,
									Architecture:   "x86_64",
									ThreadsPerCore: 2,
								}.Build(),
								Memory: privatev1.BareMetalMemorySpec_builder{
									TotalGb: 64,
								}.Build(),
							}.Build(),
							HostLabelSelector: privatev1.BareMetalLabelSelector_builder{
								MatchLabels: map[string]string{
									"type": fmt.Sprintf("type-%d", i),
								},
							}.Build(),
							Description: fmt.Sprintf("Instance type %d.", i),
						}.Build(),
					}.Build(),
				}.Build())
				Expect(err).ToNot(HaveOccurred())
			}

			// List the objects:
			response, err := server.List(ctx, privatev1.BareMetalInstanceTypesListRequest_builder{}.Build())
			Expect(err).ToNot(HaveOccurred())
			Expect(response).ToNot(BeNil())
			items := response.GetItems()
			Expect(items).To(HaveLen(count))
		})

		It("Get object", func() {
			// Create the object:
			createResponse, err := server.Create(ctx, privatev1.BareMetalInstanceTypesCreateRequest_builder{
				Object: privatev1.BareMetalInstanceType_builder{
					Metadata: privatev1.Metadata_builder{
						Name: "gpu-server",
					}.Build(),
					Spec: privatev1.BareMetalInstanceTypeSpec_builder{
						Hardware: privatev1.BareMetalHardwareSpec_builder{
							Cpu: privatev1.BareMetalCPUSpec_builder{
								Cores:          24,
								Architecture:   "x86_64",
								ThreadsPerCore: 2,
							}.Build(),
							Memory: privatev1.BareMetalMemorySpec_builder{
								TotalGb: 256,
							}.Build(),
							Accelerators: []*privatev1.BareMetalAcceleratorSpec{
								privatev1.BareMetalAcceleratorSpec_builder{
									Type:     "GPU",
									Model:    "A100",
									Vendor:   stringPtr("NVIDIA"),
									MemoryGb: int32Ptr(40),
								}.Build(),
							},
						}.Build(),
						HostLabelSelector: privatev1.BareMetalLabelSelector_builder{
							MatchLabels: map[string]string{
								"gpu.type": "nvidia-a100",
							},
						}.Build(),
						Description: "GPU server with NVIDIA A100.",
					}.Build(),
				}.Build(),
			}.Build())
			Expect(err).ToNot(HaveOccurred())

			// Get it:
			getResponse, err := server.Get(ctx, privatev1.BareMetalInstanceTypesGetRequest_builder{
				Id: createResponse.GetObject().GetId(),
			}.Build())
			Expect(err).ToNot(HaveOccurred())
			Expect(getResponse).ToNot(BeNil())
			object := getResponse.GetObject()
			Expect(object).ToNot(BeNil())
			Expect(object.GetId()).To(Equal("gpu-server"))
			Expect(object.GetSpec().GetHardware().GetAccelerators()).To(HaveLen(1))
			Expect(object.GetSpec().GetHardware().GetAccelerators()[0].GetModel()).To(Equal("A100"))
		})

		It("Update object", func() {
			// Create the object:
			createResponse, err := server.Create(ctx, privatev1.BareMetalInstanceTypesCreateRequest_builder{
				Object: privatev1.BareMetalInstanceType_builder{
					Metadata: privatev1.Metadata_builder{
						Name: "updatable-type",
					}.Build(),
					Spec: privatev1.BareMetalInstanceTypeSpec_builder{
						Hardware: privatev1.BareMetalHardwareSpec_builder{
							Cpu: privatev1.BareMetalCPUSpec_builder{
								Cores:          8,
								Architecture:   "x86_64",
								ThreadsPerCore: 2,
							}.Build(),
							Memory: privatev1.BareMetalMemorySpec_builder{
								TotalGb: 32,
							}.Build(),
						}.Build(),
						HostLabelSelector: privatev1.BareMetalLabelSelector_builder{
							MatchLabels: map[string]string{
								"profile": "standard",
							},
						}.Build(),
						Description: "Original description.",
					}.Build(),
				}.Build(),
			}.Build())
			Expect(err).ToNot(HaveOccurred())

			// Update it:
			updateResponse, err := server.Update(ctx, privatev1.BareMetalInstanceTypesUpdateRequest_builder{
				Object: privatev1.BareMetalInstanceType_builder{
					Id: createResponse.GetObject().GetId(),
					Spec: privatev1.BareMetalInstanceTypeSpec_builder{
						Description: "Updated description.",
					}.Build(),
				}.Build(),
				UpdateMask: &fieldmaskpb.FieldMask{
					Paths: []string{"spec.description"},
				},
			}.Build())
			Expect(err).ToNot(HaveOccurred())
			Expect(updateResponse).ToNot(BeNil())
			object := updateResponse.GetObject()
			Expect(object.GetSpec().GetDescription()).To(Equal("Updated description."))
			// Core hardware specs should remain unchanged:
			Expect(object.GetSpec().GetHardware().GetCpu().GetCores()).To(Equal(int32(8)))
			Expect(object.GetSpec().GetHardware().GetMemory().GetTotalGb()).To(Equal(int64(32)))
		})

		It("Delete object", func() {
			// Create the object:
			createResponse, err := server.Create(ctx, privatev1.BareMetalInstanceTypesCreateRequest_builder{
				Object: privatev1.BareMetalInstanceType_builder{
					Metadata: privatev1.Metadata_builder{
						Name: "deletable-type",
					}.Build(),
					Spec: privatev1.BareMetalInstanceTypeSpec_builder{
						Hardware: privatev1.BareMetalHardwareSpec_builder{
							Cpu: privatev1.BareMetalCPUSpec_builder{
								Cores:          4,
								Architecture:   "x86_64",
								ThreadsPerCore: 2,
							}.Build(),
							Memory: privatev1.BareMetalMemorySpec_builder{
								TotalGb: 16,
							}.Build(),
						}.Build(),
						HostLabelSelector: privatev1.BareMetalLabelSelector_builder{
							MatchLabels: map[string]string{
								"delete": "me",
							},
						}.Build(),
					}.Build(),
				}.Build(),
			}.Build())
			Expect(err).ToNot(HaveOccurred())

			// Delete it:
			_, err = server.Delete(ctx, privatev1.BareMetalInstanceTypesDeleteRequest_builder{
				Id: createResponse.GetObject().GetId(),
			}.Build())
			Expect(err).ToNot(HaveOccurred())

			// Verify it's gone:
			_, err = server.Get(ctx, privatev1.BareMetalInstanceTypesGetRequest_builder{
				Id: createResponse.GetObject().GetId(),
			}.Build())
			Expect(err).To(HaveOccurred())
			status, ok := grpcstatus.FromError(err)
			Expect(ok).To(BeTrue())
			Expect(status.Code()).To(Equal(grpccodes.NotFound))
		})

		It("Signal object", func() {
			// Create the object:
			createResponse, err := server.Create(ctx, privatev1.BareMetalInstanceTypesCreateRequest_builder{
				Object: privatev1.BareMetalInstanceType_builder{
					Metadata: privatev1.Metadata_builder{
						Name: "signal-type",
					}.Build(),
					Spec: privatev1.BareMetalInstanceTypeSpec_builder{
						Hardware: privatev1.BareMetalHardwareSpec_builder{
							Cpu: privatev1.BareMetalCPUSpec_builder{
								Cores:          2,
								Architecture:   "x86_64",
								ThreadsPerCore: 2,
							}.Build(),
							Memory: privatev1.BareMetalMemorySpec_builder{
								TotalGb: 8,
							}.Build(),
						}.Build(),
						HostLabelSelector: privatev1.BareMetalLabelSelector_builder{
							MatchLabels: map[string]string{
								"signal": "test",
							},
						}.Build(),
					}.Build(),
				}.Build(),
			}.Build())
			Expect(err).ToNot(HaveOccurred())

			// Signal it:
			_, err = server.Signal(ctx, privatev1.BareMetalInstanceTypesSignalRequest_builder{
				Id: createResponse.GetObject().GetId(),
			}.Build())
			Expect(err).ToNot(HaveOccurred())
		})

		// Immutability tests
		Describe("Immutability", func() {
			It("Rejects update of CPU cores", func() {
				createResponse, err := server.Create(ctx, privatev1.BareMetalInstanceTypesCreateRequest_builder{
					Object: privatev1.BareMetalInstanceType_builder{
						Metadata: privatev1.Metadata_builder{
							Name: "immutable-cores",
						}.Build(),
						Spec: privatev1.BareMetalInstanceTypeSpec_builder{
							Hardware: privatev1.BareMetalHardwareSpec_builder{
								Cpu: privatev1.BareMetalCPUSpec_builder{
									Cores:          16,
									Architecture:   "x86_64",
									ThreadsPerCore: 2,
								}.Build(),
								Memory: privatev1.BareMetalMemorySpec_builder{
									TotalGb: 64,
								}.Build(),
							}.Build(),
							HostLabelSelector: privatev1.BareMetalLabelSelector_builder{
								MatchLabels: map[string]string{
									"profile": "test",
								},
							}.Build(),
						}.Build(),
					}.Build(),
				}.Build())
				Expect(err).ToNot(HaveOccurred())

				_, err = server.Update(ctx, privatev1.BareMetalInstanceTypesUpdateRequest_builder{
					Object: privatev1.BareMetalInstanceType_builder{
						Id: createResponse.GetObject().GetId(),
						Spec: privatev1.BareMetalInstanceTypeSpec_builder{
							Hardware: privatev1.BareMetalHardwareSpec_builder{
								Cpu: privatev1.BareMetalCPUSpec_builder{
									Cores: 32, // Different value
								}.Build(),
							}.Build(),
						}.Build(),
					}.Build(),
				}.Build())
				Expect(err).To(HaveOccurred())
				status, ok := grpcstatus.FromError(err)
				Expect(ok).To(BeTrue())
				Expect(status.Code()).To(Equal(grpccodes.InvalidArgument))
				Expect(status.Message()).To(ContainSubstring("spec.hardware"))
				Expect(status.Message()).To(ContainSubstring("immutable"))
			})

			It("Rejects update of CPU architecture", func() {
				createResponse, err := server.Create(ctx, privatev1.BareMetalInstanceTypesCreateRequest_builder{
					Object: privatev1.BareMetalInstanceType_builder{
						Metadata: privatev1.Metadata_builder{
							Name: "immutable-arch",
						}.Build(),
						Spec: privatev1.BareMetalInstanceTypeSpec_builder{
							Hardware: privatev1.BareMetalHardwareSpec_builder{
								Cpu: privatev1.BareMetalCPUSpec_builder{
									Cores:          16,
									Architecture:   "x86_64",
									ThreadsPerCore: 2,
								}.Build(),
								Memory: privatev1.BareMetalMemorySpec_builder{
									TotalGb: 64,
								}.Build(),
							}.Build(),
							HostLabelSelector: privatev1.BareMetalLabelSelector_builder{
								MatchLabels: map[string]string{
									"profile": "test",
								},
							}.Build(),
						}.Build(),
					}.Build(),
				}.Build())
				Expect(err).ToNot(HaveOccurred())

				_, err = server.Update(ctx, privatev1.BareMetalInstanceTypesUpdateRequest_builder{
					Object: privatev1.BareMetalInstanceType_builder{
						Id: createResponse.GetObject().GetId(),
						Spec: privatev1.BareMetalInstanceTypeSpec_builder{
							Hardware: privatev1.BareMetalHardwareSpec_builder{
								Cpu: privatev1.BareMetalCPUSpec_builder{
									Architecture: "aarch64", // Different value
								}.Build(),
							}.Build(),
						}.Build(),
					}.Build(),
				}.Build())
				Expect(err).To(HaveOccurred())
				status, ok := grpcstatus.FromError(err)
				Expect(ok).To(BeTrue())
				Expect(status.Code()).To(Equal(grpccodes.InvalidArgument))
				Expect(status.Message()).To(ContainSubstring("spec.hardware"))
				Expect(status.Message()).To(ContainSubstring("immutable"))
			})

			It("Rejects update of memory total", func() {
				createResponse, err := server.Create(ctx, privatev1.BareMetalInstanceTypesCreateRequest_builder{
					Object: privatev1.BareMetalInstanceType_builder{
						Metadata: privatev1.Metadata_builder{
							Name: "immutable-memory",
						}.Build(),
						Spec: privatev1.BareMetalInstanceTypeSpec_builder{
							Hardware: privatev1.BareMetalHardwareSpec_builder{
								Cpu: privatev1.BareMetalCPUSpec_builder{
									Cores:          16,
									Architecture:   "x86_64",
									ThreadsPerCore: 2,
								}.Build(),
								Memory: privatev1.BareMetalMemorySpec_builder{
									TotalGb: 64,
								}.Build(),
							}.Build(),
							HostLabelSelector: privatev1.BareMetalLabelSelector_builder{
								MatchLabels: map[string]string{
									"profile": "test",
								},
							}.Build(),
						}.Build(),
					}.Build(),
				}.Build())
				Expect(err).ToNot(HaveOccurred())

				_, err = server.Update(ctx, privatev1.BareMetalInstanceTypesUpdateRequest_builder{
					Object: privatev1.BareMetalInstanceType_builder{
						Id: createResponse.GetObject().GetId(),
						Spec: privatev1.BareMetalInstanceTypeSpec_builder{
							Hardware: privatev1.BareMetalHardwareSpec_builder{
								Memory: privatev1.BareMetalMemorySpec_builder{
									TotalGb: 128, // Different value
								}.Build(),
							}.Build(),
						}.Build(),
					}.Build(),
				}.Build())
				Expect(err).To(HaveOccurred())
				status, ok := grpcstatus.FromError(err)
				Expect(ok).To(BeTrue())
				Expect(status.Code()).To(Equal(grpccodes.InvalidArgument))
				Expect(status.Message()).To(ContainSubstring("spec.hardware"))
				Expect(status.Message()).To(ContainSubstring("immutable"))
			})

			It("Rejects update of name", func() {
				createResponse, err := server.Create(ctx, privatev1.BareMetalInstanceTypesCreateRequest_builder{
					Object: privatev1.BareMetalInstanceType_builder{
						Metadata: privatev1.Metadata_builder{
							Name: "original-name",
						}.Build(),
						Spec: privatev1.BareMetalInstanceTypeSpec_builder{
							Hardware: privatev1.BareMetalHardwareSpec_builder{
								Cpu: privatev1.BareMetalCPUSpec_builder{
									Cores:          16,
									Architecture:   "x86_64",
									ThreadsPerCore: 2,
								}.Build(),
								Memory: privatev1.BareMetalMemorySpec_builder{
									TotalGb: 64,
								}.Build(),
							}.Build(),
							HostLabelSelector: privatev1.BareMetalLabelSelector_builder{
								MatchLabels: map[string]string{
									"profile": "test",
								},
							}.Build(),
						}.Build(),
					}.Build(),
				}.Build())
				Expect(err).ToNot(HaveOccurred())

				_, err = server.Update(ctx, privatev1.BareMetalInstanceTypesUpdateRequest_builder{
					Object: privatev1.BareMetalInstanceType_builder{
						Id: createResponse.GetObject().GetId(),
						Metadata: privatev1.Metadata_builder{
							Name: "different-name",
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
		})
	})
})

// Helper functions for optional fields
func stringPtr(s string) *string {
	return &s
}

func int32Ptr(i int32) *int32 {
	return &i
}

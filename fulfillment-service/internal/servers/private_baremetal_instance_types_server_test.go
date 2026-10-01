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

	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	grpccodes "google.golang.org/grpc/codes"
	grpcstatus "google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/fieldmaskpb"

	"github.com/osac-project/osac/fulfillment-service/internal/auth"
	privatev1 "github.com/osac-project/osac/proto/gen/osac/private/v1"
	publicv1 "github.com/osac-project/osac/proto/gen/osac/public/v1"
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

		Describe("Fabric bindings", func() {
			newBinding := func(templateID string) *privatev1.BareMetalFabricBindings {
				return privatev1.BareMetalFabricBindings_builder{
					EthernetEw: privatev1.BareMetalEthernetFabricBinding_builder{
						Netris: privatev1.BareMetalNetrisFabricBinding_builder{
							NetworkClass: "network-class-id", TemplateId: templateID,
						}.Build(),
					}.Build(),
				}.Build()
			}
			create := func() *privatev1.BareMetalInstanceType {
				response, err := server.Create(ctx, privatev1.BareMetalInstanceTypesCreateRequest_builder{
					Object: privatev1.BareMetalInstanceType_builder{
						Metadata: privatev1.Metadata_builder{Name: "fabric-type"}.Build(),
						Spec: privatev1.BareMetalInstanceTypeSpec_builder{
							Description: "Original description",
							Hardware: privatev1.BareMetalHardwareSpec_builder{
								Cpu:    privatev1.BareMetalCPUSpec_builder{Cores: 8, Architecture: "x86_64", ThreadsPerCore: 2}.Build(),
								Memory: privatev1.BareMetalMemorySpec_builder{TotalGb: 32}.Build(),
								NetworkPorts: []*privatev1.BareMetalNetworkPortSpec{
									privatev1.BareMetalNetworkPortSpec_builder{Name: "data-0", Role: "fabric", Type: "Ethernet", Speed: "25Gbps"}.Build(),
								},
							}.Build(),
							HostLabelSelector: privatev1.BareMetalLabelSelector_builder{MatchLabels: map[string]string{"profile": "fabric"}}.Build(),
							FabricBindings:    newBinding("42"),
						}.Build(),
					}.Build(),
				}.Build())
				Expect(err).ToNot(HaveOccurred())
				return response.GetObject()
			}

			It("persists private bindings on create and get", func() {
				object := create()
				response, err := server.Get(ctx, privatev1.BareMetalInstanceTypesGetRequest_builder{Id: object.GetId()}.Build())
				Expect(err).ToNot(HaveOccurred())
				Expect(proto.Equal(response.GetObject().GetSpec().GetFabricBindings(), newBinding("42"))).To(BeTrue())
			})

			DescribeTable("updates masked binding paths", func(path string) {
				object := create()
				binding := newBinding("18446744073709551615")
				if path == "spec.fabric_bindings.ethernet_ew.netris.template_id" {
					// A leaf update must use the stored NetworkClass, not validate the partial request.
					binding.GetEthernetEw().GetNetris().SetNetworkClass("")
				}
				response, err := server.Update(ctx, privatev1.BareMetalInstanceTypesUpdateRequest_builder{
					Object: privatev1.BareMetalInstanceType_builder{
						Id:   object.GetId(),
						Spec: privatev1.BareMetalInstanceTypeSpec_builder{FabricBindings: binding}.Build(),
					}.Build(),
					UpdateMask: &fieldmaskpb.FieldMask{Paths: []string{path}},
				}.Build())
				Expect(err).ToNot(HaveOccurred())
				updated := response.GetObject()
				Expect(updated.GetSpec().GetFabricBindings().GetEthernetEw().GetNetris().GetTemplateId()).To(Equal("18446744073709551615"))
				Expect(updated.GetSpec().GetFabricBindings().GetEthernetEw().GetNetris().GetNetworkClass()).To(Equal("network-class-id"))
				Expect(updated.GetSpec().GetDescription()).To(Equal("Original description"))
				Expect(proto.Equal(updated.GetSpec().GetHardware(), object.GetSpec().GetHardware())).To(BeTrue())
			},
				Entry("bindings", "spec.fabric_bindings"),
				Entry("Ethernet", "spec.fabric_bindings.ethernet_ew"),
				Entry("Netris", "spec.fabric_bindings.ethernet_ew.netris"),
				Entry("template", "spec.fabric_bindings.ethernet_ew.netris.template_id"),
			)

			DescribeTable("clears optional nested binding paths", func(path string) {
				object := create()
				response, err := server.Update(ctx, privatev1.BareMetalInstanceTypesUpdateRequest_builder{
					Object:     privatev1.BareMetalInstanceType_builder{Id: object.GetId()}.Build(),
					UpdateMask: &fieldmaskpb.FieldMask{Paths: []string{path}},
				}.Build())
				Expect(err).ToNot(HaveOccurred())
				Expect(response.GetObject().GetSpec().GetFabricBindings().GetEthernetEw().GetNetris()).To(BeNil())
				stored, err := server.Get(ctx, privatev1.BareMetalInstanceTypesGetRequest_builder{Id: object.GetId()}.Build())
				Expect(err).ToNot(HaveOccurred())
				Expect(stored.GetObject().GetSpec().GetFabricBindings().GetEthernetEw().GetNetris()).To(BeNil())
			},
				Entry("bindings", "spec.fabric_bindings"),
				Entry("Ethernet", "spec.fabric_bindings.ethernet_ew"),
				Entry("Netris", "spec.fabric_bindings.ethernet_ew.netris"),
			)

			DescribeTable("rejects clearing required binding leaves", func(path string) {
				object := create()
				_, err := server.Update(ctx, privatev1.BareMetalInstanceTypesUpdateRequest_builder{
					Object:     privatev1.BareMetalInstanceType_builder{Id: object.GetId()}.Build(),
					UpdateMask: &fieldmaskpb.FieldMask{Paths: []string{path}},
				}.Build())
				Expect(grpcstatus.Code(err)).To(Equal(grpccodes.InvalidArgument))
				stored, err := server.Get(ctx, privatev1.BareMetalInstanceTypesGetRequest_builder{Id: object.GetId()}.Build())
				Expect(err).ToNot(HaveOccurred())
				Expect(proto.Equal(stored.GetObject().GetSpec().GetFabricBindings(), newBinding("42"))).To(BeTrue())
			},
				Entry("template", "spec.fabric_bindings.ethernet_ew.netris.template_id"),
				Entry("NetworkClass", "spec.fabric_bindings.ethernet_ew.netris.network_class"),
			)

			DescribeTable("rejects invalid template updates", func(templateID string) {
				object := create()
				_, err := server.Update(ctx, privatev1.BareMetalInstanceTypesUpdateRequest_builder{
					Object: privatev1.BareMetalInstanceType_builder{
						Id:   object.GetId(),
						Spec: privatev1.BareMetalInstanceTypeSpec_builder{FabricBindings: newBinding(templateID)}.Build(),
					}.Build(),
					UpdateMask: &fieldmaskpb.FieldMask{Paths: []string{"spec.fabric_bindings.ethernet_ew.netris.template_id"}},
				}.Build())
				Expect(grpcstatus.Code(err)).To(Equal(grpccodes.InvalidArgument))
			},
				Entry("zero", "0"),
				Entry("leading zero", "042"),
				Entry("overflow", "18446744073709551616"),
			)

			It("ignores unmasked invalid bindings", func() {
				object := create()
				response, err := server.Update(ctx, privatev1.BareMetalInstanceTypesUpdateRequest_builder{
					Object: privatev1.BareMetalInstanceType_builder{
						Id:   object.GetId(),
						Spec: privatev1.BareMetalInstanceTypeSpec_builder{Description: "Changed", FabricBindings: newBinding("bad")}.Build(),
					}.Build(),
					UpdateMask: &fieldmaskpb.FieldMask{Paths: []string{"spec.description"}},
				}.Build())
				Expect(err).ToNot(HaveOccurred())
				Expect(response.GetObject().GetSpec().GetFabricBindings().GetEthernetEw().GetNetris().GetTemplateId()).To(Equal("42"))
			})

			It("hides bindings in public get and list responses", func() {
				object := create()
				publicServer, err := NewBareMetalInstanceTypesServer().SetLogger(logger).SetAttributionLogic(attribution).SetTenancyLogic(tenancy).Build()
				Expect(err).ToNot(HaveOccurred())
				got, err := publicServer.Get(ctx, publicv1.BareMetalInstanceTypesGetRequest_builder{Id: object.GetId()}.Build())
				Expect(err).ToNot(HaveOccurred())
				listed, err := publicServer.List(ctx, &publicv1.BareMetalInstanceTypesListRequest{})
				Expect(err).ToNot(HaveOccurred())
				Expect(listed.GetItems()).To(HaveLen(1))
				for _, projected := range []*publicv1.BareMetalInstanceType{got.GetObject(), listed.GetItems()[0]} {
					data, err := protojson.Marshal(projected)
					Expect(err).ToNot(HaveOccurred())
					Expect(string(data)).ToNot(ContainSubstring("fabricBindings"))
					Expect(string(data)).ToNot(ContainSubstring("network-class-id"))
					Expect(projected.GetSpec().ProtoReflect().GetUnknown()).To(BeEmpty())
					Expect(projected.GetSpec().GetDescription()).To(Equal("Original description"))
				}
			})
		})

		It("Creates object", func() {
			response, err := server.Create(ctx, privatev1.BareMetalInstanceTypesCreateRequest_builder{
				Object: privatev1.BareMetalInstanceType_builder{
					Metadata: privatev1.Metadata_builder{
						Name:   "compute-large",
						Tenant: auth.SharedTenant,
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
							NetworkPorts: []*privatev1.BareMetalNetworkPortSpec{
								privatev1.BareMetalNetworkPortSpec_builder{
									Name:  "data-0",
									Role:  "fabric",
									Type:  "Ethernet",
									Speed: "25Gbps",
								}.Build(),
							},
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
							Name:   fmt.Sprintf("type-%d", i),
							Tenant: auth.SharedTenant,
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
								NetworkPorts: []*privatev1.BareMetalNetworkPortSpec{
									privatev1.BareMetalNetworkPortSpec_builder{
										Name:  "data-0",
										Role:  "fabric",
										Type:  "Ethernet",
										Speed: "25Gbps",
									}.Build(),
								},
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
						Name:   "gpu-server",
						Tenant: auth.SharedTenant,
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
							NetworkPorts: []*privatev1.BareMetalNetworkPortSpec{
								privatev1.BareMetalNetworkPortSpec_builder{
									Name:  "data-0",
									Role:  "fabric",
									Type:  "Ethernet",
									Speed: "25Gbps",
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
						Name:   "updatable-type",
						Tenant: auth.SharedTenant,
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
							NetworkPorts: []*privatev1.BareMetalNetworkPortSpec{
								privatev1.BareMetalNetworkPortSpec_builder{
									Name:  "data-0",
									Role:  "fabric",
									Type:  "Ethernet",
									Speed: "25Gbps",
								}.Build(),
							},
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
						Name:   "deletable-type",
						Tenant: auth.SharedTenant,
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
							NetworkPorts: []*privatev1.BareMetalNetworkPortSpec{
								privatev1.BareMetalNetworkPortSpec_builder{
									Name:  "data-0",
									Role:  "fabric",
									Type:  "Ethernet",
									Speed: "25Gbps",
								}.Build(),
							},
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
						Name:   "signal-type",
						Tenant: auth.SharedTenant,
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
							NetworkPorts: []*privatev1.BareMetalNetworkPortSpec{
								privatev1.BareMetalNetworkPortSpec_builder{
									Name:  "data-0",
									Role:  "fabric",
									Type:  "Ethernet",
									Speed: "25Gbps",
								}.Build(),
							},
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
							Name:   "immutable-cores",
							Tenant: auth.SharedTenant,
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
								NetworkPorts: []*privatev1.BareMetalNetworkPortSpec{
									privatev1.BareMetalNetworkPortSpec_builder{
										Name:  "data-0",
										Role:  "fabric",
										Type:  "Ethernet",
										Speed: "25Gbps",
									}.Build(),
								},
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
							Name:   "immutable-arch",
							Tenant: auth.SharedTenant,
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
								NetworkPorts: []*privatev1.BareMetalNetworkPortSpec{
									privatev1.BareMetalNetworkPortSpec_builder{
										Name:  "data-0",
										Role:  "fabric",
										Type:  "Ethernet",
										Speed: "25Gbps",
									}.Build(),
								},
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
							Name:   "immutable-memory",
							Tenant: auth.SharedTenant,
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
								NetworkPorts: []*privatev1.BareMetalNetworkPortSpec{
									privatev1.BareMetalNetworkPortSpec_builder{
										Name:  "data-0",
										Role:  "fabric",
										Type:  "Ethernet",
										Speed: "25Gbps",
									}.Build(),
								},
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
							Name:   "original-name",
							Tenant: auth.SharedTenant,
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
								NetworkPorts: []*privatev1.BareMetalNetworkPortSpec{
									privatev1.BareMetalNetworkPortSpec_builder{
										Name:  "data-0",
										Role:  "fabric",
										Type:  "Ethernet",
										Speed: "25Gbps",
									}.Build(),
								},
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

		It("Rejects creation in a non-shared tenant", func() {
			_, err := server.Create(ctx, privatev1.BareMetalInstanceTypesCreateRequest_builder{
				Object: privatev1.BareMetalInstanceType_builder{
					Metadata: privatev1.Metadata_builder{
						Name:   "tenant-scoped-type",
						Tenant: testTenant,
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
					}.Build(),
				}.Build(),
			}.Build())
			Expect(err).To(HaveOccurred())
			status, ok := grpcstatus.FromError(err)
			Expect(ok).To(BeTrue())
			Expect(status.Code()).To(Equal(grpccodes.InvalidArgument))
			Expect(status.Message()).To(ContainSubstring("metadata.tenant"))
			Expect(status.Message()).To(ContainSubstring(auth.SharedTenant))
		})

		It("Rejects update that moves the object to another tenant", func() {
			createResponse, err := server.Create(ctx, privatev1.BareMetalInstanceTypesCreateRequest_builder{
				Object: privatev1.BareMetalInstanceType_builder{
					Metadata: privatev1.Metadata_builder{
						Name:   "immutable-tenant-type",
						Tenant: auth.SharedTenant,
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
							NetworkPorts: []*privatev1.BareMetalNetworkPortSpec{
								privatev1.BareMetalNetworkPortSpec_builder{
									Name:  "data-0",
									Role:  "fabric",
									Type:  "Ethernet",
									Speed: "25Gbps",
								}.Build(),
							},
						}.Build(),
						HostLabelSelector: privatev1.BareMetalLabelSelector_builder{
							MatchLabels: map[string]string{"profile": "test"},
						}.Build(),
					}.Build(),
				}.Build(),
			}.Build())
			Expect(err).ToNot(HaveOccurred())

			// BareMetalInstanceType is locked to the shared tenant, so an attempt to move it to
			// another tenant is rejected by the allowed-tenants guard before the object is written.
			_, err = server.Update(ctx, privatev1.BareMetalInstanceTypesUpdateRequest_builder{
				Object: privatev1.BareMetalInstanceType_builder{
					Id: createResponse.GetObject().GetId(),
					Metadata: privatev1.Metadata_builder{
						Tenant: testTenant,
					}.Build(),
				}.Build(),
			}.Build())
			Expect(err).To(HaveOccurred())
			status, ok := grpcstatus.FromError(err)
			Expect(ok).To(BeTrue())
			Expect(status.Code()).To(Equal(grpccodes.PermissionDenied))
			Expect(status.Message()).To(ContainSubstring(testTenant))
		})

		It("Rejects creation with a project set", func() {
			_, err := server.Create(ctx, privatev1.BareMetalInstanceTypesCreateRequest_builder{
				Object: privatev1.BareMetalInstanceType_builder{
					Metadata: privatev1.Metadata_builder{
						Name:    "project-scoped-type",
						Tenant:  auth.SharedTenant,
						Project: "some-project",
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
					}.Build(),
				}.Build(),
			}.Build())
			Expect(err).To(HaveOccurred())
			status, ok := grpcstatus.FromError(err)
			Expect(ok).To(BeTrue())
			Expect(status.Code()).To(Equal(grpccodes.InvalidArgument))
			Expect(status.Message()).To(ContainSubstring("metadata.project"))
		})

		It("Defaults an omitted tenant to shared", func() {
			createResponse, err := server.Create(ctx, privatev1.BareMetalInstanceTypesCreateRequest_builder{
				Object: privatev1.BareMetalInstanceType_builder{
					Metadata: privatev1.Metadata_builder{
						Name: "default-tenant-type",
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
							NetworkPorts: []*privatev1.BareMetalNetworkPortSpec{
								privatev1.BareMetalNetworkPortSpec_builder{
									Name:  "data-0",
									Role:  "fabric",
									Type:  "Ethernet",
									Speed: "25Gbps",
								}.Build(),
							},
						}.Build(),
						HostLabelSelector: privatev1.BareMetalLabelSelector_builder{
							MatchLabels: map[string]string{"profile": "test"},
						}.Build(),
					}.Build(),
				}.Build(),
			}.Build())
			Expect(err).ToNot(HaveOccurred())
			Expect(createResponse.GetObject().GetMetadata().GetTenant()).To(Equal(auth.SharedTenant))
		})

		It("Rejects update that changes project", func() {
			createResponse, err := server.Create(ctx, privatev1.BareMetalInstanceTypesCreateRequest_builder{
				Object: privatev1.BareMetalInstanceType_builder{
					Metadata: privatev1.Metadata_builder{
						Name:   "immutable-project-type",
						Tenant: auth.SharedTenant,
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
							NetworkPorts: []*privatev1.BareMetalNetworkPortSpec{
								privatev1.BareMetalNetworkPortSpec_builder{
									Name:  "data-0",
									Role:  "fabric",
									Type:  "Ethernet",
									Speed: "25Gbps",
								}.Build(),
							},
						}.Build(),
						HostLabelSelector: privatev1.BareMetalLabelSelector_builder{
							MatchLabels: map[string]string{"profile": "test"},
						}.Build(),
					}.Build(),
				}.Build(),
			}.Build())
			Expect(err).ToNot(HaveOccurred())

			_, err = server.Update(ctx, privatev1.BareMetalInstanceTypesUpdateRequest_builder{
				Object: privatev1.BareMetalInstanceType_builder{
					Id: createResponse.GetObject().GetId(),
					Metadata: privatev1.Metadata_builder{
						Project: "some-project",
					}.Build(),
				}.Build(),
			}.Build())
			Expect(err).To(HaveOccurred())
			status, ok := grpcstatus.FromError(err)
			Expect(ok).To(BeTrue())
			Expect(status.Code()).To(Equal(grpccodes.InvalidArgument))
			Expect(status.Message()).To(ContainSubstring("metadata.project"))
			Expect(status.Message()).To(ContainSubstring("immutable"))
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

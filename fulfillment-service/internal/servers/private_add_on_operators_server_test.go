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
	"github.com/osac-project/osac/fulfillment-service/internal/uuid"
)

var _ = Describe("Private add-on operators server", func() {
	Describe("Creation", func() {
		It("Can be built if all the required parameters are set", func() {
			server, err := NewPrivateAddOnOperatorsServer().
				SetLogger(logger).
				SetAttributionLogic(attribution).
				SetTenancyLogic(tenancy).
				Build()
			Expect(err).ToNot(HaveOccurred())
			Expect(server).ToNot(BeNil())
		})

		It("Fails if logger is not set", func() {
			server, err := NewPrivateAddOnOperatorsServer().
				SetAttributionLogic(attribution).
				SetTenancyLogic(tenancy).
				Build()
			Expect(err).To(MatchError("logger is mandatory"))
			Expect(server).To(BeNil())
		})

		It("Fails if tenancy logic is not set", func() {
			server, err := NewPrivateAddOnOperatorsServer().
				SetLogger(logger).
				SetAttributionLogic(attribution).
				Build()
			Expect(err).To(MatchError("tenancy logic is mandatory"))
			Expect(server).To(BeNil())
		})
	})

	Describe("Behaviour", func() {
		var server *PrivateAddOnOperatorsServer

		BeforeEach(func() {
			var err error
			server, err = NewPrivateAddOnOperatorsServer().
				SetLogger(logger).
				SetAttributionLogic(attribution).
				SetTenancyLogic(tenancy).
				Build()
			Expect(err).ToNot(HaveOccurred())
		})

		It("Creates object", func() {
			response, err := server.Create(ctx, privatev1.AddOnOperatorsCreateRequest_builder{
				Object: privatev1.AddOnOperator_builder{
					Metadata: privatev1.Metadata_builder{
						Name: fmt.Sprintf("test-%s", uuid.New()[24:32]),
					}.Build(),
					Title:       "GPU Operator",
					Description: "Installs the NVIDIA GPU Operator.",
				}.Build(),
			}.Build())
			Expect(err).ToNot(HaveOccurred())
			Expect(response).ToNot(BeNil())
			object := response.GetObject()
			Expect(object).ToNot(BeNil())
			Expect(object.GetId()).ToNot(BeEmpty())
			Expect(object.GetTitle()).To(Equal("GPU Operator"))
			Expect(object.GetPublished()).To(BeFalse())
		})

		It("Creates object with version constraints", func() {
			response, err := server.Create(ctx, privatev1.AddOnOperatorsCreateRequest_builder{
				Object: privatev1.AddOnOperator_builder{
					Metadata: privatev1.Metadata_builder{
						Name: fmt.Sprintf("test-%s", uuid.New()[24:32]),
					}.Build(),
					Title:         "GPU Operator",
					MinOcpVersion: "4.14.0",
					MaxOcpVersion: "4.17.0",
				}.Build(),
			}.Build())
			Expect(err).ToNot(HaveOccurred())
			Expect(response.GetObject().GetMinOcpVersion()).To(Equal("4.14.0"))
			Expect(response.GetObject().GetMaxOcpVersion()).To(Equal("4.17.0"))
		})

		It("Rejects inverted version range", func() {
			_, err := server.Create(ctx, privatev1.AddOnOperatorsCreateRequest_builder{
				Object: privatev1.AddOnOperator_builder{
					Metadata: privatev1.Metadata_builder{
						Name: fmt.Sprintf("test-%s", uuid.New()[24:32]),
					}.Build(),
					Title:         "GPU Operator",
					MinOcpVersion: "4.17.0",
					MaxOcpVersion: "4.14.0",
				}.Build(),
			}.Build())
			Expect(err).To(HaveOccurred())
			status, ok := grpcstatus.FromError(err)
			Expect(ok).To(BeTrue())
			Expect(status.Code()).To(Equal(grpccodes.InvalidArgument))
			Expect(status.Message()).To(ContainSubstring("min_ocp_version"))
		})

		It("Rejects invalid min version", func() {
			_, err := server.Create(ctx, privatev1.AddOnOperatorsCreateRequest_builder{
				Object: privatev1.AddOnOperator_builder{
					Metadata: privatev1.Metadata_builder{
						Name: fmt.Sprintf("test-%s", uuid.New()[24:32]),
					}.Build(),
					Title:         "GPU Operator",
					MinOcpVersion: "not-a-version",
				}.Build(),
			}.Build())
			Expect(err).To(HaveOccurred())
			status, ok := grpcstatus.FromError(err)
			Expect(ok).To(BeTrue())
			Expect(status.Code()).To(Equal(grpccodes.InvalidArgument))
			Expect(status.Message()).To(ContainSubstring("min_ocp_version"))
		})

		It("Rejects invalid max version", func() {
			_, err := server.Create(ctx, privatev1.AddOnOperatorsCreateRequest_builder{
				Object: privatev1.AddOnOperator_builder{
					Metadata: privatev1.Metadata_builder{
						Name: fmt.Sprintf("test-%s", uuid.New()[24:32]),
					}.Build(),
					Title:         "GPU Operator",
					MaxOcpVersion: "not-a-version",
				}.Build(),
			}.Build())
			Expect(err).To(HaveOccurred())
			status, ok := grpcstatus.FromError(err)
			Expect(ok).To(BeTrue())
			Expect(status.Code()).To(Equal(grpccodes.InvalidArgument))
			Expect(status.Message()).To(ContainSubstring("max_ocp_version"))
		})

		It("Accepts empty version fields", func() {
			response, err := server.Create(ctx, privatev1.AddOnOperatorsCreateRequest_builder{
				Object: privatev1.AddOnOperator_builder{
					Metadata: privatev1.Metadata_builder{
						Name: fmt.Sprintf("test-%s", uuid.New()[24:32]),
					}.Build(),
					Title: "GPU Operator",
				}.Build(),
			}.Build())
			Expect(err).ToNot(HaveOccurred())
			Expect(response.GetObject().GetMinOcpVersion()).To(BeEmpty())
			Expect(response.GetObject().GetMaxOcpVersion()).To(BeEmpty())
		})

		It("List objects", func() {
			const count = 3
			for i := range count {
				_, err := server.Create(ctx, privatev1.AddOnOperatorsCreateRequest_builder{
					Object: privatev1.AddOnOperator_builder{
						Metadata: privatev1.Metadata_builder{
							Name: fmt.Sprintf("my-addon-%d", i),
						}.Build(),
						Title: fmt.Sprintf("Addon %d", i),
					}.Build(),
				}.Build())
				Expect(err).ToNot(HaveOccurred())
			}

			response, err := server.List(ctx, privatev1.AddOnOperatorsListRequest_builder{
				Filter: new("this.metadata.name.startsWith('my-addon-')"),
			}.Build())
			Expect(err).ToNot(HaveOccurred())
			Expect(response.GetItems()).To(HaveLen(count))
		})

		It("Get object", func() {
			createResponse, err := server.Create(ctx, privatev1.AddOnOperatorsCreateRequest_builder{
				Object: privatev1.AddOnOperator_builder{
					Metadata: privatev1.Metadata_builder{
						Name: fmt.Sprintf("test-%s", uuid.New()[24:32]),
					}.Build(),
					Title: "GPU Operator",
				}.Build(),
			}.Build())
			Expect(err).ToNot(HaveOccurred())

			getResponse, err := server.Get(ctx, privatev1.AddOnOperatorsGetRequest_builder{
				Id: createResponse.GetObject().GetId(),
			}.Build())
			Expect(err).ToNot(HaveOccurred())
			Expect(getResponse.GetObject().GetTitle()).To(Equal("GPU Operator"))
		})

		It("Update object", func() {
			createResponse, err := server.Create(ctx, privatev1.AddOnOperatorsCreateRequest_builder{
				Object: privatev1.AddOnOperator_builder{
					Metadata: privatev1.Metadata_builder{
						Name: fmt.Sprintf("test-%s", uuid.New()[24:32]),
					}.Build(),
					Title:       "GPU Operator",
					Description: "Original description.",
				}.Build(),
			}.Build())
			Expect(err).ToNot(HaveOccurred())

			updateResponse, err := server.Update(ctx, privatev1.AddOnOperatorsUpdateRequest_builder{
				Object: privatev1.AddOnOperator_builder{
					Id:    createResponse.GetObject().GetId(),
					Title: "Updated GPU Operator",
				}.Build(),
				UpdateMask: &fieldmaskpb.FieldMask{Paths: []string{"title"}},
			}.Build())
			Expect(err).ToNot(HaveOccurred())
			Expect(updateResponse.GetObject().GetTitle()).To(Equal("Updated GPU Operator"))
			Expect(updateResponse.GetObject().GetDescription()).To(Equal("Original description."))
		})

		It("Rejects update that creates inverted version range", func() {
			createResponse, err := server.Create(ctx, privatev1.AddOnOperatorsCreateRequest_builder{
				Object: privatev1.AddOnOperator_builder{
					Metadata: privatev1.Metadata_builder{
						Name: fmt.Sprintf("test-%s", uuid.New()[24:32]),
					}.Build(),
					Title:         "GPU Operator",
					MinOcpVersion: "4.14.0",
					MaxOcpVersion: "4.17.0",
				}.Build(),
			}.Build())
			Expect(err).ToNot(HaveOccurred())

			_, err = server.Update(ctx, privatev1.AddOnOperatorsUpdateRequest_builder{
				Object: privatev1.AddOnOperator_builder{
					Id:            createResponse.GetObject().GetId(),
					MinOcpVersion: "4.18.0",
				}.Build(),
				UpdateMask: &fieldmaskpb.FieldMask{Paths: []string{"min_ocp_version"}},
			}.Build())
			Expect(err).To(HaveOccurred())
			status, ok := grpcstatus.FromError(err)
			Expect(ok).To(BeTrue())
			Expect(status.Code()).To(Equal(grpccodes.InvalidArgument))
		})

		It("Delete object", func() {
			createResponse, err := server.Create(ctx, privatev1.AddOnOperatorsCreateRequest_builder{
				Object: privatev1.AddOnOperator_builder{
					Metadata: privatev1.Metadata_builder{
						Name: fmt.Sprintf("test-%s", uuid.New()[24:32]),
					}.Build(),
					Title: "GPU Operator",
				}.Build(),
			}.Build())
			Expect(err).ToNot(HaveOccurred())

			_, err = server.Delete(ctx, privatev1.AddOnOperatorsDeleteRequest_builder{
				Id: createResponse.GetObject().GetId(),
			}.Build())
			Expect(err).ToNot(HaveOccurred())
		})

		It("Signal object", func() {
			createResponse, err := server.Create(ctx, privatev1.AddOnOperatorsCreateRequest_builder{
				Object: privatev1.AddOnOperator_builder{
					Metadata: privatev1.Metadata_builder{
						Name: fmt.Sprintf("test-%s", uuid.New()[24:32]),
					}.Build(),
					Title: "GPU Operator",
				}.Build(),
			}.Build())
			Expect(err).ToNot(HaveOccurred())

			_, err = server.Signal(ctx, privatev1.AddOnOperatorsSignalRequest_builder{
				Id: createResponse.GetObject().GetId(),
			}.Build())
			Expect(err).ToNot(HaveOccurred())
		})
	})

	Describe("Default published", func() {
		It("Defaults to unpublished when defaultPublished is false", func() {
			server, err := NewPrivateAddOnOperatorsServer().
				SetLogger(logger).
				SetAttributionLogic(attribution).
				SetTenancyLogic(tenancy).
				SetDefaultPublished(false).
				Build()
			Expect(err).ToNot(HaveOccurred())

			response, err := server.Create(ctx, privatev1.AddOnOperatorsCreateRequest_builder{
				Object: privatev1.AddOnOperator_builder{
					Metadata: privatev1.Metadata_builder{
						Name: fmt.Sprintf("test-%s", uuid.New()[24:32]),
					}.Build(),
					Title: "GPU Operator",
				}.Build(),
			}.Build())
			Expect(err).ToNot(HaveOccurred())
			Expect(response.GetObject().GetPublished()).To(BeFalse())
		})

		It("Defaults to published when defaultPublished is true", func() {
			server, err := NewPrivateAddOnOperatorsServer().
				SetLogger(logger).
				SetAttributionLogic(attribution).
				SetTenancyLogic(tenancy).
				SetDefaultPublished(true).
				Build()
			Expect(err).ToNot(HaveOccurred())

			response, err := server.Create(ctx, privatev1.AddOnOperatorsCreateRequest_builder{
				Object: privatev1.AddOnOperator_builder{
					Metadata: privatev1.Metadata_builder{
						Name: fmt.Sprintf("test-%s", uuid.New()[24:32]),
					}.Build(),
					Title: "GPU Operator",
				}.Build(),
			}.Build())
			Expect(err).ToNot(HaveOccurred())
			Expect(response.GetObject().GetPublished()).To(BeTrue())
		})

		It("Explicit published=true is preserved when defaultPublished is false", func() {
			server, err := NewPrivateAddOnOperatorsServer().
				SetLogger(logger).
				SetAttributionLogic(attribution).
				SetTenancyLogic(tenancy).
				SetDefaultPublished(false).
				Build()
			Expect(err).ToNot(HaveOccurred())

			response, err := server.Create(ctx, privatev1.AddOnOperatorsCreateRequest_builder{
				Object: privatev1.AddOnOperator_builder{
					Metadata: privatev1.Metadata_builder{
						Name: fmt.Sprintf("test-%s", uuid.New()[24:32]),
					}.Build(),
					Title:     "GPU Operator",
					Published: true,
				}.Build(),
			}.Build())
			Expect(err).ToNot(HaveOccurred())
			Expect(response.GetObject().GetPublished()).To(BeTrue())
		})
	})
})

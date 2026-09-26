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
	"fmt"

	"github.com/google/uuid"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	grpccodes "google.golang.org/grpc/codes"
	grpcstatus "google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/fieldmaskpb"

	"github.com/osac-project/osac/fulfillment-service/internal/auth"
	"github.com/osac-project/osac/fulfillment-service/internal/database"
	"github.com/osac-project/osac/fulfillment-service/internal/database/dao"
	privatev1 "github.com/osac-project/osac/proto/gen/osac/private/v1"
)

var _ = Describe("Network classes server", func() {
	var server *PrivateNetworkClassesServer

	BeforeEach(func() {
		var err error
		server, err = NewPrivateNetworkClassesServer().
			SetLogger(logger).
			SetAttributionLogic(attribution).
			SetTenancyLogic(tenancy).
			Build()
		Expect(err).ToNot(HaveOccurred())
	})

	create := func() *privatev1.NetworkClass {
		response, err := server.Create(ctx, privatev1.NetworkClassesCreateRequest_builder{
			Object: privatev1.NetworkClass_builder{
				Metadata:      privatev1.Metadata_builder{Name: fmt.Sprintf("test-nc-%s", uuid.NewString()[:8])}.Build(),
				Title:         "Test Network Class",
				FabricManager: new("netris"),
			}.Build(),
		}.Build())
		Expect(err).ToNot(HaveOccurred())
		return response.GetObject()
	}

	createWithDefaults := func(defaults *privatev1.NetworkDefaults) *privatev1.NetworkClass {
		response, err := server.Create(ctx, privatev1.NetworkClassesCreateRequest_builder{
			Object: privatev1.NetworkClass_builder{
				Metadata:      privatev1.Metadata_builder{Name: fmt.Sprintf("test-nc-%s", uuid.NewString()[:8])}.Build(),
				Title:         "Network Class with defaults",
				FabricManager: new("netris"),
				Spec:          privatev1.NetworkClassSpec_builder{Defaults: defaults}.Build(),
			}.Build(),
		}.Build())
		Expect(err).ToNot(HaveOccurred())
		return response.GetObject()
	}

	validDefaults := func() *privatev1.NetworkDefaults {
		return privatev1.NetworkDefaults_builder{
			VirtualNetworkIpv4Cidr: "10.0.0.0/16",
			SubnetIpv4Cidr:         "10.0.1.0/24",
		}.Build()
	}

	Describe("singleton admission", func() {
		It("accepts the first NetworkClass and leaves readiness to the controller", func() {
			response, err := server.Create(ctx, privatev1.NetworkClassesCreateRequest_builder{
				Object: privatev1.NetworkClass_builder{
					Title:         "Provider network",
					FabricManager: new("netris"),
					Status: privatev1.NetworkClassStatus_builder{
						Hub:     "caller-hub",
						State:   privatev1.NetworkClassState_NETWORK_CLASS_STATE_FAILED,
						Message: new("caller status"),
					}.Build(),
				}.Build(),
			}.Build())
			Expect(err).ToNot(HaveOccurred())
			Expect(response.GetObject().GetStatus()).To(BeNil())
		})

		It("rejects a second active NetworkClass", func() {
			first := create()
			Expect(first.GetId()).ToNot(BeEmpty())

			_, err := server.Create(ctx, privatev1.NetworkClassesCreateRequest_builder{
				Object: privatev1.NetworkClass_builder{
					Title:         "Second network",
					FabricManager: new("neutron"),
				}.Build(),
			}.Build())
			Expect(grpcstatus.Code(err)).To(Equal(grpccodes.FailedPrecondition))
			Expect(err).To(MatchError(ContainSubstring("only one NetworkClass per deployment")))
		})

		It("allows a replacement after the existing NetworkClass is soft-deleted", func() {
			first := create()
			tx, err := database.TxFromContext(ctx)
			Expect(err).ToNot(HaveOccurred())
			_, err = tx.Exec(ctx, `update network_classes set deletion_timestamp = now() where id = $1`, first.GetId())
			Expect(err).ToNot(HaveOccurred())

			second := create()
			Expect(second.GetId()).ToNot(Equal(first.GetId()))
		})

		It("is also enforced by the database unique singleton index", func() {
			networkClassDAO, err := dao.NewGenericDAO[*privatev1.NetworkClass]().
				SetLogger(logger).
				SetTenancyLogic(tenancy).
				Build()
			Expect(err).ToNot(HaveOccurred())
			build := func(name string) *privatev1.NetworkClass {
				return privatev1.NetworkClass_builder{
					Title:         "Provider network",
					FabricManager: new("netris"),
					Metadata: privatev1.Metadata_builder{
						Name:   name,
						Tenant: auth.SharedTenant,
					}.Build(),
				}.Build()
			}
			_, err = networkClassDAO.Create().SetObject(build("first")).Do(ctx)
			Expect(err).ToNot(HaveOccurred())
			_, err = networkClassDAO.Create().SetObject(build("second")).Do(ctx)
			Expect(err).To(HaveOccurred())
		})
	})

	Describe("CRUD and status ownership", func() {
		It("lists, gets, updates, and deletes the singleton", func() {
			created := create()
			listResponse, err := server.List(ctx, privatev1.NetworkClassesListRequest_builder{}.Build())
			Expect(err).ToNot(HaveOccurred())
			Expect(listResponse.GetItems()).To(HaveLen(1))

			name := created.GetMetadata().GetName()
			updated, err := server.Update(ctx, privatev1.NetworkClassesUpdateRequest_builder{
				Object: privatev1.NetworkClass_builder{
					Id:       created.GetId(),
					Metadata: privatev1.Metadata_builder{Name: name}.Build(),
					Title:    "Updated provider network",
				}.Build(),
				UpdateMask: &fieldmaskpb.FieldMask{Paths: []string{"title"}},
			}.Build())
			Expect(err).ToNot(HaveOccurred())
			Expect(updated.GetObject().GetTitle()).To(Equal("Updated provider network"))

			_, err = server.Delete(ctx, privatev1.NetworkClassesDeleteRequest_builder{Id: created.GetId()}.Build())
			Expect(err).ToNot(HaveOccurred())
		})

		It("derives a DNS name from the configured manager", func() {
			response, err := server.Create(ctx, privatev1.NetworkClassesCreateRequest_builder{
				Object: privatev1.NetworkClass_builder{
					Title:      "Kubernetes-only network",
					K8SManager: new("cudn_localnet"),
				}.Build(),
			}.Build())
			Expect(err).ToNot(HaveOccurred())
			Expect(response.GetObject().GetMetadata().GetName()).To(Equal("cudn-localnet"))
		})

		It("preserves a caller-provided metadata name", func() {
			response, err := server.Create(ctx, privatev1.NetworkClassesCreateRequest_builder{
				Object: privatev1.NetworkClass_builder{
					Metadata:      privatev1.Metadata_builder{Name: "provider-network"}.Build(),
					Title:         "Provider network",
					FabricManager: new("netris"),
				}.Build(),
			}.Build())
			Expect(err).ToNot(HaveOccurred())
			Expect(response.GetObject().GetMetadata().GetName()).To(Equal("provider-network"))
		})
	})

	Describe("manager validation", func() {
		It("requires at least one manager", func() {
			_, err := server.Create(ctx, privatev1.NetworkClassesCreateRequest_builder{
				Object: privatev1.NetworkClass_builder{Title: "No manager"}.Build(),
			}.Build())
			Expect(grpcstatus.Code(err)).To(Equal(grpccodes.InvalidArgument))
			Expect(err).To(MatchError(ContainSubstring("fabric_manager")))
		})

		It("allows a Kubernetes-only manager", func() {
			response, err := server.Create(ctx, privatev1.NetworkClassesCreateRequest_builder{
				Object: privatev1.NetworkClass_builder{
					Title:      "K8s-only",
					K8SManager: new("cudn"),
				}.Build(),
			}.Build())
			Expect(err).ToNot(HaveOccurred())
			Expect(response.GetObject().GetK8SManager()).To(Equal("cudn"))
		})

		It("keeps manager configuration immutable once set", func() {
			created := create()
			_, err := server.Update(ctx, privatev1.NetworkClassesUpdateRequest_builder{
				Object:     privatev1.NetworkClass_builder{Id: created.GetId(), FabricManager: new("neutron")}.Build(),
				UpdateMask: &fieldmaskpb.FieldMask{Paths: []string{"fabric_manager"}},
			}.Build())
			Expect(grpcstatus.Code(err)).To(Equal(grpccodes.InvalidArgument))
			Expect(err).To(MatchError(ContainSubstring("immutable")))
		})

	})

	Describe("defaults configuration", func() {
		It("validates and persists defaults without making them a second selection mechanism", func() {
			created := createWithDefaults(validDefaults())
			Expect(created.GetSpec().GetDefaults().GetVirtualNetworkIpv4Cidr()).To(Equal("10.0.0.0/16"))
		})

		It("rejects a subnet CIDR without its parent virtual-network CIDR", func() {
			_, err := server.Create(ctx, privatev1.NetworkClassesCreateRequest_builder{
				Object: privatev1.NetworkClass_builder{
					Title:         "Invalid defaults",
					FabricManager: new("netris"),
					Spec: privatev1.NetworkClassSpec_builder{Defaults: privatev1.NetworkDefaults_builder{
						SubnetIpv4Cidr: "10.0.1.0/24",
					}.Build()}.Build(),
				}.Build(),
			}.Build())
			Expect(grpcstatus.Code(err)).To(Equal(grpccodes.InvalidArgument))
		})
	})
})

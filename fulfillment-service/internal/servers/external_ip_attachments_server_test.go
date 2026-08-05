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

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/fieldmaskpb"

	privatev1 "github.com/osac-project/fulfillment-service/internal/api/osac/private/v1"
	publicv1 "github.com/osac-project/fulfillment-service/internal/api/osac/public/v1"
	"github.com/osac-project/fulfillment-service/internal/auth"
	"github.com/osac-project/fulfillment-service/internal/database/dao"
)

var _ = Describe("External IP attachments server", func() {
	Describe("Creation", func() {
		It("Can be built if all the required parameters are set", func() {
			server, err := NewExternalIPAttachmentsServer().
				SetLogger(logger).
				SetAttributionLogic(attribution).
				SetTenancyLogic(tenancy).
				Build()
			Expect(err).ToNot(HaveOccurred())
			Expect(server).ToNot(BeNil())
		})

		It("Fails if logger is not set", func() {
			server, err := NewExternalIPAttachmentsServer().
				SetAttributionLogic(attribution).
				SetTenancyLogic(tenancy).
				Build()
			Expect(err).To(MatchError("logger is mandatory"))
			Expect(server).To(BeNil())
		})

		It("Fails if tenancy logic is not set", func() {
			server, err := NewExternalIPAttachmentsServer().
				SetLogger(logger).
				SetAttributionLogic(attribution).
				Build()
			Expect(err).To(MatchError("tenancy logic is mandatory"))
			Expect(server).To(BeNil())
		})

		It("Fails if attribution logic is not set", func() {
			server, err := NewExternalIPAttachmentsServer().
				SetLogger(logger).
				SetTenancyLogic(tenancy).
				Build()
			Expect(err).To(MatchError("attribution logic is mandatory"))
			Expect(server).To(BeNil())
		})
	})

	Describe("Behaviour", func() {
		var (
			externalIPAttachmentsServer *ExternalIPAttachmentsServer
			externalIPDao               *dao.GenericDAO[*privatev1.ExternalIP]
			computeInstanceDao          *dao.GenericDAO[*privatev1.ComputeInstance]
			clusterDao                  *dao.GenericDAO[*privatev1.Cluster]
			bareMetalInstanceDao        *dao.GenericDAO[*privatev1.BareMetalInstance]
			sharedPoolID                string
		)

		BeforeEach(func() {
			var err error

			externalIPDao, err = dao.NewGenericDAO[*privatev1.ExternalIP]().
				SetLogger(logger).
				SetTenancyLogic(tenancy).
				Build()
			Expect(err).ToNot(HaveOccurred())

			computeInstanceDao, err = dao.NewGenericDAO[*privatev1.ComputeInstance]().
				SetLogger(logger).
				SetTenancyLogic(tenancy).
				Build()
			Expect(err).ToNot(HaveOccurred())

			clusterDao, err = dao.NewGenericDAO[*privatev1.Cluster]().
				SetLogger(logger).
				SetTenancyLogic(tenancy).
				Build()
			Expect(err).ToNot(HaveOccurred())

			bareMetalInstanceDao, err = dao.NewGenericDAO[*privatev1.BareMetalInstance]().
				SetLogger(logger).
				SetTenancyLogic(tenancy).
				Build()
			Expect(err).ToNot(HaveOccurred())

			externalIPPoolDao, err := dao.NewGenericDAO[*privatev1.ExternalIPPool]().
				SetLogger(logger).
				SetTenancyLogic(tenancy).
				Build()
			Expect(err).ToNot(HaveOccurred())

			poolResp, err := externalIPPoolDao.Create().SetObject(
				privatev1.ExternalIPPool_builder{
					Metadata: privatev1.Metadata_builder{
						Tenant: auth.SharedTenant,
					}.Build(),
					Spec: privatev1.ExternalIPPoolSpec_builder{
						Cidrs: []string{"203.0.113.0/24"},
					}.Build(),
					Status: privatev1.ExternalIPPoolStatus_builder{
						State:     privatev1.ExternalIPPoolState_EXTERNAL_IP_POOL_STATE_READY,
						Total:     100,
						Allocated: 0,
						Available: 100,
					}.Build(),
				}.Build(),
			).Do(ctx)
			Expect(err).ToNot(HaveOccurred())
			sharedPoolID = poolResp.GetObject().GetId()

			externalIPAttachmentsServer, err = NewExternalIPAttachmentsServer().
				SetLogger(logger).
				SetAttributionLogic(attribution).
				SetTenancyLogic(tenancy).
				Build()
			Expect(err).ToNot(HaveOccurred())
		})

		createAttachment := func() *publicv1.ExternalIPAttachment {
			eip := createExternalIPInState(ctx, externalIPDao, sharedPoolID,
				privatev1.ExternalIPState_EXTERNAL_IP_STATE_ALLOCATED, false)
			ci := createComputeInstanceInState(ctx, computeInstanceDao,
				privatev1.ComputeInstanceState_COMPUTE_INSTANCE_STATE_RUNNING)

			response, err := externalIPAttachmentsServer.Create(ctx,
				publicv1.ExternalIPAttachmentsCreateRequest_builder{
					Object: publicv1.ExternalIPAttachment_builder{
						Spec: publicv1.ExternalIPAttachmentSpec_builder{
							ExternalIp:      eip.GetId(),
							ComputeInstance: new(ci.GetId()),
						}.Build(),
					}.Build(),
				}.Build())
			Expect(err).ToNot(HaveOccurred())
			Expect(response).ToNot(BeNil())
			return response.GetObject()
		}

		It("Creates object with ComputeInstance target", func() {
			object := createAttachment()
			Expect(object).ToNot(BeNil())
			Expect(object.GetId()).ToNot(BeEmpty())
			Expect(object.GetSpec().GetExternalIp()).ToNot(BeEmpty())
			Expect(object.GetSpec().GetComputeInstance()).ToNot(BeEmpty())
		})

		It("Creates and gets object with Cluster target", func() {
			eip := createExternalIPInState(ctx, externalIPDao, sharedPoolID,
				privatev1.ExternalIPState_EXTERNAL_IP_STATE_ALLOCATED, false)
			cluster := createClusterInState(ctx, clusterDao)

			createResponse, err := externalIPAttachmentsServer.Create(ctx,
				publicv1.ExternalIPAttachmentsCreateRequest_builder{
					Object: publicv1.ExternalIPAttachment_builder{
						Spec: publicv1.ExternalIPAttachmentSpec_builder{
							ExternalIp:     eip.GetId(),
							Cluster:        new(cluster.GetId()),
							TargetEndpoint: publicv1.ExternalIPAttachmentEndpoint_EXTERNAL_IP_ATTACHMENT_ENDPOINT_API,
						}.Build(),
					}.Build(),
				}.Build())
			Expect(err).ToNot(HaveOccurred())
			Expect(createResponse.GetObject().GetSpec().GetCluster()).To(Equal(cluster.GetId()))
			Expect(createResponse.GetObject().GetSpec().GetTargetEndpoint()).To(
				Equal(publicv1.ExternalIPAttachmentEndpoint_EXTERNAL_IP_ATTACHMENT_ENDPOINT_API))

			getResponse, err := externalIPAttachmentsServer.Get(ctx,
				publicv1.ExternalIPAttachmentsGetRequest_builder{
					Id: createResponse.GetObject().GetId(),
				}.Build())
			Expect(err).ToNot(HaveOccurred())
			Expect(getResponse.GetObject().GetSpec().GetCluster()).To(Equal(cluster.GetId()))
			Expect(getResponse.GetObject().GetSpec().GetTargetEndpoint()).To(
				Equal(publicv1.ExternalIPAttachmentEndpoint_EXTERNAL_IP_ATTACHMENT_ENDPOINT_API))
		})

		It("Creates and gets object with BareMetalInstance target", func() {
			eip := createExternalIPInState(ctx, externalIPDao, sharedPoolID,
				privatev1.ExternalIPState_EXTERNAL_IP_STATE_ALLOCATED, false)
			bmi := createBareMetalInstanceInState(ctx, bareMetalInstanceDao)

			createResponse, err := externalIPAttachmentsServer.Create(ctx,
				publicv1.ExternalIPAttachmentsCreateRequest_builder{
					Object: publicv1.ExternalIPAttachment_builder{
						Spec: publicv1.ExternalIPAttachmentSpec_builder{
							ExternalIp:        eip.GetId(),
							BaremetalInstance: new(bmi.GetId()),
						}.Build(),
					}.Build(),
				}.Build())
			Expect(err).ToNot(HaveOccurred())
			Expect(createResponse.GetObject().GetSpec().GetBaremetalInstance()).To(Equal(bmi.GetId()))

			getResponse, err := externalIPAttachmentsServer.Get(ctx,
				publicv1.ExternalIPAttachmentsGetRequest_builder{
					Id: createResponse.GetObject().GetId(),
				}.Build())
			Expect(err).ToNot(HaveOccurred())
			Expect(getResponse.GetObject().GetSpec().GetBaremetalInstance()).To(Equal(bmi.GetId()))
		})

		It("List objects", func() {
			const count = 10
			for range count {
				createAttachment()
			}

			response, err := externalIPAttachmentsServer.List(ctx,
				publicv1.ExternalIPAttachmentsListRequest_builder{}.Build())
			Expect(err).ToNot(HaveOccurred())
			Expect(response).ToNot(BeNil())
			items := response.GetItems()
			Expect(items).To(HaveLen(count))
		})

		It("List objects with limit", func() {
			const count = 10
			for range count {
				createAttachment()
			}

			response, err := externalIPAttachmentsServer.List(ctx,
				publicv1.ExternalIPAttachmentsListRequest_builder{
					Limit: new(int32(1)),
				}.Build())
			Expect(err).ToNot(HaveOccurred())
			Expect(response.GetSize()).To(BeNumerically("==", 1))
		})

		It("List objects with offset", func() {
			const count = 10
			for range count {
				createAttachment()
			}

			response, err := externalIPAttachmentsServer.List(ctx,
				publicv1.ExternalIPAttachmentsListRequest_builder{
					Offset: new(int32(1)),
				}.Build())
			Expect(err).ToNot(HaveOccurred())
			Expect(response.GetSize()).To(BeNumerically("==", count-1))
		})

		It("List objects with filter", func() {
			const count = 10
			var objects []*publicv1.ExternalIPAttachment
			for range count {
				objects = append(objects, createAttachment())
			}

			for _, object := range objects {
				response, err := externalIPAttachmentsServer.List(ctx,
					publicv1.ExternalIPAttachmentsListRequest_builder{
						Filter: new(fmt.Sprintf("this.id == '%s'", object.GetId())),
					}.Build())
				Expect(err).ToNot(HaveOccurred())
				Expect(response.GetSize()).To(BeNumerically("==", 1))
				Expect(response.GetItems()[0].GetId()).To(Equal(object.GetId()))
			}
		})

		It("Get object", func() {
			created := createAttachment()

			getResponse, err := externalIPAttachmentsServer.Get(ctx,
				publicv1.ExternalIPAttachmentsGetRequest_builder{
					Id: created.GetId(),
				}.Build())
			Expect(err).ToNot(HaveOccurred())
			Expect(proto.Equal(created, getResponse.GetObject())).To(BeTrue())
		})

		It("Updates labels on an external IP attachment", func() {
			created := createAttachment()

			updateResponse, err := externalIPAttachmentsServer.Update(ctx,
				publicv1.ExternalIPAttachmentsUpdateRequest_builder{
					Object: publicv1.ExternalIPAttachment_builder{
						Id: created.GetId(),
						Metadata: publicv1.Metadata_builder{
							Labels: map[string]string{
								"env": "test",
							},
						}.Build(),
					}.Build(),
					UpdateMask: &fieldmaskpb.FieldMask{
						Paths: []string{"metadata.labels"},
					},
				}.Build())
			Expect(err).ToNot(HaveOccurred())
			Expect(updateResponse.GetObject().GetMetadata().GetLabels()).To(
				HaveKeyWithValue("env", "test"),
			)

			getResponse, err := externalIPAttachmentsServer.Get(ctx,
				publicv1.ExternalIPAttachmentsGetRequest_builder{
					Id: created.GetId(),
				}.Build())
			Expect(err).ToNot(HaveOccurred())
			Expect(getResponse.GetObject().GetMetadata().GetLabels()).To(
				HaveKeyWithValue("env", "test"),
			)
		})

		It("Rejects update with nil object", func() {
			_, err := externalIPAttachmentsServer.Update(ctx,
				publicv1.ExternalIPAttachmentsUpdateRequest_builder{}.Build())
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("object is mandatory"))
		})

		It("Propagates immutable field rejection from private server", func() {
			created := createAttachment()

			_, err := externalIPAttachmentsServer.Update(ctx,
				publicv1.ExternalIPAttachmentsUpdateRequest_builder{
					Object: publicv1.ExternalIPAttachment_builder{
						Id: created.GetId(),
						Spec: publicv1.ExternalIPAttachmentSpec_builder{
							ExternalIp: "different-ip-id",
						}.Build(),
					}.Build(),
				}.Build())
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("immutable"))
		})

		It("Delete object", func() {
			created := createAttachment()

			_, err := externalIPAttachmentsServer.Delete(ctx,
				publicv1.ExternalIPAttachmentsDeleteRequest_builder{
					Id: created.GetId(),
				}.Build())
			Expect(err).ToNot(HaveOccurred())
		})
	})
})

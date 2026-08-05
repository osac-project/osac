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
	grpccodes "google.golang.org/grpc/codes"
	grpcstatus "google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/fieldmaskpb"

	privatev1 "github.com/osac-project/fulfillment-service/internal/api/osac/private/v1"
	"github.com/osac-project/fulfillment-service/internal/auth"
	"github.com/osac-project/fulfillment-service/internal/database/dao"
)

func createExternalIPInState(
	ctx context.Context,
	externalIPDao *dao.GenericDAO[*privatev1.ExternalIP],
	poolID string,
	state privatev1.ExternalIPState,
	attached bool,
) *privatev1.ExternalIP {
	resp, err := externalIPDao.Create().SetObject(
		privatev1.ExternalIP_builder{
			Metadata: privatev1.Metadata_builder{
				Tenant: auth.SharedTenant,
			}.Build(),
			Spec: privatev1.ExternalIPSpec_builder{
				Pool: poolID,
			}.Build(),
			Status: privatev1.ExternalIPStatus_builder{
				State:    state,
				Address:  "203.0.113.1",
				Attached: attached,
			}.Build(),
		}.Build(),
	).Do(ctx)
	ExpectWithOffset(1, err).ToNot(HaveOccurred())
	return resp.GetObject()
}

func createClusterInState(
	ctx context.Context,
	clusterDao *dao.GenericDAO[*privatev1.Cluster],
) *privatev1.Cluster {
	resp, err := clusterDao.Create().SetObject(
		privatev1.Cluster_builder{
			Metadata: privatev1.Metadata_builder{
				Tenant: auth.SharedTenant,
			}.Build(),
			Spec: privatev1.ClusterSpec_builder{
				Template: "ocp_small",
			}.Build(),
		}.Build(),
	).Do(ctx)
	ExpectWithOffset(1, err).ToNot(HaveOccurred())
	return resp.GetObject()
}

func createBareMetalInstanceInState(
	ctx context.Context,
	bareMetalInstanceDao *dao.GenericDAO[*privatev1.BareMetalInstance],
) *privatev1.BareMetalInstance {
	resp, err := bareMetalInstanceDao.Create().SetObject(
		privatev1.BareMetalInstance_builder{
			Metadata: privatev1.Metadata_builder{
				Tenant: auth.SharedTenant,
			}.Build(),
			Spec: privatev1.BareMetalInstanceSpec_builder{
				CatalogItem: "bcm_h100",
			}.Build(),
		}.Build(),
	).Do(ctx)
	ExpectWithOffset(1, err).ToNot(HaveOccurred())
	return resp.GetObject()
}

var _ = Describe("Private external IP attachments server", func() {
	var (
		server               *PrivateExternalIPAttachmentsServer
		externalIPPoolDao    *dao.GenericDAO[*privatev1.ExternalIPPool]
		externalIPDao        *dao.GenericDAO[*privatev1.ExternalIP]
		computeInstanceDao   *dao.GenericDAO[*privatev1.ComputeInstance]
		clusterDao           *dao.GenericDAO[*privatev1.Cluster]
		bareMetalInstanceDao *dao.GenericDAO[*privatev1.BareMetalInstance]
		sharedPool           *privatev1.ExternalIPPool
	)

	BeforeEach(func() {
		var err error

		externalIPPoolDao, err = dao.NewGenericDAO[*privatev1.ExternalIPPool]().
			SetLogger(logger).
			SetTenancyLogic(tenancy).
			Build()
		Expect(err).ToNot(HaveOccurred())

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
		sharedPool = poolResp.GetObject()

		server, err = NewPrivateExternalIPAttachmentsServer().
			SetLogger(logger).
			SetAttributionLogic(attribution).
			SetTenancyLogic(tenancy).
			Build()
		Expect(err).ToNot(HaveOccurred())
	})

	Describe("Creation", func() {
		It("Can be built if all required parameters are set", func() {
			s, err := NewPrivateExternalIPAttachmentsServer().
				SetLogger(logger).
				SetAttributionLogic(attribution).
				SetTenancyLogic(tenancy).
				Build()
			Expect(err).ToNot(HaveOccurred())
			Expect(s).ToNot(BeNil())
		})

		It("Fails if logger is not set", func() {
			s, err := NewPrivateExternalIPAttachmentsServer().
				SetAttributionLogic(attribution).
				SetTenancyLogic(tenancy).
				Build()
			Expect(err).To(MatchError("logger is mandatory"))
			Expect(s).To(BeNil())
		})

		It("Fails if tenancy logic is not set", func() {
			s, err := NewPrivateExternalIPAttachmentsServer().
				SetLogger(logger).
				SetAttributionLogic(attribution).
				Build()
			Expect(err).To(MatchError("tenancy logic is mandatory"))
			Expect(s).To(BeNil())
		})
	})

	Describe("Behaviour", func() {
		It("Creates an attachment with a ComputeInstance target", func() {
			eip := createExternalIPInState(ctx, externalIPDao, sharedPool.GetId(),
				privatev1.ExternalIPState_EXTERNAL_IP_STATE_ALLOCATED, false)
			ci := createComputeInstanceInState(ctx, computeInstanceDao,
				privatev1.ComputeInstanceState_COMPUTE_INSTANCE_STATE_RUNNING)

			response, err := server.Create(ctx, privatev1.ExternalIPAttachmentsCreateRequest_builder{
				Object: privatev1.ExternalIPAttachment_builder{
					Metadata: privatev1.Metadata_builder{
						Name: "ci-attachment",
					}.Build(),
					Spec: privatev1.ExternalIPAttachmentSpec_builder{
						ExternalIp:      eip.GetId(),
						ComputeInstance: new(ci.GetId()),
					}.Build(),
				}.Build(),
			}.Build())
			Expect(err).ToNot(HaveOccurred())
			Expect(response.GetObject().GetId()).ToNot(BeEmpty())
			Expect(response.GetObject().GetSpec().GetExternalIp()).To(Equal(eip.GetId()))
			Expect(response.GetObject().GetSpec().GetComputeInstance()).To(Equal(ci.GetId()))
			Expect(response.GetObject().GetStatus().GetState()).To(
				Equal(privatev1.ExternalIPAttachmentState_EXTERNAL_IP_ATTACHMENT_STATE_PENDING))
		})

		It("Creates an attachment with a Cluster target and API endpoint", func() {
			eip := createExternalIPInState(ctx, externalIPDao, sharedPool.GetId(),
				privatev1.ExternalIPState_EXTERNAL_IP_STATE_ALLOCATED, false)
			cluster := createClusterInState(ctx, clusterDao)

			response, err := server.Create(ctx, privatev1.ExternalIPAttachmentsCreateRequest_builder{
				Object: privatev1.ExternalIPAttachment_builder{
					Metadata: privatev1.Metadata_builder{
						Name: "cluster-api-attachment",
					}.Build(),
					Spec: privatev1.ExternalIPAttachmentSpec_builder{
						ExternalIp:     eip.GetId(),
						Cluster:        new(cluster.GetId()),
						TargetEndpoint: privatev1.ExternalIPAttachmentEndpoint_EXTERNAL_IP_ATTACHMENT_ENDPOINT_API,
					}.Build(),
				}.Build(),
			}.Build())
			Expect(err).ToNot(HaveOccurred())
			Expect(response.GetObject().GetSpec().GetCluster()).To(Equal(cluster.GetId()))
			Expect(response.GetObject().GetSpec().GetTargetEndpoint()).To(
				Equal(privatev1.ExternalIPAttachmentEndpoint_EXTERNAL_IP_ATTACHMENT_ENDPOINT_API))
		})

		It("Creates an attachment with a Cluster target and ingress endpoint", func() {
			eip := createExternalIPInState(ctx, externalIPDao, sharedPool.GetId(),
				privatev1.ExternalIPState_EXTERNAL_IP_STATE_ALLOCATED, false)
			cluster := createClusterInState(ctx, clusterDao)

			response, err := server.Create(ctx, privatev1.ExternalIPAttachmentsCreateRequest_builder{
				Object: privatev1.ExternalIPAttachment_builder{
					Spec: privatev1.ExternalIPAttachmentSpec_builder{
						ExternalIp:     eip.GetId(),
						Cluster:        new(cluster.GetId()),
						TargetEndpoint: privatev1.ExternalIPAttachmentEndpoint_EXTERNAL_IP_ATTACHMENT_ENDPOINT_INGRESS,
					}.Build(),
				}.Build(),
			}.Build())
			Expect(err).ToNot(HaveOccurred())
			Expect(response.GetObject().GetSpec().GetTargetEndpoint()).To(
				Equal(privatev1.ExternalIPAttachmentEndpoint_EXTERNAL_IP_ATTACHMENT_ENDPOINT_INGRESS))
		})

		It("Creates an attachment with a BareMetalInstance target", func() {
			eip := createExternalIPInState(ctx, externalIPDao, sharedPool.GetId(),
				privatev1.ExternalIPState_EXTERNAL_IP_STATE_ALLOCATED, false)
			bmi := createBareMetalInstanceInState(ctx, bareMetalInstanceDao)

			response, err := server.Create(ctx, privatev1.ExternalIPAttachmentsCreateRequest_builder{
				Object: privatev1.ExternalIPAttachment_builder{
					Spec: privatev1.ExternalIPAttachmentSpec_builder{
						ExternalIp:        eip.GetId(),
						BaremetalInstance: new(bmi.GetId()),
					}.Build(),
				}.Build(),
			}.Build())
			Expect(err).ToNot(HaveOccurred())
			Expect(response.GetObject().GetSpec().GetBaremetalInstance()).To(Equal(bmi.GetId()))
		})

		It("Lists external IP attachments", func() {
			eip1 := createExternalIPInState(ctx, externalIPDao, sharedPool.GetId(),
				privatev1.ExternalIPState_EXTERNAL_IP_STATE_ALLOCATED, false)
			ci1 := createComputeInstanceInState(ctx, computeInstanceDao,
				privatev1.ComputeInstanceState_COMPUTE_INSTANCE_STATE_RUNNING)
			eip2 := createExternalIPInState(ctx, externalIPDao, sharedPool.GetId(),
				privatev1.ExternalIPState_EXTERNAL_IP_STATE_ALLOCATED, false)
			ci2 := createComputeInstanceInState(ctx, computeInstanceDao,
				privatev1.ComputeInstanceState_COMPUTE_INSTANCE_STATE_RUNNING)

			_, err := server.Create(ctx, privatev1.ExternalIPAttachmentsCreateRequest_builder{
				Object: privatev1.ExternalIPAttachment_builder{
					Spec: privatev1.ExternalIPAttachmentSpec_builder{
						ExternalIp:      eip1.GetId(),
						ComputeInstance: new(ci1.GetId()),
					}.Build(),
				}.Build(),
			}.Build())
			Expect(err).ToNot(HaveOccurred())

			_, err = server.Create(ctx, privatev1.ExternalIPAttachmentsCreateRequest_builder{
				Object: privatev1.ExternalIPAttachment_builder{
					Spec: privatev1.ExternalIPAttachmentSpec_builder{
						ExternalIp:      eip2.GetId(),
						ComputeInstance: new(ci2.GetId()),
					}.Build(),
				}.Build(),
			}.Build())
			Expect(err).ToNot(HaveOccurred())

			listResponse, err := server.List(ctx, privatev1.ExternalIPAttachmentsListRequest_builder{}.Build())
			Expect(err).ToNot(HaveOccurred())
			Expect(listResponse.GetSize()).To(Equal(int32(2)))
			Expect(listResponse.GetItems()).To(HaveLen(2))
		})

		It("Gets an external IP attachment", func() {
			eip := createExternalIPInState(ctx, externalIPDao, sharedPool.GetId(),
				privatev1.ExternalIPState_EXTERNAL_IP_STATE_ALLOCATED, false)
			ci := createComputeInstanceInState(ctx, computeInstanceDao,
				privatev1.ComputeInstanceState_COMPUTE_INSTANCE_STATE_RUNNING)

			createResponse, err := server.Create(ctx, privatev1.ExternalIPAttachmentsCreateRequest_builder{
				Object: privatev1.ExternalIPAttachment_builder{
					Metadata: privatev1.Metadata_builder{
						Name: "my-attachment",
					}.Build(),
					Spec: privatev1.ExternalIPAttachmentSpec_builder{
						ExternalIp:      eip.GetId(),
						ComputeInstance: new(ci.GetId()),
					}.Build(),
				}.Build(),
			}.Build())
			Expect(err).ToNot(HaveOccurred())

			getResponse, err := server.Get(ctx, privatev1.ExternalIPAttachmentsGetRequest_builder{
				Id: createResponse.GetObject().GetId(),
			}.Build())
			Expect(err).ToNot(HaveOccurred())
			Expect(getResponse.GetObject().GetId()).To(Equal(createResponse.GetObject().GetId()))
			Expect(getResponse.GetObject().GetSpec().GetExternalIp()).To(Equal(eip.GetId()))
			Expect(getResponse.GetObject().GetSpec().GetComputeInstance()).To(Equal(ci.GetId()))
		})

		It("Updates metadata of an external IP attachment", func() {
			eip := createExternalIPInState(ctx, externalIPDao, sharedPool.GetId(),
				privatev1.ExternalIPState_EXTERNAL_IP_STATE_ALLOCATED, false)
			ci := createComputeInstanceInState(ctx, computeInstanceDao,
				privatev1.ComputeInstanceState_COMPUTE_INSTANCE_STATE_RUNNING)

			createResponse, err := server.Create(ctx, privatev1.ExternalIPAttachmentsCreateRequest_builder{
				Object: privatev1.ExternalIPAttachment_builder{
					Metadata: privatev1.Metadata_builder{
						Name: "my-attachment",
					}.Build(),
					Spec: privatev1.ExternalIPAttachmentSpec_builder{
						ExternalIp:      eip.GetId(),
						ComputeInstance: new(ci.GetId()),
					}.Build(),
				}.Build(),
			}.Build())
			Expect(err).ToNot(HaveOccurred())

			updateResponse, err := server.Update(ctx, privatev1.ExternalIPAttachmentsUpdateRequest_builder{
				Object: privatev1.ExternalIPAttachment_builder{
					Id: createResponse.GetObject().GetId(),
					Metadata: privatev1.Metadata_builder{
						Name: "renamed-attachment",
					}.Build(),
				}.Build(),
				UpdateMask: &fieldmaskpb.FieldMask{
					Paths: []string{"metadata.name"},
				},
			}.Build())
			Expect(err).ToNot(HaveOccurred())
			Expect(updateResponse.GetObject().GetMetadata().GetName()).To(Equal("renamed-attachment"))
		})

		It("Deletes an external IP attachment", func() {
			eip := createExternalIPInState(ctx, externalIPDao, sharedPool.GetId(),
				privatev1.ExternalIPState_EXTERNAL_IP_STATE_ALLOCATED, false)
			ci := createComputeInstanceInState(ctx, computeInstanceDao,
				privatev1.ComputeInstanceState_COMPUTE_INSTANCE_STATE_RUNNING)

			createResponse, err := server.Create(ctx, privatev1.ExternalIPAttachmentsCreateRequest_builder{
				Object: privatev1.ExternalIPAttachment_builder{
					Spec: privatev1.ExternalIPAttachmentSpec_builder{
						ExternalIp:      eip.GetId(),
						ComputeInstance: new(ci.GetId()),
					}.Build(),
				}.Build(),
			}.Build())
			Expect(err).ToNot(HaveOccurred())

			_, err = server.Delete(ctx, privatev1.ExternalIPAttachmentsDeleteRequest_builder{
				Id: createResponse.GetObject().GetId(),
			}.Build())
			Expect(err).ToNot(HaveOccurred())
		})

		It("Signals an external IP attachment", func() {
			eip := createExternalIPInState(ctx, externalIPDao, sharedPool.GetId(),
				privatev1.ExternalIPState_EXTERNAL_IP_STATE_ALLOCATED, false)
			ci := createComputeInstanceInState(ctx, computeInstanceDao,
				privatev1.ComputeInstanceState_COMPUTE_INSTANCE_STATE_RUNNING)

			createResponse, err := server.Create(ctx, privatev1.ExternalIPAttachmentsCreateRequest_builder{
				Object: privatev1.ExternalIPAttachment_builder{
					Spec: privatev1.ExternalIPAttachmentSpec_builder{
						ExternalIp:      eip.GetId(),
						ComputeInstance: new(ci.GetId()),
					}.Build(),
				}.Build(),
			}.Build())
			Expect(err).ToNot(HaveOccurred())

			_, err = server.Signal(ctx, privatev1.ExternalIPAttachmentsSignalRequest_builder{
				Id: createResponse.GetObject().GetId(),
			}.Build())
			Expect(err).ToNot(HaveOccurred())
		})
	})

	Describe("Validation", func() {
		It("Rejects Create with nil object", func() {
			_, err := server.Create(ctx, privatev1.ExternalIPAttachmentsCreateRequest_builder{}.Build())
			Expect(err).To(HaveOccurred())
			status, ok := grpcstatus.FromError(err)
			Expect(ok).To(BeTrue())
			Expect(status.Code()).To(Equal(grpccodes.InvalidArgument))
			Expect(err.Error()).To(ContainSubstring("is mandatory"))
		})

		It("Rejects Create with nil spec", func() {
			_, err := server.Create(ctx, privatev1.ExternalIPAttachmentsCreateRequest_builder{
				Object: privatev1.ExternalIPAttachment_builder{
					Metadata: privatev1.Metadata_builder{
						Name: "my-attachment",
					}.Build(),
				}.Build(),
			}.Build())
			Expect(err).To(HaveOccurred())
			status, ok := grpcstatus.FromError(err)
			Expect(ok).To(BeTrue())
			Expect(status.Code()).To(Equal(grpccodes.InvalidArgument))
			Expect(err.Error()).To(ContainSubstring("spec is mandatory"))
		})

		It("Rejects Create with empty spec.external_ip", func() {
			_, err := server.Create(ctx, privatev1.ExternalIPAttachmentsCreateRequest_builder{
				Object: privatev1.ExternalIPAttachment_builder{
					Spec: privatev1.ExternalIPAttachmentSpec_builder{
						ComputeInstance: new("some-ci"),
					}.Build(),
				}.Build(),
			}.Build())
			Expect(err).To(HaveOccurred())
			status, ok := grpcstatus.FromError(err)
			Expect(ok).To(BeTrue())
			Expect(status.Code()).To(Equal(grpccodes.InvalidArgument))
			Expect(err.Error()).To(ContainSubstring("spec.external_ip"))
		})

		It("Rejects Create with no target set", func() {
			_, err := server.Create(ctx, privatev1.ExternalIPAttachmentsCreateRequest_builder{
				Object: privatev1.ExternalIPAttachment_builder{
					Spec: privatev1.ExternalIPAttachmentSpec_builder{
						ExternalIp: "some-ip",
					}.Build(),
				}.Build(),
			}.Build())
			Expect(err).To(HaveOccurred())
			status, ok := grpcstatus.FromError(err)
			Expect(ok).To(BeTrue())
			Expect(status.Code()).To(Equal(grpccodes.InvalidArgument))
			Expect(err.Error()).To(ContainSubstring("exactly one target must be set"))
		})

		It("Rejects Create when cluster target has no target_endpoint", func() {
			eip := createExternalIPInState(ctx, externalIPDao, sharedPool.GetId(),
				privatev1.ExternalIPState_EXTERNAL_IP_STATE_ALLOCATED, false)
			cluster := createClusterInState(ctx, clusterDao)

			_, err := server.Create(ctx, privatev1.ExternalIPAttachmentsCreateRequest_builder{
				Object: privatev1.ExternalIPAttachment_builder{
					Spec: privatev1.ExternalIPAttachmentSpec_builder{
						ExternalIp: eip.GetId(),
						Cluster:    new(cluster.GetId()),
					}.Build(),
				}.Build(),
			}.Build())
			Expect(err).To(HaveOccurred())
			status, ok := grpcstatus.FromError(err)
			Expect(ok).To(BeTrue())
			Expect(status.Code()).To(Equal(grpccodes.InvalidArgument))
			Expect(err.Error()).To(ContainSubstring("target_endpoint"))
			Expect(err.Error()).To(ContainSubstring("required when target is cluster"))
		})

		It("Rejects Create when non-cluster target has target_endpoint set", func() {
			eip := createExternalIPInState(ctx, externalIPDao, sharedPool.GetId(),
				privatev1.ExternalIPState_EXTERNAL_IP_STATE_ALLOCATED, false)
			ci := createComputeInstanceInState(ctx, computeInstanceDao,
				privatev1.ComputeInstanceState_COMPUTE_INSTANCE_STATE_RUNNING)

			_, err := server.Create(ctx, privatev1.ExternalIPAttachmentsCreateRequest_builder{
				Object: privatev1.ExternalIPAttachment_builder{
					Spec: privatev1.ExternalIPAttachmentSpec_builder{
						ExternalIp:      eip.GetId(),
						ComputeInstance: new(ci.GetId()),
						TargetEndpoint:  privatev1.ExternalIPAttachmentEndpoint_EXTERNAL_IP_ATTACHMENT_ENDPOINT_API,
					}.Build(),
				}.Build(),
			}.Build())
			Expect(err).To(HaveOccurred())
			status, ok := grpcstatus.FromError(err)
			Expect(ok).To(BeTrue())
			Expect(status.Code()).To(Equal(grpccodes.InvalidArgument))
			Expect(err.Error()).To(ContainSubstring("target_endpoint"))
			Expect(err.Error()).To(ContainSubstring("UNSPECIFIED for non-cluster"))
		})
	})

	Describe("ExternalIP reference validation", func() {
		It("Rejects Create when ExternalIP does not exist", func() {
			ci := createComputeInstanceInState(ctx, computeInstanceDao,
				privatev1.ComputeInstanceState_COMPUTE_INSTANCE_STATE_RUNNING)

			_, err := server.Create(ctx, privatev1.ExternalIPAttachmentsCreateRequest_builder{
				Object: privatev1.ExternalIPAttachment_builder{
					Spec: privatev1.ExternalIPAttachmentSpec_builder{
						ExternalIp:      "nonexistent-external-ip-id",
						ComputeInstance: new(ci.GetId()),
					}.Build(),
				}.Build(),
			}.Build())
			Expect(err).To(HaveOccurred())
			status, ok := grpcstatus.FromError(err)
			Expect(ok).To(BeTrue())
			Expect(status.Code()).To(Equal(grpccodes.InvalidArgument))
			Expect(err.Error()).To(ContainSubstring("does not exist"))
		})

		It("Rejects Create when ExternalIP is not in ALLOCATED state", func() {
			eip := createExternalIPInState(ctx, externalIPDao, sharedPool.GetId(),
				privatev1.ExternalIPState_EXTERNAL_IP_STATE_PENDING, false)
			ci := createComputeInstanceInState(ctx, computeInstanceDao,
				privatev1.ComputeInstanceState_COMPUTE_INSTANCE_STATE_RUNNING)

			_, err := server.Create(ctx, privatev1.ExternalIPAttachmentsCreateRequest_builder{
				Object: privatev1.ExternalIPAttachment_builder{
					Spec: privatev1.ExternalIPAttachmentSpec_builder{
						ExternalIp:      eip.GetId(),
						ComputeInstance: new(ci.GetId()),
					}.Build(),
				}.Build(),
			}.Build())
			Expect(err).To(HaveOccurred())
			status, ok := grpcstatus.FromError(err)
			Expect(ok).To(BeTrue())
			Expect(status.Code()).To(Equal(grpccodes.FailedPrecondition))
			Expect(err.Error()).To(ContainSubstring("not in ALLOCATED state"))
		})

		It("Rejects Create when ExternalIP is already attached", func() {
			eip := createExternalIPInState(ctx, externalIPDao, sharedPool.GetId(),
				privatev1.ExternalIPState_EXTERNAL_IP_STATE_ALLOCATED, true)
			ci := createComputeInstanceInState(ctx, computeInstanceDao,
				privatev1.ComputeInstanceState_COMPUTE_INSTANCE_STATE_RUNNING)

			_, err := server.Create(ctx, privatev1.ExternalIPAttachmentsCreateRequest_builder{
				Object: privatev1.ExternalIPAttachment_builder{
					Spec: privatev1.ExternalIPAttachmentSpec_builder{
						ExternalIp:      eip.GetId(),
						ComputeInstance: new(ci.GetId()),
					}.Build(),
				}.Build(),
			}.Build())
			Expect(err).To(HaveOccurred())
			status, ok := grpcstatus.FromError(err)
			Expect(ok).To(BeTrue())
			Expect(status.Code()).To(Equal(grpccodes.FailedPrecondition))
			Expect(err.Error()).To(ContainSubstring("already attached"))
		})
	})

	Describe("Target reference validation", func() {
		It("Rejects Create when ComputeInstance does not exist", func() {
			eip := createExternalIPInState(ctx, externalIPDao, sharedPool.GetId(),
				privatev1.ExternalIPState_EXTERNAL_IP_STATE_ALLOCATED, false)

			_, err := server.Create(ctx, privatev1.ExternalIPAttachmentsCreateRequest_builder{
				Object: privatev1.ExternalIPAttachment_builder{
					Spec: privatev1.ExternalIPAttachmentSpec_builder{
						ExternalIp:      eip.GetId(),
						ComputeInstance: new("nonexistent-ci-id"),
					}.Build(),
				}.Build(),
			}.Build())
			Expect(err).To(HaveOccurred())
			status, ok := grpcstatus.FromError(err)
			Expect(ok).To(BeTrue())
			Expect(status.Code()).To(Equal(grpccodes.InvalidArgument))
			Expect(err.Error()).To(ContainSubstring("ComputeInstance"))
			Expect(err.Error()).To(ContainSubstring("does not exist"))
		})

		It("Rejects Create when Cluster does not exist", func() {
			eip := createExternalIPInState(ctx, externalIPDao, sharedPool.GetId(),
				privatev1.ExternalIPState_EXTERNAL_IP_STATE_ALLOCATED, false)

			_, err := server.Create(ctx, privatev1.ExternalIPAttachmentsCreateRequest_builder{
				Object: privatev1.ExternalIPAttachment_builder{
					Spec: privatev1.ExternalIPAttachmentSpec_builder{
						ExternalIp:     eip.GetId(),
						Cluster:        new("nonexistent-cluster-id"),
						TargetEndpoint: privatev1.ExternalIPAttachmentEndpoint_EXTERNAL_IP_ATTACHMENT_ENDPOINT_API,
					}.Build(),
				}.Build(),
			}.Build())
			Expect(err).To(HaveOccurred())
			status, ok := grpcstatus.FromError(err)
			Expect(ok).To(BeTrue())
			Expect(status.Code()).To(Equal(grpccodes.InvalidArgument))
			Expect(err.Error()).To(ContainSubstring("Cluster"))
			Expect(err.Error()).To(ContainSubstring("does not exist"))
		})

		It("Rejects Create when BareMetalInstance does not exist", func() {
			eip := createExternalIPInState(ctx, externalIPDao, sharedPool.GetId(),
				privatev1.ExternalIPState_EXTERNAL_IP_STATE_ALLOCATED, false)

			_, err := server.Create(ctx, privatev1.ExternalIPAttachmentsCreateRequest_builder{
				Object: privatev1.ExternalIPAttachment_builder{
					Spec: privatev1.ExternalIPAttachmentSpec_builder{
						ExternalIp:        eip.GetId(),
						BaremetalInstance: new("nonexistent-bmi-id"),
					}.Build(),
				}.Build(),
			}.Build())
			Expect(err).To(HaveOccurred())
			status, ok := grpcstatus.FromError(err)
			Expect(ok).To(BeTrue())
			Expect(status.Code()).To(Equal(grpccodes.InvalidArgument))
			Expect(err.Error()).To(ContainSubstring("BareMetalInstance"))
			Expect(err.Error()).To(ContainSubstring("does not exist"))
		})
	})

	Describe("Uniqueness constraints", func() {
		It("Rejects Create when ExternalIP is already attached (attached flag)", func() {
			eip := createExternalIPInState(ctx, externalIPDao, sharedPool.GetId(),
				privatev1.ExternalIPState_EXTERNAL_IP_STATE_ALLOCATED, false)
			ci1 := createComputeInstanceInState(ctx, computeInstanceDao,
				privatev1.ComputeInstanceState_COMPUTE_INSTANCE_STATE_RUNNING)
			ci2 := createComputeInstanceInState(ctx, computeInstanceDao,
				privatev1.ComputeInstanceState_COMPUTE_INSTANCE_STATE_RUNNING)

			_, err := server.Create(ctx, privatev1.ExternalIPAttachmentsCreateRequest_builder{
				Object: privatev1.ExternalIPAttachment_builder{
					Spec: privatev1.ExternalIPAttachmentSpec_builder{
						ExternalIp:      eip.GetId(),
						ComputeInstance: new(ci1.GetId()),
					}.Build(),
				}.Build(),
			}.Build())
			Expect(err).ToNot(HaveOccurred())

			_, err = server.Create(ctx, privatev1.ExternalIPAttachmentsCreateRequest_builder{
				Object: privatev1.ExternalIPAttachment_builder{
					Spec: privatev1.ExternalIPAttachmentSpec_builder{
						ExternalIp:      eip.GetId(),
						ComputeInstance: new(ci2.GetId()),
					}.Build(),
				}.Build(),
			}.Build())
			Expect(err).To(HaveOccurred())
			status, ok := grpcstatus.FromError(err)
			Expect(ok).To(BeTrue())
			Expect(status.Code()).To(Equal(grpccodes.FailedPrecondition))
			Expect(err.Error()).To(ContainSubstring("already attached"))
		})

		It("Rejects Create when ExternalIP already has an attachment (uniqueness check)", func() {
			eip := createExternalIPInState(ctx, externalIPDao, sharedPool.GetId(),
				privatev1.ExternalIPState_EXTERNAL_IP_STATE_ALLOCATED, false)
			ci1 := createComputeInstanceInState(ctx, computeInstanceDao,
				privatev1.ComputeInstanceState_COMPUTE_INSTANCE_STATE_RUNNING)
			ci2 := createComputeInstanceInState(ctx, computeInstanceDao,
				privatev1.ComputeInstanceState_COMPUTE_INSTANCE_STATE_RUNNING)

			_, err := server.Create(ctx, privatev1.ExternalIPAttachmentsCreateRequest_builder{
				Object: privatev1.ExternalIPAttachment_builder{
					Spec: privatev1.ExternalIPAttachmentSpec_builder{
						ExternalIp:      eip.GetId(),
						ComputeInstance: new(ci1.GetId()),
					}.Build(),
				}.Build(),
			}.Build())
			Expect(err).ToNot(HaveOccurred())

			// Reset the attached flag so the uniqueness check path is exercised
			ipResp, err := externalIPDao.Get().SetId(eip.GetId()).Do(ctx)
			Expect(err).ToNot(HaveOccurred())
			ipResp.GetObject().GetStatus().SetAttached(false)
			_, err = externalIPDao.Update().SetObject(ipResp.GetObject()).Do(ctx)
			Expect(err).ToNot(HaveOccurred())

			_, err = server.Create(ctx, privatev1.ExternalIPAttachmentsCreateRequest_builder{
				Object: privatev1.ExternalIPAttachment_builder{
					Spec: privatev1.ExternalIPAttachmentSpec_builder{
						ExternalIp:      eip.GetId(),
						ComputeInstance: new(ci2.GetId()),
					}.Build(),
				}.Build(),
			}.Build())
			Expect(err).To(HaveOccurred())
			status, ok := grpcstatus.FromError(err)
			Expect(ok).To(BeTrue())
			Expect(status.Code()).To(Equal(grpccodes.AlreadyExists))
			Expect(err.Error()).To(ContainSubstring("ExternalIP"))
		})
	})

	Describe("Immutable fields", func() {
		It("Rejects update of spec.external_ip", func() {
			eip := createExternalIPInState(ctx, externalIPDao, sharedPool.GetId(),
				privatev1.ExternalIPState_EXTERNAL_IP_STATE_ALLOCATED, false)
			ci := createComputeInstanceInState(ctx, computeInstanceDao,
				privatev1.ComputeInstanceState_COMPUTE_INSTANCE_STATE_RUNNING)

			createResponse, err := server.Create(ctx, privatev1.ExternalIPAttachmentsCreateRequest_builder{
				Object: privatev1.ExternalIPAttachment_builder{
					Spec: privatev1.ExternalIPAttachmentSpec_builder{
						ExternalIp:      eip.GetId(),
						ComputeInstance: new(ci.GetId()),
					}.Build(),
				}.Build(),
			}.Build())
			Expect(err).ToNot(HaveOccurred())

			eip2 := createExternalIPInState(ctx, externalIPDao, sharedPool.GetId(),
				privatev1.ExternalIPState_EXTERNAL_IP_STATE_ALLOCATED, false)
			_, err = server.Update(ctx, privatev1.ExternalIPAttachmentsUpdateRequest_builder{
				Object: privatev1.ExternalIPAttachment_builder{
					Id: createResponse.GetObject().GetId(),
					Spec: privatev1.ExternalIPAttachmentSpec_builder{
						ExternalIp: eip2.GetId(),
					}.Build(),
				}.Build(),
				UpdateMask: &fieldmaskpb.FieldMask{
					Paths: []string{"spec.external_ip"},
				},
			}.Build())
			Expect(err).To(HaveOccurred())
			status, ok := grpcstatus.FromError(err)
			Expect(ok).To(BeTrue())
			Expect(status.Code()).To(Equal(grpccodes.InvalidArgument))
			Expect(err.Error()).To(ContainSubstring("spec.external_ip"))
			Expect(err.Error()).To(ContainSubstring("immutable"))
		})

		It("Rejects update of spec.compute_instance", func() {
			eip := createExternalIPInState(ctx, externalIPDao, sharedPool.GetId(),
				privatev1.ExternalIPState_EXTERNAL_IP_STATE_ALLOCATED, false)
			ci := createComputeInstanceInState(ctx, computeInstanceDao,
				privatev1.ComputeInstanceState_COMPUTE_INSTANCE_STATE_RUNNING)

			createResponse, err := server.Create(ctx, privatev1.ExternalIPAttachmentsCreateRequest_builder{
				Object: privatev1.ExternalIPAttachment_builder{
					Spec: privatev1.ExternalIPAttachmentSpec_builder{
						ExternalIp:      eip.GetId(),
						ComputeInstance: new(ci.GetId()),
					}.Build(),
				}.Build(),
			}.Build())
			Expect(err).ToNot(HaveOccurred())

			ci2 := createComputeInstanceInState(ctx, computeInstanceDao,
				privatev1.ComputeInstanceState_COMPUTE_INSTANCE_STATE_RUNNING)
			_, err = server.Update(ctx, privatev1.ExternalIPAttachmentsUpdateRequest_builder{
				Object: privatev1.ExternalIPAttachment_builder{
					Id: createResponse.GetObject().GetId(),
					Spec: privatev1.ExternalIPAttachmentSpec_builder{
						ExternalIp:      eip.GetId(),
						ComputeInstance: new(ci2.GetId()),
					}.Build(),
				}.Build(),
				UpdateMask: &fieldmaskpb.FieldMask{
					Paths: []string{"spec.compute_instance"},
				},
			}.Build())
			Expect(err).To(HaveOccurred())
			status, ok := grpcstatus.FromError(err)
			Expect(ok).To(BeTrue())
			Expect(status.Code()).To(Equal(grpccodes.InvalidArgument))
			Expect(err.Error()).To(ContainSubstring("spec.compute_instance"))
			Expect(err.Error()).To(ContainSubstring("immutable"))
		})
	})

	Describe("Attached flag management", func() {
		It("Sets ExternalIP.status.attached to true on Create", func() {
			eip := createExternalIPInState(ctx, externalIPDao, sharedPool.GetId(),
				privatev1.ExternalIPState_EXTERNAL_IP_STATE_ALLOCATED, false)
			ci := createComputeInstanceInState(ctx, computeInstanceDao,
				privatev1.ComputeInstanceState_COMPUTE_INSTANCE_STATE_RUNNING)

			_, err := server.Create(ctx, privatev1.ExternalIPAttachmentsCreateRequest_builder{
				Object: privatev1.ExternalIPAttachment_builder{
					Spec: privatev1.ExternalIPAttachmentSpec_builder{
						ExternalIp:      eip.GetId(),
						ComputeInstance: new(ci.GetId()),
					}.Build(),
				}.Build(),
			}.Build())
			Expect(err).ToNot(HaveOccurred())

			ipResp, err := externalIPDao.Get().SetId(eip.GetId()).Do(ctx)
			Expect(err).ToNot(HaveOccurred())
			Expect(ipResp.GetObject().GetStatus().GetAttached()).To(BeTrue())
		})

		It("Sets ExternalIP.status.attached to false on Delete", func() {
			eip := createExternalIPInState(ctx, externalIPDao, sharedPool.GetId(),
				privatev1.ExternalIPState_EXTERNAL_IP_STATE_ALLOCATED, false)
			ci := createComputeInstanceInState(ctx, computeInstanceDao,
				privatev1.ComputeInstanceState_COMPUTE_INSTANCE_STATE_RUNNING)

			createResp, err := server.Create(ctx, privatev1.ExternalIPAttachmentsCreateRequest_builder{
				Object: privatev1.ExternalIPAttachment_builder{
					Spec: privatev1.ExternalIPAttachmentSpec_builder{
						ExternalIp:      eip.GetId(),
						ComputeInstance: new(ci.GetId()),
					}.Build(),
				}.Build(),
			}.Build())
			Expect(err).ToNot(HaveOccurred())

			_, err = server.Delete(ctx, privatev1.ExternalIPAttachmentsDeleteRequest_builder{
				Id: createResp.GetObject().GetId(),
			}.Build())
			Expect(err).ToNot(HaveOccurred())

			ipResp, err := externalIPDao.Get().SetId(eip.GetId()).Do(ctx)
			Expect(err).ToNot(HaveOccurred())
			Expect(ipResp.GetObject().GetStatus().GetAttached()).To(BeFalse())
		})
	})

	Describe("Delete behaviour", func() {
		It("Returns error for empty ID on Delete", func() {
			_, err := server.Delete(ctx, privatev1.ExternalIPAttachmentsDeleteRequest_builder{}.Build())
			Expect(err).To(HaveOccurred())
			status, ok := grpcstatus.FromError(err)
			Expect(ok).To(BeTrue())
			Expect(status.Code()).To(Equal(grpccodes.InvalidArgument))
			Expect(err.Error()).To(ContainSubstring("identifier is mandatory"))
		})
	})
})

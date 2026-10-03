/*
Copyright (c) 2026 Red Hat Inc.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

  http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package autoexternalipattachment

import (
	"bytes"
	"context"
	"errors"
	"io"
	"log/slog"
	"strconv"
	"strings"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"google.golang.org/grpc"
	grpccodes "google.golang.org/grpc/codes"
	grpcstatus "google.golang.org/grpc/status"

	privatev1 "github.com/osac-project/osac/proto/gen/osac/private/v1"
)

type fakeExternalIPAttachmentsClient struct {
	attachments              []*privatev1.ExternalIPAttachment
	created                  []*privatev1.ExternalIPAttachment
	createErr                error
	persistBeforeCreateError bool
}

func (f *fakeExternalIPAttachmentsClient) List(_ context.Context, request *privatev1.ExternalIPAttachmentsListRequest, _ ...grpc.CallOption) (*privatev1.ExternalIPAttachmentsListResponse, error) {
	filter := request.GetFilter()
	items := make([]*privatev1.ExternalIPAttachment, 0, len(f.attachments))
	for _, attachment := range f.attachments {
		if strings.Contains(filter, strconv.Quote(attachment.GetSpec().GetExternalIp().GetId())) {
			items = append(items, attachment)
		}
	}
	return &privatev1.ExternalIPAttachmentsListResponse{
		Items: items,
		Total: int32(len(items)),
	}, nil
}

func (f *fakeExternalIPAttachmentsClient) Create(_ context.Context, request *privatev1.ExternalIPAttachmentsCreateRequest, _ ...grpc.CallOption) (*privatev1.ExternalIPAttachmentsCreateResponse, error) {
	attachment := request.GetObject()
	f.created = append(f.created, attachment)
	if f.createErr != nil {
		if f.persistBeforeCreateError {
			f.attachments = append(f.attachments, attachment)
		}
		return nil, f.createErr
	}
	f.attachments = append(f.attachments, attachment)
	return &privatev1.ExternalIPAttachmentsCreateResponse{Object: attachment}, nil
}

type fakeComputeInstancesClient struct {
	object *privatev1.ComputeInstance
	err    error
}

func (f *fakeComputeInstancesClient) Get(context.Context, *privatev1.ComputeInstancesGetRequest, ...grpc.CallOption) (*privatev1.ComputeInstancesGetResponse, error) {
	return &privatev1.ComputeInstancesGetResponse{Object: f.object}, f.err
}

type fakeClustersClient struct {
	object *privatev1.Cluster
	err    error
}

func (f *fakeClustersClient) Get(context.Context, *privatev1.ClustersGetRequest, ...grpc.CallOption) (*privatev1.ClustersGetResponse, error) {
	return &privatev1.ClustersGetResponse{Object: f.object}, f.err
}

type fakeBareMetalInstancesClient struct {
	object *privatev1.BareMetalInstance
	err    error
}

func (f *fakeBareMetalInstancesClient) Get(context.Context, *privatev1.BareMetalInstancesGetRequest, ...grpc.CallOption) (*privatev1.BareMetalInstancesGetResponse, error) {
	return &privatev1.BareMetalInstancesGetResponse{Object: f.object}, f.err
}

var _ = Describe("automatic ExternalIPAttachment reconciliation", func() {
	var (
		ctx                context.Context
		reconciler         *function
		attachments        *fakeExternalIPAttachmentsClient
		computeInstances   *fakeComputeInstancesClient
		clusters           *fakeClustersClient
		bareMetalInstances *fakeBareMetalInstancesClient
	)

	BeforeEach(func() {
		ctx = context.Background()
		attachments = &fakeExternalIPAttachmentsClient{}
		computeInstances = &fakeComputeInstancesClient{}
		clusters = &fakeClustersClient{}
		bareMetalInstances = &fakeBareMetalInstancesClient{}
		reconciler = &function{
			logger:                      slog.New(slog.NewTextHandler(io.Discard, nil)),
			externalIPAttachmentsClient: attachments,
			computeInstancesClient:      computeInstances,
			clustersClient:              clusters,
			bareMetalInstancesClient:    bareMetalInstances,
		}
	})

	It("waits for the ExternalIP to be allocated and the ComputeInstance to be ready with an address", func() {
		externalIP := computeExternalIP(privatev1.ExternalIPState_EXTERNAL_IP_STATE_PENDING)
		computeInstances.object = computeInstance(privatev1.ComputeInstanceState_COMPUTE_INSTANCE_STATE_STARTING, "")

		Expect(reconciler.run(ctx, externalIP)).To(Succeed())
		Expect(attachments.created).To(BeEmpty())

		externalIP.GetStatus().SetState(privatev1.ExternalIPState_EXTERNAL_IP_STATE_ALLOCATED)
		computeInstances.object = computeInstance(privatev1.ComputeInstanceState_COMPUTE_INSTANCE_STATE_RUNNING, "")
		Expect(reconciler.run(ctx, externalIP)).To(Succeed())
		Expect(attachments.created).To(BeEmpty())

		computeInstances.object = computeInstance(privatev1.ComputeInstanceState_COMPUTE_INSTANCE_STATE_RUNNING, "192.0.2.10")
		Expect(reconciler.run(ctx, externalIP)).To(Succeed())
		Expect(attachments.created).To(HaveLen(1))
		attachment := attachments.created[0]
		Expect(attachment.GetMetadata().GetName()).To(Equal("auto-eipa-" + externalIP.GetId()))
		Expect(attachment.GetMetadata().GetTenant()).To(Equal("tenant-1"))
		Expect(attachment.GetMetadata().GetLabels()[autoCreatedLabel]).To(Equal("true"))
		Expect(attachment.GetMetadata().GetLabels()[autoCreatedForLabel]).To(Equal("ci-1"))
		Expect(attachment.GetMetadata().GetAnnotations()[tenantAnnotation]).To(Equal("tenant-1"))
		Expect(attachment.GetMetadata().GetAnnotations()[ownerReferenceAnnotation]).To(Equal("ci-1"))
		Expect(attachment.GetSpec().GetExternalIp().GetId()).To(Equal(externalIP.GetId()))
		Expect(attachment.GetSpec().GetComputeInstance().GetId()).To(Equal("ci-1"))

		Expect(reconciler.run(ctx, externalIP)).To(Succeed())
		Expect(attachments.created).To(HaveLen(1))
	})

	It("creates endpoint-specific attachments for a ready Cluster", func() {
		clusters.object = readyCluster()
		for _, target := range []struct {
			name     string
			endpoint privatev1.ExternalIPAttachmentEndpoint
		}{
			{name: "api", endpoint: privatev1.ExternalIPAttachmentEndpoint_EXTERNAL_IP_ATTACHMENT_ENDPOINT_API},
			{name: "ingress", endpoint: privatev1.ExternalIPAttachmentEndpoint_EXTERNAL_IP_ATTACHMENT_ENDPOINT_INGRESS},
		} {
			externalIP := clusterExternalIP(target.name)
			Expect(reconciler.run(ctx, externalIP)).To(Succeed())
		}
		Expect(attachments.created).To(HaveLen(2))
		Expect(attachments.created[0].GetSpec().GetTargetEndpoint()).To(Equal(privatev1.ExternalIPAttachmentEndpoint_EXTERNAL_IP_ATTACHMENT_ENDPOINT_API))
		Expect(attachments.created[1].GetSpec().GetTargetEndpoint()).To(Equal(privatev1.ExternalIPAttachmentEndpoint_EXTERNAL_IP_ATTACHMENT_ENDPOINT_INGRESS))
		Expect(attachments.created[0].GetMetadata().GetLabels()[autoCreatedEndpointLabel]).To(Equal("api"))
		Expect(attachments.created[1].GetMetadata().GetLabels()[autoCreatedEndpointLabel]).To(Equal("ingress"))
	})

	It("waits for the selected Cluster endpoint address", func() {
		clusters.object = readyCluster()
		clusters.object.GetStatus().SetIngressEndpoint("")

		Expect(reconciler.run(ctx, clusterExternalIP("api"))).To(Succeed())
		Expect(reconciler.run(ctx, clusterExternalIP("ingress"))).To(Succeed())
		Expect(attachments.created).To(HaveLen(1))
		Expect(attachments.created[0].GetSpec().GetTargetEndpoint()).To(Equal(privatev1.ExternalIPAttachmentEndpoint_EXTERNAL_IP_ATTACHMENT_ENDPOINT_API))
	})

	It("does not attach across tenant boundaries", func() {
		computeInstances.object = computeInstance(privatev1.ComputeInstanceState_COMPUTE_INSTANCE_STATE_RUNNING, "192.0.2.10")
		computeInstances.object.GetMetadata().SetTenant("tenant-2")

		Expect(reconciler.run(ctx, computeExternalIP(privatev1.ExternalIPState_EXTERNAL_IP_STATE_ALLOCATED))).To(Succeed())
		Expect(attachments.created).To(BeEmpty())
	})

	It("ignores tenant-created ExternalIPs with forged deferred attachment markers", func() {
		computeInstances.object = computeInstance(privatev1.ComputeInstanceState_COMPUTE_INSTANCE_STATE_RUNNING, "192.0.2.10")
		computeInstances.object.GetMetadata().SetProject("victim-project")
		externalIP := computeExternalIP(privatev1.ExternalIPState_EXTERNAL_IP_STATE_ALLOCATED)
		externalIP.GetMetadata().SetProject("attacker-project")
		externalIP.GetMetadata().SetCreator("tenant-user")

		Expect(reconciler.run(ctx, externalIP)).To(Succeed())
		Expect(attachments.created).To(BeEmpty())
	})

	It("does not log tenant identity or ExternalIP identifiers", func() {
		var logs bytes.Buffer
		reconciler.logger = slog.New(slog.NewTextHandler(&logs, &slog.HandlerOptions{Level: slog.LevelDebug}))

		externalIP := computeExternalIP(privatev1.ExternalIPState_EXTERNAL_IP_STATE_ALLOCATED)
		externalIP.SetId("tenant-selected-external-ip")
		externalIP.GetMetadata().SetCreator("tenant-user@example.com")
		Expect(reconciler.run(ctx, externalIP)).To(Succeed())
		Expect(attachments.created).To(BeEmpty())
		Expect(logs.String()).NotTo(ContainSubstring("tenant-user@example.com"))
		Expect(logs.String()).NotTo(ContainSubstring("tenant-selected-external-ip"))

		logs.Reset()
		externalIP.GetMetadata().SetCreator(systemCreator)
		computeInstances.object = computeInstance(privatev1.ComputeInstanceState_COMPUTE_INSTANCE_STATE_RUNNING, "192.0.2.10")
		Expect(reconciler.run(ctx, externalIP)).To(Succeed())
		Expect(attachments.created).To(HaveLen(1))
		Expect(logs.String()).NotTo(ContainSubstring("tenant-selected-external-ip"))
		Expect(logs.String()).NotTo(ContainSubstring("external_ip_id"))
		Expect(logs.String()).NotTo(ContainSubstring("external_ip_attachment_id"))
	})

	It("waits for the ComputeInstance READY condition", func() {
		computeInstances.object = computeInstance(privatev1.ComputeInstanceState_COMPUTE_INSTANCE_STATE_RUNNING, "192.0.2.10")
		computeInstances.object.GetStatus().GetConditions()[0].SetStatus(privatev1.ConditionStatus_CONDITION_STATUS_FALSE)

		Expect(reconciler.run(ctx, computeExternalIP(privatev1.ExternalIPState_EXTERNAL_IP_STATE_ALLOCATED))).To(Succeed())
		Expect(attachments.created).To(BeEmpty())
	})

	It("waits until a ready BareMetalInstance has a primary IP", func() {
		bareMetalInstances.object = bareMetalInstance("")
		externalIP := bareMetalExternalIP()
		Expect(reconciler.run(ctx, externalIP)).To(Succeed())
		Expect(attachments.created).To(BeEmpty())

		bareMetalInstances.object = bareMetalInstance("192.0.2.20")
		Expect(reconciler.run(ctx, externalIP)).To(Succeed())
		Expect(attachments.created).To(HaveLen(1))
		Expect(attachments.created[0].GetSpec().GetBaremetalInstance().GetId()).To(Equal("bmi-1"))
	})

	It("ignores legacy automatic ExternalIPs", func() {
		externalIP := computeExternalIP(privatev1.ExternalIPState_EXTERNAL_IP_STATE_ALLOCATED)
		externalIP.GetMetadata().GetLabels()[autoAttachmentDeferredLabel] = "false"
		Expect(reconciler.run(ctx, externalIP)).To(Succeed())
		Expect(attachments.created).To(BeEmpty())
	})

	It("retries a failed Create and treats a racing AlreadyExists as success", func() {
		computeInstances.object = computeInstance(privatev1.ComputeInstanceState_COMPUTE_INSTANCE_STATE_RUNNING, "192.0.2.10")
		externalIP := computeExternalIP(privatev1.ExternalIPState_EXTERNAL_IP_STATE_ALLOCATED)
		attachments.createErr = errors.New("temporary failure")
		Expect(reconciler.run(ctx, externalIP)).To(MatchError(ContainSubstring("temporary failure")))
		Expect(attachments.created).To(HaveLen(1))

		attachments.createErr = grpcstatus.Error(grpccodes.AlreadyExists, "already exists")
		attachments.persistBeforeCreateError = true
		Expect(reconciler.run(ctx, externalIP)).To(Succeed())
		Expect(attachments.created).To(HaveLen(2))
	})

	It("validates existing attachments from typed fields without interpreting annotations", func() {
		computeInstances.object = computeInstance(privatev1.ComputeInstanceState_COMPUTE_INSTANCE_STATE_RUNNING, "192.0.2.10")
		externalIP := computeExternalIP(privatev1.ExternalIPState_EXTERNAL_IP_STATE_ALLOCATED)

		Expect(reconciler.run(ctx, externalIP)).To(Succeed())
		Expect(attachments.created).To(HaveLen(1))

		annotations := attachments.attachments[0].GetMetadata().GetAnnotations()
		annotations[tenantAnnotation] = "stale-tenant"
		annotations[ownerReferenceAnnotation] = "stale-owner"

		Expect(reconciler.run(ctx, externalIP)).To(Succeed())
		Expect(attachments.created).To(HaveLen(1))
	})
})

func computeExternalIP(state privatev1.ExternalIPState) *privatev1.ExternalIP {
	return privatev1.ExternalIP_builder{
		Id: "eip-ci-1",
		Metadata: privatev1.Metadata_builder{
			Creator: "system",
			Tenant:  "tenant-1",
			Labels: map[string]string{
				autoCreatedLabel:            "true",
				autoCreatedForLabel:         "ci-1",
				autoAttachmentDeferredLabel: "true",
				autoCreatedKindLabel:        "compute_instance",
			},
		}.Build(),
		Status: privatev1.ExternalIPStatus_builder{State: state}.Build(),
	}.Build()
}

func computeInstance(state privatev1.ComputeInstanceState, ip string) *privatev1.ComputeInstance {
	return privatev1.ComputeInstance_builder{
		Id:       "ci-1",
		Metadata: privatev1.Metadata_builder{Tenant: "tenant-1"}.Build(),
		Status: privatev1.ComputeInstanceStatus_builder{
			State:             state,
			InternalIpAddress: ip,
			Conditions: []*privatev1.ComputeInstanceCondition{
				privatev1.ComputeInstanceCondition_builder{
					Type:   privatev1.ComputeInstanceConditionType_COMPUTE_INSTANCE_CONDITION_TYPE_READY,
					Status: privatev1.ConditionStatus_CONDITION_STATUS_TRUE,
				}.Build(),
			},
		}.Build(),
	}.Build()
}

func readyCluster() *privatev1.Cluster {
	return privatev1.Cluster_builder{
		Id:       "cluster-1",
		Metadata: privatev1.Metadata_builder{Tenant: "tenant-1"}.Build(),
		Status: privatev1.ClusterStatus_builder{
			State:           privatev1.ClusterState_CLUSTER_STATE_READY,
			ApiEndpoint:     "10.0.0.10",
			IngressEndpoint: "10.0.0.11",
		}.Build(),
	}.Build()
}

func clusterExternalIP(endpoint string) *privatev1.ExternalIP {
	return privatev1.ExternalIP_builder{
		Id: "eip-cluster-" + endpoint,
		Metadata: privatev1.Metadata_builder{
			Creator: "system",
			Tenant:  "tenant-1",
			Labels: map[string]string{
				autoCreatedLabel:            "true",
				autoCreatedForLabel:         "cluster-1",
				autoAttachmentDeferredLabel: "true",
				autoCreatedKindLabel:        "cluster",
				autoCreatedEndpointLabel:    endpoint,
			},
		}.Build(),
		Status: privatev1.ExternalIPStatus_builder{State: privatev1.ExternalIPState_EXTERNAL_IP_STATE_ALLOCATED}.Build(),
	}.Build()
}

func bareMetalExternalIP() *privatev1.ExternalIP {
	return privatev1.ExternalIP_builder{
		Id: "eip-bmi-1",
		Metadata: privatev1.Metadata_builder{
			Creator: "system",
			Tenant:  "tenant-1",
			Labels: map[string]string{
				autoCreatedLabel:            "true",
				autoCreatedForLabel:         "bmi-1",
				autoAttachmentDeferredLabel: "true",
				autoCreatedKindLabel:        "bare_metal_instance",
			},
		}.Build(),
		Status: privatev1.ExternalIPStatus_builder{State: privatev1.ExternalIPState_EXTERNAL_IP_STATE_ALLOCATED}.Build(),
	}.Build()
}

func bareMetalInstance(ip string) *privatev1.BareMetalInstance {
	return privatev1.BareMetalInstance_builder{
		Id:       "bmi-1",
		Metadata: privatev1.Metadata_builder{Tenant: "tenant-1"}.Build(),
		Status: privatev1.BareMetalInstanceStatus_builder{
			State: privatev1.BareMetalInstanceState_BARE_METAL_INSTANCE_STATE_RUNNING,
			Conditions: []*privatev1.BareMetalInstanceCondition{
				privatev1.BareMetalInstanceCondition_builder{
					Type:   privatev1.BareMetalInstanceConditionType_BARE_METAL_INSTANCE_CONDITION_TYPE_READY,
					Status: privatev1.ConditionStatus_CONDITION_STATUS_TRUE,
				}.Build(),
			},
			NetworkAttachmentStatuses: []*privatev1.BareMetalNetworkAttachmentStatus{
				privatev1.BareMetalNetworkAttachmentStatus_builder{Primary: true, IpAddress: ip}.Build(),
			},
		}.Build(),
	}.Build()
}

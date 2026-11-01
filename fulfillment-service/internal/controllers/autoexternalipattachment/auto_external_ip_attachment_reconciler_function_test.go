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
	"context"
	"errors"
	"io"
	"log/slog"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"google.golang.org/grpc"
	grpccodes "google.golang.org/grpc/codes"
	grpcstatus "google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/timestamppb"

	"github.com/osac-project/osac/fulfillment-service/internal/controllers"
	privatev1 "github.com/osac-project/osac/proto/gen/osac/private/v1"
)

type fakeAttachments struct {
	existing  []*privatev1.ExternalIPAttachment
	created   []*privatev1.ExternalIPAttachment
	listErr   error
	createErr error
}

func (f *fakeAttachments) List(context.Context, *privatev1.ExternalIPAttachmentsListRequest,
	...grpc.CallOption) (*privatev1.ExternalIPAttachmentsListResponse, error) {
	if f.listErr != nil {
		return nil, f.listErr
	}
	return privatev1.ExternalIPAttachmentsListResponse_builder{Items: f.existing}.Build(), nil
}

func (f *fakeAttachments) Create(_ context.Context, request *privatev1.ExternalIPAttachmentsCreateRequest,
	_ ...grpc.CallOption) (*privatev1.ExternalIPAttachmentsCreateResponse, error) {
	if f.createErr != nil {
		return nil, f.createErr
	}
	f.created = append(f.created, request.GetObject())
	return privatev1.ExternalIPAttachmentsCreateResponse_builder{Object: request.GetObject()}.Build(), nil
}

type fakeComputeInstances struct {
	instance *privatev1.ComputeInstance
	err      error
}

func (f *fakeComputeInstances) Get(context.Context, *privatev1.ComputeInstancesGetRequest,
	...grpc.CallOption) (*privatev1.ComputeInstancesGetResponse, error) {
	if f.err != nil {
		return nil, f.err
	}
	return privatev1.ComputeInstancesGetResponse_builder{Object: f.instance}.Build(), nil
}

func automaticExternalIP(state privatev1.ExternalIPState) *privatev1.ExternalIP {
	return privatev1.ExternalIP_builder{
		Id: "eip-1",
		Metadata: privatev1.Metadata_builder{
			Name:    "auto-eip-ci-1",
			Tenant:  "tenant-a",
			Creator: systemCreator,
			Labels: map[string]string{
				autoCreatedLabel:     "true",
				autoCreatedForLabel:  "ci-1",
				autoCreatedKindLabel: computeInstanceKind,
			},
		}.Build(),
		Status: privatev1.ExternalIPStatus_builder{State: state}.Build(),
	}.Build()
}

func readyInstance() *privatev1.ComputeInstance {
	return privatev1.ComputeInstance_builder{
		Id:       "ci-1",
		Metadata: privatev1.Metadata_builder{Tenant: "tenant-a"}.Build(),
		Status: privatev1.ComputeInstanceStatus_builder{
			State: privatev1.ComputeInstanceState_COMPUTE_INSTANCE_STATE_RUNNING,
			Conditions: []*privatev1.ComputeInstanceCondition{
				privatev1.ComputeInstanceCondition_builder{
					Type:   privatev1.ComputeInstanceConditionType_COMPUTE_INSTANCE_CONDITION_TYPE_READY,
					Status: privatev1.ConditionStatus_CONDITION_STATUS_TRUE,
				}.Build(),
			},
			ComputeNetworkAttachmentStatuses: []*privatev1.ComputeNetworkAttachmentStatus{
				privatev1.ComputeNetworkAttachmentStatus_builder{IpAddress: "10.0.0.5"}.Build(),
			},
		}.Build(),
	}.Build()
}

func requeueInterval(err error) time.Duration {
	var retryable interface{ RequeueAfter() time.Duration }
	if errors.As(err, &retryable) {
		return retryable.RequeueAfter()
	}
	return 0
}

var _ = Describe("Automatic ExternalIPAttachment reconciler", func() {
	var (
		ctx         context.Context
		attachments *fakeAttachments
		instances   *fakeComputeInstances
		r           *function
	)

	BeforeEach(func() {
		ctx = context.Background()
		attachments = &fakeAttachments{}
		instances = &fakeComputeInstances{instance: readyInstance()}
		r = &function{
			logger:                      slog.New(slog.NewTextHandler(io.Discard, nil)),
			externalIPAttachmentsClient: attachments,
			computeInstancesClient:      instances,
		}
	})

	It("creates the attachment of an ALLOCATED automatic ExternalIP of a ready VM", func() {
		Expect(r.run(ctx, automaticExternalIP(privatev1.ExternalIPState_EXTERNAL_IP_STATE_ALLOCATED))).To(Succeed())
		Expect(attachments.created).To(HaveLen(1))
		created := attachments.created[0]
		Expect(created.GetMetadata().GetName()).To(Equal("auto-eipa-eip-1"))
		Expect(created.GetMetadata().GetTenant()).To(Equal("tenant-a"))
		Expect(created.GetMetadata().GetLabels()).To(HaveKeyWithValue(autoCreatedLabel, "true"))
		Expect(created.GetMetadata().GetLabels()).To(HaveKeyWithValue(autoCreatedForLabel, "ci-1"))
		Expect(created.GetMetadata().GetAnnotations()).To(HaveKeyWithValue(ownerReferenceAnnotation, "ci-1"))
		Expect(created.GetSpec().GetExternalIp().GetId()).To(Equal("eip-1"))
		Expect(created.GetSpec().GetComputeInstance().GetId()).To(Equal("ci-1"))
	})

	DescribeTable("ignores ExternalIPs that are not automatic ComputeInstance addresses",
		func(mutate func(*privatev1.ExternalIP)) {
			externalIP := automaticExternalIP(privatev1.ExternalIPState_EXTERNAL_IP_STATE_ALLOCATED)
			mutate(externalIP)
			Expect(r.run(ctx, externalIP)).To(Succeed())
			Expect(attachments.created).To(BeEmpty())
		},
		Entry("tenant creator", func(e *privatev1.ExternalIP) { e.GetMetadata().SetCreator("alice") }),
		Entry("missing auto-created label", func(e *privatev1.ExternalIP) {
			delete(e.GetMetadata().GetLabels(), autoCreatedLabel)
		}),
		Entry("cluster kind", func(e *privatev1.ExternalIP) {
			e.GetMetadata().GetLabels()[autoCreatedKindLabel] = "cluster"
		}),
		Entry("being deleted", func(e *privatev1.ExternalIP) {
			e.GetMetadata().SetDeletionTimestamp(timestamppb.Now())
		}),
	)

	It("stops for a FAILED ExternalIP", func() {
		Expect(r.run(ctx, automaticExternalIP(privatev1.ExternalIPState_EXTERNAL_IP_STATE_FAILED))).To(Succeed())
		Expect(attachments.created).To(BeEmpty())
	})

	It("rechecks a PENDING ExternalIP every 30 seconds", func() {
		err := r.run(ctx, automaticExternalIP(privatev1.ExternalIPState_EXTERNAL_IP_STATE_PENDING))
		Expect(requeueInterval(err)).To(Equal(recheckInterval))
		Expect(attachments.created).To(BeEmpty())
	})

	It("rechecks while the VM has no address", func() {
		instances.instance.GetStatus().SetComputeNetworkAttachmentStatuses(nil)
		err := r.run(ctx, automaticExternalIP(privatev1.ExternalIPState_EXTERNAL_IP_STATE_ALLOCATED))
		Expect(requeueInterval(err)).To(Equal(recheckInterval))
		Expect(attachments.created).To(BeEmpty())
	})

	It("rechecks while the VM isn't READY", func() {
		instances.instance.GetStatus().SetConditions(nil)
		err := r.run(ctx, automaticExternalIP(privatev1.ExternalIPState_EXTERNAL_IP_STATE_ALLOCATED))
		Expect(requeueInterval(err)).To(Equal(recheckInterval))
	})

	It("stops when the VM is FAILED", func() {
		instances.instance.GetStatus().SetState(privatev1.ComputeInstanceState_COMPUTE_INSTANCE_STATE_FAILED)
		Expect(r.run(ctx, automaticExternalIP(privatev1.ExternalIPState_EXTERNAL_IP_STATE_ALLOCATED))).To(Succeed())
		Expect(attachments.created).To(BeEmpty())
	})

	It("stops when the VM is being deleted", func() {
		instances.instance.GetMetadata().SetDeletionTimestamp(timestamppb.Now())
		Expect(r.run(ctx, automaticExternalIP(privatev1.ExternalIPState_EXTERNAL_IP_STATE_ALLOCATED))).To(Succeed())
		Expect(attachments.created).To(BeEmpty())
	})

	It("stops when the VM belongs to another tenant", func() {
		instances.instance.GetMetadata().SetTenant("tenant-b")
		Expect(r.run(ctx, automaticExternalIP(privatev1.ExternalIPState_EXTERNAL_IP_STATE_ALLOCATED))).To(Succeed())
		Expect(attachments.created).To(BeEmpty())
	})

	It("stops when the VM no longer exists", func() {
		instances.err = grpcstatus.Error(grpccodes.NotFound, "not found")
		Expect(r.run(ctx, automaticExternalIP(privatev1.ExternalIPState_EXTERNAL_IP_STATE_ALLOCATED))).To(Succeed())
	})

	It("does nothing when the attachment already exists", func() {
		attachments.existing = []*privatev1.ExternalIPAttachment{privatev1.ExternalIPAttachment_builder{Id: "a"}.Build()}
		Expect(r.run(ctx, automaticExternalIP(privatev1.ExternalIPState_EXTERNAL_IP_STATE_ALLOCATED))).To(Succeed())
		Expect(attachments.created).To(BeEmpty())
	})

	It("treats ALREADY_EXISTS as success", func() {
		attachments.createErr = grpcstatus.Error(grpccodes.AlreadyExists, "exists")
		Expect(r.run(ctx, automaticExternalIP(privatev1.ExternalIPState_EXTERNAL_IP_STATE_ALLOCATED))).To(Succeed())
	})

	It("rechecks when a gate isn't met", func() {
		attachments.createErr = grpcstatus.Error(grpccodes.FailedPrecondition, "not ready")
		err := r.run(ctx, automaticExternalIP(privatev1.ExternalIPState_EXTERNAL_IP_STATE_ALLOCATED))
		Expect(requeueInterval(err)).To(Equal(recheckInterval))
	})

	It("retries transient errors", func() {
		attachments.createErr = grpcstatus.Error(grpccodes.Unavailable, "down")
		err := r.run(ctx, automaticExternalIP(privatev1.ExternalIPState_EXTERNAL_IP_STATE_ALLOCATED))
		Expect(controllers.IsRequeueable(err)).To(BeTrue())
	})
})

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
	"net"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"google.golang.org/protobuf/types/known/timestamppb"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"google.golang.org/grpc"
	grpccodes "google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	grpcstatus "google.golang.org/grpc/status"
	"google.golang.org/grpc/test/bufconn"

	"github.com/osac-project/osac/fulfillment-service/internal/controllers"
	privatev1 "github.com/osac-project/osac/proto/gen/osac/private/v1"
)

type fakeExternalIPAttachmentsClient struct {
	mu                       sync.Mutex
	attachments              []*privatev1.ExternalIPAttachment
	created                  []*privatev1.ExternalIPAttachment
	listErr                  error
	createErr                error
	persistBeforeCreateError bool
}

func (f *fakeExternalIPAttachmentsClient) List(_ context.Context, request *privatev1.ExternalIPAttachmentsListRequest, _ ...grpc.CallOption) (*privatev1.ExternalIPAttachmentsListResponse, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.listErr != nil {
		return nil, f.listErr
	}
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
	f.mu.Lock()
	defer f.mu.Unlock()
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

func (f *fakeExternalIPAttachmentsClient) counts() (created, persisted int) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.created), len(f.attachments)
}

type deferredAttachmentRetryServer struct {
	privatev1.UnimplementedExternalIPsServer
	privatev1.UnimplementedEventsServer

	externalIP *privatev1.ExternalIP
	events     chan *privatev1.Event
	watchCalls atomic.Int32
}

func (s *deferredAttachmentRetryServer) Get(_ context.Context, request *privatev1.ExternalIPsGetRequest) (*privatev1.ExternalIPsGetResponse, error) {
	if request.GetId() != s.externalIP.GetId() {
		return nil, grpcstatus.Error(grpccodes.NotFound, "external IP not found")
	}
	return &privatev1.ExternalIPsGetResponse{Object: s.externalIP}, nil
}

func (s *deferredAttachmentRetryServer) Watch(_ *privatev1.EventsWatchRequest, stream grpc.ServerStreamingServer[privatev1.EventsWatchResponse]) error {
	s.watchCalls.Add(1)
	for {
		select {
		case <-stream.Context().Done():
			return stream.Context().Err()
		case event := <-s.events:
			if err := stream.Send(&privatev1.EventsWatchResponse{Event: event}); err != nil {
				return err
			}
		}
	}
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

	It("does not create a deferred attachment after the BareMetalInstance starts deleting", func() {
		bareMetalInstances.object = bareMetalInstance("192.0.2.20")
		bareMetalInstances.object.GetMetadata().SetDeletionTimestamp(timestamppb.Now())

		Expect(reconciler.run(ctx, bareMetalExternalIP())).To(Succeed())
		Expect(attachments.created).To(BeEmpty())
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

	It("marks transient target and attachment lookup failures for a prompt retry", func() {
		externalIP := computeExternalIP(privatev1.ExternalIPState_EXTERNAL_IP_STATE_ALLOCATED)
		computeInstances.err = grpcstatus.Error(grpccodes.Unavailable, "temporary ComputeInstance lookup failure")

		expectPromptRetry(reconciler.run(ctx, externalIP))

		computeInstances.err = nil
		computeInstances.object = computeInstance(privatev1.ComputeInstanceState_COMPUTE_INSTANCE_STATE_RUNNING, "192.0.2.10")
		attachments.listErr = grpcstatus.Error(grpccodes.Unavailable, "temporary ExternalIPAttachment lookup failure")
		expectPromptRetry(reconciler.run(ctx, externalIP))
	})

	It("does not schedule retries for terminal lookup errors", func() {
		computeInstances.err = grpcstatus.Error(grpccodes.NotFound, "ComputeInstance was deleted")

		err := reconciler.run(ctx, computeExternalIP(privatev1.ExternalIPState_EXTERNAL_IP_STATE_ALLOCATED))
		Expect(err).To(MatchError(ContainSubstring("ComputeInstance was deleted")))
		var retryable interface{ RequeueAfter() time.Duration }
		Expect(errors.As(err, &retryable)).To(BeFalse())
	})

	It("retries an uncertain Create through the shared worker without creating a duplicate", func() {
		computeInstances.object = computeInstance(privatev1.ComputeInstanceState_COMPUTE_INSTANCE_STATE_RUNNING, "192.0.2.10")
		externalIP := computeExternalIP(privatev1.ExternalIPState_EXTERNAL_IP_STATE_ALLOCATED)
		attachments.createErr = grpcstatus.Error(grpccodes.Unavailable, "temporary Create failure")
		attachments.persistBeforeCreateError = true

		service := &deferredAttachmentRetryServer{
			externalIP: externalIP,
			events:     make(chan *privatev1.Event, 1),
		}
		listener := bufconn.Listen(1024 * 1024)
		grpcServer := grpc.NewServer()
		privatev1.RegisterExternalIPsServer(grpcServer, service)
		privatev1.RegisterEventsServer(grpcServer, service)
		go func() {
			_ = grpcServer.Serve(listener)
		}()
		DeferCleanup(grpcServer.Stop)

		connection, err := grpc.NewClient(
			"passthrough:///auto-eipa-retry",
			grpc.WithContextDialer(func(context.Context, string) (net.Conn, error) { return listener.Dial() }),
			grpc.WithTransportCredentials(insecure.NewCredentials()),
		)
		Expect(err).ToNot(HaveOccurred())
		DeferCleanup(connection.Close)

		var attempts atomic.Int32
		sharedReconciler, err := controllers.NewReconciler[*privatev1.ExternalIP]().
			SetLogger(slog.New(slog.NewTextHandler(io.Discard, nil))).
			SetName("auto-eipa-retry-test").
			SetClient(connection).
			SetSync(false).
			SetWatchInterval(10 * time.Millisecond).
			SetFunction(func(ctx context.Context, object *privatev1.ExternalIP) error {
				attempts.Add(1)
				return reconciler.run(ctx, object)
			}).
			Build()
		Expect(err).ToNot(HaveOccurred())

		controllerContext, cancelController := context.WithCancel(context.Background())
		controllerDone := make(chan error, 1)
		go func() {
			controllerDone <- sharedReconciler.Start(controllerContext)
		}()
		DeferCleanup(func() {
			cancelController()
			Eventually(controllerDone).Should(Receive(MatchError(context.Canceled)))
		})

		Eventually(service.watchCalls.Load).Should(BeNumerically(">=", 1))
		service.events <- privatev1.Event_builder{ExternalIp: externalIP}.Build()
		Eventually(func() int {
			_, persisted := attachments.counts()
			return persisted
		}).WithTimeout(time.Second).Should(Equal(1))
		Expect(attempts.Load()).To(Equal(int32(1)))
		created, persisted := attachments.counts()
		Expect(created).To(Equal(1))
		Expect(persisted).To(Equal(1))

		Eventually(func() int32 { return attempts.Load() }).
			WithTimeout(3 * time.Second).
			WithPolling(10 * time.Millisecond).
			Should(Equal(int32(2)))
		created, persisted = attachments.counts()
		Expect(created).To(Equal(1))
		Expect(persisted).To(Equal(1))
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

func expectPromptRetry(err error) {
	var retryable interface{ RequeueAfter() time.Duration }
	Expect(errors.As(err, &retryable)).To(BeTrue(), "the error should request a retry: %T %v", err, err)
	Expect(retryable.RequeueAfter()).To(Equal(time.Second))
	var policy interface{ UseExponentialBackoff() bool }
	Expect(errors.As(err, &policy)).To(BeTrue())
	Expect(policy.UseExponentialBackoff()).To(BeTrue())
}

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

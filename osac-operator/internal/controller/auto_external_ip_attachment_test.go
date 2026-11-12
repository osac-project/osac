/*
Copyright 2026.

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

package controller

import (
	"context"
	"errors"
	"strconv"
	"strings"
	"testing"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	bmfov1alpha1 "github.com/osac-project/osac/bare-metal-fulfillment-operator/api/v1alpha1"
	"github.com/osac-project/osac/osac-operator/api/v1alpha1"
	"github.com/osac-project/osac/osac-operator/internal/networktask"
	privatev1 "github.com/osac-project/osac/proto/gen/osac/private/v1"
)

type testExternalIPAttachmentsClient struct {
	attachments []*privatev1.ExternalIPAttachment
	created     []*privatev1.ExternalIPAttachment
	listCalls   int
	createErr   error
}

func (c *testExternalIPAttachmentsClient) List(_ context.Context, request *privatev1.ExternalIPAttachmentsListRequest, _ ...grpc.CallOption) (*privatev1.ExternalIPAttachmentsListResponse, error) {
	c.listCalls++
	items := make([]*privatev1.ExternalIPAttachment, 0, len(c.attachments))
	for _, attachment := range c.attachments {
		if strings.Contains(request.GetFilter(), strconv.Quote(attachment.GetSpec().GetExternalIp().GetId())) {
			items = append(items, attachment)
		}
	}
	return &privatev1.ExternalIPAttachmentsListResponse{Items: items}, nil
}

func (c *testExternalIPAttachmentsClient) Create(_ context.Context, request *privatev1.ExternalIPAttachmentsCreateRequest, _ ...grpc.CallOption) (*privatev1.ExternalIPAttachmentsCreateResponse, error) {
	attachment := request.GetObject()
	c.created = append(c.created, attachment)
	if c.createErr != nil {
		return nil, c.createErr
	}
	c.attachments = append(c.attachments, attachment)
	return &privatev1.ExternalIPAttachmentsCreateResponse{Object: attachment}, nil
}

type testBareMetalInstancesGetter struct {
	response *privatev1.BareMetalInstancesGetResponse
	err      error
	getCalls int
}

func (c *testBareMetalInstancesGetter) Get(context.Context, *privatev1.BareMetalInstancesGetRequest, ...grpc.CallOption) (*privatev1.BareMetalInstancesGetResponse, error) {
	c.getCalls++
	return c.response, c.err
}

type countingAutomaticExternalIPClient struct {
	client.Client
	listCalls int
}

func (c *countingAutomaticExternalIPClient) List(ctx context.Context, list client.ObjectList, opts ...client.ListOption) error {
	// Resolving the installation networking mode reads the NetworkClass; the counter proves that the attachment
	// lookup ran no other list.
	if _, modeLookup := list.(*v1alpha1.NetworkClassList); !modeLookup {
		c.listCalls++
	}
	return c.Client.List(ctx, list, opts...)
}

func TestReconcileAutomaticExternalIPAttachmentsWaitsForAllocation(t *testing.T) {
	target := testAutomaticAttachmentTarget(bareMetalInstanceOwner, "bmi-1", "tenant-1")
	c := testExternalIPAttachmentsClient{}
	result, err := reconcileAutomaticExternalIPAttachments(context.Background(),
		testAutomaticExternalIPClient(t, testAutomaticExternalIP("eip-1", target, "", "")),
		"networking", &c, time.Second, target)
	if err != nil {
		t.Fatalf("reconcile automatic attachment: %v", err)
	}
	if result.RequeueAfter != time.Second {
		t.Fatalf("RequeueAfter = %s, want %s", result.RequeueAfter, time.Second)
	}
	if len(c.created) != 0 {
		t.Fatalf("created %d attachments while the ExternalIP was pending", len(c.created))
	}
}

func TestReconcileAutomaticExternalIPAttachmentsReusesClusterLookup(t *testing.T) {
	autoExternalIPAttachment := true
	cluster := privatev1.Cluster_builder{
		Metadata: privatev1.Metadata_builder{Tenant: "tenant-1"}.Build(),
		Spec:     privatev1.ClusterSpec_builder{AutoExternalIpAttachment: &autoExternalIPAttachment}.Build(),
	}.Build()
	clustersClient := &mockClustersClient{
		getResponse: privatev1.ClustersGetResponse_builder{Object: cluster}.Build(),
	}
	k8sClient := testAutomaticExternalIPClient(t)
	attachmentsClient := &testExternalIPAttachmentsClient{}
	reconciler := &ClusterOrderReconciler{
		Client:                      k8sClient,
		Engine:                      networktask.New(k8sClient, k8sClient, nil, networktask.Config{ProvisioningMode: networktask.StaticMode(true)}),
		ClustersClient:              clustersClient,
		ExternalIPAttachmentsClient: attachmentsClient,
		NetworkingNamespace:         "networking",
		StatusPollInterval:          time.Second,
	}
	instance := &v1alpha1.ClusterOrder{ObjectMeta: metav1.ObjectMeta{
		Labels: map[string]string{osacClusterOrderIDLabel: "cluster-1"},
	}}
	lookup := &fulfillmentClusterLookup{loaded: true, cluster: cluster}

	_, err := reconciler.reconcileAutomaticExternalIPAttachmentsWithLookup(context.Background(), instance, lookup)
	if err != nil {
		t.Fatalf("reconcile automatic attachments: %v", err)
	}
	if clustersClient.getCount != 0 {
		t.Fatalf("ClustersClient.Get called %d times despite a loaded reconcile lookup", clustersClient.getCount)
	}
}

func TestReconcileAutomaticExternalIPAttachmentsCreatesReadyBareMetalAttachment(t *testing.T) {
	target := testAutomaticAttachmentTarget(bareMetalInstanceOwner, "bmi-1", "tenant-1")
	c := testExternalIPAttachmentsClient{}
	result, err := reconcileAutomaticExternalIPAttachments(context.Background(),
		testAutomaticExternalIPClient(t, testAutomaticExternalIP("eip-1", target, "", "192.0.2.10")),
		"networking", &c, time.Second, target)
	if err != nil {
		t.Fatalf("reconcile automatic attachment: %v", err)
	}
	if result.RequeueAfter != 0 {
		t.Fatalf("RequeueAfter = %s, want no requeue after successful creation", result.RequeueAfter)
	}
	if len(c.created) != 1 {
		t.Fatalf("created %d attachments, want 1", len(c.created))
	}
	attachment := c.created[0]
	if got := attachment.GetSpec().GetExternalIp().GetId(); got != "eip-1" {
		t.Errorf("ExternalIP ID = %q, want eip-1", got)
	}
	if got := attachment.GetSpec().GetBaremetalInstance().GetId(); got != "bmi-1" {
		t.Errorf("BareMetalInstance ID = %q, want bmi-1", got)
	}
	if got := attachment.GetMetadata().GetTenant(); got != "tenant-1" {
		t.Errorf("tenant = %q, want tenant-1", got)
	}
	if got := attachment.GetMetadata().GetLabels()[autoCreatedLabel]; got != "true" {
		t.Errorf("auto-created label = %q, want true", got)
	}
	if got := attachment.GetMetadata().GetLabels()[autoCreatedForLabel]; got != "bmi-1" {
		t.Errorf("auto-created-for label = %q, want bmi-1", got)
	}
}

func TestReconcileAutomaticExternalIPAttachmentsCreatesClusterEndpoints(t *testing.T) {
	target := testAutomaticAttachmentTarget(clusterOrderOwner, "cluster-1", "tenant-1")
	target.endpoints = []string{autoCreatedEndpointAPI, autoCreatedEndpointIngress}
	target.availableEndpoints = map[string]bool{autoCreatedEndpointAPI: true, autoCreatedEndpointIngress: true}
	apiIP := testAutomaticExternalIP("eip-api", target, autoCreatedEndpointAPI, "192.0.2.10")
	ingressIP := testAutomaticExternalIP("eip-ingress", target, autoCreatedEndpointIngress, "192.0.2.11")
	c := testExternalIPAttachmentsClient{}
	_, err := reconcileAutomaticExternalIPAttachments(context.Background(), testAutomaticExternalIPClient(t, apiIP, ingressIP),
		"networking", &c, time.Second, target)
	if err != nil {
		t.Fatalf("reconcile automatic cluster attachments: %v", err)
	}
	if len(c.created) != 2 {
		t.Fatalf("created %d attachments, want 2", len(c.created))
	}
	got := map[string]privatev1.ExternalIPAttachmentEndpoint{}
	for _, attachment := range c.created {
		got[attachment.GetSpec().GetExternalIp().GetId()] = attachment.GetSpec().GetTargetEndpoint()
	}
	if got["eip-api"] != privatev1.ExternalIPAttachmentEndpoint_EXTERNAL_IP_ATTACHMENT_ENDPOINT_API {
		t.Errorf("API attachment endpoint = %s", got["eip-api"])
	}
	if got["eip-ingress"] != privatev1.ExternalIPAttachmentEndpoint_EXTERNAL_IP_ATTACHMENT_ENDPOINT_INGRESS {
		t.Errorf("ingress attachment endpoint = %s", got["eip-ingress"])
	}
}

func TestReconcileAutomaticExternalIPAttachmentsDoesNotCreateBeforeClusterEndpointIsReady(t *testing.T) {
	target := testAutomaticAttachmentTarget(clusterOrderOwner, "cluster-1", "tenant-1")
	target.endpoints = []string{autoCreatedEndpointAPI, autoCreatedEndpointIngress}
	target.availableEndpoints = map[string]bool{autoCreatedEndpointAPI: true}
	c := testExternalIPAttachmentsClient{}
	result, err := reconcileAutomaticExternalIPAttachments(context.Background(), testAutomaticExternalIPClient(t,
		testAutomaticExternalIP("eip-api", target, autoCreatedEndpointAPI, "192.0.2.10"),
		testAutomaticExternalIP("eip-ingress", target, autoCreatedEndpointIngress, "192.0.2.11")),
		"networking", &c, time.Second, target)
	if err != nil {
		t.Fatalf("reconcile automatic cluster attachments: %v", err)
	}
	if len(c.created) != 1 {
		t.Fatalf("created %d attachments, want only the ready API endpoint", len(c.created))
	}
	if result.RequeueAfter != time.Second {
		t.Fatalf("RequeueAfter = %s, want %s for the unavailable ingress endpoint", result.RequeueAfter, time.Second)
	}
}

func TestReconcileAutomaticExternalIPAttachmentsIsIdempotent(t *testing.T) {
	target := testAutomaticAttachmentTarget(bareMetalInstanceOwner, "bmi-1", "tenant-1")
	ip := testAutomaticExternalIP("eip-1", target, "", "192.0.2.10")
	existing := testExpectedAutomaticAttachment("eip-1", target, "")
	c := testExternalIPAttachmentsClient{attachments: []*privatev1.ExternalIPAttachment{existing}}
	_, err := reconcileAutomaticExternalIPAttachments(context.Background(), testAutomaticExternalIPClient(t, ip),
		"networking", &c, time.Second, target)
	if err != nil {
		t.Fatalf("reconcile existing automatic attachment: %v", err)
	}
	if len(c.created) != 0 {
		t.Fatalf("created %d duplicate attachments", len(c.created))
	}
}

func TestReconcileAutomaticExternalIPAttachmentsRetriesFulfillmentReadinessRace(t *testing.T) {
	target := testAutomaticAttachmentTarget(bareMetalInstanceOwner, "bmi-1", "tenant-1")
	c := testExternalIPAttachmentsClient{createErr: status.Error(codes.FailedPrecondition, "target is not ready yet")}
	result, err := reconcileAutomaticExternalIPAttachments(context.Background(),
		testAutomaticExternalIPClient(t, testAutomaticExternalIP("eip-1", target, "", "192.0.2.10")),
		"networking", &c, time.Second, target)
	if err != nil {
		t.Fatalf("reconcile readiness race: %v", err)
	}
	if result.RequeueAfter != time.Second {
		t.Fatalf("RequeueAfter = %s, want %s", result.RequeueAfter, time.Second)
	}
}

func TestReconcileAutomaticExternalIPAttachmentsWaitsForUnavailableEndpointOnlyWhileNeeded(t *testing.T) {
	target := testAutomaticAttachmentTarget(bareMetalInstanceOwner, "bmi-1", "tenant-1")
	target.availableEndpoints[""] = false

	failedIP := testAutomaticExternalIP("eip-1", target, "", "192.0.2.10")
	failedIP.Status.Conditions = []metav1.Condition{{Type: v1alpha1.NetworkConditionReady, Status: metav1.ConditionFalse,
		Reason: v1alpha1.ReasonFailed, Message: "pool exhausted", LastTransitionTime: metav1.Now()}}

	for _, tc := range []struct {
		name        string
		ip          *v1alpha1.ExternalIP
		attachments []*privatev1.ExternalIPAttachment
		wantRequeue bool
	}{
		{
			name:        "allocated IP has no attachment",
			ip:          testAutomaticExternalIP("eip-1", target, "", "192.0.2.10"),
			wantRequeue: true,
		},
		{
			name: "failed IP",
			ip:   failedIP,
		},
		{
			name:        "attachment already exists",
			ip:          testAutomaticExternalIP("eip-1", target, "", "192.0.2.10"),
			attachments: []*privatev1.ExternalIPAttachment{testExpectedAutomaticAttachment("eip-1", target, "")},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			attachmentsClient := &testExternalIPAttachmentsClient{attachments: tc.attachments}
			result, err := reconcileAutomaticExternalIPAttachments(context.Background(),
				testAutomaticExternalIPClient(t, tc.ip), "networking", attachmentsClient,
				automaticExternalAccessRecheck, target)
			if err != nil {
				t.Fatalf("reconcile unavailable endpoint: %v", err)
			}
			if tc.wantRequeue && result.RequeueAfter != automaticExternalAccessRecheck {
				t.Fatalf("RequeueAfter = %s, want %s", result.RequeueAfter, automaticExternalAccessRecheck)
			}
			if !tc.wantRequeue && result.RequeueAfter != 0 {
				t.Fatalf("RequeueAfter = %s, want no requeue", result.RequeueAfter)
			}
			if len(attachmentsClient.created) != 0 {
				t.Fatalf("created %d attachments for an unavailable endpoint", len(attachmentsClient.created))
			}
		})
	}
}

func TestBareMetalCleanupAutomaticExternalIPAttachmentRequiresReadyAlways(t *testing.T) {
	for _, tc := range []struct {
		name        string
		phase       bmfov1alpha1.BareMetalInstancePhaseType
		runStrategy bmfov1alpha1.BareMetalInstanceRunStrategy
		primaryIP   string
		autoAccess  bool
		deleting    bool
		wantGet     bool
		wantList    bool
		wantCreate  bool
		wantRequeue bool
	}{
		{name: "ready and always", phase: bmfov1alpha1.BareMetalInstancePhaseReady, runStrategy: bmfov1alpha1.RunStrategyAlways, primaryIP: "192.0.2.12", autoAccess: true, wantGet: true, wantList: true, wantCreate: true},
		{name: "halted", phase: bmfov1alpha1.BareMetalInstancePhaseReady, runStrategy: bmfov1alpha1.RunStrategyHalted, primaryIP: "192.0.2.12", autoAccess: true, wantGet: true, wantList: true, wantRequeue: true},
		{name: "unspecified run strategy", phase: bmfov1alpha1.BareMetalInstancePhaseReady, primaryIP: "192.0.2.12", autoAccess: true, wantGet: true, wantList: true, wantRequeue: true},
		{name: "progressing", phase: bmfov1alpha1.BareMetalInstancePhaseProgressing, runStrategy: bmfov1alpha1.RunStrategyAlways, primaryIP: "192.0.2.12", autoAccess: true, wantGet: true, wantList: true, wantRequeue: true},
		{name: "no primary address", phase: bmfov1alpha1.BareMetalInstancePhaseReady, runStrategy: bmfov1alpha1.RunStrategyAlways, autoAccess: true, wantGet: true, wantList: true, wantRequeue: true},
		{name: "failed BMI", phase: bmfov1alpha1.BareMetalInstancePhaseFailed, runStrategy: bmfov1alpha1.RunStrategyAlways, primaryIP: "192.0.2.12", autoAccess: true},
		{name: "deleting BMI", phase: bmfov1alpha1.BareMetalInstancePhaseReady, runStrategy: bmfov1alpha1.RunStrategyAlways, primaryIP: "192.0.2.12", autoAccess: true, deleting: true},
		{name: "automatic access not requested", phase: bmfov1alpha1.BareMetalInstancePhaseReady, runStrategy: bmfov1alpha1.RunStrategyAlways, primaryIP: "192.0.2.12", wantGet: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			target := testAutomaticAttachmentTarget(bareMetalInstanceOwner, "bmi-1", "tenant-1")
			autoAttach := tc.autoAccess
			instance := privatev1.BareMetalInstance_builder{
				Metadata: privatev1.Metadata_builder{Tenant: "tenant-1"}.Build(),
				Spec:     privatev1.BareMetalInstanceSpec_builder{AutoExternalIpAttachment: &autoAttach}.Build(),
			}.Build()
			instancesClient := &testBareMetalInstancesGetter{
				response: privatev1.BareMetalInstancesGetResponse_builder{Object: instance}.Build(),
			}
			attachmentsClient := &testExternalIPAttachmentsClient{}
			reconciler := NewBareMetalInstanceCleanupReconciler(
				testAutomaticExternalIPClient(t, testNetworkClass(true),
					testAutomaticExternalIP("eip-1", target, "", "192.0.2.10")),
				"bmi", "networking")
			reconciler.BareMetalInstancesClient = instancesClient
			reconciler.ExternalIPAttachmentsClient = attachmentsClient
			bmi := &bmfov1alpha1.BareMetalInstance{Status: bmfov1alpha1.BareMetalInstanceStatus{
				Phase:       tc.phase,
				RunStrategy: tc.runStrategy,
			}}
			if tc.primaryIP != "" {
				bmi.Status.NetworkAttachmentStatuses = []bmfov1alpha1.BareMetalNetworkAttachmentStatus{{
					Primary: true, IPAddress: tc.primaryIP,
				}}
			}
			if tc.deleting {
				deletionTimestamp := metav1.NewTime(time.Now())
				bmi.DeletionTimestamp = &deletionTimestamp
			}

			result, err := reconciler.reconcileAutomaticExternalIPAttachments(context.Background(), bmi, "bmi-1")
			if err != nil {
				t.Fatalf("reconcile automatic attachment: %v", err)
			}
			wantGet := 0
			if tc.wantGet {
				wantGet = 1
			}
			if instancesClient.getCalls != wantGet {
				t.Fatalf("Fulfillment Get calls = %d, want %d", instancesClient.getCalls, wantGet)
			}
			wantList := 0
			if tc.wantList {
				wantList = 1
			}
			if attachmentsClient.listCalls != wantList {
				t.Fatalf("Fulfillment attachment List calls = %d, want %d", attachmentsClient.listCalls, wantList)
			}
			if tc.wantRequeue && result.RequeueAfter != automaticExternalAccessRecheck {
				t.Fatalf("RequeueAfter = %s, want %s", result.RequeueAfter, automaticExternalAccessRecheck)
			}
			if !tc.wantRequeue && result.RequeueAfter != 0 {
				t.Fatalf("RequeueAfter = %s, want no requeue", result.RequeueAfter)
			}
			if tc.wantCreate {
				if len(attachmentsClient.created) != 1 {
					t.Fatalf("attachment creates = %d, want 1", len(attachmentsClient.created))
				}
				if got := attachmentsClient.created[0].GetMetadata().GetName(); got != "auto-eipa-eip-1" {
					t.Fatalf("automatic attachment name = %q, want auto-eipa-eip-1", got)
				}
				return
			}
			if len(attachmentsClient.created) != 0 {
				t.Fatalf("created %d attachments for an ineligible BMI", len(attachmentsClient.created))
			}
		})
	}
}

func TestBareMetalCleanupAutomaticExternalIPAttachmentWaitsForExternalIP(t *testing.T) {
	autoAttach := true
	instance := privatev1.BareMetalInstance_builder{
		Metadata: privatev1.Metadata_builder{Tenant: "tenant-1"}.Build(),
		Spec:     privatev1.BareMetalInstanceSpec_builder{AutoExternalIpAttachment: &autoAttach}.Build(),
	}.Build()
	instancesClient := &testBareMetalInstancesGetter{
		response: privatev1.BareMetalInstancesGetResponse_builder{Object: instance}.Build(),
	}
	attachmentsClient := &testExternalIPAttachmentsClient{}
	reconciler := NewBareMetalInstanceCleanupReconciler(testAutomaticExternalIPClient(t, testNetworkClass(true)), "bmi", "networking")
	reconciler.BareMetalInstancesClient = instancesClient
	reconciler.ExternalIPAttachmentsClient = attachmentsClient
	bmi := &bmfov1alpha1.BareMetalInstance{Status: bmfov1alpha1.BareMetalInstanceStatus{
		Phase:       bmfov1alpha1.BareMetalInstancePhaseReady,
		RunStrategy: bmfov1alpha1.RunStrategyAlways,
		NetworkAttachmentStatuses: []bmfov1alpha1.BareMetalNetworkAttachmentStatus{{
			Primary: true, IPAddress: "192.0.2.12",
		}},
	}}

	result, err := reconciler.reconcileAutomaticExternalIPAttachments(context.Background(), bmi, "bmi-1")
	if err != nil {
		t.Fatalf("reconcile before automatic ExternalIP appears: %v", err)
	}
	if instancesClient.getCalls != 1 {
		t.Fatalf("Fulfillment Get calls = %d, want 1", instancesClient.getCalls)
	}
	if attachmentsClient.listCalls != 0 || len(attachmentsClient.created) != 0 {
		t.Fatalf("Fulfillment attachment calls: lists=%d creates=%d, want none before ExternalIP appears",
			attachmentsClient.listCalls, len(attachmentsClient.created))
	}
	if result.RequeueAfter != automaticExternalAccessRecheck {
		t.Fatalf("RequeueAfter = %s, want %s", result.RequeueAfter, automaticExternalAccessRecheck)
	}
}

func TestBareMetalCleanupAutomaticExternalIPAttachmentReadinessRaceUsesThirtySeconds(t *testing.T) {
	for _, code := range []codes.Code{codes.FailedPrecondition, codes.NotFound} {
		t.Run(code.String(), func(t *testing.T) {
			target := testAutomaticAttachmentTarget(bareMetalInstanceOwner, "bmi-1", "tenant-1")
			autoAttach := true
			instance := privatev1.BareMetalInstance_builder{
				Metadata: privatev1.Metadata_builder{Tenant: "tenant-1"}.Build(),
				Spec:     privatev1.BareMetalInstanceSpec_builder{AutoExternalIpAttachment: &autoAttach}.Build(),
			}.Build()
			instancesClient := &testBareMetalInstancesGetter{
				response: privatev1.BareMetalInstancesGetResponse_builder{Object: instance}.Build(),
			}
			attachmentsClient := &testExternalIPAttachmentsClient{
				createErr: status.Error(code, "target is not ready yet"),
			}
			reconciler := NewBareMetalInstanceCleanupReconciler(
				testAutomaticExternalIPClient(t, testNetworkClass(true),
					testAutomaticExternalIP("eip-1", target, "", "192.0.2.10")),
				"bmi", "networking")
			reconciler.BareMetalInstancesClient = instancesClient
			reconciler.ExternalIPAttachmentsClient = attachmentsClient
			bmi := &bmfov1alpha1.BareMetalInstance{Status: bmfov1alpha1.BareMetalInstanceStatus{
				Phase:       bmfov1alpha1.BareMetalInstancePhaseReady,
				RunStrategy: bmfov1alpha1.RunStrategyAlways,
				NetworkAttachmentStatuses: []bmfov1alpha1.BareMetalNetworkAttachmentStatus{{
					Primary: true, IPAddress: "192.0.2.12",
				}},
			}}

			result, err := reconciler.reconcileAutomaticExternalIPAttachments(context.Background(), bmi, "bmi-1")
			if err != nil {
				t.Fatalf("readiness race should be retried without failing BMI reconcile: %v", err)
			}
			if result.RequeueAfter != automaticExternalAccessRecheck {
				t.Fatalf("RequeueAfter = %s, want %s", result.RequeueAfter, automaticExternalAccessRecheck)
			}
		})
	}
}

func TestReconcileAutomaticExternalIPAttachmentsReturnsUnexpectedCreateError(t *testing.T) {
	target := testAutomaticAttachmentTarget(bareMetalInstanceOwner, "bmi-1", "tenant-1")
	wantErr := errors.New("fulfillment unavailable")
	c := testExternalIPAttachmentsClient{createErr: wantErr}
	_, err := reconcileAutomaticExternalIPAttachments(context.Background(),
		testAutomaticExternalIPClient(t, testAutomaticExternalIP("eip-1", target, "", "192.0.2.10")),
		"networking", &c, time.Second, target)
	if !errors.Is(err, wantErr) {
		t.Fatalf("error = %v, want wrapped %v", err, wantErr)
	}
}

func TestReconcileAutomaticExternalIPAttachmentsStopsForFailedExternalIP(t *testing.T) {
	target := testAutomaticAttachmentTarget(bareMetalInstanceOwner, "bmi-1", "tenant-1")
	ip := testAutomaticExternalIP("eip-1", target, "", "")
	ip.Status.Conditions = []metav1.Condition{{Type: v1alpha1.NetworkConditionReady, Status: metav1.ConditionFalse,
		Reason: v1alpha1.ReasonFailed, Message: "pool exhausted", LastTransitionTime: metav1.Now()}}
	c := testExternalIPAttachmentsClient{}
	result, err := reconcileAutomaticExternalIPAttachments(context.Background(),
		testAutomaticExternalIPClient(t, ip), "networking", &c, time.Second, target)
	if err != nil {
		t.Fatalf("reconcile failed ExternalIP: %v", err)
	}
	if result.RequeueAfter != 0 || len(c.created) != 0 {
		t.Fatalf("a FAILED ExternalIP must stop the recheck: requeue %s, created %d", result.RequeueAfter, len(c.created))
	}
}

func TestDisabledProvisioningSkipsAutomaticExternalIPAttachmentLookups(t *testing.T) {
	t.Run("ClusterOrder", func(t *testing.T) {
		clustersClient := &mockClustersClient{}
		attachmentsClient := &testExternalIPAttachmentsClient{}
		k8sClient := &countingAutomaticExternalIPClient{Client: testAutomaticExternalIPClient(t)}
		reconciler := &ClusterOrderReconciler{
			Client:                      k8sClient,
			NetworkingNamespace:         "networking",
			ClustersClient:              clustersClient,
			ExternalIPAttachmentsClient: attachmentsClient,
		}
		order := &v1alpha1.ClusterOrder{
			ObjectMeta: metav1.ObjectMeta{Labels: map[string]string{osacClusterOrderIDLabel: "cluster-1"}},
			Status: v1alpha1.ClusterOrderStatus{
				Phase:           v1alpha1.ClusterOrderPhaseReady,
				ApiEndpoint:     "192.0.2.10",
				IngressEndpoint: "192.0.2.11",
			},
		}

		result, err := reconciler.reconcileAutomaticExternalIPAttachmentsWithLookup(
			context.Background(), order, nil,
		)
		if err != nil {
			t.Fatalf("reconcile automatic attachment with provisioning disabled: %v", err)
		}
		if result != (ctrl.Result{}) {
			t.Fatalf("result = %+v, want no requeue when provisioning is disabled", result)
		}
		if clustersClient.getCount != 0 {
			t.Errorf("Fulfillment Cluster Get calls = %d, want 0", clustersClient.getCount)
		}
		if k8sClient.listCalls != 0 {
			t.Errorf("Kubernetes list calls = %d, want 0", k8sClient.listCalls)
		}
		if attachmentsClient.listCalls != 0 || len(attachmentsClient.created) != 0 {
			t.Errorf("Fulfillment attachment calls: lists=%d creates=%d, want none",
				attachmentsClient.listCalls, len(attachmentsClient.created))
		}
	})

	t.Run("BareMetalInstance", func(t *testing.T) {
		instancesClient := &mockBareMetalInstancesClient{}
		attachmentsClient := &testExternalIPAttachmentsClient{}
		disabled := false
		class := &v1alpha1.NetworkClass{
			ObjectMeta: metav1.ObjectMeta{Name: "default", Namespace: "networking"},
			Spec:       v1alpha1.NetworkClassSpec{ProvisioningEnabled: &disabled},
		}
		k8sClient := &countingAutomaticExternalIPClient{Client: testAutomaticExternalIPClient(t, class)}
		reconciler := &BareMetalInstanceCleanupReconciler{
			Client:                      k8sClient,
			networkingNamespace:         "networking",
			BareMetalInstancesClient:    instancesClient,
			ExternalIPAttachmentsClient: attachmentsClient,
		}
		instance := &bmfov1alpha1.BareMetalInstance{
			Status: bmfov1alpha1.BareMetalInstanceStatus{
				Phase: bmfov1alpha1.BareMetalInstancePhaseReady,
				NetworkAttachmentStatuses: []bmfov1alpha1.BareMetalNetworkAttachmentStatus{{
					Primary: true, IPAddress: "192.0.2.12",
				}},
			},
		}

		result, err := reconciler.reconcileAutomaticExternalIPAttachments(context.Background(), instance, "bmi-1")
		if err != nil {
			t.Fatalf("reconcile automatic attachment with provisioning disabled: %v", err)
		}
		if result != (ctrl.Result{}) {
			t.Fatalf("result = %+v, want no requeue when provisioning is disabled", result)
		}
		if instancesClient.getCount != 0 {
			t.Errorf("Fulfillment BareMetalInstance Get calls = %d, want 0", instancesClient.getCount)
		}
		if k8sClient.listCalls != 0 {
			t.Errorf("Kubernetes list calls = %d, want 0", k8sClient.listCalls)
		}
		if attachmentsClient.listCalls != 0 || len(attachmentsClient.created) != 0 {
			t.Errorf("Fulfillment attachment calls: lists=%d creates=%d, want none",
				attachmentsClient.listCalls, len(attachmentsClient.created))
		}
	})
}

func testAutomaticAttachmentTarget(kind autoExternalIPOwnerKind, id, tenant string) automaticExternalIPAttachmentTarget {
	endpoint := ""
	if kind == clusterOrderOwner {
		endpoint = autoCreatedEndpointAPI
	}
	return automaticExternalIPAttachmentTarget{
		owner:              autoExternalIPOwner{kind: kind, id: id},
		kindLabel:          autoExternalIPKindLabel(kind),
		kind:               kind,
		tenant:             tenant,
		endpoints:          []string{endpoint},
		availableEndpoints: map[string]bool{endpoint: true},
	}
}

func testAutomaticExternalIP(name string, target automaticExternalIPAttachmentTarget, endpoint string, address string) *v1alpha1.ExternalIP {
	labels := map[string]string{
		autoCreatedLabel:     "true",
		autoCreatedForLabel:  target.owner.id,
		autoCreatedKindLabel: target.kindLabel,
		externalIPUUIDLabel:  name,
	}
	if endpoint != "" {
		labels[autoCreatedEndpointLabel] = endpoint
	}
	return &v1alpha1.ExternalIP{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: "networking",
			Labels:    labels,
			Annotations: map[string]string{
				tenantAnnotation: target.tenant,
			},
		},
		Spec:   v1alpha1.ExternalIPSpec{Pool: "pool"},
		Status: v1alpha1.ExternalIPStatus{Address: address, Conditions: allocatedConditions(address)},
	}
}

// allocatedConditions is Ready=True Succeeded for an allocated address (ALLOCATED).
func allocatedConditions(address string) []metav1.Condition {
	if address == "" {
		return nil
	}
	return []metav1.Condition{{Type: v1alpha1.NetworkConditionReady, Status: metav1.ConditionTrue,
		Reason: v1alpha1.ReasonSucceeded, LastTransitionTime: metav1.Now()}}
}

func testExpectedAutomaticAttachment(ipID string, target automaticExternalIPAttachmentTarget, endpoint string) *privatev1.ExternalIPAttachment {
	spec := &privatev1.ExternalIPAttachmentSpec_builder{
		ExternalIp: privatev1.ExternalIPLocalReference_builder{Id: ipID}.Build(),
	}
	switch target.kind {
	case clusterOrderOwner:
		spec.Cluster = privatev1.ClusterLocalReference_builder{Id: target.owner.id}.Build()
		switch endpoint {
		case autoCreatedEndpointAPI:
			spec.TargetEndpoint = privatev1.ExternalIPAttachmentEndpoint_EXTERNAL_IP_ATTACHMENT_ENDPOINT_API
		case autoCreatedEndpointIngress:
			spec.TargetEndpoint = privatev1.ExternalIPAttachmentEndpoint_EXTERNAL_IP_ATTACHMENT_ENDPOINT_INGRESS
		}
	case bareMetalInstanceOwner:
		spec.BaremetalInstance = privatev1.BareMetalInstanceLocalReference_builder{Id: target.owner.id}.Build()
	}
	labels := map[string]string{autoCreatedLabel: "true", autoCreatedForLabel: target.owner.id}
	if endpoint != "" {
		labels[autoCreatedEndpointLabel] = endpoint
	}
	return privatev1.ExternalIPAttachment_builder{
		Metadata: privatev1.Metadata_builder{
			Name:   "auto-eipa-" + ipID,
			Tenant: target.tenant,
			Labels: labels,
			Annotations: map[string]string{
				tenantAnnotation:         target.tenant,
				ownerReferenceAnnotation: target.owner.id,
			},
		}.Build(),
		Spec: spec.Build(),
	}.Build()
}

func testNetworkClass(enabled bool) *v1alpha1.NetworkClass {
	return &v1alpha1.NetworkClass{
		ObjectMeta: metav1.ObjectMeta{Name: "default", Namespace: "networking"},
		Spec:       v1alpha1.NetworkClassSpec{ProvisioningEnabled: &enabled},
	}
}

func testAutomaticExternalIPClient(t *testing.T, objects ...client.Object) client.Client {
	t.Helper()
	scheme := runtime.NewScheme()
	if err := v1alpha1.AddToScheme(scheme); err != nil {
		t.Fatal(err)
	}
	return fake.NewClientBuilder().WithScheme(scheme).WithObjects(objects...).Build()
}

// TestAutoExternalIPKindLabelMatchesFulfillment pins the kind label values the
// operator compares against. Fulfillment writes them on the automatic ExternalIP at
// private_baremetal_instances_server.go:1067 and private_clusters_server.go:1320;
// a different spelling makes the operator's list selector find no ExternalIP, so
// the automatic attachment is never found and the workload requeues forever.
func TestAutoExternalIPKindLabelMatchesFulfillment(t *testing.T) {
	if got := autoExternalIPKindLabel(bareMetalInstanceOwner); got != "bare_metal_instance" {
		t.Errorf("bare metal kind label = %q, want %q", got, "bare_metal_instance")
	}
	if got := autoExternalIPKindLabel(clusterOrderOwner); got != "cluster" {
		t.Errorf("cluster kind label = %q, want %q", got, "cluster")
	}
}

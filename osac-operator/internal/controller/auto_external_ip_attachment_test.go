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
	"google.golang.org/protobuf/proto"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	bmfov1alpha1 "github.com/osac-project/osac/bare-metal-fulfillment-operator/api/v1alpha1"
	"github.com/osac-project/osac/osac-operator/api/v1alpha1"
	privatev1 "github.com/osac-project/osac/proto/gen/osac/private/v1"
)

type testExternalIPAttachmentsClient struct {
	attachments []*privatev1.ExternalIPAttachment
	created     []*privatev1.ExternalIPAttachment
	createErr   error
}

type testClustersGetter struct {
	object *privatev1.Cluster
	err    error
}

func (c *testClustersGetter) Get(context.Context, *privatev1.ClustersGetRequest, ...grpc.CallOption) (*privatev1.ClustersGetResponse, error) {
	return &privatev1.ClustersGetResponse{Object: c.object}, c.err
}

type testBareMetalInstancesGetter struct {
	object *privatev1.BareMetalInstance
	err    error
}

func (c *testBareMetalInstancesGetter) Get(context.Context, *privatev1.BareMetalInstancesGetRequest, ...grpc.CallOption) (*privatev1.BareMetalInstancesGetResponse, error) {
	return &privatev1.BareMetalInstancesGetResponse{Object: c.object}, c.err
}

func (c *testExternalIPAttachmentsClient) List(_ context.Context, request *privatev1.ExternalIPAttachmentsListRequest, _ ...grpc.CallOption) (*privatev1.ExternalIPAttachmentsListResponse, error) {
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

func TestReconcileAutomaticExternalIPAttachmentsWaitsForAllocation(t *testing.T) {
	target := testAutomaticAttachmentTarget(bareMetalInstanceOwner, "bmi-1", "tenant-1")
	c := testExternalIPAttachmentsClient{}
	result, err := reconcileAutomaticExternalIPAttachments(context.Background(),
		testAutomaticExternalIPClient(t, testAutomaticExternalIP("eip-1", target, "", v1alpha1.ExternalIPStatePending, "")),
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

func TestReconcileAutomaticExternalIPAttachmentsCreatesReadyBareMetalAttachment(t *testing.T) {
	target := testAutomaticAttachmentTarget(bareMetalInstanceOwner, "bmi-1", "tenant-1")
	c := testExternalIPAttachmentsClient{}
	result, err := reconcileAutomaticExternalIPAttachments(context.Background(),
		testAutomaticExternalIPClient(t, testAutomaticExternalIP("eip-1", target, "", v1alpha1.ExternalIPStateAllocated, "192.0.2.10")),
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
	apiIP := testAutomaticExternalIP("eip-api", target, autoCreatedEndpointAPI, v1alpha1.ExternalIPStateAllocated, "192.0.2.10")
	ingressIP := testAutomaticExternalIP("eip-ingress", target, autoCreatedEndpointIngress, v1alpha1.ExternalIPStateAllocated, "192.0.2.11")
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
		testAutomaticExternalIP("eip-api", target, autoCreatedEndpointAPI, v1alpha1.ExternalIPStateAllocated, "192.0.2.10"),
		testAutomaticExternalIP("eip-ingress", target, autoCreatedEndpointIngress, v1alpha1.ExternalIPStateAllocated, "192.0.2.11")),
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
	ip := testAutomaticExternalIP("eip-1", target, "", v1alpha1.ExternalIPStateAllocated, "192.0.2.10")
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
		testAutomaticExternalIPClient(t, testAutomaticExternalIP("eip-1", target, "", v1alpha1.ExternalIPStateAllocated, "192.0.2.10")),
		"networking", &c, time.Second, target)
	if err != nil {
		t.Fatalf("reconcile readiness race: %v", err)
	}
	if result.RequeueAfter != time.Second {
		t.Fatalf("RequeueAfter = %s, want %s", result.RequeueAfter, time.Second)
	}
}

func TestReconcileAutomaticExternalIPAttachmentsReturnsUnexpectedCreateError(t *testing.T) {
	target := testAutomaticAttachmentTarget(bareMetalInstanceOwner, "bmi-1", "tenant-1")
	wantErr := errors.New("fulfillment unavailable")
	c := testExternalIPAttachmentsClient{createErr: wantErr}
	_, err := reconcileAutomaticExternalIPAttachments(context.Background(),
		testAutomaticExternalIPClient(t, testAutomaticExternalIP("eip-1", target, "", v1alpha1.ExternalIPStateAllocated, "192.0.2.10")),
		"networking", &c, time.Second, target)
	if !errors.Is(err, wantErr) {
		t.Fatalf("error = %v, want wrapped %v", err, wantErr)
	}
}

func TestClusterOrderRequestsAutomaticAttachmentsAfterCaaSIsReady(t *testing.T) {
	target := testAutomaticAttachmentTarget(clusterOrderOwner, "cluster-1", "tenant-1")
	target.endpoints = []string{autoCreatedEndpointAPI, autoCreatedEndpointIngress}
	target.availableEndpoints = map[string]bool{autoCreatedEndpointAPI: true, autoCreatedEndpointIngress: true}
	cluster := &privatev1.Cluster{}
	cluster.SetMetadata(privatev1.Metadata_builder{Tenant: "tenant-1"}.Build())
	cluster.SetSpec(privatev1.ClusterSpec_builder{AutoExternalIpAttachment: proto.Bool(true)}.Build())
	attachments := &testExternalIPAttachmentsClient{}
	reconciler := &ClusterOrderReconciler{
		Client: testAutomaticExternalIPClient(t,
			testAutomaticExternalIP("eip-api", target, autoCreatedEndpointAPI, v1alpha1.ExternalIPStateAllocated, "192.0.2.10"),
			testAutomaticExternalIP("eip-ingress", target, autoCreatedEndpointIngress, v1alpha1.ExternalIPStateAllocated, "192.0.2.11")),
		NetworkingNamespace:         "networking",
		StatusPollInterval:          time.Second,
		ClustersClient:              &testClustersGetter{object: cluster},
		ExternalIPAttachmentsClient: attachments,
	}
	order := &v1alpha1.ClusterOrder{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: "clusters",
			Labels:    map[string]string{osacClusterOrderIDLabel: "cluster-1"},
		},
		Status: v1alpha1.ClusterOrderStatus{
			Phase:           v1alpha1.ClusterOrderPhaseReady,
			ApiEndpoint:     "api.internal.example",
			IngressEndpoint: "ingress.internal.example",
		},
	}
	_, err := reconciler.reconcileAutomaticExternalIPAttachments(context.Background(), order)
	if err != nil {
		t.Fatalf("reconcile CaaS automatic attachments: %v", err)
	}
	if len(attachments.created) != 2 {
		t.Fatalf("created %d attachments, want API and ingress", len(attachments.created))
	}
}

func TestClusterOrderDoesNotRequestAutomaticAttachmentsBeforeCaaSIsReady(t *testing.T) {
	cluster := &privatev1.Cluster{}
	cluster.SetMetadata(privatev1.Metadata_builder{Tenant: "tenant-1"}.Build())
	cluster.SetSpec(privatev1.ClusterSpec_builder{AutoExternalIpAttachment: proto.Bool(true)}.Build())
	attachments := &testExternalIPAttachmentsClient{}
	reconciler := &ClusterOrderReconciler{
		NetworkingNamespace:         "networking",
		ClustersClient:              &testClustersGetter{object: cluster},
		ExternalIPAttachmentsClient: attachments,
	}
	order := &v1alpha1.ClusterOrder{Status: v1alpha1.ClusterOrderStatus{Phase: v1alpha1.ClusterOrderPhaseProgressing}}
	if _, err := reconciler.reconcileAutomaticExternalIPAttachments(context.Background(), order); err != nil {
		t.Fatalf("reconcile pre-ready CaaS: %v", err)
	}
	if len(attachments.created) != 0 {
		t.Fatalf("created %d attachments before CaaS became Ready", len(attachments.created))
	}
}

func TestBareMetalNetworkingReconcilerRequestsAutomaticAttachmentAfterBMIIsReady(t *testing.T) {
	target := testAutomaticAttachmentTarget(bareMetalInstanceOwner, "bmi-1", "tenant-1")
	instance := &privatev1.BareMetalInstance{}
	instance.SetMetadata(privatev1.Metadata_builder{Tenant: "tenant-1"}.Build())
	instance.SetSpec(privatev1.BareMetalInstanceSpec_builder{AutoExternalIpAttachment: proto.Bool(true)}.Build())
	attachments := &testExternalIPAttachmentsClient{}
	reconciler := &BareMetalInstanceCleanupReconciler{
		Client:                      testAutomaticExternalIPClient(t, testAutomaticExternalIP("eip-1", target, "", v1alpha1.ExternalIPStateAllocated, "192.0.2.10")),
		networkingNamespace:         "networking",
		BareMetalInstancesClient:    &testBareMetalInstancesGetter{object: instance},
		ExternalIPAttachmentsClient: attachments,
	}
	bmi := &bmfov1alpha1.BareMetalInstance{Status: bmfov1alpha1.BareMetalInstanceStatus{
		Phase:                     bmfov1alpha1.BareMetalInstancePhaseReady,
		NetworkAttachmentStatuses: []bmfov1alpha1.BareMetalNetworkAttachmentStatus{{Primary: true, IPAddress: "192.0.2.20"}},
	}}
	_, err := reconciler.reconcileAutomaticExternalIPAttachments(context.Background(), bmi, "bmi-1")
	if err != nil {
		t.Fatalf("reconcile BM automatic attachment: %v", err)
	}
	if len(attachments.created) != 1 {
		t.Fatalf("created %d attachments, want 1", len(attachments.created))
	}
	if got := attachments.created[0].GetSpec().GetBaremetalInstance().GetId(); got != "bmi-1" {
		t.Fatalf("BareMetalInstance target = %q, want bmi-1", got)
	}
}

func TestBareMetalNetworkingReconcilerWaitsUntilBMIHasPrimaryAddress(t *testing.T) {
	instance := &privatev1.BareMetalInstance{}
	instance.SetMetadata(privatev1.Metadata_builder{Tenant: "tenant-1"}.Build())
	instance.SetSpec(privatev1.BareMetalInstanceSpec_builder{AutoExternalIpAttachment: proto.Bool(true)}.Build())
	attachments := &testExternalIPAttachmentsClient{}
	reconciler := &BareMetalInstanceCleanupReconciler{
		BareMetalInstancesClient:    &testBareMetalInstancesGetter{object: instance},
		ExternalIPAttachmentsClient: attachments,
	}
	bmi := &bmfov1alpha1.BareMetalInstance{Status: bmfov1alpha1.BareMetalInstanceStatus{Phase: bmfov1alpha1.BareMetalInstancePhaseReady}}
	if _, err := reconciler.reconcileAutomaticExternalIPAttachments(context.Background(), bmi, "bmi-1"); err != nil {
		t.Fatalf("reconcile BM without primary address: %v", err)
	}
	if len(attachments.created) != 0 {
		t.Fatalf("created %d attachments before BMI had its primary address", len(attachments.created))
	}
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

func testAutomaticExternalIP(name string, target automaticExternalIPAttachmentTarget, endpoint string, state v1alpha1.ExternalIPStateType, address string) *v1alpha1.ExternalIP {
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
		Status: v1alpha1.ExternalIPStatus{State: state, Address: address},
	}
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

func testAutomaticExternalIPClient(t *testing.T, objects ...client.Object) client.Client {
	t.Helper()
	scheme := runtime.NewScheme()
	if err := v1alpha1.AddToScheme(scheme); err != nil {
		t.Fatal(err)
	}
	return fake.NewClientBuilder().WithScheme(scheme).WithObjects(objects...).Build()
}

/*
Copyright 2026.

Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except in compliance with the
License. You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on an
"AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
specific language governing permissions and limitations under the License.
*/

package controller

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"google.golang.org/grpc/codes"
	grpcstatus "google.golang.org/grpc/status"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/osac-project/osac/osac-operator/api/v1alpha1"
)

type recordingLogicalVolumeClient struct {
	objects     map[string]*unstructured.Unstructured
	created     []*unstructured.Unstructured
	deleted     []string
	createErr   error
	afterCreate func(*unstructured.Unstructured)
}

func newRecordingLogicalVolumeClient() *recordingLogicalVolumeClient {
	return &recordingLogicalVolumeClient{
		objects: make(map[string]*unstructured.Unstructured),
	}
}

func (c *recordingLogicalVolumeClient) Get(_ context.Context, key client.ObjectKey, obj client.Object, _ ...client.GetOption) error {
	volume, ok := c.objects[key.Name]
	if !ok {
		return apierrors.NewNotFound(schema.GroupResource{Group: logicalVolumeGroup, Resource: logicalVolumeResource}, key.Name)
	}
	result, ok := obj.(*unstructured.Unstructured)
	if !ok {
		return fmt.Errorf("expected unstructured LogicalVolume, got %T", obj)
	}
	result.Object = volume.DeepCopy().Object
	return nil
}

func (c *recordingLogicalVolumeClient) Create(_ context.Context, obj client.Object, _ ...client.CreateOption) error {
	volume, ok := obj.(*unstructured.Unstructured)
	if !ok {
		return fmt.Errorf("expected unstructured LogicalVolume, got %T", obj)
	}
	if c.createErr != nil {
		return c.createErr
	}

	if volume.GetName() == "" {
		volume.SetName(fmt.Sprintf("%s%02d", strings.TrimSuffix(volume.GetGenerateName(), "-"), len(c.created)+1))
	}
	if volume.GetUID() == "" {
		volume.SetUID(types.UID(fmt.Sprintf("logical-volume-uid-%02d", len(c.created)+1)))
	}
	if c.afterCreate != nil {
		c.afterCreate(volume)
	}
	created := volume.DeepCopy()
	c.objects[created.GetName()] = created
	c.created = append(c.created, created)
	return nil
}

func (c *recordingLogicalVolumeClient) Delete(_ context.Context, obj client.Object, _ ...client.DeleteOption) error {
	name := obj.GetName()
	if _, ok := c.objects[name]; !ok {
		return apierrors.NewNotFound(schema.GroupResource{Group: logicalVolumeGroup, Resource: logicalVolumeResource}, name)
	}
	delete(c.objects, name)
	c.deleted = append(c.deleted, name)
	return nil
}

func lvmsCreateRequest() VendorCreateVolumeRequest {
	return VendorCreateVolumeRequest{
		Name:       "volume-uid",
		UID:        "source-volume-uid",
		Provider:   lvmsProvider,
		Tenant:     "tenant-a",
		Tier:       "local",
		SizeGiB:    10,
		AccessMode: v1alpha1.VolumeAccessModeReadWriteOnce,
		Protocol:   v1alpha1.VolumeProtocolBlock,
		Topology: v1alpha1.VolumeTopology{Segments: map[string]string{
			nodeTopologySegment: "worker-1",
		}},
	}
}

func newTestLvmsProvisioner(t *testing.T, client *recordingLogicalVolumeClient) *LvmsVendorProvisioner {
	t.Helper()
	return NewLvmsVendorProvisioner(client)
}

func TestLvmsCreateVolumeBuildsAndWaitsForLogicalVolume(t *testing.T) {
	api := newRecordingLogicalVolumeClient()
	api.afterCreate = func(volume *unstructured.Unstructured) {
		_ = unstructured.SetNestedField(volume.Object, "lv-123", "status", "volumeID")
	}

	provisioner := newTestLvmsProvisioner(t, api)
	response, err := provisioner.CreateVolume(context.Background(), lvmsCreateRequest())
	if err != nil {
		t.Fatalf("CreateVolume error: %v", err)
	}
	if response.VendorVolumeID != "lv-123" {
		t.Fatalf("VendorVolumeID = %q, want lv-123", response.VendorVolumeID)
	}
	if response.Protocol != string(v1alpha1.VolumeProtocolBlock) {
		t.Fatalf("Protocol = %q, want %q", response.Protocol, v1alpha1.VolumeProtocolBlock)
	}
	if response.VendorContext[logicalVolumeSourceUIDContextKey] != "source-volume-uid" {
		t.Fatalf("VendorContext[%q] = %q, want source-volume-uid", logicalVolumeSourceUIDContextKey, response.VendorContext[logicalVolumeSourceUIDContextKey])
	}

	if len(api.created) != 1 {
		t.Fatalf("created %d LogicalVolumes, want 1", len(api.created))
	}
	volume := api.created[0]
	if got := volume.GetGenerateName(); got != "pvc-volume-uid-" {
		t.Errorf("generateName = %q, want pvc-volume-uid-", got)
	}
	if got := volume.GetLabels()[logicalVolumeUUIDLabel]; got != "source-volume-uid" {
		t.Errorf("volume UUID label = %q, want source-volume-uid", got)
	}
	if got := volume.GetLabels()[osacTenantKey]; got != "tenant-a" {
		t.Errorf("tenant label = %q, want tenant-a", got)
	}
	if got := volume.GetAnnotations()[osacTenantKey]; got != "tenant-a" {
		t.Errorf("tenant annotation = %q, want tenant-a", got)
	}
	if got := volume.GetAnnotations()[logicalVolumeOwnerAnnotation]; got != "volume-uid" {
		t.Errorf("owner-reference annotation = %q, want volume-uid", got)
	}
	if got := volume.GetAnnotations()[logicalVolumeSourceUIDAnnotation]; got != "source-volume-uid" {
		t.Errorf("source volume UID annotation = %q, want source-volume-uid", got)
	}
	spec, found, err := unstructured.NestedMap(volume.Object, "spec")
	if err != nil || !found {
		t.Fatalf("LogicalVolume spec missing: found=%t err=%v", found, err)
	}
	expectations := map[string]string{
		"name":        "pvc-volume-uid",
		"nodeName":    "worker-1",
		"deviceClass": "vg1",
		"size":        "10Gi",
	}
	for key, want := range expectations {
		if got := spec[key]; got != want {
			t.Errorf("spec.%s = %v, want %q", key, got, want)
		}
	}
	if got := response.VendorContext[logicalVolumeNameContextKey]; got != volume.GetName() {
		t.Errorf("VendorContext[%q] = %q, want generated name %q", logicalVolumeNameContextKey, got, volume.GetName())
	}
}

func TestMapLogicalVolumeToVolume(t *testing.T) {
	reconciler := &VolumeReconciler{VolumeNamespace: "osac-volumes"}
	volume := &unstructured.Unstructured{}
	volume.SetAnnotations(map[string]string{logicalVolumeOwnerAnnotation: "volume-name"})

	requests := reconciler.mapLogicalVolumeToVolume(context.Background(), volume)
	if len(requests) != 1 {
		t.Fatalf("mapped %d requests, want 1", len(requests))
	}
	if requests[0].Namespace != "osac-volumes" || requests[0].Name != "volume-name" {
		t.Fatalf("mapped request = %#v, want osac-volumes/volume-name", requests[0])
	}

	volume.SetAnnotations(nil)
	if requests := reconciler.mapLogicalVolumeToVolume(context.Background(), volume); len(requests) != 0 {
		t.Fatalf("mapped %d requests without owner annotation, want 0", len(requests))
	}
}

func TestLvmsCreateVolumeRequiresNodeTopology(t *testing.T) {
	api := newRecordingLogicalVolumeClient()
	provisioner := newTestLvmsProvisioner(t, api)
	req := lvmsCreateRequest()
	req.Topology.Segments = nil

	_, err := provisioner.CreateVolume(context.Background(), req)
	if grpcstatus.Code(err) != codes.FailedPrecondition {
		t.Fatalf("error code = %s, want FailedPrecondition: %v", grpcstatus.Code(err), err)
	}
	if len(api.created) != 0 {
		t.Fatalf("created %d LogicalVolumes for invalid request, want 0", len(api.created))
	}
}

func TestLvmsCreateVolumeResourceExhaustedRollsBack(t *testing.T) {
	api := newRecordingLogicalVolumeClient()
	api.afterCreate = func(volume *unstructured.Unstructured) {
		_ = unstructured.SetNestedField(volume.Object, int64(codes.ResourceExhausted), "status", "code")
		_ = unstructured.SetNestedField(volume.Object, "not enough space", "status", "message")
	}

	provisioner := newTestLvmsProvisioner(t, api)
	_, err := provisioner.CreateVolume(context.Background(), lvmsCreateRequest())
	if grpcstatus.Code(err) != codes.ResourceExhausted {
		t.Fatalf("error code = %s, want ResourceExhausted: %v", grpcstatus.Code(err), err)
	}
	if len(api.deleted) != 1 {
		t.Fatalf("deleted %d LogicalVolumes, want 1", len(api.deleted))
	}
	if len(api.objects) != 0 {
		t.Fatalf("%d LogicalVolumes remain after rollback, want 0", len(api.objects))
	}
}

func TestLvmsCreateVolumeReturnsPendingUntilReady(t *testing.T) {
	api := newRecordingLogicalVolumeClient()
	provisioner := newTestLvmsProvisioner(t, api)

	request := lvmsCreateRequest()
	response, err := provisioner.CreateVolume(context.Background(), request)
	if err != nil {
		t.Fatalf("initial CreateVolume error: %v", err)
	}
	if !response.Pending {
		t.Fatal("initial CreateVolume response is not pending")
	}
	if len(api.objects) != 1 {
		t.Fatalf("%d LogicalVolumes exist after initial create, want 1", len(api.objects))
	}
	name := response.VendorContext[logicalVolumeNameContextKey]
	_ = unstructured.SetNestedField(api.objects[name].Object, "lv-ready", "status", "volumeID")

	request.VendorContext = response.VendorContext
	response, err = provisioner.CreateVolume(context.Background(), request)
	if err != nil {
		t.Fatalf("resumed CreateVolume error: %v", err)
	}
	if response.Pending {
		t.Fatal("resumed CreateVolume response is still pending")
	}
	if response.VendorVolumeID != "lv-ready" {
		t.Fatalf("VendorVolumeID = %q, want lv-ready", response.VendorVolumeID)
	}
}

func TestLvmsCreateVolumeRejectsStaleSourceUIDContext(t *testing.T) {
	api := newRecordingLogicalVolumeClient()
	provisioner := newTestLvmsProvisioner(t, api)

	request := lvmsCreateRequest()
	response, err := provisioner.CreateVolume(context.Background(), request)
	if err != nil {
		t.Fatalf("initial CreateVolume error: %v", err)
	}

	request.VendorContext = response.VendorContext
	request.VendorContext[logicalVolumeSourceUIDContextKey] = "different-volume-uid"
	if _, err := provisioner.CreateVolume(context.Background(), request); err == nil {
		t.Fatal("expected stale source UID context error")
	}
}

func TestLvmsCreateVolumeImmediateRetryLeavesOneLogicalVolume(t *testing.T) {
	api := newRecordingLogicalVolumeClient()
	api.afterCreate = func(volume *unstructured.Unstructured) {
		if len(api.created) == 0 {
			_ = unstructured.SetNestedField(volume.Object, int64(codes.ResourceExhausted), "status", "code")
			_ = unstructured.SetNestedField(volume.Object, "not enough space", "status", "message")
			return
		}
		_ = unstructured.SetNestedField(volume.Object, "lv-retry", "status", "volumeID")
	}
	provisioner := newTestLvmsProvisioner(t, api)

	if _, err := provisioner.CreateVolume(context.Background(), lvmsCreateRequest()); grpcstatus.Code(err) != codes.ResourceExhausted {
		t.Fatalf("first CreateVolume error code = %s, want ResourceExhausted: %v", grpcstatus.Code(err), err)
	}
	response, err := provisioner.CreateVolume(context.Background(), lvmsCreateRequest())
	if err != nil {
		t.Fatalf("retry CreateVolume error: %v", err)
	}
	if response.VendorVolumeID != "lv-retry" {
		t.Fatalf("retry VendorVolumeID = %q, want lv-retry", response.VendorVolumeID)
	}
	if len(api.objects) != 1 {
		t.Fatalf("%d LogicalVolumes remain after retry, want 1", len(api.objects))
	}
	if len(api.created) != 2 || api.created[0].GetName() == api.created[1].GetName() {
		t.Fatalf("retry did not use a new generated name: %#v", api.created)
	}
}

func TestLvmsDeleteVolumeUsesGeneratedNameAndIsIdempotent(t *testing.T) {
	api := newRecordingLogicalVolumeClient()
	api.afterCreate = func(volume *unstructured.Unstructured) {
		_ = unstructured.SetNestedField(volume.Object, "lv-delete", "status", "volumeID")
	}
	provisioner := newTestLvmsProvisioner(t, api)
	response, err := provisioner.CreateVolume(context.Background(), lvmsCreateRequest())
	if err != nil {
		t.Fatalf("CreateVolume error: %v", err)
	}

	request := VendorDeleteVolumeRequest{
		Name:           "volume-uid",
		UID:            "source-volume-uid",
		VendorVolumeID: response.VendorVolumeID,
		Provider:       lvmsProvider,
		Tenant:         "tenant-a",
		VendorContext:  response.VendorContext,
	}
	if err := provisioner.DeleteVolume(context.Background(), request); err != nil {
		t.Fatalf("DeleteVolume error: %v", err)
	}
	if len(api.deleted) != 1 || api.deleted[0] != response.VendorContext[logicalVolumeNameContextKey] {
		t.Fatalf("deleted names = %v, want %q", api.deleted, response.VendorContext[logicalVolumeNameContextKey])
	}
	if err := provisioner.DeleteVolume(context.Background(), request); err != nil {
		t.Fatalf("idempotent DeleteVolume error: %v", err)
	}
}

func TestLvmsDeleteVolumeRequiresGeneratedName(t *testing.T) {
	provisioner := newTestLvmsProvisioner(t, newRecordingLogicalVolumeClient())
	if err := provisioner.DeleteVolume(context.Background(), VendorDeleteVolumeRequest{VendorVolumeID: "lv-1"}); err == nil {
		t.Fatal("expected missing generated LogicalVolume name error")
	}
}

func TestLvmsCreateVolumeRequiresTenant(t *testing.T) {
	provisioner := newTestLvmsProvisioner(t, newRecordingLogicalVolumeClient())
	request := lvmsCreateRequest()
	request.Tenant = ""
	if _, err := provisioner.CreateVolume(context.Background(), request); err == nil {
		t.Fatal("expected missing tenant error")
	}
}

func TestLvmsDeleteVolumeUsesUIDOwnershipWhenMetadataChanges(t *testing.T) {
	api := newRecordingLogicalVolumeClient()
	api.afterCreate = func(volume *unstructured.Unstructured) {
		_ = unstructured.SetNestedField(volume.Object, "lv-delete", "status", "volumeID")
	}
	provisioner := newTestLvmsProvisioner(t, api)
	response, err := provisioner.CreateVolume(context.Background(), lvmsCreateRequest())
	if err != nil {
		t.Fatalf("CreateVolume error: %v", err)
	}
	name := response.VendorContext[logicalVolumeNameContextKey]
	api.objects[name].SetLabels(map[string]string{logicalVolumeUUIDLabel: "different-volume"})
	api.objects[name].SetAnnotations(map[string]string{
		osacTenantKey:                "other-tenant",
		logicalVolumeOwnerAnnotation: "other-volume",
	})
	request := VendorDeleteVolumeRequest{
		Name:           "volume-uid",
		UID:            "source-volume-uid",
		VendorVolumeID: response.VendorVolumeID,
		Provider:       lvmsProvider,
		Tenant:         "other-tenant",
		VendorContext:  response.VendorContext,
	}
	if err := provisioner.DeleteVolume(context.Background(), request); err != nil {
		t.Fatalf("DeleteVolume error after metadata changes: %v", err)
	}
	if len(api.deleted) != 1 {
		t.Fatalf("deleted %d LogicalVolumes after metadata changes, want 1", len(api.deleted))
	}
}

func TestLvmsDeleteVolumeRejectsLogicalVolumeReplacement(t *testing.T) {
	api := newRecordingLogicalVolumeClient()
	api.afterCreate = func(volume *unstructured.Unstructured) {
		_ = unstructured.SetNestedField(volume.Object, "lv-delete", "status", "volumeID")
	}
	provisioner := newTestLvmsProvisioner(t, api)
	response, err := provisioner.CreateVolume(context.Background(), lvmsCreateRequest())
	if err != nil {
		t.Fatalf("CreateVolume error: %v", err)
	}

	name := response.VendorContext[logicalVolumeNameContextKey]
	api.objects[name].SetUID("replacement-uid")
	request := VendorDeleteVolumeRequest{
		Name:           "volume-uid",
		UID:            "source-volume-uid",
		VendorVolumeID: response.VendorVolumeID,
		Provider:       lvmsProvider,
		Tenant:         "tenant-a",
		VendorContext:  response.VendorContext,
	}
	if err := provisioner.DeleteVolume(context.Background(), request); err == nil {
		t.Fatal("expected replacement LogicalVolume error")
	}
	if len(api.deleted) != 0 {
		t.Fatalf("deleted %d LogicalVolumes after replacement detection, want 0", len(api.deleted))
	}
}

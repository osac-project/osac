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
	"time"

	"google.golang.org/grpc/codes"
	grpcstatus "google.golang.org/grpc/status"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	"github.com/osac-project/osac/osac-operator/api/v1alpha1"
)

type recordingLogicalVolumeClient struct {
	client.Client
	objects     map[string]*unstructured.Unstructured
	created     []*unstructured.Unstructured
	deleted     []string
	createErr   error
	afterCreate func(*unstructured.Unstructured)
}

func newRecordingLogicalVolumeClient() *recordingLogicalVolumeClient {
	return &recordingLogicalVolumeClient{
		Client:  fake.NewClientBuilder().Build(),
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
	return NewLvmsVendorProvisioner(
		client,
		WithLvmsPollInterval(time.Millisecond),
		WithLvmsPollTimeout(50*time.Millisecond),
	)
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
	if response.VendorContext[logicalVolumeIDContextKey] != "lv-123" {
		t.Fatalf("VendorContext[%q] = %q, want lv-123", logicalVolumeIDContextKey, response.VendorContext[logicalVolumeIDContextKey])
	}

	if len(api.created) != 1 {
		t.Fatalf("created %d LogicalVolumes, want 1", len(api.created))
	}
	volume := api.created[0]
	if got := volume.GetGenerateName(); got != "pvc-volume-uid-" {
		t.Errorf("generateName = %q, want pvc-volume-uid-", got)
	}
	if got := volume.GetLabels()[logicalVolumeUUIDLabel]; got != "volume-uid" {
		t.Errorf("volume UUID label = %q, want volume-uid", got)
	}
	if got := volume.GetLabels()[osacTenantKey]; got != "tenant-a" {
		t.Errorf("tenant label = %q, want tenant-a", got)
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

func TestLvmsCreateVolumeTimeoutCleansUp(t *testing.T) {
	api := newRecordingLogicalVolumeClient()
	provisioner := newTestLvmsProvisioner(t, api)

	_, err := provisioner.CreateVolume(context.Background(), lvmsCreateRequest())
	if err == nil {
		t.Fatal("expected readiness timeout")
	}
	if len(api.deleted) != 1 {
		t.Fatalf("deleted %d LogicalVolumes after timeout, want 1", len(api.deleted))
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

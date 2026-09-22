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
	"time"

	"google.golang.org/grpc/codes"
	grpcstatus "google.golang.org/grpc/status"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/osac-project/osac/osac-operator/api/v1alpha1"
)

const (
	lvmsProvider = "lvms"

	logicalVolumeGroup    = "topolvm.io"
	logicalVolumeVersion  = "v1"
	logicalVolumeKind     = "LogicalVolume"
	logicalVolumeResource = "logicalvolumes"

	nodeTopologySegment                = "osac.io/node"
	logicalVolumeUUIDLabel             = "osac.openshift.io/volume-uuid"
	logicalVolumeNameContextKey        = "osac.topolvm-logicalvolume-name"
	logicalVolumeSourceUIDContextKey   = "osac.topolvm-volume-uuid"
	logicalVolumeResourceUIDContextKey = "osac.topolvm-logicalvolume-uid"
	logicalVolumeOwnerAnnotation       = "osac.openshift.io/owner-reference"
	logicalVolumeSourceUIDAnnotation   = "osac.openshift.io/volume-uid"
	lvmsDeviceClass                    = "vg1"

	logicalVolumeCleanupTimeout = 10 * time.Second
)

var logicalVolumeGVK = schema.GroupVersionKind{
	Group:   logicalVolumeGroup,
	Version: logicalVolumeVersion,
	Kind:    logicalVolumeKind,
}

type logicalVolumeClient interface {
	Get(ctx context.Context, key client.ObjectKey, obj client.Object, opts ...client.GetOption) error
	Create(ctx context.Context, obj client.Object, opts ...client.CreateOption) error
	Delete(ctx context.Context, obj client.Object, opts ...client.DeleteOption) error
}

// LvmsVendorProvisioner provisions node-local volumes through TopoLVM's
// cluster-scoped LogicalVolume API.
type LvmsVendorProvisioner struct {
	client logicalVolumeClient
}

// NewLvmsVendorProvisioner creates an LVMS provisioner backed by a Kubernetes
// client that can manage topolvm.io/v1 LogicalVolume resources.
func NewLvmsVendorProvisioner(kubeClient logicalVolumeClient) *LvmsVendorProvisioner {
	return &LvmsVendorProvisioner{client: kubeClient}
}

// CreateVolume creates a LogicalVolume and checks whether TopoLVM has assigned
// its volumeID. If the CR is still pending, the LogicalVolume name is returned
// in VendorContext so the next reconcile can resume the operation without
// creating a duplicate resource or blocking a reconcile worker.
func (p *LvmsVendorProvisioner) CreateVolume(ctx context.Context, req VendorCreateVolumeRequest) (VendorCreateVolumeResponse, error) {
	if p.client == nil {
		return VendorCreateVolumeResponse{}, fmt.Errorf("LogicalVolume client is not configured")
	}
	if req.Provider != lvmsProvider {
		return VendorCreateVolumeResponse{}, fmt.Errorf("LVMS provisioner cannot handle provider %q", req.Provider)
	}
	if req.Protocol != v1alpha1.VolumeProtocolBlock {
		return VendorCreateVolumeResponse{}, fmt.Errorf("LVMS provisioner supports only block volumes; protocol %q is not implemented", req.Protocol)
	}
	if req.Name == "" {
		return VendorCreateVolumeResponse{}, fmt.Errorf("volume name is required")
	}
	if req.UID == "" {
		return VendorCreateVolumeResponse{}, fmt.Errorf("volume UID is required")
	}
	if req.Tenant == "" {
		return VendorCreateVolumeResponse{}, fmt.Errorf("tenant is required")
	}
	if req.SizeGiB <= 0 {
		return VendorCreateVolumeResponse{}, fmt.Errorf("volume size must be greater than zero")
	}
	nodeName := req.Topology.Segments[nodeTopologySegment]
	if nodeName == "" {
		return VendorCreateVolumeResponse{}, grpcstatus.Error(codes.FailedPrecondition,
			`node-local volume requires topology.segments["osac.io/node"]`)
	}

	generatedName := req.VendorContext[logicalVolumeNameContextKey]
	logicalVolumeUID := req.VendorContext[logicalVolumeResourceUIDContextKey]
	volume := &unstructured.Unstructured{}
	volume.SetGroupVersionKind(logicalVolumeGVK)
	if generatedName == "" {
		volume = buildLogicalVolume(req, nodeName)
		generatedName = volume.GetName()
		if err := p.client.Create(ctx, volume); err != nil {
			if !apierrors.IsAlreadyExists(err) {
				return VendorCreateVolumeResponse{}, fmt.Errorf("create LogicalVolume: %w", err)
			}

			// Another reconcile may have created the deterministic resource first.
			// Adopt it only when its immutable ownership metadata identifies this
			// OSAC Volume; never treat an arbitrary pre-existing object as ours.
			volume = &unstructured.Unstructured{}
			volume.SetGroupVersionKind(logicalVolumeGVK)
			volume.SetName(generatedName)
			if getErr := p.client.Get(ctx, client.ObjectKey{Name: generatedName}, volume); getErr != nil {
				return VendorCreateVolumeResponse{}, fmt.Errorf("get existing LogicalVolume %q after create conflict: %w", generatedName, getErr)
			}
			if ownershipErr := validateLogicalVolumeOwnership(volume, req); ownershipErr != nil {
				return VendorCreateVolumeResponse{}, ownershipErr
			}
		}
		generatedName = volume.GetName()
		if generatedName == "" {
			return VendorCreateVolumeResponse{}, fmt.Errorf("created LogicalVolume did not receive a name")
		}
		logicalVolumeUID = string(volume.GetUID())
		if logicalVolumeUID == "" {
			err := fmt.Errorf("created LogicalVolume %q did not receive a UID", generatedName)
			return VendorCreateVolumeResponse{}, p.cleanupAfterCreateFailure(ctx, generatedName, logicalVolumeUID, err)
		}
	} else {
		if sourceVolumeUID := req.VendorContext[logicalVolumeSourceUIDContextKey]; sourceVolumeUID != req.UID {
			return VendorCreateVolumeResponse{}, fmt.Errorf("vendor context does not match LogicalVolume owner identity")
		}
		if logicalVolumeUID == "" {
			return VendorCreateVolumeResponse{}, fmt.Errorf("vendor context is missing LogicalVolume UID")
		}
		var err error
		volume, err = p.getLogicalVolumeByUID(ctx, generatedName, logicalVolumeUID)
		if err != nil {
			return VendorCreateVolumeResponse{}, fmt.Errorf("get LogicalVolume %q: %w", generatedName, err)
		}
		if err := validateLogicalVolumeOwnership(volume, req); err != nil {
			return VendorCreateVolumeResponse{}, err
		}
	}

	vendorContext := logicalVolumeVendorContext(req.UID, generatedName, logicalVolumeUID)
	volumeID, ready, err := logicalVolumeVolumeID(volume)
	if err != nil {
		return VendorCreateVolumeResponse{}, p.cleanupAfterCreateFailure(ctx, generatedName, logicalVolumeUID, err)
	}
	if !ready {
		return VendorCreateVolumeResponse{
			Protocol:      string(v1alpha1.VolumeProtocolBlock),
			VendorContext: vendorContext,
			Pending:       true,
		}, nil
	}

	return VendorCreateVolumeResponse{
		VendorVolumeID: volumeID,
		Protocol:       string(v1alpha1.VolumeProtocolBlock),
		VendorContext:  vendorContext,
	}, nil
}

// DeleteVolume deletes the generated LogicalVolume name recorded during
// creation. A missing object is already deleted and is therefore successful.
func (p *LvmsVendorProvisioner) DeleteVolume(ctx context.Context, req VendorDeleteVolumeRequest) error {
	if p.client == nil {
		return fmt.Errorf("LogicalVolume client is not configured")
	}
	if req.Provider != lvmsProvider {
		return fmt.Errorf("LVMS provisioner cannot handle provider %q", req.Provider)
	}
	if req.Name == "" {
		return fmt.Errorf("volume name is required")
	}
	if req.UID == "" {
		return fmt.Errorf("volume UID is required")
	}
	if req.Tenant == "" {
		return fmt.Errorf("tenant is required")
	}
	generatedName := req.VendorContext[logicalVolumeNameContextKey]
	if generatedName == "" {
		return fmt.Errorf("vendor context is missing generated LogicalVolume name")
	}
	sourceVolumeUID := req.VendorContext[logicalVolumeSourceUIDContextKey]
	if sourceVolumeUID == "" || sourceVolumeUID != req.UID {
		return fmt.Errorf("vendor context does not match LogicalVolume owner identity")
	}
	logicalVolumeUID := req.VendorContext[logicalVolumeResourceUIDContextKey]
	if logicalVolumeUID == "" {
		return fmt.Errorf("vendor context is missing LogicalVolume UID")
	}

	volume, err := p.getLogicalVolumeByUID(ctx, generatedName, logicalVolumeUID)
	if err != nil {
		if apierrors.IsNotFound(err) {
			return nil
		}
		return fmt.Errorf("refusing to delete LogicalVolume %q: %w", generatedName, err)
	}
	if err := p.deleteOwnedLogicalVolume(ctx, volume, req.VendorVolumeID); err != nil {
		return fmt.Errorf("delete LogicalVolume %q: %w", generatedName, err)
	}
	return nil
}

func buildLogicalVolume(req VendorCreateVolumeRequest, nodeName string) *unstructured.Unstructured {
	volumeName := "pvc-" + req.Name
	labels := map[string]string{
		logicalVolumeUUIDLabel: req.UID,
	}
	labels[osacTenantKey] = req.Tenant

	volume := &unstructured.Unstructured{Object: map[string]interface{}{
		"apiVersion": logicalVolumeGroup + "/" + logicalVolumeVersion,
		"kind":       logicalVolumeKind,
		"metadata": map[string]interface{}{
			"name": logicalVolumeResourceName(req),
		},
		"spec": map[string]interface{}{
			"name":        volumeName,
			"nodeName":    nodeName,
			"deviceClass": lvmsDeviceClass,
			"size":        fmt.Sprintf("%dGi", req.SizeGiB),
		},
	}}
	volume.SetGroupVersionKind(logicalVolumeGVK)
	volume.SetLabels(labels)
	volume.SetAnnotations(map[string]string{
		osacTenantKey:                    req.Tenant,
		logicalVolumeOwnerAnnotation:     req.Name,
		logicalVolumeSourceUIDAnnotation: req.UID,
	})
	return volume
}

func logicalVolumeResourceName(req VendorCreateVolumeRequest) string {
	return "pvc-" + req.UID
}

func validateLogicalVolumeOwnership(volume *unstructured.Unstructured, req VendorCreateVolumeRequest) error {
	labels := volume.GetLabels()
	annotations := volume.GetAnnotations()
	checks := []struct {
		field string
		got   string
		want  string
	}{
		{field: "label " + logicalVolumeUUIDLabel, got: labels[logicalVolumeUUIDLabel], want: req.UID},
		{field: "label " + osacTenantKey, got: labels[osacTenantKey], want: req.Tenant},
		{field: "annotation " + logicalVolumeSourceUIDAnnotation, got: annotations[logicalVolumeSourceUIDAnnotation], want: req.UID},
		{field: "annotation " + logicalVolumeOwnerAnnotation, got: annotations[logicalVolumeOwnerAnnotation], want: req.Name},
		{field: "annotation " + osacTenantKey, got: annotations[osacTenantKey], want: req.Tenant},
	}
	for _, check := range checks {
		if check.got != check.want {
			return fmt.Errorf("LogicalVolume %q ownership mismatch: %s = %q, want %q", volume.GetName(), check.field, check.got, check.want)
		}
	}
	return nil
}

func logicalVolumeVendorContext(sourceVolumeUID, generatedName, logicalVolumeUID string) map[string]string {
	return map[string]string{
		logicalVolumeNameContextKey:        generatedName,
		logicalVolumeSourceUIDContextKey:   sourceVolumeUID,
		logicalVolumeResourceUIDContextKey: logicalVolumeUID,
	}
}

// getLogicalVolumeByUID authorizes access to the previously created resource
// using the immutable Kubernetes UID persisted in VendorContext. Labels and
// annotations on the LogicalVolume are traceability metadata, not ownership
// credentials.
func (p *LvmsVendorProvisioner) getLogicalVolumeByUID(ctx context.Context, name, logicalVolumeUID string) (*unstructured.Unstructured, error) {
	if logicalVolumeUID == "" {
		return nil, fmt.Errorf("LogicalVolume UID is required")
	}
	volume := &unstructured.Unstructured{}
	volume.SetGroupVersionKind(logicalVolumeGVK)
	volume.SetName(name)
	if err := p.client.Get(ctx, client.ObjectKey{Name: name}, volume); err != nil {
		return nil, err
	}
	if string(volume.GetUID()) != logicalVolumeUID {
		return nil, fmt.Errorf("LogicalVolume UID does not match persisted resource UID")
	}
	return volume, nil
}

func (p *LvmsVendorProvisioner) deleteOwnedLogicalVolume(ctx context.Context, volume *unstructured.Unstructured, expectedVolumeID string) error {
	if expectedVolumeID != "" {
		volumeID, found, err := logicalVolumeVolumeID(volume)
		if err != nil {
			return err
		}
		if !found || volumeID != expectedVolumeID {
			return fmt.Errorf("status.volumeID does not match vendor volume ID")
		}
	}

	uid := volume.GetUID()
	if uid == "" {
		return fmt.Errorf("LogicalVolume UID is missing")
	}
	deleteOptions := []client.DeleteOption{client.Preconditions{UID: &uid}}
	if err := p.client.Delete(ctx, volume, deleteOptions...); err != nil && !apierrors.IsNotFound(err) {
		return err
	}
	return nil
}

func logicalVolumeVolumeID(volume *unstructured.Unstructured) (string, bool, error) {
	if code, message, failed := logicalVolumeStatusError(volume); failed {
		return "", false, grpcstatus.Error(code, message)
	}
	id, found, err := unstructured.NestedString(volume.Object, "status", "volumeID")
	if err != nil {
		return "", false, fmt.Errorf("read LogicalVolume %q status.volumeID: %w", volume.GetName(), err)
	}
	return id, found && id != "", nil
}

func logicalVolumeStatusError(volume *unstructured.Unstructured) (codes.Code, string, bool) {
	value, found, err := unstructured.NestedInt64(volume.Object, "status", "code")
	if err != nil || !found {
		return codes.OK, "", false
	}
	code := codes.Code(value)
	if code == codes.OK {
		return codes.OK, "", false
	}
	message, _, _ := unstructured.NestedString(volume.Object, "status", "message")
	if message == "" {
		message = fmt.Sprintf("LogicalVolume reported status code %s", code)
	}
	return code, message, true
}

func (p *LvmsVendorProvisioner) cleanupAfterCreateFailure(ctx context.Context, name, logicalVolumeUID string, createErr error) error {
	if logicalVolumeUID == "" {
		return createErr
	}
	cleanupCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), logicalVolumeCleanupTimeout)
	defer cancel()

	volume, getErr := p.getLogicalVolumeByUID(cleanupCtx, name, logicalVolumeUID)
	if getErr != nil {
		if apierrors.IsNotFound(getErr) {
			return createErr
		}
		return grpcstatus.Errorf(grpcstatus.Code(createErr), "%v; get LogicalVolume %q after failed create: %v", createErr, name, getErr)
	}
	cleanupErr := p.deleteOwnedLogicalVolume(cleanupCtx, volume, "")
	if cleanupErr == nil || apierrors.IsNotFound(cleanupErr) {
		return createErr
	}
	return grpcstatus.Errorf(grpcstatus.Code(createErr), "%v; delete LogicalVolume %q after failed create: %v", createErr, name, cleanupErr)
}

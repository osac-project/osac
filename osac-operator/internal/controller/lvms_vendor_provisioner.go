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
	"math"
	"strconv"
	"time"

	"google.golang.org/grpc/codes"
	grpcstatus "google.golang.org/grpc/status"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/util/wait"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/osac-project/osac/osac-operator/api/v1alpha1"
)

const (
	lvmsProvider = "lvms"

	logicalVolumeGroup    = "topolvm.io"
	logicalVolumeVersion  = "v1"
	logicalVolumeKind     = "LogicalVolume"
	logicalVolumeResource = "logicalvolumes"

	nodeTopologySegment         = "osac.io/node"
	logicalVolumeUUIDLabel      = "osac.openshift.io/volume-uuid"
	logicalVolumeNameContextKey = "osac.topolvm-logicalvolume-name"
	logicalVolumeIDContextKey   = "osac.topolvm-volume-id"
	lvmsDeviceClass             = "vg1"

	defaultLvmsPollInterval     = 2 * time.Second
	defaultLvmsPollTimeout      = 2 * time.Minute
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
	client       logicalVolumeClient
	pollInterval time.Duration
	pollTimeout  time.Duration
}

// LvmsProvisionerOption customizes an LvmsVendorProvisioner for a deployment
// or a test.
type LvmsProvisionerOption func(*LvmsVendorProvisioner)

// WithLvmsPollInterval sets the interval between LogicalVolume status reads.
func WithLvmsPollInterval(interval time.Duration) LvmsProvisionerOption {
	return func(provisioner *LvmsVendorProvisioner) {
		if interval > 0 {
			provisioner.pollInterval = interval
		}
	}
}

// WithLvmsPollTimeout sets the maximum time spent waiting for a LogicalVolume
// to receive status.volumeID.
func WithLvmsPollTimeout(timeout time.Duration) LvmsProvisionerOption {
	return func(provisioner *LvmsVendorProvisioner) {
		if timeout > 0 {
			provisioner.pollTimeout = timeout
		}
	}
}

// NewLvmsVendorProvisioner creates an LVMS provisioner backed by a Kubernetes
// client that can manage topolvm.io/v1 LogicalVolume resources.
func NewLvmsVendorProvisioner(kubeClient client.Client, opts ...LvmsProvisionerOption) *LvmsVendorProvisioner {
	provisioner := &LvmsVendorProvisioner{
		client:       kubeClient,
		pollInterval: defaultLvmsPollInterval,
		pollTimeout:  defaultLvmsPollTimeout,
	}
	for _, option := range opts {
		if option != nil {
			option(provisioner)
		}
	}
	return provisioner
}

// CreateVolume creates a LogicalVolume, waits for TopoLVM to assign its
// volumeID, and returns both the vendor ID and the generated CR name needed for
// later deletion.
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
	if req.SizeGiB <= 0 {
		return VendorCreateVolumeResponse{}, fmt.Errorf("volume size must be greater than zero")
	}
	nodeName := req.Topology.Segments[nodeTopologySegment]
	if nodeName == "" {
		return VendorCreateVolumeResponse{}, grpcstatus.Error(codes.FailedPrecondition,
			`node-local volume requires topology.segments["osac.io/node"]`)
	}

	volume := buildLogicalVolume(req, nodeName)
	if err := p.client.Create(ctx, volume); err != nil {
		return VendorCreateVolumeResponse{}, fmt.Errorf("create LogicalVolume: %w", err)
	}
	generatedName := volume.GetName()
	if generatedName == "" {
		return VendorCreateVolumeResponse{}, fmt.Errorf("created LogicalVolume did not receive a name")
	}

	volumeID, err := p.waitForLogicalVolume(ctx, generatedName)
	if err != nil {
		return VendorCreateVolumeResponse{}, p.cleanupAfterCreateFailure(ctx, generatedName, err)
	}

	return VendorCreateVolumeResponse{
		VendorVolumeID: volumeID,
		Protocol:       string(v1alpha1.VolumeProtocolBlock),
		VendorContext: map[string]string{
			logicalVolumeNameContextKey: generatedName,
			logicalVolumeIDContextKey:   volumeID,
		},
	}, nil
}

// DeleteVolume deletes the generated LogicalVolume name recorded during
// creation. A missing object is already deleted and is therefore successful.
func (p *LvmsVendorProvisioner) DeleteVolume(ctx context.Context, req VendorDeleteVolumeRequest) error {
	if p.client == nil {
		return fmt.Errorf("LogicalVolume client is not configured")
	}
	generatedName := req.VendorContext[logicalVolumeNameContextKey]
	if generatedName == "" {
		return fmt.Errorf("vendor context is missing generated LogicalVolume name")
	}

	volume := &unstructured.Unstructured{}
	volume.SetGroupVersionKind(logicalVolumeGVK)
	volume.SetName(generatedName)
	if err := p.client.Delete(ctx, volume); err != nil && !apierrors.IsNotFound(err) {
		return fmt.Errorf("delete LogicalVolume %q: %w", generatedName, err)
	}
	return nil
}

func buildLogicalVolume(req VendorCreateVolumeRequest, nodeName string) *unstructured.Unstructured {
	volumeName := "pvc-" + req.Name
	labels := map[string]string{
		logicalVolumeUUIDLabel: req.Name,
	}
	if req.Tenant != "" {
		labels[osacTenantKey] = req.Tenant
	}

	volume := &unstructured.Unstructured{Object: map[string]interface{}{
		"apiVersion": logicalVolumeGroup + "/" + logicalVolumeVersion,
		"kind":       logicalVolumeKind,
		"metadata": map[string]interface{}{
			"generateName": volumeName + "-",
			"labels":       stringMapToInterfaceMap(labels),
		},
		"spec": map[string]interface{}{
			"name":        volumeName,
			"nodeName":    nodeName,
			"deviceClass": lvmsDeviceClass,
			"size":        fmt.Sprintf("%dGi", req.SizeGiB),
		},
	}}
	volume.SetGroupVersionKind(logicalVolumeGVK)
	return volume
}

func stringMapToInterfaceMap(values map[string]string) map[string]interface{} {
	result := make(map[string]interface{}, len(values))
	for key, value := range values {
		result[key] = value
	}
	return result
}

func (p *LvmsVendorProvisioner) waitForLogicalVolume(ctx context.Context, name string) (string, error) {
	var volumeID string
	err := wait.PollUntilContextTimeout(ctx, p.pollInterval, p.pollTimeout, true,
		func(ctx context.Context) (bool, error) {
			volume := &unstructured.Unstructured{}
			volume.SetGroupVersionKind(logicalVolumeGVK)
			if err := p.client.Get(ctx, client.ObjectKey{Name: name}, volume); err != nil {
				return false, err
			}

			if code, message, failed := logicalVolumeStatusError(volume); failed {
				return false, grpcstatus.Error(code, message)
			}

			id, found, err := unstructured.NestedString(volume.Object, "status", "volumeID")
			if err != nil {
				return false, fmt.Errorf("read LogicalVolume %q status.volumeID: %w", name, err)
			}
			if found && id != "" {
				volumeID = id
				return true, nil
			}
			return false, nil
		})
	if err != nil {
		return "", fmt.Errorf("wait for LogicalVolume %q: %w", name, err)
	}
	return volumeID, nil
}

func logicalVolumeStatusError(volume *unstructured.Unstructured) (codes.Code, string, bool) {
	value, found, err := unstructured.NestedFieldNoCopy(volume.Object, "status", "code")
	if err != nil || !found {
		return codes.OK, "", false
	}
	code, ok := parseLogicalVolumeStatusCode(value)
	if !ok || code == codes.OK {
		return codes.OK, "", false
	}
	message, _, _ := unstructured.NestedString(volume.Object, "status", "message")
	if message == "" {
		message = fmt.Sprintf("LogicalVolume reported status code %s", code)
	}
	return code, message, true
}

func parseLogicalVolumeStatusCode(value interface{}) (codes.Code, bool) {
	switch value := value.(type) {
	case int:
		return codes.Code(value), true
	case int32:
		return codes.Code(value), true
	case int64:
		return codes.Code(value), true
	case float32:
		if value != float32(math.Trunc(float64(value))) {
			return codes.Unknown, false
		}
		return codes.Code(value), true
	case float64:
		if value != math.Trunc(value) {
			return codes.Unknown, false
		}
		return codes.Code(value), true
	case string:
		if numeric, err := strconv.Atoi(value); err == nil {
			return codes.Code(numeric), true
		}
		for code := codes.OK; code <= codes.Unauthenticated; code++ {
			if code.String() == value {
				return code, true
			}
		}
	}
	return codes.Unknown, false
}

func (p *LvmsVendorProvisioner) cleanupAfterCreateFailure(ctx context.Context, name string, createErr error) error {
	cleanupCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), logicalVolumeCleanupTimeout)
	defer cancel()

	volume := &unstructured.Unstructured{}
	volume.SetGroupVersionKind(logicalVolumeGVK)
	volume.SetName(name)
	cleanupErr := p.client.Delete(cleanupCtx, volume)
	if cleanupErr == nil || apierrors.IsNotFound(cleanupErr) {
		return createErr
	}
	return grpcstatus.Errorf(grpcstatus.Code(createErr), "%v; delete LogicalVolume %q after failed create: %v", createErr, name, cleanupErr)
}

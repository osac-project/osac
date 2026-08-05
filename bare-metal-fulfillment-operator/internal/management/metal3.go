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

package management

import (
	"context"
	"encoding/json"
	"fmt"

	metal3api "github.com/metal3-io/baremetal-operator/apis/metal3.io/v1alpha1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/osac-project/bare-metal-fulfillment-operator/internal/inventory"
)

var (
	_ Client        = (*Metal3Client)(nil)
	_ NewClientFunc = NewClientFunc(NewMetal3ManagementClient)
)

func init() {
	newClientFuncs["metal3"] = NewMetal3ManagementClient
}

// Metal3Client implements the management Client interface using BareMetalHost CRs.
type Metal3Client struct {
	client    client.Client
	namespace string
}

// NewMetal3ClientForTest creates a Metal3Client with an injected client for testing.
func NewMetal3ClientForTest(k8sClient client.Client, namespace string) *Metal3Client {
	return &Metal3Client{
		client:    k8sClient,
		namespace: namespace,
	}
}

// TestClient returns the underlying client for test assertions.
func (m *Metal3Client) TestClient() client.Client {
	return m.client
}

func NewMetal3ManagementClient(ctx context.Context, cfg *Config) (Client, error) {
	namespace, err := parseMetal3ManagementNamespace(cfg)
	if err != nil {
		return nil, err
	}

	scheme := runtime.NewScheme()
	if err := metal3api.AddToScheme(scheme); err != nil {
		return nil, fmt.Errorf("failed to add metal3 types to scheme: %w", err)
	}

	restConfig, err := ctrl.GetConfig()
	if err != nil {
		return nil, fmt.Errorf("failed to load kubernetes config: %w", err)
	}

	k8sClient, err := client.New(restConfig, client.Options{Scheme: scheme})
	if err != nil {
		return nil, fmt.Errorf("failed to create kubernetes client: %w", err)
	}

	return &Metal3Client{
		client:    k8sClient,
		namespace: namespace,
	}, nil
}

func parseMetal3ManagementNamespace(cfg *Config) (string, error) {
	metal3Opts, ok := cfg.Options["metal3"]
	if !ok {
		return "", fmt.Errorf("metal3 options not found in management config")
	}

	optsJSON, err := json.Marshal(metal3Opts)
	if err != nil {
		return "", fmt.Errorf("failed to marshal metal3 options: %w", err)
	}

	var opts struct {
		Namespace string `json:"namespace"`
	}
	if err := json.Unmarshal(optsJSON, &opts); err != nil {
		return "", fmt.Errorf("failed to unmarshal metal3 options: %w", err)
	}

	if opts.Namespace == "" {
		return "", fmt.Errorf("metal3 namespace is required in management config")
	}

	return opts.Namespace, nil
}

func (m *Metal3Client) GetPowerState(ctx context.Context, hostID string) (*PowerStatus, error) {
	bmh, err := m.getBMH(ctx, hostID)
	if err != nil {
		return nil, err
	}

	var state PowerState
	if bmh.Status.PoweredOn {
		state = PowerOn
	} else {
		state = PowerOff
	}

	isTransitioning := bmh.Spec.Online != bmh.Status.PoweredOn

	return &PowerStatus{
		State:           state,
		IsTransitioning: isTransitioning,
	}, nil
}

func (m *Metal3Client) SetPowerState(ctx context.Context, hostID string, target PowerState) error {
	bmh, err := m.getBMH(ctx, hostID)
	if err != nil {
		return err
	}

	if bmh.Spec.Online != bmh.Status.PoweredOn {
		return fmt.Errorf("node %s: %w", hostID, ErrTransitioning)
	}

	var desiredOnline bool
	switch target {
	case PowerOn:
		desiredOnline = true
	case PowerOff:
		desiredOnline = false
	default:
		return fmt.Errorf("node %s: invalid target power state %q", hostID, target)
	}

	if bmh.Spec.Online == desiredOnline {
		return nil
	}

	patch, err := json.Marshal(map[string]interface{}{
		"spec": map[string]interface{}{
			"online": desiredOnline,
		},
	})
	if err != nil {
		return fmt.Errorf("failed to build power-state patch for node %s: %w", hostID, err)
	}

	if err := m.client.Patch(ctx, bmh, client.RawPatch(types.MergePatchType, patch)); err != nil {
		return fmt.Errorf("failed to set power state on node %s: %w", hostID, err)
	}

	return nil
}

func (m *Metal3Client) TriggerRestart(ctx context.Context, hostID string) error {
	bmh, err := m.getBMH(ctx, hostID)
	if err != nil {
		return err
	}

	// Check if reboot is already in progress
	if _, hasRebootAnnotation := bmh.Annotations[metal3api.RebootAnnotationPrefix]; hasRebootAnnotation {
		return fmt.Errorf("host %s: %w", hostID, ErrTransitioning)
	}

	// Set reboot annotation to trigger restart
	original := bmh.DeepCopy()
	if bmh.Annotations == nil {
		bmh.Annotations = make(map[string]string)
	}
	bmh.Annotations[metal3api.RebootAnnotationPrefix] = ""

	if err := m.client.Patch(ctx, bmh, client.MergeFrom(original)); err != nil {
		return fmt.Errorf("failed to trigger restart for host %s: %w", hostID, err)
	}

	return nil
}

func (m *Metal3Client) IsRestartComplete(ctx context.Context, hostID string) (bool, error) {
	bmh, err := m.getBMH(ctx, hostID)
	if err != nil {
		return false, err
	}

	// Restart is complete when the reboot annotation is no longer present
	_, hasRebootAnnotation := bmh.Annotations[metal3api.RebootAnnotationPrefix]
	return !hasRebootAnnotation, nil
}

func (m *Metal3Client) getBMH(ctx context.Context, hostID string) (*metal3api.BareMetalHost, error) {
	namespace, name, err := inventory.ParseHostID(hostID)
	if err != nil {
		return nil, err
	}

	if namespace != m.namespace {
		return nil, fmt.Errorf("host %s is outside configured metal3 namespace %q", hostID, m.namespace)
	}

	bmh := &metal3api.BareMetalHost{}
	if err := m.client.Get(ctx, client.ObjectKey{Namespace: namespace, Name: name}, bmh); err != nil {
		return nil, fmt.Errorf("failed to get BareMetalHost %s: %w", hostID, err)
	}

	return bmh, nil
}

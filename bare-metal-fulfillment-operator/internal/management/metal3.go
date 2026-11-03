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

	"github.com/osac-project/osac/bare-metal-fulfillment-operator/internal/inventory"
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
	namespace, err := ParseMetal3ManagementNamespace(cfg)
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

func ParseMetal3ManagementNamespace(cfg *Config) (string, error) {
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
	failureMessage := metal3PowerFailureMessage(bmh)

	return &PowerStatus{
		State:                   state,
		IsTransitioning:         isTransitioning,
		FailureMessage:          failureMessage,
		FailureMessageIsCurrent: failureMessage != "",
	}, nil
}

func metal3PowerFailureMessage(bmh *metal3api.BareMetalHost) string {
	if bmh.Status.OperationalStatus != metal3api.OperationalStatusError ||
		bmh.Status.ErrorType != metal3api.PowerManagementError {
		return ""
	}
	if bmh.Status.ErrorMessage != "" {
		return bmh.Status.ErrorMessage
	}
	return "Metal3 reports a power-management error"
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

// Metal3 network-handoff annotation (design Create step 6). Distinct from the
// generic tenant reboot annotation used by TriggerRestart / RunStrategy.
const (
	Metal3NetworkHandoffAnnotationKey   = "reboot.metal3.io/osac-handoff"
	Metal3NetworkHandoffAnnotationValue = `{"mode":"hard"}`
)

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

	// Restart is complete when the reboot annotation has been processed and
	// the host is back online. Checking only the annotation is insufficient
	// because the BMH controller removes it as soon as the reboot command is
	// sent to Ironic, before the host finishes rebooting.
	_, hasRebootAnnotation := bmh.Annotations[metal3api.RebootAnnotationPrefix]
	return !hasRebootAnnotation && bmh.Status.PoweredOn, nil
}

func (m *Metal3Client) RequestNetworkHandoff(ctx context.Context, hostID string) error {
	bmh, err := m.getBMH(ctx, hostID)
	if err != nil {
		return err
	}
	if _, ok := bmh.Annotations[Metal3NetworkHandoffAnnotationKey]; ok && bmh.Spec.Online {
		return nil
	}
	original := bmh.DeepCopy()
	if bmh.Annotations == nil {
		bmh.Annotations = make(map[string]string)
	}
	bmh.Annotations[Metal3NetworkHandoffAnnotationKey] = Metal3NetworkHandoffAnnotationValue
	bmh.Spec.Online = true
	if err := m.client.Patch(ctx, bmh, client.MergeFrom(original)); err != nil {
		return fmt.Errorf("failed to request network handoff for host %s: %w", hostID, err)
	}
	return nil
}

func (m *Metal3Client) ObserveNetworkHandoff(ctx context.Context, hostID, _, _ string) (HandoffObservation, error) {
	bmh, err := m.getBMH(ctx, hostID)
	if err != nil {
		return HandoffObservation{}, err
	}
	if message := metal3PowerFailureMessage(bmh); message != "" {
		return HandoffObservation{Failed: true, Reason: "NetworkHandoffFailed", Message: message}, nil
	}
	_, hasHandoff := bmh.Annotations[Metal3NetworkHandoffAnnotationKey]
	switch {
	case hasHandoff && !bmh.Status.PoweredOn:
		// Metal3 keeps the host off while the annotation exists; remove it so
		// Metal3 powers the host back on (design Create step 6).
		original := bmh.DeepCopy()
		delete(bmh.Annotations, Metal3NetworkHandoffAnnotationKey)
		if err := m.client.Patch(ctx, bmh, client.MergeFrom(original)); err != nil {
			return HandoffObservation{}, fmt.Errorf("failed to clear handoff annotation on host %s: %w", hostID, err)
		}
		return HandoffObservation{
			Reason:  "Progressing",
			Message: "Removed handoff annotation; waiting for host to power on",
		}, nil
	case !hasHandoff && bmh.Status.PoweredOn:
		return HandoffObservation{
			Confirmed: true,
			Reason:    "Succeeded",
			Message:   "Host rebooted onto the tenant network",
		}, nil
	default:
		return HandoffObservation{
			Reason:  "Progressing",
			Message: "Waiting for network-handoff reboot confirmation",
		}, nil
	}
}

func (m *Metal3Client) ClearPendingNetworkHandoff(ctx context.Context, hostID string) error {
	bmh, err := m.getBMH(ctx, hostID)
	if err != nil {
		return err
	}
	_, hasHandoff := bmh.Annotations[Metal3NetworkHandoffAnnotationKey]
	if !bmh.Spec.Online && !hasHandoff {
		return nil
	}
	original := bmh.DeepCopy()
	if hasHandoff {
		delete(bmh.Annotations, Metal3NetworkHandoffAnnotationKey)
	}
	// The annotation holds a host off while spec.online remains true. Set the
	// desired state offline in the same patch, including if its observer already
	// removed the annotation but Metal3 has not completed the power-on transition.
	bmh.Spec.Online = false
	if err := m.client.Patch(ctx, bmh, client.MergeFrom(original)); err != nil {
		return fmt.Errorf("failed to prepare host %s for network offboard: %w", hostID, err)
	}
	return nil
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

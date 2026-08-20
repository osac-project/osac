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

package inventory

import (
	"context"
	"encoding/json"
	"fmt"
	"math/rand"
	"strings"

	metal3api "github.com/metal3-io/baremetal-operator/apis/metal3.io/v1alpha1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/discovery"
	"k8s.io/client-go/rest"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	ctrllog "sigs.k8s.io/controller-runtime/pkg/log"

	"github.com/osac-project/osac/bare-metal-fulfillment-operator/internal/shared"
)

var (
	_ Client        = (*Metal3Client)(nil)
	_ NewClientFunc = NewClientFunc(NewMetal3Client)
)

const (
	metal3LabelPrefix = shared.OsacPrefix + "/"

	Metal3HostTypeLabel  = metal3LabelPrefix + "host-type"
	Metal3ManagedByLabel = metal3LabelPrefix + "managed-by"
	Metal3PoolIDLabel    = metal3LabelPrefix + "pool-id"
)

func init() {
	newClientFuncs["metal3"] = NewMetal3Client
}

type Metal3Client struct {
	client    client.Client
	namespace string
	hostClass string
}

// NewMetal3ClientForTest creates a Metal3Client with an injected client for testing.
func NewMetal3ClientForTest(k8sClient client.Client, namespace, hostClass string) *Metal3Client {
	return &Metal3Client{
		client:    k8sClient,
		namespace: namespace,
		hostClass: hostClass,
	}
}

func NewMetal3Client(ctx context.Context, cfg *Config) (Client, error) {
	namespace, err := parseMetal3Namespace(cfg)
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

	if err := validateBareMetalHostCRD(restConfig); err != nil {
		return nil, err
	}

	k8sClient, err := client.New(restConfig, client.Options{Scheme: scheme})
	if err != nil {
		return nil, fmt.Errorf("failed to create kubernetes client: %w", err)
	}

	return &Metal3Client{
		client:    k8sClient,
		namespace: namespace,
		hostClass: cfg.HostClass,
	}, nil
}

func parseMetal3Namespace(cfg *Config) (string, error) {
	metal3Opts, ok := cfg.Options["metal3"]
	if !ok {
		return "", fmt.Errorf("metal3 options not found in config")
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
		return "", fmt.Errorf("metal3 namespace is required in config")
	}

	return opts.Namespace, nil
}

func validateBareMetalHostCRD(restConfig *rest.Config) error {
	dc, err := discovery.NewDiscoveryClientForConfig(restConfig)
	if err != nil {
		return fmt.Errorf("failed to create discovery client: %w", err)
	}
	_, err = dc.ServerResourcesForGroupVersion("metal3.io/v1alpha1")
	if err != nil {
		return fmt.Errorf("metal3 backend configured but BareMetalHost CRD is not installed: %w", err)
	}
	return nil
}

func (m *Metal3Client) FindFreeHost(ctx context.Context, matchExpressions map[string]string) (*Host, error) {
	log := ctrllog.FromContext(ctx)
	log.Info("Finding free Metal3 host", "namespace", m.namespace)

	listOpts := make([]client.ListOption, 0, 2)
	listOpts = append(listOpts, client.InNamespace(m.namespace))

	matchLabels := map[string]string{}
	if hostType, ok := matchExpressions["hostType"]; ok && hostType != "" {
		matchLabels[Metal3HostTypeLabel] = hostType
	}
	if len(matchLabels) > 0 {
		listOpts = append(listOpts, client.MatchingLabels(matchLabels))
	}

	matchManagedBy := matchExpressions["managedBy"]
	if matchManagedBy == "" {
		matchManagedBy = shared.OsacDefaultManagedByValue
	}

	bmhList := &metal3api.BareMetalHostList{}
	if err := m.client.List(ctx, bmhList, listOpts...); err != nil {
		return nil, fmt.Errorf("failed to list BareMetalHosts: %w", err)
	}

	candidates := make([]metal3api.BareMetalHost, 0, len(bmhList.Items))
	for _, bmh := range bmhList.Items {
		if bmh.Status.OperationalStatus != metal3api.OperationalStatusOK {
			continue
		}

		if bmh.Status.Provisioning.State != metal3api.StateAvailable {
			continue
		}

		if bmh.Spec.ConsumerRef != nil {
			continue
		}

		if bmh.Annotations["inspect.metal3.io"] == "disabled" {
			log.V(1).Info("Skipping BareMetalHost: hardware inspection disabled", "host", bmh.Name)
			continue
		}

		if bmh.Status.HardwareDetails == nil || len(bmh.Status.HardwareDetails.NIC) == 0 {
			log.Error(nil, "Skipping BareMetalHost: available but NIC inventory is missing — host may be misconfigured or inspection incomplete", "host", bmh.Name)
			continue
		}

		managedBy := bmh.Labels[Metal3ManagedByLabel]
		if managedBy == "" {
			managedBy = shared.OsacDefaultManagedByValue
		}
		if managedBy != matchManagedBy {
			continue
		}

		candidates = append(candidates, bmh)
	}

	if len(candidates) == 0 {
		return nil, nil
	}

	rand.Shuffle(len(candidates), func(i, j int) {
		candidates[i], candidates[j] = candidates[j], candidates[i]
	})

	bmh := &candidates[0]
	return bmhToHost(bmh, m.hostClass), nil
}

func (m *Metal3Client) AssignHost(ctx context.Context, inventoryHostID string, bareMetalInstanceID string, labels map[string]string) (*Host, error) {
	if inventoryHostID == "" {
		return nil, fmt.Errorf("invalid input: inventoryHostID is empty")
	}
	if bareMetalInstanceID == "" {
		return nil, fmt.Errorf("invalid input: bareMetalInstanceID is empty")
	}

	namespace, name, err := ParseHostID(inventoryHostID)
	if err != nil {
		return nil, err
	}

	bmh := &metal3api.BareMetalHost{}
	if err := m.client.Get(ctx, client.ObjectKey{Namespace: namespace, Name: name}, bmh); err != nil {
		return nil, fmt.Errorf("failed to get BareMetalHost %s: %w", inventoryHostID, err)
	}

	if bmh.Spec.ConsumerRef != nil && bmh.Spec.ConsumerRef.Name != bareMetalInstanceID {
		return nil, nil
	}

	if bmh.Labels == nil {
		bmh.Labels = map[string]string{}
	}
	for key, value := range labels {
		bmh.Labels[metal3LabelPrefix+key] = value
	}

	// Set consumerRef to mark this BMH as claimed. The BareMetalOperator uses
	// consumerRef to prevent other consumers (HostClaim, Cluster API, etc.)
	// from claiming a host that is already in use. BMO does not validate that
	// the referenced object exists or resolve the reference — it only checks
	// whether consumerRef is nil (unclaimed) or non-nil (claimed), and
	// compares Name/Namespace/Kind/Group for claim matching.
	//
	// The Name is the BareMetalInstance UID rather than its resource name
	// because the generic AssignHost interface identifies consumers by an
	// opaque string ID, not by Kubernetes namespace/name. The Namespace is
	// omitted because the consumer ID does not carry namespace information.
	// This is sufficient for BMO's claim mechanism — it prevents double-
	// booking without requiring a resolvable object reference.
	bmh.Spec.ConsumerRef = &corev1.ObjectReference{
		APIVersion: "osac.openshift.io/v1alpha1",
		Kind:       "BareMetalInstance",
		Name:       bareMetalInstanceID,
	}

	if err := m.client.Update(ctx, bmh); err != nil {
		return nil, fmt.Errorf("failed to assign BareMetalHost %s: %w", inventoryHostID, err)
	}

	return bmhToHost(bmh, m.hostClass), nil
}

func (m *Metal3Client) GetHostNICs(ctx context.Context, inventoryHostID string) ([]HostNIC, error) {
	namespace, name, err := ParseHostID(inventoryHostID)
	if err != nil {
		return nil, err
	}

	bmh := &metal3api.BareMetalHost{}
	if err := m.client.Get(ctx, client.ObjectKey{Namespace: namespace, Name: name}, bmh); err != nil {
		return nil, fmt.Errorf("failed to get BareMetalHost %s: %w", inventoryHostID, err)
	}

	if bmh.Status.HardwareDetails == nil {
		return nil, nil
	}

	if len(bmh.Status.HardwareDetails.NIC) == 0 {
		return nil, fmt.Errorf("BareMetalHost %s has no NIC data despite completed inspection", inventoryHostID)
	}

	nics := make([]HostNIC, 0, len(bmh.Status.HardwareDetails.NIC))
	for _, n := range bmh.Status.HardwareDetails.NIC {
		nics = append(nics, HostNIC{MAC: strings.ToLower(n.MAC)})
	}
	return nics, nil
}

func (m *Metal3Client) UnassignHost(ctx context.Context, inventoryHostID string, labels []string) error {
	namespace, name, err := ParseHostID(inventoryHostID)
	if err != nil {
		return err
	}

	bmh := &metal3api.BareMetalHost{}
	if err := m.client.Get(ctx, client.ObjectKey{Namespace: namespace, Name: name}, bmh); err != nil {
		return fmt.Errorf("failed to get BareMetalHost %s: %w", inventoryHostID, err)
	}

	for _, label := range labels {
		delete(bmh.Labels, metal3LabelPrefix+label)
	}

	bmh.Spec.ConsumerRef = nil

	if err := m.client.Update(ctx, bmh); err != nil {
		return fmt.Errorf("failed to unassign BareMetalHost %s: %w", inventoryHostID, err)
	}

	return nil
}

func ParseHostID(hostID string) (namespace, name string, err error) {
	parts := strings.SplitN(hostID, "/", 2)
	if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
		return "", "", fmt.Errorf("invalid host ID %q: expected namespace/name format", hostID)
	}
	return parts[0], parts[1], nil
}

func bmhToHost(bmh *metal3api.BareMetalHost, hostClass string) *Host {
	labels := bmh.Labels
	if labels == nil {
		labels = map[string]string{}
	}

	managedBy := labels[Metal3ManagedByLabel]
	if managedBy == "" {
		managedBy = shared.OsacDefaultManagedByValue
	}

	var bareMetalInstanceID string
	if bmh.Spec.ConsumerRef != nil {
		bareMetalInstanceID = bmh.Spec.ConsumerRef.Name
	}

	return &Host{
		BareMetalPoolID:     labels[Metal3PoolIDLabel],
		BareMetalInstanceID: bareMetalInstanceID,
		InventoryHostID:     fmt.Sprintf("%s/%s", bmh.Namespace, bmh.Name),
		Name:                bmh.Name,
		HostType:            labels[Metal3HostTypeLabel],
		HostClass:           hostClass,
		ProvisionState:      string(bmh.Status.Provisioning.State),
		ManagedBy:           managedBy,
	}
}

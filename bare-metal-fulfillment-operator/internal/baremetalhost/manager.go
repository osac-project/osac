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

// Package baremetalhost provides utilities for managing on-demand
// BareMetalHost CRs. It is a shared utility — any inventory backend
// that needs to create, delete, or check readiness of BMH CRs can
// use this package.
package baremetalhost

import (
	"context"
	"fmt"
	"strings"

	metal3api "github.com/metal3-io/baremetal-operator/apis/metal3.io/v1alpha1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	ctrllog "sigs.k8s.io/controller-runtime/pkg/log"

	"github.com/osac-project/osac/bare-metal-fulfillment-operator/internal/shared"
)

// bmcSecretManagedByLabel marks a BMC credentials Secret as created and owned
// by the operator. Only Secrets carrying this label are deleted on unassign —
// admin-created Secrets (which lack it) are never touched.
var (
	bmcSecretManagedByLabel = shared.OsacPrefix + "/managed-by"
	bmcSecretManagedByValue = shared.OsacDefaultManagedByValue
	bmcSecretOwnerLabel     = shared.OsacPrefix + "/bare-metal-instance-uid"
)

// CreateParams holds the parameters for creating a BareMetalHost CR.
type CreateParams struct {
	Name                           string
	BMCAddress                     string
	CredentialsSecret              string
	BootMACAddress                 string
	DisableCertificateVerification bool
	ConsumerRef                    *corev1.ObjectReference
	Labels                         map[string]string
}

// Manager handles on-demand BareMetalHost CR creation, deletion, and
// readiness checking. Any inventory backend that needs BMH CRs can
// use this utility.
//
// uncachedClient handles BMC credential Secrets and confirms BMH deletion
// directly with the API server. Direct Secret access avoids cluster-wide
// Secret caching and its list/watch RBAC requirements.
type Manager struct {
	client         client.Client
	uncachedClient client.Client
	namespace      string
}

// NewManager creates a manager for the given Metal3 namespace. uncachedClient
// should be built with client.New; if nil, the cached client is used.
func NewManager(k8sClient client.Client, uncachedClient client.Client, namespace string) *Manager {
	if uncachedClient == nil {
		uncachedClient = k8sClient
	}
	return &Manager{
		client:         k8sClient,
		uncachedClient: uncachedClient,
		namespace:      namespace,
	}
}

// Namespace returns the Metal3 namespace used for BareMetalHost CRs.
func (m *Manager) Namespace() string {
	return m.namespace
}

// CreateBMH creates a BareMetalHost CR in the configured namespace.
// Sets spec.online = false — the controller manages power via reconcileManagement.
// Idempotent: if a BMH with the same name already exists and its consumerRef
// matches, treats it as success. If consumerRef does not match, returns an error.
func (m *Manager) CreateBMH(ctx context.Context, params CreateParams) error {
	log := ctrllog.FromContext(ctx)

	bmh := &metal3api.BareMetalHost{
		ObjectMeta: metav1.ObjectMeta{
			Name:      params.Name,
			Namespace: m.namespace,
			Labels:    params.Labels,
		},
		Spec: metal3api.BareMetalHostSpec{
			BMC: metal3api.BMCDetails{
				Address:                        params.BMCAddress,
				CredentialsName:                params.CredentialsSecret,
				DisableCertificateVerification: params.DisableCertificateVerification,
			},
			BootMACAddress: params.BootMACAddress,
			Online:         false,
			ConsumerRef:    params.ConsumerRef,
		},
	}

	err := m.client.Create(ctx, bmh)
	if err == nil {
		log.Info("Created BareMetalHost", "name", params.Name, "namespace", m.namespace)
		return nil
	}

	if !apierrors.IsAlreadyExists(err) {
		return fmt.Errorf("failed to create BareMetalHost %s/%s: %w", m.namespace, params.Name, err)
	}

	existing := &metal3api.BareMetalHost{}
	if getErr := m.client.Get(ctx, client.ObjectKey{Namespace: m.namespace, Name: params.Name}, existing); getErr != nil {
		return fmt.Errorf("failed to get existing BareMetalHost %s/%s: %w", m.namespace, params.Name, getErr)
	}

	if existing.Spec.ConsumerRef == nil && params.ConsumerRef == nil {
		log.Info("BareMetalHost already exists with no consumerRef", "name", params.Name)
		return nil
	}

	if existing.Spec.ConsumerRef != nil && params.ConsumerRef != nil &&
		existing.Spec.ConsumerRef.APIVersion == params.ConsumerRef.APIVersion &&
		existing.Spec.ConsumerRef.Kind == params.ConsumerRef.Kind &&
		existing.Spec.ConsumerRef.Name == params.ConsumerRef.Name &&
		existing.Spec.ConsumerRef.Namespace == params.ConsumerRef.Namespace {
		log.Info("BareMetalHost already exists with matching consumerRef", "name", params.Name)
		return nil
	}

	return fmt.Errorf("BareMetalHost %s/%s already exists with different consumerRef", m.namespace, params.Name)
}

// BMHExists checks whether a BareMetalHost CR exists in the configured namespace.
func (m *Manager) BMHExists(ctx context.Context, name string) (bool, error) {
	bmh := &metal3api.BareMetalHost{}
	err := m.uncachedClient.Get(ctx, client.ObjectKey{Namespace: m.namespace, Name: name}, bmh)
	if apierrors.IsNotFound(err) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("failed to check BareMetalHost %s/%s existence: %w", m.namespace, name, err)
	}
	return true, nil
}

// EnsureBMCSecret creates (or updates) an operator-managed BMC credentials
// Secret in the Metal3 namespace with the standard Metal3/BMO "username" and
// "password" keys. The Secret is labeled so DeleteBMCSecret can safely tell
// operator-created Secrets apart from admin-created ones. Idempotent: if the
// Secret already exists its data and label are updated for the same BMI.
func (m *Manager) EnsureBMCSecret(ctx context.Context, name, username, password, ownerUID string) error {
	secret := &corev1.Secret{ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: m.namespace}}
	update := func() error { return m.updateBMCSecret(ctx, secret, username, password, ownerUID) }
	result, err := controllerutil.CreateOrUpdate(ctx, m.uncachedClient, secret, update)
	if err != nil {
		return fmt.Errorf("failed to ensure BMC credentials Secret %s/%s: %w", m.namespace, name, err)
	}
	ctrllog.FromContext(ctx).Info("Ensured BMC credentials Secret", "name", name, "namespace", m.namespace, "result", result)
	return nil
}

func (m *Manager) updateBMCSecret(ctx context.Context, secret *corev1.Secret, username, password, ownerUID string) error {
	if err := m.checkBMCSecretOwner(ctx, secret, ownerUID); err != nil {
		return err
	}
	setBMCSecretLabels(secret, ownerUID)
	secret.Type = corev1.SecretTypeOpaque
	secret.Data = map[string][]byte{"username": []byte(username), "password": []byte(password)}
	return nil
}

func setBMCSecretLabels(secret *corev1.Secret, ownerUID string) {
	if secret.Labels == nil {
		secret.Labels = map[string]string{}
	}
	secret.Labels[bmcSecretManagedByLabel] = bmcSecretManagedByValue
	secret.Labels[bmcSecretOwnerLabel] = ownerUID
}

func (m *Manager) checkBMCSecretOwner(ctx context.Context, secret *corev1.Secret, ownerUID string) error {
	if ownerUID == "" {
		return fmt.Errorf("BMC credentials Secret %s/%s requires a BMI UID", m.namespace, secret.Name)
	}
	if secret.ResourceVersion == "" {
		return nil
	}
	if secret.Labels[bmcSecretManagedByLabel] != bmcSecretManagedByValue {
		return fmt.Errorf("BMC credentials Secret %s/%s already exists and is not operator-managed; refusing to overwrite", m.namespace, secret.Name)
	}
	owner := secret.Labels[bmcSecretOwnerLabel]
	if owner == ownerUID {
		return nil
	}
	if owner != "" {
		return fmt.Errorf("BMC credentials Secret %s/%s is not owned by this BMI", m.namespace, secret.Name)
	}
	_, err := m.findOwnedBMH(ctx, strings.TrimSuffix(secret.Name, "-bmc-secret"), ownerUID)
	return err
}

// DeleteBMCSecret deletes a generated Secret only for its BMI owner.
func (m *Manager) DeleteBMCSecret(ctx context.Context, name, ownerUID string) error {
	secret, err := m.findOwnedBMCSecret(ctx, name, ownerUID)
	if err != nil || secret == nil {
		return err
	}
	err = m.uncachedClient.Delete(ctx, secret, client.Preconditions{UID: &secret.UID, ResourceVersion: &secret.ResourceVersion})
	if err != nil && !apierrors.IsNotFound(err) {
		return fmt.Errorf("failed to delete BMC credentials Secret %s/%s: %w", m.namespace, name, err)
	}
	return m.confirmBMCSecretDeletion(ctx, name)
}

func (m *Manager) findOwnedBMCSecret(ctx context.Context, name, ownerUID string) (*corev1.Secret, error) {
	secret := &corev1.Secret{}
	if err := m.uncachedClient.Get(ctx, client.ObjectKey{Namespace: m.namespace, Name: name}, secret); err != nil {
		return nil, client.IgnoreNotFound(err)
	}
	if err := m.checkBMCSecretOwner(ctx, secret, ownerUID); err != nil {
		return nil, err
	}
	return secret, nil
}

func (m *Manager) confirmBMCSecretDeletion(ctx context.Context, name string) error {
	err := m.uncachedClient.Get(ctx, client.ObjectKey{Namespace: m.namespace, Name: name}, &corev1.Secret{})
	if err == nil {
		return fmt.Errorf("BMC credentials Secret %s/%s deletion is pending", m.namespace, name)
	}
	return client.IgnoreNotFound(err)
}

// DeleteBMH leaves a BMH with another consumerRef untouched.
func (m *Manager) DeleteBMH(ctx context.Context, name, ownerUID string) error {
	bmh, err := m.findOwnedBMH(ctx, name, ownerUID)
	if err != nil || bmh == nil {
		return err
	}
	err = m.uncachedClient.Delete(ctx, bmh, client.Preconditions{UID: &bmh.UID, ResourceVersion: &bmh.ResourceVersion})
	if err != nil && !apierrors.IsNotFound(err) {
		return fmt.Errorf("failed to delete BareMetalHost %s/%s: %w", m.namespace, name, err)
	}
	return nil
}

func (m *Manager) findOwnedBMH(ctx context.Context, name, ownerUID string) (*metal3api.BareMetalHost, error) {
	if ownerUID == "" {
		return nil, fmt.Errorf("BareMetalHost %s/%s requires a BMI UID", m.namespace, name)
	}
	bmh := &metal3api.BareMetalHost{}
	if err := m.uncachedClient.Get(ctx, client.ObjectKey{Namespace: m.namespace, Name: name}, bmh); err != nil {
		return nil, client.IgnoreNotFound(err)
	}
	if bmh.Spec.ConsumerRef == nil || bmh.Spec.ConsumerRef.Name != ownerUID {
		return nil, fmt.Errorf("BareMetalHost %s/%s is not owned by this BMI", m.namespace, name)
	}
	return bmh, nil
}

// GetHardwareNICs returns the lowercased MAC addresses for the host's NICs,
// sourcing them from the standalone HardwareData resource and falling back to
// the deprecated BareMetalHost.Status.HardwareDetails. Returns nil when neither
// source has NIC data.
func (m *Manager) GetHardwareNICs(ctx context.Context, name string) ([]string, error) {
	log := ctrllog.FromContext(ctx)

	key := client.ObjectKey{Namespace: m.namespace, Name: name}

	bmh := &metal3api.BareMetalHost{}
	if err := m.client.Get(ctx, key, bmh); err != nil {
		return nil, fmt.Errorf("failed to get BareMetalHost %s/%s: %w", m.namespace, name, err)
	}

	hd := &metal3api.HardwareData{}
	if err := m.client.Get(ctx, key, hd); err != nil {
		if !apierrors.IsNotFound(err) {
			return nil, fmt.Errorf("failed to get HardwareData %s/%s: %w", m.namespace, name, err)
		}
		hd = nil
	}

	macs := hardwareMACs(ResolveHardwareDetails(hd, bmh))
	if len(macs) == 0 {
		log.V(1).Info("BareMetalHost has no hardware NIC data",
			"name", name, "namespace", m.namespace)
		return nil, nil
	}

	log.V(1).Info("Retrieved hardware NICs",
		"name", name, "namespace", m.namespace, "count", len(macs))
	return macs, nil
}

func hardwareMACs(details *metal3api.HardwareDetails) []string {
	if details == nil || len(details.NIC) == 0 {
		return nil
	}
	macs := make([]string, 0, len(details.NIC))
	for _, nic := range details.NIC {
		if nic.MAC == "" {
			continue
		}
		macs = append(macs, strings.ToLower(nic.MAC))
	}
	return macs
}

// IsBMHReady checks whether the BMH has completed Metal3 registration and is
// ready for power management. Returns true when the BMH provisioning state is
// "available" and operational status is "OK". Returns false while the BMH is
// in registering, inspecting, or preparing state. Returns an error if the BMH
// does not exist or has an error operational status. Callers distinguish
// not-found from error-status via apierrors.IsNotFound(err).
func (m *Manager) IsBMHReady(ctx context.Context, name string) (bool, error) {
	log := ctrllog.FromContext(ctx)

	bmh := &metal3api.BareMetalHost{}
	if err := m.client.Get(ctx, client.ObjectKey{Namespace: m.namespace, Name: name}, bmh); err != nil {
		// IsBMHReady runs right after CreateBMH, so a NotFound here is almost
		// always the informer cache not having observed the just-created BMH yet.
		// Treat it as "not ready" so the caller requeues rather than erroring.
		// It's safe even if the BMH were genuinely absent: ensureBMHExists
		// re-creates it on the next reconcile before this check runs again.
		if apierrors.IsNotFound(err) {
			log.V(1).Info("BareMetalHost not observed yet, treating as not ready", "name", name)
			return false, nil
		}
		return false, fmt.Errorf("failed to get BareMetalHost %s/%s: %w", m.namespace, name, err)
	}

	if bmh.Status.OperationalStatus == metal3api.OperationalStatusError {
		return false, fmt.Errorf("BareMetalHost %s/%s has error status: %s",
			m.namespace, name, bmh.Status.ErrorMessage)
	}

	ready := bmh.Status.Provisioning.State == metal3api.StateAvailable &&
		bmh.Status.OperationalStatus == metal3api.OperationalStatusOK

	log.V(1).Info("BareMetalHost readiness check",
		"name", name,
		"provisioningState", bmh.Status.Provisioning.State,
		"operationalStatus", bmh.Status.OperationalStatus,
		"ready", ready)

	return ready, nil
}

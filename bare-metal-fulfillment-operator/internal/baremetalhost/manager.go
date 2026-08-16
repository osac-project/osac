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

	metal3api "github.com/metal3-io/baremetal-operator/apis/metal3.io/v1alpha1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	ctrllog "sigs.k8s.io/controller-runtime/pkg/log"
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
type Manager struct {
	client    client.Client
	namespace string
}

// NewManager creates a manager for the given Metal3 namespace.
func NewManager(k8sClient client.Client, namespace string) *Manager {
	return &Manager{
		client:    k8sClient,
		namespace: namespace,
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

// DeleteBMH deletes a BareMetalHost CR by name. Idempotent — ignores NotFound.
// Does not delete BMC credentials Secrets (they are admin-managed and reusable).
func (m *Manager) DeleteBMH(ctx context.Context, name string) error {
	log := ctrllog.FromContext(ctx)

	bmh := &metal3api.BareMetalHost{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: m.namespace,
		},
	}

	if err := m.client.Delete(ctx, bmh); err != nil {
		if apierrors.IsNotFound(err) {
			log.Info("BareMetalHost already deleted", "name", name, "namespace", m.namespace)
			return nil
		}
		return fmt.Errorf("failed to delete BareMetalHost %s/%s: %w", m.namespace, name, err)
	}

	log.Info("Deleted BareMetalHost", "name", name, "namespace", m.namespace)
	return nil
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

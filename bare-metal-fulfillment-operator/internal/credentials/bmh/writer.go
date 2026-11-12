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

// Package bmh stores BMC credentials as Kubernetes Secrets for BMO.
package bmh

import (
	"context"
	"fmt"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	ctrllog "sigs.k8s.io/controller-runtime/pkg/log"

	"github.com/osac-project/osac/bare-metal-fulfillment-operator/internal/baremetalhost"
	"github.com/osac-project/osac/bare-metal-fulfillment-operator/internal/credentials"
	"github.com/osac-project/osac/bare-metal-fulfillment-operator/internal/shared"
)

const (
	managedByLabel = shared.OsacPrefix + "/managed-by"
	managedByValue = shared.OsacDefaultManagedByValue
	ownerUIDLabel  = shared.OsacPrefix + "/bare-metal-instance-uid"
)

// SecretWriter manages operator-owned BMC credential Secrets in the Metal3 namespace.
type SecretWriter struct {
	client    client.Client
	namespace string
}

var _ credentials.Writer = (*SecretWriter)(nil)

// NewSecretWriter creates a Secret writer using the supplied direct Kubernetes client.
func NewSecretWriter(client client.Client, namespace string) *SecretWriter {
	return &SecretWriter{client: client, namespace: namespace}
}

// Create creates or updates the Secret consumed by BMO for its BMI owner.
func (s *SecretWriter) Create(ctx context.Context, ref credentials.Reference, bmc credentials.BMC) error {
	name := baremetalhost.CredentialsSecretName(ref.HostID)
	secret := &corev1.Secret{ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: s.namespace}}
	result, err := controllerutil.CreateOrUpdate(ctx, s.client, secret, func() error {
		if err := s.checkSecretOwner(ctx, secret, ref.HostID, ref.BMIID); err != nil {
			return err
		}
		setSecretLabels(secret, ref.BMIID)
		secret.Type = corev1.SecretTypeOpaque
		secret.Data = map[string][]byte{
			"username": bmc.Username,
			"password": bmc.Password,
		}
		return nil
	})
	if err != nil {
		return fmt.Errorf("failed to ensure BMC credentials Secret %s/%s: %w", s.namespace, name, err)
	}

	ctrllog.FromContext(ctx).Info("Ensured BMC credentials Secret", "name", name, "namespace", s.namespace, "result", result)
	return nil
}

func (s *SecretWriter) checkSecretOwner(ctx context.Context, secret *corev1.Secret, hostID, bmiID string) error {
	if bmiID == "" {
		return fmt.Errorf("BMC credentials Secret %s/%s requires a BMI UID", s.namespace, secret.Name)
	}
	if secret.ResourceVersion == "" {
		return nil
	}
	if secret.Labels[managedByLabel] != managedByValue {
		return fmt.Errorf("BMC credentials Secret %s/%s is not operator-managed; refusing to modify or delete it", s.namespace, secret.Name)
	}
	owner := secret.Labels[ownerUIDLabel]
	if owner == bmiID {
		return nil
	}
	if owner != "" {
		return fmt.Errorf("BMC credentials Secret %s/%s is not owned by this BMI", s.namespace, secret.Name)
	}
	_, err := baremetalhost.FindOwnedBMH(ctx, s.client, s.namespace, hostID, bmiID)
	return err
}

func setSecretLabels(secret *corev1.Secret, bmiID string) {
	if secret.Labels == nil {
		secret.Labels = map[string]string{}
	}
	secret.Labels[managedByLabel] = managedByValue
	secret.Labels[ownerUIDLabel] = bmiID
}

// Delete removes the Secret only when its BMI owner matches.
func (s *SecretWriter) Delete(ctx context.Context, ref credentials.Reference) error {
	name := baremetalhost.CredentialsSecretName(ref.HostID)
	secret, err := s.findOwnedSecret(ctx, ref, name)
	if err != nil || secret == nil {
		return err
	}
	err = s.client.Delete(ctx, secret, client.Preconditions{UID: &secret.UID, ResourceVersion: &secret.ResourceVersion})
	if err != nil && !apierrors.IsNotFound(err) {
		return fmt.Errorf("failed to delete BMC credentials Secret %s/%s: %w", s.namespace, name, err)
	}
	return s.confirmDeletion(ctx, name)
}

func (s *SecretWriter) findOwnedSecret(ctx context.Context, ref credentials.Reference, name string) (*corev1.Secret, error) {
	secret := &corev1.Secret{}
	if err := s.client.Get(ctx, client.ObjectKey{Namespace: s.namespace, Name: name}, secret); err != nil {
		return nil, client.IgnoreNotFound(err)
	}
	if err := s.checkSecretOwner(ctx, secret, ref.HostID, ref.BMIID); err != nil {
		return nil, err
	}
	return secret, nil
}

func (s *SecretWriter) confirmDeletion(ctx context.Context, name string) error {
	err := s.client.Get(ctx, client.ObjectKey{Namespace: s.namespace, Name: name}, &corev1.Secret{})
	if err == nil {
		return fmt.Errorf("BMC credentials Secret %s/%s deletion is pending", s.namespace, name)
	}
	return client.IgnoreNotFound(err)
}

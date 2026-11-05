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

// Package bmhsecret stores BMC credentials as Kubernetes Secrets for BMO.
package bmhsecret

import (
	"context"
	"fmt"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	ctrllog "sigs.k8s.io/controller-runtime/pkg/log"

	"github.com/osac-project/osac/bare-metal-fulfillment-operator/internal/credentials"
	"github.com/osac-project/osac/bare-metal-fulfillment-operator/internal/shared"
)

const (
	managedByLabel = shared.OsacPrefix + "/managed-by"
	managedByValue = shared.OsacDefaultManagedByValue
)

// Store manages operator-owned BMC credential Secrets in the Metal3 namespace.
type Store struct {
	client    client.Client
	namespace string
}

var _ credentials.BMCSecretStore = (*Store)(nil)

// NewStore creates a Secret store using the supplied direct Kubernetes client.
func NewStore(client client.Client, namespace string) *Store {
	return &Store{client: client, namespace: namespace}
}

// Ensure creates or updates the Secret consumed by BMO. It refuses to overwrite
// a Secret that is not already marked as operator-managed.
func (s *Store) Ensure(ctx context.Context, name string, bmc *credentials.BMC) error {
	if bmc == nil {
		return fmt.Errorf("BMC credentials are required")
	}

	secret := &corev1.Secret{ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: s.namespace}}
	result, err := controllerutil.CreateOrUpdate(ctx, s.client, secret, func() error {
		if secret.ResourceVersion != "" && secret.Labels[managedByLabel] != managedByValue {
			return fmt.Errorf("BMC credentials Secret %s/%s already exists and is not operator-managed; refusing to overwrite", s.namespace, name)
		}
		if secret.Labels == nil {
			secret.Labels = map[string]string{}
		}
		secret.Labels[managedByLabel] = managedByValue
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

// Delete removes the Secret if it is operator-managed. Missing and admin-owned
// Secrets are left alone; deletion errors are returned while deletion is pending.
func (s *Store) Delete(ctx context.Context, name string) error {
	log := ctrllog.FromContext(ctx)
	secret := &corev1.Secret{}
	if err := s.client.Get(ctx, client.ObjectKey{Namespace: s.namespace, Name: name}, secret); err != nil {
		if apierrors.IsNotFound(err) {
			return nil
		}
		return fmt.Errorf("failed to get BMC credentials Secret %s/%s: %w", s.namespace, name, err)
	}

	if secret.Labels[managedByLabel] != managedByValue {
		log.Info("BMC credentials Secret is not operator-managed, leaving it in place",
			"name", name, "namespace", s.namespace)
		return nil
	}

	if err := s.client.Delete(ctx, secret); err != nil {
		if apierrors.IsNotFound(err) {
			return nil
		}
		return fmt.Errorf("failed to delete BMC credentials Secret %s/%s: %w", s.namespace, name, err)
	}
	log.Info("Requested deletion of operator-managed BMC credentials Secret", "name", name, "namespace", s.namespace)
	return s.confirmDeletion(ctx, name)
}

func (s *Store) confirmDeletion(ctx context.Context, name string) error {
	err := s.client.Get(ctx, client.ObjectKey{Namespace: s.namespace, Name: name}, &corev1.Secret{})
	if err == nil {
		return fmt.Errorf("BMC credentials Secret %s/%s deletion is pending", s.namespace, name)
	}
	return client.IgnoreNotFound(err)
}

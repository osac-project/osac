// Copyright (c) 2026 Red Hat Inc.
// SPDX-License-Identifier: Apache-2.0

package hypershift

import (
	"bytes"
	"context"
	"crypto/x509"
	"encoding/pem"
	"fmt"

	hypershiftv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/equality"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/osac-project/osac/tests/integration/caas/internal/simulator/kube"
)

const controlPlanePullSecretName = "pull-secret"

// ensureSSHKey copies the native public-key field before the HCP references it.
// Other destination fields retain their existing writers and ownership.
func (r *HostedClusterReconciler) ensureSSHKey(
	ctx context.Context,
	hc *hypershiftv1.HostedCluster,
	namespace string,
) error {
	if hc.Spec.SSHKey.Name == "" {
		return nil
	}

	ready, err := r.copyControlPlaneSecret(
		ctx, hc, namespace, hc.Spec.SSHKey.Name, "ssh-key", corev1.SecretTypeOpaque, "id_rsa.pub",
	)
	if err != nil {
		return err
	}

	if !ready {
		return fmt.Errorf("SSH Secret must contain id_rsa.pub")
	}

	return nil
}

// ensureControlPlaneSecrets materializes the native Secret inputs before publishing
// their references to the HostedControlPlane. Incomplete encryption inputs wait
// for Secret or HostedCluster events. Guest consumers remain outside this model.
func (r *HostedClusterReconciler) ensureControlPlaneSecrets(
	ctx context.Context, hc *hypershiftv1.HostedCluster, namespace string,
) (bool, error) {
	if ready, err := r.ensureSecretEncryption(ctx, hc, namespace); err != nil || !ready {
		return false, err
	}

	if audit := hc.Spec.AuditWebhook; audit != nil && audit.Name != "" {
		ready, err := r.copyControlPlaneSecret(
			ctx, hc, namespace, audit.Name, audit.Name, corev1.SecretTypeOpaque, hypershiftv1.AuditWebhookKubeconfigKey,
		)
		if err != nil {
			return false, err
		}

		if !ready {
			return false, fmt.Errorf("audit webhook secret does not contain key %s", hypershiftv1.AuditWebhookKubeconfigKey)
		}
	}

	if hc.Spec.ServiceAccountSigningKey != nil {
		if err := r.ensureSigningKey(ctx, hc, namespace); err != nil {
			return false, err
		}
	}

	if hc.Spec.Etcd.ManagementType == hypershiftv1.Unmanaged {
		etcd := hc.Spec.Etcd.Unmanaged
		if etcd == nil || etcd.Endpoint == "" || etcd.TLS.ClientSecret.Name == "" {
			return false, fmt.Errorf("unmanaged etcd endpoint and TLS Secret reference are required")
		}

		name := etcd.TLS.ClientSecret.Name

		return r.copyControlPlaneSecret(ctx, hc, namespace, name, name, corev1.SecretTypeOpaque)
	}

	return true, nil
}

// ensureSecretEncryption copies AES-CBC keys. The native Agent platform has
// no management-side KMS credential synchronization.
func (r *HostedClusterReconciler) ensureSecretEncryption(
	ctx context.Context, hc *hypershiftv1.HostedCluster, namespace string,
) (bool, error) {
	encryption := hc.Spec.SecretEncryption
	if encryption == nil {
		return true, nil
	}

	switch encryption.Type {
	case hypershiftv1.AESCBC:
		if encryption.AESCBC == nil || encryption.AESCBC.ActiveKey.Name == "" {
			return false, nil
		}

		names := []string{encryption.AESCBC.ActiveKey.Name}
		if backup := encryption.AESCBC.BackupKey; backup != nil && backup.Name != "" {
			names = append(names, backup.Name)
		}

		for _, name := range names {
			ready, err := r.copyControlPlaneSecret(
				ctx, hc, namespace, name, name, corev1.SecretTypeOpaque, hypershiftv1.AESCBCKeySecretKey,
			)
			if err != nil || !ready {
				return false, err
			}
		}
	case hypershiftv1.KMS:
		if encryption.KMS == nil {
			return false, nil
		}
	default:
		return false, nil
	}

	return true, nil
}

// copyControlPlaneSecret copies selected native data keys, or the complete data
// owned by unmanaged etcd, while retaining the original destination identity.
// A missing data key leaves publication pending for the caller to report.
func (r *HostedClusterReconciler) copyControlPlaneSecret(
	ctx context.Context, hc *hypershiftv1.HostedCluster, namespace, sourceName, destinationName string,
	secretType corev1.SecretType, keys ...string,
) (bool, error) {
	source := &corev1.Secret{}
	if err := r.APIReader.Get(ctx, client.ObjectKey{Namespace: hc.Namespace, Name: sourceName}, source); err != nil {
		return false, err
	}

	for _, key := range keys {
		if _, present := source.Data[key]; !present {
			return false, nil
		}
	}

	desired := &corev1.Secret{ObjectMeta: metav1.ObjectMeta{
		Namespace: namespace, Name: destinationName,
		Annotations: map[string]string{kube.HostedClusterAnnotation: hc.Namespace + "/" + hc.Name},
	}}
	current := &corev1.Secret{}

	err := r.APIReader.Get(ctx, client.ObjectKeyFromObject(desired), current)
	create := apierrors.IsNotFound(err)
	if err != nil && !create {
		return false, err
	}

	if create {
		current = desired
	} else if err := validateOutput(current, desired); err != nil {
		return false, err
	}

	if current.DeletionTimestamp != nil {
		return false, fmt.Errorf("control-plane input Secret %s is terminating", destinationName)
	}

	before := current.DeepCopy()
	current.Type = secretType
	if len(keys) == 0 {
		current.Data = source.DeepCopy().Data
	} else {
		if current.Data == nil {
			current.Data = map[string][]byte{}
		}

		for _, key := range keys {
			current.Data[key] = source.Data[key]
		}
	}

	if create {
		return true, r.Client.Create(ctx, current)
	}

	if equality.Semantic.DeepEqual(before, current) {
		return true, nil
	}

	return true, r.Client.Patch(ctx, current, client.MergeFromWithOptions(before, client.MergeFromWithOptimisticLock{}))
}

// ensureSigningKey derives the native public key from a PKCS1 RSA private key.
// An initialized destination keeps its keys and must match the configured private key.
func (r *HostedClusterReconciler) ensureSigningKey(
	ctx context.Context, hc *hypershiftv1.HostedCluster, namespace string,
) error {
	if hc.Spec.IssuerURL == "" {
		return fmt.Errorf("IssuerURL is required with a service account signing key")
	}

	source := &corev1.Secret{}
	if err := r.APIReader.Get(ctx, client.ObjectKey{
		Namespace: hc.Namespace, Name: hc.Spec.ServiceAccountSigningKey.Name,
	}, source); err != nil {
		return err
	}

	private, present := source.Data[hypershiftv1.ServiceAccountSigningKeySecretKey]
	if !present {
		return fmt.Errorf("signing Secret must contain %s", hypershiftv1.ServiceAccountSigningKeySecretKey)
	}

	block, _ := pem.Decode(private)
	if block == nil {
		return fmt.Errorf("signing Secret has no private-key PEM block")
	}

	key, err := x509.ParsePKCS1PrivateKey(block.Bytes)
	if err != nil {
		return fmt.Errorf("decode service account private key: %w", err)
	}

	public, err := x509.MarshalPKIXPublicKey(&key.PublicKey)
	if err != nil {
		return err
	}

	desired := &corev1.Secret{ObjectMeta: metav1.ObjectMeta{
		Namespace: namespace, Name: "sa-signing-key",
		Annotations: map[string]string{kube.HostedClusterAnnotation: hc.Namespace + "/" + hc.Name},
	}}
	current := &corev1.Secret{}

	err = r.APIReader.Get(ctx, client.ObjectKeyFromObject(desired), current)
	create := apierrors.IsNotFound(err)
	if err != nil && !create {
		return err
	}

	if create {
		current = desired
	} else if err := validateOutput(current, desired); err != nil {
		return err
	}

	if current.DeletionTimestamp != nil {
		return fmt.Errorf("service account signing Secret is terminating")
	}

	if existing, initialized := current.Data["service-account.key"]; initialized {
		if !bytes.Equal(existing, private) {
			return fmt.Errorf("existing control plane service account signing key does not match private key")
		}

		return nil
	}

	before := current.DeepCopy()
	if current.Data == nil {
		current.Data = map[string][]byte{}
	}

	current.Data["service-account.key"] = private
	current.Data["service-account.pub"] = pem.EncodeToMemory(&pem.Block{Type: "RSA PUBLIC KEY", Bytes: public})
	if create {
		return r.Client.Create(ctx, current)
	}

	return r.Client.Patch(ctx, current, client.MergeFromWithOptions(before, client.MergeFromWithOptimisticLock{}))
}

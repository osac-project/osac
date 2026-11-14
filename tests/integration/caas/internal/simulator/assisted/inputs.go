// Copyright (c) 2026 Red Hat Inc.
// SPDX-License-Identifier: Apache-2.0

package assisted

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"strings"

	installv1 "github.com/openshift/assisted-service/api/hiveextension/v1beta1"
	assistedv1 "github.com/openshift/assisted-service/api/v1beta1"
	hivev1 "github.com/openshift/hive/apis/hive/v1"
	corev1 "k8s.io/api/core/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// validPullSecret checks the native Docker authentication input without exposing credentials.
func validPullSecret(ctx context.Context, reader client.Reader, namespace, name string) error {
	secret := &corev1.Secret{}
	if err := reader.Get(ctx, client.ObjectKey{Namespace: namespace, Name: name}, secret); err != nil {
		return err
	}

	var config struct {
		Auths map[string]map[string]any `json:"auths"`
	}

	if err := json.Unmarshal(secret.Data[corev1.DockerConfigJsonKey], &config); err != nil || len(config.Auths) == 0 {
		return fmt.Errorf("invalid pull secret")
	}

	for _, entry := range config.Auths {
		value, present := entry["auth"]
		if !present {
			continue
		}

		auth, ok := value.(string)
		if !ok {
			return fmt.Errorf("invalid pull secret authentication entry")
		}

		if auth == "" {
			continue
		}

		decoded, err := base64.StdEncoding.DecodeString(auth)
		if err != nil || !strings.Contains(string(decoded), ":") {
			return fmt.Errorf("invalid pull secret authentication format")
		}
	}

	return nil
}

// validateDayTwoInputs requires native credentials and ignition references consumed by Assisted.
// Kubeconfig transport and the ignition HTTP exchange are omitted with guest execution.
func (r *AgentReconciler) validateDayTwoInputs(
	ctx context.Context,
	cd *hivev1.ClusterDeployment,
	aci *installv1.AgentClusterInstall,
	agent *assistedv1.Agent,
) error {
	if ref := cd.Spec.PullSecretRef; ref != nil && ref.Name != "" {
		if err := validPullSecret(ctx, r.APIReader, cd.Namespace, ref.Name); err != nil {
			return err
		}
	} else if !cd.Spec.Installed {
		return fmt.Errorf("deployment pull secret reference is required")
	}

	if cd.Spec.ClusterMetadata == nil || cd.Spec.ClusterMetadata.AdminKubeconfigSecretRef.Name == "" {
		return fmt.Errorf("deployment kubeconfig reference is required")
	}

	access := &corev1.Secret{}
	if err := r.APIReader.Get(
		ctx,
		client.ObjectKey{Namespace: cd.Namespace, Name: cd.Spec.ClusterMetadata.AdminKubeconfigSecretRef.Name},
		access,
	); err != nil {
		return err
	}

	if len(access.Data["kubeconfig"]) == 0 {
		return fmt.Errorf("deployment kubeconfig data is missing")
	}

	if endpoint := aci.Spec.IgnitionEndpoint; endpoint != nil && endpoint.CaCertificateReference != nil {
		ref := endpoint.CaCertificateReference
		ca := &corev1.Secret{}
		if err := r.APIReader.Get(ctx, client.ObjectKey{Namespace: ref.Namespace, Name: ref.Name}, ca); err != nil {
			return err
		}

		if len(ca.Data[corev1.TLSCertKey]) == 0 {
			return fmt.Errorf("ignition CA data is missing")
		}
	}

	if ref := agent.Spec.IgnitionEndpointTokenReference; ref != nil {
		token := &corev1.Secret{}
		if err := r.APIReader.Get(ctx, client.ObjectKey{Namespace: ref.Namespace, Name: ref.Name}, token); err != nil {
			return err
		}

		if _, exists := token.Data["ignition-token"]; !exists {
			return fmt.Errorf("ignition token data is missing")
		}
	}

	return nil
}

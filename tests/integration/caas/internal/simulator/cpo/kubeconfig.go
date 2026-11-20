// Copyright (c) 2026 Red Hat Inc.
// SPDX-License-Identifier: Apache-2.0

package cpo

import (
	"context"
	"fmt"
	"net"
	"strconv"

	hypershiftv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/equality"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/client-go/tools/clientcmd"
	clientcmdapi "k8s.io/client-go/tools/clientcmd/api"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"

	"github.com/osac-project/osac/tests/integration/caas/internal/simulator/kube"
)

// ensureKubeconfig publishes the observed API endpoint without guest credentials.
// Allocation waits preserve existing data; updates retain Secret identity and unrelated fields.
func (r *HostedControlPlaneReconciler) ensureKubeconfig(
	ctx context.Context,
	controlPlane *hypershiftv1.HostedControlPlane,
	service *corev1.Service,
) (*hypershiftv1.KubeconfigSecretRef, error) {
	host, port := apiEndpoint(controlPlane, service)
	if host == "" {
		return nil, nil
	}

	data, err := guestKubeconfig(controlPlane.Name, host, port)
	if err != nil {
		return nil, err
	}

	ref := hypershiftv1.KubeconfigSecretRef{Name: "admin-kubeconfig", Key: "kubeconfig"}
	if controlPlane.Spec.KubeConfig != nil {
		ref = *controlPlane.Spec.KubeConfig
		if ref.Name == "" || ref.Key == "" {
			return nil, fmt.Errorf("control-plane kubeconfig reference requires a name and key")
		}
	}

	if ref.Name == ignitionCASecretName {
		return nil, fmt.Errorf("control-plane kubeconfig conflicts with ignition CA Secret %s", ref.Name)
	}

	key := client.ObjectKey{Namespace: controlPlane.Namespace, Name: ref.Name}
	secret := &corev1.Secret{}

	err = r.APIReader.Get(ctx, key, secret)
	create := apierrors.IsNotFound(err)
	if err != nil && !create {
		return nil, err
	}

	if create {
		secret.Namespace, secret.Name = key.Namespace, key.Name
		if err := controllerutil.SetControllerReference(controlPlane, secret, r.Client.Scheme()); err != nil {
			return nil, err
		}
	} else if !kube.MatchesController(secret, controlPlane, hypershiftv1.GroupVersion.WithKind("HostedControlPlane")) {
		return nil, fmt.Errorf("secret %s/%s belongs to another control plane", key.Namespace, key.Name)
	}

	if secret.DeletionTimestamp != nil {
		return nil, fmt.Errorf("control-plane kubeconfig Secret is terminating")
	}

	before := secret.DeepCopy()
	if secret.Data == nil {
		secret.Data = map[string][]byte{}
	}

	secret.Data[ref.Key] = data
	if create {
		err = r.Client.Create(ctx, secret)
	} else if !equality.Semantic.DeepEqual(before.Data, secret.Data) {
		err = r.Client.Patch(ctx, secret,
			client.MergeFromWithOptions(before, client.MergeFromWithOptimisticLock{}))
	}

	if err != nil {
		return nil, err
	}

	return &ref, nil
}

// guestKubeconfig encodes endpoint-consistent access data with empty authentication.
// The simulator models native publication without providing a guest API or credentials.
func guestKubeconfig(name, host string, port int32) ([]byte, error) {
	return clientcmd.Write(clientcmdapi.Config{
		Clusters: map[string]*clientcmdapi.Cluster{
			name: {Server: "https://" + net.JoinHostPort(host, strconv.Itoa(int(port)))},
		},
		AuthInfos: map[string]*clientcmdapi.AuthInfo{name: {}},
		Contexts: map[string]*clientcmdapi.Context{
			name: {Cluster: name, AuthInfo: name},
		},
		CurrentContext: name,
	})
}

// Copyright (c) 2026 Red Hat Inc.
// SPDX-License-Identifier: Apache-2.0

package hypershift

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net"
	"strconv"

	ignitiontypes "github.com/coreos/ignition/v2/config/v3_2/types"
	"github.com/google/uuid"
	hypershiftv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/equality"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	capiv1 "sigs.k8s.io/cluster-api/api/v1beta1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"

	"github.com/osac-project/osac/tests/integration/caas/internal/simulator/kube"
)

// ignitionEndpoint observes the allocated Service port for NodePort publishing.
// Route admission and network serving remain synthetic in this guestless installation.
func (r *HostedClusterReconciler) ignitionEndpoint(
	ctx context.Context,
	cluster *hypershiftv1.HostedCluster,
) (string, error) {
	for _, mapping := range cluster.Spec.Services {
		if mapping.Service != hypershiftv1.Ignition {
			continue
		}

		switch mapping.Type {
		case hypershiftv1.Route:
			if mapping.Route != nil && mapping.Route.Hostname != "" {
				return mapping.Route.Hostname, nil
			}

			return "ignition.caas-it.invalid", nil
		case hypershiftv1.NodePort:
			return r.ignitionNodePortEndpoint(ctx, cluster, mapping.NodePort)
		default:
			return "", fmt.Errorf("unsupported ignition publishing strategy %q", mapping.Type)
		}
	}

	return "", fmt.Errorf("ignition publishing strategy is required")
}

// ignitionNodePortEndpoint prefers the proxy Service and waits for an assigned port.
func (r *HostedClusterReconciler) ignitionNodePortEndpoint(
	ctx context.Context, cluster *hypershiftv1.HostedCluster, nodePort *hypershiftv1.NodePortPublishingStrategy,
) (string, error) {
	if nodePort == nil {
		return "", fmt.Errorf("ignition NodePort metadata is required")
	}

	for _, name := range []string{"ignition-server-proxy", "ignition-server"} {
		service := &corev1.Service{}

		err := r.APIReader.Get(ctx, client.ObjectKey{Namespace: cluster.Namespace + "-" + cluster.Name, Name: name}, service)
		if apierrors.IsNotFound(err) {
			continue
		}

		if err != nil {
			return "", err
		}

		if len(service.Spec.Ports) > 0 && service.Spec.Ports[0].NodePort > 0 {
			return net.JoinHostPort(nodePort.Address, strconv.Itoa(int(service.Spec.Ports[0].NodePort))), nil
		}

		return cluster.Status.IgnitionEndpoint, nil
	}

	return cluster.Status.IgnitionEndpoint, nil
}

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

	source := &corev1.Secret{}
	if err := r.APIReader.Get(
		ctx,
		client.ObjectKey{Namespace: hc.Namespace, Name: hc.Spec.SSHKey.Name},
		source,
	); err != nil {
		return err
	}

	key, exists := source.Data["id_rsa.pub"]
	if !exists {
		return fmt.Errorf("SSH Secret must contain id_rsa.pub")
	}

	destination := &corev1.Secret{ObjectMeta: metav1.ObjectMeta{Namespace: namespace, Name: "ssh-key"}}
	_, err := controllerutil.CreateOrUpdate(ctx, r.Client, destination, func() error {
		metav1.SetMetaDataAnnotation(&destination.ObjectMeta, kube.HostedClusterAnnotation, hc.Namespace+"/"+hc.Name)

		destination.Type = corev1.SecretTypeOpaque
		if destination.Data == nil {
			destination.Data = map[string][]byte{}
		}

		destination.Data["id_rsa.pub"] = key

		return nil
	})

	return err
}

// bootstrapData validates the published endpoint and CA, ensures the original
// Cluster's token Secret, and builds the authenticated Ignition document.
// Stable names cover scale-only InPlace pools; release rollouts remain outside this model.
func (r *NodePoolReconciler) bootstrapData(
	ctx context.Context,
	pool *hypershiftv1.NodePool,
	cluster *capiv1.Cluster,
) ([]byte, error) {
	hc := &hypershiftv1.HostedCluster{}
	if err := r.APIReader.Get(
		ctx,
		client.ObjectKey{Namespace: pool.Namespace, Name: pool.Spec.ClusterName},
		hc,
	); err != nil {
		return nil, err
	}

	if hc.Status.IgnitionEndpoint == "" {
		return nil, fmt.Errorf("ignition endpoint is pending")
	}

	ca := &corev1.Secret{}
	if err := r.APIReader.Get(
		ctx,
		client.ObjectKey{Namespace: cluster.Namespace, Name: "ignition-server-ca-cert"},
		ca,
	); err != nil {
		return nil, err
	}

	if len(ca.Data[corev1.TLSCertKey]) == 0 {
		return nil, fmt.Errorf("ignition CA has no certificate")
	}

	cp := &hypershiftv1.HostedControlPlane{}

	ref := cluster.Spec.ControlPlaneRef
	if ref == nil || ref.APIVersion != hypershiftv1.GroupVersion.String() || ref.Kind != "HostedControlPlane" ||
		(ref.Namespace != "" && ref.Namespace != cluster.Namespace) {
		return nil, fmt.Errorf("cluster requires a same-namespace HostedControlPlane reference")
	}

	if err := r.APIReader.Get(
		ctx,
		client.ObjectKey{Namespace: cluster.Namespace, Name: cluster.Spec.ControlPlaneRef.Name},
		cp,
	); err != nil {
		return nil, err
	}

	if err := validateProvider(cp, cluster); err != nil {
		return nil, err
	}

	if !kube.MatchesController(ca, cp, hypershiftv1.GroupVersion.WithKind("HostedControlPlane")) {
		return nil, fmt.Errorf("ignition CA belongs to another control plane")
	}

	desired := &corev1.Secret{ObjectMeta: metav1.ObjectMeta{
		Namespace: cluster.Namespace,
		Name:      pool.Name + "-token",
		Annotations: map[string]string{
			kube.NodePoolAnnotation:                   pool.Namespace + "/" + pool.Name,
			"hypershift.openshift.io/ignition-config": "true",
		},
		Labels: map[string]string{kube.ManagedByLabel: kube.ManagerName},
	}}
	if err := controllerutil.SetControllerReference(cluster, desired, r.Client.Scheme()); err != nil {
		return nil, err
	}

	token := &corev1.Secret{}
	err := r.APIReader.Get(ctx, client.ObjectKeyFromObject(desired), token)
	if apierrors.IsNotFound(err) {
		token = desired
		token.Data = map[string][]byte{"token": []byte(uuid.NewString())}
		if err := r.Client.Create(ctx, token); err != nil {
			return nil, err
		}
	} else if err != nil {
		return nil, err
	} else if err := validateOutput(token, desired); err != nil {
		return nil, err
	}

	if token.DeletionTimestamp != nil || len(token.Data["token"]) == 0 {
		return nil, fmt.Errorf("ignition token is unavailable")
	}

	config := ignitiontypes.Config{Ignition: ignitiontypes.Ignition{
		Version: "3.2.0",
		Config: ignitiontypes.IgnitionConfig{Merge: []ignitiontypes.Resource{
			{
				Source: new("https://" + hc.Status.IgnitionEndpoint + "/ignition"),
				HTTPHeaders: []ignitiontypes.HTTPHeader{
					{Name: "Authorization", Value: new("Bearer " + base64.StdEncoding.EncodeToString(token.Data["token"]))},
				},
			},
		}},
		Security: ignitiontypes.Security{
			TLS: ignitiontypes.TLS{
				CertificateAuthorities: []ignitiontypes.Resource{
					{Source: new("data:text/plain;base64," + base64.StdEncoding.EncodeToString(ca.Data[corev1.TLSCertKey]))},
				},
			},
		},
	}}

	return json.Marshal(config)
}

// ensurePullSecret copies the native pull-secret input into the control-plane namespace.
// Existing copies retain unrelated data and require the same original HCP owner.
func (r *HostedClusterReconciler) ensurePullSecret(
	ctx context.Context,
	hc *hypershiftv1.HostedCluster,
	cp *hypershiftv1.HostedControlPlane,
) error {
	source := &corev1.Secret{}
	if err := r.APIReader.Get(
		ctx,
		client.ObjectKey{Namespace: hc.Namespace, Name: hc.Spec.PullSecret.Name},
		source,
	); err != nil {
		return err
	}

	desired := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: cp.Namespace,
			Name:      "pull-secret",
			Labels:    map[string]string{kube.ManagedByLabel: kube.ManagerName},
		},
	}
	if err := controllerutil.SetControllerReference(cp, desired, r.Client.Scheme()); err != nil {
		return err
	}

	current := &corev1.Secret{}

	err := r.APIReader.Get(ctx, client.ObjectKeyFromObject(desired), current)
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
		return fmt.Errorf("control-plane pull secret is deleting")
	}

	before := current.DeepCopy()
	current.Type = corev1.SecretTypeDockerConfigJson
	if current.Data == nil {
		current.Data = map[string][]byte{}
	}

	current.Data[corev1.DockerConfigJsonKey] = source.Data[corev1.DockerConfigJsonKey]
	if create {
		return r.Client.Create(ctx, current)
	}

	if equality.Semantic.DeepEqual(before.Data, current.Data) && before.Type == current.Type {
		return nil
	}

	return r.Client.Patch(ctx, current, client.MergeFromWithOptions(before, client.MergeFromWithOptimisticLock{}))
}

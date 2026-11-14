// Copyright (c) 2026 Red Hat Inc.
// SPDX-License-Identifier: Apache-2.0

package scenario

import (
	"context"
	"fmt"
	"net/url"
	"strings"

	assistedv1 "github.com/openshift/assisted-service/api/v1beta1"
	hypershiftv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	capiv1 "sigs.k8s.io/cluster-api/api/v1beta1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/osac-project/osac/tests/integration/caas/internal/simulator/assisted"
	"github.com/osac-project/osac/tests/integration/caas/internal/simulator/kube"
)

// Responses reads shared case settings and supplies each component's response input.
// The caller supplies an uncached Reader. Settings select responses and store no progress.
type Responses struct {
	Reader    client.Reader
	Namespace string
}

// read finds the sole case bound to a native resource UID.
// Nil means unconfigured; callers validate missing data in a bound case.
func (r Responses) read(ctx context.Context, kind string, uid types.UID) (*caseSettings, error) {
	if uid == "" {
		return nil, fmt.Errorf("native root UID is required")
	}

	_, cases, err := readSettings(ctx, r.Reader, r.Namespace)
	if apierrors.IsNotFound(err) {
		return nil, nil
	}

	if err != nil {
		return nil, fmt.Errorf("read simulator settings: %w", err)
	}

	var found *caseSettings

	for _, entry := range cases {
		if entry.Roots[kind] != uid {
			continue
		}

		if found != nil {
			return nil, fmt.Errorf("multiple settings match native root %s", uid)
		}

		found = &entry
	}

	return found, nil
}

// DiscoveryURL supplies only Assisted's discovery endpoint; other controls are independent.
func (r Responses) DiscoveryURL(ctx context.Context, infra *assistedv1.InfraEnv) (string, error) {
	settings, err := r.read(ctx, infraEnvRoot, infra.GetUID())
	if err != nil || settings == nil {
		return "", err
	}

	endpoint, err := url.Parse(settings.DiscoveryURL)
	if err != nil || endpoint.Host == "" || (endpoint.Scheme != "http" && endpoint.Scheme != "https") {
		return "", fmt.Errorf("discovery ignition URL must be an absolute HTTP URL")
	}

	return settings.DiscoveryURL, nil
}

// Installation selects an Assisted installation outcome without decoding discovery data.
func (r Responses) Installation(
	ctx context.Context,
	infra *assistedv1.InfraEnv,
) (assisted.InstallationResponse, error) {
	profile, found, err := r.profile(ctx, infraEnvRoot, infra.GetUID())
	if err != nil || !found {
		return "", err
	}

	switch profile {
	case InstallationFailed:
		return assisted.InstallationFailed, nil
	case InstallationStalled:
		return assisted.InstallationHeld, nil
	case Normal, DetachStalled, ControlPlaneStalled:
		return assisted.InstallationComplete, nil
	default:
		return "", fmt.Errorf("unsupported installation profile %q", profile)
	}
}

// Availability follows the HostedControlPlane's original Cluster to its case settings.
// The second result reports whether those settings exist.
func (r Responses) Availability(ctx context.Context, cp *hypershiftv1.HostedControlPlane) (bool, bool, error) {
	owner := metav1.GetControllerOf(cp)
	if owner == nil || owner.Kind != "Cluster" || owner.APIVersion != capiv1.GroupVersion.String() {
		return false, false, fmt.Errorf("control plane has no Cluster owner")
	}

	cluster := &capiv1.Cluster{}

	err := r.Reader.Get(ctx, client.ObjectKey{Namespace: cp.Namespace, Name: owner.Name}, cluster)
	if apierrors.IsNotFound(err) {
		return false, false, nil
	}

	if err != nil {
		return false, false, err
	}

	ref := cluster.Spec.ControlPlaneRef
	if cluster.GetUID() == "" || cp.GetNamespace() != cluster.GetNamespace() ||
		owner.UID != cluster.GetUID() || owner.Name != cluster.GetName() ||
		ref == nil || ref.APIVersion != hypershiftv1.GroupVersion.String() || ref.Kind != "HostedControlPlane" ||
		ref.Namespace != cp.Namespace || ref.Name != cp.Name || (ref.UID != "" && ref.UID != cp.UID) {
		return false, false, fmt.Errorf("control plane differs from its original Cluster")
	}

	profile, found, err := r.clusterProfile(ctx, cluster)

	return profile != ControlPlaneStalled, found, err
}

// HoldDetach reads the detach response for the Cluster's bound test case.
// CAP-Agent calls this while releasing a claimed Agent.
// Missing case settings allow cleanup to proceed.
func (r Responses) HoldDetach(ctx context.Context, cluster metav1.Object) (bool, error) {
	profile, _, err := r.clusterProfile(ctx, cluster)

	return profile == DetachStalled, err
}

// clusterProfile follows the native Cluster link and selects only the case
// bound to this Cluster incarnation and its live HostedCluster incarnation.
func (r Responses) clusterProfile(ctx context.Context, cluster metav1.Object) (Profile, bool, error) {
	namespace, name, ok := strings.Cut(cluster.GetAnnotations()[kube.HostedClusterAnnotation], "/")
	if !ok || namespace == "" || name == "" {
		return "", false, fmt.Errorf("cluster has no valid HostedCluster link")
	}

	hc := &hypershiftv1.HostedCluster{}

	err := r.Reader.Get(ctx, client.ObjectKey{Namespace: namespace, Name: name}, hc)
	if apierrors.IsNotFound(err) {
		return "", false, nil
	}

	if err != nil {
		return "", false, err
	}

	infraID := hc.Spec.InfraID
	if infraID == "" {
		infraID = hc.GetName()
	}

	if cluster.GetNamespace() != namespace+"-"+name || cluster.GetName() != infraID {
		return "", false, fmt.Errorf("cluster differs from its linked HostedCluster")
	}

	settings, err := r.read(ctx, clusterRoot, cluster.GetUID())
	if err != nil || settings == nil {
		return "", false, err
	}

	if settings.Roots[hostedClusterRoot] != hc.GetUID() {
		return "", false, nil
	}

	return settings.Profile, true, validateProfile(settings.Profile)
}

// profile validates the response choice; DiscoveryURL validates the discovery endpoint.
func (r Responses) profile(ctx context.Context, key string, uid types.UID) (Profile, bool, error) {
	settings, err := r.read(ctx, key, uid)
	if err != nil || settings == nil {
		return "", false, err
	}

	return settings.Profile, true, validateProfile(settings.Profile)
}

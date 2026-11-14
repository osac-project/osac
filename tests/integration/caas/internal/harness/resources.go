// Copyright (c) 2026 Red Hat Inc.
// SPDX-License-Identifier: Apache-2.0

package harness

import (
	metal3v1 "github.com/metal3-io/baremetal-operator/apis/metal3.io/v1alpha1"
	installv1 "github.com/openshift/assisted-service/api/hiveextension/v1beta1"
	assistedv1 "github.com/openshift/assisted-service/api/v1beta1"
	agentv1 "github.com/openshift/cluster-api-provider-agent/api/v1beta1"
	hivev1 "github.com/openshift/hive/apis/hive/v1"
	hypershiftv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	bmfv1 "github.com/osac-project/osac/bare-metal-fulfillment-operator/api/v1alpha1"
	osacv1 "github.com/osac-project/osac/osac-operator/api/v1alpha1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/runtime"
	capiv1 "sigs.k8s.io/cluster-api/api/v1beta1"
)

const (
	HostedClusterAnnotation = "hypershift.openshift.io/cluster"
	TenantAnnotation        = "osac.openshift.io/tenant"
	OrderLabel              = "osac.openshift.io/clusterorder"
	WorkerLabel             = "osac.openshift.io/worker-name"
)

// apiScheme registers the native types observed by the integration harness.
func apiScheme() (*runtime.Scheme, error) {
	scheme := runtime.NewScheme()
	for _, add := range []func(*runtime.Scheme) error{
		corev1.AddToScheme, metal3v1.AddToScheme, assistedv1.AddToScheme, installv1.AddToScheme,
		agentv1.AddToScheme, hivev1.AddToScheme, hypershiftv1.AddToScheme, capiv1.AddToScheme,
		bmfv1.AddToScheme, osacv1.AddToScheme,
	} {
		if err := add(scheme); err != nil {
			return nil, err
		}
	}

	return scheme, nil
}

// Copyright (c) 2026 Red Hat Inc.
// SPDX-License-Identifier: Apache-2.0

package harness

import (
	"context"
	"fmt"
	"log/slog"

	routev1 "github.com/openshift/api/route/v1"
	installv1 "github.com/openshift/assisted-service/api/hiveextension/v1beta1"
	assistedv1 "github.com/openshift/assisted-service/api/v1beta1"
	agentv1 "github.com/openshift/cluster-api-provider-agent/api/v1beta1"
	hivev1 "github.com/openshift/hive/apis/hive/v1"
	hypershiftv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
	capiv1 "sigs.k8s.io/cluster-api/api/v1beta1"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// CheckNativeResourcesGone checks dedicated-installation inputs and provider outputs.
func CheckNativeResourcesGone(ctx context.Context, environment *Environment) error {
	if err := checkNativeInputsGone(ctx, environment); err != nil {
		return err
	}

	if err := checkControllerResourcesGone(ctx, environment); err != nil {
		return err
	}

	return checkNativeOutputsGone(ctx, environment)
}

// checkNativeInputsGone checks cluster-wide roots and hub-scoped discovery inputs.
func checkNativeInputsGone(ctx context.Context, e *Environment) error {
	hub := client.InNamespace(e.Namespace)
	if err := checkEmpty(ctx, e.Client, &hypershiftv1.HostedClusterList{}, "hostedclusters"); err != nil {
		return err
	}

	if err := checkEmpty(ctx, e.Client, &hypershiftv1.NodePoolList{}, "nodepools"); err != nil {
		return err
	}

	if err := checkEmpty(ctx, e.Client, &assistedv1.InfraEnvList{}, "infraenvs", hub); err != nil {
		return err
	}

	return checkEmpty(ctx, e.Client, &assistedv1.AgentList{}, "agents", hub)
}

// checkControllerResourcesGone checks provider records across all working namespaces.
func checkControllerResourcesGone(ctx context.Context, e *Environment) error {
	if err := checkEmpty(ctx, e.Client, &hypershiftv1.HostedControlPlaneList{}, "native hostedcontrolplanes"); err != nil {
		return err
	}

	if err := checkEmpty(ctx, e.Client, &capiv1.ClusterList{}, "native clusters"); err != nil {
		return err
	}

	if err := checkEmpty(ctx, e.Client, &capiv1.MachineSetList{}, "native machinesets"); err != nil {
		return err
	}

	if err := checkEmpty(ctx, e.Client, &capiv1.MachineList{}, "native machines"); err != nil {
		return err
	}

	if err := checkEmpty(ctx, e.Client, &agentv1.AgentClusterList{}, "native agentclusters"); err != nil {
		return err
	}

	if err := checkEmpty(ctx, e.Client, &agentv1.AgentMachineList{}, "native agentmachines"); err != nil {
		return err
	}

	if err := checkEmpty(ctx, e.Client, &agentv1.AgentMachineTemplateList{}, "native agentmachinetemplates"); err != nil {
		return err
	}

	if err := checkEmpty(ctx, e.Client, &hivev1.ClusterDeploymentList{}, "native clusterdeployments"); err != nil {
		return err
	}

	return checkEmpty(ctx, e.Client, &installv1.AgentClusterInstallList{}, "native agentclusterinstalls")
}

// checkNativeOutputsGone checks native associations, including registrations outside the hub.
// This read-only gate preserves unrelated infrastructure and propagates every listing error.
func checkNativeOutputsGone(ctx context.Context, e *Environment) error {
	for _, list := range []client.ObjectList{
		&assistedv1.AgentList{}, &corev1.NamespaceList{}, &corev1.ServiceList{}, &corev1.SecretList{}, &routev1.RouteList{},
	} {
		if err := e.Client.List(ctx, list); err != nil {
			return err
		}

		objects, err := meta.ExtractList(list)
		if err != nil {
			return err
		}

		for _, object := range objects {
			metadata, err := meta.Accessor(object)
			if err != nil {
				return err
			}

			if hasNativeProviderAssociation(metadata) {
				return fmt.Errorf("native provider output %T %s/%s remains", object, metadata.GetNamespace(), metadata.GetName())
			}
		}
	}

	return nil
}

// hasNativeProviderAssociation recognizes source annotations and complete native owner references.
// An absent parent still leaves useful cleanup evidence on its surviving child.
func hasNativeProviderAssociation(object metav1.Object) bool {
	annotations := object.GetAnnotations()
	if annotations[HostedClusterAnnotation] != "" || annotations["hypershift.openshift.io/nodePool"] != "" {
		return true
	}

	for _, owner := range object.GetOwnerReferences() {
		if owner.Name == "" || owner.UID == "" {
			continue
		}

		switch schema.FromAPIVersionAndKind(owner.APIVersion, owner.Kind) {
		case hypershiftv1.GroupVersion.WithKind("HostedCluster"),
			hypershiftv1.GroupVersion.WithKind("HostedControlPlane"),
			hypershiftv1.GroupVersion.WithKind("NodePool"),
			capiv1.GroupVersion.WithKind("Cluster"),
			capiv1.GroupVersion.WithKind("MachineSet"),
			capiv1.GroupVersion.WithKind("Machine"),
			agentv1.GroupVersion.WithKind("AgentCluster"),
			agentv1.GroupVersion.WithKind("AgentMachine"),
			assistedv1.GroupVersion.WithKind("InfraEnv"),
			hivev1.SchemeGroupVersion.WithKind("ClusterDeployment"):
			return true
		}
	}

	return false
}

// checkEmpty reports remaining objects while preserving read errors and caller-selected scopes.
func checkEmpty(
	ctx context.Context, reader client.Reader, list client.ObjectList, resource string, opts ...client.ListOption,
) error {
	if err := reader.List(ctx, list, opts...); err != nil {
		return err
	}

	if count := meta.LenList(list); count != 0 {
		slog.DebugContext(ctx, "native resources remain", "resource", resource, "count", count)
		return fmt.Errorf("%d %s remain", count, resource)
	}

	return nil
}

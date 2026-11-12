// Copyright (c) 2026 Red Hat Inc.
// SPDX-License-Identifier: Apache-2.0

package harness

import (
	"context"
	"fmt"
	"log/slog"

	installv1 "github.com/openshift/assisted-service/api/hiveextension/v1beta1"
	assistedv1 "github.com/openshift/assisted-service/api/v1beta1"
	agentv1 "github.com/openshift/cluster-api-provider-agent/api/v1beta1"
	hivev1 "github.com/openshift/hive/apis/hive/v1"
	hypershiftv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/meta"
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

	return checkSimulatorOutputsGone(ctx, environment)
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

// checkSimulatorOutputsGone checks labeled outputs, including cross-namespace Agents.
func checkSimulatorOutputsGone(ctx context.Context, e *Environment) error {
	managed := client.MatchingLabels{"app.kubernetes.io/managed-by": "osac-provider-simulator"}

	if err := checkEmpty(ctx, e.Client, &assistedv1.AgentList{}, "simulator-owned agents", managed); err != nil {
		return err
	}

	if err := checkEmpty(ctx, e.Client, &corev1.NamespaceList{}, "simulator-owned namespaces", managed); err != nil {
		return err
	}

	if err := checkEmpty(ctx, e.Client, &corev1.ServiceList{}, "simulator-owned services", managed); err != nil {
		return err
	}

	return checkEmpty(ctx, e.Client, &corev1.SecretList{}, "simulator-owned secrets", managed)
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

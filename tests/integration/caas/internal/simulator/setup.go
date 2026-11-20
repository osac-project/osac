// Copyright (c) 2026 Red Hat Inc.
// SPDX-License-Identifier: Apache-2.0

package simulator

import (
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/osac-project/osac/tests/integration/caas/internal/simulator/assisted"
	"github.com/osac-project/osac/tests/integration/caas/internal/simulator/capagent"
	"github.com/osac-project/osac/tests/integration/caas/internal/simulator/capi"
	"github.com/osac-project/osac/tests/integration/caas/internal/simulator/cpo"
	"github.com/osac-project/osac/tests/integration/caas/internal/simulator/hcco"
	"github.com/osac-project/osac/tests/integration/caas/internal/simulator/hypershift"
	"github.com/osac-project/osac/tests/integration/caas/internal/simulator/kube"
	"github.com/osac-project/osac/tests/integration/caas/internal/simulator/loadbalancer"
	"github.com/osac-project/osac/tests/integration/caas/internal/simulator/metal3"
	"github.com/osac-project/osac/tests/integration/caas/internal/simulator/scenario"
)

// setupControllers registers each component's controllers with the manager.
// Each controller reads Kubernetes and its configured inputs to decide its own response.
func setupControllers(mgr ctrl.Manager, reader client.Reader, cfg Config) error {
	scope := kube.Scope{AgentNamespaces: cfg.AgentNamespaces}
	responses := scenario.Responses{Reader: reader, Namespace: cfg.SettingsNamespace}

	infraEnv := &assisted.InfraEnvReconciler{
		Client:       mgr.GetClient(),
		APIReader:    reader,
		Scope:        scope,
		DiscoveryURL: responses.DiscoveryURL,
	}
	if err := infraEnv.SetupWithManager(mgr); err != nil {
		return err
	}

	hosts := &metal3.HostReconciler{
		Client:    mgr.GetClient(),
		APIReader: reader,
		Namespace: cfg.Metal3Namespace,
	}
	if err := hosts.SetupWithManager(mgr); err != nil {
		return err
	}

	agents := &assisted.AgentReconciler{
		Client:       mgr.GetClient(),
		APIReader:    reader,
		Scope:        scope,
		Installation: responses.Installation,
	}

	agentMachines := &capagent.AgentMachineReconciler{
		Client:     mgr.GetClient(),
		APIReader:  reader,
		Scope:      scope,
		HoldDetach: responses.HoldDetach,
	}
	hostedClusters := &hypershift.HostedClusterReconciler{Client: mgr.GetClient(), APIReader: reader, Scope: scope}
	if err := agents.SetupWithManager(mgr); err != nil {
		return err
	}

	if err := agentMachines.SetupWithManager(mgr); err != nil {
		return err
	}

	deployments := &assisted.DeploymentReconciler{Client: mgr.GetClient(), APIReader: reader, Scope: scope}
	if err := deployments.SetupWithManager(mgr); err != nil {
		return err
	}

	agentClusters := &capagent.AgentClusterReconciler{Client: mgr.GetClient(), APIReader: reader, Scope: scope}
	if err := agentClusters.SetupWithManager(mgr); err != nil {
		return err
	}

	if err := hostedClusters.SetupWithManager(mgr); err != nil {
		return err
	}

	nodePools := &hypershift.NodePoolReconciler{Client: mgr.GetClient(), APIReader: reader, Scope: scope}
	if err := nodePools.SetupWithManager(mgr); err != nil {
		return err
	}

	controlPlanes := &cpo.HostedControlPlaneReconciler{
		Client:               mgr.GetClient(),
		APIReader:            reader,
		Scope:                scope,
		Availability:         responses.Availability,
		DefaultIngressDomain: cfg.DefaultIngressDomain,
	}
	if err := controlPlanes.SetupWithManager(mgr); err != nil {
		return err
	}

	guestStatus := &hcco.StatusReconciler{Client: mgr.GetClient(), APIReader: reader, Scope: scope}
	if err := guestStatus.SetupWithManager(mgr); err != nil {
		return err
	}

	services := &loadbalancer.ServiceReconciler{Client: mgr.GetClient(), APIReader: reader}
	if err := services.SetupWithManager(mgr); err != nil {
		return err
	}

	return capi.SetupWithManager(mgr, reader)
}

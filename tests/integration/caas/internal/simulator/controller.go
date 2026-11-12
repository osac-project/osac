// Copyright (c) 2026 Red Hat Inc.
// SPDX-License-Identifier: Apache-2.0

package simulator

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"sync/atomic"
	"time"

	metal3v1 "github.com/metal3-io/baremetal-operator/apis/metal3.io/v1alpha1"
	installv1 "github.com/openshift/assisted-service/api/hiveextension/v1beta1"
	assistedv1 "github.com/openshift/assisted-service/api/v1beta1"
	agentv1 "github.com/openshift/cluster-api-provider-agent/api/v1beta1"
	hivev1 "github.com/openshift/hive/apis/hive/v1"
	hypershiftv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/rest"
	capiv1 "sigs.k8s.io/cluster-api/api/v1beta1"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/cache"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/healthz"
	"sigs.k8s.io/controller-runtime/pkg/manager"
	metricsserver "sigs.k8s.io/controller-runtime/pkg/metrics/server"

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

	infra := &assisted.InfraEnvReconciler{
		Client:       mgr.GetClient(),
		APIReader:    reader,
		Scope:        scope,
		DiscoveryURL: responses.DiscoveryURL,
	}
	if err := infra.SetupWithManager(mgr); err != nil {
		return err
	}

	host := &metal3.HostReconciler{
		Client:    mgr.GetClient(),
		APIReader: reader,
		Namespace: cfg.Metal3Namespace,
	}
	if err := host.SetupWithManager(mgr); err != nil {
		return err
	}

	a := &assisted.AgentReconciler{
		Client:       mgr.GetClient(),
		APIReader:    reader,
		Scope:        scope,
		Installation: responses.Installation,
	}

	m := &capagent.AgentMachineReconciler{
		Client:     mgr.GetClient(),
		APIReader:  reader,
		Scope:      scope,
		HoldDetach: responses.HoldDetach,
	}
	h := &hypershift.HostedClusterReconciler{Client: mgr.GetClient(), APIReader: reader, Scope: scope}
	if err := a.SetupWithManager(mgr); err != nil {
		return err
	}

	if err := m.SetupWithManager(mgr); err != nil {
		return err
	}

	deployment := &assisted.DeploymentReconciler{Client: mgr.GetClient(), APIReader: reader, Scope: scope}
	if err := deployment.SetupWithManager(mgr); err != nil {
		return err
	}

	ac := &capagent.AgentClusterReconciler{Client: mgr.GetClient(), APIReader: reader, Scope: scope}
	if err := ac.SetupWithManager(mgr); err != nil {
		return err
	}

	if err := h.SetupWithManager(mgr); err != nil {
		return err
	}

	np := &hypershift.NodePoolReconciler{Client: mgr.GetClient(), APIReader: reader, Scope: scope}
	if err := np.SetupWithManager(mgr); err != nil {
		return err
	}

	cp := &cpo.HostedControlPlaneReconciler{
		Client:       mgr.GetClient(),
		APIReader:    reader,
		Scope:        scope,
		Availability: responses.Availability,
	}
	if err := cp.SetupWithManager(mgr); err != nil {
		return err
	}

	guest := &hcco.StatusReconciler{Client: mgr.GetClient(), APIReader: reader, Scope: scope}
	if err := guest.SetupWithManager(mgr); err != nil {
		return err
	}

	lb := &loadbalancer.ServiceReconciler{Client: mgr.GetClient(), APIReader: reader}
	if err := lb.SetupWithManager(mgr); err != nil {
		return err
	}

	return capi.SetupWithManager(mgr, reader)
}

// prepareCaches registers a Kubernetes watch and local cache for each watched kind.
// Registering them before startup makes the manager wait for all initial reads
// before reporting readiness.
func prepareCaches(ctx context.Context, mgr ctrl.Manager) error {
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()

	for _, object := range []client.Object{
		&assistedv1.InfraEnv{},
		&assistedv1.Agent{},
		&installv1.AgentClusterInstall{},
		&hivev1.ClusterDeployment{},
		&capiv1.Cluster{},
		&capiv1.MachineSet{},
		&capiv1.Machine{},
		&agentv1.AgentCluster{},
		&agentv1.AgentMachine{},
		&agentv1.AgentMachineTemplate{},
		&hypershiftv1.HostedControlPlane{},
		&corev1.Service{},
		&corev1.Secret{},
		&hypershiftv1.HostedCluster{},
		&hypershiftv1.NodePool{},
		&metal3v1.BareMetalHost{},
	} {
		if _, err := mgr.GetCache().GetInformer(ctx, object, cache.BlockUntilSynced(false)); err != nil {
			return err
		}
	}

	return nil
}

// Run starts the provider controllers and serves health checks on port 8081.
// It sets API-request and cache-startup deadlines, then waits for cancellation or a
// manager error. Controllers read their progress from Kubernetes after a restart.
func Run(ctx context.Context, cfg Config) error {
	if err := cfg.validate(); err != nil {
		return err
	}

	logger := ctrl.LoggerFrom(ctx)
	logger.Info(
		"starting native provider controllers",
		"agentNamespaces",
		cfg.AgentNamespaces,
		"settingsNamespace",
		cfg.SettingsNamespace,
	)

	scheme := runtime.NewScheme()
	for _, add := range []func(*runtime.Scheme) error{
		corev1.AddToScheme,
		metal3v1.AddToScheme,
		assistedv1.AddToScheme,
		installv1.AddToScheme,
		hivev1.AddToScheme,
		capiv1.AddToScheme,
		agentv1.AddToScheme,
		hypershiftv1.AddToScheme,
	} {
		if err := add(scheme); err != nil {
			return err
		}
	}

	// Bound individual API calls without changing the caller's connection settings.
	connection := rest.CopyConfig(cfg.RESTConfig)
	connection.Timeout = 20 * time.Second

	direct, err := client.New(connection, client.Options{Scheme: scheme})
	if err != nil {
		return err
	}

	// Keep watches open. Scope discovery watches to Agent namespaces and BMH watches
	// to inventory; other kinds span namespaces to follow control-plane resources.
	watchConnection := rest.CopyConfig(connection)
	watchConnection.Timeout = 0

	namespaces := map[string]cache.Config{}
	for _, namespace := range cfg.AgentNamespaces {
		namespaces[namespace] = cache.Config{}
	}

	cacheOptions := cache.Options{ReaderFailOnMissingInformer: true, ByObject: map[client.Object]cache.ByObject{
		&assistedv1.Agent{}:       {Namespaces: namespaces},
		&assistedv1.InfraEnv{}:    {Namespaces: namespaces},
		&metal3v1.BareMetalHost{}: {Namespaces: map[string]cache.Config{cfg.Metal3Namespace: {}}},
	}}

	mgr, err := manager.New(
		watchConnection,
		manager.Options{
			Scheme:                  scheme,
			Logger:                  logger,
			Cache:                   cacheOptions,
			Metrics:                 metricsserver.Options{BindAddress: "0"},
			HealthProbeBindAddress:  ":8081",
			GracefulShutdownTimeout: new(10 * time.Second),
		},
	)
	if err != nil {
		return err
	}

	// Register every controller and cache before the manager starts its watches.
	if err = setupControllers(mgr, direct, cfg); err != nil {
		return err
	}

	if err = prepareCaches(ctx, mgr); err != nil {
		return err
	}

	// Cancel startup if the caches cannot start within the allowed time.
	runContext, cancel := context.WithCancelCause(ctx)
	defer cancel(nil)

	startupError := fmt.Errorf("simulator caches did not start within 30 seconds")

	startup := time.AfterFunc(30*time.Second, func() {
		logger.Error(startupError, "cache startup timed out")
		cancel(startupError)
	})
	defer startup.Stop()

	// The manager starts this runnable after its caches sync. Clear readiness on stop.
	var ready atomic.Bool

	if err = mgr.Add(manager.RunnableFunc(func(ctx context.Context) error {
		startup.Stop()
		ready.Store(true)
		logger.Info("native provider caches synchronized; simulator ready")
		<-ctx.Done()
		ready.Store(false)
		logger.Info("simulator readiness cleared during shutdown")

		return nil
	})); err != nil {
		return err
	}

	if err = addHealthChecks(mgr, &ready); err != nil {
		return err
	}

	// Run until shutdown and keep a cache-startup timeout visible in the returned error.
	err = mgr.Start(runContext)
	logger.Info("native provider controllers stopped", "failed", err != nil)

	if errors.Is(context.Cause(runContext), startupError) {
		return errors.Join(err, startupError)
	}

	return err
}

// addHealthChecks keeps readiness tied to synchronized caches throughout the manager lifecycle.
func addHealthChecks(mgr ctrl.Manager, ready *atomic.Bool) error {
	if err := mgr.AddHealthzCheck("process", healthz.Ping); err != nil {
		return err
	}

	// A held provider response still counts as ready once the caches have synced.
	return mgr.AddReadyzCheck("cache", func(*http.Request) error {
		if !ready.Load() {
			return fmt.Errorf("starting simulator caches")
		}

		return nil
	})
}

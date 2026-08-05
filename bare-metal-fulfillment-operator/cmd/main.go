/*
Copyright 2026.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package main

import (
	"context"
	"crypto/tls"
	"flag"
	"fmt"
	"os"
	"path/filepath"

	// Import all Kubernetes client auth plugins (e.g. Azure, GCP, OIDC, etc.)
	// to ensure that exec-entrypoint and run can make use of them.
	_ "k8s.io/client-go/plugin/pkg/client/auth"

	"k8s.io/apimachinery/pkg/runtime"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/cache"
	"sigs.k8s.io/controller-runtime/pkg/certwatcher"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/healthz"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"
	"sigs.k8s.io/controller-runtime/pkg/metrics/filters"
	metricsserver "sigs.k8s.io/controller-runtime/pkg/metrics/server"
	"sigs.k8s.io/controller-runtime/pkg/webhook"
	"sigs.k8s.io/yaml"

	osacv1alpha1 "github.com/osac-project/bare-metal-fulfillment-operator/api/v1alpha1"
	"github.com/osac-project/bare-metal-fulfillment-operator/internal/controller"
	"github.com/osac-project/bare-metal-fulfillment-operator/internal/helpers"
	"github.com/osac-project/bare-metal-fulfillment-operator/internal/inventory"
	"github.com/osac-project/bare-metal-fulfillment-operator/internal/management"
	"github.com/osac-project/bare-metal-fulfillment-operator/internal/profile"
	"github.com/osac-project/osac-operator/pkg/aap"
	"github.com/osac-project/osac-operator/pkg/provisioning"
	// +kubebuilder:scaffold:imports
)

var (
	scheme   = runtime.NewScheme()
	setupLog = ctrl.Log.WithName("setup")
)

const (
	// Manager level configuration
	envBareMetalFulfillmentNamespace           = "OSAC_BARE_METAL_FULFILLMENT_NAMESPACE"
	envBareMetalInstanceMaxConcurrentReconcile = "OSAC_BAREMETALINSTANCE_MAX_CONCURRENT_RECONCILES"

	// Controller level configuration
	envInventoryConfigPath  = "OSAC_INVENTORY_CONFIG_PATH"
	envManagementConfigPath = "OSAC_MANAGEMENT_CONFIG_PATH"
	envProfileConfigPath    = "OSAC_PROFILE_CONFIG_PATH"
	envMaxJobHistory        = "OSAC_MAX_JOB_HISTORY"

	envHostReadyPollInterval     = "OSAC_HOST_READY_POLL_INTERVAL"
	envHostDeletionPollInterval  = "OSAC_HOST_DELETION_POLL_INTERVAL"
	envNoFreeHostsPollInterval   = "OSAC_NO_FREE_HOSTS_POLL_INTERVAL"
	envTryLockFailPollInterval   = "OSAC_TRY_LOCK_FAIL_POLL_INTERVAL"
	envManagementRecheckInterval = "OSAC_MANAGEMENT_RECHECK_INTERVAL"
	envProvisionPollInterval     = "OSAC_PROVISION_POLL_INTERVAL"

	envAAPURL                = "OSAC_AAP_URL"
	envAAPToken              = "OSAC_AAP_TOKEN"
	envAAPStatusPollInterval = "OSAC_AAP_STATUS_POLL_INTERVAL"
	envAAPInsecureSkipVerify = "OSAC_AAP_INSECURE_SKIP_VERIFY"
	envAAPTemplatePrefix     = "OSAC_AAP_TEMPLATE_PREFIX"
)

func init() {
	utilruntime.Must(clientgoscheme.AddToScheme(scheme))

	utilruntime.Must(osacv1alpha1.AddToScheme(scheme))
	// +kubebuilder:scaffold:scheme
}

// nolint:gocyclo
func main() {
	var metricsAddr string
	var metricsCertPath, metricsCertName, metricsCertKey string
	var webhookCertPath, webhookCertName, webhookCertKey string
	var enableLeaderElection bool
	var probeAddr string
	var secureMetrics bool
	var enableHTTP2 bool
	var tlsOpts []func(*tls.Config)
	flag.StringVar(&metricsAddr, "metrics-bind-address", "0", "The address the metrics endpoint binds to. "+
		"Use :8443 for HTTPS or :8080 for HTTP, or leave as 0 to disable the metrics service.")
	flag.StringVar(&probeAddr, "health-probe-bind-address", ":8081", "The address the probe endpoint binds to.")
	flag.BoolVar(&enableLeaderElection, "leader-elect", false,
		"Enable leader election for controller manager. "+
			"Enabling this will ensure there is only one active controller manager.")
	flag.BoolVar(&secureMetrics, "metrics-secure", true,
		"If set, the metrics endpoint is served securely via HTTPS. Use --metrics-secure=false to use HTTP instead.")
	flag.StringVar(&webhookCertPath, "webhook-cert-path", "", "The directory that contains the webhook certificate.")
	flag.StringVar(&webhookCertName, "webhook-cert-name", "tls.crt", "The name of the webhook certificate file.")
	flag.StringVar(&webhookCertKey, "webhook-cert-key", "tls.key", "The name of the webhook key file.")
	flag.StringVar(&metricsCertPath, "metrics-cert-path", "",
		"The directory that contains the metrics server certificate.")
	flag.StringVar(&metricsCertName, "metrics-cert-name", "tls.crt", "The name of the metrics server certificate file.")
	flag.StringVar(&metricsCertKey, "metrics-cert-key", "tls.key", "The name of the metrics server key file.")
	flag.BoolVar(&enableHTTP2, "enable-http2", false,
		"If set, HTTP/2 will be enabled for the metrics and webhook servers")
	opts := zap.Options{
		Development: true,
	}
	opts.BindFlags(flag.CommandLine)
	flag.Parse()

	ctrl.SetLogger(zap.New(zap.UseFlagOptions(&opts)))

	ctx := ctrl.SetupSignalHandler()

	// if the enable-http2 flag is false (the default), http/2 should be disabled
	// due to its vulnerabilities. More specifically, disabling http/2 will
	// prevent from being vulnerable to the HTTP/2 Stream Cancellation and
	// Rapid Reset CVEs. For more information see:
	// - https://github.com/advisories/GHSA-qppj-fm5r-hxr3
	// - https://github.com/advisories/GHSA-4374-p667-p6c8
	disableHTTP2 := func(c *tls.Config) {
		setupLog.Info("disabling http/2")
		c.NextProtos = []string{"http/1.1"}
	}

	if !enableHTTP2 {
		tlsOpts = append(tlsOpts, disableHTTP2)
	}

	// Create watchers for metrics and webhooks certificates
	var metricsCertWatcher, webhookCertWatcher *certwatcher.CertWatcher

	// Initial webhook TLS options
	webhookTLSOpts := tlsOpts

	if len(webhookCertPath) > 0 {
		setupLog.Info("Initializing webhook certificate watcher using provided certificates",
			"webhook-cert-path", webhookCertPath, "webhook-cert-name", webhookCertName, "webhook-cert-key", webhookCertKey)

		var err error
		webhookCertWatcher, err = certwatcher.New(
			filepath.Join(webhookCertPath, webhookCertName),
			filepath.Join(webhookCertPath, webhookCertKey),
		)
		if err != nil {
			setupLog.Error(err, "Failed to initialize webhook certificate watcher")
			os.Exit(1)
		}

		webhookTLSOpts = append(webhookTLSOpts, func(config *tls.Config) {
			config.GetCertificate = webhookCertWatcher.GetCertificate
		})
	}

	webhookServer := webhook.NewServer(webhook.Options{
		TLSOpts: webhookTLSOpts,
	})

	// Metrics endpoint is enabled in 'config/default/kustomization.yaml'. The Metrics options configure the server.
	// More info:
	// - https://pkg.go.dev/sigs.k8s.io/controller-runtime@v0.21.0/pkg/metrics/server
	// - https://book.kubebuilder.io/reference/metrics.html
	metricsServerOptions := metricsserver.Options{
		BindAddress:   metricsAddr,
		SecureServing: secureMetrics,
		TLSOpts:       tlsOpts,
	}

	if secureMetrics {
		// FilterProvider is used to protect the metrics endpoint with authn/authz.
		// These configurations ensure that only authorized users and service accounts
		// can access the metrics endpoint. The RBAC are configured in 'config/rbac/kustomization.yaml'. More info:
		// https://pkg.go.dev/sigs.k8s.io/controller-runtime@v0.21.0/pkg/metrics/filters#WithAuthenticationAndAuthorization
		metricsServerOptions.FilterProvider = filters.WithAuthenticationAndAuthorization
	}

	// If the certificate is not specified, controller-runtime will automatically
	// generate self-signed certificates for the metrics server. While convenient for development and testing,
	// this setup is not recommended for production.
	//
	// TODO(user): If you enable certManager, uncomment the following lines:
	// - [METRICS-WITH-CERTS] at config/default/kustomization.yaml to generate and use certificates
	// managed by cert-manager for the metrics server.
	// - [PROMETHEUS-WITH-CERTS] at config/prometheus/kustomization.yaml for TLS certification.
	if len(metricsCertPath) > 0 {
		setupLog.Info("Initializing metrics certificate watcher using provided certificates",
			"metrics-cert-path", metricsCertPath, "metrics-cert-name", metricsCertName, "metrics-cert-key", metricsCertKey)

		var err error
		metricsCertWatcher, err = certwatcher.New(
			filepath.Join(metricsCertPath, metricsCertName),
			filepath.Join(metricsCertPath, metricsCertKey),
		)
		if err != nil {
			setupLog.Error(err, "to initialize metrics certificate watcher", "error", err)
			os.Exit(1)
		}

		metricsServerOptions.TLSOpts = append(metricsServerOptions.TLSOpts, func(config *tls.Config) {
			config.GetCertificate = metricsCertWatcher.GetCertificate
		})
	}

	bareMetalFulfillmentNamespace := os.Getenv(envBareMetalFulfillmentNamespace)
	if bareMetalFulfillmentNamespace == "" {
		setupLog.Error(nil, fmt.Sprintf("%s environment variable must be set", envBareMetalFulfillmentNamespace))
		os.Exit(1)
	}

	mgr, err := ctrl.NewManager(ctrl.GetConfigOrDie(), ctrl.Options{
		Scheme:                 scheme,
		Metrics:                metricsServerOptions,
		WebhookServer:          webhookServer,
		HealthProbeBindAddress: probeAddr,
		LeaderElection:         enableLeaderElection,
		LeaderElectionID:       "89c2406a.openshift.io",
		Cache: cache.Options{
			ByObject: map[client.Object]cache.ByObject{
				&osacv1alpha1.BareMetalPool{}: {
					Namespaces: map[string]cache.Config{
						bareMetalFulfillmentNamespace: {},
					},
				},
				&osacv1alpha1.BareMetalInstance{}: {
					Namespaces: map[string]cache.Config{
						bareMetalFulfillmentNamespace: {},
					},
				},
			},
		},
		// LeaderElectionReleaseOnCancel defines if the leader should step down voluntarily
		// when the Manager ends. This requires the binary to immediately end when the
		// Manager is stopped, otherwise, this setting is unsafe. Setting this significantly
		// speeds up voluntary leader transitions as the new leader don't have to wait
		// LeaseDuration time first.
		//
		// In the default scaffold provided, the program ends immediately after
		// the manager stops, so would be fine to enable this option. However,
		// if you are doing or is intended to do any operation such as perform cleanups
		// after the manager stops then its usage might be unsafe.
		// LeaderElectionReleaseOnCancel: true,
	})
	if err != nil {
		setupLog.Error(err, "unable to start manager")
		os.Exit(1)
	}

	// Create shared provisioning provider
	var provisioningProvider provisioning.ProvisioningProvider
	aapURL := helpers.GetEnvWithDefault(envAAPURL, "")
	aapToken := helpers.GetEnvWithDefault(envAAPToken, "")
	if aapURL != "" && aapToken != "" {
		insecureSkipVerify := helpers.GetEnvWithDefault(envAAPInsecureSkipVerify, false)
		templatePrefix := helpers.GetEnvWithDefault(envAAPTemplatePrefix, "osac")

		aapClient := aap.NewClient(aapURL, aapToken, insecureSkipVerify)

		var err error
		provisioningProvider, err = provisioning.NewProvider(provisioning.ProviderConfig{
			AAPClient:      aapClient,
			TemplatePrefix: templatePrefix,
		})
		if err != nil {
			setupLog.Error(err, "failed to create AAP provisioning provider")
			os.Exit(1)
		}

		setupLog.Info("AAP provisioning provider configured")
	} else {
		setupLog.Info("AAP not configured, provisioning workflows disabled")
	}

	if err := setupBareMetalPoolController(mgr, provisioningProvider); err != nil {
		setupLog.Error(err, "unable to setup controller", "controller", "BareMetalPool")
		os.Exit(1)
	}

	if err := setupBareMetalInstanceController(ctx, mgr, provisioningProvider); err != nil {
		setupLog.Error(err, "unable to setup controller", "controller", "BareMetalInstance")
		os.Exit(1)
	}
	// +kubebuilder:scaffold:builder

	if metricsCertWatcher != nil {
		setupLog.Info("Adding metrics certificate watcher to manager")
		if err := mgr.Add(metricsCertWatcher); err != nil {
			setupLog.Error(err, "unable to add metrics certificate watcher to manager")
			os.Exit(1)
		}
	}

	if webhookCertWatcher != nil {
		setupLog.Info("Adding webhook certificate watcher to manager")
		if err := mgr.Add(webhookCertWatcher); err != nil {
			setupLog.Error(err, "unable to add webhook certificate watcher to manager")
			os.Exit(1)
		}
	}

	if err := mgr.AddHealthzCheck("healthz", healthz.Ping); err != nil {
		setupLog.Error(err, "unable to set up health check")
		os.Exit(1)
	}
	if err := mgr.AddReadyzCheck("readyz", healthz.Ping); err != nil {
		setupLog.Error(err, "unable to set up ready check")
		os.Exit(1)
	}

	setupLog.Info("starting manager")
	if err := mgr.Start(ctx); err != nil {
		setupLog.Error(err, "problem running manager")
		os.Exit(1)
	}
}

// setupBareMetalPoolController registers the BareMetalPool controller.
func setupBareMetalPoolController(mgr ctrl.Manager, provisioningProvider provisioning.ProvisioningProvider) error {
	// Load profile configuration
	profileConfigPath := helpers.GetEnvWithDefault(
		envProfileConfigPath,
		"/etc/osac/profile/profile.yaml",
	)

	profileData, err := os.ReadFile(profileConfigPath)
	if err != nil {
		if !os.IsNotExist(err) {
			setupLog.Error(err, "unable to read profile config file")
			return err
		}
		setupLog.Info("No profile config file found, starting without profiles", "path", profileConfigPath)
	} else {
		var profiles []*profile.Profile
		if err := yaml.Unmarshal(profileData, &profiles); err != nil {
			setupLog.Error(err, "unable to parse profile config")
			return err
		}
		if err := profile.LoadProfiles(profiles); err != nil {
			setupLog.Error(err, "unable to load profile config")
			return err
		}
	}

	hostReadyPollIntervalDuration := helpers.GetEnvWithDefault(
		envHostReadyPollInterval,
		controller.DefaultHostReadyPollIntervalDuration,
	)

	hostDeletionPollIntervalDuration := helpers.GetEnvWithDefault(
		envHostDeletionPollInterval,
		controller.DefaultHostDeletionPollIntervalDuration,
	)

	provisionJobPollIntervalDuration := helpers.GetEnvWithDefault(
		envAAPStatusPollInterval,
		controller.DefaultAAPStatusPollIntervalDuration,
	)

	maxJobHistory := helpers.GetEnvWithDefault(
		envMaxJobHistory,
		controller.DefaultMaxJobHistory,
	)

	if err := controller.NewBareMetalPoolReconciler(
		mgr.GetClient(),
		mgr.GetScheme(),
		provisioningProvider,
		hostReadyPollIntervalDuration,
		hostDeletionPollIntervalDuration,
		provisionJobPollIntervalDuration,
		maxJobHistory,
	).SetupWithManager(mgr); err != nil {
		return fmt.Errorf("unable to create controller: %w", err)
	}

	return nil
}

// setupBareMetalInstanceController registers the BareMetalInstance controller.
func setupBareMetalInstanceController(
	ctx context.Context,
	mgr ctrl.Manager,
	provisioningProvider provisioning.ProvisioningProvider,
) error {
	// Read and parse inventory configuration
	inventoryConfigPath := helpers.GetEnvWithDefault(envInventoryConfigPath, "/etc/osac/inventory/inventory.yaml")
	inventoryConfigData, err := os.ReadFile(inventoryConfigPath)
	if err != nil {
		return fmt.Errorf("failed to read inventory config file: %w", err)
	}

	var inventoryConfig inventory.Config
	if err := yaml.Unmarshal(inventoryConfigData, &inventoryConfig); err != nil {
		return fmt.Errorf("failed to parse inventory config: %w", err)
	}

	inventoryClient, err := inventory.NewClient(ctx, &inventoryConfig)
	if err != nil {
		return fmt.Errorf("failed to create inventory client: %w", err)
	}
	if inventoryClient == nil {
		return fmt.Errorf("unsupported inventory type %q", inventoryConfig.Type)
	}

	// Read and parse management configuration
	managementConfigPath := helpers.GetEnvWithDefault(envManagementConfigPath, "/etc/osac/management/management.yaml")
	managementConfigData, err := os.ReadFile(managementConfigPath)
	if err != nil {
		return fmt.Errorf("failed to read management config file: %w", err)
	}

	var managementConfig management.Config
	if err := yaml.Unmarshal(managementConfigData, &managementConfig); err != nil {
		return fmt.Errorf("failed to parse management config: %w", err)
	}

	managementClient, err := management.NewClient(ctx, &managementConfig)
	if err != nil {
		return fmt.Errorf("failed to create management client: %w", err)
	}
	if managementClient == nil {
		return fmt.Errorf("unsupported management type %q", managementConfig.Type)
	}

	noFreeHostsPollInterval := helpers.GetEnvWithDefault(
		envNoFreeHostsPollInterval,
		controller.DefaultNoFreeHostsPollIntervalDuration,
	)
	tryLockFailPollInterval := helpers.GetEnvWithDefault(
		envTryLockFailPollInterval,
		controller.DefaultTryLockFailPollIntervalDuration,
	)
	managementRecheckInterval := helpers.GetEnvWithDefault(
		envManagementRecheckInterval,
		controller.DefaultManagementRecheckIntervalDuration,
	)
	provisionPollInterval := helpers.GetEnvWithDefault(
		envProvisionPollInterval,
		controller.DefaultProvisionPollIntervalDuration,
	)
	maxConcurrentReconciles := helpers.GetEnvWithDefault(
		envBareMetalInstanceMaxConcurrentReconcile,
		1,
		func(v int) bool { return v > 0 },
	)

	if err := controller.NewBareMetalInstanceReconciler(
		mgr.GetClient(),
		mgr.GetScheme(),
		inventoryClient,
		managementClient,
		provisioningProvider,
		noFreeHostsPollInterval,
		tryLockFailPollInterval,
		managementRecheckInterval,
		provisionPollInterval,
	).SetupWithManager(mgr, maxConcurrentReconciles); err != nil {
		return fmt.Errorf("baremetalinstance controller: %w", err)
	}
	return nil
}

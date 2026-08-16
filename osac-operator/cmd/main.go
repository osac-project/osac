/*
Copyright 2025.

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

// Main entrypoint for the operator
package main

import (
	"context"
	"crypto/tls"
	"flag"
	"fmt"
	"os"
	"strings"
	"time"

	"golang.org/x/sync/errgroup"

	// Import all Kubernetes client auth plugins (e.g. Azure, GCP, OIDC, etc.)
	// to ensure that exec-entrypoint and run can make use of them.
	_ "k8s.io/client-go/plugin/pkg/client/auth"

	hypershiftv1beta1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	ovnv1 "github.com/ovn-org/ovn-kubernetes/go-controller/pkg/crd/userdefinednetwork/v1"
	"golang.org/x/oauth2"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"
	insecurecredentials "google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/credentials/oauth"
	experimentalcredentials "google.golang.org/grpc/experimental/credentials"
	"k8s.io/apimachinery/pkg/runtime"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/tools/clientcmd"
	kubevirtv1 "kubevirt.io/api/core/v1"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	cluster "sigs.k8s.io/controller-runtime/pkg/cluster"
	"sigs.k8s.io/controller-runtime/pkg/healthz"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"
	"sigs.k8s.io/controller-runtime/pkg/manager"
	"sigs.k8s.io/controller-runtime/pkg/metrics/filters"
	metricsserver "sigs.k8s.io/controller-runtime/pkg/metrics/server"
	"sigs.k8s.io/controller-runtime/pkg/webhook"
	mcmanager "sigs.k8s.io/multicluster-runtime/pkg/manager"
	"sigs.k8s.io/multicluster-runtime/pkg/multicluster"
	"sigs.k8s.io/multicluster-runtime/providers/single"

	bmfov1alpha1 "github.com/osac-project/osac/bare-metal-fulfillment-operator/api/v1alpha1"
	v1alpha1 "github.com/osac-project/osac/osac-operator/api/v1alpha1"
	"github.com/osac-project/osac/osac-operator/helpers"
	privatev1 "github.com/osac-project/osac/osac-operator/internal/api/osac/private/v1"
	"github.com/osac-project/osac/osac-operator/internal/controller"
	"github.com/osac-project/osac/osac-operator/internal/dispatcheradapter"
	"github.com/osac-project/osac/osac-operator/internal/migrations"
	"github.com/osac-project/osac/osac-operator/pkg/aap"
	"github.com/osac-project/osac/osac-operator/pkg/dispatcher"
	"github.com/osac-project/osac/osac-operator/pkg/networkmanager"
	"github.com/osac-project/osac/osac-operator/pkg/provisioning"
	// +kubebuilder:scaffold:imports
)

var (
	setupLog = ctrl.Log.WithName("setup")
)

const (
	// Namespace environment variables
	envComputeInstanceNamespace   = "OSAC_COMPUTE_INSTANCE_NAMESPACE"
	envNetworkingNamespace        = "OSAC_NETWORKING_NAMESPACE"
	envClusterOrderNamespace      = "OSAC_CLUSTER_ORDER_NAMESPACE"
	envBareMetalInstanceNamespace = "OSAC_BARE_METAL_INSTANCE_NAMESPACE"

	// AAP configuration
	envAAPURL                 = "OSAC_AAP_URL"
	envAAPToken               = "OSAC_AAP_TOKEN"
	envAAPProvisionTemplate   = "OSAC_AAP_PROVISION_TEMPLATE"
	envAAPDeprovisionTemplate = "OSAC_AAP_DEPROVISION_TEMPLATE"
	envAAPStatusPollInterval  = "OSAC_AAP_STATUS_POLL_INTERVAL"
	envAAPInsecureSkipVerify  = "OSAC_AAP_INSECURE_SKIP_VERIFY"
	envAAPTemplatePrefix      = "OSAC_AAP_TEMPLATE_PREFIX"

	// Cluster (ClusterOrder) AAP template overrides
	envClusterAAPProvisionTemplate   = "OSAC_CLUSTER_AAP_PROVISION_TEMPLATE"
	envClusterAAPDeprovisionTemplate = "OSAC_CLUSTER_AAP_DEPROVISION_TEMPLATE"

	// Storage controller AAP template overrides
	envStorageBackendProvisionTemplate   = "OSAC_STORAGE_BACKEND_AAP_PROVISION_TEMPLATE"
	envStorageBackendDeprovisionTemplate = "OSAC_STORAGE_BACKEND_AAP_DEPROVISION_TEMPLATE"
	envClusterStorageProvisionTemplate   = "OSAC_STORAGE_CLUSTER_AAP_PROVISION_TEMPLATE"
	envClusterStorageDeprovisionTemplate = "OSAC_STORAGE_CLUSTER_AAP_DEPROVISION_TEMPLATE"

	// Job history configuration
	envMaxJobHistory = "OSAC_MAX_JOB_HISTORY"

	// NetworkClass capabilities sync configuration
	envNetworkClassSyncInterval = "OSAC_NETWORK_CLASS_SYNC_INTERVAL"

	// Tenant configuration
	envTenantNamespace = "OSAC_TENANT_NAMESPACE"

	// Remote cluster (tenant and compute-instance controllers)
	envRemoteClusterKubeconfig = "OSAC_REMOTE_CLUSTER_KUBECONFIG"

	// Controller enable flags (defaults when flag is not set)
	envEnableTenantController            = "OSAC_ENABLE_TENANT_CONTROLLER"
	envEnableStorageController           = "OSAC_ENABLE_STORAGE_CONTROLLER"
	envEnableComputeInstanceController   = "OSAC_ENABLE_COMPUTE_INSTANCE_CONTROLLER"
	envEnableClusterController           = "OSAC_ENABLE_CLUSTER_CONTROLLER"
	envEnableNetworkingController        = "OSAC_ENABLE_NETWORKING_CONTROLLER"
	envEnableBareMetalInstanceController = "OSAC_ENABLE_BAREMETAL_INSTANCE_CONTROLLER"

	remoteClusterName = "remote"

	// defaultNetworkClassSyncInterval is the fallback periodic resync interval for
	// NetworkClass capabilities when OSAC_NETWORK_CLASS_SYNC_INTERVAL is unset or
	// non-positive. Recommended range: 1m (avoid excessive gRPC load from an overly
	// aggressive interval) to 1h (avoid capabilities going too stale).
	defaultNetworkClassSyncInterval = 5 * time.Minute
)

// controllerFlags holds the enable flags for all controllers.
type controllerFlags struct {
	Tenant            bool
	Storage           bool
	ComputeInstance   bool
	Cluster           bool
	Networking        bool
	BareMetalInstance bool
}

// registerControllerFlags registers controller enable flags with the flag package
// and returns a function that should be called after flag.Parse() to get the final values.
func registerControllerFlags() *controllerFlags {
	flags := &controllerFlags{}
	flag.BoolVar(&flags.Tenant, "enable-tenant-controller",
		helpers.GetEnvWithDefault(envEnableTenantController, false),
		"Enable the tenant controller.")
	flag.BoolVar(&flags.Storage, "enable-storage-controller",
		helpers.GetEnvWithDefault(envEnableStorageController, false),
		"Enable the storage controller.")
	flag.BoolVar(&flags.ComputeInstance, "enable-compute-instance-controller",
		helpers.GetEnvWithDefault(envEnableComputeInstanceController, false),
		"Enable the compute-instance controller.")
	flag.BoolVar(&flags.Cluster, "enable-cluster-controller",
		helpers.GetEnvWithDefault(envEnableClusterController, false),
		"Enable the cluster controller.")
	flag.BoolVar(&flags.Networking, "enable-networking-controller",
		helpers.GetEnvWithDefault(envEnableNetworkingController, false),
		"Enable the networking controllers (VirtualNetwork, Subnet, SecurityGroup).")
	flag.BoolVar(&flags.BareMetalInstance, "enable-baremetal-instance-controller",
		helpers.GetEnvWithDefault(envEnableBareMetalInstanceController, false),
		"Enable the bare metal instance controller.")
	return flags
}

// enableAllIfNoneSet enables all controllers if none are explicitly enabled.
func (f *controllerFlags) enableAllIfNoneSet() {
	if !f.Tenant && !f.Storage && !f.ComputeInstance && !f.Cluster && !f.Networking && !f.BareMetalInstance {
		f.Tenant = true
		f.Storage = true
		f.ComputeInstance = true
		f.Cluster = true
		f.Networking = true
		f.BareMetalInstance = true
		setupLog.Info("no controller flags set, enabling all controllers")
	}
}

// addSchemesForLocalControllers registers only the API schemes required by the enabled controllers.
// Must be called before creating the manager.
func addSchemesForLocalControllers(
	localScheme *runtime.Scheme,
	enableCluster, enableComputeInstance, enableTenant, enableNetworking, enableBareMetalInstance bool,
) {
	utilruntime.Must(clientgoscheme.AddToScheme(localScheme))
	utilruntime.Must(v1alpha1.AddToScheme(localScheme))
	if enableCluster {
		utilruntime.Must(hypershiftv1beta1.AddToScheme(localScheme))
	}
	if enableComputeInstance {
		utilruntime.Must(kubevirtv1.AddToScheme(localScheme))
	}
	if enableTenant {
		utilruntime.Must(ovnv1.AddToScheme(localScheme))
	}
	if enableBareMetalInstance {
		utilruntime.Must(bmfov1alpha1.AddToScheme(localScheme))
	}
	// +kubebuilder:scaffold:scheme
}

// addSchemesForRemoteControllers registers only the API schemes required by the enabled controllers.
// Must be called before creating the manager.
func addSchemesForRemoteControllers(
	localScheme *runtime.Scheme,
	remoteScheme *runtime.Scheme,
	enableComputeInstance, enableTenant bool,
) {
	utilruntime.Must(clientgoscheme.AddToScheme(localScheme))
	utilruntime.Must(v1alpha1.AddToScheme(localScheme))

	utilruntime.Must(clientgoscheme.AddToScheme(remoteScheme))
	if enableComputeInstance {
		utilruntime.Must(kubevirtv1.AddToScheme(remoteScheme))
	}
	if enableTenant {
		utilruntime.Must(ovnv1.AddToScheme(remoteScheme))
	}
	// +kubebuilder:scaffold:scheme
}

// newClusterFromKubeconfig creates a controller-runtime cluster from a kubeconfig file path.
// The cluster uses the given scheme for type resolution. The caller is responsible for
// starting the cluster (e.g. in a goroutine with cl.Start(ctx)) so it runs with the manager.
func newClusterFromKubeconfig(kubeconfigPath string, scheme *runtime.Scheme) (cluster.Cluster, error) {
	config, err := clientcmd.BuildConfigFromFlags("", kubeconfigPath)
	if err != nil {
		return nil, fmt.Errorf("build config from kubeconfig %q: %w", kubeconfigPath, err)
	}
	cl, err := cluster.New(config, func(o *cluster.Options) {
		o.Scheme = scheme
	})
	if err != nil {
		return nil, fmt.Errorf("create cluster from kubeconfig: %w", err)
	}
	return cl, nil
}

// createAAPProvider creates and validates AAP direct provider configuration.
func createAAPProvider(
	aapURL, aapToken, provisionTemplate, deprovisionTemplate, templatePrefix string,
	aapInsecureSkipVerify bool,
) (provisioning.ProvisioningProvider, time.Duration, error) {
	statusPollInterval := helpers.GetEnvWithDefault(envAAPStatusPollInterval, provisioning.DefaultStatusPollInterval)

	aapClient := aap.NewClient(aapURL, aapToken, aapInsecureSkipVerify)
	config := provisioning.ProviderConfig{
		AAPClient:           aapClient,
		ProvisionTemplate:   provisionTemplate,
		DeprovisionTemplate: deprovisionTemplate,
		TemplatePrefix:      templatePrefix,
	}

	provider, err := provisioning.NewProvider(config)
	if err != nil {
		return nil, 0, err
	}

	setupLog.Info("using AAP direct provider",
		"url", aapURL,
		"provisionTemplate", provisionTemplate,
		"deprovisionTemplate", deprovisionTemplate,
		"templatePrefix", templatePrefix,
		"statusPollInterval", statusPollInterval,
		"insecureSkipVerify", aapInsecureSkipVerify)

	return provider, statusPollInterval, nil
}

// createAAPProviderFromEnv creates an AAP provider by reading shared env vars
// and optional per-resource-type template overrides.
func createAAPProviderFromEnv(
	templateOverrideProvisionEnv, templateOverrideDeprovisionEnv string,
) (provisioning.ProvisioningProvider, time.Duration, error) {
	aapURL := os.Getenv(envAAPURL)
	aapToken := os.Getenv(envAAPToken)
	provisionTemplate := helpers.GetEnvWithDefault(templateOverrideProvisionEnv, os.Getenv(envAAPProvisionTemplate))
	deprovisionTemplate := helpers.GetEnvWithDefault(templateOverrideDeprovisionEnv, os.Getenv(envAAPDeprovisionTemplate))
	templatePrefix := helpers.GetEnvWithDefault(envAAPTemplatePrefix, "osac")
	aapInsecureSkipVerify := helpers.GetEnvWithDefault(envAAPInsecureSkipVerify, false)
	return createAAPProvider(
		aapURL, aapToken, provisionTemplate, deprovisionTemplate,
		templatePrefix, aapInsecureSkipVerify,
	)
}

// setupProvisioningController handles the shared flow: feedback setup, provider creation, reconciler setup.
func setupProvisioningController(
	aapProvisionTemplateEnv, aapDeprovisionTemplateEnv string,
	setupFeedback func() error,
	setupReconciler func(provisioning.ProvisioningProvider, time.Duration) error,
) error {
	if err := setupFeedback(); err != nil {
		return err
	}
	provider, statusPollInterval, err := createAAPProviderFromEnv(
		aapProvisionTemplateEnv, aapDeprovisionTemplateEnv,
	)
	if err != nil {
		return err
	}
	return setupReconciler(provider, statusPollInterval)
}

func targetClusterFromManager(mgr mcmanager.Manager) multicluster.ClusterName {
	if mgr.GetProvider() != nil {
		return remoteClusterName
	}
	return mcmanager.LocalCluster
}

// setupClusterControllers registers the ClusterOrder controller and, when grpcConn is set,
// the cluster Feedback controller.
func setupClusterControllers(
	mgr mcmanager.Manager, grpcConn *grpc.ClientConn,
	maxJobHistory int,
) error {
	localMgr := mgr.GetLocalManager()
	return setupProvisioningController(
		envClusterAAPProvisionTemplate, envClusterAAPDeprovisionTemplate,
		func() error {
			if grpcConn == nil {
				return nil
			}
			return controller.NewFeedbackReconciler(
				localMgr.GetClient(), grpcConn,
				os.Getenv(envClusterOrderNamespace),
			).SetupWithManager(mgr)
		},
		func(provider provisioning.ProvisioningProvider, pollInterval time.Duration) error {
			return controller.NewClusterOrderReconciler(
				localMgr.GetClient(), localMgr.GetAPIReader(), localMgr.GetScheme(),
				os.Getenv(envClusterOrderNamespace),
				provider, pollInterval, maxJobHistory,
			).SetupWithManager(mgr)
		},
	)
}

// setupComputeInstanceControllers registers the ComputeInstance controller and, when grpcConn is set,
// the ComputeInstance Feedback controller.
func setupComputeInstanceControllers(
	mgr mcmanager.Manager,
	grpcConn *grpc.ClientConn,
	maxJobHistory int,
) error {
	localMgr := mgr.GetLocalManager()
	computeInstanceNamespace := os.Getenv(envComputeInstanceNamespace)
	tenantNamespace := os.Getenv(envTenantNamespace)
	networkingNamespace := os.Getenv(envNetworkingNamespace)
	targetCluster := targetClusterFromManager(mgr)
	computeInstanceProvider, statusPollInterval, err := createAAPProviderFromEnv("", "")
	if err != nil {
		return fmt.Errorf("create provisioning provider: %w", err)
	}
	if grpcConn != nil {
		if err := (controller.NewComputeInstanceFeedbackReconciler(
			localMgr.GetClient(),
			grpcConn,
			computeInstanceNamespace,
		)).SetupWithManager(mgr); err != nil {
			return fmt.Errorf("computeinstance feedback controller: %w", err)
		}
	}
	ciReconciler := controller.NewComputeInstanceReconciler(
		mgr,
		computeInstanceNamespace,
		tenantNamespace,
		networkingNamespace,
		computeInstanceProvider,
		statusPollInterval,
		maxJobHistory,
		targetCluster,
	)
	if grpcConn != nil {
		ciReconciler.TiersClient = privatev1.NewStorageTiersClient(grpcConn)
		ciReconciler.BackendsClient = privatev1.NewStorageBackendsClient(grpcConn)
	}
	if err := ciReconciler.SetupWithManager(mgr); err != nil {
		return fmt.Errorf("computeinstance controller: %w", err)
	}
	return nil
}

// setupTenantController registers the Tenant controller (namespace + UDN only).
func setupTenantController(mgr mcmanager.Manager) error {
	targetCluster := targetClusterFromManager(mgr)
	tenantNamespace := os.Getenv(envTenantNamespace)

	if err := (controller.NewTenantReconciler(
		mgr,
		tenantNamespace,
		targetCluster,
	)).SetupWithManager(mgr); err != nil {
		return fmt.Errorf("tenant controller: %w", err)
	}
	return nil
}

// setupStorageController registers the OSAC Storage Controller with two AAP
// provider instances (backend and cluster-storage). When grpcConn is non-nil,
// the controller queries the Backend API to determine whether a storage backend
// is registered before entering the AAP provisioning path, and wires the Tier
// and Backend API clients used to validate and pass through storage tier
// definitions.
func setupStorageController(mgr mcmanager.Manager, grpcConn *grpc.ClientConn, maxJobHistory int) error {
	targetCluster := targetClusterFromManager(mgr)
	tenantNamespace := os.Getenv(envTenantNamespace)

	var backendProvider provisioning.ProvisioningProvider
	var clusterStorageProvider provisioning.ProvisioningProvider
	var pollInterval time.Duration

	aapURL := os.Getenv(envAAPURL)
	aapToken := os.Getenv(envAAPToken)
	if aapURL != "" && aapToken != "" {
		aapInsecureSkipVerify := helpers.GetEnvWithDefault(envAAPInsecureSkipVerify, false)

		backendProvisionTemplate := helpers.GetEnvWithDefault(
			envStorageBackendProvisionTemplate, "osac-create-tenant-storage-backend")
		backendDeprovisionTemplate := helpers.GetEnvWithDefault(
			envStorageBackendDeprovisionTemplate, "osac-delete-tenant-storage-backend")
		clusterStorageProvisionTemplate := helpers.GetEnvWithDefault(
			envClusterStorageProvisionTemplate, "osac-create-tenant-cluster-storage")
		clusterStorageDeprovisionTemplate := helpers.GetEnvWithDefault(
			envClusterStorageDeprovisionTemplate, "osac-delete-tenant-cluster-storage")

		var err error
		backendProvider, pollInterval, err = createAAPProvider(
			aapURL, aapToken, backendProvisionTemplate, backendDeprovisionTemplate,
			"", aapInsecureSkipVerify,
		)
		if err != nil {
			return fmt.Errorf("storage backend provider: %w", err)
		}

		clusterStorageProvider, _, err = createAAPProvider(
			aapURL, aapToken, clusterStorageProvisionTemplate, clusterStorageDeprovisionTemplate,
			"", aapInsecureSkipVerify,
		)
		if err != nil {
			return fmt.Errorf("cluster storage provider: %w", err)
		}

		setupLog.Info("storage provisioning configured",
			"backendProvision", backendProvisionTemplate,
			"backendDeprovision", backendDeprovisionTemplate,
			"clusterStorageProvision", clusterStorageProvisionTemplate,
			"clusterStorageDeprovision", clusterStorageDeprovisionTemplate)
	}

	reconciler := controller.NewStorageReconciler(
		mgr,
		tenantNamespace,
		targetCluster,
		backendProvider,
		clusterStorageProvider,
		pollInterval,
		maxJobHistory,
	)
	if grpcConn != nil {
		reconciler.BackendsClient = privatev1.NewStorageBackendsClient(grpcConn)
		reconciler.TiersClient = privatev1.NewStorageTiersClient(grpcConn)
	}
	if err := reconciler.SetupWithManager(mgr); err != nil {
		return fmt.Errorf("storage controller: %w", err)
	}
	return nil
}

// setupNetworkingControllers registers all networking controllers along with their
// feedback controllers when grpcConn is set.
func setupNetworkingControllers(
	mgr mcmanager.Manager,
	grpcConn *grpc.ClientConn,
	maxJobHistory int,
) error {
	localMgr := mgr.GetLocalManager()
	targetCluster := targetClusterFromManager(mgr)

	networkingNamespace := os.Getenv(envNetworkingNamespace)
	computeInstanceNamespace := os.Getenv(envComputeInstanceNamespace)
	clusterOrderNamespace := os.Getenv(envClusterOrderNamespace)
	bareMetalInstanceNamespace := os.Getenv(envBareMetalInstanceNamespace)

	aapURL := os.Getenv(envAAPURL)
	aapToken := os.Getenv(envAAPToken)
	aapInsecureSkipVerify := helpers.GetEnvWithDefault(envAAPInsecureSkipVerify, false)
	statusPollInterval := helpers.GetEnvWithDefault(envAAPStatusPollInterval, provisioning.DefaultStatusPollInterval)

	// Create a single prefix-based AAP provider shared by most networking controllers.
	// Template names are derived from the resource Kind at call time:
	//   {prefix}-create-{kind-kebab} / {prefix}-delete-{kind-kebab}
	templatePrefix := helpers.GetEnvWithDefault(envAAPTemplatePrefix, "osac")
	aapClient := aap.NewClient(aapURL, aapToken, aapInsecureSkipVerify)
	networkingProvider := provisioning.NewAAPProviderWithPrefix(aapClient, templatePrefix)

	// Create a dedicated provider for ExternalIP attach/detach operations.
	externalIPAttachmentProvider, err := provisioning.NewProvider(provisioning.ProviderConfig{
		AAPClient:           aapClient,
		ProvisionTemplate:   fmt.Sprintf("%s-attach-external-ip", templatePrefix),
		DeprovisionTemplate: fmt.Sprintf("%s-detach-external-ip", templatePrefix),
	})
	if err != nil {
		return fmt.Errorf("externalip attachment provider: %w", err)
	}

	// Build a shared dispatcher Resolver for controllers that support the two-manager
	// model (VirtualNetwork, Subnet, SecurityGroup). Only available when a
	// fulfillment-service connection and networking namespace are both configured;
	// nil otherwise, in which case those controllers always use the legacy
	// implementation-strategy path.
	var resolver *dispatcher.Resolver
	var networkClassesClient privatev1.NetworkClassesClient

	if grpcConn != nil && networkingNamespace != "" {
		networkClassesClient = privatev1.NewNetworkClassesClient(grpcConn)

		disc, err := networkmanager.NewDiscovery(localMgr.GetClient(), networkingNamespace)
		if err != nil {
			return fmt.Errorf("network manager discovery: %w", err)
		}
		networkClassAdapter := dispatcheradapter.NewNetworkClassAdapter(networkClassesClient)
		resolver = dispatcher.NewResolver(networkClassAdapter, disc)

		if err := setupNetworkClassCapabilitiesController(
			mgr, localMgr, grpcConn, networkingNamespace, resolver,
		); err != nil {
			return err
		}
	}

	if err := setupVirtualNetworkControllers(
		mgr, localMgr, grpcConn, networkingNamespace,
		networkingProvider, statusPollInterval, maxJobHistory, targetCluster, resolver,
	); err != nil {
		return err
	}

	if err := setupSubnetControllers(
		mgr, localMgr, grpcConn, networkingNamespace,
		networkingProvider, statusPollInterval, maxJobHistory, targetCluster, resolver,
		networkClassesClient,
	); err != nil {
		return err
	}
	if err := setupSecurityGroupControllers(
		mgr, localMgr, grpcConn, networkingNamespace,
		networkingProvider, statusPollInterval, maxJobHistory, targetCluster, resolver,
	); err != nil {
		return err
	}
	if err := setupExternalIPPoolControllers(
		mgr, localMgr, grpcConn, networkingNamespace,
		networkingProvider, statusPollInterval, maxJobHistory, targetCluster,
	); err != nil {
		return err
	}
	if err := setupExternalIPControllers(
		mgr, localMgr, grpcConn, networkingNamespace,
		networkingProvider, statusPollInterval, maxJobHistory, targetCluster,
	); err != nil {
		return err
	}
	if err := setupExternalIPAttachmentControllers(
		mgr, localMgr, grpcConn,
		networkingNamespace, computeInstanceNamespace, clusterOrderNamespace, bareMetalInstanceNamespace,
		externalIPAttachmentProvider, statusPollInterval, maxJobHistory, targetCluster,
	); err != nil {
		return err
	}
	if err := setupNATGatewayControllers(
		mgr, localMgr, grpcConn, networkingNamespace,
		networkingProvider, statusPollInterval, maxJobHistory, targetCluster,
	); err != nil {
		return err
	}

	return nil
}

// setupNetworkClassCapabilitiesController registers the NetworkClass capabilities
// reconciler (ConfigMap-triggered) and its periodic resync runnable. Both require the
// gRPC connection and a manager resolver built from ConfigMap-based discovery, so this
// is only called when grpcConn is set and a networking namespace is configured.
func setupNetworkClassCapabilitiesController(
	mgr mcmanager.Manager, localMgr ctrl.Manager, grpcConn *grpc.ClientConn, networkingNamespace string,
	resolver *dispatcher.Resolver,
) error {
	networkClassesClient := privatev1.NewNetworkClassesClient(grpcConn)

	ncReconciler := controller.NewNetworkClassCapabilitiesReconciler(
		networkClassesClient, resolver, networkingNamespace,
	)
	if err := ncReconciler.SetupWithManager(mgr); err != nil {
		return fmt.Errorf("networkclass capabilities controller: %w", err)
	}

	syncInterval := helpers.GetEnvWithDefault(
		envNetworkClassSyncInterval, defaultNetworkClassSyncInterval, func(v time.Duration) bool {
			return v > 0
		},
	)
	if err := localMgr.Add(controller.NewNetworkClassCapabilitiesSyncRunnable(ncReconciler, syncInterval)); err != nil {
		return fmt.Errorf("networkclass capabilities sync runnable: %w", err)
	}
	return nil
}

func setupVirtualNetworkControllers(
	mgr mcmanager.Manager, localMgr ctrl.Manager, grpcConn *grpc.ClientConn,
	networkingNamespace string, provider provisioning.ProvisioningProvider,
	statusPollInterval time.Duration, maxJobHistory int, targetCluster multicluster.ClusterName,
	resolver *dispatcher.Resolver,
) error {
	if grpcConn != nil {
		if err := controller.NewVirtualNetworkFeedbackReconciler(
			localMgr.GetClient(), grpcConn, networkingNamespace,
		).SetupWithManager(mgr); err != nil {
			return fmt.Errorf("virtualnetwork feedback controller: %w", err)
		}
	}
	if err := controller.NewVirtualNetworkReconciler(
		mgr, networkingNamespace, provider, statusPollInterval, maxJobHistory, targetCluster, resolver,
	).SetupWithManager(mgr); err != nil {
		return fmt.Errorf("virtualnetwork controller: %w", err)
	}
	return nil
}

func setupSubnetControllers(
	mgr mcmanager.Manager, localMgr ctrl.Manager, grpcConn *grpc.ClientConn,
	networkingNamespace string, provider provisioning.ProvisioningProvider,
	statusPollInterval time.Duration, maxJobHistory int, targetCluster multicluster.ClusterName,
	resolver *dispatcher.Resolver,
	networkClassesClient privatev1.NetworkClassesClient,
) error {
	if grpcConn != nil {
		if err := controller.NewSubnetFeedbackReconciler(
			localMgr.GetClient(), grpcConn, networkingNamespace,
		).SetupWithManager(mgr); err != nil {
			return fmt.Errorf("subnet feedback controller: %w", err)
		}
	}
	if err := controller.NewSubnetReconciler(
		mgr, networkingNamespace, provider, statusPollInterval, maxJobHistory, targetCluster, resolver,
		networkClassesClient,
	).SetupWithManager(mgr); err != nil {
		return fmt.Errorf("subnet controller: %w", err)
	}
	return nil
}

func setupSecurityGroupControllers(
	mgr mcmanager.Manager, localMgr ctrl.Manager, grpcConn *grpc.ClientConn,
	networkingNamespace string, provider provisioning.ProvisioningProvider,
	statusPollInterval time.Duration, maxJobHistory int, targetCluster multicluster.ClusterName,
	resolver *dispatcher.Resolver,
) error {
	if grpcConn != nil {
		if err := controller.NewSecurityGroupFeedbackReconciler(
			localMgr.GetClient(), grpcConn, networkingNamespace,
		).SetupWithManager(mgr); err != nil {
			return fmt.Errorf("securitygroup feedback controller: %w", err)
		}
	}
	if err := controller.NewSecurityGroupReconciler(
		mgr, networkingNamespace, provider, statusPollInterval, maxJobHistory, targetCluster, resolver,
	).SetupWithManager(mgr); err != nil {
		return fmt.Errorf("securitygroup controller: %w", err)
	}
	return nil
}

func setupExternalIPPoolControllers(
	mgr mcmanager.Manager, localMgr ctrl.Manager, grpcConn *grpc.ClientConn,
	networkingNamespace string, provider provisioning.ProvisioningProvider,
	statusPollInterval time.Duration, maxJobHistory int, targetCluster multicluster.ClusterName,
) error {
	if grpcConn != nil {
		if err := controller.NewExternalIPPoolFeedbackReconciler(
			localMgr.GetClient(), grpcConn, networkingNamespace,
		).SetupWithManager(mgr); err != nil {
			return fmt.Errorf("externalippool feedback controller: %w", err)
		}
	}
	if err := controller.NewExternalIPPoolReconciler(
		mgr, networkingNamespace, provider, statusPollInterval, maxJobHistory, targetCluster,
	).SetupWithManager(mgr); err != nil {
		return fmt.Errorf("externalippool controller: %w", err)
	}
	return nil
}

func setupExternalIPControllers(
	mgr mcmanager.Manager, localMgr ctrl.Manager, grpcConn *grpc.ClientConn,
	networkingNamespace string, provider provisioning.ProvisioningProvider,
	statusPollInterval time.Duration, maxJobHistory int, targetCluster multicluster.ClusterName,
) error {
	if err := controller.NewExternalIPReconciler(
		mgr, networkingNamespace, provider, statusPollInterval, maxJobHistory, targetCluster,
	).SetupWithManager(mgr); err != nil {
		return fmt.Errorf("externalip controller: %w", err)
	}
	if grpcConn != nil {
		if err := controller.NewExternalIPFeedbackReconciler(
			localMgr.GetClient(), grpcConn, networkingNamespace,
		).SetupWithManager(mgr); err != nil {
			return fmt.Errorf("externalip feedback controller: %w", err)
		}
	}
	return nil
}

func setupExternalIPAttachmentControllers(
	mgr mcmanager.Manager, localMgr ctrl.Manager, grpcConn *grpc.ClientConn,
	networkingNamespace, computeInstanceNamespace, clusterOrderNamespace, baremetalInstanceNamespace string,
	provider provisioning.ProvisioningProvider,
	statusPollInterval time.Duration, maxJobHistory int, targetCluster multicluster.ClusterName,
) error {
	if err := controller.NewExternalIPAttachmentReconciler(
		mgr, networkingNamespace, computeInstanceNamespace,
		clusterOrderNamespace, baremetalInstanceNamespace,
		provider, statusPollInterval, maxJobHistory, targetCluster,
	).SetupWithManager(mgr); err != nil {
		return fmt.Errorf("externalipattachment controller: %w", err)
	}
	if grpcConn != nil {
		if err := controller.NewExternalIPAttachmentFeedbackReconciler(
			localMgr.GetClient(), grpcConn, networkingNamespace,
		).SetupWithManager(mgr); err != nil {
			return fmt.Errorf("externalipattachment feedback controller: %w", err)
		}
	}
	return nil
}

func setupNATGatewayControllers(
	mgr mcmanager.Manager, localMgr ctrl.Manager, grpcConn *grpc.ClientConn,
	networkingNamespace string, provider provisioning.ProvisioningProvider,
	statusPollInterval time.Duration, maxJobHistory int, targetCluster multicluster.ClusterName,
) error {
	if grpcConn != nil {
		if err := controller.NewNATGatewayFeedbackReconciler(
			localMgr.GetClient(), grpcConn, networkingNamespace,
		).SetupWithManager(mgr); err != nil {
			return fmt.Errorf("natgateway feedback controller: %w", err)
		}
	}
	if err := controller.NewNATGatewayReconciler(
		mgr, networkingNamespace, provider, statusPollInterval, maxJobHistory, targetCluster,
	).SetupWithManager(mgr); err != nil {
		return fmt.Errorf("natgateway controller: %w", err)
	}
	return nil
}

// setupBareMetalInstanceControllers registers the BareMetalInstance feedback controller
// when a gRPC connection to the fulfillment service is available.
func setupBareMetalInstanceControllers(
	mgr mcmanager.Manager,
	grpcConn *grpc.ClientConn,
) error {
	localMgr := mgr.GetLocalManager()
	bareMetalInstanceNamespace := os.Getenv(envBareMetalInstanceNamespace)
	if bareMetalInstanceNamespace == "" {
		bareMetalInstanceNamespace = controller.DefaultBareMetalInstanceNamespace
	}

	if grpcConn != nil {
		if err := (controller.NewBareMetalInstanceFeedbackReconciler(
			localMgr.GetClient(),
			grpcConn,
			bareMetalInstanceNamespace,
		)).SetupWithManager(mgr); err != nil {
			return fmt.Errorf("baremetalinstance feedback controller: %w", err)
		}
	}
	return nil
}

// +kubebuilder:rbac:groups="",resources=configmaps,verbs=get;list;watch

func main() {
	var err error

	var metricsAddr string
	var enableLeaderElection bool
	var probeAddr string
	var secureMetrics bool
	var enableHTTP2 bool
	var grpcPlaintext bool
	var grpcInsecure bool
	var grpcTokenFile string
	var fulfillmentServerAddress string
	var remoteClusterKubeconfig string
	var tlsOpts []func(*tls.Config)
	flag.StringVar(&metricsAddr, "metrics-bind-address", "0", "The address the metrics endpoint binds to. "+
		"Use :8443 for HTTPS or :8080 for HTTP, or leave as 0 to disable the metrics service.")
	flag.StringVar(&probeAddr, "health-probe-bind-address", ":8081", "The address the probe endpoint binds to.")
	flag.BoolVar(&enableLeaderElection, "leader-elect", false,
		"Enable leader election for controller manager. "+
			"Enabling this will ensure there is only one active controller manager.")
	flag.BoolVar(&secureMetrics, "metrics-secure", true,
		"If set, the metrics endpoint is served securely via HTTPS. Use --metrics-secure=false to use HTTP instead.")
	flag.BoolVar(&enableHTTP2, "enable-http2", false,
		"If set, HTTP/2 will be enabled for the metrics and webhook servers")
	flag.BoolVar(&grpcPlaintext,
		"grpc-plaintext",
		false,
		"Enable gRPC without TLS.",
	)
	flag.BoolVar(
		&grpcInsecure,
		"grpc-insecure",
		false,
		"Enable insecure gRPC, without checking the server TLS certificates.",
	)
	flag.StringVar(
		&grpcTokenFile,
		"fulfillment-server-token-file",
		os.Getenv("OSAC_FULFILLMENT_TOKEN_FILE"),
		"Path of the file containing the token for gRPC authentication to the fulfillment service.",
	)
	flag.StringVar(
		&fulfillmentServerAddress,
		"fulfillment-server-address",
		os.Getenv("OSAC_FULFILLMENT_SERVER_ADDRESS"),
		"Address of the fulfillment server.",
	)
	flag.StringVar(
		&remoteClusterKubeconfig,
		"remote-cluster-kubeconfig",
		os.Getenv(envRemoteClusterKubeconfig),
		"Path to the kubeconfig for the remote cluster (supported by tenant and compute-instance controllers only).",
	)

	// Controller enable flags. Defaults from env; if none are set (flag or env), all controllers are enabled.
	ctrlFlags := registerControllerFlags()
	opts := zap.Options{
		Development: true,
	}
	opts.BindFlags(flag.CommandLine)
	flag.Parse()
	ctrl.SetLogger(zap.New(zap.UseFlagOptions(&opts)))

	ctrlFlags.enableAllIfNoneSet()

	if remoteClusterKubeconfig != "" && ctrlFlags.Cluster {
		setupLog.Error(nil, "remote cluster kubeconfig option is not supported along with cluster controller")
		os.Exit(1)
	}
	if remoteClusterKubeconfig != "" && ctrlFlags.BareMetalInstance {
		setupLog.Error(nil, "remote cluster kubeconfig option is not supported along with bare metal instance controller")
		os.Exit(1)
	}

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

	webhookServer := webhook.NewServer(webhook.Options{
		TLSOpts: tlsOpts,
	})

	// Metrics endpoint is enabled in 'config/default/kustomization.yaml'. The Metrics options configure the server.
	// More info:
	// - https://pkg.go.dev/sigs.k8s.io/controller-runtime@v0.19.0/pkg/metrics/server
	// - https://book.kubebuilder.io/reference/metrics.html
	metricsServerOptions := metricsserver.Options{
		BindAddress:   metricsAddr,
		SecureServing: secureMetrics,
		// TODO(user): TLSOpts is used to allow configuring the TLS config used for the server. If certificates are
		// not provided, self-signed certificates will be generated by default. This option is not recommended for
		// production environments as self-signed certificates do not offer the same level of trust and security
		// as certificates issued by a trusted Certificate Authority (CA). The primary risk is potentially allowing
		// unauthorized access to sensitive metrics data. Consider replacing with CertDir, CertName, and KeyName
		// to provide certificates, ensuring the server communicates using trusted and secure certificates.
		TLSOpts: tlsOpts,
	}

	if secureMetrics {
		// FilterProvider is used to protect the metrics endpoint with authn/authz.
		// These configurations ensure that only authorized users and service accounts
		// can access the metrics endpoint. The RBAC are configured in 'config/rbac/kustomization.yaml'. More info:
		// https://pkg.go.dev/sigs.k8s.io/controller-runtime@v0.19.0/pkg/metrics/filters#WithAuthenticationAndAuthorization
		metricsServerOptions.FilterProvider = filters.WithAuthenticationAndAuthorization
	}

	// Add the schemes depending if controllers reconcile locally or remotely
	localScheme := runtime.NewScheme()
	var remoteScheme *runtime.Scheme
	var remoteProvider multicluster.Provider
	var remoteCluster cluster.Cluster
	if remoteClusterKubeconfig == "" {
		localScheme = runtime.NewScheme()
		addSchemesForLocalControllers(localScheme,
			ctrlFlags.Cluster,
			ctrlFlags.ComputeInstance,
			ctrlFlags.Tenant,
			ctrlFlags.Networking,
			ctrlFlags.BareMetalInstance,
		)
	} else {
		remoteScheme = runtime.NewScheme()
		addSchemesForRemoteControllers(localScheme, remoteScheme,
			ctrlFlags.ComputeInstance,
			ctrlFlags.Tenant,
		)
		remoteCluster, err = newClusterFromKubeconfig(remoteClusterKubeconfig, remoteScheme)
		if err != nil {
			setupLog.Error(err, "unable to create remote cluster from kubeconfig")
			os.Exit(1)
		}
		remoteProvider = single.New(remoteClusterName, remoteCluster)
	}

	cfg := ctrl.GetConfigOrDie()

	mgr, err := mcmanager.New(cfg, remoteProvider, manager.Options{
		Scheme:                 localScheme,
		Metrics:                metricsServerOptions,
		WebhookServer:          webhookServer,
		HealthProbeBindAddress: probeAddr,
		LeaderElection:         enableLeaderElection,
		LeaderElectionID:       "95f7e044.openshift.io",
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

	// Create the gRPC connection:
	var grpcConn *grpc.ClientConn
	if fulfillmentServerAddress != "" {
		setupLog.Info("gRPC connection to fulfillment service is enabled")
		grpcConn, err = createGrpcConn(grpcPlaintext, grpcInsecure, grpcTokenFile, fulfillmentServerAddress)
		if err != nil {
			setupLog.Error(err, "failed to create gRPC connection to fulfillment service")
			os.Exit(1)
		}
		defer grpcConn.Close() //nolint:errcheck
	} else {
		setupLog.Info("gRPC connection to fulfillment service is disabled")
	}

	maxJobHistory := helpers.GetEnvWithDefault(envMaxJobHistory, provisioning.DefaultMaxJobHistory, func(v int) bool {
		return v >= 1
	})
	setupLog.Info("job history configuration", "maxJobs", maxJobHistory)

	if ctrlFlags.Cluster {
		if err := setupClusterControllers(mgr, grpcConn, maxJobHistory); err != nil {
			setupLog.Error(err, "unable to setup cluster controllers")
			os.Exit(1)
		}
	}
	if ctrlFlags.ComputeInstance {
		if err := setupComputeInstanceControllers(mgr, grpcConn, maxJobHistory); err != nil {
			setupLog.Error(err, "unable to setup computeinstance controllers")
			os.Exit(1)
		}
	}
	if ctrlFlags.Tenant {
		if err := setupTenantController(mgr); err != nil {
			setupLog.Error(err, "unable to setup tenant controller")
			os.Exit(1)
		}
	}
	if ctrlFlags.Storage {
		if err := setupStorageController(mgr, grpcConn, maxJobHistory); err != nil {
			setupLog.Error(err, "unable to setup storage controller")
			os.Exit(1)
		}
	}
	if ctrlFlags.Networking {
		if err := setupNetworkingControllers(mgr, grpcConn, maxJobHistory); err != nil {
			setupLog.Error(err, "unable to setup networking controllers")
			os.Exit(1)
		}
	}
	if ctrlFlags.BareMetalInstance {
		if err := setupBareMetalInstanceControllers(mgr, grpcConn); err != nil {
			setupLog.Error(err, "unable to setup baremetalinstance controllers")
			os.Exit(1)
		}
	}

	// +kubebuilder:scaffold:builder

	// Register data migrations as a leader-election runnable.
	// Migrations run once after this instance becomes leader.
	migrationClient, err := client.New(cfg, client.Options{})
	if err != nil {
		setupLog.Error(err, "unable to create client for migrations")
		os.Exit(1)
	}
	if err := mgr.GetLocalManager().Add(migrations.NewRunnable(migrationClient)); err != nil {
		setupLog.Error(err, "unable to register migrations")
		os.Exit(1)
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
	if err := startComponents(ctrl.SetupSignalHandler(), remoteCluster, remoteProvider, mgr); err != nil {
		setupLog.Error(err, "problem running manager")
		os.Exit(1)
	}
}

// startComponents runs the remote cluster, remote provider, and manager
// concurrently using errgroup. If any component returns an error, the shared
// context is cancelled and all other components are signaled to stop.
// Context cancellation errors are filtered since they represent normal shutdown.
func startComponents(
	ctx context.Context,
	remoteCluster cluster.Cluster,
	remoteProvider multicluster.Provider,
	mgr mcmanager.Manager,
) error {
	g, ctx := errgroup.WithContext(ctx)
	if remoteCluster != nil {
		g.Go(func() error {
			return ignoreCanceled(remoteCluster.Start(ctx))
		})
	}
	if remoteProvider != nil {
		g.Go(func() error {
			return ignoreCanceled(remoteProvider.(multicluster.ProviderRunnable).Start(ctx, mgr))
		})
	}
	g.Go(func() error {
		return ignoreCanceled(mgr.Start(ctx))
	})
	return g.Wait()
}

// ignoreCanceled returns nil if the error is exactly context.Canceled,
// since that's the expected shutdown path when errgroup cancels the context.
// Only pure cancellation is filtered — mixed/wrapped errors are preserved.
func ignoreCanceled(err error) error {
	if err == context.Canceled {
		return nil
	}
	return err
}

//nolint:nakedret
func createGrpcConn(plaintext, insecure bool, tokenFile, serverAddress string) (result *grpc.ClientConn, err error) {
	// Configure use of TLS:
	var dialOpts []grpc.DialOption
	var transportCreds credentials.TransportCredentials
	if plaintext {
		transportCreds = insecurecredentials.NewCredentials()
	} else {
		tlsConfig := &tls.Config{}
		if insecure {
			tlsConfig.InsecureSkipVerify = true
		}

		// TODO: This should have been the non-experimental package, but we need to use this one because
		// currently the OpenShift router doesn't seem to support ALPN, and the regular credentials package
		// requires it since version 1.67. See here for details:
		//
		// https://github.com/grpc/grpc-go/issues/434
		// https://github.com/grpc/grpc-go/pull/7980
		//
		// Is there a way to configure the OpenShift router to avoid this?
		transportCreds = experimentalcredentials.NewTLSWithALPNDisabled(tlsConfig)
	}
	if transportCreds != nil {
		dialOpts = append(dialOpts, grpc.WithTransportCredentials(transportCreds))
	}

	// Confgure use of token:
	if tokenFile != "" {
		dialOpts = append(dialOpts, grpc.WithPerRPCCredentials(oauth.TokenSource{
			TokenSource: &fileTokenSource{
				tokenFile: tokenFile,
			},
		}))
	}

	// Create the connection:
	conn, err := grpc.NewClient(serverAddress, dialOpts...)
	if err != nil {
		return
	}

	result = conn
	return
}

// fileTokenSource is a token source that reads the token from a file whenever it is needed.
type fileTokenSource struct {
	tokenFile string
}

func (s *fileTokenSource) Token() (token *oauth2.Token, err error) {
	var data []byte
	data, err = os.ReadFile(s.tokenFile)
	if err != nil {
		err = fmt.Errorf("failed to read token from file '%s': %w", s.tokenFile, err)
		return
	}
	token = &oauth2.Token{
		AccessToken: strings.TrimSpace(string(data)),
	}
	return
}

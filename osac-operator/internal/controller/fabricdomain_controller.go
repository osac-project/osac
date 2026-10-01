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

package controller

import (
	"context"
	"fmt"
	"strconv"
	"time"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/equality"
	apimeta "k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/tools/events"
	"k8s.io/client-go/util/retry"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	controllerutil "sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	ctrllog "sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
	mcbuilder "sigs.k8s.io/multicluster-runtime/pkg/builder"
	mchandler "sigs.k8s.io/multicluster-runtime/pkg/handler"
	mcmanager "sigs.k8s.io/multicluster-runtime/pkg/manager"
	mcreconcile "sigs.k8s.io/multicluster-runtime/pkg/reconcile"

	"github.com/osac-project/osac/osac-operator/api/v1alpha1"
	"github.com/osac-project/osac/osac-operator/pkg/provisioning"
	privatev1 "github.com/osac-project/osac/proto/gen/osac/private/v1"
)

const (
	osacFabricDomainFinalizer           = "osac.openshift.io/fabricdomain-finalizer"
	osacFabricDomainProtectionFinalizer = "osac.openshift.io/fabricdomain-protection"
	fabricDomainConfigResyncInterval    = 5 * time.Minute
	fabricDomainUnsupportedTypeReason   = "UnsupportedType"
	fabricDomainMissingBackendIDReason  = "MissingBackendID"
	fabricDomainNetworkClassUnavailable = "NetworkClassUnavailable"
	fabricDomainVirtualNetworkNotReady  = "VirtualNetworkNotReady"
)

// FabricDomainReconciler reconciles FabricDomain resources into Netris server clusters.
type FabricDomainReconciler struct {
	client.Client
	APIReader                    client.Reader
	Scheme                       *runtime.Scheme
	NetworkingNamespace          string
	ProvisioningProvider         provisioning.ProvisioningProvider
	NetworkClassesClient         privatev1.NetworkClassesClient
	BareMetalInstanceTypesClient privatev1.BareMetalInstanceTypesClient
	StatusPollInterval           time.Duration
	MaxJobHistory                int
	NetworkProvisioningEnabled   bool
	Recorder                     events.EventRecorder
	observability                *fabricDomainObservability
}

// NewFabricDomainReconciler creates a reconciler for FabricDomain resources.
func NewFabricDomainReconciler(
	mgr mcmanager.Manager,
	networkingNamespace string,
	provider provisioning.ProvisioningProvider,
	networkClassesClient privatev1.NetworkClassesClient,
	instanceTypesClient privatev1.BareMetalInstanceTypesClient,
	statusPollInterval time.Duration,
	maxJobHistory int,
) *FabricDomainReconciler {
	if mgr == nil {
		panic("mgr must not be nil")
	}
	if statusPollInterval <= 0 {
		statusPollInterval = provisioning.DefaultStatusPollInterval
	}
	if maxJobHistory <= 0 {
		maxJobHistory = provisioning.DefaultMaxJobHistory
	}
	localMgr := mgr.GetLocalManager()
	return &FabricDomainReconciler{
		Client:                       localMgr.GetClient(),
		APIReader:                    localMgr.GetAPIReader(),
		Scheme:                       localMgr.GetScheme(),
		NetworkingNamespace:          networkingNamespace,
		ProvisioningProvider:         provider,
		NetworkClassesClient:         networkClassesClient,
		BareMetalInstanceTypesClient: instanceTypesClient,
		StatusPollInterval:           statusPollInterval,
		MaxJobHistory:                maxJobHistory,
		NetworkProvisioningEnabled:   true,
		Recorder:                     localMgr.GetEventRecorder("fabricdomain-controller"),
	}
}

// +kubebuilder:rbac:groups="",resources=configmaps,verbs=get;list;watch
// +kubebuilder:rbac:groups=osac.openshift.io,resources=fabricdomains,verbs=get;list;watch;update;patch
// +kubebuilder:rbac:groups=osac.openshift.io,resources=fabricdomains/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=osac.openshift.io,resources=fabricdomains/finalizers,verbs=update
// +kubebuilder:rbac:groups=osac.openshift.io,resources=virtualnetworks,verbs=get;list;update;patch

// Reconcile drives FabricDomain provisioning and safe deletion.
func (r *FabricDomainReconciler) Reconcile(ctx context.Context, req mcreconcile.Request) (ctrl.Result, error) {
	log := ctrllog.FromContext(ctx)
	domain := &v1alpha1.FabricDomain{}
	if err := r.Get(ctx, req.NamespacedName, domain); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}
	if r.NetworkingNamespace != "" && domain.Namespace != r.NetworkingNamespace {
		return ctrl.Result{}, nil
	}
	if domain.DeletionTimestamp.IsZero() && domain.Annotations[osacManagementStateAnnotation] == ManagementStateUnmanaged {
		log.Info("ignoring FabricDomain due to management-state annotation")
		return ctrl.Result{}, nil
	}

	if err := r.restoreFabricDomainProvisionedAt(ctx, domain); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}
	oldStatus := domain.Status.DeepCopy()
	hadFinalizer := controllerutil.ContainsFinalizer(domain, osacFabricDomainFinalizer)
	var result ctrl.Result
	var err error
	if domain.DeletionTimestamp.IsZero() {
		result, err = r.handleUpdate(ctx, domain)
	} else {
		result, err = r.handleDelete(ctx, domain)
	}

	// Once the resource finalizer is removed, Kubernetes may delete the object
	// immediately. Intermediate deprovisioning status is persisted while cleanup
	// is pending; the final terminal job record is not needed after deletion.
	if (!equality.Semantic.DeepEqual(domain.Status, *oldStatus) ||
		oldStatus.ProvisionedAt == nil && fabricDomainReady(domain.Status)) &&
		(domain.DeletionTimestamp.IsZero() || controllerutil.ContainsFinalizer(domain, osacFabricDomainFinalizer)) {
		if updateErr := r.persistFabricDomainStatusAndObserve(ctx, domain); updateErr != nil {
			return result, updateErr
		}
	}
	if err == nil && hadFinalizer && !domain.DeletionTimestamp.IsZero() &&
		!controllerutil.ContainsFinalizer(domain, osacFabricDomainFinalizer) {
		r.recordFabricDomainDeleted(domain)
	}
	return result, err
}

func (r *FabricDomainReconciler) handleUpdate(ctx context.Context, domain *v1alpha1.FabricDomain) (ctrl.Result, error) {
	if controllerutil.AddFinalizer(domain, osacFabricDomainFinalizer) {
		if err := r.Update(ctx, domain); err != nil {
			return ctrl.Result{}, err
		}
		return ctrl.Result{}, nil
	}

	vnet, result, err := r.resolveFabricDomainVirtualNetwork(ctx, domain)
	if err != nil || result.RequeueAfter > 0 || vnet == nil {
		return result, err
	}

	if !r.NetworkProvisioningEnabled {
		domain.Status.Phase = v1alpha1.FabricDomainPhaseProgressing
		setFabricDomainCondition(domain, metav1.ConditionFalse, "ProvisioningDisabled",
			"FabricDomain provisioning requires networking provisioning to be enabled on the operator")
		domain.Status.Members = pendingFabricDomainMembers(domain.Spec.Servers)
		return ctrl.Result{RequeueAfter: fabricDomainConfigResyncInterval}, nil
	}

	config, result, err := r.resolveFabricDomainProvisioningConfig(ctx, domain, vnet)
	if err != nil || result.RequeueAfter > 0 {
		return result, err
	}

	if domain.Status.ProvisioningConfig == nil {
		if domain.Status.ProvisioningIntent || domain.Status.BackendID != "" || len(domain.Status.ProvisioningJobs) > 0 {
			domain.Status.Phase = v1alpha1.FabricDomainPhaseFailed
			setFabricDomainCondition(domain, metav1.ConditionFalse, "UnresolvedBackendBinding",
				"existing provisioning has no recorded hardware binding; verify the backend before restoring provisioningConfig")
			return ctrl.Result{RequeueAfter: defaultPreconditionRequeueInterval}, nil
		}
		domain.Status.ProvisioningConfig = &config.Binding
	} else if *domain.Status.ProvisioningConfig != config.Binding {
		domain.Status.Phase = v1alpha1.FabricDomainPhaseFailed
		setFabricDomainCondition(domain, metav1.ConditionFalse, "BackendBindingChanged",
			"the NetworkClass, template, VPC, and region must match the recorded provisioning binding; recreate the domain to change it")
		return ctrl.Result{RequeueAfter: defaultPreconditionRequeueInterval}, nil
	}
	domain.Status.VPCID = config.Binding.VPCID

	desiredVersion, err := provisioning.ComputeDesiredConfigVersion(struct {
		Spec   v1alpha1.FabricDomainSpec
		Config resolvedFabricDomainConfig
	}{domain.Spec, *config})
	if err != nil {
		return ctrl.Result{}, fmt.Errorf("computing FabricDomain desired config version: %w", err)
	}
	previousVersion := domain.Status.DesiredConfigVersion
	domain.Status.DesiredConfigVersion = desiredVersion
	if provisioning.IsConfigApplied(&domain.Status.ProvisioningJobs, desiredVersion) && validNetrisID(domain.Status.BackendID) {
		domain.Status.Phase = v1alpha1.FabricDomainPhaseReady
		domain.Status.Members = activeFabricDomainMembers(domain.Spec.Servers)
		setReadyConditionTrue(&domain.Status.Conditions)
		return ctrl.Result{RequeueAfter: fabricDomainConfigResyncInterval}, nil
	}
	if domain.Status.Phase == "" || domain.Status.Phase == v1alpha1.FabricDomainPhaseReady &&
		!provisioning.IsConfigApplied(&domain.Status.ProvisioningJobs, desiredVersion) ||
		domain.Status.Phase == v1alpha1.FabricDomainPhaseFailed && previousVersion != desiredVersion {
		domain.Status.Phase = v1alpha1.FabricDomainPhaseProgressing
		domain.Status.Members = pendingFabricDomainMembers(domain.Spec.Servers)
		setFabricDomainCondition(domain, metav1.ConditionFalse, "Provisioning", "FabricDomain provisioning is in progress")
	}

	return r.runFabricDomainProvisioning(ctx, domain, vnet, config.Binding.TemplateID, desiredVersion)
}

func (r *FabricDomainReconciler) resolveFabricDomainVirtualNetwork(ctx context.Context, domain *v1alpha1.FabricDomain) (*v1alpha1.VirtualNetwork, ctrl.Result, error) {
	vnet, result, err := r.findVirtualNetwork(ctx, domain)
	if err != nil || result.RequeueAfter > 0 {
		return vnet, result, err
	}
	if !vnet.DeletionTimestamp.IsZero() {
		message := fmt.Sprintf("VirtualNetwork %q is deleting", vnet.Name)
		setFabricDomainCondition(domain, metav1.ConditionFalse, fabricDomainVirtualNetworkNotReady, message)
		return nil, ctrl.Result{RequeueAfter: defaultPreconditionRequeueInterval}, nil
	}
	if tenant := domain.Annotations[osacTenantKey]; tenant == "" || vnet.Annotations[osacTenantKey] != tenant {
		domain.Status.Phase = v1alpha1.FabricDomainPhaseFailed
		setFabricDomainCondition(domain, metav1.ConditionFalse, "TenantMismatch", "FabricDomain and VirtualNetwork must have the same nonempty tenant annotation")
		return nil, ctrl.Result{RequeueAfter: defaultPreconditionRequeueInterval}, nil
	}
	if controllerutil.AddFinalizer(vnet, osacFabricDomainProtectionFinalizer) {
		if err := r.Update(ctx, vnet); err != nil {
			return nil, ctrl.Result{}, fmt.Errorf("protecting VirtualNetwork %q: %w", vnet.Name, err)
		}
		return nil, ctrl.Result{}, nil
	}
	return vnet, ctrl.Result{}, nil
}

func (r *FabricDomainReconciler) resolveFabricDomainProvisioningConfig(
	ctx context.Context,
	domain *v1alpha1.FabricDomain,
	vnet *v1alpha1.VirtualNetwork,
) (*resolvedFabricDomainConfig, ctrl.Result, error) {
	if vnet.Status.Phase != v1alpha1.VirtualNetworkPhaseReady || vnet.Status.BackendNetworkID == "" {
		message := fmt.Sprintf("waiting for VirtualNetwork %q to be Ready with a Netris VPC ID", vnet.Name)
		domain.Status.Phase = v1alpha1.FabricDomainPhaseProgressing
		setFabricDomainCondition(domain, metav1.ConditionFalse, fabricDomainVirtualNetworkNotReady, message)
		return nil, ctrl.Result{RequeueAfter: defaultPreconditionRequeueInterval}, nil
	}
	if !validNetrisID(vnet.Status.BackendNetworkID) {
		message := fmt.Sprintf("VirtualNetwork %q has invalid Netris VPC ID %q", vnet.Name, vnet.Status.BackendNetworkID)
		domain.Status.Phase = v1alpha1.FabricDomainPhaseFailed
		setFabricDomainCondition(domain, metav1.ConditionFalse, "InvalidVPCID", message)
		return nil, ctrl.Result{RequeueAfter: defaultPreconditionRequeueInterval}, nil
	}

	if domain.Spec.Type != v1alpha1.FabricDomainTypeEthernetEW {
		message := fmt.Sprintf("FabricDomain type %q is not supported by the Ethernet east-west provisioner", domain.Spec.Type)
		domain.Status.Phase = v1alpha1.FabricDomainPhaseFailed
		setFabricDomainCondition(domain, metav1.ConditionFalse, fabricDomainUnsupportedTypeReason, message)
		return nil, ctrl.Result{RequeueAfter: defaultPreconditionRequeueInterval}, nil
	}
	if r.NetworkClassesClient == nil {
		message := "the VirtualNetwork has no resolvable NetworkClass"
		domain.Status.Phase = v1alpha1.FabricDomainPhaseProgressing
		setFabricDomainCondition(domain, metav1.ConditionFalse, fabricDomainNetworkClassUnavailable, message)
		return nil, ctrl.Result{RequeueAfter: defaultPreconditionRequeueInterval}, nil
	}

	networkClassID := vnet.Spec.NetworkClass
	if networkClassID == "" {
		var err error
		networkClassID, err = lookupDefaultNetworkClassID(ctx, r.NetworkClassesClient)
		if err != nil {
			return nil, ctrl.Result{}, fmt.Errorf("resolving default NetworkClass: %w", err)
		}
	}
	if networkClassID == "" {
		message := "the VirtualNetwork has no resolvable NetworkClass"
		domain.Status.Phase = v1alpha1.FabricDomainPhaseProgressing
		setFabricDomainCondition(domain, metav1.ConditionFalse, fabricDomainNetworkClassUnavailable, message)
		return nil, ctrl.Result{RequeueAfter: defaultPreconditionRequeueInterval}, nil
	}

	ncResponse, err := r.NetworkClassesClient.Get(ctx, &privatev1.NetworkClassesGetRequest{Id: networkClassID})
	if err != nil {
		return nil, ctrl.Result{}, fmt.Errorf("fetching NetworkClass %q: %w", networkClassID, err)
	}
	networkClass := ncResponse.GetObject()
	if networkClass.GetMetadata().GetDeletionTimestamp() != nil || networkClass.GetFabricManager() != netrisFabricManager ||
		!networkClass.GetCapabilities().GetSupportsEastWestEthernet() {
		domain.Status.Phase = v1alpha1.FabricDomainPhaseFailed
		setFabricDomainCondition(domain, metav1.ConditionFalse, "UnsupportedNetworkClass",
			fmt.Sprintf("NetworkClass %q must be active and advertise Netris Ethernet east-west support", networkClassID))
		return nil, ctrl.Result{RequeueAfter: defaultPreconditionRequeueInterval}, nil
	}
	templateID, instanceTypes, err := r.resolveFabricDomainHardware(ctx, domain, networkClassID)
	if err != nil {
		domain.Status.Phase = v1alpha1.FabricDomainPhaseFailed
		setFabricDomainCondition(domain, metav1.ConditionFalse, "InvalidHardwareBinding", err.Error())
		return nil, ctrl.Result{RequeueAfter: defaultPreconditionRequeueInterval}, nil
	}
	if r.ProvisioningProvider == nil {
		message := "no AAP provisioning provider is configured"
		domain.Status.Phase = v1alpha1.FabricDomainPhaseProgressing
		setFabricDomainCondition(domain, metav1.ConditionFalse, "ProvisionerUnavailable", message)
		return nil, ctrl.Result{RequeueAfter: defaultPreconditionRequeueInterval}, nil
	}
	return &resolvedFabricDomainConfig{
		Binding: v1alpha1.FabricDomainProvisioningConfig{
			NetworkClass: networkClassID, TemplateID: templateID,
			VPCID: vnet.Status.BackendNetworkID, Region: vnet.Spec.Region,
		},
		InstanceTypes: instanceTypes,
	}, ctrl.Result{}, nil
}

func (r *FabricDomainReconciler) runFabricDomainProvisioning(
	ctx context.Context,
	domain *v1alpha1.FabricDomain,
	vnet *v1alpha1.VirtualNetwork,
	templateID, desiredVersion string,
) (ctrl.Result, error) {
	if !domain.Status.ProvisioningIntent {
		domain.Status.ProvisioningIntent = true
		if err := r.updateStatusWithRetry(ctx, client.ObjectKeyFromObject(domain), domain.Status); err != nil {
			return ctrl.Result{}, fmt.Errorf("persisting FabricDomain provisioning intent: %w", err)
		}
	}

	aapCtx := provisioning.WithAAPExtraVars(ctx, fabricDomainAAPExtraVars(domain, vnet, templateID))
	result, err := provisioning.RunProvisioningLifecycle(aapCtx, r.ProvisioningProvider, domain,
		&provisioning.State{Jobs: &domain.Status.ProvisioningJobs, DesiredConfigVersion: desiredVersion},
		r.MaxJobHistory, r.StatusPollInterval,
		fabricDomainPollCallbacks(domain, desiredVersion),
		func() bool {
			return provisioning.CheckAPIServerForNonTerminalProvisionJob(ctx, r.APIReader,
				client.ObjectKeyFromObject(domain), &v1alpha1.FabricDomain{}, func(obj client.Object) []v1alpha1.JobStatus {
					return obj.(*v1alpha1.FabricDomain).Status.ProvisioningJobs
				})
		},
		func() error {
			return r.updateStatusWithRetry(ctx, client.ObjectKeyFromObject(domain), domain.Status)
		},
	)
	if err != nil {
		return result, err
	}
	if result.RequeueAfter == 0 {
		// Catalog configuration is fetched over gRPC. Periodically verify that the
		// members remain compatible with the recorded backend binding.
		result.RequeueAfter = fabricDomainConfigResyncInterval
		if !provisioning.IsConfigApplied(&domain.Status.ProvisioningJobs, desiredVersion) {
			result.RequeueAfter = r.StatusPollInterval
		}
	}
	return result, nil
}

func fabricDomainPollCallbacks(domain *v1alpha1.FabricDomain, desiredVersion string) *provisioning.PollCallbacks {
	return &provisioning.PollCallbacks{
		OnFailed: func(message string) {
			domain.Status.Phase = v1alpha1.FabricDomainPhaseFailed
			domain.Status.Members = failedFabricDomainMembers(domain.Spec.Servers, message)
			setReadyConditionFailed(&domain.Status.Conditions, message)
		},
		OnSuccess: func(status provisioning.ProvisionStatus) {
			backendID := outputString(status.Outputs, "server_cluster_id", "backend_id", "backendId")
			if backendID == "" {
				backendID = domain.Status.BackendID
			}
			if !validNetrisID(backendID) {
				domain.Status.Phase = v1alpha1.FabricDomainPhaseFailed
				message := fmt.Sprintf("AAP job succeeded but returned invalid server_cluster_id artifact %q", backendID)
				markFabricDomainProvisionJobFailed(domain, desiredVersion, message)
				setFabricDomainCondition(domain, metav1.ConditionFalse, fabricDomainMissingBackendIDReason, message)
				return
			}
			domain.Status.BackendID = backendID
			if vpcID := outputString(status.Outputs, "server_cluster_vpc_id", "vpc_id", "vpcId"); vpcID != "" && vpcID != domain.Status.VPCID {
				message := fmt.Sprintf("AAP job returned VPC ID %q, expected %q", vpcID, domain.Status.VPCID)
				domain.Status.Phase = v1alpha1.FabricDomainPhaseFailed
				markFabricDomainProvisionJobFailed(domain, desiredVersion, message)
				setFabricDomainCondition(domain, metav1.ConditionFalse, "MismatchedVPCID", message)
				return
			}
			if job := provisioning.FindLatestJobByType(domain.Status.ProvisioningJobs, v1alpha1.JobTypeProvision); job != nil &&
				job.ConfigVersion != "" && job.ConfigVersion != desiredVersion {
				domain.Status.Phase = v1alpha1.FabricDomainPhaseProgressing
				domain.Status.Members = pendingFabricDomainMembers(domain.Spec.Servers)
				setFabricDomainCondition(domain, metav1.ConditionFalse, "Provisioning", "previous membership update completed; the current server list is still pending")
				return
			}
			domain.Status.Phase = v1alpha1.FabricDomainPhaseReady
			domain.Status.Members = activeFabricDomainMembers(domain.Spec.Servers)
			setReadyConditionTrue(&domain.Status.Conditions)
		},
	}
}

func markFabricDomainProvisionJobFailed(domain *v1alpha1.FabricDomain, desiredVersion, message string) {
	if job := provisioning.FindLatestJobByType(domain.Status.ProvisioningJobs, v1alpha1.JobTypeProvision); job != nil {
		job.State = v1alpha1.JobStateFailed
		job.Message = message
		job.ConfigVersion = desiredVersion
	}
}

func validNetrisID(value string) bool {
	id, err := strconv.ParseUint(value, 10, 64)
	return err == nil && id > 0 && strconv.FormatUint(id, 10) == value
}

func (r *FabricDomainReconciler) handleDelete(ctx context.Context, domain *v1alpha1.FabricDomain) (ctrl.Result, error) {
	domain.Status.Phase = v1alpha1.FabricDomainPhaseDeleting
	if !controllerutil.ContainsFinalizer(domain, osacFabricDomainFinalizer) {
		return ctrl.Result{}, nil
	}

	hasProvisioningState := domain.Status.ProvisioningIntent || len(domain.Status.ProvisioningJobs) > 0 || domain.Status.BackendID != ""
	if hasProvisioningState {
		if r.ProvisioningProvider == nil {
			return ctrl.Result{}, fmt.Errorf("cannot clean up FabricDomain %q: AAP provisioning provider is unavailable", domain.Name)
		}
		if domain.Status.ProvisioningIntent && domain.Status.BackendID == "" &&
			provisioning.FindLatestJobByType(domain.Status.ProvisioningJobs, v1alpha1.JobTypeProvision) == nil {
			message := "AAP provisioning may still be running, but its job ID and ServerCluster ID were not saved; cleanup needs manual resolution"
			domain.Status.Phase = v1alpha1.FabricDomainPhaseFailed
			setFabricDomainCondition(domain, metav1.ConditionFalse, "UnresolvedProvisioningIntent", message)
			return ctrl.Result{}, fmt.Errorf("cannot safely delete FabricDomain %q: %s", domain.Name, message)
		}
		var vnet *v1alpha1.VirtualNetwork
		if domain.Status.BackendID == "" && domain.Status.ProvisioningConfig == nil {
			if domain.Status.VPCID == "" {
				return ctrl.Result{}, fmt.Errorf("cannot safely delete FabricDomain %q without a ServerCluster ID or VPC ID", domain.Name)
			}
			var result ctrl.Result
			var err error
			vnet, result, err = r.findVirtualNetwork(ctx, domain)
			if err != nil {
				return ctrl.Result{}, err
			}
			if result.RequeueAfter > 0 || vnet == nil {
				return ctrl.Result{}, fmt.Errorf("cannot safely delete FabricDomain %q without its VirtualNetwork site", domain.Name)
			}
		}
		aapCtx := provisioning.WithAAPExtraVars(ctx, fabricDomainAAPExtraVars(domain, vnet, ""))
		result, done, err := provisioning.RunDeprovisioningLifecycle(aapCtx, r.ProvisioningProvider, domain,
			&domain.Status.ProvisioningJobs, r.MaxJobHistory, r.StatusPollInterval)
		if err != nil || !done {
			return result, err
		}
	}

	if err := r.releaseVirtualNetworkProtection(ctx, domain); err != nil {
		return ctrl.Result{}, err
	}
	if controllerutil.RemoveFinalizer(domain, osacFabricDomainFinalizer) {
		if err := r.Update(ctx, domain); err != nil {
			return ctrl.Result{}, err
		}
	}
	return ctrl.Result{}, nil
}

func (r *FabricDomainReconciler) findVirtualNetwork(ctx context.Context, domain *v1alpha1.FabricDomain) (*v1alpha1.VirtualNetwork, ctrl.Result, error) {
	if domain.Spec.VirtualNetwork == "" {
		return nil, ctrl.Result{}, fmt.Errorf("FabricDomain %q has an empty virtualNetwork reference", domain.Name)
	}
	list := &v1alpha1.VirtualNetworkList{}
	if err := r.List(ctx, list, client.InNamespace(domain.Namespace),
		client.MatchingLabels{osacVirtualNetworkIDLabel: domain.Spec.VirtualNetwork}); err != nil {
		return nil, ctrl.Result{}, fmt.Errorf("listing parent VirtualNetwork %q: %w", domain.Spec.VirtualNetwork, err)
	}
	if len(list.Items) == 0 {
		ctrllog.FromContext(ctx).Info("parent VirtualNetwork not found; waiting", "virtualNetworkID", domain.Spec.VirtualNetwork)
		return nil, ctrl.Result{RequeueAfter: defaultPreconditionRequeueInterval}, nil
	}
	if len(list.Items) > 1 {
		return nil, ctrl.Result{}, fmt.Errorf("expected one VirtualNetwork with UUID %q but found %d", domain.Spec.VirtualNetwork, len(list.Items))
	}
	return &list.Items[0], ctrl.Result{}, nil
}

func (r *FabricDomainReconciler) releaseVirtualNetworkProtection(ctx context.Context, domain *v1alpha1.FabricDomain) error {
	vnet, result, err := r.findVirtualNetwork(ctx, domain)
	if err != nil {
		return err
	}
	if result.RequeueAfter > 0 || vnet == nil {
		// The parent can disappear after an administrator manually removes it.
		// There is no remaining object to protect in that case.
		return nil
	}

	domains := &v1alpha1.FabricDomainList{}
	if err := r.List(ctx, domains, client.InNamespace(domain.Namespace)); err != nil {
		return fmt.Errorf("listing FabricDomains before releasing VirtualNetwork protection: %w", err)
	}
	for i := range domains.Items {
		other := &domains.Items[i]
		if other.Name == domain.Name && other.UID == domain.UID {
			continue
		}
		if other.Spec.VirtualNetwork == domain.Spec.VirtualNetwork {
			return nil
		}
	}
	if controllerutil.RemoveFinalizer(vnet, osacFabricDomainProtectionFinalizer) {
		if err := r.Update(ctx, vnet); err != nil {
			return fmt.Errorf("releasing FabricDomain protection from VirtualNetwork %q: %w", vnet.Name, err)
		}
	}
	return nil
}

func (r *FabricDomainReconciler) updateStatusWithRetry(ctx context.Context, key client.ObjectKey, newStatus v1alpha1.FabricDomainStatus) error {
	return retry.RetryOnConflict(retry.DefaultRetry, func() error {
		latest := &v1alpha1.FabricDomain{}
		if err := r.Get(ctx, key, latest); err != nil {
			return err
		}
		latest.Status = newStatus
		return r.Status().Update(ctx, latest)
	})
}

func (r *FabricDomainReconciler) SetupWithManager(mgr mcmanager.Manager) error {
	if err := r.setupFabricDomainObservability(); err != nil {
		return err
	}
	return mcbuilder.ControllerManagedBy(mgr).
		For(&v1alpha1.FabricDomain{},
			mcbuilder.WithPredicates(NetworkingNamespacePredicate(r.NetworkingNamespace)),
			mcbuilder.WithEngageWithLocalCluster(true),
			mcbuilder.WithEngageWithProviderClusters(false)).
		Watches(&v1alpha1.VirtualNetwork{},
			mchandler.EnqueueRequestsFromMapFunc(r.mapVirtualNetworkToFabricDomains),
			mcbuilder.WithPredicates(NetworkingNamespacePredicate(r.NetworkingNamespace)),
			mcbuilder.WithEngageWithLocalCluster(true),
			mcbuilder.WithEngageWithProviderClusters(false)).
		Watches(&corev1.ConfigMap{},
			mchandler.EnqueueRequestsFromMapFunc(r.mapInventoryToFabricDomains),
			mcbuilder.WithPredicates(NetworkingNamespacePredicate(r.NetworkingNamespace)),
			mcbuilder.WithEngageWithLocalCluster(true),
			mcbuilder.WithEngageWithProviderClusters(false)).
		Complete(r)
}

func (r *FabricDomainReconciler) mapVirtualNetworkToFabricDomains(ctx context.Context, obj client.Object) []reconcile.Request {
	virtualNetworkID := obj.GetLabels()[osacVirtualNetworkIDLabel]
	if virtualNetworkID == "" || r.NetworkingNamespace != "" && obj.GetNamespace() != r.NetworkingNamespace {
		return nil
	}
	domains := &v1alpha1.FabricDomainList{}
	if err := r.List(ctx, domains, client.InNamespace(obj.GetNamespace())); err != nil {
		ctrllog.FromContext(ctx).Error(err, "listing FabricDomains for VirtualNetwork change", "virtualNetwork", obj.GetName())
		return nil
	}
	requests := make([]reconcile.Request, 0, len(domains.Items))
	for i := range domains.Items {
		if domains.Items[i].Spec.VirtualNetwork == virtualNetworkID {
			requests = append(requests, reconcile.Request{NamespacedName: client.ObjectKeyFromObject(&domains.Items[i])})
		}
	}
	return requests
}

func fabricDomainAAPExtraVars(domain *v1alpha1.FabricDomain, vnet *v1alpha1.VirtualNetwork, templateID string) map[string]any {
	annotations := make(map[string]string, len(domain.Annotations)+1)
	for key, value := range domain.Annotations {
		annotations[key] = value
	}
	annotations[osacImplementationStrategyAnnotation] = netrisFabricManager
	labels := make(map[string]string, len(domain.Labels))
	for key, value := range domain.Labels {
		labels[key] = value
	}
	metadata := map[string]any{
		"name":        domain.Name,
		"namespace":   domain.Namespace,
		"uid":         string(domain.UID),
		"labels":      labels,
		"annotations": annotations,
	}
	spec := map[string]any{
		"servers":   append([]string(nil), domain.Spec.Servers...),
		"backendId": domain.Status.BackendID,
	}
	if templateID != "" {
		spec["templateId"] = templateID
	}
	if domain.Status.VPCID != "" {
		spec["vpcId"] = domain.Status.VPCID
	}
	if vnet != nil {
		spec["virtualNetworkName"] = vnet.Name
		spec["region"] = vnet.Spec.Region
	}
	if domain.Status.ProvisioningConfig != nil {
		spec["region"] = domain.Status.ProvisioningConfig.Region
		spec["vpcId"] = domain.Status.ProvisioningConfig.VPCID
	}
	return map[string]any{
		"ansible_eda": map[string]any{
			"event": map[string]any{
				"payload": map[string]any{
					"apiVersion": "osac.openshift.io/v1alpha1",
					"kind":       "ServerCluster",
					"metadata":   metadata,
					"spec":       spec,
				},
			},
		},
	}
}

func outputString(outputs map[string]any, keys ...string) string {
	for _, key := range keys {
		if value, ok := outputs[key]; ok && value != nil {
			return fmt.Sprint(value)
		}
	}
	return ""
}

func pendingFabricDomainMembers(servers []string) []v1alpha1.FabricDomainMemberStatus {
	return fabricDomainMembers(servers, v1alpha1.FabricDomainMemberStatePending, "")
}

func activeFabricDomainMembers(servers []string) []v1alpha1.FabricDomainMemberStatus {
	return fabricDomainMembers(servers, v1alpha1.FabricDomainMemberStateActive, "")
}

func failedFabricDomainMembers(servers []string, message string) []v1alpha1.FabricDomainMemberStatus {
	return fabricDomainMembers(servers, v1alpha1.FabricDomainMemberStateFailed, message)
}

func fabricDomainMembers(servers []string, state v1alpha1.FabricDomainMemberState, message string) []v1alpha1.FabricDomainMemberStatus {
	members := make([]v1alpha1.FabricDomainMemberStatus, len(servers))
	for i, server := range servers {
		members[i] = v1alpha1.FabricDomainMemberStatus{Server: server, State: state, Message: message}
	}
	return members
}

func setFabricDomainCondition(domain *v1alpha1.FabricDomain, status metav1.ConditionStatus, reason, message string) {
	apimeta.SetStatusCondition(&domain.Status.Conditions, metav1.Condition{
		Type:    v1alpha1.ConditionReady,
		Status:  status,
		Reason:  reason,
		Message: message,
	})
}

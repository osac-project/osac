package controller

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	v1alpha1 "github.com/osac-project/osac/osac-operator/api/v1alpha1"
	"github.com/osac-project/osac/osac-operator/pkg/dispatcher"
	"github.com/osac-project/osac/osac-operator/pkg/networkmanager"
	"github.com/osac-project/osac/osac-operator/pkg/provisioning"
)

const osacNATGatewayContractVersionAnnotation = "osac.openshift.io/natgateway-contract-version"

var errNATGatewayContract = errors.New("NATGateway contract validation failed")

type natGatewayJobInputs struct {
	name            string
	namespace       string
	uid             string
	fulfillmentID   string
	generation      int64
	virtualNetwork  string
	externalIP      string
	tenant          string
	strategy        string
	contractVersion string
	deleting        bool
	context         *provisioning.NATGatewayContext
}

type natGatewayUpdateDependencies struct {
	virtualNetwork         *v1alpha1.VirtualNetwork
	externalIP             *v1alpha1.ExternalIP
	implementationStrategy string
	contractVersion        string
	context                provisioning.NATGatewayContext
}

func resolveNATGatewayManager(
	ctx context.Context,
	resolver *dispatcher.Resolver,
	networkClassID string,
	virtualNetworkStrategy string,
	pinnedStrategy string,
	pinnedVersion string,
) (string, string, error) {
	if pinnedVersion != "" && pinnedVersion != networkmanager.NATGatewayContractVersionV1 {
		return "", "", fmt.Errorf("%w: unsupported NATGateway contract version %q", errNATGatewayContract, pinnedVersion)
	}
	if pinnedVersion == networkmanager.NATGatewayContractVersionV1 {
		if pinnedStrategy == "" {
			return "", "", fmt.Errorf("%w: pinned NATGateway contract v1 has no implementation strategy", errNATGatewayContract)
		}
		return pinnedStrategy, pinnedVersion, nil
	}

	strategy := virtualNetworkStrategy
	version := pinnedVersion
	plan, err := resolveDispatchPlan(ctx, resolver, "NATGateway", networkClassID)
	if err != nil {
		if errors.Is(err, networkmanager.ErrUnsupportedNATGatewayContractVersion) {
			return "", "", fmt.Errorf("%w: %v", errNATGatewayContract, err)
		}
		return "", "", err
	}
	if plan != nil {
		target := plan.FabricTarget()
		if target == nil {
			target = plan.K8sTarget()
		}
		if target != nil {
			strategy = target.Manager.Name
			if version == "" {
				version = target.Manager.NATGatewayContractVersion
			}
		}
	}
	if version != "" && version != networkmanager.NATGatewayContractVersionV1 {
		return "", "", fmt.Errorf("%w: unsupported NATGateway contract version %q", errNATGatewayContract, version)
	}
	return strategy, version, nil
}

func buildNATGatewayContext(
	natgw *v1alpha1.NATGateway,
	virtualNetwork *v1alpha1.VirtualNetwork,
	externalIP *v1alpha1.ExternalIP,
) (provisioning.NATGatewayContext, bool, error) {
	if natgw == nil || virtualNetwork == nil || externalIP == nil {
		return provisioning.NATGatewayContext{}, false, fmt.Errorf("NATGateway dependencies are required")
	}
	if natgw.UID == "" || natgw.Labels[osacNATGatewayIDLabel] == "" || virtualNetwork.UID == "" || externalIP.UID == "" {
		return provisioning.NATGatewayContext{}, false, fmt.Errorf("NATGateway identity and dependency UIDs are required")
	}
	tenantID := natgw.Annotations[osacTenantKey]
	if tenantID == "" || virtualNetwork.Annotations[osacTenantKey] != tenantID ||
		externalIP.Annotations[osacTenantKey] != tenantID {
		return provisioning.NATGatewayContext{}, false, fmt.Errorf("NATGateway dependency tenant ownership does not match")
	}
	if natgw.Namespace != virtualNetwork.Namespace || natgw.Namespace != externalIP.Namespace {
		return provisioning.NATGatewayContext{}, false, fmt.Errorf("NATGateway dependencies must share the networking namespace")
	}
	if virtualNetwork.Labels[osacVirtualNetworkIDLabel] != natgw.Spec.VirtualNetwork ||
		externalIP.Labels[osacExternalIPIDLabel] != natgw.Spec.ExternalIP {
		return provisioning.NATGatewayContext{}, false, fmt.Errorf("NATGateway dependency identity does not match its references")
	}
	if !natgw.DeletionTimestamp.IsZero() || !virtualNetwork.DeletionTimestamp.IsZero() || !externalIP.DeletionTimestamp.IsZero() {
		return provisioning.NATGatewayContext{}, true, nil
	}
	if virtualNetwork.Status.Phase != v1alpha1.VirtualNetworkPhaseReady ||
		externalIP.Status.Phase != v1alpha1.ExternalIPPhaseReady ||
		externalIP.Status.State != v1alpha1.ExternalIPStateAllocated || externalIP.Status.Address == "" {
		return provisioning.NATGatewayContext{}, true, nil
	}
	if _, err := canonicalIPv4Address(externalIP.Status.Address); err != nil {
		return provisioning.NATGatewayContext{}, false, fmt.Errorf(
			"NATGateway ExternalIP address: %w", err,
		)
	}
	if _, err := provisioning.ValidateExternalIPPoolCIDR(
		[]string{virtualNetwork.Spec.IPv4CIDR},
	); err != nil {
		return provisioning.NATGatewayContext{}, false, fmt.Errorf(
			"NATGateway VirtualNetwork IPv4 CIDR: %w", err,
		)
	}

	return provisioning.NATGatewayContext{
		ExternalIP: provisioning.NATGatewayExternalIPContext{
			Name: externalIP.Name, Namespace: externalIP.Namespace,
			FulfillmentID: externalIP.Labels[osacExternalIPIDLabel],
			KubernetesUID: string(externalIP.UID), TenantID: tenantID,
			Address: externalIP.Status.Address, State: string(externalIP.Status.State),
			Phase: string(externalIP.Status.Phase),
		},
		VirtualNetwork: provisioning.NATGatewayVirtualNetworkContext{
			Name: virtualNetwork.Name, Namespace: virtualNetwork.Namespace,
			FulfillmentID: virtualNetwork.Labels[osacVirtualNetworkIDLabel],
			KubernetesUID: string(virtualNetwork.UID), TenantID: tenantID,
			IPv4CIDR: virtualNetwork.Spec.IPv4CIDR, Phase: string(virtualNetwork.Status.Phase),
		},
	}, false, nil
}

func (r *NATGatewayReconciler) currentNATGatewayContext(
	ctx context.Context,
	natgw *v1alpha1.NATGateway,
) (provisioning.NATGatewayContext, bool, error) {
	reader := r.APIReader
	if reader == nil {
		reader = r.Client
	}
	virtualNetworks := &v1alpha1.VirtualNetworkList{}
	if err := reader.List(ctx, virtualNetworks,
		client.InNamespace(natgw.Namespace),
		client.MatchingLabels{osacVirtualNetworkIDLabel: natgw.Spec.VirtualNetwork},
	); err != nil {
		return provisioning.NATGatewayContext{}, false, err
	}
	if len(virtualNetworks.Items) == 0 {
		return provisioning.NATGatewayContext{}, true, nil
	}
	if len(virtualNetworks.Items) != 1 {
		return provisioning.NATGatewayContext{}, false, fmt.Errorf("VirtualNetwork reference is ambiguous")
	}
	externalIPs := &v1alpha1.ExternalIPList{}
	if err := reader.List(ctx, externalIPs,
		client.InNamespace(natgw.Namespace),
		client.MatchingLabels{osacExternalIPIDLabel: natgw.Spec.ExternalIP},
	); err != nil {
		return provisioning.NATGatewayContext{}, false, err
	}
	if len(externalIPs.Items) == 0 {
		return provisioning.NATGatewayContext{}, true, nil
	}
	if len(externalIPs.Items) != 1 {
		return provisioning.NATGatewayContext{}, false, fmt.Errorf("ExternalIP reference is ambiguous")
	}
	return buildNATGatewayContext(natgw, &virtualNetworks.Items[0], &externalIPs.Items[0])
}

func decodeNATGatewayJobInputs(inputs map[string]any) (natGatewayJobInputs, error) {
	if len(inputs) == 0 {
		return natGatewayJobInputs{}, fmt.Errorf("NATGateway job inputs are missing")
	}
	encoded, err := json.Marshal(inputs)
	if err != nil {
		return natGatewayJobInputs{}, fmt.Errorf("NATGateway job inputs are malformed")
	}
	var submitted struct {
		JobVars *struct {
			Resource *struct {
				Metadata *struct {
					Name              string            `json:"name"`
					Namespace         string            `json:"namespace"`
					UID               string            `json:"uid"`
					Generation        *int64            `json:"generation"`
					Labels            map[string]string `json:"labels"`
					Annotations       map[string]string `json:"annotations"`
					DeletionTimestamp *metav1.Time      `json:"deletionTimestamp"`
				} `json:"metadata"`
				Spec *struct {
					VirtualNetwork string `json:"virtualNetwork"`
					ExternalIP     string `json:"externalIP"`
				} `json:"spec"`
			} `json:"resource"`
			NATGatewayContext *provisioning.NATGatewayContext `json:"nat_gateway_context"`
		} `json:"osac_job_vars"`
	}
	if err := json.Unmarshal(encoded, &submitted); err != nil {
		return natGatewayJobInputs{}, fmt.Errorf("NATGateway job inputs are malformed")
	}
	if submitted.JobVars == nil || submitted.JobVars.Resource == nil ||
		submitted.JobVars.Resource.Metadata == nil || submitted.JobVars.Resource.Spec == nil {
		return natGatewayJobInputs{}, fmt.Errorf("NATGateway job inputs omit the resource identity")
	}
	metadata := submitted.JobVars.Resource.Metadata
	spec := submitted.JobVars.Resource.Spec
	result := natGatewayJobInputs{
		name: metadata.Name, namespace: metadata.Namespace, uid: metadata.UID,
		fulfillmentID:  metadata.Labels[osacNATGatewayIDLabel],
		virtualNetwork: spec.VirtualNetwork, externalIP: spec.ExternalIP,
		tenant:          metadata.Annotations[osacTenantKey],
		strategy:        metadata.Annotations[osacImplementationStrategyAnnotation],
		contractVersion: metadata.Annotations[osacNATGatewayContractVersionAnnotation],
		deleting:        metadata.DeletionTimestamp != nil && !metadata.DeletionTimestamp.IsZero(),
		context:         submitted.JobVars.NATGatewayContext,
	}
	if metadata.Generation == nil {
		return natGatewayJobInputs{}, fmt.Errorf("NATGateway job inputs omit generation")
	}
	result.generation = *metadata.Generation
	if result.name == "" || result.namespace == "" || result.uid == "" || result.fulfillmentID == "" || result.generation < 0 ||
		result.virtualNetwork == "" || result.externalIP == "" || result.tenant == "" ||
		result.strategy == "" || result.contractVersion != networkmanager.NATGatewayContractVersionV1 {
		return natGatewayJobInputs{}, fmt.Errorf("NATGateway job inputs omit required identity")
	}
	return result, nil
}

func validateNATGatewaySubmittedIdentity(
	current *v1alpha1.NATGateway,
	expected *v1alpha1.NATGateway,
	submitted natGatewayJobInputs,
	jobType v1alpha1.JobType,
	status provisioning.ProvisionStatusWithExtraVars,
) error {
	if current.UID != expected.UID || current.Generation != expected.Generation ||
		current.Spec != expected.Spec || current.Annotations[osacTenantKey] != expected.Annotations[osacTenantKey] ||
		current.Annotations[osacNATGatewayContractVersionAnnotation] != networkmanager.NATGatewayContractVersionV1 {
		return fmt.Errorf("NATGateway changed while provider operation was running")
	}
	if submitted.name != current.Name || submitted.namespace != current.Namespace ||
		submitted.uid != string(current.UID) || submitted.fulfillmentID != current.Labels[osacNATGatewayIDLabel] ||
		submitted.generation != current.Generation ||
		submitted.virtualNetwork != current.Spec.VirtualNetwork || submitted.externalIP != current.Spec.ExternalIP ||
		submitted.tenant != current.Annotations[osacTenantKey] ||
		submitted.strategy != current.Annotations[osacImplementationStrategyAnnotation] ||
		submitted.contractVersion != networkmanager.NATGatewayContractVersionV1 {
		return fmt.Errorf("NATGateway job inputs do not match the current resource")
	}
	if jobType == v1alpha1.JobTypeProvision && (!current.DeletionTimestamp.IsZero() || submitted.deleting) {
		return fmt.Errorf("NATGateway is terminating")
	}
	if jobType == v1alpha1.JobTypeDeprovision && (current.DeletionTimestamp.IsZero() || !submitted.deleting) {
		return fmt.Errorf("NATGateway is not deleting")
	}
	if err := provisioning.ValidateProviderJobResult(
		expected.Status.ProvisioningJobs,
		jobType,
		status,
		expected.Status.DesiredConfigVersion,
	); err != nil {
		return fmt.Errorf("NATGateway provider result is invalid: %w", err)
	}
	return nil
}

func (r *NATGatewayReconciler) validateNATGatewayProvisionResult(
	ctx context.Context,
	expected *v1alpha1.NATGateway,
	expectedContext provisioning.NATGatewayContext,
	status provisioning.ProvisionStatusWithExtraVars,
) error {
	current := &v1alpha1.NATGateway{}
	reader := r.APIReader
	if reader == nil {
		reader = r.Client
	}
	if err := reader.Get(ctx, client.ObjectKeyFromObject(expected), current); err != nil {
		return fmt.Errorf("could not read NATGateway after provider operation: %w", err)
	}
	submitted, err := decodeNATGatewayJobInputs(status.InputExtraVars)
	if err != nil {
		return err
	}
	if err := validateNATGatewaySubmittedIdentity(current, expected, submitted, v1alpha1.JobTypeProvision, status); err != nil {
		return err
	}
	if submitted.context == nil || *submitted.context != expectedContext {
		return fmt.Errorf("NATGateway job context does not match the dispatched snapshot")
	}
	currentContext, pending, err := r.currentNATGatewayContext(ctx, current)
	if err != nil {
		return err
	}
	if pending {
		return fmt.Errorf("NATGateway dependencies are no longer Ready")
	}
	if currentContext != expectedContext {
		return fmt.Errorf("NATGateway dependencies changed while provider operation was running")
	}
	return provisioning.ValidateNATGatewayResult(
		status.ExtraVars, provisioning.NATGatewayEnsureOperation, submitted.uid, submitted.generation,
	)
}

func (r *NATGatewayReconciler) validateNATGatewayDeprovisionResult(
	ctx context.Context,
	expected *v1alpha1.NATGateway,
) error {
	job := provisioning.FindLatestJobByType(expected.Status.ProvisioningJobs, v1alpha1.JobTypeDeprovision)
	if job == nil {
		return fmt.Errorf("NATGateway delete job is missing")
	}
	outputProvider, ok := r.ProvisioningProvider.(provisioning.ProvisioningProviderWithDeprovisionOutputs)
	if !ok {
		return fmt.Errorf("provisioning provider does not return NATGateway delete results")
	}
	status, err := outputProvider.GetDeprovisionStatusWithExtraVars(ctx, expected, job.JobID)
	if err != nil {
		return err
	}
	current := &v1alpha1.NATGateway{}
	reader := r.APIReader
	if reader == nil {
		reader = r.Client
	}
	if err := reader.Get(ctx, client.ObjectKeyFromObject(expected), current); err != nil {
		return fmt.Errorf("could not read NATGateway after provider cleanup: %w", err)
	}
	submitted, err := decodeNATGatewayJobInputs(status.InputExtraVars)
	if err != nil {
		return err
	}
	if err := validateNATGatewaySubmittedIdentity(current, expected, submitted, v1alpha1.JobTypeDeprovision, status); err != nil {
		return err
	}
	return provisioning.ValidateNATGatewayResult(
		status.ExtraVars, provisioning.NATGatewayDeleteOperation, submitted.uid, submitted.generation,
	)
}

func failNATGatewayProvisionAttempt(natgw *v1alpha1.NATGateway, message string) {
	if job := provisioning.FindLatestJobByType(natgw.Status.ProvisioningJobs, v1alpha1.JobTypeProvision); job != nil {
		job.State = v1alpha1.JobStateFailed
		job.Message = message
	}
	setNATGatewayPhase(&natgw.Status, v1alpha1.NATGatewayPhaseFailed)
	setReadyConditionFailed(&natgw.Status.Conditions, message)
}

func failNATGatewayDeprovisionAttempt(natgw *v1alpha1.NATGateway, message string) {
	if job := provisioning.FindLatestJobByType(natgw.Status.ProvisioningJobs, v1alpha1.JobTypeDeprovision); job != nil {
		job.State = v1alpha1.JobStateFailed
		job.Message = message
		job.BlockDeletionOnFailure = true
	}
	setReadyConditionFailed(&natgw.Status.Conditions, message)
}

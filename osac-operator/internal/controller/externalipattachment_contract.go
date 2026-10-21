package controller

import (
	"context"
	"encoding/json"
	"fmt"
	"net/netip"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/utils/ptr"
	"sigs.k8s.io/controller-runtime/pkg/client"
	ctrllog "sigs.k8s.io/controller-runtime/pkg/log"

	bmfov1alpha1 "github.com/osac-project/osac/bare-metal-fulfillment-operator/api/v1alpha1"
	"github.com/osac-project/osac/osac-operator/api/v1alpha1"
	"github.com/osac-project/osac/osac-operator/pkg/dispatcher"
	"github.com/osac-project/osac/osac-operator/pkg/networkmanager"
	"github.com/osac-project/osac/osac-operator/pkg/provisioning"
)

type externalIPAttachmentJobInputs struct {
	name              string
	namespace         string
	uid               string
	generation        int64
	externalIP        string
	bareMetalInstance string
	tenant            string
	contractVersion   string
	deleting          bool
	context           *provisioning.ExternalIPAttachmentContext
}

func resolveExternalIPAttachmentManager(
	ctx context.Context,
	resolver *dispatcher.Resolver,
	networkClassID string,
	pinnedVersion string,
) (string, string, error) {
	if pinnedVersion != "" && pinnedVersion != networkmanager.ExternalIPAttachmentContractVersionV1 {
		return "", "", fmt.Errorf("unsupported ExternalIPAttachment contract version %q", pinnedVersion)
	}
	plan, err := resolveDispatchPlan(ctx, resolver, "ExternalIPAttachment", networkClassID)
	if err != nil {
		return "", "", err
	}
	if plan == nil {
		return "", "", nil
	}
	target := plan.FabricTarget()
	if target == nil {
		target = plan.K8sTarget()
	}
	if target == nil {
		return "", "", nil
	}
	version := pinnedVersion
	if version == "" {
		version = target.Manager.ExternalIPAttachmentContractVersion
	}
	if version != "" && version != networkmanager.ExternalIPAttachmentContractVersionV1 {
		return "", "", fmt.Errorf("unsupported ExternalIPAttachment contract version %q", version)
	}
	return target.Manager.Name, version, nil
}

func (r *ExternalIPAttachmentReconciler) resolveExternalIPAttachmentProvisioningContext(
	ctx context.Context,
	attachment *v1alpha1.ExternalIPAttachment,
	externalIP *v1alpha1.ExternalIP,
	ci *v1alpha1.ComputeInstance,
	co *v1alpha1.ClusterOrder,
	bmi *bmfov1alpha1.BareMetalInstance,
) (strategy, version, virtualNetworkName string,
	attachmentContext provisioning.ExternalIPAttachmentContext,
	pending bool,
	err error,
) {
	networkClassID, err := lookupDefaultNetworkClassID(ctx, r.networkClassesClient)
	if err != nil {
		return "", "", "", provisioning.ExternalIPAttachmentContext{}, false, err
	}
	strategy, version, err = resolveExternalIPAttachmentManager(
		ctx,
		r.Resolver,
		networkClassID,
		attachment.Annotations[osacExternalIPAttachmentContractVersionAnnotation],
	)
	if err != nil {
		return "", "", "", provisioning.ExternalIPAttachmentContext{}, false, err
	}
	if strategy == "" {
		setReadyConditionBlocked(&attachment.Status.Conditions, v1alpha1.ReasonNoManagerConfigured,
			conditionMessageNoManagerConfigured)
		ctrllog.FromContext(ctx).Info("implementation strategy not resolved", "externalIPAttachment", attachment.Name)
		return "", "", "", provisioning.ExternalIPAttachmentContext{}, false, fmt.Errorf(
			"cannot reconcile ExternalIPAttachment %q: no fabric manager is configured for NetworkClass %q",
			attachment.Name,
			networkClassID,
		)
	}

	if version == networkmanager.ExternalIPAttachmentContractVersionV1 {
		attachmentContext, virtualNetworkName, pending, err = r.resolveAgentlessAttachmentContext(
			ctx, attachment, externalIP, bmi,
		)
		if err != nil {
			setReadyConditionFailed(&attachment.Status.Conditions, err.Error())
		}
		return strategy, version, virtualNetworkName, attachmentContext, pending, err
	}

	if bmi != nil && bmi.PrimaryIPAddress() == "" {
		ctrllog.FromContext(ctx).Info("BareMetalInstance primary IP not yet discovered, requeueing",
			"baremetalInstance", bmi.Name)
		return strategy, version, "", provisioning.ExternalIPAttachmentContext{}, true, nil
	}
	virtualNetworkName, result, err := r.resolveTargetVirtualNetworkName(ctx, ci, bmi)
	if err != nil || result.RequeueAfter > 0 {
		return strategy, version, virtualNetworkName, provisioning.ExternalIPAttachmentContext{},
			result.RequeueAfter > 0, err
	}
	return strategy, version, virtualNetworkName, provisioning.ExternalIPAttachmentContext{}, false, nil
}

func (r *ExternalIPAttachmentReconciler) resolveAgentlessAttachmentContext(
	ctx context.Context,
	attachment *v1alpha1.ExternalIPAttachment,
	externalIP *v1alpha1.ExternalIP,
	bmi *bmfov1alpha1.BareMetalInstance,
) (provisioning.ExternalIPAttachmentContext, string, bool, error) {
	if attachment.Spec.BaremetalInstance == nil || bmi == nil {
		return provisioning.ExternalIPAttachmentContext{}, "", false,
			fmt.Errorf("AgentlessNet ExternalIPAttachment v1 currently supports BareMetalInstance targets")
	}
	if attachment.UID == "" || externalIP.UID == "" || bmi.UID == "" {
		return provisioning.ExternalIPAttachmentContext{}, "", false, fmt.Errorf("attachment, ExternalIP, and BareMetalInstance UIDs are required")
	}
	attachmentTenant := attachment.Annotations[osacTenantKey]
	if attachmentTenant == "" || externalIP.Annotations[osacTenantKey] != attachmentTenant ||
		bmi.Annotations[osacTenantKey] != attachmentTenant {
		return provisioning.ExternalIPAttachmentContext{}, "", false, fmt.Errorf("ExternalIPAttachment target tenant ownership does not match")
	}
	if externalIP.Namespace != attachment.Namespace ||
		externalIP.Labels[osacExternalIPIDLabel] != attachment.Spec.ExternalIP ||
		bmi.Labels[osacBareMetalInstanceIDLabel] != *attachment.Spec.BaremetalInstance {
		return provisioning.ExternalIPAttachmentContext{}, "", false, fmt.Errorf("ExternalIPAttachment dependency identity does not match its references")
	}
	if !externalIP.DeletionTimestamp.IsZero() || !bmi.DeletionTimestamp.IsZero() {
		return provisioning.ExternalIPAttachmentContext{}, "", true, nil
	}
	if externalIP.Status.State != v1alpha1.ExternalIPStateAllocated ||
		externalIP.Status.Phase != v1alpha1.ExternalIPPhaseReady || externalIP.Status.Address == "" {
		return provisioning.ExternalIPAttachmentContext{}, "", true, nil
	}
	if bmi.Status.Phase != bmfov1alpha1.BareMetalInstancePhaseReady {
		return provisioning.ExternalIPAttachmentContext{}, "", true, nil
	}
	if _, err := canonicalIPv4Address(externalIP.Status.Address); err != nil {
		return provisioning.ExternalIPAttachmentContext{}, "", false, fmt.Errorf("ExternalIP address is invalid: %w", err)
	}
	targetIP := bmi.PrimaryIPAddress()
	subnetID := primaryBMISubnetRef(bmi)
	if targetIP == "" || subnetID == "" {
		return provisioning.ExternalIPAttachmentContext{}, "", true, nil
	}
	parsedTargetIP, err := canonicalIPv4Address(targetIP)
	if err != nil {
		return provisioning.ExternalIPAttachmentContext{}, "", false, fmt.Errorf("BareMetalInstance primary address is invalid: %w", err)
	}
	subnet, virtualNetwork, pending, err := r.resolveAgentlessAttachmentNetwork(
		ctx, subnetID, attachmentTenant, parsedTargetIP,
	)
	if err != nil {
		return provisioning.ExternalIPAttachmentContext{}, "", false, err
	}
	if pending {
		return provisioning.ExternalIPAttachmentContext{}, "", true, nil
	}
	if externalIP.Namespace != subnet.Namespace || subnet.Namespace != virtualNetwork.Namespace {
		return provisioning.ExternalIPAttachmentContext{}, "", false, fmt.Errorf("ExternalIPAttachment networking resources must share a namespace")
	}

	value := provisioning.ExternalIPAttachmentContext{
		ExternalIP: provisioning.ExternalIPAttachmentExternalIPContext{
			Name: externalIP.Name, Namespace: externalIP.Namespace,
			FulfillmentID: externalIP.Labels[osacExternalIPIDLabel], KubernetesUID: string(externalIP.UID),
			TenantID: externalIP.Annotations[osacTenantKey], Address: externalIP.Status.Address,
			State: string(externalIP.Status.State), Phase: string(externalIP.Status.Phase),
		},
		Target: provisioning.ExternalIPAttachmentTargetContext{
			Kind: "BareMetalInstance", Name: bmi.Name, Namespace: bmi.Namespace,
			FulfillmentID: bmi.Labels[osacBareMetalInstanceIDLabel], KubernetesUID: string(bmi.UID),
			TenantID: bmi.Annotations[osacTenantKey], IPAddress: targetIP, Ready: true,
		},
		Subnet: provisioning.ExternalIPAttachmentSubnetContext{
			Name: subnet.Name, Namespace: subnet.Namespace,
			FulfillmentID: subnet.Labels[osacSubnetIDLabel], KubernetesUID: string(subnet.UID),
			TenantID: subnet.Annotations[osacTenantKey], IPv4CIDR: subnet.Spec.IPv4CIDR,
			VirtualNetworkID: subnet.Spec.VirtualNetwork, Phase: string(subnet.Status.Phase),
		},
		VirtualNetwork: provisioning.ExternalIPAttachmentVirtualNetworkContext{
			Name: virtualNetwork.Name, Namespace: virtualNetwork.Namespace,
			FulfillmentID: virtualNetwork.Labels[osacVirtualNetworkIDLabel],
			KubernetesUID: string(virtualNetwork.UID), TenantID: virtualNetwork.Annotations[osacTenantKey],
			Phase: string(virtualNetwork.Status.Phase),
		},
	}
	for label, value := range map[string]string{
		"attachment tenant":             attachmentTenant,
		"ExternalIP Fulfillment ID":     value.ExternalIP.FulfillmentID,
		"BMI Fulfillment ID":            value.Target.FulfillmentID,
		"Subnet Fulfillment ID":         value.Subnet.FulfillmentID,
		"VirtualNetwork Fulfillment ID": value.VirtualNetwork.FulfillmentID,
	} {
		if value == "" {
			return provisioning.ExternalIPAttachmentContext{}, "", false, fmt.Errorf("%s is required", label)
		}
	}
	return value, virtualNetwork.Name, false, nil
}

func (r *ExternalIPAttachmentReconciler) resolveAgentlessAttachmentNetwork(
	ctx context.Context,
	subnetID string,
	tenantID string,
	targetIP netip.Addr,
) (*v1alpha1.Subnet, *v1alpha1.VirtualNetwork, bool, error) {

	reader := r.APIReader
	if reader == nil {
		reader = r.Client
	}
	subnets := &v1alpha1.SubnetList{}
	if err := reader.List(ctx, subnets,
		client.InNamespace(r.NetworkingNamespace),
		client.MatchingLabels{osacSubnetIDLabel: subnetID},
	); err != nil {
		return nil, nil, false, err
	}
	if len(subnets.Items) == 0 {
		return nil, nil, true, nil
	}
	if len(subnets.Items) != 1 {
		return nil, nil, false, fmt.Errorf("BareMetalInstance primary Subnet reference is ambiguous")
	}
	subnet := &subnets.Items[0]
	if subnet.UID == "" || subnet.Labels[osacSubnetIDLabel] != subnetID ||
		subnet.Annotations[osacTenantKey] != tenantID {
		return nil, nil, false, fmt.Errorf("BareMetalInstance primary Subnet identity or tenant does not match")
	}
	if !subnet.DeletionTimestamp.IsZero() || subnet.Status.Phase != v1alpha1.SubnetPhaseReady {
		return nil, nil, true, nil
	}
	prefix, err := netip.ParsePrefix(subnet.Spec.IPv4CIDR)
	if err != nil || !prefix.Addr().Is4() || prefix != prefix.Masked() || prefix.String() != subnet.Spec.IPv4CIDR {
		return nil, nil, false, fmt.Errorf("primary Subnet CIDR is not canonical IPv4")
	}
	if !prefix.Contains(targetIP) {
		return nil, nil, false, fmt.Errorf("BareMetalInstance primary address is outside its Subnet")
	}
	if subnet.Spec.VirtualNetwork == "" {
		return nil, nil, false, fmt.Errorf("primary Subnet has no VirtualNetwork reference")
	}

	virtualNetworks := &v1alpha1.VirtualNetworkList{}
	if err := reader.List(ctx, virtualNetworks,
		client.InNamespace(r.NetworkingNamespace),
		client.MatchingLabels{osacVirtualNetworkIDLabel: subnet.Spec.VirtualNetwork},
	); err != nil {
		return nil, nil, false, err
	}
	if len(virtualNetworks.Items) == 0 {
		return nil, nil, true, nil
	}
	if len(virtualNetworks.Items) != 1 {
		return nil, nil, false, fmt.Errorf("primary Subnet VirtualNetwork reference is ambiguous")
	}
	virtualNetwork := &virtualNetworks.Items[0]
	if virtualNetwork.UID == "" || virtualNetwork.Labels[osacVirtualNetworkIDLabel] != subnet.Spec.VirtualNetwork ||
		virtualNetwork.Annotations[osacTenantKey] != tenantID {
		return nil, nil, false, fmt.Errorf("primary VirtualNetwork identity or tenant does not match")
	}
	if !virtualNetwork.DeletionTimestamp.IsZero() || virtualNetwork.Status.Phase != v1alpha1.VirtualNetworkPhaseReady {
		return nil, nil, true, nil
	}
	return subnet, virtualNetwork, false, nil
}

func canonicalIPv4Address(value string) (netip.Addr, error) {
	address, err := netip.ParseAddr(value)
	if err != nil || !address.Is4() || address.String() != value || address.IsUnspecified() {
		return netip.Addr{}, fmt.Errorf("address must be canonical IPv4")
	}
	return address, nil
}

func externalIPAttachmentDesiredConfigVersion(
	spec v1alpha1.ExternalIPAttachmentSpec,
	implementationStrategy string,
	contractVersion string,
	attachmentContext provisioning.ExternalIPAttachmentContext,
	legacyTargetIP string,
) (string, error) {
	if contractVersion == networkmanager.ExternalIPAttachmentContractVersionV1 {
		return provisioning.ComputeDesiredConfigVersion(struct {
			Spec                   v1alpha1.ExternalIPAttachmentSpec
			ImplementationStrategy string
			ContractVersion        string
			Context                provisioning.ExternalIPAttachmentContext
		}{spec, implementationStrategy, contractVersion, attachmentContext})
	}
	return provisioning.ComputeDesiredConfigVersion(struct {
		Spec                   v1alpha1.ExternalIPAttachmentSpec
		ImplementationStrategy string
		TargetIP               string
	}{spec, implementationStrategy, legacyTargetIP})
}

func (r *ExternalIPAttachmentReconciler) currentAgentlessAttachmentContext(
	ctx context.Context,
	attachment *v1alpha1.ExternalIPAttachment,
) (provisioning.ExternalIPAttachmentContext, error) {
	if attachment.Spec.BaremetalInstance == nil {
		return provisioning.ExternalIPAttachmentContext{}, fmt.Errorf("attachment does not target a BareMetalInstance")
	}
	reader := r.APIReader
	if reader == nil {
		reader = r.Client
	}
	externalIPs := &v1alpha1.ExternalIPList{}
	if err := reader.List(ctx, externalIPs, client.InNamespace(attachment.Namespace),
		client.MatchingLabels{osacExternalIPIDLabel: attachment.Spec.ExternalIP}); err != nil {
		return provisioning.ExternalIPAttachmentContext{}, err
	}
	if len(externalIPs.Items) != 1 {
		return provisioning.ExternalIPAttachmentContext{}, fmt.Errorf("ExternalIP is missing or ambiguous")
	}
	bareMetalInstances := &bmfov1alpha1.BareMetalInstanceList{}
	if err := reader.List(ctx, bareMetalInstances,
		client.InNamespace(r.BaremetalInstanceNamespace),
		client.MatchingLabels{osacBareMetalInstanceIDLabel: *attachment.Spec.BaremetalInstance},
	); err != nil {
		return provisioning.ExternalIPAttachmentContext{}, err
	}
	if len(bareMetalInstances.Items) != 1 {
		return provisioning.ExternalIPAttachmentContext{}, fmt.Errorf("BareMetalInstance is missing or ambiguous")
	}
	value, _, pending, err := r.resolveAgentlessAttachmentContext(ctx, attachment, &externalIPs.Items[0], &bareMetalInstances.Items[0])
	if err != nil {
		return provisioning.ExternalIPAttachmentContext{}, err
	}
	if pending {
		return provisioning.ExternalIPAttachmentContext{}, fmt.Errorf("ExternalIPAttachment dependencies are no longer Ready")
	}
	return value, nil
}

func decodeExternalIPAttachmentJobInputs(inputs map[string]any) (externalIPAttachmentJobInputs, error) {
	if len(inputs) == 0 {
		return externalIPAttachmentJobInputs{}, fmt.Errorf("ExternalIPAttachment job inputs are missing")
	}
	encoded, err := json.Marshal(inputs)
	if err != nil {
		return externalIPAttachmentJobInputs{}, fmt.Errorf("ExternalIPAttachment job inputs are malformed")
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
					ExternalIP        string  `json:"externalIP"`
					BaremetalInstance *string `json:"baremetalInstance"`
				} `json:"spec"`
			} `json:"resource"`
			Context *provisioning.ExternalIPAttachmentContext `json:"external_ip_attachment_context"`
		} `json:"osac_job_vars"`
	}
	if err := json.Unmarshal(encoded, &submitted); err != nil {
		return externalIPAttachmentJobInputs{}, fmt.Errorf("ExternalIPAttachment job inputs are malformed")
	}
	if submitted.JobVars == nil || submitted.JobVars.Resource == nil ||
		submitted.JobVars.Resource.Metadata == nil || submitted.JobVars.Resource.Spec == nil {
		return externalIPAttachmentJobInputs{}, fmt.Errorf("ExternalIPAttachment job inputs omit the resource identity")
	}
	metadata := submitted.JobVars.Resource.Metadata
	spec := submitted.JobVars.Resource.Spec
	if metadata.Generation == nil {
		return externalIPAttachmentJobInputs{}, fmt.Errorf("ExternalIPAttachment job inputs omit generation")
	}
	decoded := externalIPAttachmentJobInputs{
		name: metadata.Name, namespace: metadata.Namespace, uid: metadata.UID,
		generation: *metadata.Generation, externalIP: spec.ExternalIP,
		tenant:          metadata.Annotations[osacTenantKey],
		contractVersion: metadata.Annotations[osacExternalIPAttachmentContractVersionAnnotation],
		deleting:        metadata.DeletionTimestamp != nil && !metadata.DeletionTimestamp.IsZero(),
		context:         submitted.JobVars.Context,
	}
	if spec.BaremetalInstance != nil {
		decoded.bareMetalInstance = *spec.BaremetalInstance
	}
	if decoded.name == "" || decoded.namespace == "" || decoded.uid == "" || decoded.generation < 0 ||
		decoded.externalIP == "" || decoded.tenant == "" || decoded.contractVersion != networkmanager.ExternalIPAttachmentContractVersionV1 {
		return externalIPAttachmentJobInputs{}, fmt.Errorf("ExternalIPAttachment job inputs omit required identity")
	}
	return decoded, nil
}

func validateSubmittedAttachmentIdentity(
	latest *v1alpha1.ExternalIPAttachment,
	submitted externalIPAttachmentJobInputs,
	jobType v1alpha1.JobType,
	status provisioning.ProvisionStatusWithExtraVars,
) error {
	if submitted.name != latest.Name || submitted.namespace != latest.Namespace ||
		submitted.uid != string(latest.UID) || submitted.generation != latest.Generation ||
		submitted.externalIP != latest.Spec.ExternalIP || submitted.tenant != latest.Annotations[osacTenantKey] ||
		submitted.contractVersion != networkmanager.ExternalIPAttachmentContractVersionV1 ||
		submitted.bareMetalInstance != ptr.Deref(latest.Spec.BaremetalInstance, "") {
		return fmt.Errorf("ExternalIPAttachment job inputs do not match the current resource")
	}
	if jobType == v1alpha1.JobTypeProvision && (!latest.DeletionTimestamp.IsZero() || submitted.deleting) {
		return fmt.Errorf("ExternalIPAttachment is terminating")
	}
	if jobType == v1alpha1.JobTypeDeprovision && (latest.DeletionTimestamp.IsZero() || !submitted.deleting) {
		return fmt.Errorf("ExternalIPAttachment is not deleting")
	}
	job := provisioning.FindLatestJobByType(latest.Status.ProvisioningJobs, jobType)
	if job == nil || job.JobID != status.JobID {
		return fmt.Errorf("ExternalIPAttachment result does not match the latest job")
	}
	if status.State != v1alpha1.JobStateSucceeded {
		return fmt.Errorf("ExternalIPAttachment provider job did not succeed")
	}
	if jobType == v1alpha1.JobTypeProvision &&
		(job.ConfigVersion == "" || job.ConfigVersion != latest.Status.DesiredConfigVersion) {
		return fmt.Errorf("ExternalIPAttachment job does not match the current configuration")
	}
	return nil
}

func (r *ExternalIPAttachmentReconciler) validateAttachmentProvisionResult(
	ctx context.Context,
	expected *v1alpha1.ExternalIPAttachment,
	expectedContext provisioning.ExternalIPAttachmentContext,
	status provisioning.ProvisionStatusWithExtraVars,
) error {
	latest := &v1alpha1.ExternalIPAttachment{}
	reader := r.APIReader
	if reader == nil {
		reader = r.Client
	}
	if err := reader.Get(ctx, client.ObjectKeyFromObject(expected), latest); err != nil {
		return fmt.Errorf("could not read ExternalIPAttachment after provider operation: %w", err)
	}
	if latest.UID != expected.UID || latest.Generation != expected.Generation {
		return fmt.Errorf("ExternalIPAttachment changed while provider operation was running")
	}
	submitted, err := decodeExternalIPAttachmentJobInputs(status.InputExtraVars)
	if err != nil {
		return err
	}
	if err := validateSubmittedAttachmentIdentity(latest, submitted, v1alpha1.JobTypeProvision, status); err != nil {
		return err
	}
	if submitted.context == nil || *submitted.context != expectedContext {
		return fmt.Errorf("ExternalIPAttachment job context does not match the dispatched snapshot")
	}
	currentContext, err := r.currentAgentlessAttachmentContext(ctx, latest)
	if err != nil {
		return err
	}
	if currentContext != expectedContext {
		return fmt.Errorf("ExternalIPAttachment dependencies changed while provider operation was running")
	}
	return provisioning.ValidateExternalIPAttachmentResult(
		status.ExtraVars,
		provisioning.ExternalIPAttachmentAttachOperation,
		submitted.uid,
		submitted.generation,
	)
}

func (r *ExternalIPAttachmentReconciler) acceptAttachmentDeprovisionResult(
	ctx context.Context,
	expected *v1alpha1.ExternalIPAttachment,
) error {
	job := provisioning.FindLatestJobByType(expected.Status.ProvisioningJobs, v1alpha1.JobTypeDeprovision)
	if job == nil {
		return fmt.Errorf("ExternalIPAttachment detach job is missing")
	}
	outputProvider, ok := r.ProvisioningProvider.(provisioning.ProvisioningProviderWithDeprovisionOutputs)
	if !ok {
		return fmt.Errorf("provisioning provider does not return attachment detach results")
	}
	status, err := outputProvider.GetDeprovisionStatusWithExtraVars(ctx, expected, job.JobID)
	if err != nil {
		return err
	}
	reader := r.APIReader
	if reader == nil {
		reader = r.Client
	}
	latest := &v1alpha1.ExternalIPAttachment{}
	if err := reader.Get(ctx, client.ObjectKeyFromObject(expected), latest); err != nil {
		return fmt.Errorf("could not read ExternalIPAttachment after detach: %w", err)
	}
	if latest.UID != expected.UID || latest.Generation != expected.Generation || latest.DeletionTimestamp.IsZero() {
		return fmt.Errorf("ExternalIPAttachment changed while provider cleanup was running")
	}
	submitted, err := decodeExternalIPAttachmentJobInputs(status.InputExtraVars)
	if err != nil {
		return err
	}
	if err := validateSubmittedAttachmentIdentity(latest, submitted, v1alpha1.JobTypeDeprovision, status); err != nil {
		return err
	}
	return provisioning.ValidateExternalIPAttachmentResult(
		status.ExtraVars,
		provisioning.ExternalIPAttachmentDetachOperation,
		submitted.uid,
		submitted.generation,
	)
}

func markAttachmentDeprovisionJobFailed(attachment *v1alpha1.ExternalIPAttachment, message string) {
	job := provisioning.FindLatestJobByType(attachment.Status.ProvisioningJobs, v1alpha1.JobTypeDeprovision)
	if job == nil {
		return
	}
	job.State = v1alpha1.JobStateFailed
	job.Message = message
	job.BlockDeletionOnFailure = true
}

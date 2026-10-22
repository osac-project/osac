package controller

import (
	"context"
	"encoding/json"
	"fmt"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/osac-project/osac/osac-operator/api/v1alpha1"
	"github.com/osac-project/osac/osac-operator/pkg/networkmanager"
	"github.com/osac-project/osac/osac-operator/pkg/provisioning"
)

func (r *ExternalIPReconciler) resolveExternalIPManager(
	ctx context.Context,
	externalIP *v1alpha1.ExternalIP,
) (string, string, error) {
	contractVersion := externalIP.Annotations[osacExternalIPContractVersionAnnotation]
	if contractVersion != "" && contractVersion != networkmanager.ExternalIPContractVersionV1 {
		return "", "", fmt.Errorf("unsupported ExternalIP contract version %q", contractVersion)
	}

	networkClassID, err := lookupDefaultNetworkClassID(ctx, r.networkClassesClient)
	if err != nil {
		return "", "", err
	}
	plan, err := resolveDispatchPlan(ctx, r.Resolver, "ExternalIP", networkClassID)
	if err != nil {
		return "", "", err
	}
	strategy := ""
	if plan != nil {
		target := plan.FabricTarget()
		if target == nil {
			target = plan.K8sTarget()
		}
		if target != nil {
			strategy = target.Manager.Name
			if contractVersion == "" {
				contractVersion = target.Manager.ExternalIPContractVersion
			}
		}
	}
	if contractVersion != "" && contractVersion != networkmanager.ExternalIPContractVersionV1 {
		return "", "", fmt.Errorf("unsupported ExternalIP contract version %q", contractVersion)
	}
	return strategy, contractVersion, nil
}

func externalIPPoolContext(
	externalIP *v1alpha1.ExternalIP,
	pool *v1alpha1.ExternalIPPool,
) (provisioning.ExternalIPPoolContext, error) {
	fulfillmentID := pool.Labels[osacExternalIPPoolIDLabel]
	tenantID := pool.Annotations[osacTenantKey]
	if pool.UID == "" || fulfillmentID == "" || tenantID == "" {
		return provisioning.ExternalIPPoolContext{}, fmt.Errorf("ExternalIPPool identity and tenant are required")
	}
	if pool.Spec.IPFamily != "IPv4" {
		return provisioning.ExternalIPPoolContext{}, fmt.Errorf("ExternalIPPool must use IPv4")
	}
	cidr, err := provisioning.ValidateExternalIPPoolCIDR(pool.Spec.CIDRs)
	if err != nil {
		return provisioning.ExternalIPPoolContext{}, err
	}
	externalIPTenant := externalIP.Annotations[osacTenantKey]
	if externalIPTenant == "" || (tenantID != externalIPTenant && tenantID != "shared") {
		return provisioning.ExternalIPPoolContext{}, fmt.Errorf("ExternalIP tenant does not match its pool")
	}
	return provisioning.ExternalIPPoolContext{
		FulfillmentID: fulfillmentID,
		KubernetesUID: string(pool.UID),
		TenantID:      tenantID,
		CIDR:          cidr,
		Phase:         string(pool.Status.Phase),
	}, nil
}

type externalIPJobInputs struct {
	resourceName       string
	resourceNamespace  string
	resourceUID        string
	resourceGeneration int64
	externalIPID       string
	resourceTenant     string
	contractVersion    string
	resourceDeleting   bool
	poolReference      string
	pool               provisioning.ExternalIPPoolContext
}

// decodeExternalIPJobInputs reads the actual AAP job inputs. The Ansible playbooks expose
// osac_job_vars.resource as their local external_ip variable, so the submitted object stays
// under osac_job_vars.resource in AAP's extra_vars response.
func decodeExternalIPJobInputs(inputs map[string]any) (externalIPJobInputs, error) {
	if len(inputs) == 0 {
		return externalIPJobInputs{}, fmt.Errorf("ExternalIP job inputs are missing")
	}

	encoded, err := json.Marshal(inputs)
	if err != nil {
		return externalIPJobInputs{}, fmt.Errorf("ExternalIP job inputs are malformed")
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
					Pool string `json:"pool"`
				} `json:"spec"`
			} `json:"resource"`
			ExternalIPPool *struct {
				FulfillmentID string `json:"fulfillment_id"`
				KubernetesUID string `json:"kubernetes_uid"`
				TenantID      string `json:"tenant_id"`
				CIDR          string `json:"cidr"`
				Phase         string `json:"phase"`
			} `json:"external_ip_pool"`
		} `json:"osac_job_vars"`
	}
	if err := json.Unmarshal(encoded, &submitted); err != nil {
		return externalIPJobInputs{}, fmt.Errorf("ExternalIP job inputs are malformed")
	}
	if submitted.JobVars == nil || submitted.JobVars.Resource == nil ||
		submitted.JobVars.Resource.Metadata == nil || submitted.JobVars.Resource.Spec == nil ||
		submitted.JobVars.ExternalIPPool == nil {
		return externalIPJobInputs{}, fmt.Errorf("ExternalIP job inputs are missing the resource or pool snapshot")
	}

	metadata := submitted.JobVars.Resource.Metadata
	pool := submitted.JobVars.ExternalIPPool
	resource := externalIPJobInputs{
		resourceName:      metadata.Name,
		resourceNamespace: metadata.Namespace,
		resourceUID:       metadata.UID,
		externalIPID:      metadata.Labels[osacExternalIPIDLabel],
		resourceTenant:    metadata.Annotations[osacTenantKey],
		contractVersion:   metadata.Annotations[osacExternalIPContractVersionAnnotation],
		resourceDeleting:  metadata.DeletionTimestamp != nil && !metadata.DeletionTimestamp.IsZero(),
		poolReference:     submitted.JobVars.Resource.Spec.Pool,
		pool: provisioning.ExternalIPPoolContext{
			FulfillmentID: pool.FulfillmentID,
			KubernetesUID: pool.KubernetesUID,
			TenantID:      pool.TenantID,
			CIDR:          pool.CIDR,
			Phase:         pool.Phase,
		},
	}
	if metadata.Generation == nil {
		return externalIPJobInputs{}, fmt.Errorf("ExternalIP job inputs omit the resource generation")
	}
	resource.resourceGeneration = *metadata.Generation
	if resource.resourceName == "" || resource.resourceNamespace == "" || resource.resourceUID == "" ||
		resource.resourceGeneration < 0 || resource.externalIPID == "" || resource.resourceTenant == "" ||
		resource.contractVersion == "" || resource.poolReference == "" || resource.pool.FulfillmentID == "" ||
		resource.pool.KubernetesUID == "" || resource.pool.TenantID == "" || resource.pool.CIDR == "" ||
		resource.pool.Phase == "" {
		return externalIPJobInputs{}, fmt.Errorf("ExternalIP job inputs omit required resource or pool identity")
	}
	return resource, nil
}

func (r *ExternalIPReconciler) validatedExternalIPJobResult(
	ctx context.Context,
	expected *v1alpha1.ExternalIP,
	status provisioning.ProvisionStatusWithExtraVars,
	jobType v1alpha1.JobType,
	operation string,
) (*v1alpha1.ExternalIP, provisioning.ExternalIPPoolContext, string, error) {
	latest, submitted, err := r.currentExternalIPJobInputs(ctx, expected, status, jobType, operation)
	if err != nil {
		return nil, provisioning.ExternalIPPoolContext{}, "", err
	}
	address, err := provisioning.ValidateExternalIPResult(
		status.ExtraVars, operation, submitted.resourceUID, submitted.resourceGeneration,
	)
	if err != nil {
		return nil, provisioning.ExternalIPPoolContext{}, "", err
	}
	currentPool, err := r.currentExternalIPResultPool(ctx, latest, submitted, jobType)
	if err != nil {
		return nil, provisioning.ExternalIPPoolContext{}, "", err
	}
	return latest, currentPool, address, nil
}

func (r *ExternalIPReconciler) currentExternalIPJobInputs(
	ctx context.Context,
	expected *v1alpha1.ExternalIP,
	status provisioning.ProvisionStatusWithExtraVars,
	jobType v1alpha1.JobType,
	operation string,
) (*v1alpha1.ExternalIP, externalIPJobInputs, error) {
	latest := &v1alpha1.ExternalIP{}
	if err := r.APIReader.Get(ctx, client.ObjectKeyFromObject(expected), latest); err != nil {
		return nil, externalIPJobInputs{}, fmt.Errorf("could not read ExternalIP after provider operation: %w", err)
	}
	if err := validateCurrentExternalIPJob(latest, expected, status, jobType, operation); err != nil {
		return nil, externalIPJobInputs{}, err
	}
	submitted, err := decodeExternalIPJobInputs(status.InputExtraVars)
	if err != nil {
		return nil, externalIPJobInputs{}, err
	}
	if err := validateSubmittedExternalIP(latest, submitted); err != nil {
		return nil, externalIPJobInputs{}, err
	}
	return latest, submitted, nil
}

func validateCurrentExternalIPJob(
	latest *v1alpha1.ExternalIP,
	expected *v1alpha1.ExternalIP,
	status provisioning.ProvisionStatusWithExtraVars,
	jobType v1alpha1.JobType,
	operation string,
) error {
	if latest.UID != expected.UID || latest.Generation != expected.Generation {
		return fmt.Errorf("ExternalIP identity changed while the provider operation was running")
	}
	if latest.Annotations[osacExternalIPContractVersionAnnotation] != networkmanager.ExternalIPContractVersionV1 {
		return fmt.Errorf("ExternalIP v1 contract changed while the provider operation was running")
	}
	if jobType == v1alpha1.JobTypeProvision && !latest.DeletionTimestamp.IsZero() {
		return fmt.Errorf("ExternalIP is terminating")
	}
	if jobType == v1alpha1.JobTypeDeprovision && latest.DeletionTimestamp.IsZero() {
		return fmt.Errorf("ExternalIP is not terminating")
	}
	job := provisioning.FindLatestJobByType(latest.Status.ProvisioningJobs, jobType)
	if job == nil || job.JobID != status.JobID {
		return fmt.Errorf("ExternalIP provider result does not match the current job")
	}
	if jobType == v1alpha1.JobTypeProvision &&
		(job.ConfigVersion == "" || job.ConfigVersion != latest.Status.DesiredConfigVersion ||
			job.ConfigVersion != expected.Status.DesiredConfigVersion) {
		return fmt.Errorf("ExternalIP allocation job does not match the current configuration")
	}
	if status.State != v1alpha1.JobStateSucceeded {
		return fmt.Errorf("ExternalIP provider job did not succeed")
	}
	if (jobType == v1alpha1.JobTypeProvision && operation != "external_ip.allocate") ||
		(jobType == v1alpha1.JobTypeDeprovision && operation != "external_ip.release") {
		return fmt.Errorf("ExternalIP operation does not match the current job type")
	}
	return nil
}

func validateSubmittedExternalIP(latest *v1alpha1.ExternalIP, submitted externalIPJobInputs) error {
	if submitted.resourceName != latest.Name || submitted.resourceNamespace != latest.Namespace ||
		submitted.resourceUID != string(latest.UID) || submitted.resourceGeneration != latest.Generation ||
		submitted.externalIPID != latest.Labels[osacExternalIPIDLabel] ||
		submitted.resourceTenant != latest.Annotations[osacTenantKey] ||
		submitted.contractVersion != networkmanager.ExternalIPContractVersionV1 ||
		submitted.poolReference != latest.Spec.Pool || submitted.pool.FulfillmentID != latest.Spec.Pool ||
		submitted.resourceDeleting != !latest.DeletionTimestamp.IsZero() {
		return fmt.Errorf("ExternalIP job inputs do not match the current resource")
	}
	return nil
}

func (r *ExternalIPReconciler) currentExternalIPResultPool(
	ctx context.Context,
	latest *v1alpha1.ExternalIP,
	submitted externalIPJobInputs,
	jobType v1alpha1.JobType,
) (provisioning.ExternalIPPoolContext, error) {
	poolList := &v1alpha1.ExternalIPPoolList{}
	if err := r.APIReader.List(ctx, poolList,
		client.InNamespace(latest.Namespace),
		client.MatchingLabels{osacExternalIPPoolIDLabel: latest.Spec.Pool},
	); err != nil {
		return provisioning.ExternalIPPoolContext{}, fmt.Errorf("could not read ExternalIPPool after provider operation: %w", err)
	}
	if len(poolList.Items) != 1 {
		return provisioning.ExternalIPPoolContext{}, fmt.Errorf("ExternalIP result pool is missing or ambiguous")
	}
	currentPool, err := externalIPPoolContext(latest, &poolList.Items[0])
	if err != nil {
		return provisioning.ExternalIPPoolContext{}, err
	}
	if currentPool.FulfillmentID != submitted.pool.FulfillmentID ||
		currentPool.KubernetesUID != submitted.pool.KubernetesUID ||
		currentPool.TenantID != submitted.pool.TenantID || currentPool.CIDR != submitted.pool.CIDR {
		return provisioning.ExternalIPPoolContext{}, fmt.Errorf("ExternalIP job pool snapshot does not match the current pool")
	}
	if jobType == v1alpha1.JobTypeProvision &&
		(currentPool.Phase != submitted.pool.Phase || currentPool.Phase != string(v1alpha1.ExternalIPPoolPhaseReady) ||
			!poolList.Items[0].DeletionTimestamp.IsZero()) {
		return provisioning.ExternalIPPoolContext{}, fmt.Errorf("ExternalIP allocation pool changed or is not Ready")
	}
	return currentPool, nil
}

func (r *ExternalIPReconciler) acceptExternalIPAllocation(
	ctx context.Context,
	externalIP *v1alpha1.ExternalIP,
	poolContext provisioning.ExternalIPPoolContext,
	status provisioning.ProvisionStatusWithExtraVars,
) error {
	latest, currentPool, resultAddress, err := r.validatedExternalIPJobResult(
		ctx, externalIP, status, v1alpha1.JobTypeProvision, "external_ip.allocate",
	)
	if err != nil {
		return err
	}
	if poolContext != currentPool {
		return fmt.Errorf("ExternalIP allocation pool changed after dispatch")
	}
	address, err := provisioning.ValidateExternalIPAddress(resultAddress, currentPool.CIDR)
	if err != nil {
		return err
	}
	if latest.Status.State == v1alpha1.ExternalIPStateAllocated &&
		latest.Status.Address != "" && latest.Status.Address != externalIPNoProvisioningAddress &&
		latest.Status.Address != address {
		return fmt.Errorf("ExternalIP address changed for an existing UID reservation")
	}
	externalIP.Status.Address = address
	externalIP.Status.Phase = v1alpha1.ExternalIPPhaseReady
	setExternalIPState(&externalIP.Status, v1alpha1.ExternalIPStateAllocated)
	setReadyConditionTrue(&externalIP.Status.Conditions)
	return nil
}

func (r *ExternalIPReconciler) validateExternalIPReleaseResult(
	ctx context.Context,
	externalIP *v1alpha1.ExternalIP,
	status provisioning.ProvisionStatusWithExtraVars,
) error {
	_, _, _, err := r.validatedExternalIPJobResult(
		ctx, externalIP, status, v1alpha1.JobTypeDeprovision, "external_ip.release",
	)
	return err
}

func markExternalIPJobFailed(
	externalIP *v1alpha1.ExternalIP,
	jobType v1alpha1.JobType,
	message string,
) {
	job := provisioning.FindLatestJobByType(externalIP.Status.ProvisioningJobs, jobType)
	if job == nil {
		return
	}
	job.State = v1alpha1.JobStateFailed
	job.Message = message
	if jobType == v1alpha1.JobTypeDeprovision {
		job.BlockDeletionOnFailure = true
	}
}

func externalIPContractVersion(externalIP *v1alpha1.ExternalIP) string {
	return externalIP.Annotations[osacExternalIPContractVersionAnnotation]
}

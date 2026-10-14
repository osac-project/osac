package controller

import (
	"context"
	"fmt"

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

func (r *ExternalIPReconciler) validatedExternalIPJobResult(
	ctx context.Context,
	expected *v1alpha1.ExternalIP,
	status provisioning.ProvisionStatusWithExtraVars,
	jobType v1alpha1.JobType,
	operation string,
) (*v1alpha1.ExternalIP, string, error) {
	latest := &v1alpha1.ExternalIP{}
	if err := r.APIReader.Get(ctx, client.ObjectKeyFromObject(expected), latest); err != nil {
		return nil, "", fmt.Errorf("could not read ExternalIP after provider operation: %w", err)
	}
	if latest.UID != expected.UID || latest.Generation != expected.Generation {
		return nil, "", fmt.Errorf("ExternalIP identity changed while the provider operation was running")
	}
	if latest.Annotations[osacExternalIPContractVersionAnnotation] != networkmanager.ExternalIPContractVersionV1 {
		return nil, "", fmt.Errorf("ExternalIP v1 contract changed while the provider operation was running")
	}
	if jobType == v1alpha1.JobTypeProvision && !latest.DeletionTimestamp.IsZero() {
		return nil, "", fmt.Errorf("ExternalIP is terminating")
	}
	if jobType == v1alpha1.JobTypeDeprovision && latest.DeletionTimestamp.IsZero() {
		return nil, "", fmt.Errorf("ExternalIP is not terminating")
	}
	job := provisioning.FindLatestJobByType(latest.Status.ProvisioningJobs, jobType)
	if job == nil || job.JobID != status.JobID {
		return nil, "", fmt.Errorf("ExternalIP provider result does not match the current job")
	}
	if jobType == v1alpha1.JobTypeProvision &&
		(job.ConfigVersion == "" || job.ConfigVersion != latest.Status.DesiredConfigVersion ||
			job.ConfigVersion != expected.Status.DesiredConfigVersion) {
		return nil, "", fmt.Errorf("ExternalIP allocation job does not match the current configuration")
	}
	if status.State != v1alpha1.JobStateSucceeded {
		return nil, "", fmt.Errorf("ExternalIP provider job did not succeed")
	}
	address, err := provisioning.ValidateExternalIPResult(
		status.ExtraVars, operation, string(latest.UID), latest.Generation,
	)
	if err != nil {
		return nil, "", err
	}
	return latest, address, nil
}

func (r *ExternalIPReconciler) acceptExternalIPAllocation(
	ctx context.Context,
	externalIP *v1alpha1.ExternalIP,
	poolContext provisioning.ExternalIPPoolContext,
	status provisioning.ProvisionStatusWithExtraVars,
) error {
	latest, resultAddress, err := r.validatedExternalIPJobResult(
		ctx, externalIP, status, v1alpha1.JobTypeProvision, "external_ip.allocate",
	)
	if err != nil {
		return err
	}
	if poolContext.KubernetesUID == "" || poolContext.FulfillmentID == "" {
		return fmt.Errorf("ExternalIP allocation is missing its pool identity")
	}
	poolList := &v1alpha1.ExternalIPPoolList{}
	if err := r.APIReader.List(ctx, poolList,
		client.InNamespace(latest.Namespace),
		client.MatchingLabels{osacExternalIPPoolIDLabel: latest.Spec.Pool},
	); err != nil {
		return fmt.Errorf("could not read ExternalIPPool after allocation: %w", err)
	}
	if len(poolList.Items) != 1 {
		return fmt.Errorf("ExternalIP allocation pool is missing or ambiguous")
	}
	pool := &poolList.Items[0]
	currentPool, err := externalIPPoolContext(latest, pool)
	if err != nil {
		return err
	}
	if string(pool.UID) != poolContext.KubernetesUID ||
		currentPool.FulfillmentID != poolContext.FulfillmentID ||
		currentPool.CIDR != poolContext.CIDR ||
		pool.Status.Phase != v1alpha1.ExternalIPPoolPhaseReady ||
		!pool.DeletionTimestamp.IsZero() {
		return fmt.Errorf("ExternalIP allocation pool changed or is not Ready")
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
	_, _, err := r.validatedExternalIPJobResult(
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

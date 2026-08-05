package provisioning

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"

	"github.com/stoewer/go-strcase"
	"sigs.k8s.io/controller-runtime/pkg/client"
	ctrllog "sigs.k8s.io/controller-runtime/pkg/log"

	"github.com/osac-project/osac-operator/api/v1alpha1"
	"github.com/osac-project/osac-operator/pkg/aap"
)

// AAPClient is the interface for AAP operations used by the provider.
type AAPClient interface {
	GetTemplate(ctx context.Context, templateName string) (*aap.Template, error)
	LaunchJobTemplate(ctx context.Context, req aap.LaunchJobTemplateRequest) (*aap.LaunchJobTemplateResponse, error)
	LaunchWorkflowTemplate(ctx context.Context, req aap.LaunchWorkflowTemplateRequest) (*aap.LaunchWorkflowTemplateResponse, error)
	GetJob(ctx context.Context, jobID string) (*aap.Job, error)
	CancelJob(ctx context.Context, jobID string) error
}

// AAPProvider implements ProvisioningProvider using direct AAP REST API integration.
//
// Template resolution supports two modes:
//   - Explicit: provisionTemplate and deprovisionTemplate are set directly
//   - Prefix-based: templatePrefix is set, and template names are derived from the
//     resource Kind (e.g., prefix "osac" + Kind "VirtualNetwork" → "osac-create-virtual-network")
type AAPProvider struct {
	client              AAPClient
	provisionTemplate   string
	deprovisionTemplate string
	templatePrefix      string
}

// NewAAPProvider creates a new AAP provider with explicit template names.
func NewAAPProvider(client AAPClient, provisionTemplate, deprovisionTemplate string) *AAPProvider {
	return &AAPProvider{
		client:              client,
		provisionTemplate:   provisionTemplate,
		deprovisionTemplate: deprovisionTemplate,
	}
}

// NewAAPProviderWithPrefix creates a new AAP provider that derives template names
// from the resource Kind using the given prefix. For example, with prefix "osac" and
// a VirtualNetwork resource, it resolves to "osac-create-virtual-network" and
// "osac-delete-virtual-network".
func NewAAPProviderWithPrefix(client AAPClient, templatePrefix string) *AAPProvider {
	return &AAPProvider{
		client:         client,
		templatePrefix: templatePrefix,
	}
}

// kindToTemplateSuffix maps CRD Kind names to AAP template suffixes where the
// convention (kebab-case of Kind) doesn't match the actual AAP template name.
// Kinds not in this map fall through to strcase.KebabCase(kind).
var kindToTemplateSuffix = map[string]string{
	"ClusterOrder": "hosted-cluster",
}

// resolveTemplateName returns the template name to use for the given action and resource.
// When explicit template names are configured, those are returned directly.
// When a prefix is configured, the name is derived from the resource Kind.
func (p *AAPProvider) resolveTemplateName(action string, resource client.Object) (string, error) {
	switch action {
	case "create":
		if p.provisionTemplate != "" {
			return p.provisionTemplate, nil
		}
	case "delete":
		if p.deprovisionTemplate != "" {
			return p.deprovisionTemplate, nil
		}
	}
	if p.templatePrefix != "" {
		kind := resource.GetObjectKind().GroupVersionKind().Kind
		if kind == "" {
			return "", fmt.Errorf("resource has no Kind set; cannot derive template name from prefix")
		}
		suffix, ok := kindToTemplateSuffix[kind]
		if !ok {
			suffix = strcase.KebabCase(kind)
		}
		return p.templatePrefix + "-" + action + "-" + suffix, nil
	}
	return "", fmt.Errorf("%s template not configured", action)
}

// TriggerProvision triggers provisioning via AAP API.
// Autodetects whether the template is a job_template or workflow_job_template.
func (p *AAPProvider) TriggerProvision(ctx context.Context, resource client.Object) (*ProvisionResult, error) {
	jobID, err := p.launchProvisionJob(ctx, resource)
	if err != nil {
		return nil, err
	}

	return &ProvisionResult{
		JobID:        jobID,
		InitialState: v1alpha1.JobStatePending,
		Message:      "Provisioning job triggered",
	}, nil
}

// launchProvisionJob launches the provision template and returns the job ID.
func (p *AAPProvider) launchProvisionJob(ctx context.Context, resource client.Object) (string, error) {
	templateName, err := p.resolveTemplateName("create", resource)
	if err != nil {
		return "", err
	}
	return p.launchTemplate(ctx, templateName, resource)
}

// GetProvisionStatus checks provisioning job status via AAP API.
func (p *AAPProvider) GetProvisionStatus(ctx context.Context, resource client.Object, jobID string) (ProvisionStatus, error) {
	return p.getJobStatus(ctx, jobID)
}

// TriggerDeprovision attempts to start deprovisioning for a resource.
// It checks whether a running provision job needs to be cancelled first.
func (p *AAPProvider) TriggerDeprovision(ctx context.Context, resource client.Object, provisionJobs []v1alpha1.JobStatus) (*DeprovisionResult, error) {
	ready, provisionStatus, err := p.isReadyForDeprovision(ctx, resource, provisionJobs)
	if err != nil {
		return nil, err
	}
	if !ready {
		return &DeprovisionResult{
			Action:                 DeprovisionWaiting,
			BlockDeletionOnFailure: true,
			ProvisionJobStatus:     provisionStatus,
		}, nil
	}

	jobID, err := p.launchDeprovisionJob(ctx, resource)
	if err != nil {
		return nil, err
	}

	return &DeprovisionResult{
		Action:                 DeprovisionTriggered,
		JobID:                  jobID,
		BlockDeletionOnFailure: true,
		ProvisionJobStatus:     provisionStatus,
	}, nil
}

// isReadyForDeprovision checks if provision job is terminal before deprovisioning.
// Returns (ready, currentProvisionStatus, error).
// - ready: true if ready to deprovision, false if need to wait for provision job cancellation
// - currentProvisionStatus: the actual provision job status from AAP (used to update CR status)
// - error: any error encountered during the check
func (p *AAPProvider) isReadyForDeprovision(ctx context.Context, resource client.Object, provisionJobs []v1alpha1.JobStatus) (bool, *ProvisionStatus, error) {
	log := ctrllog.FromContext(ctx)

	// Find latest provision job
	latestProvisionJob := FindLatestJobByType(provisionJobs, v1alpha1.JobTypeProvision)

	// No provision job - ready to proceed
	if latestProvisionJob == nil {
		log.Info("no provision job found in status, ready to deprovision")
		return true, nil, nil
	}

	log.Info("checking provision job before deprovision", "jobID", latestProvisionJob.JobID, "currentState", latestProvisionJob.State)

	status, err := p.GetProvisionStatus(ctx, resource, latestProvisionJob.JobID)
	if err != nil {
		var notFoundErr *aap.NotFoundError
		if errors.As(err, &notFoundErr) {
			log.Info("AAP job not found (purged), treating as terminal", "jobID", latestProvisionJob.JobID)
			return true, nil, nil
		}
		return false, nil, fmt.Errorf("failed to get provision job status: %w", err)
	}

	log.Info("provision job status retrieved", "jobID", latestProvisionJob.JobID, "state", status.State, "isTerminal", status.State.IsTerminal())

	// Job already terminal - ready to proceed
	if status.State.IsTerminal() {
		log.Info("provision job is terminal, ready to deprovision", "jobID", latestProvisionJob.JobID, "state", status.State)
		return true, &status, nil
	}

	// Job still running - cancel it
	log.Info("provision job is running, attempting to cancel", "jobID", latestProvisionJob.JobID, "state", status.State)
	if err := p.cancelProvisionJob(ctx, latestProvisionJob.JobID); err != nil {
		var methodNotAllowedErr *aap.MethodNotAllowedError
		if !errors.As(err, &methodNotAllowedErr) {
			return false, &status, fmt.Errorf("failed to cancel provision job: %w", err)
		}
		// 405 means already terminal, proceed
		log.Info("job cancel returned 405 (already terminal), ready to deprovision", "jobID", latestProvisionJob.JobID)
		return true, &status, nil
	}

	// Cancellation initiated - need to wait, return current status for CR update
	log.Info("provision job cancellation initiated, waiting for termination", "jobID", latestProvisionJob.JobID)
	return false, &status, nil
}

// cancelProvisionJob attempts to cancel a running provision job via AAP API.
// Returns nil if cancellation was initiated (HTTP 202). Returns *aap.MethodNotAllowedError when
// AAP responds with HTTP 405 (job already terminal); the caller proceeds to deprovision immediately.
// Note: Cancellation is asynchronous. The job status should be polled to confirm termination.
func (p *AAPProvider) cancelProvisionJob(ctx context.Context, jobID string) error {
	// HTTP 202 → cancellation initiated (nil)
	// HTTP 405 → job already terminal (*aap.MethodNotAllowedError, handled by caller)
	err := p.client.CancelJob(ctx, jobID)
	if err != nil {
		// Check if error is "Method not allowed" (405) - indicates job already terminal
		var methodNotAllowedErr *aap.MethodNotAllowedError
		if errors.As(err, &methodNotAllowedErr) {
			// Propagate 405 so the caller can proceed immediately instead of waiting another poll.
			return err
		}
		return fmt.Errorf("failed to cancel job: %w", err)
	}

	return nil
}

// launchDeprovisionJob launches the deprovision template and returns the job ID.
func (p *AAPProvider) launchDeprovisionJob(ctx context.Context, resource client.Object) (string, error) {
	templateName, err := p.resolveTemplateName("delete", resource)
	if err != nil {
		return "", err
	}
	return p.launchTemplate(ctx, templateName, resource)
}

// launchTemplate launches the named template (job or workflow) and returns the job ID.
func (p *AAPProvider) launchTemplate(ctx context.Context, templateName string, resource client.Object) (string, error) {
	template, err := p.client.GetTemplate(ctx, templateName)
	if err != nil {
		return "", fmt.Errorf("failed to get template: %w", err)
	}

	extraVars, err := extractExtraVars(ctx, resource)
	if err != nil {
		return "", fmt.Errorf("failed to extract extra vars: %w", err)
	}

	var jobID int
	switch template.Type {
	case aap.TemplateTypeJob:
		resp, err := p.client.LaunchJobTemplate(ctx, aap.LaunchJobTemplateRequest{
			TemplateID:   template.ID,
			TemplateName: templateName,
			ExtraVars:    extraVars,
		})
		if err != nil {
			return "", fmt.Errorf("failed to launch job template: %w", err)
		}
		jobID = resp.JobID
	case aap.TemplateTypeWorkflow:
		resp, err := p.client.LaunchWorkflowTemplate(ctx, aap.LaunchWorkflowTemplateRequest{
			TemplateID:   template.ID,
			TemplateName: templateName,
			ExtraVars:    extraVars,
		})
		if err != nil {
			return "", fmt.Errorf("failed to launch workflow template: %w", err)
		}
		jobID = resp.JobID
	default:
		return "", fmt.Errorf("unknown template type: %s", template.Type)
	}

	return strconv.Itoa(jobID), nil
}

// GetDeprovisionStatus checks deprovisioning job status via AAP API.
func (p *AAPProvider) GetDeprovisionStatus(ctx context.Context, resource client.Object, jobID string) (ProvisionStatus, error) {
	return p.getJobStatus(ctx, jobID)
}

// Name returns the provider name for logging.
func (p *AAPProvider) Name() string {
	return "aap"
}

// getJobStatus retrieves job status from AAP and converts it to ProvisionStatus.
func (p *AAPProvider) getJobStatus(ctx context.Context, jobID string) (ProvisionStatus, error) {
	job, err := p.client.GetJob(ctx, jobID)
	if err != nil {
		return ProvisionStatus{}, fmt.Errorf("failed to get job: %w", err)
	}

	status := ProvisionStatus{
		JobID:     jobID,
		State:     mapAAPStatusToJobState(job.Status),
		Message:   job.Status,
		StartTime: job.Started,
		EndTime:   job.Finished,
	}

	// Populate error details if job failed
	if status.State == v1alpha1.JobStateFailed && job.ResultTraceback != "" {
		status.ErrorDetails = job.ResultTraceback
	}

	return status, nil
}

// mapAAPStatusToJobState converts AAP job status to JobState.
func mapAAPStatusToJobState(aapStatus string) v1alpha1.JobState {
	switch aapStatus {
	case "successful":
		return v1alpha1.JobStateSucceeded
	case "failed", "error":
		return v1alpha1.JobStateFailed
	case "canceled":
		return v1alpha1.JobStateCanceled
	case "pending":
		return v1alpha1.JobStatePending
	case "waiting":
		return v1alpha1.JobStateWaiting
	case "running":
		return v1alpha1.JobStateRunning
	default:
		// Unknown states should be marked as Unknown (non-terminal) to allow continued polling
		return v1alpha1.JobStateUnknown
	}
}

// extractExtraVars extracts extra variables from a resource to pass to AAP.
//
// AAP templates expect the Kubernetes resource wrapped in an ansible_eda.event.payload
// structure. Playbooks read fields such as ansible_eda.event.payload.spec and
// ansible_eda.event.payload.metadata.
func extractExtraVars(ctx context.Context, resource client.Object) (map[string]any, error) {
	// Convert the resource to map using JSON marshaling (respects JSON tags)
	resourceMap, err := serializeResource(resource)
	if err != nil {
		return nil, fmt.Errorf("failed to serialize resource: %w", err)
	}

	event := map[string]any{
		"payload": resourceMap,
	}

	// Inject tenant storage classes if present in context (set by CI controller)
	if scs := TenantStorageClassesFromContext(ctx); len(scs) > 0 {
		scList := make([]map[string]string, len(scs))
		for i, sc := range scs {
			scList[i] = map[string]string{"name": sc.Name, "tier": sc.Tier}
		}
		event["tenant_storage_classes"] = scList
	}

	if kc := AdminKubeconfigFromContext(ctx); kc != "" {
		event["admin_kubeconfig"] = kc
	}

	if tiers := StorageTierDefinitionsFromContext(ctx); len(tiers) > 0 {
		event["storage_tier_definitions"] = tierDefinitionsToExtraVars(tiers)
	}

	if conns := StorageBackendConnectionsFromContext(ctx); len(conns) > 0 {
		event["storage_backend_connections"] = backendConnectionsToExtraVars(conns)
	}

	// Wrap in ansible_eda.event structure for AAP template compatibility.
	return map[string]any{
		"ansible_eda": map[string]any{
			"event": event,
		},
	}, nil
}

// bytesPerGiB converts a gibibyte count to bytes (1 GiB = 2^30 bytes).
const bytesPerGiB = 1 << 30

// tierDefinitionsToExtraVars converts tier definitions to the AAP-schema-shaped map
// format osac-aap's storage_provider role expects (storage_provider/meta/
// argument_specs.yaml). Field names are a deliberate departure from the Go/proto
// field names: quota_bytes (not QuotaGiB) carries tier.QuotaGiB converted to bytes
// — the pre-existing static STORAGE_TIERS path already documents its own "quota"
// field as bytes (see config/base/configmap-storage-operations-ig-example.yaml in
// osac-aap), so this key is named quota_bytes rather than reusing "quota" to keep
// the unit unambiguous; a follow-on osac-aap change (OSAC-1992's AC 4/5) is
// required to read it. max_reads_bw_mbps/max_writes_bw_mbps match the role's
// documented example, not the Go struct's MaxReadBandwidthMBs/MaxWriteBandwidthMBs.
// No qos_policy key — osac-aap derives "<name>-qos" from the tier name it already
// receives.
func tierDefinitionsToExtraVars(tiers []TierDefinition) []map[string]any {
	result := make([]map[string]any, len(tiers))
	for i, tier := range tiers {
		result[i] = map[string]any{
			"name":       tier.Name,
			"protocol":   tier.Protocol,
			"provider":   tier.Provider,
			"backend_id": tier.BackendID,
			"qos_limits": map[string]any{
				"static_limits": map[string]any{
					"max_reads_bw_mbps":  tier.QosLimits.MaxReadBandwidthMBs,
					"max_writes_bw_mbps": tier.QosLimits.MaxWriteBandwidthMBs,
				},
			},
			"quota_bytes": tier.QuotaGiB * bytesPerGiB,
		}
	}
	return result
}

// backendConnectionsToExtraVars converts backend connection details to the
// AAP-schema-shaped map format, keyed by backend_id, one entry per unique backend
// (never repeated per tier — see BackendConnection's doc comment).
func backendConnectionsToExtraVars(conns map[string]BackendConnection) map[string]map[string]any {
	result := make(map[string]map[string]any, len(conns))
	for backendID, conn := range conns {
		result[backendID] = map[string]any{
			"endpoint": conn.Endpoint,
			"username": conn.Username,
			"password": conn.Password,
		}
	}
	return result
}

// serializeResource converts a Kubernetes resource to a map using JSON marshaling.
func serializeResource(resource client.Object) (map[string]any, error) {
	// Marshal to JSON
	jsonBytes, err := json.Marshal(resource)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal resource to JSON: %w", err)
	}

	// Unmarshal back to map[string]any
	var resourceMap map[string]any
	if err := json.Unmarshal(jsonBytes, &resourceMap); err != nil {
		return nil, fmt.Errorf("failed to unmarshal JSON to map: %w", err)
	}

	return resourceMap, nil
}

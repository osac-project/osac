package provisioning

import (
	"strings"
	"testing"

	"github.com/osac-project/osac/osac-operator/api/v1alpha1"
)

func TestValidateProviderJobResult(t *testing.T) {
	baseJob := v1alpha1.JobStatus{
		JobID:         "job-1",
		Type:          v1alpha1.JobTypeProvision,
		State:         v1alpha1.JobStateSucceeded,
		ConfigVersion: "config-1",
	}
	baseStatus := ProvisionStatusWithExtraVars{
		ProvisionStatus: ProvisionStatus{
			JobID: "job-1",
			State: v1alpha1.JobStateSucceeded,
		},
	}
	tests := []struct {
		name      string
		job       v1alpha1.JobStatus
		status    ProvisionStatusWithExtraVars
		jobType   v1alpha1.JobType
		versions  []string
		wantError string
	}{
		{
			name:      "latest job must match the provider result",
			job:       baseJob,
			status:    ProvisionStatusWithExtraVars{ProvisionStatus: ProvisionStatus{JobID: "job-2", State: v1alpha1.JobStateSucceeded}},
			jobType:   v1alpha1.JobTypeProvision,
			versions:  []string{"config-1"},
			wantError: "latest job",
		},
		{
			name:      "provider job must succeed",
			job:       baseJob,
			status:    ProvisionStatusWithExtraVars{ProvisionStatus: ProvisionStatus{JobID: "job-1", State: v1alpha1.JobStateFailed}},
			jobType:   v1alpha1.JobTypeProvision,
			versions:  []string{"config-1"},
			wantError: "did not succeed",
		},
		{
			name:      "provision job must have a config version",
			job:       v1alpha1.JobStatus{JobID: baseJob.JobID, Type: baseJob.Type, State: baseJob.State},
			status:    baseStatus,
			jobType:   v1alpha1.JobTypeProvision,
			versions:  []string{"config-1"},
			wantError: "desired configuration",
		},
		{
			name:      "config version must match every observed desired version",
			job:       baseJob,
			status:    baseStatus,
			jobType:   v1alpha1.JobTypeProvision,
			versions:  []string{"config-1", "config-2"},
			wantError: "desired configuration",
		},
		{
			name:     "successful provision must match desired configuration",
			job:      baseJob,
			status:   baseStatus,
			jobType:  v1alpha1.JobTypeProvision,
			versions: []string{"config-1"},
		},
		{
			name: "successful deprovision does not require a config version",
			job: v1alpha1.JobStatus{
				JobID: "job-1", Type: v1alpha1.JobTypeDeprovision, State: v1alpha1.JobStateSucceeded,
			},
			status:   baseStatus,
			jobType:  v1alpha1.JobTypeDeprovision,
			versions: []string{"ignored"},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			err := ValidateProviderJobResult(
				[]v1alpha1.JobStatus{test.job}, test.jobType, test.status, test.versions...,
			)
			if test.wantError == "" {
				if err != nil {
					t.Fatalf("ValidateProviderJobResult() error = %v", err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), test.wantError) {
				t.Fatalf("ValidateProviderJobResult() error = %v, want %q", err, test.wantError)
			}
		})
	}
}

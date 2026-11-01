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

package controller

import (
	"context"
	"fmt"
	"sync"

	"sigs.k8s.io/controller-runtime/pkg/client"

	osacv1alpha1 "github.com/osac-project/osac/osac-operator/api/v1alpha1"
	"github.com/osac-project/osac/osac-operator/pkg/provisioning"
)

// controllableProvider is a mock provider that allows test control over job progression
type controllableProvider struct {
	mu sync.Mutex

	provisionJobID      string
	provisionJobState   osacv1alpha1.JobState
	provisionJobMsg     string
	provisionTriggerErr error
	provisionCallCount  int

	deprovisionJobID      string
	deprovisionJobState   osacv1alpha1.JobState
	deprovisionJobMsg     string
	deprovisionTriggerErr error
}

func newControllableProvider() *controllableProvider {
	return &controllableProvider{
		provisionJobState:   osacv1alpha1.JobStatePending,
		deprovisionJobState: osacv1alpha1.JobStatePending,
	}
}

func (p *controllableProvider) TriggerProvision(ctx context.Context, resource client.Object) (*provisioning.ProvisionResult, error) {
	p.mu.Lock()
	defer p.mu.Unlock()

	if p.provisionTriggerErr != nil {
		return nil, p.provisionTriggerErr
	}

	p.provisionCallCount++
	p.provisionJobID = fmt.Sprintf("prov-job-%s-%d", resource.GetName(), p.provisionCallCount)
	p.provisionJobState = osacv1alpha1.JobStatePending
	p.provisionJobMsg = "Job triggered"
	return &provisioning.ProvisionResult{
		JobID:        p.provisionJobID,
		InitialState: osacv1alpha1.JobStatePending,
		Message:      "Job triggered",
	}, nil
}

func (p *controllableProvider) GetProvisionStatus(ctx context.Context, resource client.Object, jobID string) (provisioning.ProvisionStatus, error) {
	p.mu.Lock()
	defer p.mu.Unlock()

	return provisioning.ProvisionStatus{
		JobID:   jobID,
		State:   p.provisionJobState,
		Message: p.provisionJobMsg,
	}, nil
}

func (p *controllableProvider) TriggerDeprovision(ctx context.Context, resource client.Object, provisionJobs []osacv1alpha1.JobStatus) (*provisioning.DeprovisionResult, error) {
	p.mu.Lock()
	defer p.mu.Unlock()

	if p.deprovisionTriggerErr != nil {
		return nil, p.deprovisionTriggerErr
	}

	latestProvisionJob := provisioning.FindLatestJobByType(provisionJobs, osacv1alpha1.JobTypeProvision)
	if latestProvisionJob != nil {
		if !p.provisionJobState.IsTerminal() {
			// Cancel the provision job
			p.provisionJobState = osacv1alpha1.JobStateCanceled
			p.provisionJobMsg = "Job canceled"
			// Return waiting - need to poll again
			return &provisioning.DeprovisionResult{
				Action:                 provisioning.DeprovisionWaiting,
				BlockDeletionOnFailure: true,
			}, nil
		}
	}

	p.deprovisionJobID = "deprov-job-" + resource.GetName()
	p.deprovisionJobState = osacv1alpha1.JobStatePending
	p.deprovisionJobMsg = "Deprovision job triggered"
	return &provisioning.DeprovisionResult{
		Action:                 provisioning.DeprovisionTriggered,
		JobID:                  p.deprovisionJobID,
		BlockDeletionOnFailure: true,
	}, nil
}

func (p *controllableProvider) GetDeprovisionStatus(ctx context.Context, resource client.Object, jobID string) (provisioning.ProvisionStatus, error) {
	p.mu.Lock()
	defer p.mu.Unlock()

	return provisioning.ProvisionStatus{
		JobID:   jobID,
		State:   p.deprovisionJobState,
		Message: p.deprovisionJobMsg,
	}, nil
}

func (p *controllableProvider) Name() string {
	return "aap"
}

// setProvisionJobState updates the provision job state (thread-safe)
func (p *controllableProvider) setProvisionJobState(state osacv1alpha1.JobState, message string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.provisionJobState = state
	p.provisionJobMsg = message
}

// setDeprovisionJobState updates the deprovision job state (thread-safe)
func (p *controllableProvider) setDeprovisionJobState(state osacv1alpha1.JobState, message string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.deprovisionJobState = state
	p.deprovisionJobMsg = message
}

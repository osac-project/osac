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
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	osacv1alpha1 "github.com/osac-project/osac/osac-operator/api/v1alpha1"
	"github.com/osac-project/osac/osac-operator/pkg/provisioning"
)

// mockProvisioningProvider implements the ProvisioningProvider interface for testing
type mockProvisioningProvider struct {
	name                                  string
	triggerProvisionFunc                  func(ctx context.Context, resource client.Object) (*provisioning.ProvisionResult, error)
	getProvisionStatusFunc                func(ctx context.Context, resource client.Object, jobID string) (provisioning.ProvisionStatus, error)
	getProvisionStatusWithExtraVarsFunc   func(ctx context.Context, resource client.Object, jobID string) (provisioning.ProvisionStatusWithExtraVars, error)
	triggerDeprovisionFunc                func(ctx context.Context, resource client.Object, provisionJobs []osacv1alpha1.JobStatus) (*provisioning.DeprovisionResult, error)
	getDeprovisionStatusFunc              func(ctx context.Context, resource client.Object, jobID string) (provisioning.ProvisionStatus, error)
	getDeprovisionStatusWithExtraVarsFunc func(ctx context.Context, resource client.Object, jobID string) (provisioning.ProvisionStatusWithExtraVars, error)
	findJobByLaunchIDFunc                 func(ctx context.Context, resource client.Object, jobType osacv1alpha1.JobType, launchID string) (provisioning.ProvisionStatus, bool, error)
}

func (m *mockProvisioningProvider) TriggerProvision(ctx context.Context, resource client.Object) (*provisioning.ProvisionResult, error) {
	if m.triggerProvisionFunc != nil {
		return m.triggerProvisionFunc(ctx, resource)
	}
	return &provisioning.ProvisionResult{
		JobID:        "mock-job-id",
		InitialState: osacv1alpha1.JobStatePending,
		Message:      "Provisioning job triggered",
	}, nil
}

func (m *mockProvisioningProvider) GetProvisionStatus(ctx context.Context, resource client.Object, jobID string) (provisioning.ProvisionStatus, error) {
	if m.getProvisionStatusFunc != nil {
		return m.getProvisionStatusFunc(ctx, resource, jobID)
	}
	return provisioning.ProvisionStatus{
		JobID:   jobID,
		State:   osacv1alpha1.JobStateSucceeded,
		Message: "Job completed successfully",
	}, nil
}

func (m *mockProvisioningProvider) GetProvisionStatusWithExtraVars(
	ctx context.Context, resource client.Object, jobID string,
) (provisioning.ProvisionStatusWithExtraVars, error) {
	if m.getProvisionStatusWithExtraVarsFunc != nil {
		return m.getProvisionStatusWithExtraVarsFunc(ctx, resource, jobID)
	}
	status, err := m.GetProvisionStatus(ctx, resource, jobID)
	return provisioning.ProvisionStatusWithExtraVars{ProvisionStatus: status}, err
}

func (m *mockProvisioningProvider) TriggerDeprovision(ctx context.Context, resource client.Object, provisionJobs []osacv1alpha1.JobStatus) (*provisioning.DeprovisionResult, error) {
	if m.triggerDeprovisionFunc != nil {
		return m.triggerDeprovisionFunc(ctx, resource, provisionJobs)
	}
	return &provisioning.DeprovisionResult{
		Action:                 provisioning.DeprovisionTriggered,
		JobID:                  "mock-deprovision-job-id",
		BlockDeletionOnFailure: true,
	}, nil
}

func (m *mockProvisioningProvider) GetDeprovisionStatus(ctx context.Context, resource client.Object, jobID string) (provisioning.ProvisionStatus, error) {
	if m.getDeprovisionStatusFunc != nil {
		return m.getDeprovisionStatusFunc(ctx, resource, jobID)
	}
	return provisioning.ProvisionStatus{
		JobID:   jobID,
		State:   osacv1alpha1.JobStateSucceeded,
		Message: "Deprovision completed successfully",
	}, nil
}

func (m *mockProvisioningProvider) GetDeprovisionStatusWithExtraVars(
	ctx context.Context, resource client.Object, jobID string,
) (provisioning.ProvisionStatusWithExtraVars, error) {
	if m.getDeprovisionStatusWithExtraVarsFunc != nil {
		return m.getDeprovisionStatusWithExtraVarsFunc(ctx, resource, jobID)
	}
	status, err := m.GetDeprovisionStatus(ctx, resource, jobID)
	return provisioning.ProvisionStatusWithExtraVars{ProvisionStatus: status}, err
}

func (m *mockProvisioningProvider) FindJobByLaunchID(
	ctx context.Context,
	resource client.Object,
	jobType osacv1alpha1.JobType,
	launchID string,
) (provisioning.ProvisionStatus, bool, error) {
	if m.findJobByLaunchIDFunc != nil {
		return m.findJobByLaunchIDFunc(ctx, resource, jobType, launchID)
	}
	return provisioning.ProvisionStatus{}, false, nil
}

func (m *mockProvisioningProvider) Name() string {
	if m.name != "" {
		return m.name
	}
	return "mock"
}

var _ = Describe("ComputeInstance running config", func() {
	runningInstance := func(phase osacv1alpha1.ComputeInstancePhaseType, applied, restartRequired bool) *osacv1alpha1.ComputeInstance {
		conditionStatus := func(value bool) metav1.ConditionStatus {
			if value {
				return metav1.ConditionTrue
			}
			return metav1.ConditionFalse
		}
		return &osacv1alpha1.ComputeInstance{
			ObjectMeta: metav1.ObjectMeta{
				Labels: map[string]string{osacComputeInstanceTypeNameLabel: "standard-8-32"},
			},
			Status: osacv1alpha1.ComputeInstanceStatus{
				Phase:                phase,
				DesiredConfigVersion: "v2",
				RunningConfigVersion: "v1",
				RunningConfigSince:   &metav1.Time{Time: time.Now().Add(-time.Hour).UTC()},
				Conditions: []metav1.Condition{
					{
						Type:               string(osacv1alpha1.ComputeInstanceConditionConfigurationApplied),
						Status:             conditionStatus(applied),
						Reason:             "AsExpected",
						LastTransitionTime: metav1.NewTime(time.Now().UTC()),
					},
					{
						Type:               string(osacv1alpha1.ComputeInstanceConditionRestartRequired),
						Status:             conditionStatus(restartRequired),
						Reason:             "AsExpected",
						LastTransitionTime: metav1.NewTime(time.Now().UTC()),
					},
				},
			},
		}
	}

	It("records the version and time when the new configuration is running", func() {
		instance := runningInstance(osacv1alpha1.ComputeInstancePhaseRunning, true, false)
		previous := instance.Status.RunningConfigSince.Time

		recordRunningConfig(instance)

		Expect(instance.Status.RunningConfigVersion).To(Equal("v2"))
		Expect(instance.Status.RunningConfigSince).ToNot(BeNil())
		Expect(instance.Status.RunningConfigSince.Time).ToNot(Equal(previous))
	})

	It("does not re-record while the running configuration is unchanged", func() {
		instance := runningInstance(osacv1alpha1.ComputeInstancePhaseRunning, true, false)
		recordRunningConfig(instance)
		recorded := instance.Status.RunningConfigSince.Time

		recordRunningConfig(instance)

		Expect(instance.Status.RunningConfigVersion).To(Equal("v2"))
		Expect(instance.Status.RunningConfigSince.Time).To(Equal(recorded))
	})

	It("does not record before the new configuration is applied", func() {
		instance := runningInstance(osacv1alpha1.ComputeInstancePhaseRunning, false, false)

		recordRunningConfig(instance)

		Expect(instance.Status.RunningConfigVersion).To(Equal("v1"))
	})

	It("clears the recorded observation while a restart is still required", func() {
		instance := runningInstance(osacv1alpha1.ComputeInstancePhaseRunning, true, true)

		recordRunningConfig(instance)

		Expect(instance.Status.RunningConfigVersion).To(Equal(""))
		Expect(instance.Status.RunningInstanceTypeName).To(Equal(""))
		Expect(instance.Status.RunningConfigSince).To(BeNil())
	})

	It("invalidates the observation when a restart becomes required after it", func() {
		instance := runningInstance(osacv1alpha1.ComputeInstancePhaseRunning, true, false)
		instance.Labels = map[string]string{osacComputeInstanceTypeNameLabel: "standard-8-32"}
		recordRunningConfig(instance)
		Expect(instance.Status.RunningConfigVersion).To(Equal("v2"))
		recorded := instance.Status.RunningConfigSince.Time

		// KubeVirt reports RestartRequired asynchronously, so the requirement can
		// surface only after the observation was recorded.
		instance.SetStatusCondition(
			osacv1alpha1.ComputeInstanceConditionRestartRequired,
			metav1.ConditionTrue, "", osacv1alpha1.ReasonAsExpected)

		recordRunningConfig(instance)

		Expect(instance.Status.RunningConfigVersion).To(Equal(""))
		Expect(instance.Status.RunningInstanceTypeName).To(Equal(""))
		Expect(instance.Status.RunningConfigSince).To(BeNil())

		// Once the restart completes, the configuration is observed running again and
		// re-recorded, so the new configuration bills from after the restart.
		instance.SetStatusCondition(
			osacv1alpha1.ComputeInstanceConditionRestartRequired,
			metav1.ConditionFalse, "", osacv1alpha1.ReasonAsExpected)

		recordRunningConfig(instance)

		Expect(instance.Status.RunningConfigVersion).To(Equal("v2"))
		Expect(instance.Status.RunningInstanceTypeName).To(Equal("standard-8-32"))
		Expect(instance.Status.RunningConfigSince).ToNot(BeNil())
		Expect(instance.Status.RunningConfigSince.Time).ToNot(Equal(recorded))
	})

	It("moves the stamp when the instance type name changes with identical resources", func() {
		// The config version hashes the resolved resources, so a resize between two
		// types with identical cores and memory keeps the version; the type name is
		// part of the running-config identity so the stamp still moves and the new
		// type bills from the observation.
		instance := runningInstance(osacv1alpha1.ComputeInstancePhaseRunning, true, false)
		instance.Labels = map[string]string{osacComputeInstanceTypeNameLabel: "standard-8-32"}
		recordRunningConfig(instance)
		recorded := instance.Status.RunningConfigSince.Time
		Expect(instance.Status.RunningInstanceTypeName).To(Equal("standard-8-32"))

		instance.Labels[osacComputeInstanceTypeNameLabel] = "standard-8-32-flavored"

		recordRunningConfig(instance)

		Expect(instance.Status.RunningConfigVersion).To(Equal("v2"))
		Expect(instance.Status.RunningInstanceTypeName).To(Equal("standard-8-32-flavored"))
		Expect(instance.Status.RunningConfigSince.Time).ToNot(Equal(recorded))
	})

	It("does not record while the instance is not running", func() {
		instance := runningInstance(osacv1alpha1.ComputeInstancePhaseStarting, true, false)

		recordRunningConfig(instance)

		Expect(instance.Status.RunningConfigVersion).To(Equal("v1"))
	})
})

var _ = Describe("ComputeInstance phase transition time", func() {
	It("stamps the time when the phase changes", func() {
		instance := &osacv1alpha1.ComputeInstance{Status: osacv1alpha1.ComputeInstanceStatus{
			Phase: osacv1alpha1.ComputeInstancePhaseStarting,
		}}

		setComputeInstancePhase(&instance.Status, osacv1alpha1.ComputeInstancePhaseRunning)

		Expect(instance.Status.Phase).To(Equal(osacv1alpha1.ComputeInstancePhaseRunning))
		Expect(instance.Status.PhaseTransitionTime).NotTo(BeNil())
	})

	It("stamps the first phase of a new instance", func() {
		instance := &osacv1alpha1.ComputeInstance{}

		setComputeInstancePhase(&instance.Status, osacv1alpha1.ComputeInstancePhaseStarting)

		Expect(instance.Status.Phase).To(Equal(osacv1alpha1.ComputeInstancePhaseStarting))
		Expect(instance.Status.PhaseTransitionTime).NotTo(BeNil())
	})

	It("keeps the time while the phase is unchanged", func() {
		stamped := metav1.NewTime(time.Now().Add(-time.Hour).UTC())
		instance := &osacv1alpha1.ComputeInstance{Status: osacv1alpha1.ComputeInstanceStatus{
			Phase:               osacv1alpha1.ComputeInstancePhaseRunning,
			PhaseTransitionTime: &stamped,
		}}

		setComputeInstancePhase(&instance.Status, osacv1alpha1.ComputeInstancePhaseRunning)

		Expect(instance.Status.PhaseTransitionTime.Time).To(Equal(stamped.Time))
	})
})

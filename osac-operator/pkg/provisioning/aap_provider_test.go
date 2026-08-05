package provisioning_test

import (
	"context"
	"errors"
	"fmt"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/osac-project/osac-operator/api/v1alpha1"
	"github.com/osac-project/osac-operator/pkg/aap"
	"github.com/osac-project/osac-operator/pkg/provisioning"
)

// mockAAPClient is a test double for aap.Client
type mockAAPClient struct {
	getTemplateFunc            func(ctx context.Context, templateName string) (*aap.Template, error)
	launchJobTemplateFunc      func(ctx context.Context, req aap.LaunchJobTemplateRequest) (*aap.LaunchJobTemplateResponse, error)
	launchWorkflowTemplateFunc func(ctx context.Context, req aap.LaunchWorkflowTemplateRequest) (*aap.LaunchWorkflowTemplateResponse, error)
	getJobFunc                 func(ctx context.Context, jobID string) (*aap.Job, error)
	cancelJobFunc              func(ctx context.Context, jobID string) error
}

func (m *mockAAPClient) GetTemplate(ctx context.Context, templateName string) (*aap.Template, error) {
	if m.getTemplateFunc != nil {
		return m.getTemplateFunc(ctx, templateName)
	}
	return &aap.Template{ID: 1, Name: templateName, Type: aap.TemplateTypeJob}, nil
}

func (m *mockAAPClient) LaunchJobTemplate(ctx context.Context, req aap.LaunchJobTemplateRequest) (*aap.LaunchJobTemplateResponse, error) {
	if m.launchJobTemplateFunc != nil {
		return m.launchJobTemplateFunc(ctx, req)
	}
	return &aap.LaunchJobTemplateResponse{JobID: 123}, nil
}

func (m *mockAAPClient) LaunchWorkflowTemplate(ctx context.Context, req aap.LaunchWorkflowTemplateRequest) (*aap.LaunchWorkflowTemplateResponse, error) {
	if m.launchWorkflowTemplateFunc != nil {
		return m.launchWorkflowTemplateFunc(ctx, req)
	}
	return &aap.LaunchWorkflowTemplateResponse{JobID: 456}, nil
}

func (m *mockAAPClient) GetJob(ctx context.Context, jobID string) (*aap.Job, error) {
	if m.getJobFunc != nil {
		return m.getJobFunc(ctx, jobID)
	}
	// Convert jobID string to int for the ID field
	var id int
	if _, err := fmt.Sscanf(jobID, "%d", &id); err != nil {
		id = 123 // default
	}
	return &aap.Job{
		ID:       id,
		Status:   "successful",
		Started:  time.Now().UTC(),
		Finished: time.Now().UTC().Add(time.Minute),
	}, nil
}

func (m *mockAAPClient) CancelJob(ctx context.Context, jobID string) error {
	if m.cancelJobFunc != nil {
		return m.cancelJobFunc(ctx, jobID)
	}
	return nil
}

// extractEDAPayload extracts the payload from EDA event structure in extra_vars.
// This helper function is used to verify the EDA compatibility wrapper.
func extractEDAPayload(extraVars map[string]any) map[string]any {
	edaEvent := extraVars["ansible_eda"].(map[string]any)
	return edaEvent["event"].(map[string]any)["payload"].(map[string]any)
}

var _ = Describe("AAPProvider", func() {
	var (
		provider  *provisioning.AAPProvider
		aapClient *mockAAPClient
		ctx       context.Context
	)

	BeforeEach(func() {
		ctx = context.Background()
		aapClient = &mockAAPClient{}
	})

	Describe("TriggerProvision", func() {
		Context("with job template", func() {
			BeforeEach(func() {
				provider = provisioning.NewAAPProvider(aapClient, "provision-job", "deprovision-job")
				aapClient.getTemplateFunc = func(ctx context.Context, templateName string) (*aap.Template, error) {
					return &aap.Template{ID: 1, Name: templateName, Type: aap.TemplateTypeJob}, nil
				}
				aapClient.launchJobTemplateFunc = func(ctx context.Context, req aap.LaunchJobTemplateRequest) (*aap.LaunchJobTemplateResponse, error) {
					Expect(req.TemplateName).To(Equal("provision-job"))
					// Verify EDA event structure for compatibility with EDA-designed templates
					Expect(req.ExtraVars).To(HaveKey("ansible_eda"))
					payload := extractEDAPayload(req.ExtraVars)
					// Verify serialized resource contains the ObjectMeta fields under "metadata"
					Expect(payload).To(HaveKey("metadata"))
					metadata := payload["metadata"].(map[string]any)
					Expect(metadata).To(HaveKeyWithValue("name", "test-resource"))
					Expect(metadata).To(HaveKeyWithValue("namespace", "default"))
					return &aap.LaunchJobTemplateResponse{JobID: 123}, nil
				}
			})

			It("should launch job template and return job ID", func() {
				instance := &v1alpha1.ComputeInstance{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "test-resource",
						Namespace: "default",
					},
				}
				result, err := provider.TriggerProvision(ctx, instance)
				Expect(err).NotTo(HaveOccurred())
				Expect(result.JobID).To(Equal("123"))
				Expect(result.InitialState).To(Equal(v1alpha1.JobStatePending))
				Expect(result.Message).To(Equal("Provisioning job triggered"))
			})
		})

		Context("with workflow template", func() {
			BeforeEach(func() {
				provider = provisioning.NewAAPProvider(aapClient, "provision-workflow", "deprovision-workflow")
				aapClient.getTemplateFunc = func(ctx context.Context, templateName string) (*aap.Template, error) {
					return &aap.Template{ID: 2, Name: templateName, Type: aap.TemplateTypeWorkflow}, nil
				}
				aapClient.launchWorkflowTemplateFunc = func(ctx context.Context, req aap.LaunchWorkflowTemplateRequest) (*aap.LaunchWorkflowTemplateResponse, error) {
					Expect(req.TemplateName).To(Equal("provision-workflow"))
					// Verify EDA event structure for compatibility with EDA-designed templates
					Expect(req.ExtraVars).To(HaveKey("ansible_eda"))
					payload := extractEDAPayload(req.ExtraVars)
					// Verify serialized resource contains the ObjectMeta fields under "metadata"
					Expect(payload).To(HaveKey("metadata"))
					metadata := payload["metadata"].(map[string]any)
					Expect(metadata).To(HaveKeyWithValue("namespace", "default"))
					return &aap.LaunchWorkflowTemplateResponse{JobID: 456}, nil
				}
			})

			It("should launch workflow template and return job ID", func() {
				instance := &v1alpha1.ComputeInstance{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "test-resource",
						Namespace: "default",
					},
				}
				result, err := provider.TriggerProvision(ctx, instance)
				Expect(err).NotTo(HaveOccurred())
				Expect(result.JobID).To(Equal("456"))
				Expect(result.InitialState).To(Equal(v1alpha1.JobStatePending))
				Expect(result.Message).To(Equal("Provisioning job triggered"))
			})
		})

		Context("with tenant storage classes in context", func() {
			BeforeEach(func() {
				provider = provisioning.NewAAPProvider(aapClient, "provision-job", "deprovision-job")
				aapClient.getTemplateFunc = func(ctx context.Context, templateName string) (*aap.Template, error) {
					return &aap.Template{ID: 1, Name: templateName, Type: aap.TemplateTypeJob}, nil
				}
			})

			It("should inject tenant_storage_classes into extra_vars", func() {
				aapClient.launchJobTemplateFunc = func(ctx context.Context, req aap.LaunchJobTemplateRequest) (*aap.LaunchJobTemplateResponse, error) {
					edaEvent := req.ExtraVars["ansible_eda"].(map[string]any)["event"].(map[string]any)
					Expect(edaEvent).To(HaveKey("payload"))
					Expect(edaEvent).To(HaveKey("tenant_storage_classes"))
					scList := edaEvent["tenant_storage_classes"].([]map[string]string)
					Expect(scList).To(HaveLen(2))
					Expect(scList[0]).To(Equal(map[string]string{"name": "ceph-fast", "tier": "fast"}))
					Expect(scList[1]).To(Equal(map[string]string{"name": "ceph-default", "tier": "default"}))
					return &aap.LaunchJobTemplateResponse{JobID: 789}, nil
				}

				ctx = provisioning.WithTenantStorageClasses(ctx, []v1alpha1.ResolvedStorageClass{
					{Name: "ceph-fast", Tier: "fast"},
					{Name: "ceph-default", Tier: "default"},
				})

				instance := &v1alpha1.ComputeInstance{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "test-resource",
						Namespace: "default",
					},
				}
				result, err := provider.TriggerProvision(ctx, instance)
				Expect(err).NotTo(HaveOccurred())
				Expect(result.JobID).To(Equal("789"))
			})

			It("should not inject tenant_storage_classes when context has no storage classes", func() {
				aapClient.launchJobTemplateFunc = func(ctx context.Context, req aap.LaunchJobTemplateRequest) (*aap.LaunchJobTemplateResponse, error) {
					edaEvent := req.ExtraVars["ansible_eda"].(map[string]any)["event"].(map[string]any)
					Expect(edaEvent).To(HaveKey("payload"))
					Expect(edaEvent).NotTo(HaveKey("tenant_storage_classes"))
					return &aap.LaunchJobTemplateResponse{JobID: 790}, nil
				}

				instance := &v1alpha1.ComputeInstance{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "test-resource",
						Namespace: "default",
					},
				}
				result, err := provider.TriggerProvision(ctx, instance)
				Expect(err).NotTo(HaveOccurred())
				Expect(result.JobID).To(Equal("790"))
			})
		})

		Context("when template not configured", func() {
			BeforeEach(func() {
				provider = provisioning.NewAAPProvider(aapClient, "", "deprovision-job")
			})

			It("should return error", func() {
				instance := &v1alpha1.ComputeInstance{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "test-resource",
						Namespace: "default",
					},
				}
				_, err := provider.TriggerProvision(ctx, instance)
				Expect(err).To(HaveOccurred())
				Expect(err.Error()).To(ContainSubstring("create template not configured"))
			})
		})

		Context("when template detection fails", func() {
			BeforeEach(func() {
				provider = provisioning.NewAAPProvider(aapClient, "provision-job", "deprovision-job")
				aapClient.getTemplateFunc = func(ctx context.Context, templateName string) (*aap.Template, error) {
					return nil, errors.New("template not found")
				}
			})

			It("should return error", func() {
				instance := &v1alpha1.ComputeInstance{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "test-resource",
						Namespace: "default",
					},
				}
				_, err := provider.TriggerProvision(ctx, instance)
				Expect(err).To(HaveOccurred())
				Expect(err.Error()).To(ContainSubstring("failed to get template"))
			})
		})

		Context("when job launch fails", func() {
			BeforeEach(func() {
				provider = provisioning.NewAAPProvider(aapClient, "provision-job", "deprovision-job")
				aapClient.getTemplateFunc = func(ctx context.Context, templateName string) (*aap.Template, error) {
					return &aap.Template{ID: 1, Name: templateName, Type: aap.TemplateTypeJob}, nil
				}
				aapClient.launchJobTemplateFunc = func(ctx context.Context, req aap.LaunchJobTemplateRequest) (*aap.LaunchJobTemplateResponse, error) {
					return nil, errors.New("AAP API error")
				}
			})

			It("should return error", func() {
				instance := &v1alpha1.ComputeInstance{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "test-resource",
						Namespace: "default",
					},
				}
				_, err := provider.TriggerProvision(ctx, instance)
				Expect(err).To(HaveOccurred())
				Expect(err.Error()).To(ContainSubstring("failed to launch job template"))
			})
		})
	})

	Describe("GetProvisionStatus", func() {
		BeforeEach(func() {
			provider = provisioning.NewAAPProvider(aapClient, "provision-job", "deprovision-job")
		})

		Context("when job is successful", func() {
			BeforeEach(func() {
				aapClient.getJobFunc = func(ctx context.Context, jobID string) (*aap.Job, error) {
					var id int
					if _, err := fmt.Sscanf(jobID, "%d", &id); err != nil {
						id = 789 // default
					}
					return &aap.Job{
						ID:       id,
						Status:   "successful",
						Started:  time.Date(2024, 1, 1, 12, 0, 0, 0, time.UTC),
						Finished: time.Date(2024, 1, 1, 12, 5, 0, 0, time.UTC),
					}, nil
				}
			})

			It("should return succeeded state", func() {
				instance := &v1alpha1.ComputeInstance{}
				status, err := provider.GetProvisionStatus(ctx, instance, "789")
				Expect(err).NotTo(HaveOccurred())
				Expect(status.JobID).To(Equal("789"))
				Expect(status.State).To(Equal(v1alpha1.JobStateSucceeded))
				Expect(status.Message).To(Equal("successful"))
			})
		})

		Context("when job is pending", func() {
			BeforeEach(func() {
				aapClient.getJobFunc = func(ctx context.Context, jobID string) (*aap.Job, error) {
					var id int
					if _, err := fmt.Sscanf(jobID, "%d", &id); err != nil {
						id = 789 // default
					}
					return &aap.Job{
						ID:     id,
						Status: "pending",
					}, nil
				}
			})

			It("should return pending state", func() {
				instance := &v1alpha1.ComputeInstance{}
				status, err := provider.GetProvisionStatus(ctx, instance, "789")
				Expect(err).NotTo(HaveOccurred())
				Expect(status.State).To(Equal(v1alpha1.JobStatePending))
			})
		})

		Context("when job is waiting", func() {
			BeforeEach(func() {
				aapClient.getJobFunc = func(ctx context.Context, jobID string) (*aap.Job, error) {
					var id int
					if _, err := fmt.Sscanf(jobID, "%d", &id); err != nil {
						id = 789 // default
					}
					return &aap.Job{
						ID:     id,
						Status: "waiting",
					}, nil
				}
			})

			It("should return waiting state", func() {
				instance := &v1alpha1.ComputeInstance{}
				status, err := provider.GetProvisionStatus(ctx, instance, "789")
				Expect(err).NotTo(HaveOccurred())
				Expect(status.State).To(Equal(v1alpha1.JobStateWaiting))
			})
		})

		Context("when job is running", func() {
			BeforeEach(func() {
				aapClient.getJobFunc = func(ctx context.Context, jobID string) (*aap.Job, error) {
					var id int
					if _, err := fmt.Sscanf(jobID, "%d", &id); err != nil {
						id = 789 // default
					}
					return &aap.Job{
						ID:      id,
						Status:  "running",
						Started: time.Date(2024, 1, 1, 12, 0, 0, 0, time.UTC),
					}, nil
				}
			})

			It("should return running state", func() {
				instance := &v1alpha1.ComputeInstance{}
				status, err := provider.GetProvisionStatus(ctx, instance, "789")
				Expect(err).NotTo(HaveOccurred())
				Expect(status.State).To(Equal(v1alpha1.JobStateRunning))
			})
		})

		Context("when job failed with traceback", func() {
			BeforeEach(func() {
				aapClient.getJobFunc = func(ctx context.Context, jobID string) (*aap.Job, error) {
					var id int
					if _, err := fmt.Sscanf(jobID, "%d", &id); err != nil {
						id = 789 // default
					}
					return &aap.Job{
						ID:              id,
						Status:          "failed",
						Started:         time.Date(2024, 1, 1, 12, 0, 0, 0, time.UTC),
						Finished:        time.Date(2024, 1, 1, 12, 1, 0, 0, time.UTC),
						ResultTraceback: "Error: Connection timeout",
					}, nil
				}
			})

			It("should return failed state with error details", func() {
				instance := &v1alpha1.ComputeInstance{}
				status, err := provider.GetProvisionStatus(ctx, instance, "789")
				Expect(err).NotTo(HaveOccurred())
				Expect(status.State).To(Equal(v1alpha1.JobStateFailed))
				Expect(status.ErrorDetails).To(Equal("Error: Connection timeout"))
			})
		})

		Context("when job has error status", func() {
			BeforeEach(func() {
				aapClient.getJobFunc = func(ctx context.Context, jobID string) (*aap.Job, error) {
					var id int
					if _, err := fmt.Sscanf(jobID, "%d", &id); err != nil {
						id = 789 // default
					}
					return &aap.Job{
						ID:       id,
						Status:   "error",
						Started:  time.Date(2024, 1, 1, 12, 0, 0, 0, time.UTC),
						Finished: time.Date(2024, 1, 1, 12, 1, 0, 0, time.UTC),
					}, nil
				}
			})

			It("should return failed state", func() {
				instance := &v1alpha1.ComputeInstance{}
				status, err := provider.GetProvisionStatus(ctx, instance, "789")
				Expect(err).NotTo(HaveOccurred())
				Expect(status.State).To(Equal(v1alpha1.JobStateFailed))
				Expect(status.Message).To(Equal("error"))
			})
		})

		Context("when job is canceled", func() {
			BeforeEach(func() {
				aapClient.getJobFunc = func(ctx context.Context, jobID string) (*aap.Job, error) {
					var id int
					if _, err := fmt.Sscanf(jobID, "%d", &id); err != nil {
						id = 789 // default
					}
					return &aap.Job{
						ID:       id,
						Status:   "canceled",
						Started:  time.Date(2024, 1, 1, 12, 0, 0, 0, time.UTC),
						Finished: time.Date(2024, 1, 1, 12, 3, 0, 0, time.UTC),
					}, nil
				}
			})

			It("should return canceled state", func() {
				instance := &v1alpha1.ComputeInstance{}
				status, err := provider.GetProvisionStatus(ctx, instance, "789")
				Expect(err).NotTo(HaveOccurred())
				Expect(status.State).To(Equal(v1alpha1.JobStateCanceled))
				Expect(status.Message).To(Equal("canceled"))
			})
		})

		Context("when job has unknown status", func() {
			BeforeEach(func() {
				aapClient.getJobFunc = func(ctx context.Context, jobID string) (*aap.Job, error) {
					var id int
					if _, err := fmt.Sscanf(jobID, "%d", &id); err != nil {
						id = 789 // default
					}
					return &aap.Job{
						ID:      id,
						Status:  "unknown_status",
						Started: time.Date(2024, 1, 1, 12, 0, 0, 0, time.UTC),
					}, nil
				}
			})

			It("should return unknown state", func() {
				instance := &v1alpha1.ComputeInstance{}
				status, err := provider.GetProvisionStatus(ctx, instance, "789")
				Expect(err).NotTo(HaveOccurred())
				Expect(status.State).To(Equal(v1alpha1.JobStateUnknown))
				Expect(status.Message).To(Equal("unknown_status"))
			})
		})

		Context("when job ID is invalid", func() {
			BeforeEach(func() {
				aapClient.getJobFunc = func(ctx context.Context, jobID string) (*aap.Job, error) {
					return nil, errors.New("received non-success status code 404: job not found")
				}
			})

			It("should return error", func() {
				instance := &v1alpha1.ComputeInstance{}
				_, err := provider.GetProvisionStatus(ctx, instance, "invalid")
				Expect(err).To(HaveOccurred())
				Expect(err.Error()).To(ContainSubstring("failed to get job"))
			})
		})

		Context("when AAP API fails", func() {
			BeforeEach(func() {
				aapClient.getJobFunc = func(ctx context.Context, jobID string) (*aap.Job, error) {
					return nil, errors.New("AAP connection error")
				}
			})

			It("should return error", func() {
				instance := &v1alpha1.ComputeInstance{}
				_, err := provider.GetProvisionStatus(ctx, instance, "789")
				Expect(err).To(HaveOccurred())
				Expect(err.Error()).To(ContainSubstring("failed to get job"))
			})
		})
	})

	Describe("TriggerDeprovision", func() {
		var instance *v1alpha1.ComputeInstance

		BeforeEach(func() {
			instance = &v1alpha1.ComputeInstance{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-instance",
					Namespace: "default",
				},
			}
		})

		Context("with job template", func() {
			BeforeEach(func() {
				provider = provisioning.NewAAPProvider(aapClient, "provision-job", "deprovision-job")
				aapClient.getTemplateFunc = func(ctx context.Context, templateName string) (*aap.Template, error) {
					return &aap.Template{ID: 1, Name: templateName, Type: aap.TemplateTypeJob}, nil
				}
				aapClient.launchJobTemplateFunc = func(ctx context.Context, req aap.LaunchJobTemplateRequest) (*aap.LaunchJobTemplateResponse, error) {
					Expect(req.TemplateName).To(Equal("deprovision-job"))
					return &aap.LaunchJobTemplateResponse{JobID: 999}, nil
				}
			})

			It("should launch job template and return job ID", func() {
				result, err := provider.TriggerDeprovision(ctx, instance, instance.Status.ProvisioningJobs)
				Expect(err).NotTo(HaveOccurred())
				Expect(result.Action).To(Equal(provisioning.DeprovisionTriggered))
				Expect(result.JobID).To(Equal("999"))
				Expect(result.BlockDeletionOnFailure).To(BeTrue())
			})
		})

		Context("when template not configured", func() {
			BeforeEach(func() {
				provider = provisioning.NewAAPProvider(aapClient, "provision-job", "")
			})

			It("should return error", func() {
				_, err := provider.TriggerDeprovision(ctx, instance, instance.Status.ProvisioningJobs)
				Expect(err).To(HaveOccurred())
				Expect(err.Error()).To(ContainSubstring("delete template not configured"))
			})
		})

		Context("when running AAP provision job must be cancelled first", func() {
			BeforeEach(func() {
				provider = provisioning.NewAAPProvider(aapClient, "provision-job", "deprovision-job")
				aapClient.getTemplateFunc = func(ctx context.Context, templateName string) (*aap.Template, error) {
					return &aap.Template{ID: 1, Name: templateName, Type: aap.TemplateTypeJob}, nil
				}
				instance.Status.Phase = v1alpha1.ComputeInstancePhaseStarting
				instance.Status.ProvisioningJobs = []v1alpha1.JobStatus{
					{
						JobID:     "9876",
						Type:      v1alpha1.JobTypeProvision,
						State:     v1alpha1.JobStateRunning,
						Timestamp: metav1.NewTime(time.Now().UTC().Add(-5 * time.Minute)),
					},
				}
				aapClient.getJobFunc = func(ctx context.Context, jobID string) (*aap.Job, error) {
					return &aap.Job{
						ID:       9876,
						Status:   "running",
						Started:  time.Now().UTC().Add(-5 * time.Minute),
						Finished: time.Time{},
					}, nil
				}
				aapClient.cancelJobFunc = func(ctx context.Context, jobID string) error {
					return nil
				}
			})

			It("should check AAP job status and cancel if running", func() {
				result, err := provider.TriggerDeprovision(ctx, instance, instance.Status.ProvisioningJobs)
				Expect(err).NotTo(HaveOccurred())
				Expect(result.Action).To(Equal(provisioning.DeprovisionWaiting))
				Expect(result.ProvisionJobStatus).NotTo(BeNil())
				Expect(result.ProvisionJobStatus.State).To(Equal(v1alpha1.JobStateRunning))
			})
		})

		Context("when cancel returns 405 because job already became terminal", func() {
			BeforeEach(func() {
				provider = provisioning.NewAAPProvider(aapClient, "provision-job", "deprovision-job")
				aapClient.getTemplateFunc = func(ctx context.Context, templateName string) (*aap.Template, error) {
					return &aap.Template{ID: 1, Name: templateName, Type: aap.TemplateTypeJob}, nil
				}
				aapClient.launchJobTemplateFunc = func(ctx context.Context, req aap.LaunchJobTemplateRequest) (*aap.LaunchJobTemplateResponse, error) {
					Expect(req.TemplateName).To(Equal("deprovision-job"))
					return &aap.LaunchJobTemplateResponse{JobID: 999}, nil
				}
				instance.Status.Phase = v1alpha1.ComputeInstancePhaseStarting
				instance.Status.ProvisioningJobs = []v1alpha1.JobStatus{
					{
						JobID:     "9876",
						Type:      v1alpha1.JobTypeProvision,
						State:     v1alpha1.JobStateRunning,
						Timestamp: metav1.NewTime(time.Now().UTC().Add(-5 * time.Minute)),
					},
				}
				aapClient.getJobFunc = func(ctx context.Context, jobID string) (*aap.Job, error) {
					return &aap.Job{
						ID:       9876,
						Status:   "running",
						Started:  time.Now().UTC().Add(-5 * time.Minute),
						Finished: time.Time{},
					}, nil
				}
				aapClient.cancelJobFunc = func(ctx context.Context, jobID string) error {
					return &aap.MethodNotAllowedError{Operation: "cancel job " + jobID}
				}
			})

			It("should proceed to deprovision immediately", func() {
				result, err := provider.TriggerDeprovision(ctx, instance, instance.Status.ProvisioningJobs)
				Expect(err).NotTo(HaveOccurred())
				Expect(result.Action).To(Equal(provisioning.DeprovisionTriggered))
				Expect(result.JobID).To(Equal("999"))
			})
		})
	})

	Describe("GetDeprovisionStatus", func() {
		BeforeEach(func() {
			provider = provisioning.NewAAPProvider(aapClient, "provision-job", "deprovision-job")
			aapClient.getJobFunc = func(ctx context.Context, jobID string) (*aap.Job, error) {
				var id int
				if _, err := fmt.Sscanf(jobID, "%d", &id); err != nil {
					id = 888 // default
				}
				return &aap.Job{
					ID:       id,
					Status:   "successful",
					Started:  time.Date(2024, 1, 1, 12, 0, 0, 0, time.UTC),
					Finished: time.Date(2024, 1, 1, 12, 3, 0, 0, time.UTC),
				}, nil
			}
		})

		It("should return job status", func() {
			instance := &v1alpha1.ComputeInstance{}
			status, err := provider.GetDeprovisionStatus(ctx, instance, "888")
			Expect(err).NotTo(HaveOccurred())
			Expect(status.JobID).To(Equal("888"))
			Expect(status.State).To(Equal(v1alpha1.JobStateSucceeded))
		})
	})

	Describe("Name", func() {
		It("should return provider name", func() {
			Expect(provider.Name()).To(Equal("aap"))
		})
	})

	Describe("NewAAPProviderWithPrefix", func() {
		BeforeEach(func() {
			provider = provisioning.NewAAPProviderWithPrefix(aapClient, "osac")
		})

		It("should derive provision template name from resource Kind", func() {
			aapClient.getTemplateFunc = func(ctx context.Context, templateName string) (*aap.Template, error) {
				Expect(templateName).To(Equal("osac-create-virtual-network"))
				return &aap.Template{ID: 1, Name: templateName, Type: aap.TemplateTypeJob}, nil
			}
			aapClient.launchJobTemplateFunc = func(ctx context.Context, req aap.LaunchJobTemplateRequest) (*aap.LaunchJobTemplateResponse, error) {
				Expect(req.TemplateName).To(Equal("osac-create-virtual-network"))
				return &aap.LaunchJobTemplateResponse{JobID: 100}, nil
			}

			vnet := &v1alpha1.VirtualNetwork{
				ObjectMeta: metav1.ObjectMeta{Name: "test-vnet", Namespace: "default"},
			}
			vnet.SetGroupVersionKind(v1alpha1.GroupVersion.WithKind("VirtualNetwork"))

			result, err := provider.TriggerProvision(ctx, vnet)
			Expect(err).NotTo(HaveOccurred())
			Expect(result.JobID).To(Equal("100"))
		})

		It("should derive deprovision template name from resource Kind", func() {
			aapClient.getTemplateFunc = func(ctx context.Context, templateName string) (*aap.Template, error) {
				Expect(templateName).To(Equal("osac-delete-subnet"))
				return &aap.Template{ID: 2, Name: templateName, Type: aap.TemplateTypeJob}, nil
			}
			aapClient.launchJobTemplateFunc = func(ctx context.Context, req aap.LaunchJobTemplateRequest) (*aap.LaunchJobTemplateResponse, error) {
				Expect(req.TemplateName).To(Equal("osac-delete-subnet"))
				return &aap.LaunchJobTemplateResponse{JobID: 200}, nil
			}

			subnet := &v1alpha1.Subnet{
				ObjectMeta: metav1.ObjectMeta{Name: "test-subnet", Namespace: "default"},
			}
			subnet.SetGroupVersionKind(v1alpha1.GroupVersion.WithKind("Subnet"))

			result, err := provider.TriggerDeprovision(ctx, subnet, nil)
			Expect(err).NotTo(HaveOccurred())
			Expect(result.Action).To(Equal(provisioning.DeprovisionTriggered))
			Expect(result.JobID).To(Equal("200"))
		})

		It("should derive security-group template names correctly", func() {
			aapClient.getTemplateFunc = func(ctx context.Context, templateName string) (*aap.Template, error) {
				Expect(templateName).To(Equal("osac-create-security-group"))
				return &aap.Template{ID: 3, Name: templateName, Type: aap.TemplateTypeJob}, nil
			}
			aapClient.launchJobTemplateFunc = func(ctx context.Context, req aap.LaunchJobTemplateRequest) (*aap.LaunchJobTemplateResponse, error) {
				return &aap.LaunchJobTemplateResponse{JobID: 300}, nil
			}

			sg := &v1alpha1.SecurityGroup{
				ObjectMeta: metav1.ObjectMeta{Name: "test-sg", Namespace: "default"},
			}
			sg.SetGroupVersionKind(v1alpha1.GroupVersion.WithKind("SecurityGroup"))

			result, err := provider.TriggerProvision(ctx, sg)
			Expect(err).NotTo(HaveOccurred())
			Expect(result.JobID).To(Equal("300"))
		})

		It("should return error when resource has no Kind set", func() {
			instance := &v1alpha1.ComputeInstance{
				ObjectMeta: metav1.ObjectMeta{Name: "test", Namespace: "default"},
			}
			_, err := provider.TriggerProvision(ctx, instance)
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("resource has no Kind set"))
		})
	})

	Describe("Multi-resource type support", func() {
		BeforeEach(func() {
			provider = provisioning.NewAAPProvider(aapClient, "provision-job", "deprovision-job")
			aapClient.getTemplateFunc = func(ctx context.Context, templateName string) (*aap.Template, error) {
				return &aap.Template{ID: 1, Name: templateName, Type: aap.TemplateTypeJob}, nil
			}
			aapClient.launchJobTemplateFunc = func(ctx context.Context, req aap.LaunchJobTemplateRequest) (*aap.LaunchJobTemplateResponse, error) {
				return &aap.LaunchJobTemplateResponse{JobID: 100}, nil
			}
		})

		It("should trigger provision for ClusterOrder", func() {
			clusterOrder := &v1alpha1.ClusterOrder{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-cluster-order",
					Namespace: "default",
				},
				Spec: v1alpha1.ClusterOrderSpec{
					TemplateID: "cluster-template",
				},
			}
			result, err := provider.TriggerProvision(ctx, clusterOrder)
			Expect(err).NotTo(HaveOccurred())
			Expect(result.JobID).To(Equal("100"))
			Expect(result.InitialState).To(Equal(v1alpha1.JobStatePending))
		})

		It("should trigger deprovision for ClusterOrder", func() {
			clusterOrder := &v1alpha1.ClusterOrder{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-cluster-order",
					Namespace: "default",
				},
				Status: v1alpha1.ClusterOrderStatus{
					Phase: v1alpha1.ClusterOrderPhaseReady,
				},
			}
			result, err := provider.TriggerDeprovision(ctx, clusterOrder, nil)
			Expect(err).NotTo(HaveOccurred())
			Expect(result.Action).To(Equal(provisioning.DeprovisionTriggered))
			Expect(result.JobID).To(Equal("100"))
		})

		It("should get provision status for ClusterOrder", func() {
			aapClient.getJobFunc = func(ctx context.Context, jobID string) (*aap.Job, error) {
				return &aap.Job{
					ID:     42,
					Status: "successful",
				}, nil
			}
			clusterOrder := &v1alpha1.ClusterOrder{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-cluster-order",
					Namespace: "default",
				},
			}
			status, err := provider.GetProvisionStatus(ctx, clusterOrder, "42")
			Expect(err).NotTo(HaveOccurred())
			Expect(status.State).To(Equal(v1alpha1.JobStateSucceeded))
		})

	})

	Describe("ExtraVars admin kubeconfig injection", func() {
		BeforeEach(func() {
			provider = provisioning.NewAAPProvider(aapClient, "provision-job", "deprovision-job")
			aapClient.getTemplateFunc = func(ctx context.Context, templateName string) (*aap.Template, error) {
				return &aap.Template{ID: 1, Name: templateName, Type: aap.TemplateTypeJob}, nil
			}
		})

		It("should include admin_kubeconfig in event when set in context", func() {
			kubeconfig := "apiVersion: v1\nclusters: []\n"
			ctx = provisioning.WithAdminKubeconfig(ctx, kubeconfig)

			aapClient.launchJobTemplateFunc = func(ctx context.Context, req aap.LaunchJobTemplateRequest) (*aap.LaunchJobTemplateResponse, error) {
				edaEvent := req.ExtraVars["ansible_eda"].(map[string]any)["event"].(map[string]any)
				Expect(edaEvent).To(HaveKeyWithValue("admin_kubeconfig", kubeconfig))
				return &aap.LaunchJobTemplateResponse{JobID: 100}, nil
			}

			instance := &v1alpha1.ComputeInstance{
				ObjectMeta: metav1.ObjectMeta{Name: "test", Namespace: "default"},
			}
			_, err := provider.TriggerProvision(ctx, instance)
			Expect(err).NotTo(HaveOccurred())
		})

		It("should omit admin_kubeconfig from event when not set in context", func() {
			aapClient.launchJobTemplateFunc = func(ctx context.Context, req aap.LaunchJobTemplateRequest) (*aap.LaunchJobTemplateResponse, error) {
				edaEvent := req.ExtraVars["ansible_eda"].(map[string]any)["event"].(map[string]any)
				Expect(edaEvent).NotTo(HaveKey("admin_kubeconfig"))
				return &aap.LaunchJobTemplateResponse{JobID: 101}, nil
			}

			instance := &v1alpha1.ComputeInstance{
				ObjectMeta: metav1.ObjectMeta{Name: "test", Namespace: "default"},
			}
			_, err := provider.TriggerProvision(ctx, instance)
			Expect(err).NotTo(HaveOccurred())
		})
	})

	Describe("ExtraVars storage tier definitions injection", func() {
		BeforeEach(func() {
			provider = provisioning.NewAAPProvider(aapClient, "provision-job", "deprovision-job")
			aapClient.getTemplateFunc = func(ctx context.Context, templateName string) (*aap.Template, error) {
				return &aap.Template{ID: 1, Name: templateName, Type: aap.TemplateTypeJob}, nil
			}
		})

		It("should shape storage_tier_definitions per the storage_provider role's argument_specs", func() {
			ctx = provisioning.WithStorageTierDefinitions(ctx, []provisioning.TierDefinition{
				{
					Name:      "fast",
					Protocol:  "nfs",
					Provider:  "vast",
					BackendID: "backend-1",
					QosLimits: provisioning.TierQosLimits{MaxReadBandwidthMBs: 100, MaxWriteBandwidthMBs: 200},
					QuotaGiB:  500,
				},
			})

			aapClient.launchJobTemplateFunc = func(ctx context.Context, req aap.LaunchJobTemplateRequest) (*aap.LaunchJobTemplateResponse, error) {
				edaEvent := req.ExtraVars["ansible_eda"].(map[string]any)["event"].(map[string]any)
				Expect(edaEvent).To(HaveKey("storage_tier_definitions"))
				tiers := edaEvent["storage_tier_definitions"].([]map[string]any)
				Expect(tiers).To(HaveLen(1))
				Expect(tiers[0]).To(Equal(map[string]any{
					"name":       "fast",
					"protocol":   "nfs",
					"provider":   "vast",
					"backend_id": "backend-1",
					"qos_limits": map[string]any{
						"static_limits": map[string]any{
							"max_reads_bw_mbps":  int32(100),
							"max_writes_bw_mbps": int32(200),
						},
					},
					"quota_bytes": int64(500 * 1073741824),
				}))
				Expect(tiers[0]).NotTo(HaveKey("qos_policy"))
				return &aap.LaunchJobTemplateResponse{JobID: 111}, nil
			}

			instance := &v1alpha1.ComputeInstance{
				ObjectMeta: metav1.ObjectMeta{Name: "test", Namespace: "default"},
			}
			_, err := provider.TriggerProvision(ctx, instance)
			Expect(err).NotTo(HaveOccurred())
		})

		It("should omit storage_tier_definitions when not set in context", func() {
			aapClient.launchJobTemplateFunc = func(ctx context.Context, req aap.LaunchJobTemplateRequest) (*aap.LaunchJobTemplateResponse, error) {
				edaEvent := req.ExtraVars["ansible_eda"].(map[string]any)["event"].(map[string]any)
				Expect(edaEvent).NotTo(HaveKey("storage_tier_definitions"))
				return &aap.LaunchJobTemplateResponse{JobID: 112}, nil
			}

			instance := &v1alpha1.ComputeInstance{
				ObjectMeta: metav1.ObjectMeta{Name: "test", Namespace: "default"},
			}
			_, err := provider.TriggerProvision(ctx, instance)
			Expect(err).NotTo(HaveOccurred())
		})
	})

	Describe("ExtraVars storage backend connections injection", func() {
		BeforeEach(func() {
			provider = provisioning.NewAAPProvider(aapClient, "provision-job", "deprovision-job")
			aapClient.getTemplateFunc = func(ctx context.Context, templateName string) (*aap.Template, error) {
				return &aap.Template{ID: 1, Name: templateName, Type: aap.TemplateTypeJob}, nil
			}
		})

		It("should shape storage_backend_connections keyed by backend_id", func() {
			ctx = provisioning.WithStorageBackendConnections(ctx, map[string]provisioning.BackendConnection{
				"backend-1": {Endpoint: "https://vast.example.com", Username: testBackendUsername, Password: testBackendPassword},
			})

			aapClient.launchJobTemplateFunc = func(ctx context.Context, req aap.LaunchJobTemplateRequest) (*aap.LaunchJobTemplateResponse, error) {
				edaEvent := req.ExtraVars["ansible_eda"].(map[string]any)["event"].(map[string]any)
				Expect(edaEvent).To(HaveKey("storage_backend_connections"))
				conns := edaEvent["storage_backend_connections"].(map[string]map[string]any)
				Expect(conns).To(Equal(map[string]map[string]any{
					"backend-1": {
						"endpoint": "https://vast.example.com",
						"username": testBackendUsername,
						"password": testBackendPassword,
					},
				}))
				return &aap.LaunchJobTemplateResponse{JobID: 121}, nil
			}

			instance := &v1alpha1.ComputeInstance{
				ObjectMeta: metav1.ObjectMeta{Name: "test", Namespace: "default"},
			}
			_, err := provider.TriggerProvision(ctx, instance)
			Expect(err).NotTo(HaveOccurred())
		})

		It("should omit storage_backend_connections when not set in context", func() {
			aapClient.launchJobTemplateFunc = func(ctx context.Context, req aap.LaunchJobTemplateRequest) (*aap.LaunchJobTemplateResponse, error) {
				edaEvent := req.ExtraVars["ansible_eda"].(map[string]any)["event"].(map[string]any)
				Expect(edaEvent).NotTo(HaveKey("storage_backend_connections"))
				return &aap.LaunchJobTemplateResponse{JobID: 122}, nil
			}

			instance := &v1alpha1.ComputeInstance{
				ObjectMeta: metav1.ObjectMeta{Name: "test", Namespace: "default"},
			}
			_, err := provider.TriggerProvision(ctx, instance)
			Expect(err).NotTo(HaveOccurred())
		})
	})
})

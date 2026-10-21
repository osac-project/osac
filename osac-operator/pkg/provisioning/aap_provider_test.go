package provisioning_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/osac-project/osac/osac-operator/api/v1alpha1"
	"github.com/osac-project/osac/osac-operator/pkg/aap"
	"github.com/osac-project/osac/osac-operator/pkg/provisioning"
)

// mockAAPClient is a test double for aap.Client
type mockAAPClient struct {
	getTemplateFunc            func(ctx context.Context, templateName string) (*aap.Template, error)
	launchJobTemplateFunc      func(ctx context.Context, req aap.LaunchJobTemplateRequest) (*aap.LaunchJobTemplateResponse, error)
	launchWorkflowTemplateFunc func(ctx context.Context, req aap.LaunchWorkflowTemplateRequest) (*aap.LaunchWorkflowTemplateResponse, error)
	getJobFunc                 func(ctx context.Context, jobID string) (*aap.Job, error)
	listJobsFunc               func(ctx context.Context, templateID int) ([]aap.Job, error)
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

func (m *mockAAPClient) ListJobs(ctx context.Context, templateID int) ([]aap.Job, error) {
	if m.listJobsFunc != nil {
		return m.listJobsFunc(ctx, templateID)
	}
	return nil, nil
}

func (m *mockAAPClient) CancelJob(ctx context.Context, jobID string) error {
	if m.cancelJobFunc != nil {
		return m.cancelJobFunc(ctx, jobID)
	}
	return nil
}

func extractJobVarsResource(extraVars map[string]any) map[string]any {
	return extraVars["osac_job_vars"].(map[string]any)["resource"].(map[string]any)
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
					Expect(req.ExtraVars).To(HaveKey("osac_job_vars"))
					payload := extractJobVarsResource(req.ExtraVars)
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
					Expect(req.ExtraVars).To(HaveKey("osac_job_vars"))
					payload := extractJobVarsResource(req.ExtraVars)
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

		It("marks workflow launches sensitive when admin kubeconfig is present", func() {
			provider = provisioning.NewAAPProvider(aapClient, "provision-workflow", "deprovision-workflow")
			aapClient.getTemplateFunc = func(ctx context.Context, templateName string) (*aap.Template, error) {
				return &aap.Template{ID: 2, Name: templateName, Type: aap.TemplateTypeWorkflow}, nil
			}
			kubeconfig := "apiVersion: v1\nclusters: []\n"
			ctx = provisioning.WithAdminKubeconfig(ctx, kubeconfig)
			aapClient.launchWorkflowTemplateFunc = func(ctx context.Context, req aap.LaunchWorkflowTemplateRequest) (*aap.LaunchWorkflowTemplateResponse, error) {
				Expect(req.Sensitive).To(BeTrue())
				return &aap.LaunchWorkflowTemplateResponse{JobID: 457}, nil
			}

			instance := &v1alpha1.ComputeInstance{
				ObjectMeta: metav1.ObjectMeta{Name: "test-resource", Namespace: "default"},
			}
			_, err := provider.TriggerProvision(ctx, instance)
			Expect(err).NotTo(HaveOccurred())
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
					jobVars := req.ExtraVars["osac_job_vars"].(map[string]any)
					Expect(jobVars).To(HaveKey("resource"))
					Expect(jobVars).To(HaveKey("tenant_storage_classes"))
					scList := jobVars["tenant_storage_classes"].([]map[string]string)
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
					jobVars := req.ExtraVars["osac_job_vars"].(map[string]any)
					Expect(jobVars).To(HaveKey("resource"))
					Expect(jobVars).NotTo(HaveKey("tenant_storage_classes"))
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

	Describe("TriggerProvisionWithExtraVars", func() {
		BeforeEach(func() {
			provider = provisioning.NewAAPProvider(aapClient, "provision-job", "deprovision-job")
		})

		It("merges inherited outputs into the AAP request without replacing resource vars", func() {
			var launchedExtraVars map[string]any
			aapClient.launchJobTemplateFunc = func(_ context.Context, req aap.LaunchJobTemplateRequest) (*aap.LaunchJobTemplateResponse, error) {
				launchedExtraVars = req.ExtraVars
				return &aap.LaunchJobTemplateResponse{JobID: 123}, nil
			}

			resource := &v1alpha1.ComputeInstance{ObjectMeta: metav1.ObjectMeta{Name: "vm", Namespace: "tenant"}}
			inherited := map[string]any{"l2_vni": 14, "l3_vni": 11}
			result, err := provider.TriggerProvisionWithExtraVars(ctx, resource, inherited)

			Expect(err).NotTo(HaveOccurred())
			Expect(result.JobID).To(Equal("123"))
			Expect(launchedExtraVars).To(HaveKeyWithValue("l2_vni", 14))
			Expect(launchedExtraVars).To(HaveKeyWithValue("l3_vni", 11))
			Expect(launchedExtraVars).To(HaveKey("osac_job_vars"))
			Expect(inherited).To(Equal(map[string]any{"l2_vni": 14, "l3_vni": 11}))
		})

		It("prefers inherited outputs when a key conflicts with generated vars", func() {
			var launchedExtraVars map[string]any
			aapClient.launchJobTemplateFunc = func(_ context.Context, req aap.LaunchJobTemplateRequest) (*aap.LaunchJobTemplateResponse, error) {
				launchedExtraVars = req.ExtraVars
				return &aap.LaunchJobTemplateResponse{JobID: 123}, nil
			}

			resource := &v1alpha1.ComputeInstance{ObjectMeta: metav1.ObjectMeta{Name: "vm", Namespace: "tenant"}}
			inherited := map[string]any{"osac_job_vars": map[string]any{"source": "fabric"}}
			_, err := provider.TriggerProvisionWithExtraVars(ctx, resource, inherited)

			Expect(err).NotTo(HaveOccurred())
			Expect(launchedExtraVars).To(HaveKeyWithValue("osac_job_vars", inherited["osac_job_vars"]))
			Expect(inherited).To(Equal(map[string]any{"osac_job_vars": map[string]any{"source": "fabric"}}))
		})
	})

	Describe("TriggerProvision for add-on operators", func() {
		It("launches the dedicated template with the operator and admin kubeconfig", func() {
			provider = provisioning.NewAAPProvider(aapClient, "osac-install-addon-operator", "")
			ctx = provisioning.WithAdminKubeconfig(ctx, "apiVersion: v1\nclusters: []\n")
			ctx = provisioning.WithAddOnOperatorName(ctx, "cert-manager")
			aapClient.getTemplateFunc = func(ctx context.Context, templateName string) (*aap.Template, error) {
				Expect(templateName).To(Equal("osac-install-addon-operator"))
				return &aap.Template{ID: 7, Name: templateName, Type: aap.TemplateTypeJob}, nil
			}
			aapClient.launchJobTemplateFunc = func(ctx context.Context, req aap.LaunchJobTemplateRequest) (*aap.LaunchJobTemplateResponse, error) {
				Expect(req.TemplateName).To(Equal("osac-install-addon-operator"))
				jobVars := req.ExtraVars["osac_job_vars"].(map[string]any)
				Expect(jobVars).To(HaveKeyWithValue("addon_operator_name", "cert_manager"))
				Expect(jobVars).To(HaveKeyWithValue("admin_kubeconfig", "apiVersion: v1\nclusters: []\n"))
				resource := jobVars["resource"].(map[string]any)
				Expect(resource["metadata"].(map[string]any)).To(HaveKeyWithValue("name", "test-order"))
				return &aap.LaunchJobTemplateResponse{JobID: 707}, nil
			}

			order := &v1alpha1.ClusterOrder{
				ObjectMeta: metav1.ObjectMeta{Name: "test-order", Namespace: "default"},
			}
			result, err := provider.TriggerProvision(ctx, order)
			Expect(err).NotTo(HaveOccurred())
			Expect(result.JobID).To(Equal("707"))
			Expect(result.InitialState).To(Equal(v1alpha1.JobStatePending))
			Expect(result.Message).To(Equal("Provisioning job triggered"))
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

			It("does not treat launch extra vars as job output", func() {
				aapClient.getJobFunc = func(_ context.Context, jobID string) (*aap.Job, error) {
					return &aap.Job{
						ID:        789,
						Status:    "successful",
						ExtraVars: "{",
					}, nil
				}

				status, err := provider.GetProvisionStatus(ctx, &v1alpha1.ComputeInstance{}, "789")
				Expect(err).NotTo(HaveOccurred())
				Expect(status.State).To(Equal(v1alpha1.JobStateSucceeded))
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

	Describe("GetProvisionStatusWithExtraVars", func() {
		BeforeEach(func() {
			provider = provisioning.NewAAPProvider(aapClient, "provision-job", "deprovision-job")
		})

		It("returns the successful job artifacts as normalized extra vars", func() {
			aapClient.getJobFunc = func(_ context.Context, jobID string) (*aap.Job, error) {
				Expect(jobID).To(Equal("789"))
				return &aap.Job{
					ID:        789,
					Status:    "successful",
					ExtraVars: `{"osac_job_vars":{"resource":{"metadata":{"uid":"eip-uid"}}}}`,
					Artifacts: []byte(`{"l2_vni":14,"l3_vni":11}`),
				}, nil
			}

			status, err := provider.GetProvisionStatusWithExtraVars(ctx, &v1alpha1.Subnet{}, "789")

			Expect(err).NotTo(HaveOccurred())
			Expect(status.State).To(Equal(v1alpha1.JobStateSucceeded))
			Expect(status.InputExtraVars).To(HaveKey("osac_job_vars"))
			Expect(status.ExtraVars).To(HaveKeyWithValue("l2_vni", float64(14)))
			Expect(status.ExtraVars).To(HaveKeyWithValue("l3_vni", float64(11)))

			vnis, err := provisioning.ParseFabricVNIs(status.ExtraVars)
			Expect(err).NotTo(HaveOccurred())
			Expect(*vnis.L2VNI).To(Equal(int32(14)))
			Expect(*vnis.L3VNI).To(Equal(int32(11)))
		})

		It("returns inputs when the successful job has no artifacts", func() {
			aapClient.getJobFunc = func(_ context.Context, _ string) (*aap.Job, error) {
				return &aap.Job{
					Status:    "successful",
					ExtraVars: `{"osac_job_vars":{"external_ip_pool":{"kubernetes_uid":"pool-uid"}}}`,
				}, nil
			}

			status, err := provider.GetProvisionStatusWithExtraVars(ctx, &v1alpha1.Subnet{}, "789")

			Expect(err).NotTo(HaveOccurred())
			Expect(status.InputExtraVars).To(HaveKey("osac_job_vars"))
			Expect(status.ExtraVars).To(BeNil())
		})

		It("returns a sanitized error for malformed submitted inputs", func() {
			aapClient.getJobFunc = func(_ context.Context, _ string) (*aap.Job, error) {
				return &aap.Job{
					Status:    "successful",
					ExtraVars: `{"token":"sensitive-fixture-value"`,
				}, nil
			}

			status, err := provider.GetProvisionStatusWithExtraVars(ctx, &v1alpha1.ExternalIP{}, "789")

			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("job 789"))
			Expect(err.Error()).NotTo(ContainSubstring("sensitive-fixture-value"))
			Expect(status.State).To(Equal(v1alpha1.JobStateSucceeded))
		})

		It("returns an error when the AAP client cannot fetch the job", func() {
			aapClient.getJobFunc = func(_ context.Context, _ string) (*aap.Job, error) {
				return nil, errors.New("AAP connection error")
			}

			_, err := provider.GetProvisionStatusWithExtraVars(ctx, &v1alpha1.Subnet{}, "789")

			Expect(err).To(MatchError(ContainSubstring("failed to get job")))
		})

		It("returns a job-specific error for malformed artifacts", func() {
			aapClient.getJobFunc = func(_ context.Context, _ string) (*aap.Job, error) {
				return &aap.Job{Status: "successful", Artifacts: []byte(`{"l2_vni":`)}, nil
			}

			status, err := provider.GetProvisionStatusWithExtraVars(ctx, &v1alpha1.Subnet{}, "789")

			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("job 789"))
			Expect(status.State).To(Equal(v1alpha1.JobStateSucceeded))
		})
	})

	Describe("TriggerDeprovision", func() {
		var instance *v1alpha1.ComputeInstance
		var statusAfterCancel string
		var provisionJobStatusCalls int
		var deprovisionLaunchCalls int

		BeforeEach(func() {
			instance = &v1alpha1.ComputeInstance{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-instance",
					Namespace: "default",
				},
			}
		})

		It("reuses the AAP job associated with a persisted launch claim", func() {
			provider = provisioning.NewAAPProvider(aapClient, "provision-job", "deprovision-job")
			launchCalls := 0
			aapClient.getTemplateFunc = func(_ context.Context, templateName string) (*aap.Template, error) {
				return &aap.Template{ID: 11, Name: templateName, Type: aap.TemplateTypeJob}, nil
			}
			aapClient.listJobsFunc = func(_ context.Context, templateID int) ([]aap.Job, error) {
				Expect(templateID).To(Equal(11))
				return []aap.Job{{
					ID:        876,
					Status:    "running",
					ExtraVars: `{"osac_job_launch_id":"claim-deprovision-1"}`,
				}}, nil
			}
			aapClient.launchJobTemplateFunc = func(_ context.Context, _ aap.LaunchJobTemplateRequest) (*aap.LaunchJobTemplateResponse, error) {
				launchCalls++
				return &aap.LaunchJobTemplateResponse{JobID: 999}, nil
			}

			resource := &v1alpha1.ExternalIPAttachment{ObjectMeta: metav1.ObjectMeta{Name: "attachment", Namespace: "default"}}
			claimCtx := provisioning.WithAAPJobLaunchID(ctx, "claim-deprovision-1")
			result, err := provider.TriggerDeprovision(claimCtx, resource, nil)

			Expect(err).NotTo(HaveOccurred())
			Expect(result.Action).To(Equal(provisioning.DeprovisionTriggered))
			Expect(result.JobID).To(Equal("876"))
			Expect(launchCalls).To(BeZero())
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
				deprovisionLaunchCalls = 0
				provisionJobStatus := "running"
				aapClient.getTemplateFunc = func(ctx context.Context, templateName string) (*aap.Template, error) {
					return &aap.Template{ID: 1, Name: templateName, Type: aap.TemplateTypeJob}, nil
				}
				aapClient.launchJobTemplateFunc = func(ctx context.Context, req aap.LaunchJobTemplateRequest) (*aap.LaunchJobTemplateResponse, error) {
					deprovisionLaunchCalls++
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
					finished := time.Time{}
					if v1alpha1.JobState(provisionJobStatus).IsTerminal() {
						finished = time.Now().UTC()
					}
					return &aap.Job{
						ID:       9876,
						Status:   provisionJobStatus,
						Started:  time.Now().UTC().Add(-5 * time.Minute),
						Finished: finished,
					}, nil
				}
				aapClient.cancelJobFunc = func(ctx context.Context, jobID string) error {
					provisionJobStatus = "canceled"
					return nil
				}
			})

			It("should check AAP job status and cancel if running", func() {
				result, err := provider.TriggerDeprovision(ctx, instance, instance.Status.ProvisioningJobs)
				Expect(err).NotTo(HaveOccurred())
				Expect(result.Action).To(Equal(provisioning.DeprovisionWaiting))
				Expect(result.ProvisionJobStatus).NotTo(BeNil())
				Expect(result.ProvisionJobStatus.State).To(Equal(v1alpha1.JobStateRunning))
				Expect(deprovisionLaunchCalls).To(BeZero())

				result, err = provider.TriggerDeprovision(ctx, instance, instance.Status.ProvisioningJobs)
				Expect(err).NotTo(HaveOccurred())
				Expect(result.Action).To(Equal(provisioning.DeprovisionTriggered))
				Expect(result.JobID).To(Equal("999"))
				Expect(deprovisionLaunchCalls).To(Equal(1))
			})
		})

		Context("when cancel returns 405 because job already became terminal", func() {
			BeforeEach(func() {
				provider = provisioning.NewAAPProvider(aapClient, "provision-job", "deprovision-job")
				statusAfterCancel = "successful"
				provisionJobStatusCalls = 0
				deprovisionLaunchCalls = 0
				aapClient.getTemplateFunc = func(ctx context.Context, templateName string) (*aap.Template, error) {
					return &aap.Template{ID: 1, Name: templateName, Type: aap.TemplateTypeJob}, nil
				}
				aapClient.launchJobTemplateFunc = func(ctx context.Context, req aap.LaunchJobTemplateRequest) (*aap.LaunchJobTemplateResponse, error) {
					deprovisionLaunchCalls++
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
					provisionJobStatusCalls++
					status := "running"
					if provisionJobStatusCalls > 1 {
						status = statusAfterCancel
					}
					finished := time.Time{}
					if status == "successful" {
						finished = time.Now().UTC()
					}
					return &aap.Job{
						ID:       9876,
						Status:   status,
						Started:  time.Now().UTC().Add(-5 * time.Minute),
						Finished: finished,
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
				Expect(provisionJobStatusCalls).To(Equal(2))
				Expect(deprovisionLaunchCalls).To(Equal(1))
			})

			It("waits when cancel returns 405 but the fresh provider status is still running", func() {
				statusAfterCancel = "running"

				result, err := provider.TriggerDeprovision(ctx, instance, instance.Status.ProvisioningJobs)
				Expect(err).NotTo(HaveOccurred())
				Expect(result.Action).To(Equal(provisioning.DeprovisionWaiting))
				Expect(result.ProvisionJobStatus).NotTo(BeNil())
				Expect(result.ProvisionJobStatus.State).To(Equal(v1alpha1.JobStateRunning))
				Expect(provisionJobStatusCalls).To(Equal(2))
				Expect(deprovisionLaunchCalls).To(BeZero())
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

		It("returns deprovision job artifacts", func() {
			aapClient.getJobFunc = func(_ context.Context, jobID string) (*aap.Job, error) {
				return &aap.Job{
					ID:        888,
					Status:    "successful",
					ExtraVars: `{"osac_job_vars":{"resource":{"metadata":{"uid":"eip-uid"}}}}`,
					Artifacts: []byte(`{"osac_result":{"schemaVersion":"v1"}}`),
				}, nil
			}
			status, err := provider.GetDeprovisionStatusWithExtraVars(ctx, &v1alpha1.ExternalIP{}, "888")
			Expect(err).NotTo(HaveOccurred())
			Expect(status.JobID).To(Equal("888"))
			Expect(status.InputExtraVars).To(HaveKey("osac_job_vars"))
			Expect(status.ExtraVars).To(HaveKey("osac_result"))
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
				jobVars := req.ExtraVars["osac_job_vars"].(map[string]any)
				Expect(jobVars).To(HaveKeyWithValue("admin_kubeconfig", kubeconfig))
				Expect(req.Sensitive).To(BeTrue())
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
				jobVars := req.ExtraVars["osac_job_vars"].(map[string]any)
				Expect(jobVars).NotTo(HaveKey("admin_kubeconfig"))
				return &aap.LaunchJobTemplateResponse{JobID: 101}, nil
			}

			instance := &v1alpha1.ComputeInstance{
				ObjectMeta: metav1.ObjectMeta{Name: "test", Namespace: "default"},
			}
			_, err := provider.TriggerProvision(ctx, instance)
			Expect(err).NotTo(HaveOccurred())
		})
	})

	Describe("ExtraVars Subnet parent VirtualNetwork injection", func() {
		BeforeEach(func() {
			provider = provisioning.NewAAPProvider(aapClient, "provision-job", "deprovision-job")
			aapClient.getTemplateFunc = func(ctx context.Context, templateName string) (*aap.Template, error) {
				return &aap.Template{ID: 1, Name: templateName, Type: aap.TemplateTypeJob}, nil
			}
		})

		It("should include the resolved parent identity in the Subnet job payload", func() {
			parent := provisioning.SubnetParentVirtualNetwork{
				FulfillmentID: "44444444-4444-4444-8444-444444444444",
				KubernetesUID: "55555555-5555-4555-8555-555555555555",
				TenantID:      "tenant-a",
				Phase:         "Ready",
			}
			ctx = provisioning.WithSubnetParentVirtualNetwork(ctx, parent)

			aapClient.launchJobTemplateFunc = func(ctx context.Context, req aap.LaunchJobTemplateRequest) (*aap.LaunchJobTemplateResponse, error) {
				jobVars := req.ExtraVars["osac_job_vars"].(map[string]any)
				Expect(jobVars["parent_virtual_network"]).To(Equal(map[string]any{
					"fulfillment_id": parent.FulfillmentID,
					"kubernetes_uid": parent.KubernetesUID,
					"tenant_id":      parent.TenantID,
					"phase":          parent.Phase,
				}))
				return &aap.LaunchJobTemplateResponse{JobID: 102}, nil
			}

			instance := &v1alpha1.ComputeInstance{
				ObjectMeta: metav1.ObjectMeta{Name: "test", Namespace: "default"},
			}
			_, err := provider.TriggerProvision(ctx, instance)
			Expect(err).NotTo(HaveOccurred())
		})

		It("includes the selected ExternalIPPool identity in the job payload", func() {
			pool := provisioning.ExternalIPPoolContext{
				FulfillmentID: "44444444-4444-4444-8444-444444444444",
				KubernetesUID: "55555555-5555-4555-8555-555555555555",
				TenantID:      "tenant-a",
				CIDR:          "198.51.100.0/24",
				Phase:         "Ready",
			}
			ctx = provisioning.WithExternalIPPool(ctx, pool)
			aapClient.launchJobTemplateFunc = func(_ context.Context, req aap.LaunchJobTemplateRequest) (*aap.LaunchJobTemplateResponse, error) {
				jobVars := req.ExtraVars["osac_job_vars"].(map[string]any)
				Expect(jobVars["external_ip_pool"]).To(Equal(map[string]any{
					"fulfillment_id": pool.FulfillmentID,
					"kubernetes_uid": pool.KubernetesUID,
					"tenant_id":      pool.TenantID,
					"cidr":           pool.CIDR,
					"phase":          pool.Phase,
				}))
				return &aap.LaunchJobTemplateResponse{JobID: 103}, nil
			}
			_, err := provider.TriggerProvision(ctx, &v1alpha1.ExternalIP{})
			Expect(err).NotTo(HaveOccurred())
		})

		It("includes the controller-resolved ExternalIPAttachment context", func() {
			attachmentContext := provisioning.ExternalIPAttachmentContext{
				ExternalIP: provisioning.ExternalIPAttachmentExternalIPContext{
					Name: "public-ip", Namespace: "networking", FulfillmentID: "eip-id",
					KubernetesUID: "eip-uid", TenantID: "tenant-a", Address: "198.51.100.10",
					State: "Allocated", Phase: "Ready",
				},
				Target: provisioning.ExternalIPAttachmentTargetContext{
					Kind: "BareMetalInstance", Name: "machine", Namespace: "bmaas",
					FulfillmentID: "bmi-id", KubernetesUID: "bmi-uid", TenantID: "tenant-a",
					IPAddress: "10.0.0.10", Ready: true,
				},
				Subnet: provisioning.ExternalIPAttachmentSubnetContext{
					Name: "subnet", Namespace: "networking", FulfillmentID: "subnet-id",
					KubernetesUID: "subnet-uid", TenantID: "tenant-a", IPv4CIDR: "10.0.0.0/24",
					VirtualNetworkID: "vn-id", Phase: "Ready",
				},
				VirtualNetwork: provisioning.ExternalIPAttachmentVirtualNetworkContext{
					Name: "network", Namespace: "networking", FulfillmentID: "vn-id",
					KubernetesUID: "vn-uid", TenantID: "tenant-a", Phase: "Ready",
				},
			}
			ctx = provisioning.WithExternalIPAttachmentContext(ctx, attachmentContext)
			aapClient.launchJobTemplateFunc = func(_ context.Context, req aap.LaunchJobTemplateRequest) (*aap.LaunchJobTemplateResponse, error) {
				jobVars := req.ExtraVars["osac_job_vars"].(map[string]any)
				Expect(jobVars["external_ip_attachment_context"]).To(Equal(map[string]any{
					"external_ip": map[string]any{
						"name": "public-ip", "namespace": "networking", "fulfillment_id": "eip-id",
						"kubernetes_uid": "eip-uid", "tenant_id": "tenant-a", "address": "198.51.100.10",
						"state": "Allocated", "phase": "Ready",
					},
					"target": map[string]any{
						"kind": "BareMetalInstance", "name": "machine", "namespace": "bmaas",
						"fulfillment_id": "bmi-id", "kubernetes_uid": "bmi-uid", "tenant_id": "tenant-a",
						"ip_address": "10.0.0.10", "ready": true,
					},
					"subnet": map[string]any{
						"name": "subnet", "namespace": "networking", "fulfillment_id": "subnet-id",
						"kubernetes_uid": "subnet-uid", "tenant_id": "tenant-a", "ipv4_cidr": "10.0.0.0/24",
						"virtual_network_id": "vn-id", "phase": "Ready",
					},
					"virtual_network": map[string]any{
						"name": "network", "namespace": "networking", "fulfillment_id": "vn-id",
						"kubernetes_uid": "vn-uid", "tenant_id": "tenant-a", "phase": "Ready",
					},
				}))
				return &aap.LaunchJobTemplateResponse{JobID: 104}, nil
			}
			_, err := provider.TriggerProvision(ctx, &v1alpha1.ExternalIPAttachment{})
			Expect(err).NotTo(HaveOccurred())
		})

		It("should omit the parent identity when it is not set in context", func() {
			aapClient.launchJobTemplateFunc = func(ctx context.Context, req aap.LaunchJobTemplateRequest) (*aap.LaunchJobTemplateResponse, error) {
				jobVars := req.ExtraVars["osac_job_vars"].(map[string]any)
				Expect(jobVars).NotTo(HaveKey("parent_virtual_network"))
				return &aap.LaunchJobTemplateResponse{JobID: 103}, nil
			}

			instance := &v1alpha1.ComputeInstance{
				ObjectMeta: metav1.ObjectMeta{Name: "test", Namespace: "default"},
			}
			_, err := provider.TriggerProvision(ctx, instance)
			Expect(err).NotTo(HaveOccurred())
		})
	})

	Describe("Additional AAP extra vars", func() {
		BeforeEach(func() {
			provider = provisioning.NewAAPProvider(aapClient, "provision-job", "deprovision-job")
		})

		It("merges event payload variables with the standard resource variables", func() {
			payload := map[string]any{"metadata": map[string]any{"name": "fabric-domain-a"}}
			ctx = provisioning.WithAAPExtraVars(ctx, map[string]any{
				"ansible_eda": map[string]any{"event": map[string]any{"payload": payload}},
			})
			aapClient.launchJobTemplateFunc = func(_ context.Context, req aap.LaunchJobTemplateRequest) (*aap.LaunchJobTemplateResponse, error) {
				Expect(req.ExtraVars).To(HaveKey("osac_job_vars"))
				eda := req.ExtraVars["ansible_eda"].(map[string]any)
				event := eda["event"].(map[string]any)
				Expect(event["payload"]).To(Equal(payload))
				return &aap.LaunchJobTemplateResponse{JobID: 101}, nil
			}

			_, err := provider.TriggerProvision(ctx, &v1alpha1.FabricDomain{ObjectMeta: metav1.ObjectMeta{Name: "fd", Namespace: "default"}})
			Expect(err).NotTo(HaveOccurred())
		})

		It("does not allow context variables to replace reserved osac_job_vars", func() {
			ctx = provisioning.WithAAPExtraVars(ctx, map[string]any{
				"osac_job_vars":   "caller override",
				"custom_variable": "preserved",
			})
			aapClient.launchJobTemplateFunc = func(_ context.Context, req aap.LaunchJobTemplateRequest) (*aap.LaunchJobTemplateResponse, error) {
				Expect(extractJobVarsResource(req.ExtraVars)).To(HaveKey("metadata"))
				Expect(req.ExtraVars).To(HaveKeyWithValue("custom_variable", "preserved"))
				return &aap.LaunchJobTemplateResponse{JobID: 102}, nil
			}

			_, err := provider.TriggerProvision(ctx, &v1alpha1.FabricDomain{ObjectMeta: metav1.ObjectMeta{Name: "fd", Namespace: "default"}})
			Expect(err).NotTo(HaveOccurred())
		})
	})

	Describe("AAP job artifacts", func() {
		It("returns successful job artifacts as provisioning outputs", func() {
			provider = provisioning.NewAAPProvider(aapClient, "provision-job", "deprovision-job")
			aapClient.getJobFunc = func(_ context.Context, _ string) (*aap.Job, error) {
				return &aap.Job{ID: 42, Status: "successful", Artifacts: json.RawMessage(`{"server_cluster_id":42,"server_cluster_vpc_id":"vpc-7"}`)}, nil
			}

			status, err := provider.GetProvisionStatus(ctx, &v1alpha1.FabricDomain{}, "42")
			Expect(err).NotTo(HaveOccurred())
			Expect(status.Outputs).To(HaveKeyWithValue("server_cluster_id", BeNumerically("==", 42)))
			Expect(status.Outputs).To(HaveKeyWithValue("server_cluster_vpc_id", "vpc-7"))
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
				},
			})

			aapClient.launchJobTemplateFunc = func(ctx context.Context, req aap.LaunchJobTemplateRequest) (*aap.LaunchJobTemplateResponse, error) {
				jobVars := req.ExtraVars["osac_job_vars"].(map[string]any)
				Expect(jobVars).To(HaveKey("storage_tier_definitions"))
				tiers := jobVars["storage_tier_definitions"].([]map[string]any)
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
				jobVars := req.ExtraVars["osac_job_vars"].(map[string]any)
				Expect(jobVars).NotTo(HaveKey("storage_tier_definitions"))
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
				jobVars := req.ExtraVars["osac_job_vars"].(map[string]any)
				Expect(jobVars).To(HaveKey("storage_backend_connections"))
				conns := jobVars["storage_backend_connections"].(map[string]map[string]any)
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
				jobVars := req.ExtraVars["osac_job_vars"].(map[string]any)
				Expect(jobVars).NotTo(HaveKey("storage_backend_connections"))
				return &aap.LaunchJobTemplateResponse{JobID: 122}, nil
			}

			instance := &v1alpha1.ComputeInstance{
				ObjectMeta: metav1.ObjectMeta{Name: "test", Namespace: "default"},
			}
			_, err := provider.TriggerProvision(ctx, instance)
			Expect(err).NotTo(HaveOccurred())
		})

		It("should emit network_attachment_macs keyed by subnet ref", func() {
			ctx = provisioning.WithNetworkAttachmentMACs(ctx, map[string]string{
				"subnet-a": "52:54:00:16:04:83",
			})

			aapClient.launchJobTemplateFunc = func(ctx context.Context, req aap.LaunchJobTemplateRequest) (*aap.LaunchJobTemplateResponse, error) {
				jobVars := req.ExtraVars["osac_job_vars"].(map[string]any)
				Expect(jobVars).To(HaveKey("network_attachment_macs"))
				Expect(jobVars["network_attachment_macs"]).To(Equal(map[string]string{
					"subnet-a": "52:54:00:16:04:83",
				}))
				return &aap.LaunchJobTemplateResponse{JobID: 123}, nil
			}

			instance := &v1alpha1.ComputeInstance{
				ObjectMeta: metav1.ObjectMeta{Name: "test", Namespace: "default"},
			}
			_, err := provider.TriggerProvision(ctx, instance)
			Expect(err).NotTo(HaveOccurred())
		})

		It("should omit network_attachment_macs when not set in context", func() {
			aapClient.launchJobTemplateFunc = func(ctx context.Context, req aap.LaunchJobTemplateRequest) (*aap.LaunchJobTemplateResponse, error) {
				jobVars := req.ExtraVars["osac_job_vars"].(map[string]any)
				Expect(jobVars).NotTo(HaveKey("network_attachment_macs"))
				return &aap.LaunchJobTemplateResponse{JobID: 124}, nil
			}

			instance := &v1alpha1.ComputeInstance{
				ObjectMeta: metav1.ObjectMeta{Name: "test", Namespace: "default"},
			}
			_, err := provider.TriggerProvision(ctx, instance)
			Expect(err).NotTo(HaveOccurred())
		})
	})
})

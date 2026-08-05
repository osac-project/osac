package aap_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/osac-project/osac-operator/pkg/aap"
)

func TestAAP(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "AAP Suite")
}

var _ = Describe("Client", func() {
	var (
		client *aap.Client
		server *httptest.Server
		ctx    context.Context
	)

	BeforeEach(func() {
		ctx = context.Background()
	})

	AfterEach(func() {
		if server != nil {
			server.Close()
		}
	})

	Describe("LaunchJobTemplate", func() {
		Context("when request succeeds", func() {
			BeforeEach(func() {
				server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					Expect(r.URL.Path).To(Equal(fmt.Sprintf("/%s/%s/test-template/launch/", aap.APIVersion, aap.JobTemplatesEndpoint)))
					Expect(r.Method).To(Equal(http.MethodPost))
					Expect(r.Header.Get("Content-Type")).To(Equal("application/json"))
					Expect(r.Header.Get("Authorization")).To(ContainSubstring("Bearer test-token"))

					w.WriteHeader(http.StatusOK)
					_ = json.NewEncoder(w).Encode(map[string]any{
						"id": 123,
					})
				}))
				client = aap.NewClient(server.URL, "test-token", false)
			})

			It("should return job ID", func() {
				resp, err := client.LaunchJobTemplate(ctx, aap.LaunchJobTemplateRequest{
					TemplateName: "test-template",
					ExtraVars:    map[string]any{"key": "value"},
				})
				Expect(err).NotTo(HaveOccurred())
				Expect(resp.JobID).To(Equal(123))
			})
		})

		Context("when request fails", func() {
			BeforeEach(func() {
				server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					w.WriteHeader(http.StatusNotFound)
					_, _ = w.Write([]byte("template not found"))
				}))
				client = aap.NewClient(server.URL, "test-token", false)
			})

			It("should return NotFoundError", func() {
				_, err := client.LaunchJobTemplate(ctx, aap.LaunchJobTemplateRequest{
					TemplateName: "missing-template",
				})
				Expect(err).To(HaveOccurred())
				var notFoundErr *aap.NotFoundError
				Expect(errors.As(err, &notFoundErr)).To(BeTrue())
			})
		})
	})

	Describe("LaunchWorkflowTemplate", func() {
		Context("when request succeeds", func() {
			BeforeEach(func() {
				server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					Expect(r.URL.Path).To(Equal(fmt.Sprintf("/%s/%s/test-workflow/launch/", aap.APIVersion, aap.WorkflowJobTemplatesEndpoint)))
					Expect(r.Method).To(Equal(http.MethodPost))

					w.WriteHeader(http.StatusOK)
					_ = json.NewEncoder(w).Encode(map[string]any{
						"id": 456,
					})
				}))
				client = aap.NewClient(server.URL, "test-token", false)
			})

			It("should return job ID", func() {
				resp, err := client.LaunchWorkflowTemplate(ctx, aap.LaunchWorkflowTemplateRequest{
					TemplateName: "test-workflow",
					ExtraVars:    map[string]any{"workflow_var": "value"},
				})
				Expect(err).NotTo(HaveOccurred())
				Expect(resp.JobID).To(Equal(456))
			})
		})
	})

	Describe("GetJob", func() {
		Context("when job exists", func() {
			BeforeEach(func() {
				server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					Expect(r.URL.Path).To(Equal(fmt.Sprintf("/%s/jobs/789/", aap.APIVersion)))
					Expect(r.Method).To(Equal(http.MethodGet))

					w.WriteHeader(http.StatusOK)
					_ = json.NewEncoder(w).Encode(map[string]any{
						"id":               789,
						"status":           "successful",
						"started":          time.Now().UTC().Format(time.RFC3339),
						"finished":         time.Now().UTC().Add(time.Minute).Format(time.RFC3339),
						"extra_vars":       "{\"key\": \"value\"}",
						"result_traceback": "",
					})
				}))
				client = aap.NewClient(server.URL, "test-token", false)
			})

			It("should return job details", func() {
				job, err := client.GetJob(ctx, "789")
				Expect(err).NotTo(HaveOccurred())
				Expect(job.ID).To(Equal(789))
				Expect(job.Status).To(Equal("successful"))
			})
		})

		Context("when job does not exist", func() {
			BeforeEach(func() {
				server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					w.WriteHeader(http.StatusNotFound)
				}))
				client = aap.NewClient(server.URL, "test-token", false)
			})

			It("should return error", func() {
				_, err := client.GetJob(ctx, "999")
				Expect(err).To(HaveOccurred())
			})
		})
	})

	Describe("GetTemplateByName", func() {
		Context("when template is a job_template", func() {
			BeforeEach(func() {
				server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					if r.URL.Path == fmt.Sprintf("/%s/%s/", aap.APIVersion, aap.JobTemplatesEndpoint) && r.URL.Query().Get("name") == "my-job" {
						w.WriteHeader(http.StatusOK)
						_ = json.NewEncoder(w).Encode(map[string]any{
							"count": 1,
							"results": []map[string]any{
								{"id": 19, "name": "my-job"},
							},
						})
					} else {
						w.WriteHeader(http.StatusOK)
						_ = json.NewEncoder(w).Encode(map[string]any{"count": 0, "results": []any{}})
					}
				}))
				client = aap.NewClient(server.URL, "test-token", false)
			})

			It("should return job template", func() {
				template, err := client.GetTemplateByName(ctx, "my-job")
				Expect(err).NotTo(HaveOccurred())
				Expect(template.Type).To(Equal(aap.TemplateTypeJob))
				Expect(template.Name).To(Equal("my-job"))
				Expect(template.ID).To(Equal(19))
			})
		})

		Context("when template is a workflow_job_template", func() {
			BeforeEach(func() {
				server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					if r.URL.Path == fmt.Sprintf("/%s/%s/", aap.APIVersion, aap.WorkflowJobTemplatesEndpoint) && r.URL.Query().Get("name") == "my-workflow" {
						w.WriteHeader(http.StatusOK)
						_ = json.NewEncoder(w).Encode(map[string]any{
							"count": 1,
							"results": []map[string]any{
								{"id": 20, "name": "my-workflow"},
							},
						})
					} else {
						w.WriteHeader(http.StatusOK)
						_ = json.NewEncoder(w).Encode(map[string]any{"count": 0, "results": []any{}})
					}
				}))
				client = aap.NewClient(server.URL, "test-token", false)
			})

			It("should return workflow template", func() {
				template, err := client.GetTemplateByName(ctx, "my-workflow")
				Expect(err).NotTo(HaveOccurred())
				Expect(template.Type).To(Equal(aap.TemplateTypeWorkflow))
				Expect(template.Name).To(Equal("my-workflow"))
				Expect(template.ID).To(Equal(20))
			})
		})

		Context("when template does not exist", func() {
			BeforeEach(func() {
				server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					w.WriteHeader(http.StatusOK)
					_ = json.NewEncoder(w).Encode(map[string]any{"count": 0, "results": []any{}})
				}))
				client = aap.NewClient(server.URL, "test-token", false)
			})

			It("should return error", func() {
				_, err := client.GetTemplateByName(ctx, "nonexistent")
				Expect(err).To(HaveOccurred())
				Expect(err.Error()).To(ContainSubstring("not found"))
			})
		})
	})

	Describe("GetTemplate", func() {
		Context("with caching", func() {
			var requestCount int

			BeforeEach(func() {
				requestCount = 0
				server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					requestCount++
					if r.URL.Path == fmt.Sprintf("/%s/%s/", aap.APIVersion, aap.JobTemplatesEndpoint) && r.URL.Query().Get("name") == "cached-job" {
						w.WriteHeader(http.StatusOK)
						_ = json.NewEncoder(w).Encode(map[string]any{
							"count": 1,
							"results": []map[string]any{
								{"id": 1, "name": "cached-job"},
							},
						})
					} else {
						w.WriteHeader(http.StatusOK)
						_ = json.NewEncoder(w).Encode(map[string]any{"count": 0, "results": []any{}})
					}
				}))
				client = aap.NewClient(server.URL, "test-token", false)
			})

			It("should cache result and avoid repeated AAP queries", func() {
				// First call queries AAP and caches
				template, err := client.GetTemplate(ctx, "cached-job")
				Expect(err).NotTo(HaveOccurred())
				Expect(template.Type).To(Equal(aap.TemplateTypeJob))
				Expect(requestCount).To(Equal(1))

				// Second call uses cache without querying AAP
				template, err = client.GetTemplate(ctx, "cached-job")
				Expect(err).NotTo(HaveOccurred())
				Expect(template.Type).To(Equal(aap.TemplateTypeJob))
				Expect(requestCount).To(Equal(1)) // No additional requests
			})
		})
	})

	Describe("Template cache invalidation on 404", func() {
		var requestCount int
		const templateName = "deleted-template"

		BeforeEach(func() {
			requestCount = 0
			server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				requestCount++

				templateQueryPath := fmt.Sprintf("/%s/%s/", aap.APIVersion, aap.JobTemplatesEndpoint)
				isTemplateQuery := r.URL.Path == templateQueryPath && r.URL.Query().Get("name") == templateName

				launchPath := fmt.Sprintf("/%s/%s/%s/launch/", aap.APIVersion, aap.JobTemplatesEndpoint, templateName)
				isLaunchRequest := r.URL.Path == launchPath && r.Method == http.MethodPost

				if isTemplateQuery {
					// First call: template exists
					// Second call (after 404): template exists again (simulating re-query)
					w.WriteHeader(http.StatusOK)
					_ = json.NewEncoder(w).Encode(map[string]any{
						"count": 1,
						"results": []map[string]any{
							{"id": 99, "name": templateName},
						},
					})
				} else if isLaunchRequest {
					// Launch returns 404 - template was deleted in AAP
					w.WriteHeader(http.StatusNotFound)
					_, _ = w.Write([]byte("template not found"))
				} else {
					w.WriteHeader(http.StatusOK)
					_ = json.NewEncoder(w).Encode(map[string]any{"count": 0, "results": []any{}})
				}
			}))
			client = aap.NewClient(server.URL, "test-token", false)
		})

		It("should invalidate cache when LaunchJobTemplate returns 404", func() {
			// Step 1: GetTemplate caches the template
			template, err := client.GetTemplate(ctx, templateName)
			Expect(err).NotTo(HaveOccurred())
			Expect(template.ID).To(Equal(99))
			Expect(requestCount).To(Equal(1)) // 1 request to get template

			// Step 2: LaunchJobTemplate returns 404 (template deleted in AAP)
			// This should trigger cache invalidation
			_, err = client.LaunchJobTemplate(ctx, aap.LaunchJobTemplateRequest{
				TemplateName: templateName,
			})
			Expect(err).To(HaveOccurred())
			var notFoundErr *aap.NotFoundError
			Expect(errors.As(err, &notFoundErr)).To(BeTrue())
			Expect(requestCount).To(Equal(2)) // 2 requests: initial get + failed launch

			// Step 3: Next GetTemplate should query AAP again (not from cache)
			template, err = client.GetTemplate(ctx, templateName)
			Expect(err).NotTo(HaveOccurred())
			Expect(template.ID).To(Equal(99))
			Expect(requestCount).To(Equal(3)) // 3 requests: get + launch + get (cache invalidated)
		})

		It("should invalidate cache when LaunchWorkflowTemplate returns 404", func() {
			const workflowName = "deleted-workflow"

			// Reset server for workflow template test
			server.Close()
			requestCount = 0
			server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				requestCount++

				workflowQueryPath := fmt.Sprintf("/%s/%s/", aap.APIVersion, aap.WorkflowJobTemplatesEndpoint)
				isWorkflowQuery := r.URL.Path == workflowQueryPath && r.URL.Query().Get("name") == workflowName

				workflowLaunchPath := fmt.Sprintf("/%s/%s/%s/launch/", aap.APIVersion, aap.WorkflowJobTemplatesEndpoint, workflowName)
				isWorkflowLaunchRequest := r.URL.Path == workflowLaunchPath && r.Method == http.MethodPost

				jobQueryPath := fmt.Sprintf("/%s/%s/", aap.APIVersion, aap.JobTemplatesEndpoint)
				isJobQuery := r.URL.Path == jobQueryPath && r.URL.Query().Get("name") == workflowName

				if isWorkflowQuery {
					w.WriteHeader(http.StatusOK)
					_ = json.NewEncoder(w).Encode(map[string]any{
						"count": 1,
						"results": []map[string]any{
							{"id": 88, "name": workflowName},
						},
					})
				} else if isWorkflowLaunchRequest {
					w.WriteHeader(http.StatusNotFound)
					_, _ = w.Write([]byte("workflow template not found"))
				} else if isJobQuery {
					w.WriteHeader(http.StatusOK)
					_ = json.NewEncoder(w).Encode(map[string]any{"count": 0, "results": []any{}})
				} else {
					w.WriteHeader(http.StatusOK)
					_ = json.NewEncoder(w).Encode(map[string]any{"count": 0, "results": []any{}})
				}
			}))
			client = aap.NewClient(server.URL, "test-token", false)

			// Step 1: GetTemplate caches the workflow template
			template, err := client.GetTemplate(ctx, workflowName)
			Expect(err).NotTo(HaveOccurred())
			Expect(template.ID).To(Equal(88))
			Expect(template.Type).To(Equal(aap.TemplateTypeWorkflow))
			Expect(requestCount).To(Equal(2)) // 2 requests: job_templates + workflow_job_templates

			// Step 2: LaunchWorkflowTemplate returns 404 (template deleted in AAP)
			// This should trigger cache invalidation
			_, err = client.LaunchWorkflowTemplate(ctx, aap.LaunchWorkflowTemplateRequest{
				TemplateName: workflowName,
			})
			Expect(err).To(HaveOccurred())
			var notFoundErr *aap.NotFoundError
			Expect(errors.As(err, &notFoundErr)).To(BeTrue())
			Expect(requestCount).To(Equal(3)) // 3 requests: 2 initial gets + failed launch

			// Step 3: Next GetTemplate should query AAP again (not from cache)
			template, err = client.GetTemplate(ctx, workflowName)
			Expect(err).NotTo(HaveOccurred())
			Expect(template.ID).To(Equal(88))
			Expect(requestCount).To(Equal(5)) // 5 requests: 2 initial + launch + 2 re-query (cache invalidated)
		})
	})

	Describe("ClearTemplateCache", func() {
		var requestCount int

		BeforeEach(func() {
			requestCount = 0
			server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				requestCount++
				if r.URL.Path == fmt.Sprintf("/%s/%s/", aap.APIVersion, aap.JobTemplatesEndpoint) && r.URL.Query().Get("name") == "job1" {
					w.WriteHeader(http.StatusOK)
					_ = json.NewEncoder(w).Encode(map[string]any{
						"count": 1,
						"results": []map[string]any{
							{"id": 1, "name": "job1"},
						},
					})
				} else if r.URL.Path == fmt.Sprintf("/%s/%s/", aap.APIVersion, aap.WorkflowJobTemplatesEndpoint) && r.URL.Query().Get("name") == "workflow1" {
					w.WriteHeader(http.StatusOK)
					_ = json.NewEncoder(w).Encode(map[string]any{
						"count": 1,
						"results": []map[string]any{
							{"id": 2, "name": "workflow1"},
						},
					})
				} else {
					w.WriteHeader(http.StatusOK)
					_ = json.NewEncoder(w).Encode(map[string]any{"count": 0, "results": []any{}})
				}
			}))
			client = aap.NewClient(server.URL, "test-token", false)
		})

		It("should clear all cached templates", func() {
			// Populate cache with multiple entries
			_, err := client.GetTemplate(ctx, "job1")
			Expect(err).NotTo(HaveOccurred())
			_, err = client.GetTemplate(ctx, "workflow1")
			Expect(err).NotTo(HaveOccurred())
			initialCount := requestCount

			// Clear cache
			client.ClearTemplateCache()

			// Both should query AAP again
			_, err = client.GetTemplate(ctx, "job1")
			Expect(err).NotTo(HaveOccurred())
			_, err = client.GetTemplate(ctx, "workflow1")
			Expect(err).NotTo(HaveOccurred())
			Expect(requestCount).To(Equal(initialCount * 2))
		})
	})

	Describe("CanCancelJob", func() {
		Context("when job can be canceled", func() {
			BeforeEach(func() {
				server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					_ = json.NewEncoder(w).Encode(map[string]any{
						"can_cancel": true,
					})
				}))
				client = aap.NewClient(server.URL, "test-token", false)
			})

			It("should return true", func() {
				canCancel, err := client.CanCancelJob(ctx, "123")
				Expect(err).NotTo(HaveOccurred())
				Expect(canCancel).To(BeTrue())
			})
		})

		Context("when job cannot be canceled", func() {
			BeforeEach(func() {
				server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					_ = json.NewEncoder(w).Encode(map[string]any{
						"can_cancel": false,
					})
				}))
				client = aap.NewClient(server.URL, "test-token", false)
			})

			It("should return false", func() {
				canCancel, err := client.CanCancelJob(ctx, "456")
				Expect(err).NotTo(HaveOccurred())
				Expect(canCancel).To(BeFalse())
			})
		})

		Context("when request fails", func() {
			BeforeEach(func() {
				server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					w.WriteHeader(http.StatusNotFound)
				}))
				client = aap.NewClient(server.URL, "test-token", false)
			})

			It("should return error", func() {
				_, err := client.CanCancelJob(ctx, "999")
				Expect(err).To(HaveOccurred())
			})
		})
	})

	Describe("CancelJob", func() {
		Context("when cancellation succeeds", func() {
			BeforeEach(func() {
				server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					w.WriteHeader(http.StatusAccepted)
					_ = json.NewEncoder(w).Encode(map[string]any{})
				}))
				client = aap.NewClient(server.URL, "test-token", false)
			})

			It("should return no error", func() {
				err := client.CancelJob(ctx, "123")
				Expect(err).NotTo(HaveOccurred())
			})
		})

		Context("when job cannot be canceled (method not allowed)", func() {
			BeforeEach(func() {
				server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					w.WriteHeader(http.StatusMethodNotAllowed)
					_, _ = w.Write([]byte("job cannot be canceled"))
				}))
				client = aap.NewClient(server.URL, "test-token", false)
			})

			It("should return MethodNotAllowedError", func() {
				err := client.CancelJob(ctx, "456")
				Expect(err).To(HaveOccurred())
				var methodNotAllowedErr *aap.MethodNotAllowedError
				Expect(errors.As(err, &methodNotAllowedErr)).To(BeTrue())
			})
		})
	})
})

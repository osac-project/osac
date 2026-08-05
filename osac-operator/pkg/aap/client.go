// Package aap provides a client for interacting with the Ansible Automation Platform (AAP) REST API.
package aap

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"sync"
	"time"

	ctrllog "sigs.k8s.io/controller-runtime/pkg/log"
)

// NotFoundError indicates a resource was not found in AAP (HTTP 404).
// This typically happens when querying for jobs that have been purged.
type NotFoundError struct {
	Resource string // e.g., "job 123", "template foo"
	URL      string
}

func (e *NotFoundError) Error() string {
	return fmt.Sprintf("resource not found: %s", e.Resource)
}

// MethodNotAllowedError indicates the requested operation is not allowed (HTTP 405).
// For job cancellation, this typically means the job is already in a terminal state.
type MethodNotAllowedError struct {
	Operation string // e.g., "cancel job 123"
	URL       string
}

func (e *MethodNotAllowedError) Error() string {
	return fmt.Sprintf("method not allowed: %s", e.Operation)
}

const (
	// APIVersion is the AAP API version path
	APIVersion = "v2"

	// AAP template endpoint paths
	JobTemplatesEndpoint         = "job_templates"
	WorkflowJobTemplatesEndpoint = "workflow_job_templates"
)

// Client provides an HTTP client for interacting with AAP (Ansible Automation Platform) REST API.
type Client struct {
	baseURL       string
	httpClient    *http.Client
	token         string
	templateCache sync.Map // map[string]*Template - caches template name → template (with ID, name, type)
}

// NewClient creates a new AAP API client.
// When insecureSkipVerify is true, TLS certificate verification is disabled (InsecureSkipVerify) for AAP API requests.
func NewClient(baseURL, token string, insecureSkipVerify bool) *Client {
	transport := &http.Transport{}
	if insecureSkipVerify {
		transport.TLSClientConfig = &tls.Config{InsecureSkipVerify: true}
	}
	return &Client{
		baseURL: baseURL,
		token:   token,
		httpClient: &http.Client{
			Timeout:   30 * time.Second,
			Transport: transport,
		},
	}
}

// LaunchJobTemplateRequest contains parameters for launching a job template.
type LaunchJobTemplateRequest struct {
	TemplateID   int
	TemplateName string
	ExtraVars    map[string]any
}

// LaunchJobTemplateResponse contains the response from launching a job template.
type LaunchJobTemplateResponse struct {
	JobID int `json:"id"`
}

// LaunchWorkflowTemplateRequest contains parameters for launching a workflow template.
type LaunchWorkflowTemplateRequest struct {
	TemplateID   int
	TemplateName string
	ExtraVars    map[string]any
}

// LaunchWorkflowTemplateResponse contains the response from launching a workflow template.
type LaunchWorkflowTemplateResponse struct {
	JobID int `json:"id"`
}

// Job represents an AAP job with status information.
type Job struct {
	ID              int       `json:"id"`
	Status          string    `json:"status"`
	Started         time.Time `json:"started"`
	Finished        time.Time `json:"finished"`
	ExtraVars       string    `json:"extra_vars"` // AAP returns this as a JSON-encoded string
	ResultTraceback string    `json:"result_traceback"`
}

// TemplateType represents the type of AAP template.
type TemplateType string

const (
	TemplateTypeJob      TemplateType = "job_template"
	TemplateTypeWorkflow TemplateType = "workflow_job_template"
)

// Template represents an AAP job template or workflow job template.
type Template struct {
	ID   int          `json:"id"`
	Name string       `json:"name"`
	Type TemplateType `json:"-"` // Type is determined by which endpoint returned the template
}

// LaunchJobTemplate launches a job template and returns the job ID.
func (c *Client) LaunchJobTemplate(ctx context.Context, req LaunchJobTemplateRequest) (*LaunchJobTemplateResponse, error) {
	identifier := req.TemplateName
	if req.TemplateID > 0 {
		identifier = fmt.Sprintf("%d", req.TemplateID)
	}
	url := fmt.Sprintf("%s/%s/%s/%s/launch/", c.baseURL, APIVersion, JobTemplatesEndpoint, identifier)

	payload := map[string]any{
		"extra_vars": req.ExtraVars,
	}

	resp, err := c.doTemplateRequest(ctx, http.MethodPost, url, payload, req.TemplateName)
	if err != nil {
		return nil, fmt.Errorf("failed to launch job template: %w", err)
	}

	var launchResp LaunchJobTemplateResponse
	if err := json.Unmarshal(resp, &launchResp); err != nil {
		return nil, fmt.Errorf("failed to parse launch response: %w", err)
	}

	return &launchResp, nil
}

// LaunchWorkflowTemplate launches a workflow template and returns the job ID.
func (c *Client) LaunchWorkflowTemplate(ctx context.Context, req LaunchWorkflowTemplateRequest) (*LaunchWorkflowTemplateResponse, error) {
	identifier := req.TemplateName
	if req.TemplateID > 0 {
		identifier = fmt.Sprintf("%d", req.TemplateID)
	}
	url := fmt.Sprintf("%s/%s/%s/%s/launch/", c.baseURL, APIVersion, WorkflowJobTemplatesEndpoint, identifier)

	payload := map[string]any{
		"extra_vars": req.ExtraVars,
	}

	resp, err := c.doTemplateRequest(ctx, http.MethodPost, url, payload, req.TemplateName)
	if err != nil {
		return nil, fmt.Errorf("failed to launch workflow template: %w", err)
	}

	var launchResp LaunchWorkflowTemplateResponse
	if err := json.Unmarshal(resp, &launchResp); err != nil {
		return nil, fmt.Errorf("failed to parse launch response: %w", err)
	}

	return &launchResp, nil
}

// GetJob retrieves job status by job ID.
func (c *Client) GetJob(ctx context.Context, jobID string) (*Job, error) {
	url := fmt.Sprintf("%s/%s/jobs/%s/", c.baseURL, APIVersion, jobID)

	resp, err := c.doRequest(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to get job: %w", err)
	}

	var job Job
	if err := json.Unmarshal(resp, &job); err != nil {
		return nil, fmt.Errorf("failed to parse job response: %w", err)
	}

	return &job, nil
}

// CanCancelJobResponse contains the response from checking if a job can be canceled.
type CanCancelJobResponse struct {
	CanCancel bool `json:"can_cancel"`
}

// CanCancelJob checks if a job can be canceled.
// Returns true if the job is in a state that allows cancellation (pending/running).
func (c *Client) CanCancelJob(ctx context.Context, jobID string) (bool, error) {
	url := fmt.Sprintf("%s/%s/jobs/%s/cancel/", c.baseURL, APIVersion, jobID)

	resp, err := c.doRequest(ctx, http.MethodGet, url, nil)
	if err != nil {
		return false, fmt.Errorf("failed to check if job can be canceled: %w", err)
	}

	var canCancelResp CanCancelJobResponse
	if err := json.Unmarshal(resp, &canCancelResp); err != nil {
		return false, fmt.Errorf("failed to parse can_cancel response: %w", err)
	}

	return canCancelResp.CanCancel, nil
}

// CancelJob cancels a pending or running job.
// Returns nil if successful (HTTP 202), or an error if the job cannot be canceled (HTTP 405).
// Note: When canceling a job, AAP issues a SIGINT to the ansible-playbook process,
// which causes Ansible to stop dispatching new tasks. Tasks already dispatched to
// remote hosts will run to completion.
func (c *Client) CancelJob(ctx context.Context, jobID string) error {
	url := fmt.Sprintf("%s/%s/jobs/%s/cancel/", c.baseURL, APIVersion, jobID)

	_, err := c.doRequest(ctx, http.MethodPost, url, nil)
	if err != nil {
		return fmt.Errorf("failed to cancel job: %w", err)
	}

	return nil
}

// getTemplateFromEndpoint queries a specific AAP template endpoint by name.
// Returns the template if found, or an error if not found or request failed.
func (c *Client) getTemplateFromEndpoint(ctx context.Context, templateEndpoint, templateName string, templateType TemplateType) (*Template, error) {
	url := fmt.Sprintf("%s/%s/%s/?name=%s", c.baseURL, APIVersion, templateEndpoint, templateName)
	resp, err := c.doRequest(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}

	var result struct {
		Count   int        `json:"count"`
		Results []Template `json:"results"`
	}
	if err := json.Unmarshal(resp, &result); err != nil {
		return nil, fmt.Errorf("failed to parse template lookup response: %w", err)
	}

	if result.Count == 0 {
		return nil, fmt.Errorf("template %s not found", templateName)
	}

	template := result.Results[0]
	template.Type = templateType
	return &template, nil
}

// GetTemplateByName queries AAP to find a template by name.
// Returns the template with its ID, name, and type.
// This method does not use caching.
func (c *Client) GetTemplateByName(ctx context.Context, templateName string) (*Template, error) {
	// Try job template first
	template, lookupErr := c.getTemplateFromEndpoint(ctx, JobTemplatesEndpoint, templateName, TemplateTypeJob)
	if lookupErr == nil {
		return template, nil
	}

	// Try workflow template
	template, lookupErr = c.getTemplateFromEndpoint(ctx, WorkflowJobTemplatesEndpoint, templateName, TemplateTypeWorkflow)
	if lookupErr == nil {
		return template, nil
	}

	return nil, fmt.Errorf("template %s not found as %s or %s", templateName, JobTemplatesEndpoint, WorkflowJobTemplatesEndpoint)
}

// GetTemplate retrieves a template by name with caching.
// Checks cache first, then queries AAP if not cached.
// Returns the template with ID, name, and type.
func (c *Client) GetTemplate(ctx context.Context, templateName string) (*Template, error) {
	// Check cache first
	if cached, ok := c.templateCache.Load(templateName); ok {
		return cached.(*Template), nil
	}

	// Not in cache, query AAP
	template, err := c.GetTemplateByName(ctx, templateName)
	if err != nil {
		return nil, err
	}

	// Store template in cache
	c.templateCache.Store(templateName, template)
	return template, nil
}

// invalidateTemplateCache removes a template from the cache.
// Called internally when template lookups fail to ensure fresh lookups on retry.
func (c *Client) invalidateTemplateCache(templateName string) {
	c.templateCache.Delete(templateName)
}

// ClearTemplateCache removes all templates from the cache.
func (c *Client) ClearTemplateCache() {
	c.templateCache.Range(func(key, value any) bool {
		c.templateCache.Delete(key)
		return true
	})
}

// doTemplateRequest performs an HTTP request for template operations with cache invalidation on 404.
// If the template is not found (404), it invalidates the cache to ensure fresh lookup on retry.
func (c *Client) doTemplateRequest(ctx context.Context, method, url string, payload any, templateName string) ([]byte, error) {
	resp, err := c.doRequest(ctx, method, url, payload)
	if err != nil {
		// If template not found (404), invalidate cache to ensure fresh lookup on retry
		var notFoundErr *NotFoundError
		if errors.As(err, &notFoundErr) {
			c.invalidateTemplateCache(templateName)
		}
		return nil, err
	}
	return resp, nil
}

// doRequest performs an HTTP request with authentication and returns the response body.
func (c *Client) doRequest(ctx context.Context, method, url string, payload any) ([]byte, error) {
	log := ctrllog.FromContext(ctx)

	log.V(1).Info("AAP request starting", "method", method, "url", url)

	var body io.Reader
	if payload != nil {
		jsonData, err := json.Marshal(payload)
		if err != nil {
			log.Error(err, "AAP request failed to marshal payload", "method", method, "url", url)
			return nil, fmt.Errorf("failed to marshal payload: %w", err)
		}
		body = bytes.NewBuffer(jsonData)
	}

	req, err := http.NewRequestWithContext(ctx, method, url, body)
	if err != nil {
		log.Error(err, "AAP request failed to create request", "method", method, "url", url)
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", fmt.Sprintf("Bearer %s", c.token))

	resp, err := c.httpClient.Do(req)
	if err != nil {
		log.Error(err, "AAP request failed to send", "method", method, "url", url)
		return nil, fmt.Errorf("failed to send request: %w", err)
	}
	defer func() {
		_ = resp.Body.Close()
	}()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		log.Error(err, "AAP request failed to read response body", "method", method, "url", url, "status", resp.StatusCode)
		return nil, fmt.Errorf("failed to read response body: %w", err)
	}

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		bodyPreview := string(respBody)
		if len(bodyPreview) > 500 {
			bodyPreview = bodyPreview[:500] + "..."
		}
		log.Info("AAP request returned non-success status", "method", method, "url", url, "status", resp.StatusCode, "responseBody", bodyPreview)
		// Return typed error for 404 Not Found
		if resp.StatusCode == http.StatusNotFound {
			return nil, &NotFoundError{
				Resource: url,
				URL:      url,
			}
		}
		// Return typed error for 405 Method Not Allowed
		if resp.StatusCode == http.StatusMethodNotAllowed {
			return nil, &MethodNotAllowedError{
				Operation: fmt.Sprintf("%s %s", method, url),
				URL:       url,
			}
		}
		return nil, fmt.Errorf("received non-success status code %d: %s", resp.StatusCode, string(respBody))
	}

	log.V(1).Info("AAP request succeeded", "method", method, "url", url, "status", resp.StatusCode)
	return respBody, nil
}

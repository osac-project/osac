package aap

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
)

// Project identifies the source and synchronization state of an AAP project.
type Project struct {
	ID          int    `json:"id"`
	SCMURL      string `json:"scm_url"`
	SCMBranch   string `json:"scm_branch"`
	SCMRevision string `json:"scm_revision"`
	SCMType     string `json:"scm_type"`
	Status      string `json:"status"`
}

// ExecutionEnvironment describes the image and pull policy used by AAP jobs.
type ExecutionEnvironment struct {
	ID    int    `json:"id"`
	Image string `json:"image"`
	Pull  string `json:"pull"`
}

// InstanceGroup describes an AAP execution group and its container Pod template.
type InstanceGroup struct {
	ID               int    `json:"id"`
	IsContainerGroup bool   `json:"is_container_group"`
	PodSpecOverride  string `json:"pod_spec_override"`
}

// ListProjects reads projects with the given name.
func (c *Client) ListProjects(ctx context.Context, name string) ([]Project, error) {
	var result struct {
		Results []Project `json:"results"`
	}
	if err := c.getConfiguration(ctx, "projects", name, &result); err != nil {
		return nil, err
	}
	return result.Results, nil
}

// ListExecutionEnvironments reads execution environments with the given name.
func (c *Client) ListExecutionEnvironments(ctx context.Context, name string) ([]ExecutionEnvironment, error) {
	var result struct {
		Results []ExecutionEnvironment `json:"results"`
	}
	if err := c.getConfiguration(ctx, "execution_environments", name, &result); err != nil {
		return nil, err
	}
	return result.Results, nil
}

// ListInstanceGroups reads execution groups with the given name.
func (c *Client) ListInstanceGroups(ctx context.Context, name string) ([]InstanceGroup, error) {
	var result struct {
		Results []InstanceGroup `json:"results"`
	}
	if err := c.getConfiguration(ctx, "instance_groups", name, &result); err != nil {
		return nil, err
	}
	return result.Results, nil
}

// getConfiguration reads a name-filtered configuration response through the shared client.
func (c *Client) getConfiguration(ctx context.Context, endpoint, name string, result any) error {
	query := url.Values{"name": {name}}
	address := fmt.Sprintf("%s/%s/%s/?%s", c.baseURL, APIVersion, endpoint, query.Encode())
	response, err := c.doRequestWithRedaction(ctx, http.MethodGet, address, nil, true)
	if err != nil {
		return fmt.Errorf("failed to list %s: %w", endpoint, err)
	}
	if err := json.Unmarshal(response, result); err != nil {
		return fmt.Errorf("failed to parse %s: %w", endpoint, err)
	}
	return nil
}

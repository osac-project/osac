package mcpserver

import (
	"context"
	"fmt"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"google.golang.org/protobuf/types/known/fieldmaskpb"

	publicv1 "github.com/osac-project/osac/proto/gen/osac/public/v1"
)

type CreateProjectInput struct {
	Name        string  `json:"name" jsonschema:"hierarchical project name; the public API derives the parent Project from its dot-separated prefix"`
	Title       string  `json:"title,omitempty" jsonschema:"optional project title"`
	Description *string `json:"description,omitempty" jsonschema:"optional project description"`
}

type UpdateProjectInput struct {
	ID          string  `json:"id" jsonschema:"project ID from get_resource"`
	Title       *string `json:"title,omitempty" jsonschema:"optional replacement project title"`
	Description *string `json:"description,omitempty" jsonschema:"optional replacement project description"`
}

type ProjectOutput struct {
	ID      string `json:"id"`
	Name    string `json:"name"`
	Version int32  `json:"version"`
	State   string `json:"state"`
}

func handleCreateProject(
	client publicv1.ProjectsClient,
) mcp.ToolHandlerFor[CreateProjectInput, ProjectOutput] {
	return func(
		ctx context.Context, req *mcp.CallToolRequest, input CreateProjectInput,
	) (*mcp.CallToolResult, ProjectOutput, error) {
		input.Name = strings.TrimSpace(input.Name)
		if input.Name == "" {
			return nil, ProjectOutput{}, safeInvalidRequestError(fmt.Errorf("name is required"))
		}

		ctx, cancel := toolCallContext(ctx)
		defer cancel()
		ctx, err := forwardAPIToken(ctx, req)
		if err != nil {
			return nil, ProjectOutput{}, err
		}

		response, err := client.Create(ctx, publicv1.ProjectsCreateRequest_builder{
			Object: publicv1.Project_builder{
				Metadata: publicv1.Metadata_builder{Name: input.Name}.Build(),
				Spec: publicv1.ProjectSpec_builder{
					Title:       input.Title,
					Description: input.Description,
				}.Build(),
			}.Build(),
		}.Build())
		if err != nil {
			return nil, ProjectOutput{}, safePublicAPIError(err)
		}
		return nil, projectOutput(response.GetObject()), nil
	}
}

func handleUpdateProject(
	client publicv1.ProjectsClient,
) mcp.ToolHandlerFor[UpdateProjectInput, ProjectOutput] {
	return func(
		ctx context.Context, req *mcp.CallToolRequest, input UpdateProjectInput,
	) (*mcp.CallToolResult, ProjectOutput, error) {
		input.ID = strings.TrimSpace(input.ID)
		if input.ID == "" {
			return nil, ProjectOutput{}, safeInvalidRequestError(fmt.Errorf("id is required"))
		}
		if input.Title == nil && input.Description == nil {
			return nil, ProjectOutput{}, safeInvalidRequestError(fmt.Errorf(
				"at least one supported update field is required",
			))
		}

		ctx, cancel := toolCallContext(ctx)
		defer cancel()
		ctx, err := forwardAPIToken(ctx, req)
		if err != nil {
			return nil, ProjectOutput{}, err
		}

		paths := make([]string, 0, 2)
		spec := publicv1.ProjectSpec_builder{}
		if input.Title != nil {
			paths = append(paths, "spec.title")
			spec.Title = *input.Title
		}
		if input.Description != nil {
			paths = append(paths, "spec.description")
			spec.Description = input.Description
		}

		response, err := client.Update(ctx, publicv1.ProjectsUpdateRequest_builder{
			Object: publicv1.Project_builder{
				Id:   input.ID,
				Spec: spec.Build(),
			}.Build(),
			UpdateMask: &fieldmaskpb.FieldMask{Paths: paths},
		}.Build())
		if err != nil {
			return nil, ProjectOutput{}, safePublicAPIError(err)
		}
		return nil, projectOutput(response.GetObject()), nil
	}
}

func projectOutput(project *publicv1.Project) ProjectOutput {
	return ProjectOutput{
		ID:      project.GetId(),
		Name:    project.GetMetadata().GetName(),
		Version: project.GetMetadata().GetVersion(),
		State:   project.GetStatus().GetState().String(),
	}
}

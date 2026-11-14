package mcpserver

import (
	"context"
	"fmt"
	"strings"

	"buf.build/go/protovalidate"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/fieldmaskpb"

	publicv1 "github.com/osac-project/osac/proto/gen/osac/public/v1"
)

type CatalogOfferingKind string

const (
	CatalogOfferingKindComputeInstance   CatalogOfferingKind = "compute_instance"
	CatalogOfferingKindCluster           CatalogOfferingKind = "cluster"
	CatalogOfferingKindBareMetalInstance CatalogOfferingKind = "bare_metal_instance"
)

type catalogOfferingClients struct {
	computeInstances publicv1.ComputeInstanceCatalogItemsClient
	clusters         publicv1.ClusterCatalogItemsClient
	bareMetal        publicv1.BareMetalInstanceCatalogItemsClient
}

// newCatalogOfferingClients collects the public catalog API clients by family.
func newCatalogOfferingClients(deps ServerDeps) catalogOfferingClients {
	return catalogOfferingClients{
		computeInstances: deps.ComputeInstanceCatalogItemsClient,
		clusters:         deps.ClusterCatalogItemsClient,
		bareMetal:        deps.BareMetalInstanceCatalogItemsClient,
	}
}

type CreateCatalogOfferingInput struct {
	Kind        CatalogOfferingKind `json:"kind" jsonschema:"offering family"`
	Name        string              `json:"name" jsonschema:"DNS-safe catalog offering name"`
	Title       string              `json:"title,omitempty" jsonschema:"optional catalog offering title"`
	Description string              `json:"description,omitempty" jsonschema:"optional catalog offering description"`
	TemplateID  string              `json:"template_id" jsonschema:"ID of the offering's immutable template"`
}

type UpdateCatalogOfferingInput struct {
	Kind        CatalogOfferingKind `json:"kind" jsonschema:"offering family"`
	ID          string              `json:"id" jsonschema:"catalog offering ID"`
	Version     int32               `json:"version" jsonschema:"current metadata.version from the public API"`
	Title       *string             `json:"title,omitempty" jsonschema:"optional replacement title"`
	Description *string             `json:"description,omitempty" jsonschema:"optional replacement description"`
}

type PublishCatalogOfferingInput struct {
	Kind    CatalogOfferingKind `json:"kind" jsonschema:"offering family"`
	ID      string              `json:"id" jsonschema:"catalog offering ID"`
	Version int32               `json:"version" jsonschema:"current metadata.version from the public API"`
}

type DeleteCatalogOfferingInput struct {
	Kind CatalogOfferingKind `json:"kind" jsonschema:"offering family"`
	ID   string              `json:"id" jsonschema:"catalog offering ID"`
}

type CatalogOfferingOutput struct {
	Kind      string   `json:"kind"`
	ID        string   `json:"id"`
	Name      string   `json:"name"`
	Version   int32    `json:"version"`
	Published bool     `json:"published"`
	Warnings  []string `json:"warnings,omitempty"`
}

type DeleteCatalogOfferingOutput struct {
	Kind string `json:"kind"`
	ID   string `json:"id"`
}

// handleCreateCatalogOffering creates a catalog offering through its family API.
func handleCreateCatalogOffering(
	clients catalogOfferingClients,
) mcp.ToolHandlerFor[CreateCatalogOfferingInput, CatalogOfferingOutput] {
	return func(
		ctx context.Context, req *mcp.CallToolRequest, input CreateCatalogOfferingInput,
	) (*mcp.CallToolResult, CatalogOfferingOutput, error) {
		input.Kind = CatalogOfferingKind(strings.TrimSpace(string(input.Kind)))
		input.Name = strings.TrimSpace(input.Name)
		input.TemplateID = strings.TrimSpace(input.TemplateID)
		if err := validateCatalogOfferingKind(input.Kind); err != nil {
			return nil, CatalogOfferingOutput{}, safeInvalidRequestError(err)
		}
		if input.Name == "" {
			return nil, CatalogOfferingOutput{}, safeInvalidRequestError(fmt.Errorf("name is required"))
		}
		metadata := publicv1.Metadata_builder{Name: input.Name}
		if err := protovalidate.Validate(metadata.Build()); err != nil {
			return nil, CatalogOfferingOutput{}, safeInvalidRequestError(fmt.Errorf(
				"name %q must be a valid RFC 1123 DNS label (1-63 lowercase letters, digits, or hyphens; must start and end with a letter or digit)",
				input.Name,
			))
		}
		if input.TemplateID == "" {
			return nil, CatalogOfferingOutput{}, safeInvalidRequestError(fmt.Errorf("template_id is required"))
		}

		ctx, cancel := toolCallContext(ctx)
		defer cancel()
		ctx, err := forwardAPIToken(ctx, req)
		if err != nil {
			return nil, CatalogOfferingOutput{}, err
		}

		switch input.Kind {
		case CatalogOfferingKindComputeInstance:
			response, err := clients.computeInstances.Create(ctx,
				publicv1.ComputeInstanceCatalogItemsCreateRequest_builder{
					Object: publicv1.ComputeInstanceCatalogItem_builder{
						Metadata:    metadata.Build(),
						Title:       input.Title,
						Description: input.Description,
						Template: publicv1.ComputeInstanceTemplateReference_builder{
							Id: input.TemplateID,
						}.Build(),
					}.Build(),
				}.Build())
			if err != nil {
				return nil, CatalogOfferingOutput{}, safeCatalogOfferingAPIError(err)
			}
			return nil, catalogOfferingOutput(
				string(input.Kind), "", response.GetObject(), response.GetWarnings(),
			), nil
		case CatalogOfferingKindCluster:
			response, err := clients.clusters.Create(ctx, publicv1.ClusterCatalogItemsCreateRequest_builder{
				Object: publicv1.ClusterCatalogItem_builder{
					Metadata:    metadata.Build(),
					Title:       input.Title,
					Description: input.Description,
					Template: publicv1.ClusterTemplateReference_builder{
						Id: input.TemplateID,
					}.Build(),
				}.Build(),
			}.Build())
			if err != nil {
				return nil, CatalogOfferingOutput{}, safeCatalogOfferingAPIError(err)
			}
			return nil, catalogOfferingOutput(
				string(input.Kind), "", response.GetObject(), nil,
			), nil
		case CatalogOfferingKindBareMetalInstance:
			response, err := clients.bareMetal.Create(ctx,
				publicv1.BareMetalInstanceCatalogItemsCreateRequest_builder{
					Object: publicv1.BareMetalInstanceCatalogItem_builder{
						Metadata:    metadata.Build(),
						Title:       input.Title,
						Description: input.Description,
						Template: publicv1.BareMetalInstanceTemplateReference_builder{
							Id: input.TemplateID,
						}.Build(),
					}.Build(),
				}.Build())
			if err != nil {
				return nil, CatalogOfferingOutput{}, safeCatalogOfferingAPIError(err)
			}
			return nil, catalogOfferingOutput(
				string(input.Kind), "", response.GetObject(), response.GetWarnings(),
			), nil
		default:
			return nil, CatalogOfferingOutput{}, safeInvalidRequestError(fmt.Errorf(
				"unsupported catalog offering kind",
			))
		}
	}
}

// handleUpdateCatalogOffering applies an exact-mask catalog offering update.
func handleUpdateCatalogOffering(
	clients catalogOfferingClients,
) mcp.ToolHandlerFor[UpdateCatalogOfferingInput, CatalogOfferingOutput] {
	return func(
		ctx context.Context, req *mcp.CallToolRequest, input UpdateCatalogOfferingInput,
	) (*mcp.CallToolResult, CatalogOfferingOutput, error) {
		normalized, err := normalizeCatalogOfferingUpdateInput(input)
		if err != nil {
			return nil, CatalogOfferingOutput{}, safeInvalidRequestError(err)
		}
		input = normalized

		ctx, cancel := toolCallContext(ctx)
		defer cancel()
		ctx, err = forwardAPIToken(ctx, req)
		if err != nil {
			return nil, CatalogOfferingOutput{}, err
		}

		paths := make([]string, 0, 2)
		if input.Title != nil {
			paths = append(paths, "title")
		}
		if input.Description != nil {
			paths = append(paths, "description")
		}
		mask := &fieldmaskpb.FieldMask{Paths: paths}
		metadata := publicv1.Metadata_builder{Version: input.Version}

		switch input.Kind {
		case CatalogOfferingKindComputeInstance:
			response, err := clients.computeInstances.Update(ctx,
				publicv1.ComputeInstanceCatalogItemsUpdateRequest_builder{
					Object: publicv1.ComputeInstanceCatalogItem_builder{
						Id:          input.ID,
						Metadata:    metadata.Build(),
						Title:       derefString(input.Title),
						Description: derefString(input.Description),
					}.Build(),
					UpdateMask: mask,
					Lock:       true,
				}.Build())
			if err != nil {
				return nil, CatalogOfferingOutput{}, safeCatalogOfferingAPIError(err)
			}
			return nil, catalogOfferingOutput(
				string(input.Kind), input.ID, response.GetObject(), response.GetWarnings(),
			), nil
		case CatalogOfferingKindCluster:
			response, err := clients.clusters.Update(ctx, publicv1.ClusterCatalogItemsUpdateRequest_builder{
				Object: publicv1.ClusterCatalogItem_builder{
					Id:          input.ID,
					Metadata:    metadata.Build(),
					Title:       derefString(input.Title),
					Description: derefString(input.Description),
				}.Build(),
				UpdateMask: mask,
				Lock:       true,
			}.Build())
			if err != nil {
				return nil, CatalogOfferingOutput{}, safeCatalogOfferingAPIError(err)
			}
			return nil, catalogOfferingOutput(
				string(input.Kind), input.ID, response.GetObject(), nil,
			), nil
		case CatalogOfferingKindBareMetalInstance:
			response, err := clients.bareMetal.Update(ctx,
				publicv1.BareMetalInstanceCatalogItemsUpdateRequest_builder{
					Object: publicv1.BareMetalInstanceCatalogItem_builder{
						Id:          input.ID,
						Metadata:    metadata.Build(),
						Title:       derefString(input.Title),
						Description: derefString(input.Description),
					}.Build(),
					UpdateMask: mask,
					Lock:       true,
				}.Build())
			if err != nil {
				return nil, CatalogOfferingOutput{}, safeCatalogOfferingAPIError(err)
			}
			return nil, catalogOfferingOutput(
				string(input.Kind), input.ID, response.GetObject(), response.GetWarnings(),
			), nil
		default:
			return nil, CatalogOfferingOutput{}, safeInvalidRequestError(fmt.Errorf(
				"unsupported catalog offering kind",
			))
		}
	}
}

// handlePublishCatalogOffering publishes a catalog offering at a known version.
func handlePublishCatalogOffering(
	clients catalogOfferingClients,
) mcp.ToolHandlerFor[PublishCatalogOfferingInput, CatalogOfferingOutput] {
	return func(
		ctx context.Context, req *mcp.CallToolRequest, input PublishCatalogOfferingInput,
	) (*mcp.CallToolResult, CatalogOfferingOutput, error) {
		normalized, err := normalizeCatalogOfferingVersionInput(
			input.Kind, input.ID, input.Version,
		)
		if err != nil {
			return nil, CatalogOfferingOutput{}, safeInvalidRequestError(err)
		}
		input.Kind = normalized.Kind
		input.ID = normalized.ID
		input.Version = normalized.Version

		ctx, cancel := toolCallContext(ctx)
		defer cancel()
		ctx, err = forwardAPIToken(ctx, req)
		if err != nil {
			return nil, CatalogOfferingOutput{}, err
		}

		mask := &fieldmaskpb.FieldMask{Paths: []string{"published"}}
		metadata := publicv1.Metadata_builder{Version: input.Version}

		switch input.Kind {
		case CatalogOfferingKindComputeInstance:
			response, err := clients.computeInstances.Update(ctx,
				publicv1.ComputeInstanceCatalogItemsUpdateRequest_builder{
					Object: publicv1.ComputeInstanceCatalogItem_builder{
						Id:        input.ID,
						Metadata:  metadata.Build(),
						Published: true,
					}.Build(),
					UpdateMask: mask,
					Lock:       true,
				}.Build())
			if err != nil {
				return nil, CatalogOfferingOutput{}, safeCatalogOfferingAPIError(err)
			}
			return nil, catalogOfferingOutput(
				string(input.Kind), input.ID, response.GetObject(), response.GetWarnings(),
			), nil
		case CatalogOfferingKindCluster:
			response, err := clients.clusters.Update(ctx, publicv1.ClusterCatalogItemsUpdateRequest_builder{
				Object: publicv1.ClusterCatalogItem_builder{
					Id:        input.ID,
					Metadata:  metadata.Build(),
					Published: true,
				}.Build(),
				UpdateMask: mask,
				Lock:       true,
			}.Build())
			if err != nil {
				return nil, CatalogOfferingOutput{}, safeCatalogOfferingAPIError(err)
			}
			return nil, catalogOfferingOutput(
				string(input.Kind), input.ID, response.GetObject(), nil,
			), nil
		case CatalogOfferingKindBareMetalInstance:
			response, err := clients.bareMetal.Update(ctx,
				publicv1.BareMetalInstanceCatalogItemsUpdateRequest_builder{
					Object: publicv1.BareMetalInstanceCatalogItem_builder{
						Id:        input.ID,
						Metadata:  metadata.Build(),
						Published: true,
					}.Build(),
					UpdateMask: mask,
					Lock:       true,
				}.Build())
			if err != nil {
				return nil, CatalogOfferingOutput{}, safeCatalogOfferingAPIError(err)
			}
			return nil, catalogOfferingOutput(
				string(input.Kind), input.ID, response.GetObject(), response.GetWarnings(),
			), nil
		default:
			return nil, CatalogOfferingOutput{}, safeInvalidRequestError(fmt.Errorf(
				"unsupported catalog offering kind",
			))
		}
	}
}

// handleDeleteCatalogOffering deletes a catalog offering by family and ID.
func handleDeleteCatalogOffering(
	clients catalogOfferingClients,
) mcp.ToolHandlerFor[DeleteCatalogOfferingInput, DeleteCatalogOfferingOutput] {
	return func(
		ctx context.Context, req *mcp.CallToolRequest, input DeleteCatalogOfferingInput,
	) (*mcp.CallToolResult, DeleteCatalogOfferingOutput, error) {
		input.Kind = CatalogOfferingKind(strings.TrimSpace(string(input.Kind)))
		input.ID = strings.TrimSpace(input.ID)
		if err := validateCatalogOfferingKind(input.Kind); err != nil {
			return nil, DeleteCatalogOfferingOutput{}, safeInvalidRequestError(err)
		}
		if input.ID == "" {
			return nil, DeleteCatalogOfferingOutput{}, safeInvalidRequestError(fmt.Errorf("id is required"))
		}

		ctx, cancel := toolCallContext(ctx)
		defer cancel()
		ctx, err := forwardAPIToken(ctx, req)
		if err != nil {
			return nil, DeleteCatalogOfferingOutput{}, err
		}

		switch input.Kind {
		case CatalogOfferingKindComputeInstance:
			_, err := clients.computeInstances.Delete(ctx,
				publicv1.ComputeInstanceCatalogItemsDeleteRequest_builder{Id: input.ID}.Build())
			if err != nil {
				return nil, DeleteCatalogOfferingOutput{}, safeCatalogOfferingAPIError(err)
			}
		case CatalogOfferingKindCluster:
			_, err := clients.clusters.Delete(ctx,
				publicv1.ClusterCatalogItemsDeleteRequest_builder{Id: input.ID}.Build())
			if err != nil {
				return nil, DeleteCatalogOfferingOutput{}, safeCatalogOfferingAPIError(err)
			}
		case CatalogOfferingKindBareMetalInstance:
			_, err := clients.bareMetal.Delete(ctx,
				publicv1.BareMetalInstanceCatalogItemsDeleteRequest_builder{Id: input.ID}.Build())
			if err != nil {
				return nil, DeleteCatalogOfferingOutput{}, safeCatalogOfferingAPIError(err)
			}
		}
		return nil, DeleteCatalogOfferingOutput{
			Kind: string(input.Kind),
			ID:   input.ID,
		}, nil
	}
}

type catalogOfferingVersionInput struct {
	Kind    CatalogOfferingKind
	ID      string
	Version int32
}

// normalizeCatalogOfferingUpdateInput validates and trims update input.
func normalizeCatalogOfferingUpdateInput(
	input UpdateCatalogOfferingInput,
) (UpdateCatalogOfferingInput, error) {
	normalized, err := normalizeCatalogOfferingVersionInput(input.Kind, input.ID, input.Version)
	if err != nil {
		return UpdateCatalogOfferingInput{}, err
	}
	input.Kind = normalized.Kind
	input.ID = normalized.ID
	input.Version = normalized.Version
	if input.Title == nil && input.Description == nil {
		return UpdateCatalogOfferingInput{}, fmt.Errorf(
			"at least one supported update field is required",
		)
	}
	return input, nil
}

// normalizeCatalogOfferingVersionInput validates versioned offering input.
func normalizeCatalogOfferingVersionInput(
	kind CatalogOfferingKind, id string, version int32,
) (catalogOfferingVersionInput, error) {
	kind = CatalogOfferingKind(strings.TrimSpace(string(kind)))
	if err := validateCatalogOfferingKind(kind); err != nil {
		return catalogOfferingVersionInput{}, err
	}
	id = strings.TrimSpace(id)
	if id == "" {
		return catalogOfferingVersionInput{}, fmt.Errorf("id is required")
	}
	if version < 0 {
		return catalogOfferingVersionInput{}, fmt.Errorf("version must not be negative")
	}
	return catalogOfferingVersionInput{Kind: kind, ID: id, Version: version}, nil
}

// validateCatalogOfferingKind rejects unsupported catalog offering families.
func validateCatalogOfferingKind(kind CatalogOfferingKind) error {
	switch kind {
	case CatalogOfferingKindComputeInstance, CatalogOfferingKindCluster,
		CatalogOfferingKindBareMetalInstance:
		return nil
	default:
		return fmt.Errorf("kind must be compute_instance, cluster, or bare_metal_instance")
	}
}

// safeCatalogOfferingAPIError maps stale and policy failures to safe errors.
func safeCatalogOfferingAPIError(err error) error {
	switch status.Code(err) {
	case codes.Aborted:
		return safeStaleVersionError()
	case codes.FailedPrecondition:
		return safeCatalogPolicyError()
	default:
		return safePublicAPIError(err)
	}
}

// safeCatalogPolicyError returns a non-revealing policy failure.
func safeCatalogPolicyError() error {
	envelope := newToolError(
		"conflict",
		"FailedPrecondition",
		"The catalog offering policy or dependencies are not ready for this operation.",
		false,
	)
	envelope.Details = []string{
		"Resolve catalog policy or dependency failures and retry with the current version.",
	}
	return envelope
}

// derefString converts an optional string to its zero value.
func derefString(value *string) string {
	if value == nil {
		return ""
	}
	return *value
}

// catalogOfferingOutput projects a catalog response into the tool contract.
func catalogOfferingOutput(
	kind, fallbackID string, object interface {
		GetId() string
		GetMetadata() *publicv1.Metadata
		GetPublished() bool
	}, warnings []string,
) CatalogOfferingOutput {
	id := object.GetId()
	if id == "" {
		id = fallbackID
	}
	return CatalogOfferingOutput{
		Kind:      kind,
		ID:        id,
		Name:      object.GetMetadata().GetName(),
		Version:   object.GetMetadata().GetVersion(),
		Published: object.GetPublished(),
		Warnings:  warnings,
	}
}

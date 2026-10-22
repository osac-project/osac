/*
Copyright (c) 2026 Red Hat Inc.

Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except in compliance with the
License. You may obtain a copy of the License at

http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on an
"AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the specific
language governing permissions and limitations under the License.
*/

package mcpserver

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/fieldmaskpb"
	"google.golang.org/protobuf/types/known/timestamppb"

	publicv1 "github.com/osac-project/osac/proto/gen/osac/public/v1"
)

type computeInstanceClients struct {
	instances    publicv1.ComputeInstancesClient
	catalogItems publicv1.ComputeInstanceCatalogItemsClient
	projects     publicv1.ProjectsClient
}

func newComputeInstanceClients(deps ServerDeps) computeInstanceClients {
	return computeInstanceClients{
		instances:    deps.ComputeInstancesClient,
		catalogItems: deps.ComputeInstanceCatalogItemsClient,
		projects:     deps.ProjectsClient,
	}
}

// CreateComputeInstanceInput creates a compute instance from a published catalog item.
// The catalog item is intentionally required in this MCP phase so the server can
// enforce its policy-controlled defaults and permitted overrides.
type CreateComputeInstanceInput struct {
	Name               string                                  `json:"name" jsonschema:"name for the new compute instance"`
	CatalogItem        string                                  `json:"catalog_item" jsonschema:"ID of the compute instance catalog item"`
	DiskImageID        string                                  `json:"disk_image_id,omitempty" jsonschema:"optional ID from list_resources(disk_image); omit to use catalog or template defaults; the catalog item must permit the override"`
	ProjectID          string                                  `json:"project_id,omitempty" jsonschema:"optional ID from list_resources(project); omit to use the caller's default Project"`
	InstanceTypeID     string                                  `json:"instance_type_id,omitempty" jsonschema:"optional ID from list_resources(instance_type); the catalog item must permit the override"`
	BootDisk           *ComputeInstanceBootDiskInput           `json:"boot_disk,omitempty" jsonschema:"optional object with size_gib and/or storage_tier_id; example: {\"size_gib\":20,\"storage_tier_id\":\"<storage-tier-id>\"}; copy the ID from list_resources(storage_tier), not its name or a nested storageTier object; omit to use catalog defaults; the catalog item must permit each supplied field"`
	NetworkAttachments []ComputeInstanceNetworkAttachmentInput `json:"network_attachments,omitempty" jsonschema:"optional array of objects, maximum one attachment; example: [{\"subnet_id\":\"subnet-id\",\"security_group_ids\":[\"security-group-id\"]}]; omit to use the catalog or tenant default network"`
}

const maxPreflightSecurityGroups = 100

// ComputeInstanceBootDiskInput selects values already visible through
// list_resources. Omitted fields stay absent so the catalog item's defaults
// and locked values remain authoritative.
type ComputeInstanceBootDiskInput struct {
	SizeGiB       *int32 `json:"size_gib,omitempty" jsonschema:"optional boot disk size in GiB; must be greater than zero"`
	StorageTierID string `json:"storage_tier_id,omitempty" jsonschema:"optional ID from list_resources(storage_tier)"`
}

// ComputeInstanceNetworkAttachmentInput is one NIC. Subnet and security group
// IDs must come from the caller's tenant-visible list_resources results.
type ComputeInstanceNetworkAttachmentInput struct {
	SubnetID         string   `json:"subnet_id" jsonschema:"ID from list_resources(subnet)"`
	SecurityGroupIDs []string `json:"security_group_ids,omitempty" jsonschema:"optional IDs from list_resources(security_group)"`
}

type CreateComputeInstanceOutput struct {
	ID      string `json:"id"`
	Version int32  `json:"version"`
	State   string `json:"state"`
}

type UpdateComputeInstanceInput struct {
	ID             string                        `json:"id" jsonschema:"compute instance ID from get_resource"`
	Version        int32                         `json:"version" jsonschema:"current metadata.version from get_resource; must be greater than zero"`
	InstanceTypeID string                        `json:"instance_type_id,omitempty" jsonschema:"optional ID from list_resources(instance_type); the catalog item must permit the update"`
	BootDisk       *ComputeInstanceBootDiskInput `json:"boot_disk,omitempty" jsonschema:"optional object with size_gib and/or storage_tier_id; each supplied field is updated and must be permitted by the catalog item"`
	RunStrategy    string                        `json:"run_strategy,omitempty" jsonschema:"optional run strategy: ALWAYS or HALTED"`
}

type UpdateComputeInstanceOutput struct {
	ID      string `json:"id"`
	Version int32  `json:"version"`
	State   string `json:"state"`
}

type SetComputeInstancePowerInput struct {
	ID      string `json:"id" jsonschema:"compute instance ID from get_resource"`
	Version int32  `json:"version" jsonschema:"current metadata.version from get_resource; must be greater than zero"`
	Action  string `json:"action" jsonschema:"power action: start or stop"`
}

type SetComputeInstancePowerOutput struct {
	ID          string `json:"id"`
	Version     int32  `json:"version"`
	State       string `json:"state"`
	RunStrategy string `json:"run_strategy"`
}

type RestartComputeInstanceInput struct {
	ID      string `json:"id" jsonschema:"compute instance ID from get_resource"`
	Version int32  `json:"version" jsonschema:"current metadata.version from get_resource; must be greater than zero"`
}

type RestartComputeInstanceOutput struct {
	ID                 string `json:"id"`
	Version            int32  `json:"version"`
	State              string `json:"state"`
	RestartRequestedAt string `json:"restart_requested_at"`
}

type DeleteComputeInstanceInput struct {
	ID string `json:"id" jsonschema:"compute instance ID"`
}

type DeleteComputeInstanceOutput struct {
	ID string `json:"id"`
}

func handleCreateComputeInstance(
	clients computeInstanceClients,
) mcp.ToolHandlerFor[CreateComputeInstanceInput, CreateComputeInstanceOutput] {
	return func(
		ctx context.Context, req *mcp.CallToolRequest, input CreateComputeInstanceInput,
	) (*mcp.CallToolResult, CreateComputeInstanceOutput, error) {
		var err error
		input, err = normalizeCreateComputeInstanceInput(input)
		if err != nil {
			return nil, CreateComputeInstanceOutput{}, safeInvalidRequestError(err)
		}
		networkAttachment, err := validateComputeInstanceNetworkAttachments(input.NetworkAttachments)
		if err != nil {
			return nil, CreateComputeInstanceOutput{}, safeInvalidRequestError(err)
		}
		spec, err := computeInstanceSpecFromInput(input, networkAttachment)
		if err != nil {
			return nil, CreateComputeInstanceOutput{}, safeInvalidRequestError(err)
		}
		ctx, cancel := toolCallContext(ctx)
		defer cancel()
		ctx, err = forwardAPIToken(ctx, req)
		if err != nil {
			return nil, CreateComputeInstanceOutput{}, err
		}
		project, err := lookupCreateComputeInstanceProject(ctx, clients.projects, input.ProjectID)
		if err != nil {
			return nil, CreateComputeInstanceOutput{}, err
		}
		response, err := clients.instances.Create(ctx, publicv1.ComputeInstancesCreateRequest_builder{
			Object: publicv1.ComputeInstance_builder{
				Metadata: publicv1.Metadata_builder{
					Name: input.Name, Tenant: project.tenant, Project: project.name,
				}.Build(),
				Spec: spec,
			}.Build(),
		}.Build())
		if err != nil {
			return nil, CreateComputeInstanceOutput{}, safeCreateComputeInstanceError(err)
		}
		created := response.GetObject()
		return nil, CreateComputeInstanceOutput{
			ID:      created.GetId(),
			Version: created.GetMetadata().GetVersion(),
			State:   created.GetStatus().GetState().String(),
		}, nil
	}
}

type createComputeInstanceProject struct {
	tenant string
	name   string
}

func lookupCreateComputeInstanceProject(
	ctx context.Context, client publicv1.ProjectsClient, projectID string,
) (createComputeInstanceProject, error) {
	if projectID == "" {
		return createComputeInstanceProject{}, nil
	}
	filter := fmt.Sprintf("this.id == %q", projectID)
	response, err := client.List(ctx, publicv1.ProjectsListRequest_builder{
		Filter: new(filter),
		Limit:  new(int32(2)),
	}.Build())
	if err != nil {
		return createComputeInstanceProject{}, safePrerequisiteError("Project", projectID, err)
	}
	items := response.GetItems()
	if len(items) != 1 {
		return createComputeInstanceProject{}, safeMissingPrerequisiteError("Project", projectID)
	}
	project := items[0]
	return createComputeInstanceProject{
		tenant: project.GetMetadata().GetTenant(),
		name:   project.GetMetadata().GetName(),
	}, nil
}

func safeCreateComputeInstanceError(err error) error {
	if isMissingReferenceError(err) {
		return safeMissingReferenceError()
	}
	return safePublicAPIError(err)
}

func isMissingReferenceError(err error) bool {
	if status.Code(err) != codes.InvalidArgument {
		return false
	}
	message := strings.ToLower(err.Error())
	return strings.Contains(message, "reference validation failed") &&
		strings.Contains(message, "not found")
}

func normalizeCreateComputeInstanceInput(input CreateComputeInstanceInput) (CreateComputeInstanceInput, error) {
	input.Name = strings.TrimSpace(input.Name)
	input.CatalogItem = strings.TrimSpace(input.CatalogItem)
	input.DiskImageID = strings.TrimSpace(input.DiskImageID)
	input.ProjectID = strings.TrimSpace(input.ProjectID)
	input.InstanceTypeID = strings.TrimSpace(input.InstanceTypeID)
	if input.Name == "" {
		return CreateComputeInstanceInput{}, fmt.Errorf("name is required")
	}
	if input.CatalogItem == "" {
		return CreateComputeInstanceInput{}, fmt.Errorf("catalog_item is required")
	}
	if input.BootDisk != nil {
		input.BootDisk = &ComputeInstanceBootDiskInput{
			SizeGiB:       input.BootDisk.SizeGiB,
			StorageTierID: strings.TrimSpace(input.BootDisk.StorageTierID),
		}
	}
	if input.NetworkAttachments != nil {
		for attachmentIndex := range input.NetworkAttachments {
			input.NetworkAttachments[attachmentIndex].SubnetID =
				strings.TrimSpace(input.NetworkAttachments[attachmentIndex].SubnetID)
			for securityGroupIndex := range input.NetworkAttachments[attachmentIndex].SecurityGroupIDs {
				input.NetworkAttachments[attachmentIndex].SecurityGroupIDs[securityGroupIndex] =
					strings.TrimSpace(input.NetworkAttachments[attachmentIndex].SecurityGroupIDs[securityGroupIndex])
			}
		}
	}
	return input, nil
}

func validateComputeInstanceNetworkAttachments(
	input []ComputeInstanceNetworkAttachmentInput,
) (*publicv1.ComputeNetworkAttachment, error) {
	if input == nil {
		return nil, nil
	}
	if len(input) == 0 {
		return nil, fmt.Errorf("network_attachments must contain at least one attachment when supplied")
	}
	if len(input) > 1 {
		return nil, fmt.Errorf("network_attachments must contain at most one attachment")
	}
	if input[0].SubnetID == "" {
		return nil, fmt.Errorf("network_attachments[0].subnet_id is required")
	}
	if len(input[0].SecurityGroupIDs) > maxPreflightSecurityGroups {
		return nil, fmt.Errorf(
			"network_attachments must reference at most %d security groups", maxPreflightSecurityGroups,
		)
	}
	uniqueSecurityGroupIDs := make([]string, 0, maxPreflightSecurityGroups)
	seenSecurityGroupIDs := make(map[string]struct{}, maxPreflightSecurityGroups+1)
	for securityGroupIndex, securityGroupID := range input[0].SecurityGroupIDs {
		if securityGroupID == "" {
			return nil, fmt.Errorf(
				"network_attachments[0].security_group_ids[%d] is required", securityGroupIndex,
			)
		}
		if _, exists := seenSecurityGroupIDs[securityGroupID]; exists {
			continue
		}
		seenSecurityGroupIDs[securityGroupID] = struct{}{}
		uniqueSecurityGroupIDs = append(uniqueSecurityGroupIDs, securityGroupID)
	}
	securityGroups := make([]*publicv1.SecurityGroupLocalReference, len(uniqueSecurityGroupIDs))
	for securityGroupIndex, securityGroupID := range uniqueSecurityGroupIDs {
		securityGroups[securityGroupIndex] =
			publicv1.SecurityGroupLocalReference_builder{Id: securityGroupID}.Build()
	}
	attachment := publicv1.ComputeNetworkAttachment_builder{
		Subnet:         publicv1.SubnetLocalReference_builder{Id: input[0].SubnetID}.Build(),
		SecurityGroups: securityGroups,
	}.Build()
	return attachment, nil
}

func computeInstanceSpecFromInput(
	input CreateComputeInstanceInput, networkAttachment *publicv1.ComputeNetworkAttachment,
) (*publicv1.ComputeInstanceSpec, error) {
	builder := publicv1.ComputeInstanceSpec_builder{
		CatalogItem: publicv1.ComputeInstanceCatalogItemReference_builder{Id: input.CatalogItem}.Build(),
	}
	if instanceTypeID := strings.TrimSpace(input.InstanceTypeID); instanceTypeID != "" {
		builder.InstanceType = publicv1.InstanceTypeReference_builder{Id: instanceTypeID}.Build()
	}
	if diskImageID := strings.TrimSpace(input.DiskImageID); diskImageID != "" {
		builder.DiskImage = publicv1.DiskImageReference_builder{Id: diskImageID}.Build()
	}
	if input.BootDisk != nil {
		bootDisk, err := computeInstanceBootDiskFromInput(*input.BootDisk)
		if err != nil {
			return nil, err
		}
		builder.BootDisk = bootDisk
	}
	if networkAttachment != nil {
		builder.NetworkAttachments = []*publicv1.ComputeNetworkAttachment{networkAttachment}
	}
	return builder.Build(), nil
}

func computeInstanceBootDiskFromInput(input ComputeInstanceBootDiskInput) (*publicv1.ComputeInstanceDisk, error) {
	storageTierID := strings.TrimSpace(input.StorageTierID)
	if input.SizeGiB == nil && storageTierID == "" {
		return nil, fmt.Errorf("boot_disk must include size_gib and/or storage_tier_id")
	}
	if input.SizeGiB != nil && *input.SizeGiB <= 0 {
		return nil, fmt.Errorf("boot_disk.size_gib must be greater than zero")
	}
	builder := publicv1.ComputeInstanceDisk_builder{SizeGib: input.SizeGiB}
	if storageTierID != "" {
		builder.StorageTier = publicv1.StorageTierReference_builder{Id: storageTierID}.Build()
	}
	return builder.Build(), nil
}

func handleUpdateComputeInstance(
	clients computeInstanceClients,
) mcp.ToolHandlerFor[UpdateComputeInstanceInput, UpdateComputeInstanceOutput] {
	return func(
		ctx context.Context, req *mcp.CallToolRequest, input UpdateComputeInstanceInput,
	) (*mcp.CallToolResult, UpdateComputeInstanceOutput, error) {
		var err error
		input, err = normalizeUpdateComputeInstanceInput(input)
		if err != nil {
			return nil, UpdateComputeInstanceOutput{}, safeInvalidRequestError(err)
		}
		ctx, cancel := toolCallContext(ctx)
		defer cancel()
		ctx, err = forwardAPIToken(ctx, req)
		if err != nil {
			return nil, UpdateComputeInstanceOutput{}, err
		}

		current, err := getComputeInstanceAtVersion(ctx, clients.instances, input.ID, input.Version)
		if err != nil {
			return nil, UpdateComputeInstanceOutput{}, err
		}

		catalogItemID := current.GetSpec().GetCatalogItem().GetId()
		if catalogItemID != "" {
			catalogResponse, catalogErr := clients.catalogItems.Get(ctx,
				publicv1.ComputeInstanceCatalogItemsGetRequest_builder{Id: catalogItemID}.Build())
			if catalogErr != nil {
				return nil, UpdateComputeInstanceOutput{}, safePrerequisiteError("catalog item", catalogItemID, catalogErr)
			}
			if err := validateUpdateAgainstCatalogItem(input, catalogResponse.GetObject().GetFields()); err != nil {
				return nil, UpdateComputeInstanceOutput{}, err
			}
		}

		paths := make([]string, 0, 3)
		metadata := publicv1.Metadata_builder{Version: input.Version}
		spec := publicv1.ComputeInstanceSpec_builder{}
		if input.InstanceTypeID != "" {
			paths = append(paths, "spec.instance_type")
			spec.InstanceType = publicv1.InstanceTypeReference_builder{Id: input.InstanceTypeID}.Build()
		}
		if input.BootDisk != nil {
			bootDisk, bootDiskErr := computeInstanceBootDiskFromInput(*input.BootDisk)
			if bootDiskErr != nil {
				return nil, UpdateComputeInstanceOutput{}, safeInvalidRequestError(bootDiskErr)
			}
			spec.BootDisk = bootDisk
			if input.BootDisk.SizeGiB != nil {
				paths = append(paths, "spec.boot_disk.size_gib")
			}
			if input.BootDisk.StorageTierID != "" {
				paths = append(paths, "spec.boot_disk.storage_tier")
			}
		}
		if input.RunStrategy != "" {
			paths = append(paths, "spec.run_strategy")
			spec.RunStrategy = parseComputeInstanceRunStrategy(input.RunStrategy).Enum()
		}
		response, err := clients.instances.Update(ctx, publicv1.ComputeInstancesUpdateRequest_builder{
			Object: publicv1.ComputeInstance_builder{
				Id:       input.ID,
				Metadata: metadata.Build(),
				Spec:     spec.Build(),
			}.Build(),
			UpdateMask: &fieldmaskpb.FieldMask{Paths: paths},
			Lock:       true,
		}.Build())
		if err != nil {
			return nil, UpdateComputeInstanceOutput{}, safeComputeInstanceUpdateError(err)
		}
		updated := response.GetObject()
		updatedID := updated.GetId()
		if updatedID == "" {
			updatedID = input.ID
		}
		return nil, UpdateComputeInstanceOutput{
			ID:      updatedID,
			Version: updated.GetMetadata().GetVersion(),
			State:   updated.GetStatus().GetState().String(),
		}, nil
	}
}

func getComputeInstanceAtVersion(
	ctx context.Context, client publicv1.ComputeInstancesClient, id string, version int32,
) (*publicv1.ComputeInstance, error) {
	response, err := client.Get(ctx, publicv1.ComputeInstancesGetRequest_builder{Id: id}.Build())
	if err != nil {
		return nil, safePrerequisiteError("ComputeInstance", id, err)
	}
	current := response.GetObject()
	if current.GetMetadata().GetVersion() != version {
		return nil, safeStaleVersionError()
	}
	return current, nil
}

func safeComputeInstanceUpdateError(err error) error {
	if status.Code(err) == codes.Aborted {
		return safeStaleVersionError()
	}
	return safePublicAPIError(err)
}

func handleSetComputeInstancePower(
	client publicv1.ComputeInstancesClient,
) mcp.ToolHandlerFor[SetComputeInstancePowerInput, SetComputeInstancePowerOutput] {
	return func(
		ctx context.Context, req *mcp.CallToolRequest, input SetComputeInstancePowerInput,
	) (*mcp.CallToolResult, SetComputeInstancePowerOutput, error) {
		var err error
		input, err = normalizeSetComputeInstancePowerInput(input)
		if err != nil {
			return nil, SetComputeInstancePowerOutput{}, safeInvalidRequestError(err)
		}
		ctx, cancel := toolCallContext(ctx)
		defer cancel()
		ctx, err = forwardAPIToken(ctx, req)
		if err != nil {
			return nil, SetComputeInstancePowerOutput{}, err
		}
		if _, err = getComputeInstanceAtVersion(ctx, client, input.ID, input.Version); err != nil {
			return nil, SetComputeInstancePowerOutput{}, err
		}

		runStrategy := publicv1.ComputeInstanceRunStrategy_COMPUTE_INSTANCE_RUN_STRATEGY_ALWAYS
		if input.Action == "stop" {
			runStrategy = publicv1.ComputeInstanceRunStrategy_COMPUTE_INSTANCE_RUN_STRATEGY_HALTED
		}
		response, err := client.Update(ctx, publicv1.ComputeInstancesUpdateRequest_builder{
			Object: publicv1.ComputeInstance_builder{
				Id:       input.ID,
				Metadata: publicv1.Metadata_builder{Version: input.Version}.Build(),
				Spec: publicv1.ComputeInstanceSpec_builder{
					RunStrategy: runStrategy.Enum(),
				}.Build(),
			}.Build(),
			UpdateMask: &fieldmaskpb.FieldMask{Paths: []string{"spec.run_strategy"}},
			Lock:       true,
		}.Build())
		if err != nil {
			return nil, SetComputeInstancePowerOutput{}, safeComputeInstanceUpdateError(err)
		}
		updated := response.GetObject()
		updatedID := updated.GetId()
		if updatedID == "" {
			updatedID = input.ID
		}
		return nil, SetComputeInstancePowerOutput{
			ID:          updatedID,
			Version:     updated.GetMetadata().GetVersion(),
			State:       updated.GetStatus().GetState().String(),
			RunStrategy: computeInstanceRunStrategyName(updated.GetSpec().GetRunStrategy()),
		}, nil
	}
}

func handleRestartComputeInstance(
	client publicv1.ComputeInstancesClient,
) mcp.ToolHandlerFor[RestartComputeInstanceInput, RestartComputeInstanceOutput] {
	return func(
		ctx context.Context, req *mcp.CallToolRequest, input RestartComputeInstanceInput,
	) (*mcp.CallToolResult, RestartComputeInstanceOutput, error) {
		var err error
		input, err = normalizeRestartComputeInstanceInput(input)
		if err != nil {
			return nil, RestartComputeInstanceOutput{}, safeInvalidRequestError(err)
		}
		ctx, cancel := toolCallContext(ctx)
		defer cancel()
		ctx, err = forwardAPIToken(ctx, req)
		if err != nil {
			return nil, RestartComputeInstanceOutput{}, err
		}
		if _, err = getComputeInstanceAtVersion(ctx, client, input.ID, input.Version); err != nil {
			return nil, RestartComputeInstanceOutput{}, err
		}

		restartRequestedAt := timestamppb.New(time.Now().UTC())
		response, err := client.Update(ctx, publicv1.ComputeInstancesUpdateRequest_builder{
			Object: publicv1.ComputeInstance_builder{
				Id:       input.ID,
				Metadata: publicv1.Metadata_builder{Version: input.Version}.Build(),
				Spec: publicv1.ComputeInstanceSpec_builder{
					RestartRequestedAt: restartRequestedAt,
				}.Build(),
			}.Build(),
			UpdateMask: &fieldmaskpb.FieldMask{Paths: []string{"spec.restart_requested_at"}},
			Lock:       true,
		}.Build())
		if err != nil {
			return nil, RestartComputeInstanceOutput{}, safeComputeInstanceUpdateError(err)
		}
		updated := response.GetObject()
		updatedID := updated.GetId()
		if updatedID == "" {
			updatedID = input.ID
		}
		updatedRestartRequestedAt := updated.GetSpec().GetRestartRequestedAt()
		if updatedRestartRequestedAt == nil {
			updatedRestartRequestedAt = restartRequestedAt
		}
		return nil, RestartComputeInstanceOutput{
			ID:                 updatedID,
			Version:            updated.GetMetadata().GetVersion(),
			State:              updated.GetStatus().GetState().String(),
			RestartRequestedAt: updatedRestartRequestedAt.AsTime().UTC().Format(time.RFC3339Nano),
		}, nil
	}
}

func normalizeSetComputeInstancePowerInput(input SetComputeInstancePowerInput) (SetComputeInstancePowerInput, error) {
	input.ID = strings.TrimSpace(input.ID)
	input.Action = strings.ToLower(strings.TrimSpace(input.Action))
	if input.ID == "" {
		return SetComputeInstancePowerInput{}, fmt.Errorf("id is required")
	}
	if input.Version < 1 {
		return SetComputeInstancePowerInput{}, fmt.Errorf("version must be greater than zero")
	}
	if input.Action != "start" && input.Action != "stop" {
		return SetComputeInstancePowerInput{}, fmt.Errorf("action must be start or stop")
	}
	return input, nil
}

func normalizeRestartComputeInstanceInput(input RestartComputeInstanceInput) (RestartComputeInstanceInput, error) {
	input.ID = strings.TrimSpace(input.ID)
	if input.ID == "" {
		return RestartComputeInstanceInput{}, fmt.Errorf("id is required")
	}
	if input.Version < 1 {
		return RestartComputeInstanceInput{}, fmt.Errorf("version must be greater than zero")
	}
	return input, nil
}

func computeInstanceRunStrategyName(value publicv1.ComputeInstanceRunStrategy) string {
	switch value {
	case publicv1.ComputeInstanceRunStrategy_COMPUTE_INSTANCE_RUN_STRATEGY_ALWAYS:
		return "ALWAYS"
	case publicv1.ComputeInstanceRunStrategy_COMPUTE_INSTANCE_RUN_STRATEGY_HALTED:
		return "HALTED"
	default:
		return "UNSPECIFIED"
	}
}

func normalizeUpdateComputeInstanceInput(input UpdateComputeInstanceInput) (UpdateComputeInstanceInput, error) {
	input.ID = strings.TrimSpace(input.ID)
	input.InstanceTypeID = strings.TrimSpace(input.InstanceTypeID)
	input.RunStrategy = strings.ToUpper(strings.TrimSpace(input.RunStrategy))
	if input.ID == "" {
		return UpdateComputeInstanceInput{}, fmt.Errorf("id is required")
	}
	if input.Version < 1 {
		return UpdateComputeInstanceInput{}, fmt.Errorf("version must be greater than zero")
	}
	if input.InstanceTypeID == "" && input.BootDisk == nil && input.RunStrategy == "" {
		return UpdateComputeInstanceInput{}, fmt.Errorf("at least one supported update field is required")
	}
	if input.RunStrategy != "" && parseComputeInstanceRunStrategy(input.RunStrategy) ==
		publicv1.ComputeInstanceRunStrategy_COMPUTE_INSTANCE_RUN_STRATEGY_UNSPECIFIED {
		return UpdateComputeInstanceInput{}, fmt.Errorf(
			"run_strategy must be ALWAYS or HALTED",
		)
	}
	if input.BootDisk != nil {
		input.BootDisk = &ComputeInstanceBootDiskInput{
			SizeGiB:       input.BootDisk.SizeGiB,
			StorageTierID: strings.TrimSpace(input.BootDisk.StorageTierID),
		}
	}
	return input, nil
}

func parseComputeInstanceRunStrategy(value string) publicv1.ComputeInstanceRunStrategy {
	switch value {
	case "ALWAYS":
		return publicv1.ComputeInstanceRunStrategy_COMPUTE_INSTANCE_RUN_STRATEGY_ALWAYS
	case "HALTED":
		return publicv1.ComputeInstanceRunStrategy_COMPUTE_INSTANCE_RUN_STRATEGY_HALTED
	default:
		return publicv1.ComputeInstanceRunStrategy_COMPUTE_INSTANCE_RUN_STRATEGY_UNSPECIFIED
	}
}

func validateUpdateAgainstCatalogItem(
	input UpdateComputeInstanceInput, fields *publicv1.ComputeInstanceCatalogItemFields,
) error {
	if fields == nil {
		return nil
	}
	if input.InstanceTypeID != "" && fields.GetInstanceType().GetEditable() == nil {
		return safeInvalidRequestError(fmt.Errorf("instance_type is locked by the selected catalog item"))
	}
	if input.RunStrategy != "" && fields.GetRunStrategy().GetEditable() == nil {
		return safeInvalidRequestError(fmt.Errorf("run_strategy is locked by the selected catalog item"))
	}
	if input.BootDisk != nil {
		if input.BootDisk.SizeGiB != nil && fields.GetBootDisk().GetSizeGib().GetEditable() == nil {
			return safeInvalidRequestError(fmt.Errorf("boot_disk.size_gib is locked by the selected catalog item"))
		}
		if input.BootDisk.StorageTierID != "" && fields.GetBootDisk().GetStorageTier().GetEditable() == nil {
			return safeInvalidRequestError(fmt.Errorf(
				"boot_disk.storage_tier_id is locked by the selected catalog item",
			))
		}
	}
	return nil
}

func handleDeleteComputeInstance(
	client publicv1.ComputeInstancesClient,
) mcp.ToolHandlerFor[DeleteComputeInstanceInput, DeleteComputeInstanceOutput] {
	return func(
		ctx context.Context, req *mcp.CallToolRequest, input DeleteComputeInstanceInput,
	) (*mcp.CallToolResult, DeleteComputeInstanceOutput, error) {
		var err error
		input.ID = strings.TrimSpace(input.ID)
		if input.ID == "" {
			return nil, DeleteComputeInstanceOutput{}, safeInvalidRequestError(
				fmt.Errorf("id is required"),
			)
		}
		ctx, cancel := toolCallContext(ctx)
		defer cancel()
		ctx, err = forwardAPIToken(ctx, req)
		if err != nil {
			return nil, DeleteComputeInstanceOutput{}, err
		}
		_, err = client.Delete(ctx, publicv1.ComputeInstancesDeleteRequest_builder{Id: input.ID}.Build())
		if err != nil {
			return nil, DeleteComputeInstanceOutput{}, safePublicAPIError(err)
		}
		return nil, DeleteComputeInstanceOutput(input), nil
	}
}

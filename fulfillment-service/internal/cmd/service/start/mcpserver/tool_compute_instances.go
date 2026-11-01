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

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/fieldmaskpb"

	publicv1 "github.com/osac-project/osac/proto/gen/osac/public/v1"
)

type computeInstanceClients struct {
	instances      publicv1.ComputeInstancesClient
	catalogItems   publicv1.ComputeInstanceCatalogItemsClient
	diskImages     publicv1.DiskImagesClient
	projects       publicv1.ProjectsClient
	instanceTypes  publicv1.InstanceTypesClient
	storageTiers   publicv1.StorageTiersClient
	subnets        publicv1.SubnetsClient
	securityGroups publicv1.SecurityGroupsClient
}

func newComputeInstanceClients(deps ServerDeps) computeInstanceClients {
	return computeInstanceClients{
		instances:      deps.ComputeInstancesClient,
		catalogItems:   deps.ComputeInstanceCatalogItemsClient,
		diskImages:     deps.DiskImagesClient,
		projects:       deps.ProjectsClient,
		instanceTypes:  deps.InstanceTypesClient,
		storageTiers:   deps.StorageTiersClient,
		subnets:        deps.SubnetsClient,
		securityGroups: deps.SecurityGroupsClient,
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
	SubnetID         string   `json:"subnet_id,omitempty" jsonschema:"optional ID from list_resources(subnet); omit to use the tenant default Subnet"`
	SecurityGroupIDs []string `json:"security_group_ids,omitempty" jsonschema:"optional IDs from list_resources(security_group)"`
}

type CreateComputeInstanceOutput struct {
	ID      string `json:"id"`
	Version int32  `json:"version"`
	State   string `json:"state"`
}

type UpdateComputeInstanceInput struct {
	ID             string `json:"id" jsonschema:"compute instance ID from get_resource"`
	Version        int32  `json:"version" jsonschema:"current metadata.version from get_resource; must be greater than zero"`
	InstanceTypeID string `json:"instance_type_id,omitempty" jsonschema:"optional ID from list_resources(instance_type); the catalog item must permit the update"`
	RunStrategy    string `json:"run_strategy,omitempty" jsonschema:"optional run strategy: ALWAYS or HALTED"`
}

type UpdateComputeInstanceOutput struct {
	ID       string   `json:"id"`
	Version  int32    `json:"version"`
	State    string   `json:"state"`
	Warnings []string `json:"warnings,omitempty"`
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
		spec, err := computeInstanceSpecFromInput(input)
		if err != nil {
			return nil, CreateComputeInstanceOutput{}, safeInvalidRequestError(err)
		}
		ctx, cancel := toolCallContext(ctx)
		defer cancel()
		ctx, err = forwardAPIToken(ctx, req)
		if err != nil {
			return nil, CreateComputeInstanceOutput{}, err
		}
		project, err := preflightCreateComputeInstancePrerequisites(ctx, clients, input)
		if err != nil {
			return nil, CreateComputeInstanceOutput{}, err
		}
		metadata := publicv1.Metadata_builder{Name: input.Name}
		if project != nil {
			metadata.Tenant = project.GetMetadata().GetTenant()
			metadata.Project = project.GetMetadata().GetName()
		}
		response, err := clients.instances.Create(ctx, publicv1.ComputeInstancesCreateRequest_builder{
			Object: publicv1.ComputeInstance_builder{
				Metadata: metadata.Build(),
				Spec:     spec,
			}.Build(),
		}.Build())
		if err != nil {
			return nil, CreateComputeInstanceOutput{}, safePublicAPIError(err)
		}
		created := response.GetObject()
		return nil, CreateComputeInstanceOutput{
			ID:      created.GetId(),
			Version: created.GetMetadata().GetVersion(),
			State:   created.GetStatus().GetState().String(),
		}, nil
	}
}

func preflightCreateComputeInstancePrerequisites(
	ctx context.Context, clients computeInstanceClients, input CreateComputeInstanceInput,
) (*publicv1.Project, error) {
	catalogResponse, err := clients.catalogItems.Get(ctx, publicv1.ComputeInstanceCatalogItemsGetRequest_builder{
		Id: input.CatalogItem,
	}.Build())
	if err != nil {
		return nil, safePrerequisiteError("catalog item", input.CatalogItem, err)
	}
	if !catalogResponse.GetObject().GetPublished() {
		return nil, safeInvalidRequestError(fmt.Errorf(
			"catalog item %s is not published", input.CatalogItem,
		))
	}

	if input.DiskImageID != "" {
		imageResponse, imageErr := clients.diskImages.Get(ctx, publicv1.DiskImagesGetRequest_builder{
			Id: input.DiskImageID,
		}.Build())
		if imageErr != nil {
			return nil, safePrerequisiteError("disk image", input.DiskImageID, imageErr)
		}
		if imageResponse.GetObject().GetSpec().GetLifecycle() == publicv1.DiskImageLifecycle_DISK_IMAGE_LIFECYCLE_OBSOLETE {
			return nil, safeInvalidRequestError(fmt.Errorf(
				"disk image %s is obsolete", input.DiskImageID,
			))
		}
	}

	var selectedProject *publicv1.Project
	if input.ProjectID != "" {
		filter := fmt.Sprintf("this.id == %q", input.ProjectID)
		projectResponse, projectErr := clients.projects.List(ctx, publicv1.ProjectsListRequest_builder{
			Filter: &filter,
		}.Build())
		if projectErr != nil {
			return nil, safePrerequisiteError("Project", input.ProjectID, projectErr)
		}
		if len(projectResponse.GetItems()) != 1 {
			return nil, safePrerequisiteError(
				"Project", input.ProjectID, status.Error(codes.NotFound, "project is not visible"),
			)
		}
		project := projectResponse.GetItems()[0]
		if project.GetStatus().GetState() != publicv1.ProjectState_PROJECT_STATE_ACTIVE {
			return nil, safeInvalidRequestError(fmt.Errorf(
				"project %s is not active", input.ProjectID,
			))
		}
		selectedProject = project
	}

	if input.InstanceTypeID != "" {
		_, instanceTypeErr := clients.instanceTypes.Get(ctx, publicv1.InstanceTypesGetRequest_builder{
			Id: input.InstanceTypeID,
		}.Build())
		if instanceTypeErr != nil {
			return nil, safePrerequisiteError("instance type", input.InstanceTypeID, instanceTypeErr)
		}
	}
	if input.BootDisk != nil && input.BootDisk.StorageTierID != "" {
		_, storageTierErr := clients.storageTiers.Get(ctx, publicv1.StorageTiersGetRequest_builder{
			Id: input.BootDisk.StorageTierID,
		}.Build())
		if storageTierErr != nil {
			return nil, safePrerequisiteError("storage tier", input.BootDisk.StorageTierID, storageTierErr)
		}
	}

	securityGroupIDs := make([]string, 0)
	for _, attachment := range input.NetworkAttachments {
		securityGroupIDs = append(securityGroupIDs, attachment.SecurityGroupIDs...)
	}
	if len(securityGroupIDs) > 0 {
		uniqueSecurityGroupIDs := make([]string, 0, len(securityGroupIDs))
		seenSecurityGroupIDs := make(map[string]struct{}, len(securityGroupIDs))
		for _, securityGroupID := range securityGroupIDs {
			if _, exists := seenSecurityGroupIDs[securityGroupID]; exists {
				continue
			}
			seenSecurityGroupIDs[securityGroupID] = struct{}{}
			uniqueSecurityGroupIDs = append(uniqueSecurityGroupIDs, securityGroupID)
		}
		if len(uniqueSecurityGroupIDs) > maxPreflightSecurityGroups {
			return nil, safeInvalidRequestError(fmt.Errorf(
				"network_attachments must reference at most %d unique security groups", maxPreflightSecurityGroups,
			))
		}
		quotedSecurityGroupIDs := make([]string, len(uniqueSecurityGroupIDs))
		for i, securityGroupID := range uniqueSecurityGroupIDs {
			quotedSecurityGroupIDs[i] = fmt.Sprintf("%q", securityGroupID)
		}
		filter := fmt.Sprintf("this.id in [%s]", strings.Join(quotedSecurityGroupIDs, ","))
		limit := int32(maxPreflightSecurityGroups)
		response, listErr := clients.securityGroups.List(ctx, publicv1.SecurityGroupsListRequest_builder{
			Filter: &filter,
			Limit:  &limit,
		}.Build())
		if listErr != nil {
			return nil, safePrerequisiteError("SecurityGroup", uniqueSecurityGroupIDs[0], listErr)
		}
		visibleSecurityGroups := make(map[string]struct{}, len(response.GetItems()))
		for _, securityGroup := range response.GetItems() {
			visibleSecurityGroups[securityGroup.GetId()] = struct{}{}
		}
		for _, securityGroupID := range uniqueSecurityGroupIDs {
			if _, visible := visibleSecurityGroups[securityGroupID]; !visible {
				return nil, safePrerequisiteError(
					"SecurityGroup", securityGroupID, status.Error(codes.NotFound, "security group is not visible"),
				)
			}
		}
	}

	for _, attachment := range input.NetworkAttachments {
		if attachment.SubnetID == "" {
			continue
		}
		_, subnetErr := clients.subnets.Get(ctx, publicv1.SubnetsGetRequest_builder{
			Id: attachment.SubnetID,
		}.Build())
		if subnetErr != nil {
			return nil, safePrerequisiteError("Subnet", attachment.SubnetID, subnetErr)
		}
	}

	return selectedProject, nil
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
		attachments := make([]ComputeInstanceNetworkAttachmentInput, len(input.NetworkAttachments))
		for i, attachment := range input.NetworkAttachments {
			attachment.SubnetID = strings.TrimSpace(attachment.SubnetID)
			securityGroupIDs := make([]string, len(attachment.SecurityGroupIDs))
			for j, securityGroupID := range attachment.SecurityGroupIDs {
				securityGroupIDs[j] = strings.TrimSpace(securityGroupID)
			}
			attachment.SecurityGroupIDs = securityGroupIDs
			attachments[i] = attachment
		}
		input.NetworkAttachments = attachments
	}
	return input, nil
}

func computeInstanceSpecFromInput(input CreateComputeInstanceInput) (*publicv1.ComputeInstanceSpec, error) {
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
	if input.NetworkAttachments != nil {
		attachments, err := computeInstanceNetworkAttachmentsFromInput(input.NetworkAttachments)
		if err != nil {
			return nil, err
		}
		builder.NetworkAttachments = attachments
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

func computeInstanceNetworkAttachmentsFromInput(input []ComputeInstanceNetworkAttachmentInput) ([]*publicv1.ComputeNetworkAttachment, error) {
	// An empty list means the tenant defaults, like an omitted one.
	if len(input) == 0 {
		return nil, nil
	}
	if len(input) > 1 {
		return nil, fmt.Errorf("network_attachments must contain at most one attachment")
	}
	attachments := make([]*publicv1.ComputeNetworkAttachment, len(input))
	for i, attachment := range input {
		// An omitted subnet_id means the tenant's default Subnet.
		subnetID := strings.TrimSpace(attachment.SubnetID)
		securityGroups := make([]*publicv1.SecurityGroupLocalReference, len(attachment.SecurityGroupIDs))
		for j, securityGroupID := range attachment.SecurityGroupIDs {
			securityGroupID = strings.TrimSpace(securityGroupID)
			if securityGroupID == "" {
				return nil, fmt.Errorf("network_attachments[%d].security_group_ids[%d] is required", i, j)
			}
			securityGroups[j] = publicv1.SecurityGroupLocalReference_builder{Id: securityGroupID}.Build()
		}
		builder := publicv1.ComputeNetworkAttachment_builder{SecurityGroups: securityGroups}
		if subnetID != "" {
			builder.Subnet = publicv1.SubnetLocalReference_builder{Id: subnetID}.Build()
		}
		attachments[i] = builder.Build()
	}
	return attachments, nil
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

		currentResponse, err := clients.instances.Get(ctx, publicv1.ComputeInstancesGetRequest_builder{
			Id: input.ID,
		}.Build())
		if err != nil {
			return nil, UpdateComputeInstanceOutput{}, safePrerequisiteError("ComputeInstance", input.ID, err)
		}
		current := currentResponse.GetObject()
		if current.GetMetadata().GetVersion() != input.Version {
			return nil, UpdateComputeInstanceOutput{}, safeStaleVersionError()
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

		if input.InstanceTypeID != "" {
			_, instanceTypeErr := clients.instanceTypes.Get(ctx, publicv1.InstanceTypesGetRequest_builder{
				Id: input.InstanceTypeID,
			}.Build())
			if instanceTypeErr != nil {
				return nil, UpdateComputeInstanceOutput{}, safePrerequisiteError(
					"instance type", input.InstanceTypeID, instanceTypeErr,
				)
			}
		}
		paths := make([]string, 0, 2)
		metadata := publicv1.Metadata_builder{Version: input.Version}
		spec := publicv1.ComputeInstanceSpec_builder{}
		if input.InstanceTypeID != "" {
			paths = append(paths, "spec.instance_type")
			spec.InstanceType = publicv1.InstanceTypeReference_builder{Id: input.InstanceTypeID}.Build()
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
			if status.Code(err) == codes.Aborted {
				return nil, UpdateComputeInstanceOutput{}, safeStaleVersionError()
			}
			if status.Code(err) == codes.FailedPrecondition {
				return nil, UpdateComputeInstanceOutput{}, safeUpdatePreconditionError()
			}
			return nil, UpdateComputeInstanceOutput{}, safePublicAPIError(err)
		}
		updated := response.GetObject()
		return nil, UpdateComputeInstanceOutput{
			ID:       input.ID,
			Version:  updated.GetMetadata().GetVersion(),
			State:    updated.GetStatus().GetState().String(),
			Warnings: response.GetWarnings(),
		}, nil
	}
}

func safeUpdatePreconditionError() error {
	envelope := newToolError(
		"conflict",
		"FailedPrecondition",
		"The update conflicts with the current compute instance state.",
		false,
	)
	envelope.Details = []string{
		"Choose an available instance type with the same GPU configuration.",
	}
	return envelope
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
	if input.InstanceTypeID == "" && input.RunStrategy == "" {
		return UpdateComputeInstanceInput{}, fmt.Errorf("at least one supported update field is required")
	}
	if input.RunStrategy != "" && parseComputeInstanceRunStrategy(input.RunStrategy) ==
		publicv1.ComputeInstanceRunStrategy_COMPUTE_INSTANCE_RUN_STRATEGY_UNSPECIFIED {
		return UpdateComputeInstanceInput{}, fmt.Errorf(
			"run_strategy must be ALWAYS or HALTED",
		)
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
	if input.InstanceTypeID != "" && fields.GetInstanceType().HasLocked() {
		return safeInvalidRequestError(fmt.Errorf("instance_type is locked by the selected catalog item"))
	}
	if input.RunStrategy != "" && fields.GetRunStrategy().HasLocked() {
		return safeInvalidRequestError(fmt.Errorf("run_strategy is locked by the selected catalog item"))
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

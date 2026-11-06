//
// Copyright (c) 2026 Red Hat, Inc.
//
// Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except in compliance with
// the License. You may obtain a copy of the License at
//
//   http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.
//

package provider

import (
	"context"
	"fmt"
	"regexp"
	"time"

	"encoding/json/jsontext"
	json "encoding/json/v2"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/types"
	gen "github.com/osac-project/osac/terraform-provider/internal/gen/resource_bare_metal_instance"
)

// Ensure the resource satisfies the interfaces:
var (
	_ resource.Resource                = &bareMetalInstanceResource{}
	_ resource.ResourceWithImportState = &bareMetalInstanceResource{}
)

// uuidPattern matches the identifiers used by the fulfillment service, so that the reference attributes can accept
// both names and identifiers.
var uuidPattern = regexp.MustCompile(
	`^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$`,
)

// sharedTenant is the name of the tenant where platform scoped resources live.
const sharedTenant = "shared"

// instanceObject is the minimal shape of a bare metal instance response needed by this resource.
type instanceObject struct {
	Id     string         `json:"id"`
	Status jsontext.Value `json:"status"`
}

// state returns the state name reported in the status.
func (o *instanceObject) state() string {
	var status struct {
		State string `json:"state"`
	}
	_ = json.Unmarshal(o.Status, &status)
	return status.State
}

// bareMetalInstanceResource manages BareMetalInstance resources. The schema and the models are generated from the
// API definitions; this file contains only the CRUD glue: the reference resolution, the tenant placement and the
// update mask handling, which are policy and can't be generated. Requests and responses are JSON, in the same shape
// as the generated schemas, so no per-resource field mapping is needed.
type bareMetalInstanceResource struct {
	client *Client
}

// NewBareMetalInstanceResource creates a new resource.
func NewBareMetalInstanceResource() resource.Resource {
	return &bareMetalInstanceResource{}
}

// Metadata is the implementation of the resource.Resource interface.
func (r *bareMetalInstanceResource) Metadata(ctx context.Context,
	request resource.MetadataRequest, response *resource.MetadataResponse) {
	response.TypeName = request.ProviderTypeName + "_bare_metal_instance"
}

// Schema is the implementation of the resource.Resource interface. The schema is the one generated from the API
// definitions. No attribute is marked sensitive: the SSH key is public by definition and inline user data is
// plain configuration, already visible in the Terraform files; the sensitive variant is the Secret reference,
// whose contents never flow through the provider.
func (r *bareMetalInstanceResource) Schema(ctx context.Context,
	request resource.SchemaRequest, response *resource.SchemaResponse) {
	generated := gen.BareMetalInstanceResourceSchema(ctx)
	if attribute, ok := generated.Attributes["id"].(schema.StringAttribute); ok {
		attribute.PlanModifiers = append(attribute.PlanModifiers, stringplanmodifier.UseStateForUnknown())
		generated.Attributes["id"] = attribute
	}
	response.Schema = generated
}

// Configure is the implementation of the resource.Resource interface.
func (r *bareMetalInstanceResource) Configure(ctx context.Context,
	request resource.ConfigureRequest, response *resource.ConfigureResponse) {
	if request.ProviderData == nil {
		return
	}
	r.client = clientFromResource(ctx, request, response)
}

// Create is the implementation of the resource.Resource interface.
func (r *bareMetalInstanceResource) Create(ctx context.Context,
	request resource.CreateRequest, response *resource.CreateResponse) {
	var plan gen.BareMetalInstanceModel
	response.Diagnostics.Append(request.Plan.Get(ctx, &plan)...)
	if response.Diagnostics.HasError() {
		return
	}

	body, err := r.buildBody(ctx, plan)
	if err != nil {
		response.Diagnostics.AddError("Failed to prepare the bare metal instance", err.Error())
		return
	}
	data, err := r.client.create(ctx, bareMetalInstancesPath, body)
	if err != nil {
		response.Diagnostics.AddError(
			"Failed to create bare metal instance",
			fmt.Sprintf("Failed to create the bare metal instance '%s': %s",
				plan.Metadata.Name.ValueString(), err),
		)
		return
	}
	var created instanceObject
	_ = json.Unmarshal(data, &created)

	// Wait until the instance is actually running before reporting success, like most Terraform providers do.
	// Otherwise the apply would finish while the machine is still being provisioned. Note that the REST API
	// doesn't return the create warnings (for example deprecated image notices); see OSAC-7380.
	object, waitErr := r.waitForReady(ctx, created.Id, plan.Spec.RunStrategy.ValueString())
	if waitErr != nil {
		response.Diagnostics.AddError(
			"Failed to create bare metal instance",
			fmt.Sprintf(
				"The bare metal instance '%s' was created, but isn't ready: %s. The instance may still exist; "+
					"run 'tofu destroy' to remove it.",
				plan.Metadata.Name.ValueString(), waitErr,
			),
		)
		return
	}

	plan.Id = types.StringValue(created.Id)
	plan.Status = valueOfJSON(ctx, object.Status, gen.StatusValue{})
	settleModel(ctx, &plan)
	response.Diagnostics.Append(response.State.Set(ctx, &plan)...)
}

// Read is the implementation of the resource.Resource interface.
func (r *bareMetalInstanceResource) Read(ctx context.Context,
	request resource.ReadRequest, response *resource.ReadResponse) {
	var state gen.BareMetalInstanceModel
	response.Diagnostics.Append(request.State.Get(ctx, &state)...)
	if response.Diagnostics.HasError() {
		return
	}
	data, err := r.client.get(ctx, bareMetalInstancesPath, state.Id.ValueString())
	if err != nil {
		if isNotFound(err) {
			response.State.RemoveResource(ctx)
			return
		}
		response.Diagnostics.AddError(
			"Failed to read bare metal instance",
			fmt.Sprintf("Failed to read the bare metal instance '%s': %s", state.Id.ValueString(), err),
		)
		return
	}
	var object instanceObject
	_ = json.Unmarshal(data, &object)
	state.Status = valueOfJSON(ctx, object.Status, gen.StatusValue{})
	response.Diagnostics.Append(response.State.Set(ctx, &state)...)
}

// Update is the implementation of the resource.Resource interface. Only the run strategy and the restart trigger
// can be updated; everything else forces a replacement.
func (r *bareMetalInstanceResource) Update(ctx context.Context,
	request resource.UpdateRequest, response *resource.UpdateResponse) {
	var plan gen.BareMetalInstanceModel
	response.Diagnostics.Append(request.Plan.Get(ctx, &plan)...)
	if response.Diagnostics.HasError() {
		return
	}
	var state gen.BareMetalInstanceModel
	response.Diagnostics.Append(request.State.Get(ctx, &state)...)
	if response.Diagnostics.HasError() {
		return
	}

	// Build the partial spec containing the updatable fields:
	spec, err := r.specJSON(ctx, plan)
	if err != nil {
		response.Diagnostics.AddError("Failed to prepare the bare metal instance", err.Error())
		return
	}
	update := map[string]any{}
	if value, ok := spec["run_strategy"]; ok {
		update["run_strategy"] = value
	}
	if value, ok := spec["restart_trigger"]; ok {
		update["restart_trigger"] = value
	}
	body := map[string]any{
		"id":   state.Id.ValueString(),
		"spec": update,
	}
	data, err := r.client.update(ctx, bareMetalInstancesPath, state.Id.ValueString(), body,
		[]string{"spec.run_strategy", "spec.restart_trigger"},
	)
	if err != nil {
		response.Diagnostics.AddError(
			"Failed to update bare metal instance",
			fmt.Sprintf("Failed to update the bare metal instance '%s': %s", state.Id.ValueString(), err),
		)
		return
	}

	var object instanceObject
	_ = json.Unmarshal(data, &object)
	plan.Id = types.StringValue(state.Id.ValueString())
	plan.Status = valueOfJSON(ctx, object.Status, gen.StatusValue{})
	settleModel(ctx, &plan)
	response.Diagnostics.Append(response.State.Set(ctx, &plan)...)
}

// Delete is the implementation of the resource.Resource interface.
func (r *bareMetalInstanceResource) Delete(ctx context.Context,
	request resource.DeleteRequest, response *resource.DeleteResponse) {
	var state gen.BareMetalInstanceModel
	response.Diagnostics.Append(request.State.Get(ctx, &state)...)
	if response.Diagnostics.HasError() {
		return
	}
	err := r.client.delete(ctx, bareMetalInstancesPath, state.Id.ValueString())
	if err != nil && !isNotFound(err) {
		response.Diagnostics.AddError(
			"Failed to delete bare metal instance",
			fmt.Sprintf("Failed to delete the bare metal instance '%s': %s", state.Id.ValueString(), err),
		)
	}
}

// ImportState is the implementation of the resource.ResourceWithImportState interface.
func (r *bareMetalInstanceResource) ImportState(ctx context.Context,
	request resource.ImportStateRequest, response *resource.ImportStateResponse) {
	resource.ImportStatePassthroughID(ctx, path.Root("id"), request, response)
}

// tenant resolves the tenant of the instance: the `metadata.tenant` attribute when set, or the tenant selected in
// the CLI configuration otherwise.
func (r *bareMetalInstanceResource) tenant(plan gen.BareMetalInstanceModel) string {
	if !plan.Metadata.Tenant.IsNull() {
		return plan.Metadata.Tenant.ValueString()
	}
	return r.client.DefaultTenant
}

// specJSON serializes the spec of the plan into the JSON shape of the API, which is the same shape as the generated
// schema.
func (r *bareMetalInstanceResource) specJSON(ctx context.Context,
	plan gen.BareMetalInstanceModel) (map[string]any, error) {
	object, diags := plan.Spec.ToObjectValue(ctx)
	if diags.HasError() {
		return nil, fmt.Errorf("failed to encode the spec: %v", diags)
	}
	terraformValue, err := object.ToTerraformValue(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to encode the spec: %w", err)
	}
	encoded, err := terraformToJSON(ctx, terraformValue)
	if err != nil {
		return nil, fmt.Errorf("failed to encode the spec: %w", err)
	}
	spec, ok := encoded.(map[string]any)
	if !ok {
		return nil, fmt.Errorf("the spec didn't encode to an object")
	}
	return spec, nil
}

// referenceFields are the spec fields that hold full references. The server resolves them in the tenant of the
// instance unless they are explicitly marked as shared, so they are resolved here, like the CLI does: both names
// and identifiers work, and objects in the shared tenant are referenced correctly.
var referenceFields = map[string]struct {
	path string
	kind string
}{
	"catalog_item":  {path: bareMetalInstanceCatalogsPath, kind: "bare metal instance catalog item"},
	"template":      {path: bareMetalInstanceTemplatesPath, kind: "bare metal instance template"},
	"instance_type": {path: bareMetalInstanceTypesPath, kind: "bare metal instance type"},
	"disk_image":    {path: diskImagesPath, kind: "disk image"},
}

// buildBody builds the JSON request body for the create operation from the plan.
func (r *bareMetalInstanceResource) buildBody(ctx context.Context,
	plan gen.BareMetalInstanceModel) (map[string]any, error) {
	spec, err := r.specJSON(ctx, plan)
	if err != nil {
		return nil, err
	}
	for field, target := range referenceFields {
		reference, ok := spec[field].(map[string]any)
		if !ok {
			continue
		}
		value, _ := reference["id"].(string)
		if value == "" {
			value, _ = reference["name"].(string)
		}
		if value == "" {
			continue
		}
		id, shared, err := r.client.resolveReference(ctx, target.path, target.kind, value)
		if err != nil {
			return nil, err
		}
		spec[field] = map[string]any{"id": id, "shared": shared}
	}
	metadata := map[string]any{
		"name": plan.Metadata.Name.ValueString(),
	}
	if tenant := r.tenant(plan); tenant != "" {
		metadata["tenant"] = tenant
	}
	return map[string]any{
		"metadata": metadata,
		"spec":     spec,
	}, nil
}

// createWaitTimeout is the maximum time the creation of a bare metal instance waits for the machine to be ready.
// Bare metal provisioning is slow: allocating the host and installing the OS takes minutes.
const createWaitTimeout = 30 * time.Minute

// waitInterval is the polling interval used while waiting for the instance to be ready.
const waitInterval = 10 * time.Second

// waitForReady polls the instance until it reaches the state desired by the run strategy, and returns an error if
// it fails or if it doesn't become ready within the timeout.
func (r *bareMetalInstanceResource) waitForReady(ctx context.Context,
	id, runStrategy string) (*instanceObject, error) {
	desired := "BARE_METAL_INSTANCE_STATE_RUNNING"
	if runStrategy == "BARE_METAL_INSTANCE_RUN_STRATEGY_HALTED" {
		desired = "BARE_METAL_INSTANCE_STATE_STOPPED"
	}
	deadline := time.Now().Add(createWaitTimeout)
	for {
		data, err := r.client.get(ctx, bareMetalInstancesPath, id)
		if err != nil {
			return nil, fmt.Errorf("failed to read the instance state: %w", err)
		}
		var object instanceObject
		_ = json.Unmarshal(data, &object)
		switch object.state() {
		case desired:
			return &object, nil
		case "BARE_METAL_INSTANCE_STATE_FAILED":
			return nil, fmt.Errorf("provisioning failed, instance state is 'FAILED'")
		case "BARE_METAL_INSTANCE_STATE_DELETING":
			return nil, fmt.Errorf("the instance is being deleted, instance state is 'DELETING'")
		}
		if time.Now().After(deadline) {
			return nil, fmt.Errorf(
				"timed out after %s, instance state is '%s'", createWaitTimeout, object.state(),
			)
		}
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(waitInterval):
		}
	}
}

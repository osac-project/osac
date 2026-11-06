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

	json "encoding/json/v2"

	"github.com/hashicorp/terraform-plugin-framework/attr"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/types"
	gen "github.com/osac-project/osac/terraform-provider/internal/gen/resource_secret"
)

// Ensure the resource satisfies the interfaces:
var (
	_ resource.Resource                = &secretResource{}
	_ resource.ResourceWithImportState = &secretResource{}
)

// secretResource manages Secret resources. The schema and the models are generated from the API definitions; this
// file contains only the CRUD glue: the tenant placement, the update mask handling and the sensitive marking of the
// data entries. The entries are base64 encoded, like in the REST API, so what is written is what is read back.
type secretResource struct {
	client *Client
}

// NewSecretResource creates a new resource.
func NewSecretResource() resource.Resource {
	return &secretResource{}
}

// Metadata is the implementation of the resource.Resource interface.
func (r *secretResource) Metadata(ctx context.Context,
	request resource.MetadataRequest, response *resource.MetadataResponse) {
	response.TypeName = request.ProviderTypeName + "_secret"
}

// Schema is the implementation of the resource.Resource interface. The schema is the one generated from the API
// definitions, with two adjustments that the generator can't express: the data entries are marked sensitive (a
// display hint — the values are still recorded in the Terraform state), and the type forces a replacement, since
// the server rejects type changes.
func (r *secretResource) Schema(ctx context.Context,
	request resource.SchemaRequest, response *resource.SchemaResponse) {
	generated := markSensitive(gen.SecretResourceSchema(ctx), "data")
	if attribute, ok := generated.Attributes["type"].(schema.StringAttribute); ok {
		attribute.PlanModifiers = append(attribute.PlanModifiers, stringplanmodifier.RequiresReplace())
		generated.Attributes["type"] = attribute
	}
	if attribute, ok := generated.Attributes["id"].(schema.StringAttribute); ok {
		attribute.PlanModifiers = append(attribute.PlanModifiers, stringplanmodifier.UseStateForUnknown())
		generated.Attributes["id"] = attribute
	}
	response.Schema = generated
}

// Configure is the implementation of the resource.Resource interface.
func (r *secretResource) Configure(ctx context.Context,
	request resource.ConfigureRequest, response *resource.ConfigureResponse) {
	if request.ProviderData == nil {
		return
	}
	r.client = clientFromResource(ctx, request, response)
}

// Create is the implementation of the resource.Resource interface.
func (r *secretResource) Create(ctx context.Context,
	request resource.CreateRequest, response *resource.CreateResponse) {
	var plan gen.SecretModel
	response.Diagnostics.Append(request.Plan.Get(ctx, &plan)...)
	if response.Diagnostics.HasError() {
		return
	}

	body, err := r.buildBody(ctx, plan)
	if err != nil {
		response.Diagnostics.AddError("Failed to prepare the secret", err.Error())
		return
	}
	data, err := r.client.create(ctx, secretsPath, body)
	if err != nil {
		response.Diagnostics.AddError(
			"Failed to create secret",
			fmt.Sprintf("Failed to create the secret '%s': %s",
				plan.Metadata.Name.ValueString(), err),
		)
		return
	}
	var created struct {
		Id string `json:"id"`
	}
	_ = json.Unmarshal(data, &created)

	plan.Id = types.StringValue(created.Id)
	if value, ok := settle(ctx, plan.Metadata).(gen.MetadataValue); ok {
		plan.Metadata = value
	}
	response.Diagnostics.Append(response.State.Set(ctx, &plan)...)
}

// Read is the implementation of the resource.Resource interface.
func (r *secretResource) Read(ctx context.Context,
	request resource.ReadRequest, response *resource.ReadResponse) {
	var state gen.SecretModel
	response.Diagnostics.Append(request.State.Get(ctx, &state)...)
	if response.Diagnostics.HasError() {
		return
	}
	data, err := r.client.get(ctx, secretsPath, state.Id.ValueString())
	if err != nil {
		if isNotFound(err) {
			response.State.RemoveResource(ctx)
			return
		}
		response.Diagnostics.AddError(
			"Failed to read secret",
			fmt.Sprintf("Failed to read the secret '%s': %s", state.Id.ValueString(), err),
		)
		return
	}
	var object struct {
		Data map[string]string `json:"data"`
		Type string            `json:"type"`
	}
	_ = json.Unmarshal(data, &object)
	state.Data = mapValue(object.Data)
	state.Type = types.StringValue(object.Type)
	response.Diagnostics.Append(response.State.Set(ctx, &state)...)
}

// Update is the implementation of the resource.Resource interface. The data entries and the type can be updated;
// everything else forces a replacement.
func (r *secretResource) Update(ctx context.Context,
	request resource.UpdateRequest, response *resource.UpdateResponse) {
	var plan gen.SecretModel
	response.Diagnostics.Append(request.Plan.Get(ctx, &plan)...)
	if response.Diagnostics.HasError() {
		return
	}
	var state gen.SecretModel
	response.Diagnostics.Append(request.State.Get(ctx, &state)...)
	if response.Diagnostics.HasError() {
		return
	}

	body, err := r.buildBody(ctx, plan)
	if err != nil {
		response.Diagnostics.AddError("Failed to prepare the secret", err.Error())
		return
	}
	// Only the data entries can be updated; the server rejects type changes:
	delete(body, "type")
	body["id"] = state.Id.ValueString()
	data, err := r.client.update(ctx, secretsPath, state.Id.ValueString(), body,
		[]string{"data"},
	)
	if err != nil {
		response.Diagnostics.AddError(
			"Failed to update secret",
			fmt.Sprintf("Failed to update the secret '%s': %s", state.Id.ValueString(), err),
		)
		return
	}
	var object struct {
		Id string `json:"id"`
	}
	_ = json.Unmarshal(data, &object)

	plan.Id = types.StringValue(state.Id.ValueString())
	if value, ok := settle(ctx, plan.Metadata).(gen.MetadataValue); ok {
		plan.Metadata = value
	}
	response.Diagnostics.Append(response.State.Set(ctx, &plan)...)
}

// Delete is the implementation of the resource.Resource interface.
func (r *secretResource) Delete(ctx context.Context,
	request resource.DeleteRequest, response *resource.DeleteResponse) {
	var state gen.SecretModel
	response.Diagnostics.Append(request.State.Get(ctx, &state)...)
	if response.Diagnostics.HasError() {
		return
	}
	err := r.client.delete(ctx, secretsPath, state.Id.ValueString())
	if err != nil && !isNotFound(err) {
		response.Diagnostics.AddError(
			"Failed to delete secret",
			fmt.Sprintf("Failed to delete the secret '%s': %s", state.Id.ValueString(), err),
		)
	}
}

// ImportState is the implementation of the resource.ResourceWithImportState interface.
func (r *secretResource) ImportState(ctx context.Context,
	request resource.ImportStateRequest, response *resource.ImportStateResponse) {
	resource.ImportStatePassthroughID(ctx, path.Root("id"), request, response)
}

// buildBody builds the JSON request body for the create and update operations from the plan.
func (r *secretResource) buildBody(ctx context.Context, plan gen.SecretModel) (map[string]any, error) {
	entries := make(map[string]any, len(plan.Data.Elements()))
	for name, value := range plan.Data.Elements() {
		text, ok := value.(types.String)
		if !ok {
			return nil, fmt.Errorf("the data entry '%s' isn't a string", name)
		}
		entries[name] = text.ValueString()
	}
	metadata := map[string]any{
		"name": plan.Metadata.Name.ValueString(),
	}
	if !plan.Metadata.Tenant.IsNull() && !plan.Metadata.Tenant.IsUnknown() {
		metadata["tenant"] = plan.Metadata.Tenant.ValueString()
	} else if r.client.DefaultTenant != "" {
		metadata["tenant"] = r.client.DefaultTenant
	}
	body := map[string]any{
		"metadata": metadata,
	}
	if len(entries) > 0 {
		body["data"] = entries
	}
	if !plan.Type.IsNull() && !plan.Type.IsUnknown() && plan.Type.ValueString() != "SECRET_TYPE_UNSPECIFIED" {
		body["type"] = plan.Type.ValueString()
	}
	return body, nil
}

// mapValue converts the JSON entries of a secret into a map value. The entries are base64 encoded strings, like in
// the REST API.
func mapValue(entries map[string]string) types.Map {
	if len(entries) == 0 {
		return types.MapNull(types.StringType)
	}
	values := make(map[string]attr.Value, len(entries))
	for name, value := range entries {
		values[name] = types.StringValue(value)
	}
	return types.MapValueMust(types.StringType, values)
}

// markSensitive marks the given attributes of a generated resource schema as sensitive. The generator can't
// determine which fields hold sensitive material, so the resource glue marks them.
func markSensitive(generated schema.Schema, names ...string) schema.Schema {
	for _, name := range names {
		switch attribute := generated.Attributes[name].(type) {
		case schema.StringAttribute:
			attribute.Sensitive = true
			generated.Attributes[name] = attribute
		case schema.MapAttribute:
			attribute.Sensitive = true
			generated.Attributes[name] = attribute
		case schema.SingleNestedAttribute:
			attribute.Sensitive = true
			generated.Attributes[name] = attribute
		}
	}
	return generated
}

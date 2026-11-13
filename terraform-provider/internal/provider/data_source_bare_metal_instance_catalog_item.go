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
	"encoding/json"
	"fmt"

	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/types"
	gen "github.com/osac-project/osac/terraform-provider/internal/gen/datasource_bare_metal_instance_catalog_item"
)

// Ensure the data source satisfies the interfaces:
var _ datasource.DataSource = &bareMetalInstanceCatalogItemDataSource{}

// bareMetalInstanceCatalogItemDataSource resolves a bare metal instance catalog item by name or identifier. The schema and the models are generated from the API
// definitions; the name resolution and the conversion of the object are the glue.
type bareMetalInstanceCatalogItemDataSource struct {
	client *Client
}

// bareMetalInstanceCatalogItemModel is the generated model plus the `name` lookup attribute.
type bareMetalInstanceCatalogItemModel struct {
	Name types.String `tfsdk:"name"`
	gen.BareMetalInstanceCatalogItemModel
}

// NewBareMetalInstanceCatalogItemDataSource creates a new data source.
func NewBareMetalInstanceCatalogItemDataSource() datasource.DataSource {
	return &bareMetalInstanceCatalogItemDataSource{}
}

// Metadata is the implementation of the datasource.DataSource interface.
func (d *bareMetalInstanceCatalogItemDataSource) Metadata(ctx context.Context,
	request datasource.MetadataRequest, response *datasource.MetadataResponse) {
	response.TypeName = request.ProviderTypeName + "_bare_metal_instance_catalog_item"
}

// Schema is the implementation of the datasource.DataSource interface. The schema is the one generated from the API
// definitions, adapted so that the bare metal instance catalog item can also be resolved by name.
func (d *bareMetalInstanceCatalogItemDataSource) Schema(ctx context.Context,
	request datasource.SchemaRequest, response *datasource.SchemaResponse) {
	response.Schema = withNameLookup(
		gen.BareMetalInstanceCatalogItemDataSourceSchema(ctx), "bare metal instance catalog item",
	)
}

// Configure is the implementation of the datasource.DataSource interface.
func (d *bareMetalInstanceCatalogItemDataSource) Configure(ctx context.Context,
	request datasource.ConfigureRequest, response *datasource.ConfigureResponse) {
	d.client = clientFromDataSource(ctx, request, response)
}

// Read is the implementation of the datasource.DataSource interface.
func (d *bareMetalInstanceCatalogItemDataSource) Read(ctx context.Context,
	request datasource.ReadRequest, response *datasource.ReadResponse) {
	var state bareMetalInstanceCatalogItemModel
	response.Diagnostics.Append(request.Config.Get(ctx, &state)...)
	if response.Diagnostics.HasError() {
		return
	}

	// Resolve the identifier, by name when necessary:
	id, err := d.client.lookupID(ctx, bareMetalInstanceCatalogsPath, "bare metal instance catalog item",
		state.Name.ValueString(), state.Id.ValueString())
	if err != nil {
		response.Diagnostics.AddError("Failed to find bare metal instance catalog item", err.Error())
		return
	}
	data, err := d.client.get(ctx, bareMetalInstanceCatalogsPath, id)
	if err != nil {
		response.Diagnostics.AddError(
			"Failed to get bare metal instance catalog item",
			fmt.Sprintf("Failed to get the bare metal instance catalog item '%s': %s", id, err),
		)
		return
	}

	// Copy the results to the state. The response is in the same JSON shape as the generated schema:
	var object struct {
		Id          string          `json:"id"`
		Metadata    json.RawMessage `json:"metadata"`
		Description string          `json:"description"`
		Published   bool            `json:"published"`
		Template    json.RawMessage `json:"template"`
		Title       string          `json:"title"`
	}
	_ = json.Unmarshal(data, &object)
	state.Id = types.StringValue(object.Id)
	state.Metadata = valueOfJSON(ctx, object.Metadata, gen.MetadataValue{})
	state.Description = types.StringValue(object.Description)
	state.Published = types.BoolValue(object.Published)
	state.Template = valueOfJSON(ctx, object.Template, gen.TemplateValue{})
	state.Title = types.StringValue(object.Title)

	response.Diagnostics.Append(response.State.Set(ctx, &state)...)
}

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

	"encoding/json/jsontext"
	json "encoding/json/v2"

	"github.com/hashicorp/terraform-plugin-framework/attr"
	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/types"
	gen "github.com/osac-project/osac/terraform-provider/internal/gen/datasource_secret"
)

// Ensure the data source satisfies the interfaces:
var _ datasource.DataSource = &secretDataSource{}

// secretDataSource resolves a secret by name or identifier. The schema and the models are generated from the API
// definitions; the name resolution and the conversion of the object are the glue.
type secretDataSource struct {
	client *Client
}

// secretModel is the generated model plus the `name` lookup attribute. The `data` entries are base64 encoded,
// like in the REST API.
type secretModel struct {
	Name     types.String      `tfsdk:"name"`
	Data     types.Map         `tfsdk:"data"`
	Id       types.String      `tfsdk:"id"`
	Metadata gen.MetadataValue `tfsdk:"metadata"`
	Type     types.String      `tfsdk:"type"`
}

// NewSecretDataSource creates a new data source.
func NewSecretDataSource() datasource.DataSource {
	return &secretDataSource{}
}

// Metadata is the implementation of the datasource.DataSource interface.
func (d *secretDataSource) Metadata(ctx context.Context,
	request datasource.MetadataRequest, response *datasource.MetadataResponse) {
	response.TypeName = request.ProviderTypeName + "_secret"
}

// Schema is the implementation of the datasource.DataSource interface. The schema is the one generated from the API
// definitions, adapted so that the secret can also be resolved by name.
func (d *secretDataSource) Schema(ctx context.Context,
	request datasource.SchemaRequest, response *datasource.SchemaResponse) {
	// The contents are exposed, but marked sensitive: the generator can't express that (the OpenAPI format
	// that maps to sensitivity doesn't survive the map translation), so it is done here. Note that sensitive is
	// a display hint: the values are still recorded in the Terraform state.
	response.Schema = markSensitiveDataSource(
		withNameLookup(gen.SecretDataSourceSchema(ctx), "secret"),
		"data",
	)
}

// Configure is the implementation of the datasource.DataSource interface.
func (d *secretDataSource) Configure(ctx context.Context,
	request datasource.ConfigureRequest, response *datasource.ConfigureResponse) {
	d.client = clientFromDataSource(ctx, request, response)
}

// Read is the implementation of the datasource.DataSource interface.
func (d *secretDataSource) Read(ctx context.Context,
	request datasource.ReadRequest, response *datasource.ReadResponse) {
	var state secretModel
	response.Diagnostics.Append(request.Config.Get(ctx, &state)...)
	if response.Diagnostics.HasError() {
		return
	}

	// Resolve the identifier, by name when necessary:
	id, err := lookupIdentifier(ctx, "secret",
		state.Name.ValueString(), state.Id.ValueString(),
		func(ctx context.Context) ([]string, error) {
			items, err := d.client.list(ctx, secretsPath, nameFilter(state.Name.ValueString()))
			if err != nil {
				return nil, err
			}
			ids := make([]string, 0, len(items))
			for _, item := range items {
				ids = append(ids, item.Id)
			}
			return ids, nil
		})
	if err != nil {
		response.Diagnostics.AddError("Failed to find secret", err.Error())
		return
	}
	data, err := d.client.get(ctx, secretsPath, id)
	if err != nil {
		response.Diagnostics.AddError(
			"Failed to get secret",
			fmt.Sprintf("Failed to get the secret '%s': %s", id, err),
		)
		return
	}

	// Copy the results to the state. The response is in the same JSON shape as the generated schema:
	var object struct {
		Data     map[string]string `json:"data"`
		Id       string            `json:"id"`
		Metadata jsontext.Value    `json:"metadata"`
		Type     string            `json:"type"`
	}
	_ = json.Unmarshal(data, &object)
	state.Id = types.StringValue(object.Id)
	state.Metadata = valueOfJSON(ctx, object.Metadata, gen.MetadataValue{})
	state.Type = types.StringValue(object.Type)
	entries := make(map[string]attr.Value, len(object.Data))
	for name, value := range object.Data {
		entries[name] = types.StringValue(value)
	}
	if len(entries) > 0 {
		state.Data = types.MapValueMust(types.StringType, entries)
	} else {
		state.Data = types.MapNull(types.StringType)
	}
	response.Diagnostics.Append(response.State.Set(ctx, &state)...)
}

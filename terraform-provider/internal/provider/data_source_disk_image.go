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
	gen "github.com/osac-project/osac/terraform-provider/internal/gen/datasource_disk_image"
)

// Ensure the data source satisfies the interfaces:
var _ datasource.DataSource = &diskImageDataSource{}

// diskImageDataSource resolves a disk image by name or identifier. The schema and the models are generated from the API
// definitions; the name resolution and the conversion of the object are the glue.
type diskImageDataSource struct {
	client *Client
}

// diskImageModel is the generated model plus the `name` and `labels` lookup attributes.
type diskImageModel struct {
	Name   types.String `tfsdk:"name"`
	Labels types.Map    `tfsdk:"labels"`
	gen.DiskImageModel
}

// NewDiskImageDataSource creates a new data source.
func NewDiskImageDataSource() datasource.DataSource {
	return &diskImageDataSource{}
}

// Metadata is the implementation of the datasource.DataSource interface.
func (d *diskImageDataSource) Metadata(ctx context.Context,
	request datasource.MetadataRequest, response *datasource.MetadataResponse) {
	response.TypeName = request.ProviderTypeName + "_disk_image"
}

// Schema is the implementation of the datasource.DataSource interface. The schema is the one generated from the API
// definitions, adapted so that the disk image can also be resolved by name.
func (d *diskImageDataSource) Schema(ctx context.Context,
	request datasource.SchemaRequest, response *datasource.SchemaResponse) {
	response.Schema = withLookup(
		gen.DiskImageDataSourceSchema(ctx), "disk image",
	)
}

// Configure is the implementation of the datasource.DataSource interface.
func (d *diskImageDataSource) Configure(ctx context.Context,
	request datasource.ConfigureRequest, response *datasource.ConfigureResponse) {
	d.client = clientFromDataSource(ctx, request, response)
}

// Read is the implementation of the datasource.DataSource interface.
func (d *diskImageDataSource) Read(ctx context.Context,
	request datasource.ReadRequest, response *datasource.ReadResponse) {
	var state diskImageModel
	response.Diagnostics.Append(request.Config.Get(ctx, &state)...)
	if response.Diagnostics.HasError() {
		return
	}

	// Resolve the identifier, by name when necessary:
	id, err := d.client.lookupID(ctx, diskImagesPath, "disk image",
		state.Name.ValueString(), state.Id.ValueString(), stringMap(state.Labels))
	if err != nil {
		response.Diagnostics.AddError("Failed to find disk image", err.Error())
		return
	}
	data, err := d.client.get(ctx, diskImagesPath, id)
	if err != nil {
		response.Diagnostics.AddError(
			"Failed to get disk image",
			fmt.Sprintf("Failed to get the disk image '%s': %s", id, err),
		)
		return
	}

	// Copy the results to the state. The response is in the same JSON shape as the generated schema:
	var object struct {
		Id       string          `json:"id"`
		Metadata json.RawMessage `json:"metadata"`
		Spec     json.RawMessage `json:"spec"`
		Status   json.RawMessage `json:"status"`
	}
	_ = json.Unmarshal(data, &object)
	state.Id = types.StringValue(object.Id)
	state.Metadata = valueOfJSON(ctx, object.Metadata, gen.MetadataValue{})
	state.Spec = valueOfJSON(ctx, object.Spec, gen.SpecValue{})
	state.Status = valueOfJSON(ctx, object.Status, gen.StatusValue{})

	response.Diagnostics.Append(response.State.Set(ctx, &state)...)
}

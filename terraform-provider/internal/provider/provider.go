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

// Package provider contains the Terraform (and OpenTofu) provider for the OSAC fulfillment service.
package provider

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"

	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/provider"
	"github.com/hashicorp/terraform-plugin-framework/provider/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/types"

	"github.com/osac-project/osac/terraform-provider/internal/osaccli"
)

// Ensure the provider satisfies the provider interfaces:
var _ provider.Provider = &osacProvider{}

// osacProvider is the OSAC provider. It doesn't have any credentials of its own: it reuses the session created by
// the `osac login` command of the OSAC CLI.
type osacProvider struct {
	version string
}

// osacProviderModel is the configuration of the provider itself.
type osacProviderModel struct {
	ConfigDir types.String `tfsdk:"config_dir"`
}

// New creates a new provider with the given version.
func New(version string) provider.Provider {
	return &osacProvider{
		version: version,
	}
}

// Metadata is the implementation of the provider.Provider interface.
func (p *osacProvider) Metadata(ctx context.Context, request provider.MetadataRequest, response *provider.MetadataResponse) {
	response.TypeName = "osac"
}

// Schema is the implementation of the provider.Provider interface.
func (p *osacProvider) Schema(ctx context.Context, request provider.SchemaRequest, response *provider.SchemaResponse) {
	response.Schema = schema.Schema{
		Description: "The OSAC provider manages resources of the OSAC fulfillment service.\n\n" +
			"Authentication reuses the session created by the `osac login` command of the OSAC CLI: run it once and the " +
			"provider will use the same address, CA certificates and OAuth credentials.",
		Attributes: map[string]schema.Attribute{
			"config_dir": schema.StringAttribute{
				Optional: true,
				Description: "Directory containing the OSAC CLI configuration written by `osac login`. Defaults to " +
					"the `OSAC_CONFIG_DIR` environment variable when set, or to the directory the CLI uses " +
					"(typically `~/.config/osac`).",
			},
		},
	}
}

// Configure is the implementation of the provider.Provider interface. It loads the OSAC CLI configuration and
// creates the client connection, storing it in the data sources and resources via the ConfigureData and
// ConfigureResource calls.
func (p *osacProvider) Configure(ctx context.Context, request provider.ConfigureRequest, response *provider.ConfigureResponse) {
	// Load the provider configuration:
	var config osacProviderModel
	diags := request.Config.Get(ctx, &config)
	response.Diagnostics.Append(diags...)
	if response.Diagnostics.HasError() {
		return
	}

	// Resolve the configuration directory:
	configDir := config.ConfigDir.ValueString()
	if configDir == "" {
		configDir = os.Getenv("OSAC_CONFIG_DIR")
	}
	if configDir == "" {
		var err error
		configDir, err = osaccli.DefaultDir()
		if err != nil {
			response.Diagnostics.AddError(
				"Failed to determine OSAC configuration directory",
				err.Error(),
			)
			return
		}
	}
	configDir, err := filepath.Abs(configDir)
	if err != nil {
		response.Diagnostics.AddError(
			"Failed to resolve OSAC configuration directory",
			fmt.Sprintf("Failed to resolve the absolute path of '%s': %s", configDir, err),
		)
		return
	}

	// Load the configuration:
	logger := slog.Default().With("dir", configDir)
	settings, err := osaccli.Load(ctx, logger, configDir)
	if err != nil {
		response.Diagnostics.AddError(
			"Failed to load the OSAC CLI configuration",
			fmt.Sprintf(
				"Failed to load the OSAC CLI configuration from '%s': %s. Run 'osac login' first.",
				configDir, err,
			),
		)
		return
	}

	// Create the client:
	client, err := NewClient(ctx, logger, settings)
	if err != nil {
		response.Diagnostics.AddError(
			"Failed to create the fulfillment service client",
			err.Error(),
		)
		return
	}
	response.DataSourceData = client
	response.ResourceData = client
}

// DataSources is the implementation of the provider.Provider interface.
func (p *osacProvider) DataSources(ctx context.Context) []func() datasource.DataSource {
	return []func() datasource.DataSource{
		NewBareMetalInstanceTypeDataSource,
		NewBareMetalInstanceCatalogItemDataSource,
		NewDiskImageDataSource,
		NewSecretDataSource,
	}
}

// Resources is the implementation of the provider.Provider interface.
func (p *osacProvider) Resources(ctx context.Context) []func() resource.Resource {
	return []func() resource.Resource{
		NewBareMetalInstanceResource,
		NewSecretResource,
	}
}

// clientFromDataSource retrieves the client stored by the provider configuration in a data source.
func clientFromDataSource(ctx context.Context, request datasource.ConfigureRequest, response *datasource.ConfigureResponse) *Client {
	// ProviderData is nil during config validation, before the provider itself is configured:
	if request.ProviderData == nil {
		return nil
	}
	client, ok := request.ProviderData.(*Client)
	if !ok {
		response.Diagnostics.AddError(
			"Unexpected data source configure type",
			fmt.Sprintf("Expected *provider.Client, got: %T. Report this bug to the provider developers.", request.ProviderData),
		)
		return nil
	}
	return client
}

// clientFromResource retrieves the client stored by the provider configuration in a resource.
func clientFromResource(ctx context.Context, request resource.ConfigureRequest, response *resource.ConfigureResponse) *Client {
	// ProviderData is nil during config validation, before the provider itself is configured:
	if request.ProviderData == nil {
		return nil
	}
	client, ok := request.ProviderData.(*Client)
	if !ok {
		response.Diagnostics.AddError(
			"Unexpected resource configure type",
			fmt.Sprintf("Expected *provider.Client, got: %T. Report this bug to the provider developers.", request.ProviderData),
		)
		return nil
	}
	return client
}

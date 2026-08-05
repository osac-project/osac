/*
Copyright (c) 2025 Red Hat Inc.

Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except in compliance with the
License. You may obtain a copy of the License at

  http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on an
"AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the specific
language governing permissions and limitations under the License.
*/

package cluster

import (
	"context"
	"embed"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/spf13/cobra"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/anypb"
	"google.golang.org/protobuf/types/known/durationpb"
	"google.golang.org/protobuf/types/known/timestamppb"
	"google.golang.org/protobuf/types/known/wrapperspb"

	publicv1 "github.com/osac-project/fulfillment-service/internal/api/osac/public/v1"
	"github.com/osac-project/fulfillment-service/internal/cmd/cli/create/fieldutil"
	"github.com/osac-project/fulfillment-service/internal/config"
	"github.com/osac-project/fulfillment-service/internal/exit"
	"github.com/osac-project/fulfillment-service/internal/logging"
	"github.com/osac-project/fulfillment-service/internal/reflection"
	"github.com/osac-project/fulfillment-service/internal/terminal"
)

//go:embed templates
var templatesFS embed.FS

func Cmd() *cobra.Command {
	runner := &runnerContext{}
	result := &cobra.Command{
		Use:                   "cluster [FLAG...]",
		Aliases:               []string{string(proto.MessageName((*publicv1.Cluster)(nil)))},
		Short:                 shortHelp,
		Long:                  longHelp,
		DisableFlagsInUseLine: true,
		Args:                  cobra.NoArgs,
		RunE:                  runner.run,
	}
	flags := result.Flags()
	flags.StringVarP(
		&runner.args.name,
		"name",
		"n",
		"",
		nameFlagHelp,
	)
	flags.StringVarP(
		&runner.args.template,
		"template",
		"t",
		"",
		templateFlagHelp,
	)
	flags.StringVar(
		&runner.args.catalogItem,
		"catalog-item",
		"",
		catalogItemFlagHelp,
	)
	flags.StringSliceVarP(
		&runner.args.templateParameterValues,
		"template-parameter",
		"p",
		[]string{},
		templateParameterFlagHelp,
	)
	flags.StringSliceVarP(
		&runner.args.templateParameterFiles,
		"template-parameter-file",
		"f",
		[]string{},
		templateParameterFileFlagHelp,
	)
	flags.StringVar(
		&runner.args.pullSecret,
		"pull-secret",
		"",
		pullSecretFlagHelp,
	)
	flags.StringVar(
		&runner.args.pullSecretFile,
		"pull-secret-file",
		"",
		pullSecretFileFlagHelp,
	)
	flags.StringVar(
		&runner.args.sshPublicKey,
		"ssh-public-key",
		"",
		sshPublicKeyFlagHelp,
	)
	flags.StringVar(
		&runner.args.sshPublicKeyFile,
		"ssh-public-key-file",
		"",
		sshPublicKeyFileFlagHelp,
	)
	flags.StringVar(
		&runner.args.version,
		"version",
		"",
		versionFlagHelp,
	)
	flags.StringVar(
		&runner.args.podCIDR,
		"pod-cidr",
		"",
		podCIDRFlagHelp,
	)
	flags.StringVar(
		&runner.args.serviceCIDR,
		"service-cidr",
		"",
		serviceCIDRFlagHelp,
	)
	flags.StringArrayVar(
		&runner.args.setFields,
		"set",
		nil,
		setFlagHelp,
	)
	result.MarkFlagsMutuallyExclusive("catalog-item", "template")
	result.MarkFlagsOneRequired("catalog-item", "template")
	return result
}

type runnerContext struct {
	args struct {
		name                    string
		template                string
		catalogItem             string
		templateParameterValues []string
		templateParameterFiles  []string
		setFields               []string
		pullSecret              string
		pullSecretFile          string
		sshPublicKey            string
		sshPublicKeyFile        string
		version                 string
		podCIDR                 string
		serviceCIDR             string
	}
	logger                *slog.Logger
	console               *terminal.Console
	settings              *config.Settings
	templatesClient       publicv1.ClusterTemplatesClient
	clustersClient        publicv1.ClustersClient
	clusterVersionsClient publicv1.ClusterVersionsClient
}

func (c *runnerContext) run(cmd *cobra.Command, args []string) error {
	var err error

	// Get the context:
	ctx := cmd.Context()

	// Get the logger and console:
	c.logger = logging.LoggerFromContext(ctx)
	c.console = terminal.ConsoleFromContext(ctx)

	// Load the templates for the console messages:
	err = c.console.AddTemplates(templatesFS, "templates")
	if err != nil {
		return fmt.Errorf("failed to load templates: %w", err)
	}

	// Reject template parameters when using catalog item (per D-04):
	if c.args.catalogItem != "" {
		if len(c.args.templateParameterValues) > 0 || len(c.args.templateParameterFiles) > 0 {
			return fmt.Errorf(
				"--template-parameter and --template-parameter-file are not supported with --catalog-item",
			)
		}
	}

	// Reject --set without --catalog-item:
	if c.args.catalogItem == "" && len(c.args.setFields) > 0 {
		return fmt.Errorf("--set is only supported with --catalog-item")
	}

	// Deprecation warning for --template (per D-03):
	if c.args.template != "" {
		fmt.Fprintf(os.Stderr, "Warning: --template is deprecated, use --catalog-item instead\n")
	}

	// Get the configuration:
	c.settings = config.SettingsFromContext(ctx)
	if !c.settings.Armed() {
		return fmt.Errorf("there is no configuration, run the 'login' command")
	}

	// Create the gRPC connection from the configuration:
	conn, err := c.settings.Connect(ctx, cmd.Flags())
	if err != nil {
		return fmt.Errorf("failed to create gRPC connection: %w", err)
	}
	defer conn.Close()

	// Create the reflection helper:
	helper, err := reflection.NewHelper().
		SetLogger(c.logger).
		SetConnection(conn).
		AddPackages(c.settings.Packages()).
		SetTenantFunc(config.TenantFromContext).
		Build()
	if err != nil {
		return fmt.Errorf("failed to create reflection tool: %w", err)
	}
	c.console.SetHelper(helper)

	// Create the gRPC clients:
	c.templatesClient = publicv1.NewClusterTemplatesClient(conn)
	c.clustersClient = publicv1.NewClustersClient(conn)
	c.clusterVersionsClient = publicv1.NewClusterVersionsClient(conn)

	// Resolve credentials before branching (used in both catalog-item and template paths):
	pullSecret, sshPublicKey, err := c.resolveCredentials()
	if err != nil {
		return err
	}

	// Resolve the version reference to a ClusterVersion metadata.name:
	if c.args.version != "" {
		version, err := c.findVersion(ctx)
		if err != nil {
			return err
		}
		c.args.version = version.GetMetadata().GetName()
	}

	if c.args.catalogItem != "" {
		// Catalog item path: skip template lookup entirely (per D-04).
		specBuilder := publicv1.ClusterSpec_builder{
			CatalogItem: c.args.catalogItem,
		}
		c.applyOptionalSpecFields(&specBuilder, pullSecret, sshPublicKey)
		spec := specBuilder.Build()
		if err := fieldutil.ApplyFields(spec, c.args.setFields); err != nil {
			return err
		}
		return c.createCluster(ctx, spec)
	}

	// Legacy template path (existing code continues below):

	// Fetch the cluster template:
	template, err := c.findTemplate(ctx)
	if err != nil {
		return err
	}
	if template == nil {
		return exit.Error(1)
	}

	// Parse the template parameters:
	templateParameterValues, templateParameterIssues := c.parseTemplateParameters(ctx, template)
	if len(templateParameterIssues) > 0 {
		validTemplateParameters := c.validTemplateParameters(template)
		c.console.Render(ctx, "template_parameter_issues.txt", map[string]any{
			"Template":   c.args.template,
			"Parameters": validTemplateParameters,
			"Issues":     templateParameterIssues,
		})
		return exit.Error(1)
	}

	// Build the cluster spec:
	specBuilder := publicv1.ClusterSpec_builder{
		Template:           template.GetId(),
		TemplateParameters: templateParameterValues,
	}
	c.applyOptionalSpecFields(&specBuilder, pullSecret, sshPublicKey)
	return c.createCluster(ctx, specBuilder.Build())
}

// resolveCredentials reads pull secret and SSH public key from file flags when specified.
func (c *runnerContext) resolveCredentials() (pullSecret, sshPublicKey string, err error) {
	pullSecret = c.args.pullSecret
	if c.args.pullSecretFile != "" {
		data, readErr := os.ReadFile(c.args.pullSecretFile)
		if readErr != nil {
			err = fmt.Errorf("failed to read pull secret file '%s': %w", c.args.pullSecretFile, readErr)
			return
		}
		pullSecret = strings.TrimSpace(string(data))
	}
	sshPublicKey = c.args.sshPublicKey
	if c.args.sshPublicKeyFile != "" {
		data, readErr := os.ReadFile(c.args.sshPublicKeyFile)
		if readErr != nil {
			err = fmt.Errorf("failed to read SSH public key file '%s': %w", c.args.sshPublicKeyFile, readErr)
			return
		}
		sshPublicKey = strings.TrimSpace(string(data))
	}
	return
}

// applyOptionalSpecFields sets pull secret, SSH public key, version, and network CIDRs
// on the spec builder when their corresponding flags are provided.
func (c *runnerContext) applyOptionalSpecFields(
	specBuilder *publicv1.ClusterSpec_builder, pullSecret, sshPublicKey string,
) {
	if pullSecret != "" {
		specBuilder.PullSecret = &pullSecret
	}
	if sshPublicKey != "" {
		specBuilder.SshPublicKey = &sshPublicKey
	}
	if c.args.version != "" {
		specBuilder.VersionName = &c.args.version
	}
	if c.args.podCIDR != "" || c.args.serviceCIDR != "" {
		networkBuilder := publicv1.ClusterNetwork_builder{}
		if c.args.podCIDR != "" {
			networkBuilder.PodCidr = &c.args.podCIDR
		}
		if c.args.serviceCIDR != "" {
			networkBuilder.ServiceCidr = &c.args.serviceCIDR
		}
		specBuilder.Network = networkBuilder.Build()
	}
}

// createCluster creates a cluster with the given spec and prints the result.
func (c *runnerContext) createCluster(ctx context.Context, spec *publicv1.ClusterSpec) error {
	cluster := publicv1.Cluster_builder{
		Metadata: publicv1.Metadata_builder{
			Name:   c.args.name,
			Tenant: c.settings.Tenant(),
		}.Build(),
		Spec: spec,
	}.Build()
	response, err := c.clustersClient.Create(ctx, publicv1.ClustersCreateRequest_builder{
		Object: cluster,
	}.Build())
	if err != nil {
		return fmt.Errorf("failed to create cluster: %w", err)
	}
	cluster = response.Object
	c.console.Infof(ctx, "Created cluster '%s'.\n", cluster.Id)
	return nil
}

// findTemplate finds a cluster template by identifier or name. It tries to find by identifier first, and if that fails
// it searches for templates matching the value as either an identifier or name using a server-side filter. If there is
// exactly one match it returns it. If there are multiple matches it displays them to the user and returns an error. If
// there are no matches it displays available templates and returns an error.
func (c *runnerContext) findTemplate(ctx context.Context) (result *publicv1.ClusterTemplate, err error) {
	// Try to find the template by identifier or name using a filter:
	filter := fmt.Sprintf(
		"this.id == %[1]q || this.metadata.name == %[1]q",
		c.args.template,
	)
	response, err := c.templatesClient.List(ctx, publicv1.ClusterTemplatesListRequest_builder{
		Filter: proto.String(filter),
		Limit:  proto.Int32(10),
	}.Build())
	if err != nil {
		return nil, fmt.Errorf("failed to list templates: %w", err)
	}
	total := response.GetTotal()
	matches := response.GetItems()

	// If there is exactly one match, use it:
	if len(matches) == 1 {
		result = matches[0]
		return
	}

	// If there are multiple matches, display them and advise to use the identifier:
	if len(matches) > 1 {
		c.console.Render(ctx, "template_conflict.txt", map[string]any{
			"Matches": matches,
			"Ref":     c.args.template,
			"Total":   total,
		})
		err = exit.Error(1)
		return
	}

	// If we are here then no matches were found, we will show to the user some of the available templates:
	response, err = c.templatesClient.List(ctx, publicv1.ClusterTemplatesListRequest_builder{
		Limit: proto.Int32(10),
	}.Build())
	if err != nil {
		return nil, fmt.Errorf("failed to list templates: %w", err)
	}
	examples := response.GetItems()
	c.console.Render(ctx, "template_not_found.txt", map[string]any{
		"Examples": examples,
		"Ref":      c.args.template,
	})
	err = exit.Error(1)
	return
}

// findVersion finds a cluster version by metadata.name or spec.version. If the reference matches a metadata.name and a
// spec.version of different versions, the metadata.name match takes precedence. If no match is found it displays
// available versions and returns an error.
func (c *runnerContext) findVersion(ctx context.Context) (result *publicv1.ClusterVersion, err error) {
	// Try to find the version by metadata.name or spec.version using a filter:
	filter := fmt.Sprintf(
		"this.metadata.name == %[1]q || this.spec.version == %[1]q",
		c.args.version,
	)
	// At most 2 matches: one by metadata.name + one by spec.version (both are unique per tenant).
	response, err := c.clusterVersionsClient.List(ctx, publicv1.ClusterVersionsListRequest_builder{
		Filter: proto.String(filter),
		Limit:  proto.Int32(2),
	}.Build())
	if err != nil {
		return nil, fmt.Errorf("failed to list cluster versions: %w", err)
	}
	matches := response.GetItems()

	// If there is exactly one match, use it:
	if len(matches) == 1 {
		result = matches[0]
		return
	}

	// If there are two matches, one matched by metadata.name and one by spec.version. The metadata.name match takes
	// precedence:
	if len(matches) == 2 {
		for _, m := range matches {
			if m.GetMetadata().GetName() == c.args.version {
				result = m
				return
			}
		}
		// Both matched but neither has metadata.name == ref; fall back to the first match:
		result = matches[0]
		return
	}

	// If we are here then no matches were found, we will show to the user some of the available versions:
	response, err = c.clusterVersionsClient.List(ctx, publicv1.ClusterVersionsListRequest_builder{
		Limit: proto.Int32(10),
	}.Build())
	if err != nil {
		return nil, fmt.Errorf("failed to list cluster versions: %w", err)
	}
	examples := response.GetItems()
	c.console.Render(ctx, "version_not_found.txt", map[string]any{
		"Examples": examples,
		"Ref":      c.args.version,
	})
	err = exit.Error(1)
	return
}

// parseTemplateParameters parses the '--template-parameter' and '--template-parameter-file' flags into a map of
// parameter name to value, and a list of issues found. The issues are intended for display to the user.
func (c *runnerContext) parseTemplateParameters(ctx context.Context,
	template *publicv1.ClusterTemplate) (result map[string]*anypb.Any, issues []string) {
	// Prepare empty results and issues:
	result = map[string]*anypb.Any{}

	// Make a map of parameter definitions indexed by name for quick lookup:
	definitions := map[string]*publicv1.ClusterTemplateParameterDefinition{}
	for _, definition := range template.GetParameters() {
		definitions[definition.GetName()] = definition
	}

	// Parse '--template-parameter' flags:
	for _, flag := range c.args.templateParameterValues {
		parts := strings.SplitN(flag, "=", 2)
		if len(parts) != 2 {
			name := strings.TrimSpace(flag)
			definition := definitions[name]
			if definition == nil {
				issues = append(
					issues,
					fmt.Sprintf(
						"In '%s' parameter '%s' doesn't exist, and if it existed the value "+
							"would be missing",
						flag, name,
					),
				)
			} else {
				issues = append(
					issues,
					fmt.Sprintf(
						"In '%s' parameter value is missing",
						flag,
					),
				)
			}
			continue
		}
		name := strings.TrimSpace(parts[0])
		if name == "" {
			issues = append(
				issues,
				fmt.Sprintf(
					"In '%s' parameter name is missing",
					flag,
				),
			)
			continue
		}
		definition := definitions[name]
		if definition == nil {
			issues = append(
				issues,
				fmt.Sprintf(
					"In '%s' parameter '%s' doesn't exist",
					flag, name,
				),
			)
			continue
		}
		text := strings.TrimSpace(parts[1])
		value, issue := c.convertTextToTemplateParameterValue(ctx, text, definition.GetType())
		if issue != "" {
			issues = append(issues, fmt.Sprintf("In '%s' %s", flag, issue))
			continue
		}
		result[name] = value
	}

	// Parse '--template-parameter-file' flags:
	for _, flag := range c.args.templateParameterFiles {
		parts := strings.SplitN(flag, "=", 2)
		if len(parts) != 2 {
			name := strings.TrimSpace(flag)
			definition := definitions[name]
			if definition == nil {
				issues = append(issues, fmt.Sprintf(
					"In '%s' parameter '%s' doesn't exist, and if existed the file would be "+
						"missing",
					flag, name,
				))
			} else {
				issues = append(
					issues,
					fmt.Sprintf(
						"In '%s' file is missing",
						flag,
					))
			}
			continue
		}
		name := strings.TrimSpace(parts[0])
		if name == "" {
			issues = append(
				issues,
				fmt.Sprintf(
					"In '%s' parameter name is missing",
					flag,
				),
			)
			continue
		}
		definition := definitions[name]
		if definition == nil {
			issues = append(
				issues,
				fmt.Sprintf(
					"In '%s' parameter '%s' doesn't exist",
					flag, name,
				),
			)
			continue
		}
		file := strings.TrimSpace(parts[1])
		if file == "" {
			issues = append(
				issues,
				fmt.Sprintf(
					"In '%s' file is missing",
					flag,
				),
			)
			continue
		}
		data, err := os.ReadFile(filepath.Clean(file))
		if errors.Is(err, os.ErrNotExist) {
			issues = append(
				issues, fmt.Sprintf(
					"In '%s' file '%s' doesn't exist",
					flag, file,
				),
			)
			continue
		}
		if err != nil {
			issues = append(
				issues,
				fmt.Sprintf(
					"In '%s' failed to read file '%s': %v",
					flag, file, err,
				),
			)
			continue
		}
		text := string(data)
		value, issue := c.convertTextToTemplateParameterValue(ctx, text, definition.GetType())
		if issue != "" {
			issues = append(
				issues,
				fmt.Sprintf("In '%s' %s'", flag, issue),
			)
			continue
		}
		result[name] = value
	}

	// Add issues for missing required parameters, at the end of the list and sorted by parameter name:
	var missing []*publicv1.ClusterTemplateParameterDefinition
	for _, definition := range template.GetParameters() {
		if definition.GetRequired() && result[definition.GetName()] == nil {
			missing = append(missing, definition)
		}
	}
	sort.Slice(missing, func(i, j int) bool {
		return missing[i].GetName() < missing[j].GetName()
	})
	for _, definition := range missing {
		issues = append(
			issues,
			fmt.Sprintf("Parameter '%s' is required", definition.GetName()),
		)
	}

	return
}

// convertTextToTemplateParameterValue converts a string value to the appropriate protobuf type based on the kind. It
// returns the value and a string descibing the issue if the conversion fails.
func (c *runnerContext) convertTextToTemplateParameterValue(ctx context.Context, text,
	kind string) (result *anypb.Any, issue string) {
	var wrapper proto.Message
	switch kind {
	case "type.googleapis.com/google.protobuf.StringValue":
		wrapper = &wrapperspb.StringValue{Value: text}
	case "type.googleapis.com/google.protobuf.BoolValue":
		text = strings.TrimSpace(text)
		value, err := strconv.ParseBool(text)
		if err != nil {
			c.logger.DebugContext(
				ctx,
				"Failed to parse boolean",
				slog.String("text", text),
				slog.Any("error", err),
			)
			issue = fmt.Sprintf(
				"value '%s' isn't a valid boolean, valid values are 'true' and 'false'",
				text,
			)
			return
		}
		wrapper = &wrapperspb.BoolValue{Value: value}
	case "type.googleapis.com/google.protobuf.Int32Value":
		text = strings.TrimSpace(text)
		var value int64
		value, err := strconv.ParseInt(text, 10, 32)
		if err != nil {
			c.logger.DebugContext(
				ctx,
				"Failed to parse 32-bit integer number",
				slog.String("text", text),
				slog.Any("error", err),
			)
			issue = fmt.Sprintf("value '%s' isn't a valid 32-bit integer", text)
			return
		}
		wrapper = &wrapperspb.Int32Value{Value: int32(value)}
	case "type.googleapis.com/google.protobuf.Int64Value":
		text = strings.TrimSpace(text)
		var value int64
		value, err := strconv.ParseInt(text, 10, 64)
		if err != nil {
			c.logger.DebugContext(
				ctx,
				"Failed to parse 64-bit integer number",
				slog.String("text", text),
				slog.Any("error", err),
			)
			issue = fmt.Sprintf("value '%s' isn't a valid 64-bit integer", text)
			return
		}
		wrapper = &wrapperspb.Int64Value{Value: value}
	case "type.googleapis.com/google.protobuf.FloatValue":
		text = strings.TrimSpace(text)
		var value float64
		value, err := strconv.ParseFloat(text, 32)
		if err != nil {
			c.logger.DebugContext(
				ctx,
				"Failed to parse 32-bit floating point number",
				slog.String("text", text),
				slog.Any("error", err),
			)
			issue = fmt.Sprintf("value '%s' isn't a valid 32-bit floating point number", text)
			return
		}
		wrapper = &wrapperspb.FloatValue{Value: float32(value)}
	case "type.googleapis.com/google.protobuf.DoubleValue":
		text = strings.TrimSpace(text)
		var value float64
		value, err := strconv.ParseFloat(text, 64)
		if err != nil {
			c.logger.DebugContext(
				ctx,
				"Failed to parse 64-bit floating point number",
				slog.String("text", text),
				slog.Any("error", err),
			)
			issue = fmt.Sprintf("value '%s' isn't a valid 64-bit floating point numberw", text)
			return
		}
		wrapper = &wrapperspb.DoubleValue{Value: value}
	case "type.googleapis.com/google.protobuf.BytesValue":
		wrapper = &wrapperspb.BytesValue{Value: []byte(text)}
	case "type.googleapis.com/google.protobuf.Timestamp":
		text = strings.TrimSpace(text)
		var value time.Time
		value, err := time.Parse(time.RFC3339, text)
		if err != nil {
			c.logger.DebugContext(
				ctx,
				"Failed to parse RFC3339 timestamp",
				slog.String("text", text),
				slog.Any("error", err),
			)
			issue = fmt.Sprintf("value '%s' isn't a valid RFC3339 timestamp", text)
			return
		}
		wrapper = timestamppb.New(value)
	case "type.googleapis.com/google.protobuf.Duration":
		var value time.Duration
		value, err := time.ParseDuration(text)
		if err != nil {
			c.logger.DebugContext(
				ctx,
				"Failed to parse duration",
				slog.String("text", text),
				slog.Any("error", err),
			)
			issue = fmt.Sprintf("value '%s' isn't a valid duration", text)
			return
		}
		wrapper = durationpb.New(value)
	default:
		issue = fmt.Sprintf("flag has is of an unsupported type '%s'", kind)
		return
	}
	if issue != "" {
		return
	}
	result, err := anypb.New(wrapper)
	if err != nil {
		c.logger.DebugContext(
			ctx,
			"Failed to create protobuf value for template parameter",
			slog.String("text", text),
			slog.String("kind", kind),
			slog.Any("error", err),
		)
		issue = fmt.Sprintf("Failed to create protobuf value for template parameter: %v", err)
		return
	}
	return
}

// validTemplateParameter contains the information about a valid template parameter, for use in the error messages that
// display them.
type validTemplateParameter struct {
	// Name is the name of the parameter.
	Name string

	// Type is the type of the parameter.
	Type string

	// Title is the title of the parameter.
	Title string
}

// validTemplateParameters returns the list of valid template parameters for the given template.
func (c *runnerContext) validTemplateParameters(template *publicv1.ClusterTemplate) []validTemplateParameter {
	// Prepare the results:
	results := []validTemplateParameter{}
	for _, parameter := range template.GetParameters() {
		result := validTemplateParameter{
			Name:  parameter.GetName(),
			Title: parameter.GetTitle(),
		}
		switch parameter.GetType() {
		case "type.googleapis.com/google.protobuf.StringValue":
			result.Type = "string"
		case "type.googleapis.com/google.protobuf.BoolValue":
			result.Type = "boolean"
		case "type.googleapis.com/google.protobuf.Int32Value":
			result.Type = "int32"
		case "type.googleapis.com/google.protobuf.Int64Value":
			result.Type = "int64"
		case "type.googleapis.com/google.protobuf.FloatValue":
			result.Type = "float"
		case "type.googleapis.com/google.protobuf.DoubleValue":
			result.Type = "double"
		case "type.googleapis.com/google.protobuf.BytesValue":
			result.Type = "bytes"
		case "type.googleapis.com/google.protobuf.Timestamp":
			result.Type = "timestamp"
		case "type.googleapis.com/google.protobuf.Duration":
			result.Type = "duration"
		default:
			result.Type = "unknown"
		}
		results = append(results, result)
	}

	// Sort the result by name so that the output will be predictable:
	sort.Slice(results, func(i, j int) bool {
		return results[i].Name < results[j].Name
	})

	return results
}

const shortHelp = `Create a cluster`

const longHelp = `
Create a cluster.
`

const nameFlagHelp = `
_NAME_ - Name of the cluster.
`

const templateFlagHelp = `
_TEMPLATE_ - Template identifier or name. Mutually exclusive with
{{ bt }}--catalog-item{{ bt }}.
`

const catalogItemFlagHelp = `
_ID_ - Catalog item identifier. Mutually exclusive with
{{ bt }}--template{{ bt }}.
`

const templateParameterFlagHelp = `
_NAME=VALUE_ - Template parameter in the format
{{ bt }}name=value{{ bt }}. Can be specified multiple times.
`

const templateParameterFileFlagHelp = `
_NAME=FILE_ - Template parameter whose value is read from a file, in the
format {{ bt }}name=filename{{ bt }}. Can be specified multiple
times.
`

const pullSecretFlagHelp = `
_SECRET_ - Pull secret for authenticating to image repositories, provided as
an inline value. See also {{ bt }}--pull-secret-file{{ bt }}.
`

const pullSecretFileFlagHelp = `
_FILE_ - Path to a file containing the pull secret.
`

const sshPublicKeyFlagHelp = `
_KEY_ - SSH public key to install on cluster worker nodes, provided as an
inline value. See also {{ bt }}--ssh-public-key-file{{ bt }}.
`

const sshPublicKeyFileFlagHelp = `
_FILE_ - Path to a file containing the SSH public key.
`

const versionFlagHelp = `
_VERSION_ - ClusterVersion {{ bt }}metadata.name{{ bt }} (for example
{{ bt }}4-17-0{{ bt }}) or {{ bt }}spec.version{{ bt }} string (for example
{{ bt }}4.17.0{{ bt }}). If both match different versions, the name takes
precedence. If omitted, the template or system default version is used.
`

const podCIDRFlagHelp = `
_CIDR_ - CIDR for the cluster's pod network. If omitted the server default
is used.
`

const serviceCIDRFlagHelp = `
_CIDR_ - CIDR for the cluster's service network. If omitted the server
default is used.
`

const setFlagHelp = `
_KEY=VALUE_ - Set a spec field or template parameter on the resource.
Use dot notation for nested fields (e.g.
{{ bt }}template_parameters.vpc_id=vpc-123{{ bt }}). Only supported
with {{ bt }}--catalog-item{{ bt }}. Can be specified multiple times.
`

/*
Copyright (c) 2025 Red Hat Inc.

Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except in compliance with the
License. You may obtain a copy of the License at

  http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on an
"AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the specific
language governing permissions and limitations under the License.
*/

package computeinstance

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
		Use:                   "computeinstance [FLAG...]",
		Aliases:               []string{string(proto.MessageName((*publicv1.ComputeInstance)(nil)))},
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
		&runner.args.instanceType,
		"instance-type",
		"",
		instanceTypeFlagHelp,
	)
	flags.StringVar(
		&runner.args.imageSourceRef,
		"image",
		"",
		imageFlagHelp,
	)
	flags.StringVar(
		&runner.args.imageSourceType,
		"image-source-type",
		"registry",
		imageSourceTypeFlagHelp,
	)
	flags.StringVar(
		&runner.args.sshPublicKey,
		"ssh-public-key",
		"",
		sshPublicKeyFlagHelp,
	)
	flags.Int32Var(
		&runner.args.bootDiskSizeGiB,
		"boot-disk-size",
		0,
		bootDiskSizeFlagHelp,
	)
	flags.StringSliceVar(
		&runner.args.additionalDisks,
		"additional-disk",
		[]string{},
		additionalDiskFlagHelp,
	)
	flags.StringVar(
		&runner.args.runStrategy,
		"run-strategy",
		"",
		runStrategyFlagHelp,
	)
	flags.StringVar(
		&runner.args.userData,
		"user-data",
		"",
		userDataFlagHelp,
	)
	flags.StringArrayVar(
		&runner.args.networkAttachments,
		"network-attachment",
		nil,
		networkAttachmentFlagHelp,
	)
	flags.BoolVar(
		&runner.args.windows,
		"windows",
		false,
		windowsFlagHelp,
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
		instanceType            string
		imageSourceRef          string
		imageSourceType         string
		sshPublicKey            string
		bootDiskSizeGiB         int32
		additionalDisks         []string
		runStrategy             string
		userData                string
		networkAttachments      []string
		windows                 bool
	}
	logger                 *slog.Logger
	console                *terminal.Console
	settings               *config.Settings
	templatesClient        publicv1.ComputeInstanceTemplatesClient
	computeInstancesClient publicv1.ComputeInstancesClient
}

func (c *runnerContext) run(cmd *cobra.Command, args []string) error {
	var err error

	// Get the context:
	ctx := cmd.Context()

	// Get the logger and console:
	c.logger = logging.LoggerFromContext(ctx)
	c.console = terminal.ConsoleFromContext(ctx)

	// Add the templates file system to the console:
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
	c.templatesClient = publicv1.NewComputeInstanceTemplatesClient(conn)
	c.computeInstancesClient = publicv1.NewComputeInstancesClient(conn)

	if c.args.catalogItem != "" {
		// Catalog item path: skip template lookup entirely (per D-04).
		specResult, specErr := c.buildSpecFromCatalogItem(c.args.catalogItem)
		if specErr != nil {
			return specErr
		}
		if err := fieldutil.ApplyFields(specResult, c.args.setFields); err != nil {
			return err
		}

		computeInstance := publicv1.ComputeInstance_builder{
			Metadata: publicv1.Metadata_builder{
				Name:   c.args.name,
				Tenant: c.settings.Tenant(),
			}.Build(),
			Spec: specResult,
		}.Build()

		response, err := c.computeInstancesClient.Create(ctx, publicv1.ComputeInstancesCreateRequest_builder{
			Object: computeInstance,
		}.Build())
		if err != nil {
			return fmt.Errorf("failed to create compute instance: %w", err)
		}

		for _, w := range response.GetWarnings() {
			fmt.Fprintf(os.Stderr, "Warning: %s\n", w)
		}

		computeInstance = response.Object
		c.console.Infof(ctx, "Created compute instance '%s'.\n", computeInstance.Id)
		return nil
	}

	// Legacy template path (existing code continues below):

	// Fetch the compute instance template:
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

	// Build the spec:
	spec, err := c.buildSpec(template.GetId(), templateParameterValues)
	if err != nil {
		return err
	}

	// Prepare the compute instance:
	computeInstance := publicv1.ComputeInstance_builder{
		Metadata: publicv1.Metadata_builder{
			Name: c.args.name,
		}.Build(),
		Spec: spec,
	}.Build()

	// Create the compute instance:
	response, err := c.computeInstancesClient.Create(ctx, publicv1.ComputeInstancesCreateRequest_builder{
		Object: computeInstance,
	}.Build())
	if err != nil {
		return fmt.Errorf("failed to create compute instance: %w", err)
	}

	// Display warnings from the server response:
	for _, w := range response.GetWarnings() {
		fmt.Fprintf(os.Stderr, "Warning: %s\n", w)
	}

	// Display the result:
	computeInstance = response.Object
	c.console.Infof(ctx, "Created compute instance '%s'.\n", computeInstance.Id)

	return nil
}

// findTemplate finds a compute instance template by identifier or name. It tries to find by identifier or name using a
// server-side filter. If there is exactly one match it returns it. If there are multiple matches it displays them to
// the user and returns an error. If there are no matches it displays available templates and returns an error.
func (c *runnerContext) findTemplate(ctx context.Context) (result *publicv1.ComputeInstanceTemplate, err error) {
	// Try to find the template by identifier or name using a filter:
	filter := fmt.Sprintf(
		"this.id == %[1]q || this.metadata.name == %[1]q",
		c.args.template,
	)
	response, err := c.templatesClient.List(ctx, publicv1.ComputeInstanceTemplatesListRequest_builder{
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
	response, err = c.templatesClient.List(ctx, publicv1.ComputeInstanceTemplatesListRequest_builder{
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

// parseTemplateParameters parses the '--template-parameter' and '--template-parameter-file' flags into a map of
// parameter name to value, and a list of issues found. The issues are intended for display to the user.
func (c *runnerContext) parseTemplateParameters(ctx context.Context,
	template *publicv1.ComputeInstanceTemplate) (result map[string]*anypb.Any, issues []string) {
	// Prepare empty results and issues:
	result = map[string]*anypb.Any{}

	// Make a map of parameter definitions indexed by name for quick lookup:
	definitions := map[string]*publicv1.ComputeInstanceTemplateParameterDefinition{}
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
	var missing []*publicv1.ComputeInstanceTemplateParameterDefinition
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

// buildSpec constructs the ComputeInstanceSpec from template info and CLI flags.
func (c *runnerContext) buildSpec(templateID string,
	templateParams map[string]*anypb.Any) (*publicv1.ComputeInstanceSpec, error) {
	spec := publicv1.ComputeInstanceSpec_builder{
		Template:           templateID,
		TemplateParameters: templateParams,
	}
	if c.args.imageSourceRef != "" {
		spec.Image = publicv1.ComputeInstanceImage_builder{
			SourceType: c.args.imageSourceType,
			SourceRef:  c.args.imageSourceRef,
		}.Build()
	}
	if c.args.instanceType != "" {
		spec.InstanceType = proto.String(c.args.instanceType)
	}
	if c.args.sshPublicKey != "" {
		spec.SshPublicKey = proto.String(c.args.sshPublicKey)
	}
	if c.args.bootDiskSizeGiB > 0 {
		spec.BootDisk = publicv1.ComputeInstanceDisk_builder{
			SizeGib: c.args.bootDiskSizeGiB,
		}.Build()
	}
	if len(c.args.additionalDisks) > 0 {
		disks, err := parseAdditionalDisks(c.args.additionalDisks)
		if err != nil {
			return nil, err
		}
		spec.AdditionalDisks = disks
	}
	if c.args.runStrategy != "" {
		spec.RunStrategy = proto.String(c.args.runStrategy)
	}
	if c.args.userData != "" {
		spec.UserData = proto.String(c.args.userData)
	}
	if c.args.windows {
		spec.IsWindows = proto.Bool(true)
	}
	if err := c.applyNetworkingFlags(&spec); err != nil {
		return nil, err
	}
	return spec.Build(), nil
}

// applyNetworkingFlags sets spec.network_attachments from CLI flags.
func (c *runnerContext) applyNetworkingFlags(spec *publicv1.ComputeInstanceSpec_builder) error {
	if len(c.args.networkAttachments) == 0 {
		return nil
	}

	attachments := make([]*publicv1.NetworkAttachment, 0, len(c.args.networkAttachments))
	for _, raw := range c.args.networkAttachments {
		na, err := parseNetworkAttachmentFlag(raw)
		if err != nil {
			return err
		}
		attachments = append(attachments, na)
	}
	spec.NetworkAttachments = attachments
	return nil
}

// extractSecurityGroupListSuffix returns the substring before a trailing "security-groups=" or "security_groups="
// clause (case-insensitive) and the list parsed from the remainder of the string after that clause.
// Works entirely on the lowercase copy to avoid Unicode byte-offset issues.
func extractSecurityGroupListSuffix(s string) (prefix string, groups []string, ok bool) {
	lower := strings.ToLower(s)

	// Try both "security-groups=" and "security_groups="
	for _, marker := range []string{"security-groups=", "security_groups="} {
		if i := strings.Index(lower, marker); i >= 0 {
			// Work entirely on lowercase copy to avoid Unicode byte/rune offset mismatches
			prefix = strings.TrimSpace(strings.TrimSuffix(lower[:i], ","))
			rest := strings.TrimSpace(lower[i+len(marker):])
			for _, id := range strings.Split(rest, ",") {
				id = strings.TrimSpace(id)
				if id != "" {
					groups = append(groups, id)
				}
			}
			return prefix, groups, true
		}
	}
	return s, nil, false
}

// parseMainSubnetOnly parses the subnet portion of --network-attachment (no security-groups clause): either a bare id
// or exactly subnet=<id>, optionally with a single comma between other parts only if we add more keys later.
func parseMainSubnetOnly(main string) (string, error) {
	main = strings.TrimSpace(strings.TrimSuffix(main, ","))
	if main == "" {
		return "", fmt.Errorf("--network-attachment must include a subnet or subnet=<id>")
	}
	if !strings.Contains(main, "=") {
		return main, nil
	}
	var subnet string
	for _, fragment := range strings.Split(main, ",") {
		fragment = strings.TrimSpace(fragment)
		if fragment == "" {
			continue
		}
		key, val, ok := strings.Cut(fragment, "=")
		if !ok {
			return "", fmt.Errorf("invalid --network-attachment fragment %q (expected key=value)", fragment)
		}
		key = strings.TrimSpace(strings.ToLower(key))
		val = strings.TrimSpace(val)
		if val == "" {
			return "", fmt.Errorf("invalid --network-attachment fragment %q (value is empty)", fragment)
		}
		if key != "subnet" {
			return "", fmt.Errorf("unknown key %q before security-groups (use subnet)", key)
		}
		if subnet != "" {
			return "", fmt.Errorf("subnet appears more than once in --network-attachment %q", main)
		}
		subnet = val
	}
	if subnet == "" {
		return "", fmt.Errorf("--network-attachment %q must include subnet=... or be a bare subnet id", main)
	}
	return subnet, nil
}

// parseNetworkAttachmentFlag parses one --network-attachment value: a bare subnet id, or subnet=<id> with optional
// security-groups=/security_groups= suffix (commas allowed in the group list).
func parseNetworkAttachmentFlag(s string) (*publicv1.NetworkAttachment, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return nil, fmt.Errorf("empty --network-attachment value")
	}
	prefix, securityGroups, hadGroups := extractSecurityGroupListSuffix(s)
	subnet, err := parseMainSubnetOnly(prefix)
	if err != nil {
		return nil, err
	}
	if !hadGroups && !strings.Contains(s, "=") {
		return publicv1.NetworkAttachment_builder{Subnet: s}.Build(), nil
	}
	return publicv1.NetworkAttachment_builder{
		Subnet:         subnet,
		SecurityGroups: securityGroups,
	}.Build(), nil
}

// buildSpecFromCatalogItem builds the spec for catalog-item-based creation.
func (c *runnerContext) buildSpecFromCatalogItem(catalogItemID string) (*publicv1.ComputeInstanceSpec, error) {
	spec := publicv1.ComputeInstanceSpec_builder{
		CatalogItem: catalogItemID,
	}
	if c.args.imageSourceRef != "" {
		spec.Image = publicv1.ComputeInstanceImage_builder{
			SourceType: c.args.imageSourceType,
			SourceRef:  c.args.imageSourceRef,
		}.Build()
	}
	if c.args.instanceType != "" {
		spec.InstanceType = proto.String(c.args.instanceType)
	}
	if c.args.sshPublicKey != "" {
		spec.SshPublicKey = proto.String(c.args.sshPublicKey)
	}
	if c.args.bootDiskSizeGiB > 0 {
		spec.BootDisk = publicv1.ComputeInstanceDisk_builder{
			SizeGib: c.args.bootDiskSizeGiB,
		}.Build()
	}
	if len(c.args.additionalDisks) > 0 {
		disks, diskErr := parseAdditionalDisks(c.args.additionalDisks)
		if diskErr != nil {
			return nil, diskErr
		}
		spec.AdditionalDisks = disks
	}
	if c.args.runStrategy != "" {
		spec.RunStrategy = proto.String(c.args.runStrategy)
	}
	if c.args.userData != "" {
		spec.UserData = proto.String(c.args.userData)
	}
	if c.args.windows {
		spec.IsWindows = proto.Bool(true)
	}
	if err := c.applyNetworkingFlags(&spec); err != nil {
		return nil, err
	}
	return spec.Build(), nil
}

// parseAdditionalDisks parses disk sizes in GiB.
// Example: "100"
func parseAdditionalDisks(diskArgs []string) ([]*publicv1.ComputeInstanceDisk, error) {
	disks := make([]*publicv1.ComputeInstanceDisk, 0, len(diskArgs))
	for _, arg := range diskArgs {
		sizeGiB, err := strconv.ParseInt(arg, 10, 32)
		if err != nil {
			return nil, fmt.Errorf("invalid disk size '%s': expected an integer number of GiB", arg)
		}
		disks = append(disks, publicv1.ComputeInstanceDisk_builder{
			SizeGib: int32(sizeGiB),
		}.Build())
	}
	return disks, nil
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
func (c *runnerContext) validTemplateParameters(template *publicv1.ComputeInstanceTemplate) []validTemplateParameter {
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

const shortHelp = `Create a compute instance.`

const longHelp = `
Create a compute instance.
`

const nameFlagHelp = `
_NAME_ - Name of the compute instance.
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

const instanceTypeFlagHelp = `
_NAME_ - Instance type name. Specifies the compute resource
configuration for this instance.
`

const imageFlagHelp = `
_URL_ - Image reference, for example an OCI image URL.
`

const imageSourceTypeFlagHelp = `
_TYPE_ - Image source type.
`

const sshPublicKeyFlagHelp = `
_KEY_ - SSH public key.
`

const bootDiskSizeFlagHelp = `
_SIZE_ - Boot disk size in GiB.
`

const additionalDiskFlagHelp = `
_SIZE_ - Additional disk size in GiB. Can be specified multiple times to add
more than one disk.
`

const runStrategyFlagHelp = `
_STRATEGY_ - Run strategy, for example {{ bt }}Always{{ bt }} or
{{ bt }}Halted{{ bt }}.
`

const userDataFlagHelp = `
_DATA_ - User data for the compute instance, for example cloud-init or
ignition configuration.
`

const networkAttachmentFlagHelp = `
_SPEC_ - Per-NIC network attachment. The value can be a plain subnet ID, or a
comma-separated specification in the format
{{ bt }}subnet=ID[,security-groups=ID,ID...]{{ bt }}. Can be
specified multiple times to attach multiple NICs.
`

const windowsFlagHelp = `
_[BOOLEAN]_ - Create a Windows VM. Defaults to {{ bt }}false{{ bt }} (Linux VM).
`

const setFlagHelp = `
_KEY=VALUE_ - Set a spec field or template parameter on the resource.
Use dot notation for nested fields (e.g.
{{ bt }}template_parameters.vpc_id=vpc-123{{ bt }}). Only supported
with {{ bt }}--catalog-item{{ bt }}. Can be specified multiple times.
`

/*
Copyright (c) 2025 Red Hat Inc.

Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except in compliance with the
License. You may obtain a copy of the License at

  http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on an
"AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the specific
language governing permissions and limitations under the License.
*/

package edit

import (
	"bytes"
	"context"
	"embed"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"

	"github.com/spf13/cobra"
	"google.golang.org/grpc"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protoreflect"
	"gopkg.in/yaml.v3"

	privatev1 "github.com/osac-project/fulfillment-service/internal/api/osac/private/v1"
	publicv1 "github.com/osac-project/fulfillment-service/internal/api/osac/public/v1"
	"github.com/osac-project/fulfillment-service/internal/config"
	"github.com/osac-project/fulfillment-service/internal/logging"
	"github.com/osac-project/fulfillment-service/internal/packages"
	"github.com/osac-project/fulfillment-service/internal/reflection"
	"github.com/osac-project/fulfillment-service/internal/terminal"
)

//go:embed templates
var templatesFS embed.FS

// Possible output formats:
const (
	outputFormatJson = "json"
	outputFormatYaml = "yaml"
)

func Cmd() *cobra.Command {
	runner := &runnerContext{
		marshalOptions: protojson.MarshalOptions{
			UseProtoNames: true,
		},
	}
	result := &cobra.Command{
		Use:                   "edit [FLAG...] OBJECT ID|NAME",
		DisableFlagsInUseLine: true,
		Short:                 shortHelp,
		Long:                  longHelp,
		RunE:                  runner.run,
		ValidArgsFunction:     completeObjectTypes,
	}
	flags := result.Flags()
	flags.StringVarP(
		&runner.format,
		"output",
		"o",
		outputFormatYaml,
		outputFlagHelp,
	)
	return result
}

type runnerContext struct {
	logger         *slog.Logger
	console        *terminal.Console
	format         string
	conn           *grpc.ClientConn
	marshalOptions protojson.MarshalOptions
	helper         reflection.ObjectHelper
}

func (c *runnerContext) run(cmd *cobra.Command, args []string) error {
	var err error

	// Get the context:
	ctx := cmd.Context()

	// Get the logger and the console:
	c.logger = logging.LoggerFromContext(ctx)
	c.console = terminal.ConsoleFromContext(ctx)

	// Load the templates for the console messages:
	err = c.console.AddTemplates(templatesFS, "templates")
	if err != nil {
		return fmt.Errorf("failed to load templates: %w", err)
	}

	// Get the configuration:
	cfg := config.SettingsFromContext(ctx)
	if !cfg.Armed() {
		return fmt.Errorf("there is no configuration, run the 'login' command")
	}

	// Create the gRPC connection from the configuration:
	c.conn, err = cfg.Connect(ctx, cmd.Flags())
	if err != nil {
		return fmt.Errorf("failed to create gRPC connection: %w", err)
	}
	defer c.conn.Close()

	// Create the reflection helper:
	helper, err := reflection.NewHelper().
		SetLogger(c.logger).
		SetConnection(c.conn).
		AddPackages(cfg.Packages()).
		SetTenantFunc(config.TenantFromContext).
		Build()
	if err != nil {
		return fmt.Errorf("failed to create reflection tool: %w", err)
	}
	c.console.SetHelper(helper)

	// Check that the object type has been specified:
	if len(args) == 0 {
		c.console.Render(ctx, "no_object.txt", map[string]any{
			"Helper": helper,
		})
		return nil
	}

	// Get the information about the object type:
	c.helper = helper.Lookup(args[0])
	if c.helper == nil {
		c.console.Render(ctx, "wrong_object.txt", map[string]any{
			"Helper": helper,
			"Object": args[0],
		})
		return nil
	}

	// Check the flags:
	if c.format != outputFormatJson && c.format != outputFormatYaml {
		return fmt.Errorf(
			"unknown output format '%s', should be '%s' or '%s'",
			c.format, outputFormatJson, outputFormatYaml,
		)
	}

	// Check that the object identifier or name has been specified:
	if len(args) < 2 {
		c.console.Render(ctx, "no_id.txt", map[string]any{})
		return nil
	}
	key := args[1]

	// Find the object by identifier or name:
	object, err := c.helper.FindObject(ctx, key, c.console)
	if err != nil {
		return err
	}

	// Render the object:
	var render func(proto.Message) ([]byte, error)
	switch c.format {
	case outputFormatJson:
		render = c.renderJson
	default:
		render = c.renderYaml
	}
	data, err := render(object)
	if err != nil {
		return err
	}

	// Write the rendered object to a temporary file:
	tmpDir, err := os.MkdirTemp("", "")
	if err != nil {
		return err
	}
	defer func() {
		err := os.RemoveAll(tmpDir)
		if err != nil {
			c.logger.ErrorContext(
				ctx,
				"Failed to remove temporary directory",
				slog.String("dir", tmpDir),
				slog.Any("error", err),
			)
		}
	}()
	objectId := c.helper.GetId(object)
	tmpFile := filepath.Join(tmpDir, fmt.Sprintf("%s-%s.%s", c.helper, objectId, c.format))
	err = os.WriteFile(tmpFile, data, 0600)
	if err != nil {
		return fmt.Errorf("failed to create temporary file '%s': %w", tmpFile, err)
	}

	// Run the editor:
	editorName := c.findEditor(ctx)
	editorPath, err := exec.LookPath(editorName)
	if err != nil {
		return fmt.Errorf("failed to find editor command '%s': %w", editorName, err)
	}
	editorCmd := &exec.Cmd{
		Path: editorPath,
		Args: []string{
			editorName,
			tmpFile,
		},
		Stdin:  os.Stdin,
		Stdout: os.Stdout,
		Stderr: os.Stderr,
	}
	err = editorCmd.Run()
	if err != nil {
		return fmt.Errorf("failed to edit: %w", err)
	}

	// Load the potentiall modified file:
	data, err = os.ReadFile(filepath.Clean(tmpFile))
	if err != nil {
		return fmt.Errorf("failed to read back temporary file '%s': %w", tmpFile, err)
	}

	// Parse the result:
	var parse func([]byte) (proto.Message, error)
	switch c.format {
	case outputFormatJson:
		parse = c.parseJson
	default:
		parse = c.parseYaml
	}
	object, err = parse(data)
	if err != nil {
		return fmt.Errorf("failed to parse modified object: %w", err)
	}

	// Save the result:
	updated, err := c.update(ctx, object)
	if err != nil {
		return err
	}

	if c.isWatchable() {
		c.showWatchSuggestion(ctx, updated)
	}

	return nil
}

// findEditor tries to find the name of the editor command. It will first try with the content of the `EDITOR` and
// `VISUAL` environment variables, and if those are empty it defaults to `vi`.
func (c *runnerContext) findEditor(ctx context.Context) string {
	for _, editorEnvVar := range editorEnvVars {
		value, ok := os.LookupEnv(editorEnvVar)
		if ok && value != "" {
			c.logger.DebugContext(
				ctx,
				"Found editor using environment variable",
				slog.String("var", editorEnvVar),
				slog.String("value", value),
			)
			return value
		}
	}
	c.logger.InfoContext(
		ctx,
		"Didn't find a editor in the environment, will use the default",
		slog.Any("vars", editorEnvVars),
		slog.String("default", defaultEditor),
	)
	return defaultEditor
}

func (c *runnerContext) update(ctx context.Context, object proto.Message) (result proto.Message, err error) {
	result, err = c.helper.Update(ctx, object)
	return
}

func (c *runnerContext) isWatchable() bool {
	objectFullName := c.helper.Descriptor().FullName()
	for _, eventDesc := range []protoreflect.MessageDescriptor{
		(&publicv1.Event{}).ProtoReflect().Descriptor(),
		(&privatev1.Event{}).ProtoReflect().Descriptor(),
	} {
		payloadOneof := eventDesc.Oneofs().ByName("payload")
		if payloadOneof == nil {
			continue
		}
		for i := 0; i < payloadOneof.Fields().Len(); i++ {
			field := payloadOneof.Fields().Get(i)
			if field.Message() != nil && field.Message().FullName() == objectFullName {
				return true
			}
		}
	}
	return false
}

func (c *runnerContext) showWatchSuggestion(ctx context.Context, object proto.Message) {
	objectId := c.helper.GetId(object)
	c.console.Render(ctx, "watch_suggestion.txt", map[string]any{
		"Object": c.helper.Singular(),
		"Id":     objectId,
	})
}

func (c *runnerContext) renderJson(object proto.Message) (result []byte, err error) {
	result, err = c.marshalOptions.Marshal(object)
	return
}

func (c *runnerContext) renderYaml(object proto.Message) (result []byte, err error) {
	data, err := c.renderJson(object)
	if err != nil {
		return
	}
	var value any
	err = json.Unmarshal(data, &value)
	if err != nil {
		return
	}
	buffer := &bytes.Buffer{}
	encoder := yaml.NewEncoder(buffer)
	encoder.SetIndent(2)
	err = encoder.Encode(value)
	if err != nil {
		return
	}
	result = buffer.Bytes()
	return
}

func (c *runnerContext) parseJson(data []byte) (result proto.Message, err error) {
	object := c.helper.Instance()
	err = protojson.Unmarshal(data, object)
	if err != nil {
		return
	}
	result = object
	return
}

func (c *runnerContext) parseYaml(data []byte) (result proto.Message, err error) {
	var value any
	err = yaml.Unmarshal(data, &value)
	if err != nil {
		return
	}
	data, err = json.Marshal(value)
	if err != nil {
		return
	}
	result, err = c.parseJson(data)
	return
}

// editorEnvVars is the list of environment variables that will be used to obtain the name of the editor command.
var editorEnvVars = []string{
	"EDITOR",
	"VISUAL",
}

// defualtEditor is the editor used when the environment variables don't indicate any other editor.
const defaultEditor = "vi"

func completeObjectTypes(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
	if len(args) != 0 {
		return nil, cobra.ShellCompDirectiveNoFileComp
	}
	return reflection.ObjectTypeNames(packages.Public...), cobra.ShellCompDirectiveNoFileComp
}

const shortHelp = `Edit objects`

const longHelp = `
Edit an object by opening it in a text editor.

The object is fetched from the server, rendered as YAML (or JSON), and opened in your preferred editor. When you save
and close the editor the modified object is sent back to the server.

To edit a cluster:

{{ bt 3 }}shell
{{ binary }} edit cluster my-cluster
{{ bt 3 }}

The editor is selected from the {{ bt }}EDITOR{{ bt }} or {{ bt }}VISUAL{{ bt }} environment variables, falling back to
{{ bt }}vi{{ bt }} if neither is set.

By default the object is rendered as YAML. Use the {{ bt }}--output{{ bt }} flag to switch to JSON:

{{ bt 3 }}shell
{{ binary }} edit cluster my-cluster -o json
{{ bt 3 }}

Objects can be referenced by their identifier or by their name.
`

const outputFlagHelp = `
_FORMAT_ - Format used for editing. Must be one of {{ bt }}json{{ bt }} or {{ bt }}yaml{{ bt }}.
`

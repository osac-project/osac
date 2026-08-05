/*
Copyright (c) 2025 Red Hat Inc.

Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except in compliance with the
License. You may obtain a copy of the License at

  http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on an
"AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the specific
language governing permissions and limitations under the License.
*/

package annotate

import (
	"embed"
	"fmt"
	"log/slog"
	"strings"

	"github.com/spf13/cobra"
	"google.golang.org/grpc"

	"github.com/osac-project/fulfillment-service/internal/config"
	"github.com/osac-project/fulfillment-service/internal/logging"
	"github.com/osac-project/fulfillment-service/internal/packages"
	"github.com/osac-project/fulfillment-service/internal/reflection"
	"github.com/osac-project/fulfillment-service/internal/terminal"
)

//go:embed templates
var templatesFS embed.FS

// Cmd creates and returns the command that adds or removes annotations.
func Cmd() *cobra.Command {
	runner := &runnerContext{}
	result := &cobra.Command{
		Use:                   "annotate [FLAG...] OBJECT ID|NAME KEY=VALUE...",
		DisableFlagsInUseLine: true,
		Short:                 shortHelp,
		Long:                  longHelp,
		RunE:                  runner.run,
		ValidArgsFunction:     completeObjectTypes,
	}
	return result
}

type runnerContext struct {
	logger  *slog.Logger
	console *terminal.Console
	conn    *grpc.ClientConn
	helper  reflection.ObjectHelper
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

	// Check that the object identifier or name has been specified:
	if len(args) < 2 {
		c.console.Render(ctx, "no_id.txt", map[string]any{})
		return nil
	}
	ref := args[1]

	// Check that at least one annotation operation has been specified:
	if len(args) < 3 {
		c.console.Render(ctx, "no_annotations.txt", map[string]any{})
		return nil
	}

	// Parse the annotation operations:
	operations, err := c.parseAnnotationOperations(args[2:])
	if err != nil {
		return err
	}

	// Find the object by identifier or name:
	object, err := c.helper.FindObject(ctx, ref, c.console)
	if err != nil {
		return err
	}

	// Apply the annotation operations:
	metadata := c.helper.GetMetadata(object)
	c.applyAnnotationOperations(metadata, operations)

	// Save the result:
	_, err = c.helper.Update(ctx, object)
	if err != nil {
		return err
	}

	return nil
}

// annotationOperation represents a single annotation set or remove operation.
type annotationOperation struct {
	key    string
	value  *string
	remove bool
}

func (c *runnerContext) parseAnnotationOperations(values []string) (result []annotationOperation, err error) {
	for _, value := range values {
		var operation annotationOperation
		operation, err = c.parseAnnotationOperation(value)
		if err != nil {
			return
		}
		result = append(result, operation)
	}
	return
}

func (c *runnerContext) parseAnnotationOperation(text string) (operation annotationOperation, err error) {
	key, value, ok := strings.Cut(text, "=")
	if ok {
		if key == "" {
			err = fmt.Errorf("annotation name can't be empty in %q", text)
			return
		}
		operation = annotationOperation{
			key:   key,
			value: &value,
		}
		return
	}
	if strings.HasSuffix(text, "-") {
		key := strings.TrimSuffix(text, "-")
		if key == "" {
			err = fmt.Errorf("annotation name can't be empty in %q", text)
			return
		}
		operation = annotationOperation{
			key:    key,
			remove: true,
		}
		return
	}
	err = fmt.Errorf("invalid annotation specification %q, expected 'annotation=value' or 'annotation-'", text)
	return
}

func (c *runnerContext) applyAnnotationOperations(metadata reflection.Metadata, operations []annotationOperation) {
	annotations := metadata.GetAnnotations()
	if annotations == nil {
		annotations = map[string]string{}
	}
	for _, operation := range operations {
		if operation.remove {
			delete(annotations, operation.key)
			continue
		}
		annotations[operation.key] = *operation.value
	}
	if len(annotations) > 0 || metadata.GetAnnotations() != nil {
		metadata.SetAnnotations(annotations)
	}
}

func completeObjectTypes(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
	if len(args) != 0 {
		return nil, cobra.ShellCompDirectiveNoFileComp
	}
	return reflection.ObjectTypeNames(packages.Public...), cobra.ShellCompDirectiveNoFileComp
}

const shortHelp = `Add or remove annotations from objects`

const longHelp = `
Add or remove annotations from objects.

Annotations are key-value pairs attached to objects that can be used to store arbitrary metadata, such as contact
information, descriptions or links to external systems.

To add or update an annotation use the {{ bt }}key=value{{ bt }} syntax:

{{ bt 3 }}shell
{{ binary }} annotate cluster my-cluster description="Production cluster"
{{ bt 3 }}

Multiple annotations can be set at once:

{{ bt 3 }}shell
{{ binary }} annotate cluster my-cluster owner=team-a env=production
{{ bt 3 }}

To remove an annotation append a dash ({{ bt }}-{{ bt }}) to the key name:

{{ bt 3 }}shell
{{ binary }} annotate cluster my-cluster owner-
{{ bt 3 }}

Adding and removing annotations can be combined in a single command:

{{ bt 3 }}shell
{{ binary }} annotate cluster my-cluster owner- env=staging
{{ bt 3 }}

Objects can be referenced by their identifier or by their name.
`

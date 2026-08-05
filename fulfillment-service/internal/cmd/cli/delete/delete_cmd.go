/*
Copyright (c) 2025 Red Hat Inc.

Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except in compliance with the
License. You may obtain a copy of the License at

  http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on an
"AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the specific
language governing permissions and limitations under the License.
*/

package delete

import (
	"context"
	"embed"
	"fmt"
	"log/slog"
	"strconv"
	"strings"

	"github.com/spf13/cobra"
	"google.golang.org/grpc"
	grpccodes "google.golang.org/grpc/codes"
	grpcstatus "google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"

	"github.com/osac-project/fulfillment-service/internal/config"
	"github.com/osac-project/fulfillment-service/internal/exit"
	"github.com/osac-project/fulfillment-service/internal/logging"
	"github.com/osac-project/fulfillment-service/internal/packages"
	"github.com/osac-project/fulfillment-service/internal/reflection"
	"github.com/osac-project/fulfillment-service/internal/terminal"
)

//go:embed templates
var templatesFS embed.FS

func Cmd() *cobra.Command {
	runner := &runnerContext{}
	result := &cobra.Command{
		Use:                   "delete [FLAG...] OBJECT ID|NAME...",
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

	// Check that at least one object identifier or name has been specified:
	if len(args) < 2 {
		c.console.Render(ctx, "no_id.txt", map[string]any{})
		return nil
	}

	// Get the object helper:
	c.helper = helper.Lookup(args[0])
	if c.helper == nil {
		c.console.Render(ctx, "wrong_object.txt", map[string]any{
			"Helper": helper,
			"Object": args[0],
		})
		return nil
	}

	// Find all objects matching the provided references using a single list operation:
	refs := args[1:]
	matches, err := c.findMatches(ctx, refs)
	if err != nil {
		return err
	}

	// Validate that each reference has exactly one match. If any resolution fails or is ambiguous we stop and show
	// the error without deleting anything.
	objects := make([]proto.Message, 0, len(refs))
	for _, ref := range refs {
		matches := matches[ref]
		switch len(matches) {
		case 0:
			c.console.Render(ctx, "no_matches.txt", map[string]any{
				"Object": c.helper.Singular(),
				"Ref":    ref,
			})
			return nil
		case 1:
			objects = append(objects, matches[0])
		default:
			c.console.Render(ctx, "multiple_matches.txt", map[string]any{
				"Matches": matches,
				"Object":  c.helper.Singular(),
				"Ref":     ref,
				"Total":   len(matches),
			})
			return nil
		}
	}

	// Delete each resolved object. Attempt all deletions and report errors at the end
	var hadErrors bool
	for _, object := range objects {
		id := c.helper.GetId(object)
		err = c.helper.Delete(ctx, id)
		if err != nil {
			hadErrors = true
			status, ok := grpcstatus.FromError(err)
			if ok && status.Code() == grpccodes.NotFound {
				c.console.Errorf(
					ctx,
					"Can't delete %s '%s' because it doesn't exist.\n",
					args[0], id,
				)
			} else {
				c.console.Errorf(
					ctx,
					"Failed to delete %s '%s': %v\n",
					args[0], id, err,
				)
			}
			continue
		}
		c.console.Infof(ctx, "Deleted %s '%s'.\n", args[0], id)
	}
	if hadErrors {
		return exit.Error(1)
	}

	return nil
}

// findMatches finds all objects matching the provided references using a single list operation. It builds a filter that
// matches all the provided references at once and returns a map where the key is the reference and the value is the
// list of matching objectx.
func (c *runnerContext) findMatches(ctx context.Context, refs []string) (result map[string][]proto.Message, err error) {
	// Build a filter that matches all references:
	quoted := make([]string, len(refs))
	for i, ref := range refs {
		quoted[i] = strconv.Quote(ref)
	}
	list := strings.Join(quoted, ", ")
	filter := fmt.Sprintf(`this.id in [%[1]s] || this.metadata.name in [%[1]s]`, list)

	// Find all objects matching any of the references:
	response, err := c.helper.List(ctx, reflection.ListOptions{
		Filter: filter,
	})
	if err != nil {
		err = fmt.Errorf("failed to find objects of type '%s': %w", c.helper, err)
		return
	}

	// Build a map where the key is the reference and the value is the list of matching objects:
	result = map[string][]proto.Message{}
	for _, object := range response.Items {
		id := c.helper.GetId(object)
		name := c.helper.GetName(object)
		for _, ref := range refs {
			if id == ref || name == ref {
				matches := result[ref]
				result[ref] = append(matches, object)
			}
		}
	}

	return
}

func completeObjectTypes(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
	if len(args) != 0 {
		return nil, cobra.ShellCompDirectiveNoFileComp
	}
	return reflection.ObjectTypeNames(packages.Public...), cobra.ShellCompDirectiveNoFileComp
}

const shortHelp = `Delete objects`

const longHelp = `
Delete one or more objects from the server.

Objects can be referenced by their identifier or by their name.

To delete a single cluster:

{{ bt 3 }}shell
{{ binary }} delete clusters my-cluster
{{ bt 3 }}

Multiple objects can be deleted at once:

{{ bt 3 }}shell
{{ binary }} delete clusters my-cluster my-other-cluster
{{ bt 3 }}

All specified objects are resolved before any deletion takes place. If any
reference is ambiguous or not found the command stops without deleting
anything.
`

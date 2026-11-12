/*
Copyright (c) 2026 Red Hat Inc.

Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except in compliance with the
License. You may obtain a copy of the License at

  http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on an
"AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the specific
language governing permissions and limitations under the License.
*/

package cani

import (
	"fmt"
	"log/slog"
	"os"
	"strings"

	"github.com/gobuffalo/flect"
	"github.com/spf13/cobra"

	"github.com/osac-project/osac/fulfillment-service/internal/config"
	"github.com/osac-project/osac/fulfillment-service/internal/logging"
	"github.com/osac-project/osac/fulfillment-service/internal/reflection"
	"github.com/osac-project/osac/fulfillment-service/internal/terminal"
	publicv1 "github.com/osac-project/osac/proto/gen/osac/public/v1"
)

func Cmd() *cobra.Command {
	runner := &runnerContext{}
	result := &cobra.Command{
		Use:                   "can-i METHOD SERVICE",
		Short:                 shortHelp,
		Long:                  longHelp,
		DisableFlagsInUseLine: true,
		Args:                  cobra.ExactArgs(2),
		RunE:                  runner.run,
	}
	flags := result.Flags()
	flags.BoolVarP(
		&runner.args.quiet,
		"quiet",
		"q",
		false,
		quietFlagHelp,
	)
	return result
}

type runnerContext struct {
	args struct {
		quiet bool
	}
	logger   *slog.Logger
	console  *terminal.Console
	settings *config.Settings
	helper   reflection.Helper
}

func (c *runnerContext) run(cmd *cobra.Command, args []string) error {
	ctx := cmd.Context()

	c.logger = logging.LoggerFromContext(ctx)
	c.console = terminal.ConsoleFromContext(ctx)

	c.settings = config.SettingsFromContext(ctx)
	if !c.settings.Armed() {
		return fmt.Errorf("there is no configuration, run the 'login' command")
	}

	methodArg := args[0]
	serviceArg := args[1]

	// Map method argument to gRPC method name
	method, err := toMethod(methodArg)
	if err != nil {
		return err
	}

	conn, err := c.settings.Connect(ctx, cmd.Flags())
	if err != nil {
		return fmt.Errorf("failed to create gRPC connection: %w", err)
	}
	defer conn.Close()

	// Create reflection helper to discover services
	c.helper, err = reflection.NewHelper().
		SetLogger(c.logger).
		SetConnection(conn).
		AddPackages(c.settings.Packages()).
		Build()
	if err != nil {
		return fmt.Errorf("failed to create reflection helper: %w", err)
	}

	// Map service argument to full service name using reflection
	service, err := c.toService(serviceArg)
	if err != nil {
		return err
	}

	client := publicv1.NewSelfSubjectAccessReviewsClient(conn)

	review := publicv1.SelfSubjectAccessReview_builder{
		Metadata: publicv1.Metadata_builder{
			Name:   "can-i-check",
			Tenant: c.settings.Tenant(),
		}.Build(),
		Spec: publicv1.SelfSubjectAccessReviewSpec_builder{
			Service: service,
			Method:  method,
		}.Build(),
	}.Build()

	response, err := client.Create(ctx, publicv1.SelfSubjectAccessReviewsCreateRequest_builder{Object: review}.Build())
	if err != nil {
		return fmt.Errorf("failed to check permissions: %w", err)
	}

	result := response.GetObject()
	allowed := result.GetStatus().GetAllowed()

	if !c.args.quiet {
		if allowed {
			c.console.Infof(ctx, "yes\n")
		} else {
			c.console.Infof(ctx, "no")
			if reason := result.GetStatus().GetReason(); reason != "" {
				c.console.Infof(ctx, " - %s", reason)
			}
			c.console.Infof(ctx, "\n")
		}
	}

	// Exit with code 1 if denied
	if !allowed {
		os.Exit(1)
	}

	return nil
}

// toMethod maps user-friendly method names to gRPC method names
func toMethod(methodArg string) (string, error) {
	methodArg = strings.ToLower(methodArg)
	switch methodArg {
	case "create":
		return "Create", nil
	case "get":
		return "Get", nil
	case "list":
		return "List", nil
	case "update":
		return "Update", nil
	case "delete":
		return "Delete", nil
	case "watch":
		return "Watch", nil
	default:
		return "", fmt.Errorf("unknown method %q, supported methods: create, get, list, update, delete, watch", methodArg)
	}
}

// toService maps user-friendly service argument to full service name using reflection
func (c *runnerContext) toService(serviceArg string) (string, error) {
	// Look up the service using the reflection helper (supports singular and plural)
	objectHelper := c.helper.Lookup(serviceArg)
	if objectHelper == nil {
		return "", fmt.Errorf("unknown service %q", serviceArg)
	}

	// Extract service name from the object's full name
	// For example: osac.public.v1.Cluster -> osac.public.v1.Clusters
	fullName := string(objectHelper.FullName())
	lastDot := strings.LastIndex(fullName, ".")
	if lastDot == -1 {
		return "", fmt.Errorf("invalid object type format: %s", fullName)
	}

	packageName := fullName[:lastDot]
	// The service name is the package + CamelCased plural form
	serviceName := packageName + "." + flect.Pascalize(objectHelper.Plural())

	return serviceName, nil
}

const shortHelp = `Check whether you can perform an action`

const longHelp = `
Check whether you have permission to perform an action on a service type.

The command takes a method (create, get, list, update, delete, watch) and a service type,
and returns whether the current user has permission to perform that action.

Exit codes:
  0 - Permission allowed
  1 - Permission denied or error

Examples:

{{ bt 3 }}shell
# Check if you can create clusters
{{ binary }} can-i create clusters

# Check if you can list virtual networks
{{ binary }} can-i list virtualnetworks

# Check if you can delete compute instances
{{ binary }} can-i delete computeinstances

# Quiet mode (useful for scripts)
{{ binary }} can-i create clusters --quiet && echo "Creating cluster..."
{{ bt 3 }}
`

const quietFlagHelp = `
Only return exit code (0 for allowed, 1 for denied). No output is printed.
`

/*
Copyright (c) 2025 Red Hat Inc.

Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except in compliance with the
License. You may obtain a copy of the License at

  http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on an
"AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the specific
language governing permissions and limitations under the License.
*/

package create

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"

	"github.com/spf13/cobra"
	"google.golang.org/grpc"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/anypb"
	"gopkg.in/yaml.v3"

	privatev1 "github.com/osac-project/fulfillment-service/internal/api/osac/private/v1"
	"github.com/osac-project/fulfillment-service/internal/cmd/cli/create/baremetalinstance"
	"github.com/osac-project/fulfillment-service/internal/cmd/cli/create/baremetalinstancecatalogitem"
	"github.com/osac-project/fulfillment-service/internal/cmd/cli/create/cluster"
	"github.com/osac-project/fulfillment-service/internal/cmd/cli/create/clustercatalogitem"
	"github.com/osac-project/fulfillment-service/internal/cmd/cli/create/clusterversion"
	"github.com/osac-project/fulfillment-service/internal/cmd/cli/create/computeinstance"
	"github.com/osac-project/fulfillment-service/internal/cmd/cli/create/computeinstancecatalogitem"
	"github.com/osac-project/fulfillment-service/internal/cmd/cli/create/externalip"
	"github.com/osac-project/fulfillment-service/internal/cmd/cli/create/externalipattachment"
	"github.com/osac-project/fulfillment-service/internal/cmd/cli/create/hub"
	"github.com/osac-project/fulfillment-service/internal/cmd/cli/create/instancetype"
	"github.com/osac-project/fulfillment-service/internal/cmd/cli/create/natgateway"
	"github.com/osac-project/fulfillment-service/internal/cmd/cli/create/securitygroup"
	"github.com/osac-project/fulfillment-service/internal/cmd/cli/create/storagebackend"
	"github.com/osac-project/fulfillment-service/internal/cmd/cli/create/storagetier"
	"github.com/osac-project/fulfillment-service/internal/cmd/cli/create/subnet"
	"github.com/osac-project/fulfillment-service/internal/cmd/cli/create/virtualnetwork"
	"github.com/osac-project/fulfillment-service/internal/config"
	"github.com/osac-project/fulfillment-service/internal/logging"
	"github.com/osac-project/fulfillment-service/internal/reflection"
	"github.com/osac-project/fulfillment-service/internal/terminal"
)

func Cmd() *cobra.Command {
	runner := &runnerContext{}
	result := &cobra.Command{
		Use:                   "create [FLAG...] -f FILE",
		DisableFlagsInUseLine: true,
		Short:                 shortHelp,
		Long:                  longHelp,
		RunE:                  runner.run,
	}
	result.AddCommand(baremetalinstance.Cmd())
	result.AddCommand(baremetalinstancecatalogitem.Cmd())
	result.AddCommand(cluster.Cmd())
	result.AddCommand(clustercatalogitem.Cmd())
	result.AddCommand(clusterversion.Cmd())
	result.AddCommand(computeinstance.Cmd())
	result.AddCommand(computeinstancecatalogitem.Cmd())
	result.AddCommand(externalip.Cmd())
	result.AddCommand(externalipattachment.Cmd())
	result.AddCommand(hub.Cmd())
	result.AddCommand(instancetype.Cmd())
	result.AddCommand(natgateway.Cmd())
	result.AddCommand(virtualnetwork.Cmd())
	result.AddCommand(subnet.Cmd())
	result.AddCommand(securitygroup.Cmd())
	result.AddCommand(storagebackend.Cmd())
	result.AddCommand(storagetier.Cmd())
	flags := result.Flags()
	flags.StringVarP(
		&runner.args.file,
		"filename",
		"f",
		"",
		filenameFlagHelp,
	)
	return result
}

type runnerContext struct {
	args struct {
		file string
	}
	logger   *slog.Logger
	console  *terminal.Console
	settings *config.Settings
	conn     *grpc.ClientConn
}

func (c *runnerContext) run(cmd *cobra.Command, args []string) error {
	// Get the context:
	ctx := cmd.Context()

	// Get the logger and console:
	c.logger = logging.LoggerFromContext(ctx)
	c.console = terminal.ConsoleFromContext(ctx)

	// Get the configuration:
	c.settings = config.SettingsFromContext(ctx)
	if !c.settings.Armed() {
		return fmt.Errorf("there is no configuration, run the 'login' command")
	}

	// Create the gRPC connection from the configuration:
	var err error
	c.conn, err = c.settings.Connect(ctx, cmd.Flags())
	if err != nil {
		return fmt.Errorf("failed to create gRPC connection: %w", err)
	}
	defer c.conn.Close()

	// Create the reflection helper:
	helper, err := reflection.NewHelper().
		SetLogger(c.logger).
		SetConnection(c.conn).
		AddPackages(c.settings.Packages()).
		SetTenantFunc(config.TenantFromContext).
		Build()
	if err != nil {
		return fmt.Errorf("failed to create reflection tool: %w", err)
	}

	// Check the flags:
	if c.args.file == "" {
		return fmt.Errorf("it is mandatory to specify the input file with the '--filename' or '-f' options")
	}

	// Open the input:
	var reader io.ReadCloser
	if c.args.file == "-" {
		reader = os.Stdin
	} else {
		reader, err = os.Open(c.args.file)
		if err != nil {
			return fmt.Errorf("failed to open the file '%s': %w", c.args.file, err)
		}
		defer func() {
			reader.Close()
			if err != nil {
				c.logger.LogAttrs(
					ctx,
					slog.LevelError,
					"Failed to close file",
					slog.String("file", c.args.file),
					slog.Any("error", err),
				)
			}
		}()
	}

	// Convert the input to a list of objects, and then create them:
	objects, err := c.decodeObjects(reader)
	if err != nil {
		return err
	}
	tenant := config.TenantFromContext(ctx)
	for i, object := range objects {
		objectDesc := object.ProtoReflect().Descriptor()
		objectType := string(objectDesc.FullName())
		objectHelper := helper.Lookup(objectType)
		if objectHelper == nil {
			return fmt.Errorf("input object at index %d is of an unknown type '%s'", i, objectType)
		}
		if tenant != "" && objectHelper.IsTenantScoped() {
			objectHelper.SetTenant(object, tenant)
		}
		object, err = objectHelper.Create(ctx, object)
		if err != nil {
			return fmt.Errorf("failed to create object at index %d: %w", i, err)
		}
		objectSingular := objectHelper.Singular()
		objectId := objectHelper.GetId(object)
		objectName := objectHelper.GetName(object)
		if objectName != "" {
			c.console.Infof(
				ctx,
				"Created %s with name '%s' and identifier '%s'.\n",
				objectSingular, objectName, objectId,
			)
		} else {
			c.console.Infof(
				ctx,
				"Created %s with identifier '%s'.\n",
				objectSingular, objectId,
			)
		}
		if tenant, ok := object.(*privatev1.Tenant); ok {
			if tenant.HasStatus() && tenant.GetStatus().HasBreakGlassCredentials() {
				creds := tenant.GetStatus().GetBreakGlassCredentials()
				c.console.Infof(ctx, "\n")
				c.console.Infof(ctx, "Break-glass account credentials (shown only once, save them now):\n")
				c.console.Infof(ctx, "  Username: %s\n", creds.GetUsername())
				c.console.Infof(ctx, "  Password: %s\n", creds.GetPassword())
				c.console.Infof(ctx, "\n")
				c.console.Infof(ctx, "This is a temporary password that must be changed on first login.\n")
			}
		}
	}

	return nil
}

// decode reads the given input, which may contain multiple YAML or JSON documents, each of them being a single object
// or alist, and returns the corresponding list of protocol buffers messages
func (c *runnerContext) decodeObjects(input io.Reader) (result []proto.Message, err error) {
	// Parse the input file assuming it is a YAML file. As JSON is a subset of YAML, this will also work for JSON.
	decoder := yaml.NewDecoder(input)
	var items []any
	for {
		var item any
		err = decoder.Decode(&item)
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return
		}
		items = append(items, item)
	}

	// Items may be a single object or a list of objects. Those that are a list need to be converted to single
	// objects.
	list := make([]any, 0, len(items))
	for _, item := range items {
		switch item := item.(type) {
		case []any:
			list = append(list, item...)
		default:
			list = append(list, item)
		}
	}

	// We assume that input objects are protocol buffers any objects, and we need to convert them to the
	// appropriate type.
	objects := make([]proto.Message, len(list))
	for i, item := range list {
		var data []byte
		data, err = json.Marshal(item)
		if err != nil {
			err = fmt.Errorf("failed to convert item at index %d to JSON: %w", i, err)
			return
		}
		value := &anypb.Any{}
		err = protojson.Unmarshal(data, value)
		if err != nil {
			err = fmt.Errorf(
				"failed to unmarshal item at index %d to a protocol buffers any: %w",
				i, err,
			)
			return
		}
		var object proto.Message
		object, err = value.UnmarshalNew()
		if err != nil {
			err = fmt.Errorf(
				"failed to unmarshal object at index %d to a protocol buffers object: %w",
				i, err,
			)
			return
		}
		objects[i] = object
	}

	result = objects
	return
}

const shortHelp = `Create objects`

const longHelp = `
Create objects from a YAML or JSON file. The input file must contain one or more objects encoded as protocol buffers
{{ bt }}Any{{ bt }} messages, which include the {{ bt }}@type{{ bt }} field to identify the object type.

To create an object from a file:

{{ bt 3 }}shell
{{ binary }} create -f my-cluster.yaml
{{ bt 3 }}

To read the object from standard input:

{{ bt 3 }}shell
cat my-cluster.yaml | {{ binary }} create -f -
{{ bt 3 }}

The input file can contain multiple documents separated by {{ bt }}---{{ bt }}, and each document can be a single object
or a list of objects. All objects are created in order.

There are also subcommands for creating specific types of objects with dedicated flags instead of a file. Use {{ bt
}}--help{{ bt }} on any subcommand for details.
`

const filenameFlagHelp = `
_FILE_ - Name of the file containing the object to create. Use {{ bt }}-{{ bt }} to read from standard input.
`

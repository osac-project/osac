/*
Copyright (c) 2025 Red Hat Inc.

Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except in compliance with the
License. You may obtain a copy of the License at

  http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on an
"AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the specific
language governing permissions and limitations under the License.
*/

package baremetalinstance

import (
	"fmt"
	"log/slog"
	"strings"

	"github.com/spf13/cobra"
	"google.golang.org/protobuf/proto"

	publicv1 "github.com/osac-project/osac/fulfillment-service/internal/api/osac/public/v1"
	"github.com/osac-project/osac/fulfillment-service/internal/cmd/cli/create/fieldutil"
	"github.com/osac-project/osac/fulfillment-service/internal/cmd/cli/create/netutil"
	"github.com/osac-project/osac/fulfillment-service/internal/cmd/cli/lookup"
	"github.com/osac-project/osac/fulfillment-service/internal/config"
	"github.com/osac-project/osac/fulfillment-service/internal/logging"
	"github.com/osac-project/osac/fulfillment-service/internal/terminal"
)

func Cmd() *cobra.Command {
	runner := &runnerContext{}
	result := &cobra.Command{
		Use:                   "baremetalinstance [FLAG...]",
		Aliases:               []string{string(proto.MessageName((*publicv1.BareMetalInstance)(nil)))},
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
	flags.StringVar(
		&runner.args.catalogItem,
		"catalog-item",
		"",
		catalogItemFlagHelp,
	)
	flags.StringVar(
		&runner.args.sshKey,
		"ssh-key",
		"",
		sshKeyFlagHelp,
	)
	flags.StringVar(
		&runner.args.userData,
		"user-data",
		"",
		userDataFlagHelp,
	)
	flags.StringVar(
		&runner.args.runStrategy,
		"run-strategy",
		"",
		runStrategyFlagHelp,
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
	flags.BoolVar(
		&runner.args.externalIPAttachment,
		"external-ip-attachment",
		false,
		externalIPAttachmentFlagHelp,
	)
	flags.StringArrayVar(
		&runner.args.networkAttachments,
		"network-attachment",
		nil,
		networkAttachmentFlagHelp,
	)
	flags.StringArrayVar(
		&runner.args.setFields,
		"set",
		nil,
		setFlagHelp,
	)

	if err := result.MarkFlagRequired("catalog-item"); err != nil {
		panic(fmt.Sprintf("failed to mark catalog-item flag as required: %v", err))
	}
	return result
}

type runnerContext struct {
	args struct {
		name                 string
		catalogItem          string
		setFields            []string
		networkAttachments   []string
		sshKey               string
		userData             string
		runStrategy          string
		imageSourceRef       string
		imageSourceType      string
		externalIPAttachment bool
	}
	logger *slog.Logger
}

func (c *runnerContext) run(cmd *cobra.Command, _ []string) error {
	ctx := cmd.Context()
	c.logger = logging.LoggerFromContext(ctx)
	console := terminal.ConsoleFromContext(ctx)

	cfg := config.SettingsFromContext(ctx)
	if !cfg.Armed() {
		return fmt.Errorf("there is no configuration, run the 'login' command")
	}

	conn, err := cfg.Connect(ctx, cmd.Flags())
	if err != nil {
		return fmt.Errorf("failed to create gRPC connection: %w", err)
	}
	defer conn.Close()

	catalogItemsClient := publicv1.NewBareMetalInstanceCatalogItemsClient(conn)
	catalogItem, err := lookup.Find(c.args.catalogItem, "bare metal instance catalog item",
		func(filter string, limit int32) ([]*publicv1.BareMetalInstanceCatalogItem, error) {
			resp, err := catalogItemsClient.List(ctx, publicv1.BareMetalInstanceCatalogItemsListRequest_builder{
				Filter: proto.String(filter),
				Limit:  proto.Int32(limit),
			}.Build())
			if err != nil {
				return nil, fmt.Errorf("failed to list catalog items: %w", err)
			}
			return resp.GetItems(), nil
		})
	if err != nil {
		return err
	}

	spec := publicv1.BareMetalInstanceSpec_builder{
		CatalogItem: &publicv1.BareMetalInstanceCatalogItemReference{Id: catalogItem.GetId()},
	}
	if c.args.sshKey != "" {
		sshKey := c.args.sshKey
		spec.SshPublicKey = &sshKey
	}
	if c.args.userData != "" {
		userData := c.args.userData
		spec.UserData = &userData
	}
	if c.args.imageSourceRef != "" {
		spec.Image = publicv1.BareMetalInstanceImage_builder{
			SourceType: c.args.imageSourceType,
			SourceRef:  c.args.imageSourceRef,
		}.Build()
	}
	if c.args.runStrategy != "" {
		val, ok := publicv1.BareMetalInstanceRunStrategy_value["BARE_METAL_INSTANCE_RUN_STRATEGY_"+strings.ToUpper(c.args.runStrategy)]
		if !ok {
			return fmt.Errorf(
				"unknown run strategy %q, valid values are Always and Halted",
				c.args.runStrategy,
			)
		}
		rs := publicv1.BareMetalInstanceRunStrategy(val)
		spec.RunStrategy = &rs
	}
	spec.AutoExternalIpAttachment = c.args.externalIPAttachment

	if err := c.applyNetworkingFlags(&spec); err != nil {
		return err
	}

	builtSpec := spec.Build()
	if err := fieldutil.ApplyFields(builtSpec, c.args.setFields); err != nil {
		return err
	}

	bmi := publicv1.BareMetalInstance_builder{
		Metadata: publicv1.Metadata_builder{
			Name: c.args.name,
		}.Build(),
		Spec: builtSpec,
	}.Build()

	client := publicv1.NewBareMetalInstancesClient(conn)
	response, err := client.Create(ctx, publicv1.BareMetalInstancesCreateRequest_builder{
		Object: bmi,
	}.Build())
	if err != nil {
		return fmt.Errorf("failed to create bare metal instance: %w", err)
	}

	console.Infof(ctx, "Created bare metal instance '%s'.\n", response.GetObject().GetId())
	return nil
}

const shortHelp = `Create a bare metal instance`

const longHelp = `
Create a bare metal instance.
`

const nameFlagHelp = `
_NAME_ - Name of the bare metal instance.
`

const catalogItemFlagHelp = `
_ID_ - Catalog item identifier or name. Required.
`

const sshKeyFlagHelp = `
_KEY_ - SSH public key injected into the OS at provisioning time. Must be a
valid OpenSSH public key. Immutable after creation.
`

const userDataFlagHelp = `
_DATA_ - User data passed to the OS at first boot (e.g. cloud-init).
Maximum 64 KB. Immutable after creation.
`

const runStrategyFlagHelp = `
_STRATEGY_ - Run strategy controlling the power state. Valid values are
{{ bt }}Always{{ bt }} (keep powered on) and {{ bt }}Halted{{ bt }}
(power off).
`

const imageFlagHelp = `
_URL_ - Image reference, for example an OCI image URL.
`

const imageSourceTypeFlagHelp = `
_TYPE_ - Image source type.
`

const externalIPAttachmentFlagHelp = `
_[BOOLEAN]_ - When set, the system auto-selects an ExternalIPPool and
creates an ExternalIP with an ExternalIPAttachment for this instance
atomically during creation. Immutable after creation.
`

const networkAttachmentFlagHelp = `
_SPEC_ - Per-NIC network attachment. The value can be a plain subnet ID, or a
comma-separated specification in the format
{{ bt }}subnet=ID[,interface=NAME][,primary][,security-groups=ID,ID...]{{ bt }}.
The {{ bt }}interface{{ bt }} field maps a physical NIC from the bare metal
instance type. The {{ bt }}primary{{ bt }} keyword (bare, no value) designates
the default gateway for multi-NIC instances. Can be specified multiple times
to attach multiple NICs.
`

const setFlagHelp = `
_KEY=VALUE_ - Set a spec field or template parameter on the resource.
Use dot notation for nested fields (e.g.
{{ bt }}template_parameters.vpc_id=vpc-123{{ bt }}). Can be specified
multiple times.
`

func (c *runnerContext) applyNetworkingFlags(spec *publicv1.BareMetalInstanceSpec_builder) error {
	if len(c.args.networkAttachments) == 0 {
		return nil
	}
	attachments := make([]*publicv1.BareMetalNetworkAttachment, 0, len(c.args.networkAttachments))
	for _, raw := range c.args.networkAttachments {
		na, err := parseBareMetalNetworkAttachmentFlag(raw)
		if err != nil {
			return err
		}
		attachments = append(attachments, na)
	}
	spec.NetworkAttachments = attachments
	return nil
}

func parseBareMetalNetworkAttachmentFlag(s string) (*publicv1.BareMetalNetworkAttachment, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return nil, fmt.Errorf("empty --network-attachment value")
	}

	prefix, securityGroups, _ := netutil.ExtractSecurityGroupListSuffix(s)

	if !strings.Contains(prefix, "=") && !strings.EqualFold(prefix, "primary") {
		builder := publicv1.BareMetalNetworkAttachment_builder{
			Subnet: &publicv1.SubnetLocalReference{Id: prefix},
		}
		if len(securityGroups) > 0 {
			sgRefs := make([]*publicv1.SecurityGroupLocalReference, len(securityGroups))
			for i, sg := range securityGroups {
				sgRefs[i] = &publicv1.SecurityGroupLocalReference{Id: sg}
			}
			builder.SecurityGroups = sgRefs
		}
		return builder.Build(), nil
	}

	var subnet, iface string
	var primary bool
	var subnetSeen, ifaceSeen bool

	for _, fragment := range strings.Split(prefix, ",") {
		fragment = strings.TrimSpace(fragment)
		if fragment == "" {
			continue
		}
		if strings.EqualFold(fragment, "primary") {
			primary = true
			continue
		}
		key, val, ok := strings.Cut(fragment, "=")
		if !ok {
			return nil, fmt.Errorf("invalid --network-attachment fragment %q (expected key=value or bare keyword 'primary')", fragment)
		}
		key = strings.TrimSpace(strings.ToLower(key))
		val = strings.TrimSpace(val)
		if val == "" {
			return nil, fmt.Errorf("invalid --network-attachment fragment %q (value is empty)", fragment)
		}
		switch key {
		case "subnet":
			if subnetSeen {
				return nil, fmt.Errorf("subnet appears more than once in --network-attachment %q", prefix)
			}
			subnet = val
			subnetSeen = true
		case "interface":
			if ifaceSeen {
				return nil, fmt.Errorf("interface appears more than once in --network-attachment %q", prefix)
			}
			iface = val
			ifaceSeen = true
		default:
			return nil, fmt.Errorf("unknown key %q in --network-attachment (use subnet, interface, primary, or security-groups)", key)
		}
	}

	if subnet == "" {
		return nil, fmt.Errorf("--network-attachment must include a subnet or subnet=<id>")
	}

	sgRefs := make([]*publicv1.SecurityGroupLocalReference, len(securityGroups))
	for i, sg := range securityGroups {
		sgRefs[i] = &publicv1.SecurityGroupLocalReference{Id: sg}
	}

	builder := publicv1.BareMetalNetworkAttachment_builder{
		Subnet:         &publicv1.SubnetLocalReference{Id: subnet},
		SecurityGroups: sgRefs,
	}
	if iface != "" {
		builder.Interface = &iface
	}
	if primary {
		builder.Primary = &primary
	}
	return builder.Build(), nil
}

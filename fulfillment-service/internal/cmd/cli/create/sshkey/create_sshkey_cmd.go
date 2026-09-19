/*
Copyright (c) 2026 Red Hat Inc.

Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except in compliance with the
License. You may obtain a copy of the License at

  http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on an
"AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the specific
language governing permissions and limitations under the License.
*/

package sshkey

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"
	"google.golang.org/protobuf/proto"

	"github.com/osac-project/osac/fulfillment-service/internal/config"
	"github.com/osac-project/osac/fulfillment-service/internal/terminal"
	publicv1 "github.com/osac-project/osac/proto/gen/osac/public/v1"
)

func Cmd() *cobra.Command {
	runner := &runnerContext{}
	result := &cobra.Command{
		Use:                   "sshkey [FLAG...]",
		Aliases:               []string{string(proto.MessageName((*publicv1.SshKey)(nil)))},
		Short:                 shortHelp,
		Long:                  longHelp,
		DisableFlagsInUseLine: true,
		Args:                  cobra.NoArgs,
		RunE:                  runner.run,
	}
	flags := result.Flags()
	flags.StringVarP(&runner.args.name, "name", "n", "", nameFlagHelp)
	flags.StringVar(&runner.args.publicKey, "public-key", "", publicKeyFlagHelp)
	flags.StringVar(&runner.args.publicKeyFile, "public-key-file", "", publicKeyFileFlagHelp)
	result.MarkFlagRequired("name") //nolint:errcheck
	result.MarkFlagsMutuallyExclusive("public-key", "public-key-file")
	return result
}

type runnerContext struct {
	args struct {
		name          string
		publicKey     string
		publicKeyFile string
	}
	console  *terminal.Console
	settings *config.Settings
}

func (c *runnerContext) run(cmd *cobra.Command, args []string) error {
	ctx := cmd.Context()

	publicKey, err := resolvePublicKey(c.args.publicKey, c.args.publicKeyFile)
	if err != nil {
		return err
	}

	c.console = terminal.ConsoleFromContext(ctx)

	c.settings = config.SettingsFromContext(ctx)
	if !c.settings.Armed() {
		return fmt.Errorf("there is no configuration, run the 'login' command")
	}

	conn, err := c.settings.Connect(ctx, cmd.Flags())
	if err != nil {
		return fmt.Errorf("failed to create gRPC connection: %w", err)
	}
	defer conn.Close()

	client := publicv1.NewSshKeysClient(conn)
	object, err := buildSshKey(c.args.name, publicKey, c.settings.Tenant())
	if err != nil {
		return err
	}

	response, err := client.Create(ctx, publicv1.SshKeysCreateRequest_builder{Object: object}.Build())
	if err != nil {
		return fmt.Errorf("failed to create SSH key: %w", err)
	}

	c.console.Infof(ctx, "Created SSH key '%s' (ID: %s).\n",
		response.GetObject().GetMetadata().GetName(), response.GetObject().GetId())

	return nil
}

func resolvePublicKey(value, path string) (string, error) {
	if value != "" {
		result := strings.TrimSpace(value)
		if result == "" {
			return "", fmt.Errorf("public key must not be empty")
		}
		return result, nil
	}
	if path == "" {
		return "", fmt.Errorf("exactly one of --public-key or --public-key-file is required")
	}

	expandedPath, err := expandHome(path)
	if err != nil {
		return "", err
	}
	data, err := os.ReadFile(filepath.Clean(expandedPath))
	if err != nil {
		return "", fmt.Errorf("failed to read public key file %q: %w", path, err)
	}

	result := strings.TrimSpace(string(data))
	if result == "" {
		return "", fmt.Errorf("public key must not be empty")
	}
	return result, nil
}

func expandHome(path string) (string, error) {
	if path != "~" && !strings.HasPrefix(path, "~/") {
		return path, nil
	}

	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("failed to determine home directory: %w", err)
	}
	if path == "~" {
		return home, nil
	}
	return filepath.Join(home, strings.TrimPrefix(path, "~/")), nil
}

func buildSshKey(name, publicKey, tenant string) (*publicv1.SshKey, error) {
	if name == "" {
		return nil, fmt.Errorf("name is required")
	}
	if strings.TrimSpace(publicKey) == "" {
		return nil, fmt.Errorf("public key must not be empty")
	}

	return publicv1.SshKey_builder{
		Metadata: publicv1.Metadata_builder{
			Name:   name,
			Tenant: tenant,
		}.Build(),
		Spec: publicv1.SshKeySpec_builder{
			PublicKey: publicKey,
		}.Build(),
	}.Build(), nil
}

const shortHelp = `Create an SSH public key`

const longHelp = `
Create an SSH public key from inline material or a public key file.

To create an SSH key from inline public key material:

{{ bt 3 }}shell
{{ binary }} create sshkey --name my-key --public-key "ssh-ed25519 ..."
{{ bt 3 }}

To create an SSH key from a file:

{{ bt 3 }}shell
{{ binary }} create sshkey --name my-key --public-key-file ~/.ssh/id_ed25519.pub
{{ bt 3 }}
`

const nameFlagHelp = `
_NAME_ - Name of the SSH key.
`

const publicKeyFlagHelp = `
_PUBLIC_KEY_ - SSH public key material.
`

const publicKeyFileFlagHelp = `
_PATH_ - Read SSH public key material from a file.
`

/*
Copyright (c) 2026 Red Hat Inc.

Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except in compliance with the
License. You may obtain a copy of the License at

  http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on an
"AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the specific
language governing permissions and limitations under the License.
*/

package secret

import (
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"
	"google.golang.org/protobuf/proto"

	"github.com/osac-project/osac/fulfillment-service/internal/config"
	"github.com/osac-project/osac/fulfillment-service/internal/logging"
	"github.com/osac-project/osac/fulfillment-service/internal/terminal"
	publicv1 "github.com/osac-project/osac/proto/gen/osac/public/v1"
)

func Cmd() *cobra.Command {
	runner := &runnerContext{}
	result := &cobra.Command{
		Use:                   "secret [FLAG...]",
		Aliases:               []string{string(proto.MessageName((*publicv1.Secret)(nil)))},
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
	flags.StringArrayVar(
		&runner.args.fromFile,
		"from-file",
		nil,
		fromFileFlagHelp,
	)
	flags.StringArrayVar(
		&runner.args.labels,
		"label",
		nil,
		labelFlagHelp,
	)
	return result
}

type runnerContext struct {
	args struct {
		name     string
		fromFile []string
		labels   []string
	}
	logger   *slog.Logger
	console  *terminal.Console
	settings *config.Settings
}

func (c *runnerContext) run(cmd *cobra.Command, args []string) error {
	ctx := cmd.Context()

	c.logger = logging.LoggerFromContext(ctx)
	c.console = terminal.ConsoleFromContext(ctx)

	c.settings = config.SettingsFromContext(ctx)
	if !c.settings.Armed() {
		return fmt.Errorf("there is no configuration, run the 'login' command")
	}

	if len(c.args.fromFile) == 0 {
		return fmt.Errorf("at least one --from-file flag is required")
	}

	data, err := parseFromFileFlags(c.args.fromFile)
	if err != nil {
		return err
	}

	labels, err := parseLabels(c.args.labels)
	if err != nil {
		return err
	}

	conn, err := c.settings.Connect(ctx, cmd.Flags())
	if err != nil {
		return fmt.Errorf("failed to create gRPC connection: %w", err)
	}
	defer conn.Close()

	client := publicv1.NewSecretsClient(conn)

	secret := publicv1.Secret_builder{
		Metadata: publicv1.Metadata_builder{
			Name:   c.args.name,
			Tenant: c.settings.Tenant(),
			Labels: labels,
		}.Build(),
		Data: data,
	}.Build()

	response, err := client.Create(ctx, publicv1.SecretsCreateRequest_builder{Object: secret}.Build())
	if err != nil {
		return fmt.Errorf("failed to create secret: %w", err)
	}

	c.console.Infof(ctx, "Created secret '%s' (ID: %s).\n",
		response.GetObject().GetMetadata().GetName(), response.GetObject().GetId())

	return nil
}

// parseFromFileSpec parses a single --from-file flag value into a key and path.
// Formats: "key=path", "path" (key derived from filename), "-" (stdin, key="stdin").
func parseFromFileSpec(value string) (key, path string, err error) {
	if value == "" {
		return "", "", fmt.Errorf("--from-file value must not be empty")
	}

	if k, p, ok := strings.Cut(value, "="); ok {
		key = k
		path = p
		if key == "" {
			return "", "", fmt.Errorf("--from-file key must not be empty in %q", value)
		}
		if path == "" {
			return "", "", fmt.Errorf("--from-file path must not be empty in %q", value)
		}
	} else {
		path = value
		if path == "-" {
			return "", "", fmt.Errorf("--from-file key is required when reading from stdin (use --from-file=KEY=-)")
		}
		key = filepath.Base(path)
	}

	return key, path, nil
}

// parseFromFileFlags parses all --from-file flag values into a map of key to raw bytes.
func parseFromFileFlags(fromFile []string) (map[string][]byte, error) {
	data := make(map[string][]byte, len(fromFile))
	stdinUsed := false

	for _, value := range fromFile {
		key, path, err := parseFromFileSpec(value)
		if err != nil {
			return nil, err
		}

		if _, exists := data[key]; exists {
			return nil, fmt.Errorf("duplicate key %q in --from-file flags", key)
		}

		var content []byte
		if path == "-" {
			if stdinUsed {
				return nil, fmt.Errorf("stdin (-) can only be referenced once in --from-file flags")
			}
			stdinUsed = true
			content, err = io.ReadAll(os.Stdin)
		} else {
			content, err = os.ReadFile(filepath.Clean(path))
		}
		if err != nil {
			return nil, fmt.Errorf("failed to read %q: %w", path, err)
		}

		data[key] = content
	}

	return data, nil
}

// parseLabels parses --label flag values in "key=value" format into a map.
func parseLabels(labels []string) (map[string]string, error) {
	if len(labels) == 0 {
		return nil, nil
	}

	result := make(map[string]string, len(labels))
	for _, label := range labels {
		key, value, ok := strings.Cut(label, "=")
		if !ok {
			return nil, fmt.Errorf("invalid label %q: must be in key=value format", label)
		}
		if key == "" {
			return nil, fmt.Errorf("invalid label %q: key must not be empty", label)
		}
		if _, exists := result[key]; exists {
			return nil, fmt.Errorf("duplicate label key %q", key)
		}
		result[key] = value
	}

	return result, nil
}

const shortHelp = `Create a secret from file contents`

const longHelp = `
Create a secret from one or more files. Each {{ bt }}--from-file{{ bt }} flag adds a key-value
entry to the secret, where the value is the raw file content.

To create a secret with an explicit key:

{{ bt 3 }}shell
{{ binary }} create secret --name my-tls-secret --from-file=tls.crt=/path/to/cert.pem
{{ bt 3 }}

To use the filename as the key:

{{ bt 3 }}shell
{{ binary }} create secret --name my-secret --from-file=/path/to/config.yaml
{{ bt 3 }}

To create a secret with multiple keys:

{{ bt 3 }}shell
{{ binary }} create secret --name my-tls --from-file=tls.crt=/path/to/cert.pem --from-file=tls.key=/path/to/key.pem
{{ bt 3 }}

To read secret data from stdin (an explicit key is required):

{{ bt 3 }}shell
echo "my-value" | {{ binary }} create secret --name my-secret --from-file=password=-
{{ bt 3 }}

To create a secret with labels:

{{ bt 3 }}shell
{{ binary }} create secret --name my-secret --from-file=data.txt --label=env=prod --label=team=backend
{{ bt 3 }}
`

const nameFlagHelp = `
_NAME_ - Name of the secret.
`

const fromFileFlagHelp = `
_[KEY=]PATH_ - Read secret data from a file. If {{ bt }}KEY={{ bt }} is omitted,
the filename is used as the key. Use {{ bt }}KEY=-{{ bt }} to read from standard
input (an explicit key is required for stdin). Can be specified multiple times.
`

const labelFlagHelp = `
_KEY=VALUE_ - Label to set on the secret. Can be specified multiple times.
`

/*
Copyright (c) 2026 Red Hat Inc.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

  http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

// Package regenerate implements credential regeneration commands.
package regenerate

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/spf13/cobra"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"

	"github.com/osac-project/osac/fulfillment-service/internal/cmd/cli/lookup"
	"github.com/osac-project/osac/fulfillment-service/internal/config"
	publicv1 "github.com/osac-project/osac/proto/gen/osac/public/v1"
)

const (
	credentialWaitTimeout time.Duration = 5 * time.Minute
	credentialPollPeriod  time.Duration = time.Second
)

type serviceAccountsClient interface {
	List(context.Context, *publicv1.ServiceAccountsListRequest, ...grpc.CallOption) (*publicv1.ServiceAccountsListResponse, error)
	Get(context.Context, *publicv1.ServiceAccountsGetRequest, ...grpc.CallOption) (*publicv1.ServiceAccountsGetResponse, error)
	RegenerateSecret(context.Context, *publicv1.ServiceAccountsRegenerateSecretRequest, ...grpc.CallOption) (*publicv1.ServiceAccountsRegenerateSecretResponse, error)
}

type secretsClient interface {
	Get(context.Context, *publicv1.SecretsGetRequest, ...grpc.CallOption) (*publicv1.SecretsGetResponse, error)
}

func Cmd() *cobra.Command {
	runner := &runnerContext{}
	return &cobra.Command{
		Use:                   "regenerate [FLAG...] OBJECT ID|NAME",
		DisableFlagsInUseLine: true,
		Short:                 shortHelp,
		Long:                  longHelp,
		Args:                  cobra.ExactArgs(2),
		RunE:                  runner.run,
	}
}

type runnerContext struct {
	accounts serviceAccountsClient
	secrets  secretsClient
}

func (c *runnerContext) run(cmd *cobra.Command, args []string) (err error) {
	resource := strings.ToLower(strings.ReplaceAll(args[0], "-", ""))
	if resource != "serviceaccount" && resource != "serviceaccounts" {
		return fmt.Errorf("credential regeneration is not supported for resource %q", args[0])
	}

	ctx := cmd.Context()
	cfg := config.SettingsFromContext(ctx)
	if !cfg.Armed() {
		return fmt.Errorf("there is no configuration, run the 'login' command")
	}
	conn, err := cfg.Connect(ctx, cmd.Flags())
	if err != nil {
		return fmt.Errorf("failed to create gRPC connection: %w", err)
	}
	defer func() {
		if closeErr := conn.Close(); closeErr != nil && err == nil {
			err = fmt.Errorf("failed to close gRPC connection: %w", closeErr)
		}
	}()

	c.accounts = publicv1.NewServiceAccountsClient(conn)
	c.secrets = publicv1.NewSecretsClient(conn)
	return c.regenerateServiceAccount(ctx, cmd.OutOrStdout(), args[1])
}

func (c *runnerContext) regenerateServiceAccount(ctx context.Context, writer io.Writer, ref string) error {
	account, err := c.findAccount(ctx, ref)
	if err != nil {
		return err
	}
	_, previousSecret, err := c.readCredential(ctx, account)
	if err != nil {
		return err
	}
	if _, err = c.accounts.RegenerateSecret(ctx,
		publicv1.ServiceAccountsRegenerateSecretRequest_builder{Id: account.GetId()}.Build()); err != nil {
		return c.apiError("failed to request service-account secret rotation", err)
	}
	rotated, clientID, secret, err := c.waitForRotation(ctx, account.GetId(),
		account.GetStatus().GetClientSecret().GetId(), previousSecret)
	if err != nil {
		return err
	}
	return c.writeCredential(writer, rotated, clientID, secret)
}

func (c *runnerContext) findAccount(ctx context.Context, ref string) (*publicv1.ServiceAccount, error) {
	return lookup.Find(ref, "service account", func(filter string, limit int32) ([]*publicv1.ServiceAccount, error) {
		response, err := c.accounts.List(ctx, publicv1.ServiceAccountsListRequest_builder{
			Filter: proto.String(filter), Limit: proto.Int32(limit),
		}.Build())
		if err != nil {
			return nil, c.apiError("failed to find service account", err)
		}
		return response.GetItems(), nil
	})
}

func (c *runnerContext) waitForRotation(ctx context.Context, id, previousSecretID, previousSecret string) (*publicv1.ServiceAccount, string, string, error) {
	waitCtx, cancel := context.WithTimeout(ctx, credentialWaitTimeout)
	defer cancel()
	ticker := time.NewTicker(credentialPollPeriod)
	defer ticker.Stop()
	started := time.Now()
	timeoutError := func() error {
		return fmt.Errorf("timed out after %s waiting for service account %q credential Secret to change",
			time.Since(started).Round(time.Millisecond), id)
	}
	for {
		response, err := c.accounts.Get(waitCtx, publicv1.ServiceAccountsGetRequest_builder{Id: id}.Build())
		if err != nil {
			if waitCtx.Err() == context.DeadlineExceeded {
				return nil, "", "", timeoutError()
			}
			return nil, "", "", c.apiError("failed while waiting for service-account secret rotation", err)
		}
		account := response.GetObject()
		if account == nil {
			return nil, "", "", c.olderServiceError("Get returned no service-account object")
		}
		secretID := account.GetStatus().GetClientSecret().GetId()
		if secretID == "" {
			return nil, "", "", c.olderServiceError("service account has no status.client_secret reference")
		}
		clientID, secret, err := c.readCredential(waitCtx, account)
		if err != nil {
			if waitCtx.Err() == context.DeadlineExceeded {
				return nil, "", "", timeoutError()
			}
			if status.Code(err) != codes.NotFound {
				return nil, "", "", fmt.Errorf("failed while waiting for service-account secret rotation: %w", err)
			}
		} else if secretID != previousSecretID || secret != previousSecret {
			return account, clientID, secret, nil
		}
		select {
		case <-waitCtx.Done():
			if waitCtx.Err() == context.DeadlineExceeded {
				return nil, "", "", timeoutError()
			}
			return nil, "", "", waitCtx.Err()
		case <-ticker.C:
		}
	}
}

func (c *runnerContext) readCredential(ctx context.Context, account *publicv1.ServiceAccount) (string, string, error) {
	secretID := account.GetStatus().GetClientSecret().GetId()
	if secretID == "" {
		return "", "", c.olderServiceError("service account has no status.client_secret reference")
	}
	response, err := c.secrets.Get(ctx, publicv1.SecretsGetRequest_builder{Id: secretID}.Build())
	if err != nil {
		if status.Code(err) == codes.PermissionDenied {
			return "", "", fmt.Errorf("could not read service-account credential Secret %q: caller is not authorized for Secrets/Get", secretID)
		}
		return "", "", c.apiError("failed to read service-account credential Secret", err)
	}
	secretObject := response.GetObject()
	if secretObject == nil {
		return "", "", c.olderServiceError("Secrets/Get returned no object")
	}
	data := secretObject.GetData()
	clientID := data["client_id"]
	if len(clientID) == 0 {
		return "", "", c.olderServiceError("referenced Secret has no client_id value")
	}
	secret := data["client_secret"]
	if len(secret) == 0 {
		return "", "", c.olderServiceError("referenced Secret has no client_secret value")
	}
	return string(clientID), string(secret), nil
}

func (c *runnerContext) writeCredential(writer io.Writer, account *publicv1.ServiceAccount, clientID, secret string) error {
	accountJSON, err := (protojson.MarshalOptions{UseProtoNames: true}).Marshal(account)
	if err != nil {
		return fmt.Errorf("failed to serialize service account: %w", err)
	}
	return c.writeJSON(writer, struct {
		ServiceAccount json.RawMessage `json:"service_account"`
		ClientID       string          `json:"client_id"`
		ClientSecret   string          `json:"client_secret"`
	}{
		ServiceAccount: accountJSON,
		ClientID:       clientID,
		ClientSecret:   secret,
	})
}

func (c *runnerContext) writeJSON(writer io.Writer, value any) error {
	data, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to serialize JSON output: %w", err)
	}
	if _, err := fmt.Fprintln(writer, string(data)); err != nil {
		return fmt.Errorf("failed to write JSON output: %w", err)
	}
	return nil
}

func (c *runnerContext) apiError(operation string, err error) error {
	if status.Code(err) == codes.Unimplemented {
		return c.olderServiceError(operation + " is not implemented")
	}
	return fmt.Errorf("%s: %w", operation, err)
}

func (c *runnerContext) olderServiceError(detail string) error {
	return fmt.Errorf("%s; upgrade the Fulfillment service to a release with the async service-account API (OSAC-6555)", detail)
}

const shortHelp = `Regenerate resource credentials`

const longHelp = `
Regenerate credentials for supported resource types. Service-account client secrets can be rotated with:

{{ bt 3 }}shell
{{ binary }} regenerate serviceaccount ID|NAME
{{ bt 3 }}

The command waits for rotation to complete, then emits the replacement client ID and secret once as JSON. Protect the
output as a credential and keep it out of source files, shell history, and logs.
`

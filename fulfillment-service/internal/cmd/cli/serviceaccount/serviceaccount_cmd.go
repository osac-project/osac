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

// Package serviceaccount implements non-interactive service-account CLI commands.
package serviceaccount

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"time"

	"github.com/spf13/cobra"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protoreflect"
	"google.golang.org/protobuf/types/known/anypb"
	"google.golang.org/protobuf/types/known/fieldmaskpb"
	"gopkg.in/yaml.v3"

	"github.com/osac-project/osac/fulfillment-service/internal/cmd/cli/lookup"
	"github.com/osac-project/osac/fulfillment-service/internal/config"
	publicv1 "github.com/osac-project/osac/proto/gen/osac/public/v1"
)

const (
	listPageSize                        = 100
	credentialWaitTimeout time.Duration = 5 * time.Minute
	credentialPollPeriod  time.Duration = time.Second
)

type serviceAccountsClient interface {
	List(context.Context, *publicv1.ServiceAccountsListRequest, ...grpc.CallOption) (*publicv1.ServiceAccountsListResponse, error)
	Get(context.Context, *publicv1.ServiceAccountsGetRequest, ...grpc.CallOption) (*publicv1.ServiceAccountsGetResponse, error)
	Create(context.Context, *publicv1.ServiceAccountsCreateRequest, ...grpc.CallOption) (*publicv1.ServiceAccountsCreateResponse, error)
	Update(context.Context, *publicv1.ServiceAccountsUpdateRequest, ...grpc.CallOption) (*publicv1.ServiceAccountsUpdateResponse, error)
	Delete(context.Context, *publicv1.ServiceAccountsDeleteRequest, ...grpc.CallOption) (*publicv1.ServiceAccountsDeleteResponse, error)
	RegenerateSecret(context.Context, *publicv1.ServiceAccountsRegenerateSecretRequest, ...grpc.CallOption) (*publicv1.ServiceAccountsRegenerateSecretResponse, error)
}

type secretsClient interface {
	Get(context.Context, *publicv1.SecretsGetRequest, ...grpc.CallOption) (*publicv1.SecretsGetResponse, error)
}

type clients struct {
	accounts serviceAccountsClient
	secrets  secretsClient
}

func Cmd() *cobra.Command {
	result := &cobra.Command{
		Use:     "serviceaccount",
		Aliases: []string{"serviceaccounts", "service-account", "service-accounts"},
		Short:   "Manage tenant service accounts",
		Long:    longHelp,
		Args:    cobra.NoArgs,
	}
	result.AddCommand(createCmd(), getCmd(), listCmd(), updateCmd(), deleteCmd(), regenerateSecretCmd())
	return result
}

func createCmd() *cobra.Command {
	var filename string
	cmd := &cobra.Command{
		Use:   "create -f FILE",
		Short: "Create a service account from a JSON or YAML file",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			input, err := openInput(filename)
			if err != nil {
				return err
			}
			defer input.Close()
			account, _, err := decodeAccount(input)
			if err != nil {
				return err
			}
			clearCreateOnlyFields(account)
			return withClients(cmd, func(ctx context.Context, api clients) error {
				response, err := api.accounts.Create(ctx, publicv1.ServiceAccountsCreateRequest_builder{Object: account}.Build())
				if err != nil {
					return apiError("failed to create service account", err)
				}
				created := response.GetObject()
				if created == nil || created.GetId() == "" {
					return olderServiceError("Create did not return a service-account ID")
				}
				ready, err := waitForProvisioning(ctx, api.accounts, created.GetId())
				if err != nil {
					return err
				}
				clientID, secret, err := readCredential(ctx, api.secrets, ready)
				if err != nil {
					return err
				}
				return writeCredential(cmd.OutOrStdout(), ready, clientID, secret)
			})
		},
	}
	cmd.Flags().StringVarP(&filename, "filename", "f", "", "Read the service-account object from FILE, or - for stdin")
	_ = cmd.MarkFlagRequired("filename")
	return cmd
}

func getCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "get ID|NAME",
		Short: "Get a service account as JSON",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return withClients(cmd, func(ctx context.Context, api clients) error {
				account, err := findAccount(ctx, api.accounts, args[0])
				if err != nil {
					return err
				}
				response, err := api.accounts.Get(ctx, publicv1.ServiceAccountsGetRequest_builder{Id: account.GetId()}.Build())
				if err != nil {
					return apiError("failed to get service account", err)
				}
				return writeAccount(cmd.OutOrStdout(), response.GetObject())
			})
		},
	}
}

func listCmd() *cobra.Command {
	var filter string
	cmd := &cobra.Command{
		Use:   "list",
		Short: "List service accounts as JSON",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return withClients(cmd, func(ctx context.Context, api clients) error {
				accounts, err := listAccounts(ctx, api.accounts, filter)
				if err != nil {
					return apiError("failed to list service accounts", err)
				}
				return writeAccounts(cmd.OutOrStdout(), accounts)
			})
		},
	}
	cmd.Flags().StringVar(&filter, "filter", "", "Filter service accounts with a CEL expression")
	return cmd
}

func updateCmd() *cobra.Command {
	var filename string
	cmd := &cobra.Command{
		Use:   "update -f FILE",
		Short: "Update service-account metadata from a JSON or YAML file",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			input, err := openInput(filename)
			if err != nil {
				return err
			}
			defer input.Close()
			patch, raw, err := decodeAccount(input)
			if err != nil {
				return err
			}
			ref := patch.GetId()
			if ref == "" {
				ref = patch.GetMetadata().GetName()
			}
			if ref == "" {
				return fmt.Errorf("update file must identify the service account with id or metadata.name")
			}
			return withClients(cmd, func(ctx context.Context, api clients) error {
				matched, err := findAccount(ctx, api.accounts, ref)
				if err != nil {
					return err
				}
				currentResponse, err := api.accounts.Get(ctx, publicv1.ServiceAccountsGetRequest_builder{Id: matched.GetId()}.Build())
				if err != nil {
					return apiError("failed to get service account before update", err)
				}
				current := currentResponse.GetObject()
				if patch.GetMetadata().GetName() != "" && patch.GetMetadata().GetName() != current.GetMetadata().GetName() {
					return fmt.Errorf("service-account metadata.name is immutable")
				}
				object, paths, err := applyPatch(current, patch, raw)
				if err != nil {
					return err
				}
				response, err := api.accounts.Update(ctx, publicv1.ServiceAccountsUpdateRequest_builder{
					Object:     object,
					UpdateMask: &fieldmaskpb.FieldMask{Paths: paths},
					Lock:       true,
				}.Build())
				if err != nil {
					return apiError("failed to update service account", err)
				}
				return writeAccount(cmd.OutOrStdout(), response.GetObject())
			})
		},
	}
	cmd.Flags().StringVarP(&filename, "filename", "f", "", "Read the service-account update from FILE, or - for stdin")
	_ = cmd.MarkFlagRequired("filename")
	return cmd
}

func deleteCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "delete ID|NAME",
		Short: "Request deletion of a service account",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return withClients(cmd, func(ctx context.Context, api clients) error {
				account, err := findAccount(ctx, api.accounts, args[0])
				if err != nil {
					return err
				}
				if _, err = api.accounts.Delete(ctx, publicv1.ServiceAccountsDeleteRequest_builder{Id: account.GetId()}.Build()); err != nil {
					return apiError("failed to request service-account deletion", err)
				}
				return writeJSON(cmd.OutOrStdout(), map[string]any{
					"id": account.GetId(), "deletion_requested": true,
				})
			})
		},
	}
}

func regenerateSecretCmd() *cobra.Command {
	return &cobra.Command{
		Use:     "regenerate-secret ID|NAME",
		Aliases: []string{"rotate-secret"},
		Short:   "Regenerate a service-account credential and print it as JSON once",
		Args:    cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return withClients(cmd, func(ctx context.Context, api clients) error {
				account, err := findAccount(ctx, api.accounts, args[0])
				if err != nil {
					return err
				}
				if _, err = api.accounts.RegenerateSecret(ctx, publicv1.ServiceAccountsRegenerateSecretRequest_builder{Id: account.GetId()}.Build()); err != nil {
					return apiError("failed to request service-account secret rotation", err)
				}
				rotated, err := waitForRotation(ctx, api.accounts, account.GetId())
				if err != nil {
					return err
				}
				clientID, secret, err := readCredential(ctx, api.secrets, rotated)
				if err != nil {
					return err
				}
				return writeCredential(cmd.OutOrStdout(), rotated, clientID, secret)
			})
		},
	}
}

func withClients(cmd *cobra.Command, run func(context.Context, clients) error) (err error) {
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
	return run(ctx, clients{
		accounts: publicv1.NewServiceAccountsClient(conn),
		secrets:  publicv1.NewSecretsClient(conn),
	})
}

func openInput(filename string) (io.ReadCloser, error) {
	if filename == "-" {
		return io.NopCloser(os.Stdin), nil
	}
	file, err := os.Open(filename) // #nosec G304 -- the CLI intentionally opens the user-selected input file.
	if err != nil {
		return nil, fmt.Errorf("failed to open file %q: %w", filename, err)
	}
	return file, nil
}

// decodeAccount accepts a direct ServiceAccount JSON/YAML object or the Any
// wrapper used by the generic `osac create -f` command.
func decodeAccount(input io.Reader) (*publicv1.ServiceAccount, map[string]json.RawMessage, error) {
	decoder := yaml.NewDecoder(input)
	var value any
	if err := decoder.Decode(&value); err != nil {
		return nil, nil, fmt.Errorf("failed to decode service-account file: %w", err)
	}
	var extra any
	if err := decoder.Decode(&extra); err != nil && !errors.Is(err, io.EOF) {
		return nil, nil, fmt.Errorf("failed to decode service-account file: %w", err)
	} else if err == nil {
		return nil, nil, fmt.Errorf("service-account file must contain exactly one object")
	}
	data, err := json.Marshal(value)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to convert service-account file to JSON: %w", err)
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(data, &fields); err != nil || fields == nil {
		return nil, nil, fmt.Errorf("service-account file must contain a JSON or YAML object")
	}
	account := &publicv1.ServiceAccount{}
	if _, ok := fields["@type"]; ok {
		wrapper := &anypb.Any{}
		if err := protojson.Unmarshal(data, wrapper); err != nil {
			return nil, nil, fmt.Errorf("failed to decode service-account Any: %w", err)
		}
		message, err := wrapper.UnmarshalNew()
		if err != nil {
			return nil, nil, fmt.Errorf("failed to decode service-account Any: %w", err)
		}
		var ok bool
		account, ok = message.(*publicv1.ServiceAccount)
		if !ok {
			return nil, nil, fmt.Errorf("input file contains %q, expected a public ServiceAccount", proto.MessageName(message))
		}
	} else if err := protojson.Unmarshal(data, account); err != nil {
		return nil, nil, fmt.Errorf("failed to decode ServiceAccount: %w", err)
	}
	return account, fields, nil
}

func clearCreateOnlyFields(account *publicv1.ServiceAccount) {
	account.SetId("")
	account.ProtoReflect().Clear(account.ProtoReflect().Descriptor().Fields().ByName("status"))
	metadata := account.GetMetadata()
	if metadata == nil {
		metadata = &publicv1.Metadata{}
		account.SetMetadata(metadata)
	}
	for _, field := range []string{"creation_timestamp", "deletion_timestamp", "creator", "tenant", "version", "finalizers"} {
		fieldDescriptor := metadata.ProtoReflect().Descriptor().Fields().ByName(protoreflect.Name(field))
		if fieldDescriptor != nil {
			metadata.ProtoReflect().Clear(fieldDescriptor)
		}
	}
}

func listAccounts(ctx context.Context, client serviceAccountsClient, filter string) ([]*publicv1.ServiceAccount, error) {
	var result []*publicv1.ServiceAccount
	for offset := int32(0); ; offset += listPageSize {
		request := publicv1.ServiceAccountsListRequest_builder{
			Offset: proto.Int32(offset),
			Limit:  proto.Int32(listPageSize),
		}.Build()
		if filter != "" {
			request.SetFilter(filter)
		}
		response, err := client.List(ctx, request)
		if err != nil {
			return nil, err
		}
		result = append(result, response.GetItems()...)
		if len(response.GetItems()) < listPageSize {
			return result, nil
		}
	}
}

func findAccount(ctx context.Context, client serviceAccountsClient, ref string) (*publicv1.ServiceAccount, error) {
	return lookup.Find(ref, "service account", func(filter string, limit int32) ([]*publicv1.ServiceAccount, error) {
		response, err := client.List(ctx, publicv1.ServiceAccountsListRequest_builder{
			Filter: proto.String(filter), Limit: proto.Int32(limit),
		}.Build())
		if err != nil {
			return nil, apiError("failed to find service account", err)
		}
		return response.GetItems(), nil
	})
}

func applyPatch(current, patch *publicv1.ServiceAccount, raw map[string]json.RawMessage) (*publicv1.ServiceAccount, []string, error) {
	paths := make([]string, 0, 4)
	metadataRaw := nestedRaw(raw, "metadata")
	metadataPatch := &publicv1.Metadata{}
	var metadataFields map[string]json.RawMessage
	if len(metadataRaw) > 0 {
		if err := protojson.Unmarshal(metadataRaw, metadataPatch); err != nil {
			return nil, nil, fmt.Errorf("failed to decode service-account metadata update: %w", err)
		}
		if err := json.Unmarshal(metadataRaw, &metadataFields); err != nil {
			return nil, nil, fmt.Errorf("failed to inspect service-account metadata update: %w", err)
		}
	}
	if hasRawField(metadataFields, "display_name", "displayName") {
		paths = append(paths, "metadata.display_name")
	}
	if hasRawField(metadataFields, "description") {
		paths = append(paths, "metadata.description")
	}
	if hasRawField(metadataFields, "labels") {
		paths = append(paths, "metadata.labels")
	}
	if len(paths) == 0 {
		return nil, nil, fmt.Errorf("update file must set at least one of metadata.display_name, metadata.description, or metadata.labels")
	}

	object := &publicv1.ServiceAccount{}
	object.SetId(current.GetId())
	metadata := &publicv1.Metadata{}
	metadata.SetVersion(current.GetMetadata().GetVersion())
	for _, path := range paths {
		switch path {
		case "metadata.display_name":
			metadata.SetDisplayName(metadataPatch.GetDisplayName())
		case "metadata.description":
			metadata.SetDescription(metadataPatch.GetDescription())
		case "metadata.labels":
			labels := metadataPatch.GetLabels()
			if labels == nil {
				labels = map[string]string{}
			}
			metadata.SetLabels(labels)
		}
	}
	object.SetMetadata(metadata)
	return object, paths, nil
}

func nestedRaw(fields map[string]json.RawMessage, name string) json.RawMessage {
	return fields[name]
}

func hasRawField(fields map[string]json.RawMessage, names ...string) bool {
	for _, name := range names {
		if _, ok := fields[name]; ok {
			return true
		}
	}
	return false
}

func waitForProvisioning(ctx context.Context, client serviceAccountsClient, id string) (*publicv1.ServiceAccount, error) {
	waitCtx, cancel := context.WithTimeout(ctx, credentialWaitTimeout)
	defer cancel()
	ticker := time.NewTicker(credentialPollPeriod)
	defer ticker.Stop()
	for {
		response, err := client.Get(waitCtx, publicv1.ServiceAccountsGetRequest_builder{Id: id}.Build())
		if err != nil {
			return nil, apiError("failed while waiting for service-account provisioning", err)
		}
		account := response.GetObject()
		if account == nil {
			return nil, olderServiceError("Get returned no service-account object")
		}
		if account.GetStatus().GetState() == publicv1.ServiceAccountState_SERVICE_ACCOUNT_STATE_FAILED {
			message := account.GetStatus().GetMessage()
			if message == "" {
				message = "the controller reported a failed provisioning state"
			}
			return nil, fmt.Errorf("service-account provisioning failed: %s", message)
		}
		if account.GetStatus().GetClientSecret().GetId() != "" {
			return account, nil
		}
		select {
		case <-waitCtx.Done():
			return nil, olderServiceError("timed out waiting for status.client_secret; upgrade the Fulfillment service to support async service-account credentials")
		case <-ticker.C:
		}
	}
}

func waitForRotation(ctx context.Context, client serviceAccountsClient, id string) (*publicv1.ServiceAccount, error) {
	waitCtx, cancel := context.WithTimeout(ctx, credentialWaitTimeout)
	defer cancel()
	ticker := time.NewTicker(credentialPollPeriod)
	defer ticker.Stop()
	var lastRotationError string
	for {
		response, err := client.Get(waitCtx, publicv1.ServiceAccountsGetRequest_builder{Id: id}.Build())
		if err != nil {
			return nil, apiError("failed while waiting for service-account secret rotation", err)
		}
		account := response.GetObject()
		if account == nil {
			return nil, olderServiceError("Get returned no service-account object")
		}
		status := account.GetStatus()
		if status.GetSecretRotationError() != "" {
			lastRotationError = status.GetSecretRotationError()
		}
		if status.GetSecretRotationState() == publicv1.ServiceAccountSecretRotationState_SERVICE_ACCOUNT_SECRET_ROTATION_STATE_COMPLETED &&
			status.GetClientSecret().GetId() != "" {
			return account, nil
		}
		select {
		case <-waitCtx.Done():
			if lastRotationError != "" {
				return nil, fmt.Errorf("timed out waiting for service-account secret rotation: %s", lastRotationError)
			}
			return nil, olderServiceError("timed out waiting for status.secret_rotation_state to complete; upgrade the Fulfillment service to support async service-account credential rotation")
		case <-ticker.C:
		}
	}
}

func readCredential(ctx context.Context, client secretsClient, account *publicv1.ServiceAccount) (string, string, error) {
	secretID := account.GetStatus().GetClientSecret().GetId()
	if secretID == "" {
		return "", "", olderServiceError("service account has no status.client_secret reference")
	}
	response, err := client.Get(ctx, publicv1.SecretsGetRequest_builder{Id: secretID}.Build())
	if err != nil {
		if status.Code(err) == codes.PermissionDenied {
			return "", "", fmt.Errorf("could not read service-account credential Secret %q: caller is not authorized for Secrets/Get", secretID)
		}
		return "", "", apiError("failed to read service-account credential Secret", err)
	}
	secretObject := response.GetObject()
	if secretObject == nil {
		return "", "", olderServiceError("Secrets/Get returned no object")
	}
	data := secretObject.GetData()
	clientID := data["client_id"]
	if len(clientID) == 0 {
		return "", "", olderServiceError("referenced Secret has no client_id value")
	}
	secret := data["client_secret"]
	if len(secret) == 0 {
		return "", "", olderServiceError("referenced Secret has no client_secret value")
	}
	return string(clientID), string(secret), nil
}

func writeAccount(writer io.Writer, account *publicv1.ServiceAccount) error {
	if account == nil {
		return olderServiceError("server returned no service-account object")
	}
	data, err := (protojson.MarshalOptions{UseProtoNames: true}).Marshal(account)
	if err != nil {
		return fmt.Errorf("failed to serialize service account: %w", err)
	}
	return writeRawJSON(writer, data)
}

func writeAccounts(writer io.Writer, accounts []*publicv1.ServiceAccount) error {
	values := make([]json.RawMessage, 0, len(accounts))
	for _, account := range accounts {
		data, err := (protojson.MarshalOptions{UseProtoNames: true}).Marshal(account)
		if err != nil {
			return fmt.Errorf("failed to serialize service account: %w", err)
		}
		values = append(values, data)
	}
	return writeJSON(writer, values)
}

func writeCredential(writer io.Writer, account *publicv1.ServiceAccount, clientID, secret string) error {
	accountJSON, err := (protojson.MarshalOptions{UseProtoNames: true}).Marshal(account)
	if err != nil {
		return fmt.Errorf("failed to serialize service account: %w", err)
	}
	return writeJSON(writer, struct {
		ServiceAccount json.RawMessage `json:"service_account"`
		ClientID       string          `json:"client_id"`
		ClientSecret   string          `json:"client_secret"`
	}{
		ServiceAccount: accountJSON,
		ClientID:       clientID,
		ClientSecret:   secret,
	})
}

func writeJSON(writer io.Writer, value any) error {
	data, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to serialize JSON output: %w", err)
	}
	if _, err := fmt.Fprintln(writer, string(data)); err != nil {
		return fmt.Errorf("failed to write JSON output: %w", err)
	}
	return nil
}

func writeRawJSON(writer io.Writer, data []byte) error {
	var value any
	if err := json.Unmarshal(data, &value); err != nil {
		return fmt.Errorf("failed to serialize JSON output: %w", err)
	}
	return writeJSON(writer, value)
}

func apiError(operation string, err error) error {
	if status.Code(err) == codes.Unimplemented {
		return olderServiceError(operation + " is not implemented")
	}
	return fmt.Errorf("%s: %w", operation, err)
}

func olderServiceError(detail string) error {
	return fmt.Errorf("%s; upgrade the Fulfillment service to a release with the async service-account API (OSAC-6555)", detail)
}

const longHelp = `
Manage tenant service accounts through the public Fulfillment API. All command output is JSON.

Create and secret rotation wait for the asynchronous controller operation to finish, then read the
credential through the tenant-scoped Secret reference with Secrets/Get. Their output contains the
new client secret once; protect it as a credential. Get, list, and update return only the Secret
reference, never the credential bytes.

Service-account access is granted by an active RoleBinding in the account's tenant whose
spec.service_accounts includes the account. Use the generic ` + "`osac create -f`" + ` command to create
a RoleBinding and ` + "`osac delete rolebindings`" + ` to revoke one.

Ordinary service-account users cannot read raw Secrets or watch managed Events. Their access is
limited by the role assigned through RoleBinding and by their tenant.

Example input file:

` + "```yaml" + `
"@type": type.googleapis.com/osac.public.v1.ServiceAccount
metadata:
  name: batch-worker
  display_name: Batch worker
  description: Runs tenant batch jobs
` + "```" + `

Create, get/list, update from a file, delete, and rotate credentials:

` + "```shell" + `
osac serviceaccount create -f service-account.yaml
osac serviceaccount get batch-worker
osac serviceaccount list --filter "this.metadata.labels['team'] == 'data'"
osac serviceaccount update -f service-account-update.yaml
osac serviceaccount delete batch-worker
osac serviceaccount regenerate-secret batch-worker
` + "```" + `

The ServiceAccountSpec has no role grants. The update file identifies the account with its id or
metadata.name and may set metadata.display_name, metadata.description, and metadata.labels. Manage
access by creating or deleting the corresponding RoleBinding. The ` + "`client_secret`" + ` value from create or
regenerate-secret is emitted on standard output only once. Do not redirect it to a source file or log.
`

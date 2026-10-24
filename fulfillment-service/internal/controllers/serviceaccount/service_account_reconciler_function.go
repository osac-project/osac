/*
Copyright (c) 2026 Red Hat Inc.

Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except in compliance with the
License. You may obtain a copy of the License at

  http://www.apache.org/licenses/LICENSE-2.0
*/

package serviceaccount

import (
	"context"
	"errors"
	"log/slog"
	"slices"
	"time"

	"github.com/google/uuid"
	"google.golang.org/grpc"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/fieldmaskpb"

	"github.com/osac-project/osac/fulfillment-service/internal/controllers"
	"github.com/osac-project/osac/fulfillment-service/internal/controllers/finalizers"
	"github.com/osac-project/osac/fulfillment-service/internal/idp"
	"github.com/osac-project/osac/fulfillment-service/internal/serviceaccounts"
	"github.com/osac-project/osac/fulfillment-service/internal/vault"
	privatev1 "github.com/osac-project/osac/proto/gen/osac/private/v1"
)

// FunctionBuilder configures service-account cleanup.
const serviceAccountCleanupTimeout = 30 * time.Second

type FunctionBuilder struct {
	logger      *slog.Logger
	connection  *grpc.ClientConn
	idpClient   idp.ClientInterface
	secretStore vault.SecretStore
}

func NewFunction() *FunctionBuilder                                      { return &FunctionBuilder{} }
func (b *FunctionBuilder) SetLogger(value *slog.Logger) *FunctionBuilder { b.logger = value; return b }
func (b *FunctionBuilder) SetConnection(value *grpc.ClientConn) *FunctionBuilder {
	b.connection = value
	return b
}
func (b *FunctionBuilder) SetIdpClient(value idp.ClientInterface) *FunctionBuilder {
	b.idpClient = value
	return b
}
func (b *FunctionBuilder) SetSecretStore(value vault.SecretStore) *FunctionBuilder {
	b.secretStore = value
	return b
}
func (b *FunctionBuilder) Build() (*function, error) {
	if b.logger == nil {
		return nil, errors.New("logger is mandatory")
	}
	if b.connection == nil {
		return nil, errors.New("connection is mandatory")
	}
	if b.idpClient == nil {
		return nil, errors.New("IDP client is mandatory")
	}
	if b.secretStore == nil {
		return nil, errors.New("secret store is mandatory")
	}
	return &function{logger: b.logger, client: privatev1.NewServiceAccountsClient(b.connection), idpClient: b.idpClient, secretStore: b.secretStore}, nil
}

type function struct {
	logger      *slog.Logger
	client      privatev1.ServiceAccountsClient
	idpClient   idp.ClientInterface
	secretStore vault.SecretStore
}

func (r *function) Run(ctx context.Context, account *privatev1.ServiceAccount) error {
	metadata := account.GetMetadata()
	if !metadata.HasDeletionTimestamp() || !slices.Contains(metadata.GetFinalizers(), finalizers.Controller) {
		return nil
	}
	ctx, cancel := context.WithTimeout(ctx, serviceAccountCleanupTimeout)
	defer cancel()
	if _, err := uuid.Parse(account.GetId()); err != nil {
		return errors.New("invalid service-account identity")
	}
	if metadata.GetTenant() == "" || account.GetStatus().GetClientId() != serviceaccounts.ClientID(metadata.GetTenant(), account.GetId()) {
		return errors.New("inconsistent service-account client identity")
	}
	clientUUID := account.GetStatus().GetKeycloakClientUuid()
	if _, err := uuid.Parse(clientUUID); err != nil {
		return errors.New("invalid service-account client UUID")
	}
	credentials, err := r.secretStore.Fetch(ctx, metadata.GetTenant(), "", serviceaccounts.RegistrationSecretName(account.GetId()))
	secretMissing := vault.IsSecretNotFound(err)
	if err != nil && !secretMissing {
		return err
	}
	registrationToken := ""
	if !secretMissing {
		registrationToken = string(credentials[serviceaccounts.RegistrationAccessTokenKey])
		if registrationToken == "" {
			return errors.New("service-account management credential is missing")
		}
	}
	if err := r.idpClient.DeleteServiceAccountClient(ctx, account.GetStatus().GetClientId(), clientUUID, registrationToken); err != nil {
		// A permanent cleanup error intentionally leaves the account terminating.
		// Retaining the finalizer prevents an orphaned Keycloak client; retries back off.
		return controllers.RequeueAfter(err, time.Second)
	}
	if !secretMissing {
		if err := r.secretStore.Delete(ctx, metadata.GetTenant(), "", serviceaccounts.RegistrationSecretName(account.GetId())); err != nil &&
			!vault.IsSecretNotFound(err) {
			r.logger.ErrorContext(
				ctx,
				"Failed to delete service-account management credential from Vault",
				slog.String("account_id", account.GetId()),
				slog.Any("error", err),
			)
			return controllers.RequeueAfter(err, time.Second)
		}
	}
	updated := proto.Clone(account).(*privatev1.ServiceAccount)
	updated.GetMetadata().SetFinalizers(slices.DeleteFunc(updated.GetMetadata().GetFinalizers(), func(value string) bool { return value == finalizers.Controller }))
	_, err = r.client.Update(ctx, privatev1.ServiceAccountsUpdateRequest_builder{
		Object: updated, Lock: true, UpdateMask: &fieldmaskpb.FieldMask{Paths: []string{"metadata.finalizers"}},
	}.Build())
	if err != nil {
		return err
	}
	return nil
}

//
// Copyright (c) 2026 Red Hat, Inc.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//   http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.
//

package serviceaccount

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"log/slog"
	"slices"
	"strconv"
	"time"

	"github.com/google/uuid"
	"google.golang.org/grpc"
	grpccodes "google.golang.org/grpc/codes"
	grpcstatus "google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/fieldmaskpb"

	"github.com/osac-project/osac/fulfillment-service/internal/auth"
	"github.com/osac-project/osac/fulfillment-service/internal/controllers"
	"github.com/osac-project/osac/fulfillment-service/internal/controllers/finalizers"
	"github.com/osac-project/osac/fulfillment-service/internal/idp"
	"github.com/osac-project/osac/fulfillment-service/internal/serviceaccounts"
	privatev1 "github.com/osac-project/osac/proto/gen/osac/private/v1"
)

const (
	serviceAccountReconcileTimeout    = 30 * time.Second
	serviceAccountCleanupTimeout      = serviceAccountReconcileTimeout
	serviceAccountCompensationTimeout = 10 * time.Second
	serviceAccountRetryDelay          = time.Second
	serviceAccountRecoveryRetryDelay  = 30 * time.Second
	clientIDDataKey                   = "client_id"
	clientSecretDataKey               = "client_secret"
	tenantAnnotation                  = "osac.openshift.io/tenant"
	ownerReferenceAnnotation          = "osac.openshift.io/owner-reference"
	serviceAccountOrphanClientError   = "A Keycloak client exists without recoverable registration credentials. Remove it from Keycloak to let reconciliation continue."
)

type FunctionBuilder struct {
	logger     *slog.Logger
	connection *grpc.ClientConn
	idpClient  idp.ClientInterface
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
	secretsClient := privatev1.NewSecretsClient(b.connection)
	return &function{
		logger:        b.logger,
		client:        privatev1.NewServiceAccountsClient(b.connection),
		secretsClient: secretsClient,
		idpClient:     b.idpClient,
	}, nil
}

type function struct {
	logger        *slog.Logger
	client        privatev1.ServiceAccountsClient
	secretsClient privatev1.SecretsClient
	idpClient     idp.ClientInterface
}

func (r *function) Run(ctx context.Context, account *privatev1.ServiceAccount) error {
	metadata := account.GetMetadata()
	if !slices.Contains(metadata.GetFinalizers(), finalizers.Controller) {
		return nil
	}
	ctx, cancel := context.WithTimeout(ctx, serviceAccountReconcileTimeout)
	defer cancel()
	if metadata.HasDeletionTimestamp() {
		return r.delete(ctx, account)
	}
	return r.provision(ctx, account)
}

func (r *function) provision(ctx context.Context, account *privatev1.ServiceAccount) error {
	accountID := account.GetId()
	if _, err := uuid.Parse(accountID); err != nil {
		return errors.New("invalid service-account identity")
	}
	tenant := account.GetMetadata().GetTenant()
	if tenant == "" {
		return errors.New("service-account tenant is missing")
	}
	clientID := serviceaccounts.ClientID(tenant, accountID)
	if err := r.ensurePendingState(ctx, account, clientID, tenant); err != nil {
		return err
	}
	status := account.GetStatus()
	if status.GetKeycloakClientUuid() != "" && status.GetClientSecret() != nil {
		return r.finishExistingProvisioning(ctx, account)
	}
	if status.GetKeycloakClientUuid() != "" || status.GetClientSecret() != nil {
		return errors.New("inconsistent service-account provisioning status")
	}
	credentials, err := r.fetchOrCreateRegistrationCredentials(ctx, clientID, account)
	if err != nil {
		return err
	}
	return r.publishProvisionedCredentials(ctx, account, tenant, clientID, credentials)
}

func (r *function) ensurePendingState(ctx context.Context, account *privatev1.ServiceAccount,
	clientID, tenant string) error {
	status := account.GetStatus()
	if status.GetState() == privatev1.ServiceAccountState_SERVICE_ACCOUNT_STATE_UNSPECIFIED {
		if err := r.updateState(ctx, account, privatev1.ServiceAccountState_SERVICE_ACCOUNT_STATE_PENDING, ""); err != nil {
			return controllers.RequeueAfter(err, serviceAccountRetryDelay)
		}
		status = account.GetStatus()
	}
	if status.GetState() == privatev1.ServiceAccountState_SERVICE_ACCOUNT_STATE_FAILED && status.GetMessage() != "" {
		_, found, err := r.idpClient.FindServiceAccountClient(ctx, clientID, tenant)
		if err != nil {
			return controllers.RequeueAfter(err, serviceAccountRetryDelay)
		}
		if found {
			return controllers.RequeueAfter(idp.ErrServiceAccountClientAlreadyExists, serviceAccountRecoveryRetryDelay)
		}
		if err = r.updateState(ctx, account, privatev1.ServiceAccountState_SERVICE_ACCOUNT_STATE_PENDING, ""); err != nil {
			return controllers.RequeueAfter(err, serviceAccountRetryDelay)
		}
	}
	return nil
}

func (r *function) finishExistingProvisioning(ctx context.Context, account *privatev1.ServiceAccount) error {
	status := account.GetStatus()
	if _, err := uuid.Parse(status.GetKeycloakClientUuid()); err != nil {
		return errors.New("invalid service-account client UUID")
	}
	if status.GetClientSecret() == nil {
		return errors.New("service-account credential Secret reference is missing")
	}
	if status.GetState() != privatev1.ServiceAccountState_SERVICE_ACCOUNT_STATE_READY {
		if err := r.updateState(ctx, account, privatev1.ServiceAccountState_SERVICE_ACCOUNT_STATE_READY, ""); err != nil {
			return controllers.RequeueAfter(err, serviceAccountRetryDelay)
		}
	}
	return r.removePendingClientSecret(ctx, account)
}

func (r *function) fetchOrCreateRegistrationCredentials(ctx context.Context, clientID string,
	account *privatev1.ServiceAccount) (map[string][]byte, error) {
	credentials, err := r.fetchRegistrationCredentials(ctx, account)
	if err == nil {
		return credentials, nil
	}
	if grpcstatus.Code(err) != grpccodes.NotFound {
		return nil, controllers.RequeueAfter(err, serviceAccountRetryDelay)
	}
	credentials, err = r.createClient(ctx, clientID, account)
	if err == nil {
		return credentials, nil
	}
	if errors.Is(err, idp.ErrServiceAccountClientAlreadyExists) {
		if updateErr := r.updateState(ctx, account, privatev1.ServiceAccountState_SERVICE_ACCOUNT_STATE_FAILED,
			serviceAccountOrphanClientError); updateErr != nil {
			return nil, controllers.RequeueAfter(updateErr, serviceAccountRetryDelay)
		}
		return nil, controllers.RequeueAfter(err, serviceAccountRecoveryRetryDelay)
	}
	return nil, controllers.RequeueAfter(err, serviceAccountRetryDelay)
}

func (r *function) publishProvisionedCredentials(ctx context.Context, account *privatev1.ServiceAccount,
	tenant, clientID string, credentials map[string][]byte) error {
	registrationToken := string(credentials[serviceaccounts.RegistrationAccessTokenKey])
	clientUUID := string(credentials[serviceaccounts.RegistrationClientUUIDKey])
	clientSecret := string(credentials[serviceaccounts.PendingClientSecretKey])
	if registrationToken == "" || clientUUID == "" {
		return errors.New("service-account provisioning credential is incomplete")
	}
	if _, err := uuid.Parse(clientUUID); err != nil {
		return errors.New("invalid service-account client UUID")
	}
	if clientSecret == "" {
		return r.recoverIncompleteClient(ctx, clientID, clientUUID, registrationToken, account)
	}
	if statusUUID := account.GetStatus().GetKeycloakClientUuid(); statusUUID != "" && statusUUID != clientUUID {
		return errors.New("service-account client UUID does not match its pending credential")
	}

	secretRef, err := r.createClientSecret(ctx, tenant, account.GetId(), clientID, clientSecret)
	if err != nil {
		return controllers.RequeueAfter(err, serviceAccountRetryDelay)
	}
	credentials[serviceaccounts.PendingClientSecretRefIDKey] = []byte(secretRef.GetId())
	credentials[serviceaccounts.PendingClientSecretRefNameKey] = []byte(secretRef.GetName())
	if err := r.storeRegistrationCredentials(ctx, account, credentials); err != nil {
		return controllers.RequeueAfter(err, serviceAccountRetryDelay)
	}
	return r.markProvisioningReady(ctx, account, clientID, clientUUID, secretRef)
}

func (r *function) markProvisioningReady(ctx context.Context, account *privatev1.ServiceAccount,
	clientID, clientUUID string, secretRef *privatev1.SecretLocalReference) error {
	updated := proto.Clone(account).(*privatev1.ServiceAccount)
	updatedStatus := updated.GetStatus()
	if updatedStatus == nil {
		updatedStatus = &privatev1.ServiceAccountStatus{}
		updated.SetStatus(updatedStatus)
	}
	updatedStatus.SetKeycloakClientUuid(clientUUID)
	updatedStatus.SetClientSecret(secretRef)
	updatedStatus.SetState(privatev1.ServiceAccountState_SERVICE_ACCOUNT_STATE_READY)
	updatedStatus.ClearMessage()
	response, err := r.client.Update(ctx, privatev1.ServiceAccountsUpdateRequest_builder{
		Object: updated,
		Lock:   true,
		UpdateMask: &fieldmaskpb.FieldMask{Paths: []string{
			"status.keycloak_client_uuid", "status.client_secret", "status.state", "status.message",
		}},
	}.Build())
	if err != nil {
		return controllers.RequeueAfter(err, serviceAccountRetryDelay)
	}
	if response != nil && response.GetObject() != nil && response.GetObject().GetMetadata() != nil {
		account.GetMetadata().SetVersion(response.GetObject().GetMetadata().GetVersion())
	}
	account.SetStatus(proto.Clone(updatedStatus).(*privatev1.ServiceAccountStatus))
	return r.removePendingClientSecret(ctx, account)
}

func (r *function) createClient(ctx context.Context, clientID string, account *privatev1.ServiceAccount) (map[string][]byte, error) {
	tenant := account.GetMetadata().GetTenant()
	accountID := account.GetId()
	clientUUID, clientSecret, registrationToken, err := r.idpClient.CreateServiceAccountClient(ctx, clientID, tenant)
	if err != nil {
		if clientUUID != "" && registrationToken != "" {
			r.compensateClient(ctx, account, clientID, clientUUID, registrationToken, false)
		}
		return nil, fmt.Errorf("failed to create service-account client: %w", err)
	}
	if _, err := uuid.Parse(clientUUID); err != nil || clientSecret == "" || registrationToken == "" {
		if clientUUID != "" && registrationToken != "" {
			r.compensateClient(ctx, account, clientID, clientUUID, registrationToken, false)
		}
		return nil, errors.New("invalid service-account client setup result")
	}

	credentials := map[string][]byte{
		serviceaccounts.RegistrationServiceAccountIDKey: []byte(accountID),
		serviceaccounts.RegistrationAccessTokenKey:      []byte(registrationToken),
		serviceaccounts.RegistrationClientUUIDKey:       []byte(clientUUID),
		serviceaccounts.PendingClientSecretKey:          []byte(clientSecret),
	}
	if err := r.storeRegistrationCredentials(ctx, account, credentials); err != nil {
		r.compensateClient(ctx, account, clientID, clientUUID, registrationToken, true)
		return nil, fmt.Errorf("failed to store service-account management credential: %w", err)
	}
	r.logger.InfoContext(ctx, "Created service-account client", slog.String("account_id", accountID), slog.String("client_id", clientID))
	return credentials, nil
}

func (r *function) createClientSecret(ctx context.Context, tenant, accountID, clientID, clientSecret string) (*privatev1.SecretLocalReference, error) {
	name := serviceaccounts.ClientSecretName(accountID)
	created, err := r.secretsClient.Create(ctx, privatev1.SecretsCreateRequest_builder{
		Object: privatev1.Secret_builder{
			Metadata: privatev1.Metadata_builder{
				Name:   name,
				Tenant: tenant,
				Annotations: map[string]string{
					tenantAnnotation:         tenant,
					ownerReferenceAnnotation: accountID,
				},
			}.Build(),
			Type: privatev1.SecretType_SECRET_TYPE_OPAQUE,
			Data: map[string][]byte{
				clientIDDataKey:     []byte(clientID),
				clientSecretDataKey: []byte(clientSecret),
			},
		}.Build(),
	}.Build())
	if err == nil {
		return &privatev1.SecretLocalReference{Id: created.GetObject().GetId(), Name: created.GetObject().GetMetadata().GetName()}, nil
	}
	if grpcstatus.Code(err) != grpccodes.AlreadyExists {
		return nil, fmt.Errorf("failed to create service-account credential Secret: %w", err)
	}

	filter := fmt.Sprintf("this.metadata.name == %s && this.metadata.tenant == %s", strconv.Quote(name), strconv.Quote(tenant))
	listed, err := r.secretsClient.List(ctx, privatev1.SecretsListRequest_builder{Filter: &filter, Limit: new(int32(2))}.Build())
	if err != nil {
		return nil, fmt.Errorf("failed to find existing service-account credential Secret: %w", err)
	}
	items := listed.GetItems()
	if len(items) != 1 {
		return nil, fmt.Errorf("expected one service-account credential Secret named %q, found %d", name, len(items))
	}
	secret := items[0]
	if err := r.verifyClientSecret(ctx, secret, tenant, clientID, clientSecret); err != nil {
		return nil, err
	}
	return &privatev1.SecretLocalReference{Id: secret.GetId(), Name: secret.GetMetadata().GetName()}, nil
}

func (r *function) updateState(ctx context.Context, account *privatev1.ServiceAccount,
	state privatev1.ServiceAccountState, message string) error {
	if account.GetStatus().GetState() == state && account.GetStatus().GetMessage() == message {
		return nil
	}
	updated := proto.Clone(account).(*privatev1.ServiceAccount)
	status := updated.GetStatus()
	if status == nil {
		status = &privatev1.ServiceAccountStatus{}
		updated.SetStatus(status)
	}
	status.SetState(state)
	if message == "" {
		status.ClearMessage()
	} else {
		status.SetMessage(message)
	}
	response, err := r.client.Update(ctx, privatev1.ServiceAccountsUpdateRequest_builder{
		Object: updated,
		Lock:   true,
		UpdateMask: &fieldmaskpb.FieldMask{Paths: []string{
			"status.state", "status.message",
		}},
	}.Build())
	if err != nil {
		return fmt.Errorf("failed to update service-account state: %w", err)
	}
	if response != nil && response.GetObject() != nil && response.GetObject().GetMetadata() != nil {
		account.GetMetadata().SetVersion(response.GetObject().GetMetadata().GetVersion())
	}
	if account.GetStatus() == nil {
		account.SetStatus(&privatev1.ServiceAccountStatus{})
	}
	account.GetStatus().SetState(state)
	if message == "" {
		account.GetStatus().ClearMessage()
	} else {
		account.GetStatus().SetMessage(message)
	}
	return nil
}

func (r *function) verifyClientSecret(ctx context.Context, listed *privatev1.Secret, tenant, clientID, expectedSecret string) error {
	if listed.GetMetadata().GetTenant() != tenant {
		return fmt.Errorf("existing service-account credential Secret %q has a different owner", listed.GetMetadata().GetName())
	}
	response, err := r.secretsClient.Get(ctx, privatev1.SecretsGetRequest_builder{Id: listed.GetId()}.Build())
	if err != nil {
		return fmt.Errorf("failed to verify existing service-account credential Secret: %w", err)
	}
	secret := response.GetObject()
	if secret.GetType() != privatev1.SecretType_SECRET_TYPE_OPAQUE ||
		!bytes.Equal(secret.GetData()[clientIDDataKey], []byte(clientID)) ||
		!bytes.Equal(secret.GetData()[clientSecretDataKey], []byte(expectedSecret)) {
		return fmt.Errorf("existing service-account credential Secret %q has different data", listed.GetMetadata().GetName())
	}
	return nil
}

func (r *function) registrationSecretByName(ctx context.Context, name string) (*privatev1.Secret, error) {
	filter := fmt.Sprintf("this.metadata.name == %s && this.metadata.tenant == %s",
		strconv.Quote(name), strconv.Quote(auth.SystemTenant))
	listed, err := r.secretsClient.List(ctx, privatev1.SecretsListRequest_builder{
		Filter: &filter, Limit: new(int32(2)),
	}.Build())
	if err != nil {
		return nil, err
	}
	items := listed.GetItems()
	if len(items) == 0 {
		return nil, grpcstatus.Error(grpccodes.NotFound, "registration credential Secret not found")
	}
	if len(items) != 1 {
		return nil, errors.New("multiple registration credential Secrets found")
	}
	response, err := r.secretsClient.Get(ctx, privatev1.SecretsGetRequest_builder{Id: items[0].GetId()}.Build())
	if err != nil {
		return nil, err
	}
	return response.GetObject(), nil
}

func (r *function) getRegistrationSecret(ctx context.Context, account *privatev1.ServiceAccount) (*privatev1.Secret, error) {
	ref := account.GetStatus().GetRegistrationSecret()
	if ref == nil || ref.GetName() == "" {
		return nil, grpcstatus.Error(grpccodes.NotFound, "registration credential Secret reference is missing")
	}
	var secret *privatev1.Secret
	var err error
	if ref.GetId() != "" {
		response, getErr := r.secretsClient.Get(ctx, privatev1.SecretsGetRequest_builder{Id: ref.GetId()}.Build())
		if getErr == nil {
			secret = response.GetObject()
		} else if grpcstatus.Code(getErr) != grpccodes.NotFound {
			return nil, getErr
		}
	}
	if secret == nil {
		secret, err = r.registrationSecretByName(ctx, ref.GetName())
		if err != nil {
			return nil, err
		}
	}
	if secret.GetMetadata().GetName() != ref.GetName() ||
		secret.GetMetadata().GetTenant() != auth.SystemTenant ||
		secret.GetType() != privatev1.SecretType_SECRET_TYPE_OPAQUE ||
		string(secret.GetData()[serviceaccounts.RegistrationServiceAccountIDKey]) != account.GetId() {
		return nil, errors.New("registration credential Secret has a different owner")
	}
	return secret, nil
}

func (r *function) fetchRegistrationCredentials(ctx context.Context, account *privatev1.ServiceAccount) (map[string][]byte, error) {
	secret, err := r.getRegistrationSecret(ctx, account)
	if err != nil {
		return nil, err
	}
	return cloneSecretData(secret.GetData()), nil
}

func (r *function) updateRegistrationSecretReference(ctx context.Context, account *privatev1.ServiceAccount,
	ref *privatev1.SecretLocalReference) error {
	if proto.Equal(account.GetStatus().GetRegistrationSecret(), ref) {
		return nil
	}
	updated := proto.Clone(account).(*privatev1.ServiceAccount)
	status := updated.GetStatus()
	if status == nil {
		status = &privatev1.ServiceAccountStatus{}
		updated.SetStatus(status)
	}
	if ref == nil {
		status.ClearRegistrationSecret()
	} else {
		status.SetRegistrationSecret(proto.Clone(ref).(*privatev1.SecretLocalReference))
	}
	response, err := r.client.Update(ctx, privatev1.ServiceAccountsUpdateRequest_builder{
		Object: updated,
		Lock:   true,
		UpdateMask: &fieldmaskpb.FieldMask{Paths: []string{
			"status.registration_secret",
		}},
	}.Build())
	if err != nil {
		return fmt.Errorf("failed to update registration credential Secret reference: %w", err)
	}
	if response != nil && response.GetObject() != nil && response.GetObject().GetMetadata() != nil {
		account.GetMetadata().SetVersion(response.GetObject().GetMetadata().GetVersion())
	}
	if account.GetStatus() == nil {
		account.SetStatus(&privatev1.ServiceAccountStatus{})
	}
	if ref == nil {
		account.GetStatus().ClearRegistrationSecret()
	} else {
		account.GetStatus().SetRegistrationSecret(proto.Clone(ref).(*privatev1.SecretLocalReference))
	}
	return nil
}

func (r *function) storeRegistrationCredentials(ctx context.Context, account *privatev1.ServiceAccount,
	data map[string][]byte) error {
	accountID := account.GetId()
	data = cloneSecretData(data)
	data[serviceaccounts.RegistrationServiceAccountIDKey] = []byte(accountID)
	baseName := account.GetMetadata().GetName()
	preferredName := account.GetStatus().GetRegistrationSecret().GetName()
	for suffix := 0; suffix < 10000; suffix++ {
		name := serviceaccounts.RegistrationSecretName(baseName, suffix)
		if suffix == 0 && preferredName != "" {
			name = preferredName
		}
		if name == "" {
			return errors.New("could not allocate a registration credential Secret name")
		}
		secret, err := r.registrationSecretByName(ctx, name)
		if grpcstatus.Code(err) == grpccodes.NotFound {
			created, createErr := r.secretsClient.Create(ctx, privatev1.SecretsCreateRequest_builder{
				Object: privatev1.Secret_builder{
					Metadata: privatev1.Metadata_builder{Name: name, Tenant: auth.SystemTenant}.Build(),
					Type:     privatev1.SecretType_SECRET_TYPE_OPAQUE,
					Data:     cloneSecretData(data),
				}.Build(),
			}.Build())
			if grpcstatus.Code(createErr) == grpccodes.AlreadyExists {
				secret, err = r.registrationSecretByName(ctx, name)
				if grpcstatus.Code(err) == grpccodes.NotFound {
					continue
				}
				if err != nil {
					return err
				}
			} else if createErr != nil {
				return createErr
			} else {
				return r.updateRegistrationSecretReference(ctx, account, &privatev1.SecretLocalReference{
					Id: created.GetObject().GetId(), Name: name,
				})
			}
		}
		if err != nil {
			return err
		}
		if string(secret.GetData()[serviceaccounts.RegistrationServiceAccountIDKey]) != accountID {
			continue
		}
		_, err = r.secretsClient.Update(ctx, privatev1.SecretsUpdateRequest_builder{
			Object:     privatev1.Secret_builder{Id: secret.GetId(), Data: cloneSecretData(data)}.Build(),
			UpdateMask: &fieldmaskpb.FieldMask{Paths: []string{"data"}},
		}.Build())
		if err != nil {
			return err
		}
		return r.updateRegistrationSecretReference(ctx, account, &privatev1.SecretLocalReference{
			Id: secret.GetId(), Name: name,
		})
	}
	return errors.New("exhausted registration credential Secret name suffixes")
}

func (r *function) deleteRegistrationCredentials(ctx context.Context, account *privatev1.ServiceAccount) error {
	secret, err := r.getRegistrationSecret(ctx, account)
	if grpcstatus.Code(err) == grpccodes.NotFound {
		return nil
	}
	if err != nil {
		return err
	}
	_, err = r.secretsClient.Delete(ctx, privatev1.SecretsDeleteRequest_builder{Id: secret.GetId()}.Build())
	if grpcstatus.Code(err) == grpccodes.NotFound {
		return nil
	}
	return err
}

func (r *function) removePendingClientSecret(ctx context.Context, account *privatev1.ServiceAccount) error {
	credentials, err := r.fetchRegistrationCredentials(ctx, account)
	if grpcstatus.Code(err) == grpccodes.NotFound {
		return nil
	}
	if err != nil {
		return controllers.RequeueAfter(err, serviceAccountRetryDelay)
	}
	if len(credentials[serviceaccounts.PendingClientSecretKey]) == 0 &&
		len(credentials[serviceaccounts.PendingClientSecretRefIDKey]) == 0 &&
		len(credentials[serviceaccounts.RegistrationClientUUIDKey]) == 0 {
		return nil
	}
	token := credentials[serviceaccounts.RegistrationAccessTokenKey]
	if len(token) == 0 {
		return errors.New("service-account management credential is missing")
	}
	if err := r.storeRegistrationCredentials(ctx, account, map[string][]byte{
		serviceaccounts.RegistrationServiceAccountIDKey: []byte(account.GetId()),
		serviceaccounts.RegistrationAccessTokenKey:      token,
	}); err != nil {
		return controllers.RequeueAfter(err, serviceAccountRetryDelay)
	}
	return nil
}

func (r *function) recoverIncompleteClient(ctx context.Context, clientID, clientUUID, registrationToken string,
	account *privatev1.ServiceAccount) error {
	if err := r.idpClient.DeleteServiceAccountClient(ctx, clientID, clientUUID, registrationToken); err != nil {
		return controllers.RequeueAfter(err, serviceAccountRetryDelay)
	}
	if err := r.deleteRegistrationCredentials(ctx, account); err != nil {
		return controllers.RequeueAfter(err, serviceAccountRetryDelay)
	}
	if err := r.updateRegistrationSecretReference(ctx, account, nil); err != nil {
		return controllers.RequeueAfter(err, serviceAccountRetryDelay)
	}
	return controllers.RequeueAfter(errors.New("removed incomplete service-account client; retrying provisioning"), serviceAccountRetryDelay)
}

func (r *function) compensateClient(ctx context.Context, account *privatev1.ServiceAccount,
	clientID, clientUUID, registrationToken string, deleteCredential bool) {
	cleanupCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), serviceAccountCompensationTimeout)
	defer cancel()
	if err := r.idpClient.DeleteServiceAccountClient(cleanupCtx, clientID, clientUUID, registrationToken); err != nil {
		r.logger.ErrorContext(cleanupCtx, "Failed to delete service-account client during compensation",
			slog.String("client_id", clientID), slog.String("client_uuid", clientUUID), slog.Any("error", err))
		if registrationToken != "" {
			if _, parseErr := uuid.Parse(clientUUID); parseErr == nil {
				if storeErr := r.storeRegistrationCredentials(cleanupCtx, account, map[string][]byte{
					serviceaccounts.RegistrationServiceAccountIDKey: []byte(account.GetId()),
					serviceaccounts.RegistrationAccessTokenKey:      []byte(registrationToken),
					serviceaccounts.RegistrationClientUUIDKey:       []byte(clientUUID),
				}); storeErr != nil {
					r.logger.ErrorContext(cleanupCtx, "Failed to preserve service-account cleanup credential",
						slog.String("client_id", clientID), slog.String("client_uuid", clientUUID), slog.Any("error", storeErr))
				}
			}
		}
		return
	}
	if deleteCredential {
		if err := r.deleteRegistrationCredentials(cleanupCtx, account); err != nil {
			r.logger.ErrorContext(cleanupCtx, "Failed to delete service-account management credential during compensation",
				slog.String("client_id", clientID), slog.String("client_uuid", clientUUID), slog.Any("error", err))
			return
		}
		if err := r.updateRegistrationSecretReference(cleanupCtx, account, nil); err != nil {
			r.logger.ErrorContext(cleanupCtx, "Failed to clear registration credential Secret reference during compensation",
				slog.String("client_id", clientID), slog.String("client_uuid", clientUUID), slog.Any("error", err))
		}
	}
}

func (r *function) delete(ctx context.Context, account *privatev1.ServiceAccount) error {
	metadata := account.GetMetadata()
	accountID := account.GetId()
	if _, err := uuid.Parse(accountID); err != nil {
		return errors.New("invalid service-account identity")
	}
	tenant := metadata.GetTenant()
	if tenant == "" {
		return errors.New("service-account tenant is missing")
	}
	clientID := serviceaccounts.ClientID(tenant, accountID)
	clientUUID := account.GetStatus().GetKeycloakClientUuid()
	if clientUUID != "" {
		if _, err := uuid.Parse(clientUUID); err != nil {
			return errors.New("invalid service-account client UUID")
		}
	}

	clientUUID, registrationToken, clientSecretRef, pendingClientSecret, secretMissing, err :=
		r.fetchDeletionCredentials(ctx, account, clientUUID, account.GetStatus().GetClientSecret())
	if err != nil {
		return err
	}
	if registrationToken == "" {
		foundUUID, found, findErr := r.idpClient.FindServiceAccountClient(ctx, clientID, tenant)
		if findErr != nil {
			return controllers.RequeueAfter(findErr, serviceAccountRetryDelay)
		}
		if found {
			if clientUUID != "" && foundUUID != clientUUID {
				return errors.New("keycloak service-account client UUID does not match its identity")
			}
			if updateErr := r.updateState(ctx, account, privatev1.ServiceAccountState_SERVICE_ACCOUNT_STATE_FAILED, serviceAccountOrphanClientError); updateErr != nil {
				return controllers.RequeueAfter(updateErr, serviceAccountRetryDelay)
			}
			return controllers.RequeueAfter(idp.ErrServiceAccountClientAlreadyExists, serviceAccountRecoveryRetryDelay)
		}
	}
	if clientUUID != "" {
		if err := r.idpClient.DeleteServiceAccountClient(ctx, clientID, clientUUID, registrationToken); err != nil {
			return controllers.RequeueAfter(err, serviceAccountRetryDelay)
		}
	}
	if err := r.deleteClientSecret(ctx, tenant, accountID, clientSecretRef, pendingClientSecret); err != nil {
		return controllers.RequeueAfter(err, serviceAccountRetryDelay)
	}
	if !secretMissing {
		if err := r.deleteRegistrationCredentials(ctx, account); err != nil {
			r.logger.ErrorContext(ctx, "Failed to delete service-account registration credential Secret",
				slog.String("account_id", accountID), slog.Any("error", err))
			return controllers.RequeueAfter(err, serviceAccountRetryDelay)
		}
	}
	updated := proto.Clone(account).(*privatev1.ServiceAccount)
	updated.GetMetadata().SetFinalizers(slices.DeleteFunc(updated.GetMetadata().GetFinalizers(), func(value string) bool { return value == finalizers.Controller }))
	updatePaths := []string{"metadata.finalizers"}
	if updated.GetStatus().GetState() != privatev1.ServiceAccountState_SERVICE_ACCOUNT_STATE_UNSPECIFIED ||
		updated.GetStatus().GetMessage() != "" {
		updated.GetStatus().SetState(privatev1.ServiceAccountState_SERVICE_ACCOUNT_STATE_UNSPECIFIED)
		updated.GetStatus().ClearMessage()
		updatePaths = append(updatePaths, "status.state", "status.message")
	}
	if updated.GetStatus().GetRegistrationSecret() != nil {
		updated.GetStatus().ClearRegistrationSecret()
		updatePaths = append(updatePaths, "status.registration_secret")
	}
	_, err = r.client.Update(ctx, privatev1.ServiceAccountsUpdateRequest_builder{
		Object:     updated,
		Lock:       true,
		UpdateMask: &fieldmaskpb.FieldMask{Paths: updatePaths},
	}.Build())
	if err != nil {
		return controllers.RequeueAfter(err, serviceAccountRetryDelay)
	}
	return nil
}

func (r *function) fetchDeletionCredentials(ctx context.Context, account *privatev1.ServiceAccount, clientUUID string,
	clientSecretRef *privatev1.SecretLocalReference) (string, string, *privatev1.SecretLocalReference, []byte, bool, error) {
	credentials, err := r.fetchRegistrationCredentials(ctx, account)
	secretMissing := grpcstatus.Code(err) == grpccodes.NotFound
	if err != nil && !secretMissing {
		return "", "", nil, nil, false, err
	}
	registrationToken := ""
	var pendingClientSecret []byte
	if !secretMissing {
		if token := credentials[serviceaccounts.RegistrationAccessTokenKey]; len(token) != 0 {
			registrationToken = string(token)
		}
		if clientUUID == "" {
			clientUUID = string(credentials[serviceaccounts.RegistrationClientUUIDKey])
			if clientUUID != "" {
				if _, err := uuid.Parse(clientUUID); err != nil {
					return "", "", nil, nil, false, errors.New("invalid service-account client UUID")
				}
			}
		}
		if registrationToken != "" && clientUUID == "" {
			return "", "", nil, nil, false, errors.New("service-account client UUID is missing")
		}
		if registrationToken == "" && clientUUID != "" {
			return "", "", nil, nil, false, errors.New("service-account management credential is missing")
		}
		if registrationToken == "" && clientUUID == "" && len(credentials) != 0 {
			return "", "", nil, nil, false, errors.New("service-account management credential is incomplete")
		}
		pendingClientSecret = credentials[serviceaccounts.PendingClientSecretKey]
		if clientSecretRef == nil && len(credentials[serviceaccounts.PendingClientSecretRefIDKey]) != 0 {
			clientSecretRef = &privatev1.SecretLocalReference{
				Id:   string(credentials[serviceaccounts.PendingClientSecretRefIDKey]),
				Name: string(credentials[serviceaccounts.PendingClientSecretRefNameKey]),
			}
		}
	}
	return clientUUID, registrationToken, clientSecretRef, pendingClientSecret, secretMissing, nil
}

func (r *function) deleteClientSecret(ctx context.Context, tenant, accountID string, ref *privatev1.SecretLocalReference, expectedSecret []byte) error {
	name := serviceaccounts.ClientSecretName(accountID)
	if ref != nil {
		if ref.GetId() == "" {
			return errors.New("service-account credential Secret reference is missing its ID")
		}
		if ref.GetName() != "" && ref.GetName() != name {
			return errors.New("service-account credential Secret reference has an unexpected name")
		}
		response, err := r.secretsClient.Get(ctx, privatev1.SecretsGetRequest_builder{Id: ref.GetId()}.Build())
		if grpcstatus.Code(err) == grpccodes.NotFound {
			return nil
		}
		if err != nil {
			return err
		}
		secret := response.GetObject()
		clientID := serviceaccounts.ClientID(tenant, accountID)
		if secret.GetMetadata().GetName() != name || secret.GetMetadata().GetTenant() != tenant ||
			string(secret.GetData()[clientIDDataKey]) != clientID {
			return fmt.Errorf("service-account credential Secret %q has a different owner", name)
		}
		_, err = r.secretsClient.Delete(ctx, privatev1.SecretsDeleteRequest_builder{Id: ref.GetId()}.Build())
		if grpcstatus.Code(err) == grpccodes.NotFound {
			return nil
		}
		return err
	}
	filter := fmt.Sprintf("this.metadata.name == %s && this.metadata.tenant == %s", strconv.Quote(name), strconv.Quote(tenant))
	response, err := r.secretsClient.List(ctx, privatev1.SecretsListRequest_builder{Filter: &filter, Limit: new(int32(2))}.Build())
	if err != nil {
		return err
	}
	items := response.GetItems()
	if len(items) == 0 {
		return nil
	}
	if len(items) != 1 {
		return fmt.Errorf("expected one service-account credential Secret named %q, found %d", name, len(items))
	}
	secret := items[0]
	if secret.GetMetadata().GetTenant() != tenant {
		return fmt.Errorf("service-account credential Secret %q has a different owner", name)
	}
	if len(expectedSecret) == 0 {
		return nil
	}
	fetched, getErr := r.secretsClient.Get(ctx, privatev1.SecretsGetRequest_builder{Id: secret.GetId()}.Build())
	if grpcstatus.Code(getErr) == grpccodes.NotFound {
		return nil
	}
	if getErr != nil {
		return getErr
	}
	clientID := serviceaccounts.ClientID(tenant, accountID)
	if string(fetched.GetObject().GetData()[clientIDDataKey]) != clientID ||
		!bytes.Equal(fetched.GetObject().GetData()[clientSecretDataKey], expectedSecret) {
		return fmt.Errorf("service-account credential Secret %q has different data", name)
	}
	_, err = r.secretsClient.Delete(ctx, privatev1.SecretsDeleteRequest_builder{Id: secret.GetId()}.Build())
	if grpcstatus.Code(err) == grpccodes.NotFound {
		return nil
	}
	return err
}

func cloneSecretData(data map[string][]byte) map[string][]byte {
	if data == nil {
		return nil
	}
	cloned := make(map[string][]byte, len(data))
	for key, value := range data {
		cloned[key] = bytes.Clone(value)
	}
	return cloned
}

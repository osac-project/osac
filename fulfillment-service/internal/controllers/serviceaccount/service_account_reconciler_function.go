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
	serviceAccountRotationError       = "Client-secret rotation is pending; reconciliation will retry."
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
		return r.reconcileProvisioned(ctx, account)
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

func (r *function) reconcileProvisioned(ctx context.Context, account *privatev1.ServiceAccount) error {
	status := account.GetStatus()
	switch status.GetSecretRotationState() {
	case privatev1.ServiceAccountSecretRotationState_SERVICE_ACCOUNT_SECRET_ROTATION_STATE_PENDING,
		privatev1.ServiceAccountSecretRotationState_SERVICE_ACCOUNT_SECRET_ROTATION_STATE_ERROR:
		return r.rotateSecret(ctx, account)
	case privatev1.ServiceAccountSecretRotationState_SERVICE_ACCOUNT_SECRET_ROTATION_STATE_COMPLETED:
		if err := r.removePendingRotationSecret(ctx, account, status.GetSecretRotationGeneration()); err != nil {
			return controllers.RequeueAfter(err, serviceAccountRetryDelay)
		}
	case privatev1.ServiceAccountSecretRotationState_SERVICE_ACCOUNT_SECRET_ROTATION_STATE_UNSPECIFIED:
	default:
		return errors.New("invalid service-account secret-rotation state")
	}
	return r.finishExistingProvisioning(ctx, account)
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
			Type: privatev1.SecretType_SECRET_TYPE_SERVICE_ACCOUNT_CREDENTIAL,
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
	if err := r.verifyClientSecret(ctx, secret, tenant, accountID, clientID, clientSecret); err != nil {
		return nil, err
	}
	return &privatev1.SecretLocalReference{Id: secret.GetId(), Name: secret.GetMetadata().GetName()}, nil
}

func isServiceAccountCredentialSecret(secret *privatev1.Secret, name, tenant, accountID string) bool {
	metadata := secret.GetMetadata()
	annotations := metadata.GetAnnotations()
	return metadata.GetName() == name && metadata.GetTenant() == tenant &&
		annotations[tenantAnnotation] == tenant && annotations[ownerReferenceAnnotation] == accountID &&
		secret.GetType() == privatev1.SecretType_SECRET_TYPE_SERVICE_ACCOUNT_CREDENTIAL
}

func (r *function) rotateSecret(ctx context.Context, account *privatev1.ServiceAccount) error {
	accountID := account.GetId()
	tenant := account.GetMetadata().GetTenant()
	status := account.GetStatus()
	generation := status.GetSecretRotationGeneration()
	clientUUID := status.GetKeycloakClientUuid()
	clientSecretRef := status.GetClientSecret()
	if _, err := uuid.Parse(generation); err != nil {
		return r.rotationFailure(ctx, account, errors.New("invalid service-account rotation generation"))
	}
	if _, err := uuid.Parse(clientUUID); err != nil || tenant == "" || clientSecretRef == nil || clientSecretRef.GetId() == "" {
		return r.rotationFailure(ctx, account, errors.New("incomplete service-account identity for secret rotation"))
	}

	credentials, err := r.fetchRegistrationCredentials(ctx, account)
	if err != nil {
		return r.rotationFailure(ctx, account, fmt.Errorf("failed to fetch service-account management credential: %w", err))
	}
	if len(credentials[serviceaccounts.RegistrationAccessTokenKey]) == 0 ||
		string(credentials[serviceaccounts.RegistrationClientUUIDKey]) != clientUUID {
		return r.rotationFailure(ctx, account, errors.New("service-account management credential is incomplete"))
	}

	stagedSecret := credentials[serviceaccounts.PendingRotationSecretKey]
	stagedGeneration := string(credentials[serviceaccounts.PendingRotationGenerationKey])
	if stagedGeneration == generation && len(stagedSecret) == 0 {
		return r.rotationFailure(ctx, account, errors.New("staged service-account rotation credential is missing"))
	}
	if stagedGeneration == "" && len(stagedSecret) != 0 {
		return r.rotationFailure(ctx, account, errors.New("staged service-account rotation generation is missing"))
	}
	if len(stagedSecret) == 0 || stagedGeneration != generation {
		if len(stagedSecret) != 0 || stagedGeneration != "" {
			delete(credentials, serviceaccounts.PendingRotationSecretKey)
			delete(credentials, serviceaccounts.PendingRotationGenerationKey)
			if err := r.storeRegistrationCredentials(ctx, account, credentials); err != nil {
				return r.rotationFailure(ctx, account, fmt.Errorf("failed to clear stale staged rotation credential: %w", err))
			}
		}
		clientID := serviceaccounts.ClientID(tenant, accountID)
		registrationToken := string(credentials[serviceaccounts.RegistrationAccessTokenKey])
		rotatedSecret, updatedRegistrationToken, rotateErr := r.idpClient.RotateServiceAccountClientSecret(
			ctx, clientID, clientUUID, registrationToken)
		if rotateErr != nil {
			return r.rotationFailure(ctx, account, rotateErr)
		}
		if rotatedSecret == "" || updatedRegistrationToken == "" {
			return r.rotationFailure(ctx, account, errors.New("keycloak returned incomplete service-account rotation credentials"))
		}
		stagedSecret = []byte(rotatedSecret)
		credentials[serviceaccounts.RegistrationAccessTokenKey] = []byte(updatedRegistrationToken)
		credentials[serviceaccounts.PendingRotationSecretKey] = stagedSecret
		credentials[serviceaccounts.PendingRotationGenerationKey] = []byte(generation)
		if err := r.storeRegistrationCredentials(ctx, account, credentials); err != nil {
			return r.rotationFailure(ctx, account, fmt.Errorf("failed to stage rotated service-account credential: %w", err))
		}
	}

	secretRef, err := r.updateClientSecret(ctx, tenant, accountID, clientSecretRef, stagedSecret)
	if err != nil {
		return r.rotationFailure(ctx, account, err)
	}
	if err := r.updateRotationStatus(ctx, account, secretRef,
		privatev1.ServiceAccountSecretRotationState_SERVICE_ACCOUNT_SECRET_ROTATION_STATE_COMPLETED, ""); err != nil {
		return controllers.RequeueAfter(fmt.Errorf("failed to mark service-account secret rotation complete: %w", err), serviceAccountRetryDelay)
	}
	if err := r.removePendingRotationSecret(ctx, account, generation); err != nil {
		return controllers.RequeueAfter(err, serviceAccountRetryDelay)
	}
	return r.removePendingClientSecret(ctx, account)
}

func (r *function) updateClientSecret(ctx context.Context, tenant, accountID string,
	ref *privatev1.SecretLocalReference, clientSecret []byte) (*privatev1.SecretLocalReference, error) {
	name := serviceaccounts.ClientSecretName(accountID)
	if ref == nil || ref.GetId() == "" || (ref.GetName() != "" && ref.GetName() != name) {
		return nil, errors.New("service-account credential Secret reference is invalid")
	}
	getSecret := func(id string) (*privatev1.Secret, error) {
		response, err := r.secretsClient.Get(ctx, privatev1.SecretsGetRequest_builder{Id: id}.Build())
		if err != nil {
			return nil, err
		}
		if response.GetObject() == nil {
			return nil, errors.New("service-account credential Secret response is empty")
		}
		return response.GetObject(), nil
	}
	secret, err := getSecret(ref.GetId())
	if grpcstatus.Code(err) == grpccodes.NotFound {
		filter := fmt.Sprintf("this.metadata.name == %s && this.metadata.tenant == %s", strconv.Quote(name), strconv.Quote(tenant))
		limit := int32(2)
		listed, listErr := r.secretsClient.List(ctx,
			privatev1.SecretsListRequest_builder{Filter: &filter, Limit: &limit}.Build())
		if listErr != nil {
			return nil, fmt.Errorf("failed to find service-account credential Secret: %w", listErr)
		}
		if len(listed.GetItems()) > 1 {
			return nil, fmt.Errorf("expected one service-account credential Secret named %q, found %d", name, len(listed.GetItems()))
		}
		if len(listed.GetItems()) == 1 {
			secret, err = getSecret(listed.GetItems()[0].GetId())
		} else {
			return r.createClientSecret(ctx, tenant, accountID, serviceaccounts.ClientID(tenant, accountID), string(clientSecret))
		}
	}
	if err != nil {
		return nil, fmt.Errorf("failed to get service-account credential Secret: %w", err)
	}
	if !isServiceAccountCredentialSecret(secret, name, tenant, accountID) {
		return nil, fmt.Errorf("service-account credential Secret %q has a different owner", name)
	}
	data := cloneSecretData(secret.GetData())
	if data == nil {
		data = make(map[string][]byte)
	}
	if bytes.Equal(data[clientSecretDataKey], clientSecret) {
		return &privatev1.SecretLocalReference{Id: secret.GetId(), Name: name}, nil
	}
	data[clientSecretDataKey] = bytes.Clone(clientSecret)
	updated := proto.Clone(secret).(*privatev1.Secret)
	updated.SetData(data)
	_, err = r.secretsClient.Update(ctx, privatev1.SecretsUpdateRequest_builder{
		Object: updated, Lock: true,
		UpdateMask: &fieldmaskpb.FieldMask{Paths: []string{"data"}},
	}.Build())
	if err != nil {
		return nil, fmt.Errorf("failed to publish rotated service-account credential: %w", err)
	}
	return &privatev1.SecretLocalReference{Id: secret.GetId(), Name: name}, nil
}

func (r *function) updateRotationStatus(ctx context.Context, account *privatev1.ServiceAccount,
	secretRef *privatev1.SecretLocalReference, state privatev1.ServiceAccountSecretRotationState, message string) error {
	updated := proto.Clone(account).(*privatev1.ServiceAccount)
	status := updated.GetStatus()
	if status == nil {
		status = &privatev1.ServiceAccountStatus{}
		updated.SetStatus(status)
	}
	status.SetSecretRotationState(state)
	status.SetSecretRotationError(message)
	if secretRef != nil {
		status.SetClientSecret(secretRef)
	}
	paths := []string{"status.secret_rotation_state", "status.secret_rotation_error"}
	if secretRef != nil {
		paths = append(paths, "status.client_secret")
	}
	response, err := r.client.Update(ctx, privatev1.ServiceAccountsUpdateRequest_builder{
		Object: updated, Lock: true, UpdateMask: &fieldmaskpb.FieldMask{Paths: paths},
	}.Build())
	if err != nil {
		return err
	}
	if response != nil && response.GetObject() != nil {
		if response.GetObject().GetMetadata() != nil {
			account.GetMetadata().SetVersion(response.GetObject().GetMetadata().GetVersion())
		}
		if account.GetStatus() == nil {
			account.SetStatus(&privatev1.ServiceAccountStatus{})
		}
		account.GetStatus().SetSecretRotationState(state)
		account.GetStatus().SetSecretRotationError(message)
		if secretRef != nil {
			account.GetStatus().SetClientSecret(secretRef)
		}
	}
	return nil
}

func (r *function) rotationFailure(ctx context.Context, account *privatev1.ServiceAccount, cause error) error {
	if err := r.updateRotationStatus(ctx, account, nil,
		privatev1.ServiceAccountSecretRotationState_SERVICE_ACCOUNT_SECRET_ROTATION_STATE_ERROR,
		serviceAccountRotationError); err != nil {
		cause = errors.Join(cause, fmt.Errorf("failed to record service-account rotation error: %w", err))
	}
	return controllers.RequeueAfter(cause, serviceAccountRetryDelay)
}

func (r *function) removePendingRotationSecret(ctx context.Context, account *privatev1.ServiceAccount, generation string) error {
	credentials, err := r.fetchRegistrationCredentials(ctx, account)
	if grpcstatus.Code(err) == grpccodes.NotFound {
		return nil
	}
	if err != nil {
		return err
	}
	if string(credentials[serviceaccounts.PendingRotationGenerationKey]) != generation {
		return nil
	}
	if len(credentials[serviceaccounts.PendingRotationSecretKey]) == 0 {
		return errors.New("staged service-account rotation generation has no credential")
	}
	delete(credentials, serviceaccounts.PendingRotationSecretKey)
	delete(credentials, serviceaccounts.PendingRotationGenerationKey)
	return r.storeRegistrationCredentials(ctx, account, credentials)
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

func (r *function) verifyClientSecret(ctx context.Context, listed *privatev1.Secret, tenant, accountID, clientID, expectedSecret string) error {
	if !isServiceAccountCredentialSecret(listed, serviceaccounts.ClientSecretName(accountID), tenant, accountID) {
		return fmt.Errorf("existing service-account credential Secret %q has a different owner", listed.GetMetadata().GetName())
	}
	response, err := r.secretsClient.Get(ctx, privatev1.SecretsGetRequest_builder{Id: listed.GetId()}.Build())
	if err != nil {
		return fmt.Errorf("failed to verify existing service-account credential Secret: %w", err)
	}
	secret := response.GetObject()
	if secret.GetType() != privatev1.SecretType_SECRET_TYPE_SERVICE_ACCOUNT_CREDENTIAL ||
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
		len(credentials[serviceaccounts.PendingClientSecretRefIDKey]) == 0 {
		return nil
	}
	token := credentials[serviceaccounts.RegistrationAccessTokenKey]
	if len(token) == 0 {
		return errors.New("service-account management credential is missing")
	}
	retainedCredentials := map[string][]byte{
		serviceaccounts.RegistrationServiceAccountIDKey: []byte(account.GetId()),
		serviceaccounts.RegistrationAccessTokenKey:      token,
	}
	if clientUUID := credentials[serviceaccounts.RegistrationClientUUIDKey]; len(clientUUID) > 0 {
		retainedCredentials[serviceaccounts.RegistrationClientUUIDKey] = clientUUID
	}
	if err := r.storeRegistrationCredentials(ctx, account, retainedCredentials); err != nil {
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
		if !isServiceAccountCredentialSecret(secret, name, tenant, accountID) ||
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

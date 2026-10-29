/*
Copyright (c) 2026 Red Hat, Inc.

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

package serviceaccounts

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/osac-project/osac/fulfillment-service/internal/database"
)

const tenantAnnotation = "osac.openshift.io/tenant"

var ErrRoleBindingSubjectNotActive = errors.New("service account is not active and provisioned in the RoleBinding tenant")

// AuthorizationIdentity is the current, database-backed authorization state
// for one provisioned service account.
type AuthorizationIdentity struct {
	ID       string
	Tenant   string
	ClientID string
	Roles    []string
}

// ResolveAuthorization finds the active service account identified by the JWT
// client ID, validates its tenant and Keycloak identity, then reads its current
// RoleBinding grants. RoleBinding status and ServiceAccount spec.roles are not
// authorization inputs.
func ResolveAuthorization(ctx context.Context, clientID, tenantClaim string) (*AuthorizationIdentity, error) {
	if clientID == "" || tenantClaim == "" {
		return nil, nil
	}
	accountID, ok := strings.CutPrefix(clientID, ManagedClientIDPrefix+tenantClaim+"-")
	if !ok || accountID == "" {
		return nil, nil
	}
	if _, err := uuid.Parse(accountID); err != nil {
		return nil, nil
	}

	tx, err := database.TxFromContext(ctx)
	if err != nil {
		var identity *AuthorizationIdentity
		_, txErr := database.WithNewTx(ctx, func(txCtx context.Context) (struct{}, error) {
			var resolveErr error
			identity, resolveErr = ResolveAuthorization(txCtx, clientID, tenantClaim)
			return struct{}{}, resolveErr
		})
		if txErr != nil {
			return nil, fmt.Errorf("failed to resolve service account authorization in a transaction: %w", txErr)
		}
		return identity, nil
	}

	var id, tenant string
	var annotationsJSON, dataJSON []byte
	err = tx.QueryRow(ctx, `
		select sa.id, sa.tenant, sa.annotations, sa.data
		from service_accounts sa
		join active_service_accounts active on active.id = sa.id
		join secrets credential
		  on credential.id = sa.data #>> '{status,client_secret,id}'
		join active_secrets active_credential on active_credential.id = credential.id
		where sa.id = $1
		  and sa.tenant = $2
		  and sa.deletion_timestamp = 'epoch'
		  and credential.deletion_timestamp = 'epoch'
		  and credential.tenant = sa.tenant
		  and credential.annotations ->> 'osac.openshift.io/tenant' = sa.tenant
		  and credential.annotations ->> 'osac.openshift.io/owner-reference' = sa.id
	`, accountID, tenantClaim).Scan(&id, &tenant, &annotationsJSON, &dataJSON)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("failed to read service account authorization identity: %w", err)
	}

	var annotations map[string]string
	if err = json.Unmarshal(annotationsJSON, &annotations); err != nil {
		return nil, fmt.Errorf("failed to decode service account annotations: %w", err)
	}
	var data struct {
		Status struct {
			ClientSecret *struct {
				ID string `json:"id"`
			} `json:"client_secret"`
			State              string `json:"state"`
			KeycloakClientUUID string `json:"keycloak_client_uuid"`
		} `json:"status"`
	}
	if err = json.Unmarshal(dataJSON, &data); err != nil {
		return nil, fmt.Errorf("failed to decode service account identity: %w", err)
	}

	if tenant == "" || tenant != tenantClaim || annotations[tenantAnnotation] != tenant ||
		data.Status.ClientSecret == nil || data.Status.ClientSecret.ID == "" ||
		data.Status.State != "SERVICE_ACCOUNT_STATE_READY" || ClientID(tenant, id) != clientID {
		return nil, nil
	}
	if _, err = uuid.Parse(id); err != nil {
		return nil, nil
	}
	if _, err = uuid.Parse(data.Status.KeycloakClientUUID); err != nil {
		return nil, nil
	}

	roles, err := resolveRoleBindingGrants(ctx, tx, tenant, id)
	if err != nil {
		return nil, err
	}

	return &AuthorizationIdentity{
		ID:       id,
		Tenant:   tenant,
		ClientID: clientID,
		Roles:    roles,
	}, nil
}

// ValidateRoleBindingSubject verifies that a RoleBinding references an active,
// fully provisioned service account in the binding's tenant.
func ValidateRoleBindingSubject(ctx context.Context, id, tenant string) error {
	if id == "" || tenant == "" {
		return ErrRoleBindingSubjectNotActive
	}
	identity, err := ResolveAuthorization(ctx, ClientID(tenant, id), tenant)
	if err != nil {
		return err
	}
	if identity == nil || identity.ID != id || identity.Tenant != tenant {
		return ErrRoleBindingSubjectNotActive
	}
	return nil
}

func resolveRoleBindingGrants(ctx context.Context, tx database.Tx, tenant, accountID string) ([]string, error) {
	rows, err := tx.Query(ctx, `
		select r.name
		from role_bindings rb
		join active_role_bindings active_binding on active_binding.id = rb.id
		join roles r on
		  (
		    (coalesce(rb.data #>> '{spec,role,id}', '') <> '' and
		     r.id = rb.data #>> '{spec,role,id}')
		    or
		    (coalesce(rb.data #>> '{spec,role,id}', '') = '' and
		     coalesce(rb.data #>> '{spec,role,name}', '') <> '' and
		     r.name = rb.data #>> '{spec,role,name}')
		  )
		join active_roles active_role on active_role.id = r.id
		where rb.deletion_timestamp = 'epoch'
		  and rb.tenant = $1
		  and (rb.data -> 'spec' -> 'service_accounts') @>
		      jsonb_build_array(jsonb_build_object('id', $2::text))
		  and r.tenant = 'shared'
	`, tenant, accountID)
	if err != nil {
		return nil, fmt.Errorf("failed to read service account RoleBinding grants: %w", err)
	}
	defer rows.Close()

	seen := make(map[string]struct{})
	roles := make([]string, 0)
	for rows.Next() {
		var role string
		if err = rows.Scan(&role); err != nil {
			return nil, fmt.Errorf("failed to read service account RoleBinding role: %w", err)
		}
		if !IsAssignableRole(role) {
			return nil, fmt.Errorf("RoleBinding references unsupported service account role %q", role)
		}
		if _, exists := seen[role]; !exists {
			seen[role] = struct{}{}
			roles = append(roles, role)
		}
	}
	if err = rows.Err(); err != nil {
		return nil, fmt.Errorf("failed to iterate service account RoleBinding grants: %w", err)
	}
	return roles, nil
}

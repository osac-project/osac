/*
Copyright (c) 2026 Red Hat Inc.

Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except in compliance with the
License. You may obtain a copy of the License at

  http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on an
"AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
specific language governing permissions and limitations under the License.
*/

package baremetalinstance

import (
	"context"
	"encoding/json"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	bmfov1alpha1 "github.com/osac-project/osac/bare-metal-fulfillment-operator/api/v1alpha1"
	"github.com/osac-project/osac/fulfillment-service/internal/controllers"
	privatev1 "github.com/osac-project/osac/proto/gen/osac/private/v1"
)

// resolveBareMetalSSHKey loads the current SSH public-key material through the
// private Secret API. The API returns Vault- or HUB-backed data without making
// that material part of the persisted BareMetalInstance. Failures are
// classified: a deleted or unusable Secret is permanent, while API errors are
// transient and retried by the reconciler.
func resolveBareMetalSSHKey(
	ctx context.Context,
	secretsClient privatev1.SecretsClient,
	ref *privatev1.SecretReference,
) (string, error) {
	if ref == nil {
		return "", nil
	}
	if ref.GetId() == "" {
		return "", permanentSSHKeyError("SecretInvalid",
			status.Error(codes.InvalidArgument, "registered SSH-key Secret reference has no canonical ID"))
	}
	response, err := secretsClient.Get(ctx, privatev1.SecretsGetRequest_builder{Id: ref.GetId()}.Build())
	if err != nil {
		resolutionErr := &controllers.SSHKeyResolutionError{Err: err}
		switch status.Code(err) {
		case codes.NotFound:
			resolutionErr.Permanent = true
			resolutionErr.Reason = "SecretNotFound"
		case codes.InvalidArgument:
			resolutionErr.Permanent = true
			resolutionErr.Reason = "SecretInvalid"
		}
		return "", resolutionErr
	}
	secret := response.GetObject()
	if secret == nil {
		return "", &controllers.SSHKeyResolutionError{
			Err: status.Error(codes.Unknown, "Secrets.Get returned no object"),
		}
	}
	// Secrets.Get returns soft-deleted rows: fail closed so deleting a key
	// revokes it for instances that have not materialized it yet.
	if secret.GetMetadata().HasDeletionTimestamp() {
		return "", permanentSSHKeyError("SecretNotFound",
			status.Error(codes.NotFound, "registered SSH-key Secret '"+ref.GetId()+"' has been deleted"))
	}
	if err := controllers.ValidateSSHKeySecretData(secret, secret.GetData(), ref.GetId()); err != nil {
		return "", permanentSSHKeyError("SecretInvalid", err)
	}
	return string(secret.GetData()["public_key"]), nil
}

func permanentSSHKeyError(reason string, err error) *controllers.SSHKeyResolutionError {
	return &controllers.SSHKeyResolutionError{Permanent: true, Reason: reason, Err: err}
}

func materializedBareMetalSSHKey(object *bmfov1alpha1.BareMetalInstance) string {
	if object == nil || object.Spec.TemplateParameters == "" {
		return ""
	}
	var params map[string]any
	if err := json.Unmarshal([]byte(object.Spec.TemplateParameters), &params); err != nil {
		return ""
	}
	value, _ := params["sshPublicKey"].(string)
	return value
}

func (t *task) resolveSSHKey(ctx context.Context, object *bmfov1alpha1.BareMetalInstance) error {
	if raw := t.bareMetalInstance.GetSpec().GetSshPublicKey(); raw != "" {
		t.sshPublicKey = raw
		return nil
	}
	ref := t.bareMetalInstance.GetSpec().GetSshKey()
	if ref == nil {
		t.sshPublicKey = ""
		return nil
	}
	if materialized := materializedBareMetalSSHKey(object); materialized != "" {
		t.sshPublicKey = materialized
		return nil
	}
	key, err := resolveBareMetalSSHKey(ctx, t.r.secretsClient, ref)
	if err != nil {
		return err
	}
	t.sshPublicKey = key
	return nil
}

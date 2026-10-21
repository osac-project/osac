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

	bmfov1alpha1 "github.com/osac-project/osac/bare-metal-fulfillment-operator/api/v1alpha1"
	sharedsshkey "github.com/osac-project/osac/proto/sshkey"
)

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
	ref := t.bareMetalInstance.GetSpec().GetSshKey()
	if ref == nil {
		t.sshPublicKey = ""
		return nil
	}
	if materialized := materializedBareMetalSSHKey(object); materialized != "" {
		t.sshPublicKey = materialized
		return nil
	}
	// The fetch, tenant, and usability rules live in the shared proto-module
	// sshkey package (also used by the operator's resolution paths). The
	// returned error is the shared SSHKeyResolutionError shape: a deleted,
	// foreign, or unusable Secret fails permanently while transient API
	// errors are retried.
	key, err := sharedsshkey.Fetch(ctx, t.r.secretsClient, ref.GetId(),
		t.bareMetalInstance.GetMetadata().GetTenant())
	if err != nil {
		return err
	}
	t.sshPublicKey = key
	return nil
}

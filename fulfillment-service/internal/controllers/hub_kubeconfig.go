/*
Copyright (c) 2025 Red Hat Inc.

Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except in compliance with the
License. You may obtain a copy of the License at

  http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on an
"AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the specific
language governing permissions and limitations under the License.
*/

package controllers

import (
	"context"
	"fmt"

	privatev1 "github.com/osac-project/osac/fulfillment-service/internal/api/osac/private/v1"
)

// HubKubeconfigSecretKey is the Secret data key that holds a kubeconfig document.
const HubKubeconfigSecretKey = "kubeconfig"

const kubeconfigSecretKey = HubKubeconfigSecretKey

// HubSecretGetter fetches a Secret by id. Callers wrap gRPC/DAO errors as needed.
type HubSecretGetter func(ctx context.Context, id string) (*privatev1.Secret, error)

// ResolveHubKubeconfig returns kubeconfig bytes from kubeconfig_secret when set, otherwise the inline kubeconfig.
func ResolveHubKubeconfig(ctx context.Context, spec *privatev1.HubSpec, getSecret HubSecretGetter) ([]byte, error) {
	if spec == nil {
		return nil, fmt.Errorf("hub spec is missing")
	}
	if !spec.HasKubeconfigSecret() {
		return spec.GetKubeconfig(), nil
	}
	ref := spec.GetKubeconfigSecret()
	id := ref.GetId()
	if id == "" {
		return nil, fmt.Errorf("kubeconfig_secret has no id")
	}
	if getSecret == nil {
		return nil, fmt.Errorf("secrets client is required to resolve kubeconfig_secret")
	}
	secret, err := getSecret(ctx, id)
	if err != nil {
		return nil, err
	}
	if secret == nil {
		return nil, fmt.Errorf("kubeconfig secret '%s' not found", id)
	}
	raw, ok := secret.GetData()[HubKubeconfigSecretKey]
	if !ok || len(raw) == 0 {
		return nil, fmt.Errorf("secret '%s' is missing %s key", id, HubKubeconfigSecretKey)
	}
	return raw, nil
}

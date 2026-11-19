/*
Copyright 2026.

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

package controller

import (
	"context"
	"errors"

	logf "sigs.k8s.io/controller-runtime/pkg/log"
)

// ProvisioningMode resolves the installation-wide provider networking mode.
type ProvisioningMode interface {
	Mode(ctx context.Context) (bool, error)
}

// ErrProvisioningModeUnresolved reports that the installation networking mode isn't available yet. It is transient:
// the reconciler retries and takes no provider action in the meantime.
var ErrProvisioningModeUnresolved = errors.New("the installation networking mode is not resolved")

// StaticProvisioningMode returns a fixed mode. Standalone deployments, which have no fulfillment service and no
// NetworkClass, and tests use it.
func StaticProvisioningMode(enabled bool) ProvisioningMode {
	return staticProvisioningMode(enabled)
}

type staticProvisioningMode bool

func (mode staticProvisioningMode) Mode(context.Context) (bool, error) {
	return bool(mode), nil
}

// networkingEnabledForReconcile resolves the provider networking mode and reports requeue when it isn't resolved yet.
// Callers must return without provider action when requeue is set.
func (r *BareMetalInstanceReconciler) networkingEnabledForReconcile(
	ctx context.Context,
) (enabled, requeue bool, err error) {
	enabled, err = r.provisioningEnabled(ctx)
	switch {
	case errors.Is(err, ErrProvisioningModeUnresolved):
		logf.FromContext(ctx).Info("Networking provisioning mode is not resolved yet; requeueing", "reason", err.Error())
		return false, true, nil
	case err != nil:
		return false, false, err
	default:
		return enabled, false, nil
	}
}

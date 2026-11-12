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
	"time"

	logf "sigs.k8s.io/controller-runtime/pkg/log"

	"github.com/osac-project/osac/osac-operator/internal/networkingmode"
	"github.com/osac-project/osac/osac-operator/internal/networktask"
)

// ProvisioningModeRequeue is how long a controller waits before it reads the installation networking mode again.
const ProvisioningModeRequeue = time.Minute

// networkingMode resolves the installation-wide provider networking mode from the deployment's NetworkClass. When the
// mode isn't resolved yet it reports requeue, and the caller must take no provider action.
func networkingMode(ctx context.Context, engine *networktask.Engine) (enabled, requeue bool, err error) {
	mode, err := engine.ProvisioningMode(ctx)
	switch {
	case errors.Is(err, networkingmode.ErrUnresolved):
		logf.FromContext(ctx).Info("Networking provisioning mode is not resolved yet; requeueing",
			"reason", err.Error())
		return false, true, nil
	case err != nil:
		return false, false, err
	default:
		return mode, false, nil
	}
}

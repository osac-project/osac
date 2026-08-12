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
	"fmt"

	"github.com/osac-project/osac/osac-operator/pkg/dispatcher"
)

// resolveImplementationStrategy determines the value a networking controller should
// write into osacImplementationStrategyAnnotation for AAP playbook selection.
//
// When resolver is configured (non-nil, i.e. the gRPC connection and networking
// namespace needed for manager discovery are set up) and networkClassID is non-empty,
// it resolves the NetworkClass's fabric manager via the dispatcher package (the
// "dispatcher path") and returns the resolved manager's name. If the NetworkClass has
// neither a fabricManager nor a k8sManager set yet (dispatcher.ErrNoManagerConfigured),
// it falls back to legacyStrategy (the "implementation_strategy annotation path"). Any
// other resolution error (e.g. a fabricManager referencing an unregistered manager
// ConfigMap) is returned to the caller as a real reconcile error, since that indicates
// a misconfiguration rather than an expected pre-migration state.
//
// When resolver is nil or networkClassID is empty, dispatch is skipped entirely and
// legacyStrategy is returned unchanged — this is the behavior for deployments without
// the two-manager model configured, or resources using the platform-default
// NetworkClass (which has no ID to resolve against).
func resolveImplementationStrategy(
	ctx context.Context,
	resolver *dispatcher.Resolver,
	kind string,
	networkClassID string,
	legacyStrategy string,
) (string, error) {
	if resolver == nil || networkClassID == "" {
		return legacyStrategy, nil
	}

	plan, err := dispatcher.NewDispatcher(resolver).Dispatch(ctx, kind, networkClassID)
	switch {
	case err == nil:
		target := plan.FabricTarget()
		if target == nil {
			// Defensive: every entry in the dispatch table includes the fabric role,
			// so this should not happen in practice.
			return legacyStrategy, nil
		}
		return target.Manager.Name, nil
	case errors.Is(err, dispatcher.ErrNoManagerConfigured):
		return legacyStrategy, nil
	default:
		return "", fmt.Errorf("resolving dispatch plan for %s (networkClass %q): %w", kind, networkClassID, err)
	}
}

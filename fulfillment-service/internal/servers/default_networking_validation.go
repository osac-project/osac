/*
Copyright (c) 2026 Red Hat Inc.

Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except in compliance with the
License. You may obtain a copy of the License at

  http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on an
"AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the specific
language governing permissions and limitations under the License.
*/

package servers

import (
	"context"

	grpccodes "google.golang.org/grpc/codes"
	grpcstatus "google.golang.org/grpc/status"

	"github.com/osac-project/osac/fulfillment-service/internal/auth"
)

const defaultLabel = "osac.openshift.io/default"
const ownerReferenceAnnotation = "osac.openshift.io/owner-reference"

// validateNotDefault prevents direct deletion of system-managed networking.
// Only the authenticated fulfillment controller may delete these objects as
// part of root-project cleanup.
func validateNotDefault(ctx context.Context, labels map[string]string, resourceType string) error {
	if labels[defaultLabel] == "true" && !auth.IsControllerServiceAccount(ctx) {
		return grpcstatus.Errorf(grpccodes.FailedPrecondition,
			"cannot delete default %s: default networking resources are system-managed", resourceType)
	}
	return nil
}

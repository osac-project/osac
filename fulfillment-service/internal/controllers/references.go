/*
Copyright (c) 2026 Red Hat Inc.

Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except in compliance with the
License. You may obtain a copy of the License at

  http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on an
"AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the specific
language governing permissions and limitations under the License.
*/

package controllers

import (
	"math"

	osacv1alpha1 "github.com/osac-project/osac/osac-operator/api/v1alpha1"

	"github.com/osac-project/osac/fulfillment-service/internal/kubernetes/annotations"
	privatev1 "github.com/osac-project/osac/proto/gen/osac/private/v1"
)

// RefKeyer is implemented by all typed reference messages (LocalReference, Reference).
type RefKeyer interface {
	GetId() string
	GetName() string
}

// RefKeyStr extracts the lookup key from a typed reference, preferring id over name.
func RefKeyStr(ref RefKeyer) string {
	if ref.GetId() != "" {
		return ref.GetId()
	}
	return ref.GetName()
}

// AddressPlanSixteenths converts the shares of an address plan, fractions that are multiples of 1/16, to the
// sixteenths that the hub CRs use.
func AddressPlanSixteenths(plan *privatev1.AddressPlan) osacv1alpha1.SubnetAddressPlan {
	return osacv1alpha1.SubnetAddressPlan{
		Overlay: int32(math.Round(plan.GetOverlay() * 16)),
		Fabric:  int32(math.Round(plan.GetFabric() * 16)),
		LBVIP:   int32(math.Round(plan.GetLbVip() * 16)),
	}
}

// AutomaticCRAnnotations returns the annotations of the hub CR of a networking record: the tenant and, for a record
// that the system created for another resource, its owner reference.
func AutomaticCRAnnotations(metadata *privatev1.Metadata) map[string]string {
	result := map[string]string{
		annotations.Tenant: metadata.GetTenant(),
	}
	if owner := metadata.GetAnnotations()[annotations.OwnerReference]; owner != "" && metadata.GetCreator() == "system" {
		result[annotations.OwnerReference] = owner
	}
	return result
}

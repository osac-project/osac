/*
Copyright (c) 2026 Red Hat, Inc.

Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except in compliance with the
License. You may obtain a copy of the License at

  http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on an
"AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the specific
language governing permissions and limitations under the License.
*/

package grpcserver

import (
	"fmt"
	"log/slog"

	"github.com/prometheus/client_golang/prometheus"

	"github.com/osac-project/osac/fulfillment-service/internal/auth"
	"github.com/osac-project/osac/fulfillment-service/internal/references"
	privatev1 "github.com/osac-project/osac/proto/gen/osac/private/v1"
	publicv1 "github.com/osac-project/osac/proto/gen/osac/public/v1"
)

// newReferenceValidator registers ordinary reference lookups and skips fields resolved by their
// handlers. Catalog Item handlers resolve Template and field-policy references after ownership
// assignment and update-mask merging. Resource Create handlers resolve their Catalog Item or
// Template source; Resource Update preserves the stored Catalog Item reference without fetching
// it again. Other references continue through the interceptor's registered lookups.
func newReferenceValidator(logger *slog.Logger, tenancy auth.TenancyLogic, registerer prometheus.Registerer) (*references.ReferenceValidator, error) {
	validator, err := references.NewReferenceValidator().SetLogger(logger).SetMetricsRegisterer(registerer).
		SetExcludedReferencePaths(catalogProvenanceUpdateMethods(), "object.spec.catalog_item").
		SetExcludedReferencePaths(catalogAuthoringMethods(), "object.template", "object.fields").
		SetExcludedReferencePaths(catalogCreationSourceMethods(), "object.spec.catalog_item", "object.spec.template").Build()
	if err != nil {
		return nil, fmt.Errorf("failed to create reference validator: %w", err)
	}
	if err := registerReferenceLookups(validator, logger, tenancy, registerer); err != nil {
		return nil, err
	}
	return validator, nil
}

func catalogProvenanceUpdateMethods() []string {
	return []string{
		publicv1.ComputeInstances_Update_FullMethodName,
		privatev1.ComputeInstances_Update_FullMethodName,
		publicv1.Clusters_Update_FullMethodName,
		privatev1.Clusters_Update_FullMethodName,
		publicv1.BareMetalInstances_Update_FullMethodName,
		privatev1.BareMetalInstances_Update_FullMethodName,
	}
}

func catalogAuthoringMethods() []string {
	return []string{
		publicv1.ComputeInstanceCatalogItems_Create_FullMethodName,
		privatev1.ComputeInstanceCatalogItems_Create_FullMethodName,
		publicv1.ComputeInstanceCatalogItems_Update_FullMethodName,
		privatev1.ComputeInstanceCatalogItems_Update_FullMethodName,
		publicv1.ClusterCatalogItems_Create_FullMethodName,
		privatev1.ClusterCatalogItems_Create_FullMethodName,
		publicv1.ClusterCatalogItems_Update_FullMethodName,
		privatev1.ClusterCatalogItems_Update_FullMethodName,
		publicv1.BareMetalInstanceCatalogItems_Create_FullMethodName,
		privatev1.BareMetalInstanceCatalogItems_Create_FullMethodName,
		publicv1.BareMetalInstanceCatalogItems_Update_FullMethodName,
		privatev1.BareMetalInstanceCatalogItems_Update_FullMethodName,
	}
}

func catalogCreationSourceMethods() []string {
	return []string{
		publicv1.ComputeInstances_Create_FullMethodName,
		privatev1.ComputeInstances_Create_FullMethodName,
		publicv1.Clusters_Create_FullMethodName,
		privatev1.Clusters_Create_FullMethodName,
		publicv1.BareMetalInstances_Create_FullMethodName,
		privatev1.BareMetalInstances_Create_FullMethodName,
	}
}

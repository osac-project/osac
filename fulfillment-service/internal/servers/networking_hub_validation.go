/*
Copyright (c) 2026 Red Hat Inc.

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

package servers

import (
	"context"
	"errors"
	"log/slog"

	"github.com/prometheus/client_golang/prometheus"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/osac-project/osac/fulfillment-service/internal/auth"
	"github.com/osac-project/osac/fulfillment-service/internal/database/dao"
	privatev1 "github.com/osac-project/osac/proto/gen/osac/private/v1"
)

type networkingHubReference struct {
	resourceType string
	id           string
	hubID        string
}

func newNetworkClassesDAO(
	logger *slog.Logger,
	tenancyLogic auth.TenancyLogic,
	metricsRegisterer prometheus.Registerer,
) (*dao.GenericDAO[*privatev1.NetworkClass], error) {
	return dao.NewGenericDAO[*privatev1.NetworkClass]().
		SetLogger(logger).
		SetTenancyLogic(tenancyLogic).
		SetMetricsRegisterer(metricsRegisterer).
		Build()
}

func canonicalNetworkingHubID(
	ctx context.Context,
	logger *slog.Logger,
	networkClassesDao *dao.GenericDAO[*privatev1.NetworkClass],
) (string, error) {
	networkClass, err := findSingletonNetworkClass(ctx, networkClassesDao)
	if err != nil {
		if errors.Is(err, errMultipleActiveNetworkClasses) {
			return "", status.Error(codes.FailedPrecondition, err.Error())
		}
		logger.ErrorContext(ctx, "Failed to resolve the canonical networking Hub", slog.Any("error", err))
		return "", status.Error(codes.Internal, "failed to resolve the canonical networking Hub")
	}
	if networkClass == nil {
		return "", status.Error(codes.FailedPrecondition, "no active NetworkClass is available to resolve the canonical networking Hub")
	}
	hubID := networkClass.GetStatus().GetHub()
	if hubID == "" {
		return "", status.Error(codes.FailedPrecondition,
			"canonical networking Hub assignment is pending; retry after Hub assignment completes")
	}
	return hubID, nil
}

func validateNetworkingHubReferences(canonicalHubID string, references ...networkingHubReference) error {
	if canonicalHubID == "" {
		return status.Error(codes.FailedPrecondition, "the canonical networking Hub is not assigned")
	}
	for _, reference := range references {
		if reference.hubID == "" {
			return status.Errorf(codes.FailedPrecondition,
				"networking reference %s %q has no Hub assignment; retry after it is assigned to the canonical networking Hub",
				reference.resourceType, reference.id)
		}
		if reference.hubID != canonicalHubID {
			return status.Errorf(codes.FailedPrecondition,
				"networking reference %s %q is assigned to a different Hub than the canonical networking Hub",
				reference.resourceType, reference.id)
		}
	}
	return nil
}

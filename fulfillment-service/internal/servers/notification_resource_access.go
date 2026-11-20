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
	"errors"
	"fmt"
	"log/slog"
	"strconv"
	"strings"

	"github.com/prometheus/client_golang/prometheus"

	"github.com/osac-project/osac/fulfillment-service/internal/auth"
	"github.com/osac-project/osac/fulfillment-service/internal/database/dao"
	privatev1 "github.com/osac-project/osac/proto/gen/osac/private/v1"
)

func isDAONotFound(err error) bool {
	var notFound *dao.ErrNotFound
	return errors.As(err, &notFound)
}

// NotificationResourceAccess decides whether the caller may see a notification that
// references another API object. Errors are treated as deny (fail closed).
type NotificationResourceAccess interface {
	MayAccess(ctx context.Context, resource *privatev1.NotificationResourceReference) (bool, error)
	// MayAccessMany reports whether the caller in ctx may access each resource.
	// A nil entry is treated as allowed. Results are aligned with the input slice.
	// Visibility is checked with one DAO List per resource kind using this.id in [...].
	MayAccessMany(ctx context.Context, resources []*privatev1.NotificationResourceReference) ([]bool, error)
}

type resourceIDGetter interface {
	GetId() string
}

// daoNotificationResourceAccess checks curated resource visibility via Get / List.
type daoNotificationResourceAccess struct {
	checkers []resourceKindChecker
}

type resourceKindChecker struct {
	kind        string
	extract     func(*privatev1.NotificationResourceReference) resourceIDGetter
	get         func(context.Context, string) error
	listVisible func(context.Context, []string) (map[string]struct{}, error)
}

func newDAONotificationResourceAccess(
	logger *slog.Logger,
	tenancy auth.TenancyLogic,
	metricsRegisterer prometheus.Registerer,
) (NotificationResourceAccess, error) {
	access := &daoNotificationResourceAccess{}

	add := func(
		kind string,
		extract func(*privatev1.NotificationResourceReference) resourceIDGetter,
		get func(context.Context, string) error,
		listVisible func(context.Context, []string) (map[string]struct{}, error),
	) {
		access.checkers = append(access.checkers, resourceKindChecker{
			kind:        kind,
			extract:     extract,
			get:         get,
			listVisible: listVisible,
		})
	}

	if err := registerNotificationResourceDAO(add, "cluster",
		notificationResourceID(func(r *privatev1.NotificationResourceReference) *privatev1.ClusterLocalReference {
			return r.GetCluster()
		}),
		func() (*dao.GenericDAO[*privatev1.Cluster], error) {
			return dao.NewGenericDAO[*privatev1.Cluster]().
				SetLogger(logger).SetTenancyLogic(tenancy).SetMetricsRegisterer(metricsRegisterer).Build()
		}); err != nil {
		return nil, err
	}
	if err := registerNotificationResourceDAO(add, "compute_instance",
		notificationResourceID(func(r *privatev1.NotificationResourceReference) *privatev1.ComputeInstanceLocalReference {
			return r.GetComputeInstance()
		}),
		func() (*dao.GenericDAO[*privatev1.ComputeInstance], error) {
			return dao.NewGenericDAO[*privatev1.ComputeInstance]().
				SetLogger(logger).SetTenancyLogic(tenancy).SetMetricsRegisterer(metricsRegisterer).Build()
		}); err != nil {
		return nil, err
	}
	if err := registerNotificationResourceDAO(add, "bare_metal_instance",
		notificationResourceID(func(r *privatev1.NotificationResourceReference) *privatev1.BareMetalInstanceLocalReference {
			return r.GetBareMetalInstance()
		}),
		func() (*dao.GenericDAO[*privatev1.BareMetalInstance], error) {
			return dao.NewGenericDAO[*privatev1.BareMetalInstance]().
				SetLogger(logger).SetTenancyLogic(tenancy).SetMetricsRegisterer(metricsRegisterer).Build()
		}); err != nil {
		return nil, err
	}
	if err := registerNotificationResourceDAO(add, "subnet",
		notificationResourceID(func(r *privatev1.NotificationResourceReference) *privatev1.SubnetLocalReference {
			return r.GetSubnet()
		}),
		func() (*dao.GenericDAO[*privatev1.Subnet], error) {
			return dao.NewGenericDAO[*privatev1.Subnet]().
				SetLogger(logger).SetTenancyLogic(tenancy).SetMetricsRegisterer(metricsRegisterer).Build()
		}); err != nil {
		return nil, err
	}
	if err := registerNotificationResourceDAO(add, "virtual_network",
		notificationResourceID(func(r *privatev1.NotificationResourceReference) *privatev1.VirtualNetworkLocalReference {
			return r.GetVirtualNetwork()
		}),
		func() (*dao.GenericDAO[*privatev1.VirtualNetwork], error) {
			return dao.NewGenericDAO[*privatev1.VirtualNetwork]().
				SetLogger(logger).SetTenancyLogic(tenancy).SetMetricsRegisterer(metricsRegisterer).Build()
		}); err != nil {
		return nil, err
	}
	if err := registerNotificationResourceDAO(add, "external_ip",
		notificationResourceID(func(r *privatev1.NotificationResourceReference) *privatev1.ExternalIPLocalReference {
			return r.GetExternalIp()
		}),
		func() (*dao.GenericDAO[*privatev1.ExternalIP], error) {
			return dao.NewGenericDAO[*privatev1.ExternalIP]().
				SetLogger(logger).SetTenancyLogic(tenancy).SetMetricsRegisterer(metricsRegisterer).Build()
		}); err != nil {
		return nil, err
	}

	return access, nil
}

// notificationResourceID wraps a typed getter so an absent oneof arm returns a
// nil interface instead of an interface holding a nil pointer.
func notificationResourceID[T resourceIDGetter](
	get func(*privatev1.NotificationResourceReference) T,
) func(*privatev1.NotificationResourceReference) resourceIDGetter {
	return func(r *privatev1.NotificationResourceReference) resourceIDGetter {
		ref := get(r)
		// Compare the concrete pointer before boxing into the interface.
		var zero T
		if any(ref) == any(zero) {
			return nil
		}
		return ref
	}
}

func registerNotificationResourceDAO[T dao.Object](
	add func(
		string,
		func(*privatev1.NotificationResourceReference) resourceIDGetter,
		func(context.Context, string) error,
		func(context.Context, []string) (map[string]struct{}, error),
	),
	kind string,
	extract func(*privatev1.NotificationResourceReference) resourceIDGetter,
	buildDAO func() (*dao.GenericDAO[T], error),
) error {
	objectDAO, err := buildDAO()
	if err != nil {
		return fmt.Errorf("build %s DAO: %w", kind, err)
	}
	add(kind, extract,
		func(ctx context.Context, id string) error {
			_, err := objectDAO.Get().SetId(id).Do(ctx)
			return err
		},
		func(ctx context.Context, ids []string) (map[string]struct{}, error) {
			if len(ids) == 0 {
				return map[string]struct{}{}, nil
			}
			quoted := make([]string, len(ids))
			for i, id := range ids {
				quoted[i] = strconv.Quote(id)
			}
			filter := fmt.Sprintf("this.id in [%s]", strings.Join(quoted, ", "))
			response, err := objectDAO.List().
				SetFilter(filter).
				SetLimit(int32(len(ids))). // #nosec G115 -- bounded by page size
				Do(ctx)
			if err != nil {
				return nil, err
			}
			visible := make(map[string]struct{}, len(response.GetItems()))
			for _, item := range response.GetItems() {
				visible[item.GetId()] = struct{}{}
			}
			return visible, nil
		},
	)
	return nil
}

func (c *daoNotificationResourceAccess) MayAccess(ctx context.Context,
	resource *privatev1.NotificationResourceReference) (bool, error) {
	if resource == nil {
		return true, nil
	}
	for _, checker := range c.checkers {
		ref := checker.extract(resource)
		if ref == nil {
			continue
		}
		id := ref.GetId()
		if id == "" {
			return false, fmt.Errorf("%s resource reference id is required", checker.kind)
		}
		err := checker.get(ctx, id)
		if err != nil {
			if isDAONotFound(err) {
				return false, nil
			}
			return false, err
		}
		return true, nil
	}
	// Unsupported or empty oneof — fail closed.
	return false, errors.New("unsupported notification resource reference")
}

func (c *daoNotificationResourceAccess) MayAccessMany(ctx context.Context,
	resources []*privatev1.NotificationResourceReference) ([]bool, error) {
	results := make([]bool, len(resources))
	idsByKind := make([][]string, len(c.checkers))

	type resolved struct {
		kindIndex int
		id        string
		check     bool
	}
	resolvedRefs := make([]resolved, len(resources))

	for i, resource := range resources {
		if resource == nil {
			results[i] = true
			continue
		}
		for kindIndex, checker := range c.checkers {
			ref := checker.extract(resource)
			if ref == nil {
				continue
			}
			id := ref.GetId()
			if id == "" {
				// Fail closed for this item; do not fail the batch.
				break
			}
			idsByKind[kindIndex] = append(idsByKind[kindIndex], id)
			resolvedRefs[i] = resolved{kindIndex: kindIndex, id: id, check: true}
			break
		}
		// Empty / unsupported oneof → results[i] stays false.
	}

	visibleByKind := make([]map[string]struct{}, len(c.checkers))
	for kindIndex, checker := range c.checkers {
		ids := uniqueStrings(idsByKind[kindIndex])
		if len(ids) == 0 {
			visibleByKind[kindIndex] = map[string]struct{}{}
			continue
		}
		visible, err := checker.listVisible(ctx, ids)
		if err != nil {
			return nil, fmt.Errorf("%s visibility list: %w", checker.kind, err)
		}
		visibleByKind[kindIndex] = visible
	}

	for i, ref := range resolvedRefs {
		if !ref.check {
			continue
		}
		_, results[i] = visibleByKind[ref.kindIndex][ref.id]
	}
	return results, nil
}

func uniqueStrings(values []string) []string {
	if len(values) == 0 {
		return nil
	}
	seen := make(map[string]struct{}, len(values))
	out := make([]string, 0, len(values))
	for _, value := range values {
		if _, ok := seen[value]; ok {
			continue
		}
		seen[value] = struct{}{}
		out = append(out, value)
	}
	return out
}

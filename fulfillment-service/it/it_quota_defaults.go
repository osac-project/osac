/*
Copyright (c) 2026 Red Hat Inc.

Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except in compliance with the
License. You may obtain a copy of the License at

  http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on an
"AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the specific
language governing permissions and limitations under the License.
*/

package it

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/protobuf/proto"

	privatev1 "github.com/osac-project/osac/proto/gen/osac/private/v1"
	publicv1 "github.com/osac-project/osac/proto/gen/osac/public/v1"
)

// quotaDefaultSeedValue is the default limit the suite writes for every quota dimension it
// touches. It is far above anything the suite creates, so the quota check admits the suite's
// creates the way a configured provider does, while a test that wants the refusal path sets a
// smaller limit explicitly.
const quotaDefaultSeedValue = 1_000_000

// quotaFixedSeedDimensions lists every fixed quota dimension the suite's resources use: the
// vCPUs, memory and volumes of compute, the addresses, gateways and networks of networking,
// the disk images, and the cluster control planes. A dimension with no default has limit 0, so
// with enforcement on and nothing seeded, every create in a tenant is refused. The templated
// dimensions (storage.<tier>.gib, bare_metal.instances.<type>, compute.gpu.<resource>) cannot
// be listed here, because the suite creates their types with per-test random names; the
// interceptor below seeds those as the types appear.
var quotaFixedSeedDimensions = []string{
	"compute.vcpus",
	"compute.memory_gib",
	"storage.volumes",
	"network.external_ips",
	"network.nat_gateways",
	"network.virtual_networks",
	"images.count",
	"caas.control_planes",
}

// SeedQuotaDefaultLimit sets the suite's default limit of one dimension through the private
// QuotaDefaultLimits API, as the administrator. It creates the default, or replaces the value
// of a dimension that already has one, so the call is safe to repeat across runs against a
// live cluster.
func (t *Tool) SeedQuotaDefaultLimit(ctx context.Context, dimension string) error {
	client := privatev1.NewQuotaDefaultLimitsClient(t.InternalView().AdminConn())
	filter := fmt.Sprintf(`this.spec.dimension == %q`, dimension)
	list, err := client.List(ctx, privatev1.QuotaDefaultLimitsListRequest_builder{
		Filter: &filter,
	}.Build())
	if err != nil {
		return fmt.Errorf("failed to seed the default of quota dimension '%s': %w", dimension, err)
	}
	if len(list.GetItems()) > 0 {
		existing := list.GetItems()[0]
		_, err = client.Update(ctx, privatev1.QuotaDefaultLimitsUpdateRequest_builder{
			Object: privatev1.QuotaDefaultLimit_builder{
				Id: existing.GetId(),
				Metadata: privatev1.Metadata_builder{
					Name: existing.GetMetadata().GetName(),
				}.Build(),
				Spec: privatev1.QuotaDefaultLimitSpec_builder{
					Dimension: dimension,
					Value:     proto.Int64(quotaDefaultSeedValue),
				}.Build(),
			}.Build(),
		}.Build())
		if err != nil {
			return fmt.Errorf("failed to seed the default of quota dimension '%s': %w", dimension, err)
		}
		return nil
	}
	_, err = client.Create(ctx, privatev1.QuotaDefaultLimitsCreateRequest_builder{
		Object: privatev1.QuotaDefaultLimit_builder{
			Spec: privatev1.QuotaDefaultLimitSpec_builder{
				Dimension: dimension,
				Value:     proto.Int64(quotaDefaultSeedValue),
			}.Build(),
		}.Build(),
	}.Build())
	if err != nil {
		return fmt.Errorf("failed to seed the default of quota dimension '%s': %w", dimension, err)
	}
	return nil
}

// seedQuotaDefaults writes the suite's default limit for every fixed dimension. Setup calls it
// after the servers are ready and before it creates the first tenant: the tenant reconciler
// gives each new tenant default networking, and that VirtualNetwork create is refused without
// a default for network.virtual_networks.
func (t *Tool) seedQuotaDefaults(ctx context.Context) error {
	t.logger.InfoContext(ctx, "Seeding quota default limits")
	for _, dimension := range quotaFixedSeedDimensions {
		if err := t.SeedQuotaDefaultLimit(ctx, dimension); err != nil {
			return err
		}
	}
	return nil
}

// seedQuotaDefaultsForType is a unary client interceptor that seeds the templated quota
// dimensions as the suite creates the types they derive from: a storage tier makes volumes
// count against storage.<name>.gib, a bare metal instance type makes hosts count against
// bare_metal.instances.<name>, and an instance type's GPU makes instances count against
// compute.gpu.<resource>. The suite's fixtures create these types with per-test random names
// through the tool's connections, so no bootstrap can cover them ahead of time; seeding at the
// create is this suite's counterpart of the database triggers the in-process suite installs
// (see seedQuotaTypeTriggers in internal/servers).
//
// The nested SetDefault runs as the administrator on the internal connection, whatever
// connection created the type, and only after the create succeeded. A seeding failure is
// logged and otherwise ignored: the create itself already succeeded, and the next checked
// create names the missing dimension in its refusal.
func (t *Tool) seedQuotaDefaultsForType(
	ctx context.Context, method string, request, reply any,
	conn *grpc.ClientConn, invoker grpc.UnaryInvoker, opts ...grpc.CallOption,
) error {
	err := invoker(ctx, method, request, reply, conn, opts...)
	if err != nil {
		return err
	}
	dimension := quotaDimensionOfTypeCreate(method, reply)
	if dimension == "" {
		return nil
	}
	seedCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), time.Minute)
	defer cancel()
	if seedErr := t.SeedQuotaDefaultLimit(seedCtx, dimension); seedErr != nil {
		t.logger.WarnContext(seedCtx, "Failed to seed the quota default of a created type",
			slog.String("method", method),
			slog.String("dimension", dimension),
			slog.Any("error", seedErr),
		)
	}
	return nil
}

// quotaDimensionOfTypeCreate returns the templated quota dimension that resources of the type
// the method created count against, or an empty string when the method creates no type, or
// the type prices no templated dimension, such as an instance type without a GPU.
func quotaDimensionOfTypeCreate(method string, reply any) string {
	switch method {
	case privatev1.StorageTiers_Create_FullMethodName:
		if response, ok := reply.(*privatev1.StorageTiersCreateResponse); ok {
			return fmt.Sprintf("storage.%s.gib", response.GetObject().GetMetadata().GetName())
		}
	case privatev1.BareMetalInstanceTypes_Create_FullMethodName:
		if response, ok := reply.(*privatev1.BareMetalInstanceTypesCreateResponse); ok {
			return fmt.Sprintf("bare_metal.instances.%s", response.GetObject().GetMetadata().GetName())
		}
	case privatev1.InstanceTypes_Create_FullMethodName:
		if response, ok := reply.(*privatev1.InstanceTypesCreateResponse); ok {
			return gpuQuotaDimension(response.GetObject().GetSpec().GetGpu().GetResourceName())
		}
	case publicv1.InstanceTypes_Create_FullMethodName:
		if response, ok := reply.(*publicv1.InstanceTypesCreateResponse); ok {
			return gpuQuotaDimension(response.GetObject().GetSpec().GetGpu().GetResourceName())
		}
	}
	return ""
}

// gpuQuotaDimension returns the dimension an instance type's GPUs count against. A type
// without a GPU prices only the fixed vCPU and memory dimensions.
func gpuQuotaDimension(resource string) string {
	if resource == "" {
		return ""
	}
	return fmt.Sprintf("compute.gpu.%s", resource)
}

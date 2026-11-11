/*
Copyright (c) 2025 Red Hat Inc.

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
	"fmt"
	"log/slog"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	. "github.com/onsi/ginkgo/v2/dsl/core"
	. "github.com/onsi/gomega"
	"go.uber.org/mock/gomock"
	grpcmetadata "google.golang.org/grpc/metadata"

	"github.com/osac-project/osac/fulfillment-service/internal/auth"
	"github.com/osac-project/osac/fulfillment-service/internal/database"
	"github.com/osac-project/osac/fulfillment-service/internal/database/dao"
	"github.com/osac-project/osac/fulfillment-service/internal/logging"
	privatev1 "github.com/osac-project/osac/proto/gen/osac/private/v1"
)

func TestServers(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "Servers package")
}

const testTenant = "test-tenant"

var (
	ctx         context.Context
	ctrl        *gomock.Controller
	logger      *slog.Logger
	server      *database.Container
	testPool    *pgxpool.Pool
	tm          database.TxManager
	suiteTx     database.Tx
	attribution *auth.MockAttributionLogic
	tenancy     *auth.MockTenancyLogic
)

var _ = BeforeSuite(func() {
	var err error

	// Create the mock controller:
	ctrl = gomock.NewController(GinkgoT())
	DeferCleanup(ctrl.Finish)

	// Create the logger:
	logger, err = logging.NewLogger().
		SetLevel(slog.LevelDebug.String()).
		SetWriter(GinkgoWriter).
		Build()
	Expect(err).ToNot(HaveOccurred())

	// Create the attribution logic:
	attribution = auth.NewMockAttributionLogic(ctrl)
	attribution.EXPECT().DetermineAssignedCreator(gomock.Any()).
		Return("system", nil).
		AnyTimes()

	// Create the tenancy logic:
	tenancy = auth.NewMockTenancyLogic(ctrl)
	tenancy.EXPECT().DetermineAssignableTenants(gomock.Any()).
		Return(auth.AllTenants, nil).
		AnyTimes()
	tenancy.EXPECT().DetermineDefaultTenant(gomock.Any()).
		Return(testTenant, nil).
		AnyTimes()
	tenancy.EXPECT().DetermineVisibility(gomock.Any()).
		Return(auth.TotalVisibility(), nil).
		AnyTimes()

	// Create the database server:
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	DeferCleanup(cancel)
	server, err = database.NewContainer().
		SetLogger(logger).
		Build()
	Expect(err).ToNot(HaveOccurred())
	DeferCleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
		defer cancel()
		err = server.Stop(ctx)
		Expect(err).ToNot(HaveOccurred())
	})
	err = server.Start(ctx)
	Expect(err).ToNot(HaveOccurred())
})

var _ = BeforeEach(func() {
	var err error

	// Create a context:
	ctx = context.Background()

	// Prepare the database pool:
	db, err := server.NewInstance().Build()
	Expect(err).ToNot(HaveOccurred())
	DeferCleanup(db.Close)
	testPool, err = db.Pool(ctx)
	Expect(err).ToNot(HaveOccurred())
	DeferCleanup(func() {
		testPool.Close()
		testPool = nil
	})

	// Create the transaction manager:
	tm, err = database.NewTxManager().
		SetLogger(logger).
		SetPool(testPool).
		Build()
	Expect(err).ToNot(HaveOccurred())

	// Create the type-seeding triggers in their own committed transaction: CREATE TRIGGER
	// takes a table lock that blocks writes from other transactions, and the suite
	// transaction below stays open for the whole spec.
	seedQuotaTypeTriggers()

	// Start a transaction and add it to the context:
	suiteTx, err = tm.Begin(ctx)
	Expect(err).ToNot(HaveOccurred())
	DeferCleanup(func() {
		if suiteTx == nil {
			return
		}
		err := suiteTx.End(ctx)
		Expect(err).ToNot(HaveOccurred())
		suiteTx = nil
	})
	ctx = database.TxIntoContext(ctx, suiteTx)

	// Create the testTenant in the database so foreign key constraints are satisfied
	// when resources are created with the default tenant mock.
	tenantsDao, err := dao.NewGenericDAO[*privatev1.Tenant]().
		SetLogger(logger).
		SetTenancyLogic(tenancy).
		Build()
	Expect(err).ToNot(HaveOccurred())
	_, err = tenantsDao.Create().
		SetObject(privatev1.Tenant_builder{
			Id: testTenant,
			Metadata: privatev1.Metadata_builder{
				Name:   testTenant,
				Tenant: testTenant,
			}.Build(),
		}.Build()).
		Do(ctx)
	Expect(err).ToNot(HaveOccurred())

	seedQuotaDefaults()
})

// quotaDefaultSeedDimensions lists every quota dimension the suite's resources use, including
// the concrete names of the templated dimensions (storage tiers, host types, GPU resources).
// The quota check refuses a request that adds to a dimension with no limit, so a default of a
// million keeps the suite's creates working the way a configured provider does. Extend the
// list when a test uses a new storage tier, host type or GPU resource; the refusal message
// names the missing dimension.
var quotaDefaultSeedDimensions = []string{
	"compute.vcpus",
	"compute.memory_gib",
	"compute.gpu.nvidia.com/A100",
	"compute.gpu.nvidia.com/H100",
	"compute.gpu.nvidia.com/V100",
	"storage.volumes",
	"storage.standard.gib",
	"storage.tier1.gib",
	"storage.fast.gib",
	"storage.gold.gib",
	"storage.local.gib",
	"storage.network.gib",
	"storage.nfs-tier.gib",
	"storage.premium-block.gib",
	"storage.silver.gib",
	"storage.standard-block.gib",
	"images.count",
	"network.external_ips",
	"network.nat_gateways",
	"network.virtual_networks",
	"caas.control_planes",
	"bare_metal.instances.bm.large",
	"bare_metal.instances.gpu.gb200",
	"bare_metal.instances.bmi-type-name",
	"bare_metal.instances.metal-1tb",
	"bare_metal.instances.small",
	"bare_metal.instances.large",
	"bare_metal.instances.default-type",
	"bare_metal.instances.test-bmit-standard",
	"bare_metal.instances.acme-bmit-name",
	"bare_metal.instances.acme-gpu-name",
	"bare_metal.instances.legacy-tenant-bmit",
	"bare_metal.instances.resize-target",
}

// seedQuotaDefaults writes a default limit for every seeded dimension. The name matches the
// one the API derives from the dimension, so a later SetDefault of the same dimension updates
// the row without touching its immutable name.
func seedQuotaDefaults() {
	tx, err := database.TxFromContext(ctx)
	Expect(err).ToNot(HaveOccurred())
	for _, dimension := range quotaDefaultSeedDimensions {
		_, err = tx.Exec(ctx,
			`insert into quota_default_limits (id, name, creator, data)
			values ($1, $2, 'system', jsonb_build_object('spec',
			jsonb_build_object('dimension', $3::text, 'value', '1000000')))`,
			"seed-"+dimension, quotaNameFromDimension(dimension), dimension,
		)
		Expect(err).ToNot(HaveOccurred())
	}
}

// seedQuotaTypeTriggers creates triggers that seed a default limit for the templated
// dimensions as the types appear: every host type counts as one of
// bare_metal.instances.<name>, every storage tier as storage.<name>.gib, and an instance
// type's GPU as compute.gpu.<resource>. The suite's transaction stays open for the whole
// spec, so the triggers are created and committed in their own transaction before it begins.
func seedQuotaTypeTriggers() {
	tx, err := tm.Begin(ctx)
	Expect(err).ToNot(HaveOccurred())
	DeferCleanup(func() {
		if tx == nil {
			return
		}
		Expect(tx.End(ctx)).To(Succeed())
		tx = nil
	})
	_, err = tx.Exec(ctx, `
		create function seed_quota_default_for_type() returns trigger as $$
		declare
			dimension text;
		begin
			case TG_TABLE_NAME
				when 'bare_metal_instance_types' then
					dimension := 'bare_metal.instances.' || new.name;
				when 'storage_tiers' then
					dimension := 'storage.' || new.name || '.gib';
				when 'instance_types' then
					dimension := 'compute.gpu.' ||
						(new.data -> 'spec' -> 'gpu' ->> 'resource_name');
			end case;
			if dimension is null or dimension = 'compute.gpu.' then
				return null;
			end if;
			insert into quota_default_limits (id, name, creator, data)
			values (
				'seed-' || dimension,
				'seed-' || dimension,
				'system',
				jsonb_build_object('spec', jsonb_build_object(
					'dimension', dimension, 'value', '1000000'))
			)
			on conflict (id) do nothing;
			return null;
		end;
		$$ language plpgsql`)
	Expect(err).ToNot(HaveOccurred())
	for _, table := range []string{
		"bare_metal_instance_types", "storage_tiers", "instance_types",
	} {
		_, err = tx.Exec(ctx, fmt.Sprintf(
			`create trigger seed_quota_default_for_type after insert on %s
			for each row execute function seed_quota_default_for_type()`, table))
		Expect(err).ToNot(HaveOccurred())
	}
	Expect(tx.End(ctx)).To(Succeed())
	tx = nil
}

func dryRunCtx() context.Context {
	md := grpcmetadata.Pairs(DryRunMetadataKey, "true")
	return grpcmetadata.NewIncomingContext(ctx, md)
}

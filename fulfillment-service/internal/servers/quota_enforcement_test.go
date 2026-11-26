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
	"sync"
	"time"

	"github.com/jackc/pgerrcode"
	"github.com/jackc/pgx/v5/pgconn"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/prometheus/client_golang/prometheus/testutil"
	"go.uber.org/mock/gomock"
	grpccodes "google.golang.org/grpc/codes"
	grpcstatus "google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/fieldmaskpb"
	"google.golang.org/protobuf/types/known/timestamppb"

	"github.com/osac-project/osac/fulfillment-service/internal/auth"
	"github.com/osac-project/osac/fulfillment-service/internal/database"
	"github.com/osac-project/osac/fulfillment-service/internal/database/dao"
	osactesting "github.com/osac-project/osac/fulfillment-service/internal/testing"
	privatev1 "github.com/osac-project/osac/proto/gen/osac/private/v1"
	publicv1 "github.com/osac-project/osac/proto/gen/osac/public/v1"
)

// quotaTierResolver resolves every tier to the same provider, so the volume tests need no
// storage backends.
var quotaTierResolver = TierResolverFunc(func(_ context.Context, _ string) (*TierResolution, error) {
	return &TierResolution{
		Provider: "test-provider",
		Protocol: privatev1.StorageProtocol_STORAGE_PROTOCOL_BLOCK,
	}, nil
})

// quotaFailure and the row-seeding helpers live in quota_test_fixtures_test.go.

var _ = Describe("Quota enforcement", func() {
	var volumes *PrivateVolumesServer
	var instances *GenericServer[*privatev1.ComputeInstance]

	BeforeEach(func() {
		var err error
		volumes, err = NewPrivateVolumesServer().
			SetLogger(logger).
			SetAttributionLogic(attribution).
			SetTenancyLogic(tenancy).
			SetTierResolver(quotaTierResolver).
			Build()
		Expect(err).ToNot(HaveOccurred())

		instances, err = NewGenericServer[*privatev1.ComputeInstance]().
			SetLogger(logger).
			SetService(privatev1.ComputeInstances_ServiceDesc.ServiceName).
			SetAttributionLogic(attribution).
			SetTenancyLogic(tenancy).
			Build()
		Expect(err).ToNot(HaveOccurred())
	})

	// committed runs the function in its own transaction, committed, so concurrent
	// transactions and the after-commit scans can see what it wrote. Inside the
	// function, ctx carries that transaction, so the seeding helpers write to it.
	committed := func(fn func()) {
		tx, err := tm.Begin(ctx)
		Expect(err).ToNot(HaveOccurred())
		saved := ctx
		ctx = database.TxIntoContext(ctx, tx)
		fn()
		ctx = saved
		Expect(tx.End(ctx)).To(Succeed())
	}

	deleteRow := func(table, id string) {
		tx, err := database.TxFromContext(ctx)
		Expect(err).ToNot(HaveOccurred())
		_, err = tx.Exec(ctx, `delete from `+table+` where id = $1`, id)
		Expect(err).ToNot(HaveOccurred())
	}

	insertDefaultLimit := func(dimension string, value int64) {
		// The name is the one the servers derive, so a later SetDefault of the dimension
		// updates the row instead of rejecting a name change:
		insertNamedRow("quota_default_limits", "default-"+dimension, quotaNameFromDimension(dimension),
			auth.SharedTenant, "",
			`{"spec":{"dimension":"`+dimension+`","value":`+fmt.Sprint(value)+`}}`)
	}

	insertThreshold := func(project, dimension string, percent int32) {
		insertRow("quota_thresholds", "threshold-"+dimension+"-"+project, testTenant, project,
			`{"spec":{"dimension":"`+dimension+`","percent":`+fmt.Sprint(percent)+`}}`)
	}

	createProject := func(name string) {
		tx, err := database.TxFromContext(ctx)
		Expect(err).ToNot(HaveOccurred())
		_, err = tx.Exec(ctx,
			`insert into projects (id, tenant, project, name, creator, data)
			values (uuidv7(), $1, ''::ltree, $2::ltree, 'system', '{}')`, testTenant, name,
		)
		Expect(err).ToNot(HaveOccurred())
	}

	insertInstanceType := func(name string, vcpus, memoryGib int32) {
		insertRow("instance_types", name, auth.SharedTenant, "",
			`{"spec":{"vcpus":`+fmt.Sprint(vcpus)+`,"memory_gib":`+fmt.Sprint(memoryGib)+`}}`)
	}

	createVolume := func(name, project, tier string, sizeGib int64) error {
		_, err := volumes.Create(ctx, privatev1.VolumesCreateRequest_builder{
			Object: privatev1.Volume_builder{
				Metadata: privatev1.Metadata_builder{Name: name, Tenant: testTenant, Project: project}.Build(),
				Spec: privatev1.VolumeSpec_builder{
					StorageTier: tier,
					SizeGib:     sizeGib,
					AccessMode:  privatev1.VolumeAccessMode_VOLUME_ACCESS_MODE_READ_WRITE_ONCE,
				}.Build(),
			}.Build(),
		}.Build())
		return err
	}

	createInstance := func(name, project, typeName string) error {
		var response *privatev1.ComputeInstancesCreateResponse
		return instances.Create(ctx, privatev1.ComputeInstancesCreateRequest_builder{
			Object: privatev1.ComputeInstance_builder{
				Metadata: privatev1.Metadata_builder{Name: name, Tenant: testTenant, Project: project}.Build(),
				Spec: privatev1.ComputeInstanceSpec_builder{
					InstanceType: &privatev1.InstanceTypeReference{Name: typeName},
				}.Build(),
			}.Build(),
		}.Build(), &response)
	}

	updateInstance := func(request *privatev1.ComputeInstancesUpdateRequest) error {
		var response *privatev1.ComputeInstancesUpdateResponse
		return instances.Update(ctx, request, &response)
	}

	countRows := func(table string) int {
		tx, err := database.TxFromContext(ctx)
		Expect(err).ToNot(HaveOccurred())
		var count int
		Expect(tx.QueryRow(ctx, `select count(*) from `+table).Scan(&count)).To(Succeed())
		return count
	}

	outboxRows := func() []map[string]any {
		tx, err := database.TxFromContext(ctx)
		Expect(err).ToNot(HaveOccurred())
		rows, err := tx.Query(ctx,
			`select tenant, project::text, dimension, data from quota_outbox order by dimension`)
		Expect(err).ToNot(HaveOccurred())
		defer rows.Close()
		var result []map[string]any
		for rows.Next() {
			var tenant, project, dimension string
			var data []byte
			Expect(rows.Scan(&tenant, &project, &dimension, &data)).To(Succeed())
			result = append(result, map[string]any{
				"tenant": tenant, "project": project, "dimension": dimension, "data": data,
			})
		}
		Expect(rows.Err()).To(Succeed())
		return result
	}

	firstInstanceId := func() string {
		tx, err := database.TxFromContext(ctx)
		Expect(err).ToNot(HaveOccurred())
		var id string
		Expect(tx.QueryRow(ctx, `select id from compute_instances`).Scan(&id)).To(Succeed())
		return id
	}

	Describe("Creates over a limit", func() {
		BeforeEach(func() {
			insertQuotaLimit(testTenant, "", "storage.volumes", 100)
			insertQuotaLimit(testTenant, "", "storage.fast.gib", 10)
		})

		It("refuses a create over the tenant limit and writes nothing", func() {
			Expect(createVolume("vol-1", "", "fast", 5)).To(Succeed())

			err := createVolume("vol-2", "", "fast", 6)
			reason, failure := quotaFailure(err)
			Expect(reason).To(Equal("QUOTA_EXCEEDED"))
			Expect(failure.GetScope()).To(Equal(publicv1.QuotaScope_QUOTA_SCOPE_TENANT))
			Expect(failure.GetTenant()).To(Equal(testTenant))
			Expect(failure.GetDimension()).To(Equal("storage.fast.gib"))
			Expect(failure.GetLimit()).To(Equal(int64(10)))
			Expect(failure.GetUsage()).To(Equal(int64(5)))
			Expect(failure.GetAdded()).To(Equal(int64(6)))
			Expect(failure.GetSources()).To(ConsistOf("spec.size_gib"))
			Expect(countRows("volumes")).To(Equal(1))
		})

		It("refuses a create over a project limit and names the project", func() {
			createProject("team")
			insertQuotaLimit(testTenant, "team", "storage.fast.gib", 4)

			err := createVolume("vol-1", "team", "fast", 5)
			reason, failure := quotaFailure(err)
			Expect(reason).To(Equal("QUOTA_EXCEEDED"))
			Expect(failure.GetScope()).To(Equal(publicv1.QuotaScope_QUOTA_SCOPE_PROJECT))
			Expect(failure.GetProject()).To(Equal("team"))
			Expect(failure.GetDimension()).To(Equal("storage.fast.gib"))
			Expect(failure.GetLimit()).To(Equal(int64(4)))
			Expect(failure.GetUsage()).To(Equal(int64(0)))
			Expect(failure.GetAdded()).To(Equal(int64(5)))
			Expect(countRows("volumes")).To(Equal(0))
		})

		It("refuses a create over an ancestor project's limit and names the ancestor", func() {
			createProject("team")
			createProject("team.web")
			insertQuotaLimit(testTenant, "team", "storage.fast.gib", 4)

			err := createVolume("vol-1", "team.web", "fast", 5)
			reason, failure := quotaFailure(err)
			Expect(reason).To(Equal("QUOTA_EXCEEDED"))
			Expect(failure.GetScope()).To(Equal(publicv1.QuotaScope_QUOTA_SCOPE_ANCESTOR_PROJECT))
			Expect(failure.GetProject()).To(Equal("team"))
			Expect(failure.GetDimension()).To(Equal("storage.fast.gib"))
			Expect(countRows("volumes")).To(Equal(0))
		})

		It("names the tenant when a project without a limit of its own is bound by the tenant's", func() {
			createProject("team")
			Expect(createVolume("vol-default", "", "fast", 3)).To(Succeed())
			Expect(createVolume("vol-team", "team", "fast", 6)).To(Succeed())

			// The team subtree crosses the tenant's limit, and so does the tenant as a whole.
			// The tenant holds the only limit, so the refusal must name the tenant and show
			// the usage the tenant's limit counts:
			err := createVolume("vol-team-2", "team", "fast", 5)
			reason, failure := quotaFailure(err)
			Expect(reason).To(Equal("QUOTA_EXCEEDED"))
			Expect(failure.GetScope()).To(Equal(publicv1.QuotaScope_QUOTA_SCOPE_TENANT))
			Expect(failure.GetProject()).To(BeEmpty())
			Expect(failure.GetDimension()).To(Equal("storage.fast.gib"))
			Expect(failure.GetLimit()).To(Equal(int64(10)))
			Expect(failure.GetUsage()).To(Equal(int64(9)))
			Expect(failure.GetAdded()).To(Equal(int64(5)))
			Expect(countRows("volumes")).To(Equal(2))
		})

		It("names the tenant's limit, not the project's higher one, when both apply", func() {
			// The project's own limit is far above the tenant's. Usage plus the ask fits the
			// project and crosses the tenant, so the refusal must compare against the
			// tenant's limit and name the tenant, not the project:
			createProject("team")
			insertQuotaLimit(testTenant, "team", "storage.fast.gib", 100)
			Expect(createVolume("vol-team", "team", "fast", 7)).To(Succeed())

			err := createVolume("vol-team-2", "team", "fast", 5)
			reason, failure := quotaFailure(err)
			Expect(reason).To(Equal("QUOTA_EXCEEDED"))
			Expect(failure.GetScope()).To(Equal(publicv1.QuotaScope_QUOTA_SCOPE_TENANT))
			Expect(failure.GetProject()).To(BeEmpty())
			Expect(failure.GetDimension()).To(Equal("storage.fast.gib"))
			Expect(failure.GetLimit()).To(Equal(int64(10)))
			Expect(failure.GetUsage()).To(Equal(int64(7)))
			Expect(failure.GetAdded()).To(Equal(int64(5)))
			Expect(countRows("volumes")).To(Equal(1))
		})

		It("reports the conflict of a retried create instead of the quota refusal", func() {
			Expect(createVolume("vol-1", "", "fast", 6)).To(Succeed())

			// The retry names the volume the first create landed, so the caller must see the
			// conflict the write would report, not the refusal of a second copy. The CSI
			// driver recovers only from the conflict, so a lost response leaves no stranded
			// volume:
			err := createVolume("vol-1", "", "fast", 6)
			Expect(grpcstatus.Code(err)).To(Equal(grpccodes.AlreadyExists))
			Expect(err.Error()).To(ContainSubstring("'vol-1'"))
			Expect(countRows("volumes")).To(Equal(1))
		})

		It("prices a create whose reference carries a type id", func() {
			// The servers' fill-in stores both the id and the name on a reference, and
			// the price follows the id, the way RefKeyStr reads references: a name that
			// no row carries must not misdirect the lookup when the id points at one.
			tx, err := database.TxFromContext(ctx)
			Expect(err).ToNot(HaveOccurred())
			_, err = tx.Exec(ctx,
				`insert into instance_types (id, name, tenant, project, creator, data)
				values ('type-id-1', 'gpu-8', 'shared', ''::ltree, 'system',
				'{"spec":{"vcpus":8,"memory_gib":32}}'::jsonb)`)
			Expect(err).ToNot(HaveOccurred())
			insertQuotaLimit(testTenant, "", "compute.vcpus", 7)

			var response *privatev1.ComputeInstancesCreateResponse
			err = instances.Create(ctx, privatev1.ComputeInstancesCreateRequest_builder{
				Object: privatev1.ComputeInstance_builder{
					Metadata: privatev1.Metadata_builder{Name: "vm-by-id", Tenant: testTenant}.Build(),
					Spec: privatev1.ComputeInstanceSpec_builder{
						InstanceType: &privatev1.InstanceTypeReference{Id: "type-id-1", Name: "no-such-type"},
					}.Build(),
				}.Build(),
			}.Build(), &response)
			reason, failure := quotaFailure(err)
			Expect(reason).To(Equal("QUOTA_EXCEEDED"))
			Expect(failure.GetDimension()).To(Equal("compute.vcpus"))
			Expect(failure.GetAdded()).To(Equal(int64(8)))
		})

		It("prices the type a scoped reference selects, not a same-name type of another scope", func() {
			// Two types of one name, one per scope, priced differently: the shared one
			// at ninety-nine vCPUs, the tenant's at two.
			insertInstanceType("scoped-type", 99, 99)
			tx, err := database.TxFromContext(ctx)
			Expect(err).ToNot(HaveOccurred())
			_, err = tx.Exec(ctx,
				`insert into instance_types (id, name, tenant, project, creator, data)
				values ('scoped-type-tenant', 'scoped-type', $1, ''::ltree, 'system',
				'{"spec":{"vcpus":2,"memory_gib":4}}'::jsonb)`, testTenant)
			Expect(err).ToNot(HaveOccurred())
			insertQuotaLimit(testTenant, "", "compute.vcpus", 4)
			insertQuotaLimit(testTenant, "", "compute.memory_gib", 1000)

			// The reference carries the tenant selector, so the check prices the
			// tenant's type of two vCPUs, which fits the limit of four:
			var response *privatev1.ComputeInstancesCreateResponse
			err = instances.Create(ctx, privatev1.ComputeInstancesCreateRequest_builder{
				Object: privatev1.ComputeInstance_builder{
					Metadata: privatev1.Metadata_builder{Name: "vm-tenant-type", Tenant: testTenant}.Build(),
					Spec: privatev1.ComputeInstanceSpec_builder{
						InstanceType: &privatev1.InstanceTypeReference{Name: "scoped-type", Tenant: testTenant},
					}.Build(),
				}.Build(),
			}.Build(), &response)
			Expect(err).To(Succeed())

			// A reference that selects the shared type prices its ninety-nine, which
			// the limit of four refuses:
			err = instances.Create(ctx, privatev1.ComputeInstancesCreateRequest_builder{
				Object: privatev1.ComputeInstance_builder{
					Metadata: privatev1.Metadata_builder{Name: "vm-shared-type", Tenant: testTenant}.Build(),
					Spec: privatev1.ComputeInstanceSpec_builder{
						InstanceType: &privatev1.InstanceTypeReference{Name: "scoped-type", Tenant: "shared"},
					}.Build(),
				}.Build(),
			}.Build(), &response)
			reason, failure := quotaFailure(err)
			Expect(reason).To(Equal("QUOTA_EXCEEDED"))
			Expect(failure.GetDimension()).To(Equal("compute.vcpus"))
			Expect(failure.GetUsage()).To(Equal(int64(2)))
			Expect(failure.GetAdded()).To(Equal(int64(99)))
		})

		// limitResolution is one scenario of the order: the seeded defaults and
		// overrides, the volume sizes that fit, the refused create's size, and the
		// refusal's limit, usage and added.
		type limitResolution struct {
			defaults  map[string]int64
			overrides map[string]int64
			tier      string
			fitting   []int64
			refused   int64
			expected  int64
			usage     int64
			added     int64
		}

		DescribeTable("resolves the limit from the tenant override, the default, then 0",
			func(scenario limitResolution) {
				for dimension, value := range scenario.defaults {
					insertDefaultLimit(dimension, value)
				}
				for dimension, value := range scenario.overrides {
					insertQuotaLimit(testTenant, "", dimension, value)
				}
				for i, size := range scenario.fitting {
					Expect(createVolume(fmt.Sprintf("vol-%d", i+1), "", scenario.tier, size)).
						To(Succeed())
				}

				_, failure := quotaFailure(createVolume(
					fmt.Sprintf("vol-%d", len(scenario.fitting)+1), "", scenario.tier, scenario.refused))
				Expect(failure.GetScope()).To(Equal(publicv1.QuotaScope_QUOTA_SCOPE_TENANT))
				Expect(failure.GetLimit()).To(Equal(scenario.expected))
				Expect(failure.GetUsage()).To(Equal(scenario.usage))
				Expect(failure.GetAdded()).To(Equal(scenario.added))
			},

			Entry("the default when the tenant sets no override",
				limitResolution{
					defaults: map[string]int64{"storage.slow.gib": 3},
					tier:     "slow",
					fitting:  []int64{2},
					refused:  2,
					expected: 3, usage: 2, added: 2,
				},
			),
			Entry("the tenant's override over the default",
				limitResolution{
					defaults:  map[string]int64{"storage.archive.gib": 3},
					overrides: map[string]int64{"storage.archive.gib": 10},
					tier:      "archive",
					fitting:   []int64{5, 5},
					refused:   1,
					expected:  10, usage: 10, added: 1,
				},
			),
			Entry("zero when nothing sets the dimension",
				limitResolution{
					tier:     "slow",
					refused:  1,
					expected: 0, usage: 0, added: 1,
				},
			),
		)

		It("runs the check on a dry run and writes nothing", func() {
			_, err := volumes.Create(dryRunCtx(), privatev1.VolumesCreateRequest_builder{
				Object: privatev1.Volume_builder{
					Metadata: privatev1.Metadata_builder{Name: "dry", Tenant: testTenant}.Build(),
					Spec: privatev1.VolumeSpec_builder{
						StorageTier: "fast",
						SizeGib:     11,
						AccessMode:  privatev1.VolumeAccessMode_VOLUME_ACCESS_MODE_READ_WRITE_ONCE,
					}.Build(),
				}.Build(),
			}.Build())
			reason, _ := quotaFailure(err)
			Expect(reason).To(Equal("QUOTA_EXCEEDED"))
			Expect(countRows("volumes")).To(Equal(0))
		})

		It("fits at a limit of 0 on a dimension the request adds nothing to", func() {
			// The suite seeds a compute.vcpus default; drop it, so the dimension's limit is 0,
			// but a volume adds no vCPUs. No type-seeding trigger re-creates the row, because
			// the spec inserts no types:
			tx, err := database.TxFromContext(ctx)
			Expect(err).ToNot(HaveOccurred())
			_, err = tx.Exec(ctx,
				`delete from quota_default_limits where data -> 'spec' ->> 'dimension' = 'compute.vcpus'`)
			Expect(err).ToNot(HaveOccurred())
			Expect(createVolume("vol-1", "", "fast", 5)).To(Succeed())
		})

		It("skips the check for the built-in shared and system tenants", func() {
			// A default far below the volume, in place of the suite's million: the built-in
			// tenants hold no override of their own, so only the skip lets their creates
			// through.
			deleteRow("quota_default_limits", "seed-storage.fast.gib")
			insertDefaultLimit("storage.fast.gib", 5)

			// The same create in a normal tenant is refused by its override, so the
			// built-in tenants' success below proves the skip and not a fitting limit:
			_, failure := quotaFailure(createVolume("vol-over-limit", "", "fast", 1000))
			Expect(failure.GetScope()).To(Equal(publicv1.QuotaScope_QUOTA_SCOPE_TENANT))
			Expect(failure.GetDimension()).To(Equal("storage.fast.gib"))
			Expect(failure.GetLimit()).To(Equal(int64(10)))
			Expect(failure.GetUsage()).To(Equal(int64(0)))
			Expect(failure.GetAdded()).To(Equal(int64(1000)))
			Expect(countRows("volumes")).To(Equal(0))

			sharedServer, err := NewGenericServer[*privatev1.Volume]().
				SetLogger(logger).
				SetService(privatev1.Volumes_ServiceDesc.ServiceName).
				SetAttributionLogic(attribution).
				SetTenancyLogic(tenancy).
				AddAllowedTenants(auth.SharedTenant, auth.SystemTenant).
				Build()
			Expect(err).ToNot(HaveOccurred())
			for _, tenant := range []string{auth.SharedTenant, auth.SystemTenant} {
				var response *privatev1.VolumesCreateResponse
				err = sharedServer.Create(ctx, privatev1.VolumesCreateRequest_builder{
					Object: privatev1.Volume_builder{
						Metadata: privatev1.Metadata_builder{Name: "builtin-" + tenant, Tenant: tenant}.Build(),
						Spec: privatev1.VolumeSpec_builder{
							StorageTier: "fast",
							SizeGib:     1000,
							AccessMode:  privatev1.VolumeAccessMode_VOLUME_ACCESS_MODE_READ_WRITE_ONCE,
						}.Build(),
					}.Build(),
				}.Build(), &response)
				Expect(err).ToNot(HaveOccurred())
			}
			Expect(countRows("volumes")).To(Equal(2))
		})
	})

	Describe("Kinds the installation never creates", func() {
		createWithAutoAddress := func(server *GenericServer[*privatev1.ComputeInstance]) error {
			autoAttachment := true
			var response *privatev1.ComputeInstancesCreateResponse
			return server.Create(ctx, privatev1.ComputeInstancesCreateRequest_builder{
				Object: privatev1.ComputeInstance_builder{
					Metadata: privatev1.Metadata_builder{Name: "vm-auto-address", Tenant: testTenant}.Build(),
					Spec: privatev1.ComputeInstanceSpec_builder{
						AutoExternalIpAttachment: &autoAttachment,
					}.Build(),
				}.Build(),
			}.Build(), &response)
		}

		// publishNetworkingDisabled republishes the installation settings the way the startup
		// does, before any server is built, so the shared parts derive the uncreated kinds from
		// them. The cleanup publishes the previous settings back, which resets the parts again.
		publishNetworkingDisabled := func() {
			previous := installationNetworkingSettings
			disabled := NetworkingSettings{ProvisioningEnabled: false}
			SetInstallationNetworkingSettings(&disabled)
			DeferCleanup(func() {
				SetInstallationNetworkingSettings(&previous)
			})
		}

		It("refuses the automatic address when the installation creates it", func() {
			// The suite seeds a generous default for the dimension, so an override of 0
			// makes the refusal observable: a create that asks for an automatic
			// address adds one address to a limit of 0:
			insertQuotaLimit(testTenant, "", "network.external_ips", 0)
			reason, failure := quotaFailure(createWithAutoAddress(instances))
			Expect(reason).To(Equal("QUOTA_EXCEEDED"))
			Expect(failure.GetDimension()).To(Equal("network.external_ips"))
			Expect(failure.GetAdded()).To(Equal(int64(1)))
			Expect(countRows("compute_instances")).To(Equal(0))
		})

		It("prices no automatic address when the installation creates none", func() {
			// Provider networking disabled means the writers create no automatic
			// addresses, so the check must not count them either: with the same limit
			// of 0 the create passes, because the address the request asks for is
			// nothing the system will actually create. The settings are published before
			// the server is built, the order the startup uses.
			insertQuotaLimit(testTenant, "", "network.external_ips", 0)
			publishNetworkingDisabled()
			skipping, err := NewGenericServer[*privatev1.ComputeInstance]().
				SetLogger(logger).
				SetService(privatev1.ComputeInstances_ServiceDesc.ServiceName).
				SetAttributionLogic(attribution).
				SetTenancyLogic(tenancy).
				Build()
			Expect(err).ToNot(HaveOccurred())

			Expect(createWithAutoAddress(skipping)).To(Succeed())
			Expect(countRows("compute_instances")).To(Equal(1))
		})

		It("counts no stored ask for a kind the installation never creates", func() {
			// A stored instance that asks for an automatic address is a stored ask for an
			// ExternalIP. With provider networking disabled no address ever arrives to net
			// it, so the shared counter must skip it. The check returns before reading the
			// counter when its added map holds no dimension, so this create asks for an
			// explicit address: it adds exactly the one unit of room the stored ask must not
			// hold, and the create fits only if the counter skipped it.
			insertRow("compute_instances", "vm-auto", testTenant, "",
				`{"spec":{"auto_external_ip_attachment":true},"status":{}}`)
			insertQuotaLimit(testTenant, "", "network.external_ips", 1)

			// The order the startup uses: publish the settings, then build the server.
			publishNetworkingDisabled()
			addresses, err := NewGenericServer[*privatev1.ExternalIP]().
				SetLogger(logger).
				SetService(privatev1.ExternalIPs_ServiceDesc.ServiceName).
				SetAttributionLogic(attribution).
				SetTenancyLogic(tenancy).
				Build()
			Expect(err).ToNot(HaveOccurred())

			var response *privatev1.ExternalIPsCreateResponse
			err = addresses.Create(ctx, privatev1.ExternalIPsCreateRequest_builder{
				Object: privatev1.ExternalIP_builder{
					Metadata: privatev1.Metadata_builder{Name: "address-1", Tenant: testTenant}.Build(),
				}.Build(),
			}.Build(), &response)
			Expect(err).ToNot(HaveOccurred())
		})
	})

	Describe("Updates over a limit", func() {
		updateInstanceType := func(id, typeName string) error {
			return updateInstance(privatev1.ComputeInstancesUpdateRequest_builder{
				Object: privatev1.ComputeInstance_builder{
					Id:   id,
					Spec: privatev1.ComputeInstanceSpec_builder{InstanceType: &privatev1.InstanceTypeReference{Name: typeName}}.Build(),
				}.Build(),
				UpdateMask: &fieldmaskpb.FieldMask{Paths: []string{"spec.instance_type"}},
			}.Build())
		}

		// statusClock gives each status write a later instance type transition time, the
		// way the operator feedback does; the database rejects one that doesn't move
		// forward. The hour of headroom clears the row's creation timestamp whatever the
		// database clock says.
		statusClock := time.Now().Add(time.Hour)
		updateInstanceStatusType := func(id, typeName string) error {
			statusClock = statusClock.Add(time.Second)
			return updateInstance(privatev1.ComputeInstancesUpdateRequest_builder{
				Object: privatev1.ComputeInstance_builder{
					Id: id,
					Status: privatev1.ComputeInstanceStatus_builder{
						InstanceType:               &privatev1.InstanceTypeReference{Name: typeName},
						InstanceTypeTransitionTime: timestamppb.New(statusClock),
					}.Build(),
				}.Build(),
				UpdateMask: &fieldmaskpb.FieldMask{Paths: []string{
					"status.instance_type", "status.instance_type_transition_time",
				}},
			}.Build())
		}

		It("refuses an update that adds over the limit and writes nothing", func() {
			insertQuotaLimit(testTenant, "", "compute.vcpus", 4)
			insertQuotaLimit(testTenant, "", "compute.memory_gib", 100)
			insertInstanceType("small", 2, 4)
			insertInstanceType("large", 6, 8)
			Expect(createInstance("vm-1", "", "small")).To(Succeed())

			id := firstInstanceId()
			err := updateInstanceType(id, "large")
			reason, failure := quotaFailure(err)
			Expect(reason).To(Equal("QUOTA_EXCEEDED"))
			Expect(failure.GetDimension()).To(Equal("compute.vcpus"))
			Expect(failure.GetLimit()).To(Equal(int64(4)))
			Expect(failure.GetUsage()).To(Equal(int64(2)))
			Expect(failure.GetAdded()).To(Equal(int64(4)))
			Expect(failure.GetSources()).To(ConsistOf("spec.instance_type"))
			var getResponse *privatev1.ComputeInstancesGetResponse
			Expect(instances.Get(ctx, privatev1.ComputeInstancesGetRequest_builder{Id: id}.Build(),
				&getResponse)).To(Succeed())
			Expect(getResponse.GetObject().GetSpec().GetInstanceType().GetName()).To(Equal("small"))
		})

		It("does not check an update whose mask names no annotated spec field", func() {
			insertQuotaLimit(testTenant, "", "compute.vcpus", 0)
			insertQuotaLimit(testTenant, "", "compute.memory_gib", 0)
			insertInstanceType("small", 2, 4)
			Expect(createInstance("vm-1", "", "small")).ToNot(Succeed())

			// The create was refused at limit 0, so insert the row directly and update its
			// status, which no annotation reads:
			insertRow("compute_instances", "vm-1", testTenant, "",
				`{"spec":{"instance_type":{"name":"small"}},"status":{}}`)
			Expect(updateInstanceStatusType("vm-1", "small")).To(Succeed())
			Expect(updateInstance(privatev1.ComputeInstancesUpdateRequest_builder{
				Object: privatev1.ComputeInstance_builder{
					Id: "vm-1",
					Spec: privatev1.ComputeInstanceSpec_builder{
						RunStrategy: privatev1.ComputeInstanceRunStrategy_COMPUTE_INSTANCE_RUN_STRATEGY_ALWAYS.Enum(),
					}.Build(),
				}.Build(),
				UpdateMask: &fieldmaskpb.FieldMask{Paths: []string{"spec.run_strategy"}},
			}.Build())).To(Succeed())
		})

		It("checks an update with no field mask", func() {
			insertQuotaLimit(testTenant, "", "compute.vcpus", 4)
			insertQuotaLimit(testTenant, "", "compute.memory_gib", 100)
			insertInstanceType("small", 2, 4)
			insertInstanceType("large", 6, 8)
			Expect(createInstance("vm-1", "", "small")).To(Succeed())

			err := updateInstance(privatev1.ComputeInstancesUpdateRequest_builder{
				Object: privatev1.ComputeInstance_builder{
					Id:       firstInstanceId(),
					Metadata: privatev1.Metadata_builder{Name: "vm-1", Tenant: testTenant}.Build(),
					Spec: privatev1.ComputeInstanceSpec_builder{
						InstanceType: &privatev1.InstanceTypeReference{Name: "large"},
					}.Build(),
				}.Build(),
			}.Build())
			_, failure := quotaFailure(err)
			Expect(failure.GetDimension()).To(Equal("compute.vcpus"))
		})

		It("checks an update whose mask path needs normalizing", func() {
			insertQuotaLimit(testTenant, "", "compute.vcpus", 4)
			insertQuotaLimit(testTenant, "", "compute.memory_gib", 100)
			insertInstanceType("small", 2, 4)
			insertInstanceType("large", 6, 8)
			Expect(createInstance("vm-1", "", "small")).To(Succeed())

			// The field-path compiler trims whitespace and drops empty segments before
			// it applies the mask, so a raw comparison would let this mask update the
			// type while skipping the check:
			err := updateInstance(privatev1.ComputeInstancesUpdateRequest_builder{
				Object: privatev1.ComputeInstance_builder{
					Id: firstInstanceId(),
					Spec: privatev1.ComputeInstanceSpec_builder{
						InstanceType: &privatev1.InstanceTypeReference{Name: "large"},
					}.Build(),
				}.Build(),
				UpdateMask: &fieldmaskpb.FieldMask{Paths: []string{"spec. instance_type"}},
			}.Build())
			_, failure := quotaFailure(err)
			Expect(failure.GetDimension()).To(Equal("compute.vcpus"))
		})

		It("locks no tenant row of the built-in tenants for a checked update", func() {
			// The row is committed in its own transaction: inserting it through the
			// suite's transaction would take a key-share lock on the shared tenant's
			// row, and the second transaction's lock below would wait for it forever.
			committed(func() {
				insertRow("compute_instances", "vm-shared", "shared", "",
					`{"spec":{"instance_type":{"name":"small"}},"status":{}}`)
			})
			enforcer, err := newQuotaEnforcer(logger, nil)
			Expect(err).ToNot(HaveOccurred())
			instancesDAO, err := dao.NewGenericDAO[*privatev1.ComputeInstance]().
				SetLogger(logger).
				SetTenancyLogic(tenancy).
				Build()
			Expect(err).ToNot(HaveOccurred())
			enforcer.objects = &quotaDAO[*privatev1.ComputeInstance]{objects: instancesDAO}

			// Hold the shared tenant's row in a second transaction, in the mode the lock
			// itself uses: no key update doesn't conflict with the key-share the
			// suite's seeding holds, but a lock taken on the row would block the call
			// below, and the suite's transaction fails fast instead of waiting, so a
			// regression shows as an error, not a hang:
			other, err := tm.Begin(ctx)
			Expect(err).ToNot(HaveOccurred())
			DeferCleanup(func(ctx context.Context) { _ = other.End(ctx) })
			_, err = other.Exec(ctx, `select id from tenants where id = 'shared' for no key update`)
			Expect(err).ToNot(HaveOccurred())
			tx, err := database.TxFromContext(ctx)
			Expect(err).ToNot(HaveOccurred())
			_, err = tx.Exec(ctx, `set local lock_timeout = '500ms'`)
			Expect(err).ToNot(HaveOccurred())

			Expect(enforcer.lockTenantOf(ctx, "vm-shared")).To(Succeed())
		})

		It("does not free quota on a resize down until the resource runs smaller", func() {
			insertQuotaLimit(testTenant, "", "compute.vcpus", 10)
			insertQuotaLimit(testTenant, "", "compute.memory_gib", 100)
			insertInstanceType("small", 2, 4)
			insertInstanceType("large", 6, 8)
			Expect(createInstance("vm-1", "", "large")).To(Succeed())
			id := firstInstanceId()

			// The resize down adds nothing, so it passes, but the instance still runs large:
			Expect(updateInstanceType(id, "small")).To(Succeed())
			Expect(updateInstanceStatusType(id, "large")).To(Succeed())

			// By the spec alone vm-1 counts 2 and the create would fit; the larger-of rule
			// keeps it at 6 while it runs large:
			err := createInstance("vm-2", "", "large")
			reason, failure := quotaFailure(err)
			Expect(reason).To(Equal("QUOTA_EXCEEDED"))
			Expect(failure.GetDimension()).To(Equal("compute.vcpus"))
			Expect(failure.GetUsage()).To(Equal(int64(6)))
			Expect(failure.GetAdded()).To(Equal(int64(6)))
			Expect(countRows("compute_instances")).To(Equal(1))

			// Once the instance runs small, the create fits:
			Expect(updateInstanceStatusType(id, "small")).To(Succeed())
			Expect(createInstance("vm-2", "", "large")).To(Succeed())
		})
	})

	Describe("Concurrent requests in one tenant", func() {
		BeforeEach(func() {
			committed(func() {
				tx, err := database.TxFromContext(ctx)
				Expect(err).ToNot(HaveOccurred())
				_, err = tx.Exec(ctx,
					`insert into tenants (id, name, tenant, creator, data)
					values ('acme', 'acme', 'acme', 'system', '{}')`)
				Expect(err).ToNot(HaveOccurred())
				insertQuotaLimit("acme", "", "compute.vcpus", 1)
				insertQuotaLimit("acme", "", "compute.memory_gib", 100)
				insertQuotaLimit("acme", "", "caas.control_planes", 10)
				insertRow("instance_types", "small", auth.SharedTenant, "",
					`{"spec":{"vcpus":1,"memory_gib":2}}`)
			})
		})

		createInstanceInTx := func(runCtx context.Context, name string) error {
			var response *privatev1.ComputeInstancesCreateResponse
			return instances.Create(runCtx, privatev1.ComputeInstancesCreateRequest_builder{
				Object: privatev1.ComputeInstance_builder{
					Metadata: privatev1.Metadata_builder{Name: name, Tenant: "acme"}.Build(),
					Spec: privatev1.ComputeInstanceSpec_builder{
						InstanceType: &privatev1.InstanceTypeReference{Name: "small"},
					}.Build(),
				}.Build(),
			}.Build(), &response)
		}

		It("lets only one of two parallel creates for the last vCPU pass", func() {
			txOne, err := tm.Begin(ctx)
			Expect(err).ToNot(HaveOccurred())
			txTwo, err := tm.Begin(ctx)
			Expect(err).ToNot(HaveOccurred())
			// A bug that never unblocks the second create fails its lock wait fast
			// instead of holding the connection until the suite times out:
			_, err = txTwo.Exec(ctx, `set local lock_timeout = '10s'`)
			Expect(err).ToNot(HaveOccurred())
			var twoPID int
			Expect(txTwo.QueryRow(ctx, `select pg_backend_pid()`).Scan(&twoPID)).To(Succeed())

			// The first create holds the tenant lock until its transaction commits; the
			// second waits for the lock, then counts the first:
			Expect(createInstanceInTx(database.TxIntoContext(ctx, txOne), "vm-one")).To(Succeed())

			var twoErr error
			var done sync.WaitGroup
			done.Add(1)
			go func() {
				defer done.Done()
				twoErr = createInstanceInTx(database.TxIntoContext(ctx, txTwo), "vm-two")
			}()
			// Prove the second create contends before the first commits: wait until it
			// asks for the tenant lock the first create holds. Without the barrier the
			// second create could start after the commit and refuse without ever
			// racing. Poll on a separate connection: the suite's transaction keeps one
			// view for its whole life and would never see the second move.
			Eventually(func() (bool, error) {
				var blocked bool
				err := testPool.QueryRow(context.Background(), `
					select exists (
						select 1 from pg_locks where pid = $1 and not granted
					)`, twoPID).Scan(&blocked)
				return blocked, err
			}, 10*time.Second, 10*time.Millisecond).Should(BeTrue())

			Expect(txOne.End(ctx)).To(Succeed())
			done.Wait()
			Expect(txTwo.End(ctx)).To(Succeed())

			reason, failure := quotaFailure(twoErr)
			Expect(reason).To(Equal("QUOTA_EXCEEDED"))
			Expect(failure.GetDimension()).To(Equal("compute.vcpus"))
			Expect(failure.GetUsage()).To(Equal(int64(1)))
			Expect(failure.GetAdded()).To(Equal(int64(1)))
		})

		It("finishes a cluster create and a VM create that lock the same subnet row", func() {
			clusters, err := NewGenericServer[*privatev1.Cluster]().
				SetLogger(logger).
				SetService(privatev1.Clusters_ServiceDesc.ServiceName).
				SetAttributionLogic(attribution).
				SetTenancyLogic(tenancy).
				Build()
			Expect(err).ToNot(HaveOccurred())
			committed(func() {
				insertRow("subnets", "subnet", "acme", "", `{}`)
			})

			// Both fill-ins lock the subnet's row after the tenant lock, the way the
			// networking creates do; neither request waits for the other's locks in the
			// opposite order:
			lockSubnet := func(runCtx context.Context) error {
				subnetsDao, err := dao.NewGenericDAO[*privatev1.Subnet]().
					SetLogger(logger).
					SetTenancyLogic(tenancy).
					Build()
				if err != nil {
					return err
				}
				_, err = subnetsDao.Get().SetId("subnet").SetLock(true).Do(runCtx)
				return err
			}

			txCluster, err := tm.Begin(ctx)
			Expect(err).ToNot(HaveOccurred())
			txVm, err := tm.Begin(ctx)
			Expect(err).ToNot(HaveOccurred())

			clusterDone := make(chan struct{})
			clusterEnded := make(chan struct{})
			var clusterErr error
			go func() {
				defer close(clusterDone)
				var response *privatev1.ClustersCreateResponse
				clusterErr = clusters.CreateWithCandidatePreparation(
					database.TxIntoContext(ctx, txCluster),
					privatev1.ClustersCreateRequest_builder{
						Object: privatev1.Cluster_builder{
							Metadata: privatev1.Metadata_builder{Name: "cluster", Tenant: "acme"}.Build(),
						}.Build(),
					}.Build(),
					&response,
					func(runCtx context.Context, _, candidate *privatev1.Cluster) error {
						return lockSubnet(runCtx)
					},
				)
			}()
			go func() {
				<-clusterDone
				if endErr := txCluster.End(ctx); endErr != nil {
					clusterErr = endErr
				}
				close(clusterEnded)
			}()

			var vmErr error
			var vmDone sync.WaitGroup
			vmDone.Add(1)
			go func() {
				defer vmDone.Done()
				var response *privatev1.ComputeInstancesCreateResponse
				vmErr = instances.CreateWithCandidatePreparation(
					database.TxIntoContext(ctx, txVm),
					privatev1.ComputeInstancesCreateRequest_builder{
						Object: privatev1.ComputeInstance_builder{
							Metadata: privatev1.Metadata_builder{Name: "vm-subnet", Tenant: "acme"}.Build(),
							Spec: privatev1.ComputeInstanceSpec_builder{
								InstanceType: &privatev1.InstanceTypeReference{Name: "small"},
							}.Build(),
						}.Build(),
					}.Build(),
					&response,
					func(runCtx context.Context, _, candidate *privatev1.ComputeInstance) error {
						return lockSubnet(runCtx)
					},
				)
			}()
			vmDone.Wait()
			Expect(txVm.End(ctx)).To(Succeed())
			<-clusterEnded

			Expect(clusterErr).ToNot(HaveOccurred())
			Expect(vmErr).ToNot(HaveOccurred())
		})
	})

	Describe("Requests in separate tenants", func() {
		BeforeEach(func() {
			committed(func() {
				tx, err := database.TxFromContext(ctx)
				Expect(err).ToNot(HaveOccurred())
				_, err = tx.Exec(ctx,
					`insert into tenants (id, name, tenant, creator, data)
					values ('acme', 'acme', 'acme', 'system', '{}'),
					       ('globex', 'globex', 'globex', 'system', '{}')`)
				Expect(err).ToNot(HaveOccurred())
				insertQuotaLimit("acme", "", "compute.vcpus", 1)
				insertQuotaLimit("acme", "", "compute.memory_gib", 100)
				insertRow("instance_types", "small", auth.SharedTenant, "",
					`{"spec":{"vcpus":1,"memory_gib":2}}`)
			})
		})

		It("does not wait on a tenant lock held in another tenant", func() {
			// Each checked request locks its own tenant's row and nothing else, so a
			// request in one tenant must finish while another tenant's row is locked.
			// Were the lock shared, the create below would wait and fail on its bounded
			// lock timeout instead of succeeding:
			txHold, err := tm.Begin(ctx)
			Expect(err).ToNot(HaveOccurred())
			DeferCleanup(func(ctx context.Context) { _ = txHold.End(ctx) })
			_, err = txHold.Exec(ctx, `select id from tenants where id = 'globex' for no key update`)
			Expect(err).ToNot(HaveOccurred())

			txCheck, err := tm.Begin(ctx)
			Expect(err).ToNot(HaveOccurred())
			_, err = txCheck.Exec(ctx, `set local lock_timeout = '2s'`)
			Expect(err).ToNot(HaveOccurred())

			var response *privatev1.ComputeInstancesCreateResponse
			err = instances.Create(database.TxIntoContext(ctx, txCheck),
				privatev1.ComputeInstancesCreateRequest_builder{
					Object: privatev1.ComputeInstance_builder{
						Metadata: privatev1.Metadata_builder{Name: "vm-acme", Tenant: "acme"}.Build(),
						Spec: privatev1.ComputeInstanceSpec_builder{
							InstanceType: &privatev1.InstanceTypeReference{Name: "small"},
						}.Build(),
					}.Build(),
				}.Build(), &response)
			Expect(err).ToNot(HaveOccurred())

			// The create took acme's own row, so the lock it never waited for is
			// globex's alone: a second no-key-update on acme's row is refused at once
			// while the create's transaction is open. globex's holder is still open too.
			var lockedID string
			err = testPool.QueryRow(context.Background(),
				`select id from tenants where id = 'acme' for no key update nowait`).Scan(&lockedID)
			Expect(err).To(HaveOccurred())
			var pgErr *pgconn.PgError
			Expect(errors.As(err, &pgErr)).To(BeTrue(), "want a lock-not-available error, got: %v", err)
			if pgErr != nil {
				Expect(pgErr.Code).To(Equal(pgerrcode.LockNotAvailable))
			}

			Expect(txCheck.End(ctx)).To(Succeed())
		})

		It("takes no lock on the tenant of an object the caller cannot see", func() {
			// The object sits in globex, committed, so only the caller's visibility hides it:
			committed(func() {
				insertRow("compute_instances", "vm-globex", "globex", "",
					`{"spec":{"instance_type":{"name":"small"}},"status":{}}`)
			})

			// A server whose caller sees only acme:
			visibility, err := auth.NewVisibility().
				AddVisibleTenants("acme").
				Build()
			Expect(err).ToNot(HaveOccurred())
			scopedTenancy := auth.NewMockTenancyLogic(ctrl)
			scopedTenancy.EXPECT().DetermineVisibility(gomock.Any()).
				Return(visibility, nil).
				AnyTimes()
			scopedInstances, err := NewGenericServer[*privatev1.ComputeInstance]().
				SetLogger(logger).
				SetService(privatev1.ComputeInstances_ServiceDesc.ServiceName).
				SetAttributionLogic(attribution).
				SetTenancyLogic(scopedTenancy).
				Build()
			Expect(err).ToNot(HaveOccurred())

			// Another transaction holds globex's tenant row. A gate that reads the row before
			// it checks visibility would wait here and fail on the bounded lock timeout,
			// instead of reporting the object the caller can't see:
			txHold, err := tm.Begin(ctx)
			Expect(err).ToNot(HaveOccurred())
			DeferCleanup(func(ctx context.Context) { _ = txHold.End(ctx) })
			_, err = txHold.Exec(ctx, `select id from tenants where id = 'globex' for no key update`)
			Expect(err).ToNot(HaveOccurred())

			txUpdate, err := tm.Begin(ctx)
			Expect(err).ToNot(HaveOccurred())
			_, err = txUpdate.Exec(ctx, `set local lock_timeout = '2s'`)
			Expect(err).ToNot(HaveOccurred())

			var response *privatev1.ComputeInstancesUpdateResponse
			err = scopedInstances.Update(database.TxIntoContext(ctx, txUpdate),
				privatev1.ComputeInstancesUpdateRequest_builder{
					Object: privatev1.ComputeInstance_builder{
						Id: "vm-globex",
						Spec: privatev1.ComputeInstanceSpec_builder{
							InstanceType: &privatev1.InstanceTypeReference{Name: "small"},
						}.Build(),
					}.Build(),
					UpdateMask: &fieldmaskpb.FieldMask{Paths: []string{"spec.instance_type"}},
				}.Build(), &response)
			Expect(grpcstatus.Code(err)).To(Equal(grpccodes.NotFound))
			Expect(txUpdate.End(ctx)).To(Succeed())
		})
	})

	Describe("Statement snapshots in one check", func() {
		It("sees usage committed between two counts of one check", func() {
			// A check counts usage with the counter, and its transaction runs at READ
			// COMMITTED, where each statement takes a snapshot of its own. The window two
			// counts of one check share is therefore the statement's, not the
			// transaction's: what another request committed before a count is in it, and
			// what commits after it is not. The tenant row lock, not a frozen snapshot,
			// is what serializes the requests of one tenant, so this is the documented
			// bound the check relies on.
			parts, err := loadSharedEnforcerParts()
			Expect(err).ToNot(HaveOccurred())

			// A committed tenant holds the usage both counts read: the suite's own tenant
			// and its rows are uncommitted, so a change committed here is the only way for
			// the second count to see more than the first.
			committed(func() {
				tx, err := database.TxFromContext(ctx)
				Expect(err).ToNot(HaveOccurred())
				_, err = tx.Exec(ctx,
					`insert into tenants (id, name, tenant, creator, data)
					values ('acme', 'acme', 'acme', 'system', '{}')`)
				Expect(err).ToNot(HaveOccurred())
			})

			volumeUsage := func(usage map[string]map[string]int64) int64 {
				return usage[""]["storage.volumes"]
			}
			before, err := parts.counter.Count(ctx, "acme")
			Expect(err).ToNot(HaveOccurred())
			Expect(volumeUsage(before)).To(BeZero())

			// Another transaction commits a volume between the two counts:
			committed(func() {
				insertRow("volumes", "vol-between-counts", "acme", "",
					`{"spec":{"storage_tier":"fast","size_gib":1}}`)
			})

			after, err := parts.counter.Count(ctx, "acme")
			Expect(err).ToNot(HaveOccurred())
			Expect(volumeUsage(after)).To(Equal(int64(1)))
		})
	})

	Describe("Quota warning writes serialize with admission", func() {
		var thresholds *PrivateQuotaThresholdsServer

		BeforeEach(func() {
			var err error
			thresholds, err = NewPrivateQuotaThresholdsServer().
				SetLogger(logger).
				SetAttributionLogic(attribution).
				SetTenancyLogic(tenancy).
				Build()
			Expect(err).ToNot(HaveOccurred())
		})

		createThreshold := func(runCtx context.Context, name, tenant string) error {
			_, err := thresholds.Create(runCtx, privatev1.QuotaThresholdsCreateRequest_builder{
				Object: privatev1.QuotaThreshold_builder{
					Metadata: privatev1.Metadata_builder{Name: name, Tenant: tenant}.Build(),
					Spec: privatev1.QuotaThresholdSpec_builder{
						Dimension: "storage.fast.gib",
						Percent:   proto.Int32(50),
						Scope:     privatev1.QuotaScope_QUOTA_SCOPE_TENANT.Enum(),
					}.Build(),
				}.Build(),
			}.Build())
			return err
		}

		It("refuses a threshold create in a tenant that is being deleted", func() {
			// The write counts usage and may warn, so it takes the tenant row first; the
			// lock refuses a tenant being deleted, and the create writes nothing:
			tx, err := database.TxFromContext(ctx)
			Expect(err).ToNot(HaveOccurred())
			_, err = tx.Exec(ctx,
				`update tenants set deletion_timestamp = now() where id = $1`, testTenant)
			Expect(err).ToNot(HaveOccurred())

			err = createThreshold(ctx, "threshold-deleted", testTenant)
			Expect(grpcstatus.Code(err)).To(Equal(grpccodes.FailedPrecondition))
			Expect(grpcstatus.Convert(err).Message()).To(
				Equal("tenant '" + testTenant + "' is being deleted"))
			Expect(countRows("quota_thresholds")).To(BeZero())
		})

		It("waits for the tenant lock another transaction holds", func() {
			committed(func() {
				tx, err := database.TxFromContext(ctx)
				Expect(err).ToNot(HaveOccurred())
				_, err = tx.Exec(ctx,
					`insert into tenants (id, name, tenant, creator, data)
					values ('acme', 'acme', 'acme', 'system', '{}')`)
				Expect(err).ToNot(HaveOccurred())
				// The threshold's scope needs a limit the create's own transaction can
				// see, and the suite's seeded defaults are uncommitted:
				insertQuotaLimit("acme", "", "storage.fast.gib", 10)
			})

			// Hold acme's row in one transaction, then start the threshold create in
			// another: the create must wait for the tenant lock before it counts usage.
			txHold, err := tm.Begin(ctx)
			Expect(err).ToNot(HaveOccurred())
			DeferCleanup(func(ctx context.Context) { _ = txHold.End(ctx) })
			_, err = txHold.Exec(ctx, `select id from tenants where id = 'acme' for no key update`)
			Expect(err).ToNot(HaveOccurred())

			txCreate, err := tm.Begin(ctx)
			Expect(err).ToNot(HaveOccurred())
			_, err = txCreate.Exec(ctx, `set local lock_timeout = '10s'`)
			Expect(err).ToNot(HaveOccurred())
			var createPID int
			Expect(txCreate.QueryRow(ctx, `select pg_backend_pid()`).Scan(&createPID)).To(Succeed())

			var createErr error
			var done sync.WaitGroup
			done.Add(1)
			go func() {
				defer done.Done()
				createErr = createThreshold(
					database.TxIntoContext(ctx, txCreate), "threshold-acme", "acme")
			}()
			// Prove the create contends before the holder ends: poll on a separate
			// connection, because the suite's transaction keeps one view for its whole
			// life:
			Eventually(func() (bool, error) {
				var blocked bool
				err := testPool.QueryRow(context.Background(), `
					select exists (
						select 1 from pg_locks where pid = $1 and not granted
					)`, createPID).Scan(&blocked)
				return blocked, err
			}, 10*time.Second, 10*time.Millisecond).Should(BeTrue())

			Expect(txHold.End(ctx)).To(Succeed())
			done.Wait()
			Expect(txCreate.End(ctx)).To(Succeed())
			Expect(createErr).ToNot(HaveOccurred())
		})
	})

	Describe("Warnings", func() {
		var thresholds *QuotaThresholdsServer
		var limits *QuotaLimitsServer

		BeforeEach(func() {
			var err error
			thresholds, err = NewQuotaThresholdsServer().
				SetLogger(logger).
				SetAttributionLogic(attribution).
				SetTenancyLogic(tenancy).
				Build()
			Expect(err).ToNot(HaveOccurred())
			limits, err = NewQuotaLimitsServer().
				SetLogger(logger).
				SetAttributionLogic(attribution).
				SetTenancyLogic(tenancy).
				Build()
			Expect(err).ToNot(HaveOccurred())
			createProject("team")
			insertQuotaLimit(testTenant, "", "storage.volumes", 100)
			insertQuotaLimit(testTenant, "", "storage.fast.gib", 10)
			insertQuotaLimit(testTenant, "team", "storage.fast.gib", 10)
		})

		It("writes one outbox row when a request crosses a threshold", func() {
			insertThreshold("team", "storage.fast.gib", 20)
			Expect(createVolume("vol-1", "team", "fast", 1)).To(Succeed())
			Expect(outboxRows()).To(BeEmpty())

			Expect(createVolume("vol-2", "team", "fast", 1)).To(Succeed())
			rows := outboxRows()
			Expect(rows).To(HaveLen(1))
			Expect(rows[0]["tenant"]).To(Equal(testTenant))
			Expect(rows[0]["project"]).To(Equal("team"))
			Expect(rows[0]["dimension"]).To(Equal("storage.fast.gib"))
			Expect(rows[0]["data"]).To(MatchJSON(`{"dimension":"storage.fast.gib","percent":20,"usage":2,"limit":10}`))
		})

		It("writes one outbox row when a request crosses a tenant-scoped threshold", func() {
			// A threshold of the empty project watches the tenant-total usage, the way
			// a project's threshold watches its subtree:
			insertThreshold("", "storage.fast.gib", 30)
			Expect(createVolume("vol-1", "team", "fast", 1)).To(Succeed())
			Expect(createVolume("vol-2", "team", "fast", 1)).To(Succeed())
			Expect(outboxRows()).To(BeEmpty())

			Expect(createVolume("vol-3", "team", "fast", 1)).To(Succeed())
			rows := outboxRows()
			Expect(rows).To(HaveLen(1))
			Expect(rows[0]["tenant"]).To(Equal(testTenant))
			Expect(rows[0]["project"]).To(Equal(""))
			Expect(rows[0]["dimension"]).To(Equal("storage.fast.gib"))
			Expect(rows[0]["data"]).To(MatchJSON(`{"dimension":"storage.fast.gib","percent":30,"usage":3,"limit":10}`))
		})

		It("warns against the ancestor's limit for a project with no limit of its own", func() {
			// team.web holds the threshold and no limit of its own, so its parent's
			// limit binds it, below the tenant's own limit of ten:
			createProject("team.web")
			deleteRow("quota_limits", "limit-storage.fast.gib-team")
			insertQuotaLimit(testTenant, "team", "storage.fast.gib", 5)
			insertThreshold("team.web", "storage.fast.gib", 50)
			Expect(createVolume("vol-1", "team.web", "fast", 1)).To(Succeed())
			Expect(createVolume("vol-2", "team.web", "fast", 1)).To(Succeed())
			Expect(outboxRows()).To(BeEmpty())

			Expect(createVolume("vol-3", "team.web", "fast", 1)).To(Succeed())
			rows := outboxRows()
			Expect(rows).To(HaveLen(1))
			Expect(rows[0]["project"]).To(Equal("team.web"))
			// The warning carries the parent's limit of five, not the tenant's ten:
			Expect(rows[0]["data"]).To(MatchJSON(
				`{"dimension":"storage.fast.gib","percent":50,"usage":3,"limit":5}`))
		})

		It("writes no outbox row for a dry run that crosses a threshold", func() {
			insertThreshold("team", "storage.fast.gib", 20)
			_, err := volumes.Create(dryRunCtx(), privatev1.VolumesCreateRequest_builder{
				Object: privatev1.Volume_builder{
					Metadata: privatev1.Metadata_builder{Name: "dry", Tenant: testTenant, Project: "team"}.Build(),
					Spec: privatev1.VolumeSpec_builder{
						StorageTier: "fast",
						SizeGib:     2,
						AccessMode:  privatev1.VolumeAccessMode_VOLUME_ACCESS_MODE_READ_WRITE_ONCE,
					}.Build(),
				}.Build(),
			}.Build())
			Expect(err).To(Succeed())
			// The dry run never moved usage, so it must leave no warning behind:
			Expect(outboxRows()).To(BeEmpty())

			// The same create for real writes the crossing once:
			Expect(createVolume("vol-1", "team", "fast", 2)).To(Succeed())
			rows := outboxRows()
			Expect(rows).To(HaveLen(1))
			Expect(rows[0]["project"]).To(Equal("team"))
			Expect(rows[0]["dimension"]).To(Equal("storage.fast.gib"))
		})

		It("writes no outbox row for a dry-run threshold or limit create", func() {
			// Usage at thirty percent of the limit, so the threshold create would warn
			// the moment it saved:
			Expect(createVolume("vol-1", "team", "fast", 3)).To(Succeed())
			_, err := thresholds.Create(dryRunCtx(), publicv1.QuotaThresholdsCreateRequest_builder{
				Object: publicv1.QuotaThreshold_builder{
					Metadata: publicv1.Metadata_builder{Tenant: testTenant, Project: "team"}.Build(),
					Spec: publicv1.QuotaThresholdSpec_builder{
						Dimension: "storage.fast.gib",
						Percent:   int32Ptr(30),
						Scope:     publicv1.QuotaScope_QUOTA_SCOPE_PROJECT.Enum(),
					}.Build(),
				}.Build(),
			}.Build())
			Expect(err).To(Succeed())
			// A dry run saves nothing, so it leaves no warning for a change that never
			// happened:
			Expect(outboxRows()).To(BeEmpty())

			// A dry-run limit create that would tighten the scope below the usage
			// warns nothing either. team falls back to the tenant's limit of ten, so
			// the created five would put the usage of three above a forty-percent
			// threshold:
			deleteRow("quota_limits", "limit-storage.fast.gib-team")
			insertThreshold("team", "storage.fast.gib", 40)
			_, err = limits.Create(dryRunCtx(), publicv1.QuotaLimitsCreateRequest_builder{
				Object: publicv1.QuotaLimit_builder{
					Metadata: publicv1.Metadata_builder{Tenant: testTenant, Project: "team"}.Build(),
					Spec:     publicv1.QuotaLimitSpec_builder{Dimension: "storage.fast.gib", Value: proto.Int64(5), Scope: publicv1.QuotaScope_QUOTA_SCOPE_PROJECT.Enum()}.Build(),
				}.Build(),
			}.Build())
			Expect(err).To(Succeed())
			Expect(outboxRows()).To(BeEmpty())
		})

		It("writes no warning when the usage stays below the threshold", func() {
			insertThreshold("team", "storage.fast.gib", 50)
			Expect(createVolume("vol-1", "team", "fast", 1)).To(Succeed())
			Expect(createVolume("vol-2", "team", "fast", 1)).To(Succeed())
			Expect(outboxRows()).To(BeEmpty())
		})

		It("writes a warning when a threshold change puts the project at or above it", func() {
			Expect(createVolume("vol-1", "team", "fast", 3)).To(Succeed())
			_, err := thresholds.Create(ctx, publicv1.QuotaThresholdsCreateRequest_builder{
				Object: publicv1.QuotaThreshold_builder{
					Metadata: publicv1.Metadata_builder{Tenant: testTenant, Project: "team"}.Build(),
					Spec: publicv1.QuotaThresholdSpec_builder{
						Dimension: "storage.fast.gib",
						Percent:   int32Ptr(30),
						Scope:     publicv1.QuotaScope_QUOTA_SCOPE_PROJECT.Enum(),
					}.Build(),
				}.Build(),
			}.Build())
			Expect(err).ToNot(HaveOccurred())
			rows := outboxRows()
			Expect(rows).To(HaveLen(1))
			Expect(rows[0]["dimension"]).To(Equal("storage.fast.gib"))
		})

		// createLimit makes a limit of the suite's dimension through the server: a
		// project limit when project names one, a tenant override when it is empty.
		createLimit := func(project string, value int64) *publicv1.QuotaLimit {
			scope := publicv1.QuotaScope_QUOTA_SCOPE_PROJECT.Enum()
			if project == "" {
				scope = publicv1.QuotaScope_QUOTA_SCOPE_TENANT.Enum()
			}
			response, createErr := limits.Create(ctx, publicv1.QuotaLimitsCreateRequest_builder{
				Object: publicv1.QuotaLimit_builder{
					Metadata: publicv1.Metadata_builder{Tenant: testTenant, Project: project}.Build(),
					Spec:     publicv1.QuotaLimitSpec_builder{Dimension: "storage.fast.gib", Value: proto.Int64(value), Scope: scope}.Build(),
				}.Build(),
			}.Build())
			ExpectWithOffset(1, createErr).ToNot(HaveOccurred())
			return response.GetObject()
		}

		updateLimit := func(limit *publicv1.QuotaLimit, value int64) error {
			scope := publicv1.QuotaScope_QUOTA_SCOPE_PROJECT.Enum()
			if limit.GetMetadata().GetProject() == "" {
				scope = publicv1.QuotaScope_QUOTA_SCOPE_TENANT.Enum()
			}
			_, updateErr := limits.Update(ctx, publicv1.QuotaLimitsUpdateRequest_builder{
				Object: publicv1.QuotaLimit_builder{
					Id:       limit.GetId(),
					Metadata: limit.GetMetadata(),
					Spec:     publicv1.QuotaLimitSpec_builder{Dimension: "storage.fast.gib", Value: proto.Int64(value), Scope: scope}.Build(),
				}.Build(),
			}.Build())
			return updateErr
		}

		It("writes no warning when a limit change raises the limit", func() {
			// Replace the raw-seeded team limit with a server-created one, so the update
			// below passes name validation:
			deleteRow("quota_limits", "limit-storage.fast.gib-team")
			limit := createLimit("team", 10)
			insertThreshold("team", "storage.fast.gib", 20)
			Expect(createVolume("vol-1", "team", "fast", 3)).To(Succeed())
			Expect(outboxRows()).To(HaveLen(1))

			Expect(updateLimit(limit, 20)).To(Succeed())
			Expect(outboxRows()).To(HaveLen(1))
		})

		It("writes a warning when a limit change lowers the limit", func() {
			deleteRow("quota_limits", "limit-storage.fast.gib-team")
			limit := createLimit("team", 10)
			insertThreshold("team", "storage.fast.gib", 20)
			Expect(createVolume("vol-1", "team", "fast", 3)).To(Succeed())
			Expect(outboxRows()).To(HaveLen(1))

			Expect(updateLimit(limit, 5)).To(Succeed())
			rows := outboxRows()
			Expect(rows).To(HaveLen(2))
			payloads := []any{}
			for _, row := range rows {
				payloads = append(payloads, row["data"])
			}
			Expect(payloads).To(ContainElement(
				MatchJSON(`{"dimension":"storage.fast.gib","percent":20,"usage":3,"limit":5}`)))
		})

		It("writes a warning when a created limit is tighter than the usage it applies to", func() {
			// The volume lands while the project falls back to the tenant's limit of
			// ten, so nothing warns yet:
			deleteRow("quota_limits", "limit-storage.fast.gib-team")
			Expect(createVolume("vol-1", "team", "fast", 3)).To(Succeed())
			insertThreshold("team", "storage.fast.gib", 40)
			Expect(outboxRows()).To(BeEmpty())

			// A created project limit below the fallback tightens the scope:
			createLimit("team", 5)
			rows := outboxRows()
			Expect(rows).To(HaveLen(1))
			Expect(rows[0]["data"]).To(MatchJSON(
				`{"dimension":"storage.fast.gib","percent":40,"usage":3,"limit":5}`))
		})

		It("writes a warning when a lowered tenant override puts descendant projects at or above their thresholds", func() {
			// team.web carries the threshold and no limit of its own, so the tenant's
			// override binds it:
			createProject("team.web")
			deleteRow("quota_limits", "limit-storage.fast.gib-team")
			// Replace the raw-seeded override with a server-created one, so the update
			// below passes name validation. Nothing warns on the way in: no threshold
			// exists yet:
			deleteRow("quota_limits", "limit-storage.fast.gib-")
			override := createLimit("", 10)
			Expect(createVolume("vol-1", "team.web", "fast", 3)).To(Succeed())
			insertThreshold("team.web", "storage.fast.gib", 40)

			Expect(updateLimit(override, 5)).To(Succeed())

			rows := outboxRows()
			Expect(rows).To(HaveLen(1))
			Expect(rows[0]["project"]).To(Equal("team.web"))
			Expect(rows[0]["data"]).To(MatchJSON(
				`{"dimension":"storage.fast.gib","percent":40,"usage":3,"limit":5}`))
		})

		It("writes a warning when a limit request approval tightens the approved scope", func() {
			requests, err := NewQuotaLimitRequestsServer().
				SetLogger(logger).
				SetAttributionLogic(attribution).
				SetTenancyLogic(tenancy).
				Build()
			Expect(err).ToNot(HaveOccurred())

			// The tenant's override of ten is what the approval lowers, and team holds
			// no limit of its own, so the approved five is what binds it:
			deleteRow("quota_limits", "limit-storage.fast.gib-team")
			Expect(createVolume("vol-1", "team", "fast", 3)).To(Succeed())
			insertThreshold("team", "storage.fast.gib", 40)

			created, err := requests.Create(ctx, publicv1.QuotaLimitRequestsCreateRequest_builder{
				Object: publicv1.QuotaLimitRequest_builder{
					Metadata: publicv1.Metadata_builder{Tenant: testTenant}.Build(),
					Spec: publicv1.QuotaLimitRequestSpec_builder{
						Dimension: "storage.fast.gib", RequestedValue: proto.Int64(5),
					}.Build(),
				}.Build(),
			}.Build())
			Expect(err).ToNot(HaveOccurred())
			_, err = requests.Update(ctx, publicv1.QuotaLimitRequestsUpdateRequest_builder{
				Object: publicv1.QuotaLimitRequest_builder{
					Id: created.GetObject().GetId(),
					Status: publicv1.QuotaLimitRequestStatus_builder{
						State: publicv1.QuotaLimitRequestState_QUOTA_LIMIT_REQUEST_STATE_APPROVED,
					}.Build(),
				}.Build(),
				UpdateMask: &fieldmaskpb.FieldMask{Paths: []string{"status.state"}},
			}.Build())
			Expect(err).ToNot(HaveOccurred())

			rows := outboxRows()
			Expect(rows).To(HaveLen(1))
			Expect(rows[0]["project"]).To(Equal("team"))
			Expect(rows[0]["data"]).To(MatchJSON(
				`{"dimension":"storage.fast.gib","percent":40,"usage":3,"limit":5}`))
		})

		It("writes a warning when a threshold update lowers it below the usage", func() {
			// A threshold above the usage warns nothing on the way in:
			created, err := thresholds.Create(ctx, publicv1.QuotaThresholdsCreateRequest_builder{
				Object: publicv1.QuotaThreshold_builder{
					Metadata: publicv1.Metadata_builder{Tenant: testTenant, Project: "team"}.Build(),
					Spec:     publicv1.QuotaThresholdSpec_builder{Dimension: "storage.fast.gib", Percent: int32Ptr(50), Scope: publicv1.QuotaScope_QUOTA_SCOPE_PROJECT.Enum()}.Build(),
				}.Build(),
			}.Build())
			Expect(err).ToNot(HaveOccurred())
			Expect(createVolume("vol-1", "team", "fast", 3)).To(Succeed())
			Expect(outboxRows()).To(BeEmpty())

			_, err = thresholds.Update(ctx, publicv1.QuotaThresholdsUpdateRequest_builder{
				Object: publicv1.QuotaThreshold_builder{
					Id:       created.GetObject().GetId(),
					Metadata: created.GetObject().GetMetadata(),
					Spec:     publicv1.QuotaThresholdSpec_builder{Dimension: "storage.fast.gib", Percent: int32Ptr(20), Scope: publicv1.QuotaScope_QUOTA_SCOPE_PROJECT.Enum()}.Build(),
				}.Build(),
			}.Build())
			Expect(err).ToNot(HaveOccurred())

			rows := outboxRows()
			Expect(rows).To(HaveLen(1))
			Expect(rows[0]["data"]).To(MatchJSON(
				`{"dimension":"storage.fast.gib","percent":20,"usage":3,"limit":10}`))
		})

		It("writes no warning when a threshold is deleted", func() {
			created, err := thresholds.Create(ctx, publicv1.QuotaThresholdsCreateRequest_builder{
				Object: publicv1.QuotaThreshold_builder{
					Metadata: publicv1.Metadata_builder{Tenant: testTenant, Project: "team"}.Build(),
					Spec:     publicv1.QuotaThresholdSpec_builder{Dimension: "storage.fast.gib", Percent: int32Ptr(20), Scope: publicv1.QuotaScope_QUOTA_SCOPE_PROJECT.Enum()}.Build(),
				}.Build(),
			}.Build())
			Expect(err).ToNot(HaveOccurred())
			Expect(createVolume("vol-1", "team", "fast", 3)).To(Succeed())
			Expect(outboxRows()).To(HaveLen(1))

			_, err = thresholds.Delete(ctx, publicv1.QuotaThresholdsDeleteRequest_builder{
				Id: created.GetObject().GetId(),
			}.Build())
			Expect(err).ToNot(HaveOccurred())
			Expect(outboxRows()).To(HaveLen(1))
		})

		It("rolls a limit create back when its warning fails to land", func() {
			// Fail the outbox insert of this transaction only, with a real database trigger
			// gated by a session setting, the way the metering suites force failures:
			_, err := testPool.Exec(ctx, `create function fail_quota_outbox_insert() returns trigger language plpgsql as $$
			begin
				if current_setting('osac.fail_quota_outbox', true) = 'on' then
					raise exception 'test outbox failure';
				end if;
				return new;
			end $$;
			create trigger fail_quota_outbox_insert before insert on quota_outbox
			for each row execute function fail_quota_outbox_insert()`)
			Expect(err).ToNot(HaveOccurred())

			// Usage above the threshold, so a limit that tightens the scope warns:
			insertThreshold("team", "storage.fast.gib", 20)
			Expect(createVolume("vol-1", "team", "fast", 3)).To(Succeed())
			Expect(outboxRows()).To(HaveLen(1))

			// Fail every later outbox insert of the suite's transaction:
			_, err = suiteTx.Exec(ctx, `select set_config('osac.fail_quota_outbox', 'on', true)`)
			Expect(err).ToNot(HaveOccurred())

			deleteRow("quota_limits", "limit-storage.fast.gib-team")
			_, err = limits.Create(ctx, publicv1.QuotaLimitsCreateRequest_builder{
				Object: publicv1.QuotaLimit_builder{
					Metadata: publicv1.Metadata_builder{Tenant: testTenant, Project: "team"}.Build(),
					Spec:     publicv1.QuotaLimitSpec_builder{Dimension: "storage.fast.gib", Value: proto.Int64(5), Scope: publicv1.QuotaScope_QUOTA_SCOPE_PROJECT.Enum()}.Build(),
				}.Build(),
			}.Build())
			Expect(grpcstatus.Code(err)).To(Equal(grpccodes.Internal))
			Expect(grpcstatus.Convert(err).Message()).To(
				ContainSubstring("failed to write the quota warnings"))

			// The failed warning is reported to the transaction, so ending it rolls the
			// limit back instead of committing it:
			didCommit := false
			suiteTx.OnCommit(func() { didCommit = true })
			Expect(suiteTx.End(ctx)).To(Succeed())
			Expect(didCommit).To(BeFalse(), "the failed warning must roll the limit create back")

			// A fresh transaction sees nothing of the rolled-back create:
			verifyTx, verifyErr := tm.Begin(context.Background())
			Expect(verifyErr).ToNot(HaveOccurred())
			verifyCtx := database.TxIntoContext(context.Background(), verifyTx)
			var count int
			Expect(verifyTx.QueryRow(verifyCtx, `select count(*) from quota_limits`).Scan(&count)).To(Succeed())
			Expect(count).To(BeZero())
			Expect(verifyTx.End(verifyCtx)).To(Succeed())

			// Continue the spec on a fresh transaction:
			suiteTx, err = tm.Begin(context.Background())
			Expect(err).ToNot(HaveOccurred())
			ctx = database.TxIntoContext(ctx, suiteTx)
		})

		It("rolls a threshold create back when its warning fails to land", func() {
			// Fail the outbox insert of this transaction only, with a real database trigger
			// gated by a session setting, the way the metering suites force failures:
			_, err := testPool.Exec(ctx, `create function fail_quota_outbox_insert() returns trigger language plpgsql as $$
			begin
				if current_setting('osac.fail_quota_outbox', true) = 'on' then
					raise exception 'test outbox failure';
				end if;
				return new;
			end $$;
			create trigger fail_quota_outbox_insert before insert on quota_outbox
			for each row execute function fail_quota_outbox_insert()`)
			Expect(err).ToNot(HaveOccurred())

			// Usage at thirty percent of the limit, so creating the threshold warns:
			Expect(createVolume("vol-1", "team", "fast", 3)).To(Succeed())
			Expect(outboxRows()).To(BeEmpty())

			// Fail every later outbox insert of the suite's transaction:
			_, err = suiteTx.Exec(ctx, `select set_config('osac.fail_quota_outbox', 'on', true)`)
			Expect(err).ToNot(HaveOccurred())

			_, err = thresholds.Create(ctx, publicv1.QuotaThresholdsCreateRequest_builder{
				Object: publicv1.QuotaThreshold_builder{
					Metadata: publicv1.Metadata_builder{Tenant: testTenant, Project: "team"}.Build(),
					Spec: publicv1.QuotaThresholdSpec_builder{
						Dimension: "storage.fast.gib",
						Percent:   int32Ptr(30),
						Scope:     publicv1.QuotaScope_QUOTA_SCOPE_PROJECT.Enum(),
					}.Build(),
				}.Build(),
			}.Build())
			Expect(grpcstatus.Code(err)).To(Equal(grpccodes.Internal))
			Expect(grpcstatus.Convert(err).Message()).To(
				ContainSubstring("failed to write the quota warning"))

			// The failed warning is reported to the transaction, so ending it rolls the
			// threshold back instead of committing it:
			didCommit := false
			suiteTx.OnCommit(func() { didCommit = true })
			Expect(suiteTx.End(ctx)).To(Succeed())
			Expect(didCommit).To(BeFalse(), "the failed warning must roll the threshold create back")

			// A fresh transaction sees nothing of the rolled-back create:
			verifyTx, verifyErr := tm.Begin(context.Background())
			Expect(verifyErr).ToNot(HaveOccurred())
			verifyCtx := database.TxIntoContext(context.Background(), verifyTx)
			var count int
			Expect(verifyTx.QueryRow(verifyCtx, `select count(*) from quota_thresholds`).Scan(&count)).To(Succeed())
			Expect(count).To(BeZero())
			Expect(verifyTx.End(verifyCtx)).To(Succeed())

			// Continue the spec on a fresh transaction:
			suiteTx, err = tm.Begin(context.Background())
			Expect(err).ToNot(HaveOccurred())
			ctx = database.TxIntoContext(ctx, suiteTx)
		})
	})

	Describe("Default limit changes", func() {
		var scanner *QuotaDefaultLimitScanner

		BeforeEach(func() {
			var err error
			scanner, err = NewQuotaDefaultLimitScanner().
				SetLogger(logger).
				SetTxManager(tm).
				Build()
			Expect(err).ToNot(HaveOccurred())
			DeferCleanup(scanner.Close)
		})

		// seedTightenedDefault commits the world a lowered default tightens: the tenant
		// acme, its project team, a default and a team limit of ten, a threshold of two
		// and a volume of three above it. The scan runs in its own transactions after
		// the change commits, so everything it reads must be committed first.
		seedTightenedDefault := func() {
			committed(func() {
				tx, err := database.TxFromContext(ctx)
				Expect(err).ToNot(HaveOccurred())
				_, err = tx.Exec(ctx,
					`insert into tenants (id, name, tenant, creator, data)
					values ('acme', 'acme', 'acme', 'system', '{}')`)
				Expect(err).ToNot(HaveOccurred())
				_, err = tx.Exec(ctx,
					`insert into projects (id, tenant, project, name, creator, data)
					values (uuidv7(), 'acme', ''::ltree, 'team'::ltree, 'system', '{}')`)
				Expect(err).ToNot(HaveOccurred())
				insertDefaultLimit("storage.slow.gib", 10)
				insertQuotaLimit("acme", "team", "storage.slow.gib", 10)
				insertRow("quota_thresholds", "threshold", "acme", "team",
					`{"spec":{"dimension":"storage.slow.gib","percent":20}}`)
				insertRow("volumes", "vol", "acme", "team",
					`{"spec":{"storage_tier":"slow","size_gib":3}}`)
			})
		}

		// lowerDefault commits a default change of the dimension in its own transaction,
		// the way the deployed server does, so the after-commit scan can run. The default
		// exists already: seedTightenedDefault inserted it with the id the servers derive.
		lowerDefault := func(value int64) {
			tx, err := tm.Begin(ctx)
			Expect(err).ToNot(HaveOccurred())
			managerCtx := database.TxManagerIntoContext(ctx, tm)
			setCtx := database.TxIntoContext(managerCtx, tx)
			defaults, err := NewPrivateQuotaDefaultLimitsServer().
				SetLogger(logger).
				SetAttributionLogic(attribution).
				SetTenancyLogic(tenancy).
				SetScanner(scanner).
				Build()
			Expect(err).ToNot(HaveOccurred())
			_, err = defaults.Update(setCtx, privatev1.QuotaDefaultLimitsUpdateRequest_builder{
				Object: privatev1.QuotaDefaultLimit_builder{
					Id: "default-storage.slow.gib",
					Metadata: privatev1.Metadata_builder{
						Tenant: auth.SharedTenant,
						Name:   quotaNameFromDimension("storage.slow.gib"),
					}.Build(),
					Spec: privatev1.QuotaDefaultLimitSpec_builder{
						Dimension: "storage.slow.gib",
						Value:     proto.Int64(value),
					}.Build(),
				}.Build(),
			}.Build())
			Expect(err).ToNot(HaveOccurred())
			Expect(tx.End(ctx)).To(Succeed())
		}

		It("warns, after the change commits, the tenants a lowered default tightens", func() {
			seedTightenedDefault()
			lowerDefault(1)

			// The scan runs after the commit, detached from the request, so its warning
			// appears once it finishes:
			var rows []map[string]any
			Eventually(func(g Gomega) {
				rows = outboxRows()
				g.Expect(rows).To(HaveLen(1))
			}).WithTimeout(10 * time.Second).Should(Succeed())
			Expect(rows[0]["tenant"]).To(Equal("acme"))
			Expect(rows[0]["project"]).To(Equal("team"))
			// The lowered default of one is now the tightest bound of team, below its
			// own limit of ten, the way the usage report resolves the project's limit:
			Expect(rows[0]["data"]).To(MatchJSON(
				`{"dimension":"storage.slow.gib","percent":20,"usage":3,"limit":1}`))
		})

		It("writes no warning for a tenant whose override keeps its limit", func() {
			seedTightenedDefault()
			// acme's own override of ten survives the lowered default, so the change
			// tightens nothing the tenant is held to:
			committed(func() {
				insertQuotaLimit("acme", "", "storage.slow.gib", 10)
			})
			lowerDefault(1)

			// The scan runs within milliseconds of the commit; two seconds of silence
			// cover it:
			Consistently(func() int {
				return len(outboxRows())
			}).WithTimeout(2 * time.Second).Should(BeZero())
		})

		// seedDrainingTenant seeds a tenant whose usage sits above a low default's threshold,
		// with the default and the usage committed so a scan's own transactions see them.
		seedDrainingTenant := func(tenant string) {
			tx, err := database.TxFromContext(ctx)
			Expect(err).ToNot(HaveOccurred())
			_, err = tx.Exec(ctx,
				`insert into tenants (id, name, tenant, creator, data)
				values ($1, $1, $1, 'system', '{}')
				on conflict (id) do nothing`, tenant)
			Expect(err).ToNot(HaveOccurred())
			_, err = tx.Exec(ctx,
				`insert into projects (id, tenant, project, name, creator, data)
				values (uuidv7(), $1, ''::ltree, 'team'::ltree, 'system', '{}')
				on conflict do nothing`, tenant)
			Expect(err).ToNot(HaveOccurred())
			insertRow("quota_thresholds", "threshold-"+tenant, tenant, "team",
				`{"spec":{"dimension":"storage.slow.gib","percent":20}}`)
			insertRow("volumes", "vol-"+tenant, tenant, "team",
				`{"spec":{"storage_tier":"slow","size_gib":3}}`)
		}

		It("coalesces two rapid changes of one dimension into a single scan", func() {
			// The final default of one is committed before the scan, and two changes are
			// queued: one from twenty and one from ten. Only the newer one may remain, and
			// the worker may walk the dimension once: a walk per change would write the
			// warning twice.
			committed(func() {
				seedDrainingTenant("acme")
				insertNamedRow("quota_default_limits", "default-storage.slow.gib",
					quotaNameFromDimension("storage.slow.gib"), auth.SharedTenant, "",
					`{"spec":{"dimension":"storage.slow.gib","value":"1"}}`)
			})

			enforcer, err := newQuotaEnforcer(logger, nil)
			Expect(err).ToNot(HaveOccurred())
			manual := newQuotaDefaultLimitScanner(logger, tm)
			manual.schedule(enforcer, "storage.slow.gib", 20)
			manual.schedule(enforcer, "storage.slow.gib", 10)
			manual.drain()

			rows := outboxRows()
			Expect(rows).To(HaveLen(1))
			Expect(rows[0]["tenant"]).To(Equal("acme"))
			Expect(rows[0]["data"]).To(MatchJSON(
				`{"dimension":"storage.slow.gib","percent":20,"usage":3,"limit":1}`))
		})

		It("keeps walking when a tenant is deleted mid-scan", func() {
			// The tenant list is read once, before the walk, and a listed tenant is deleted
			// before the walk reaches it: the deleted tenant warns nothing, and the walk
			// still reaches the next tenant.
			committed(func() {
				seedDrainingTenant("acme")
				// The threshold measures against the tenant's limit, and the scan warns
				// only for a default it lowers:
				insertDefaultLimit("storage.slow.gib", 10)
				tx, err := database.TxFromContext(ctx)
				Expect(err).ToNot(HaveOccurred())
				_, err = tx.Exec(ctx,
					`insert into tenants (id, name, tenant, creator, data)
					values ('gone', 'gone', 'gone', 'system', '{}')`)
				Expect(err).ToNot(HaveOccurred())
			})
			enforcer, err := newQuotaEnforcer(logger, nil)
			Expect(err).ToNot(HaveOccurred())

			// Read the list the way a scan does, then delete one listed tenant:
			listTx, err := tm.Begin(ctx)
			Expect(err).ToNot(HaveOccurred())
			listCtx := database.TxIntoContext(ctx, listTx)
			tenants, listErr := enforcer.listTenants(listCtx)
			Expect(listErr).ToNot(HaveOccurred())
			Expect(listTx.End(ctx)).To(Succeed())
			Expect(tenants).To(ContainElement("gone"))
			committed(func() {
				tx, err := database.TxFromContext(ctx)
				Expect(err).ToNot(HaveOccurred())
				// The tenant insert materializes the tenant's default project, so the
				// hard delete removes the project first:
				_, err = tx.Exec(ctx, `delete from projects where tenant = 'gone'`)
				Expect(err).ToNot(HaveOccurred())
				_, err = tx.Exec(ctx, `delete from tenants where id = 'gone'`)
				Expect(err).ToNot(HaveOccurred())
			})

			manual := newQuotaDefaultLimitScanner(logger, tm)
			manual.scanTenants([]string{"gone", "acme"}, "storage.slow.gib",
				pendingDefaultScan{enforcer: enforcer, oldDefault: 20})

			rows := outboxRows()
			Expect(rows).To(HaveLen(1))
			Expect(rows[0]["tenant"]).To(Equal("acme"))
		})

		It("keeps walking when one tenant's scan fails", func() {
			// A tenant whose stored limit value isn't a number fails its own read. The
			// failure is logged and skipped, and the next tenant's warning still lands:
			committed(func() {
				seedDrainingTenant("acme")
				// The threshold measures against the tenant's limit, and the scan warns
				// only for a default it lowers:
				insertDefaultLimit("storage.slow.gib", 10)
				tx, err := database.TxFromContext(ctx)
				Expect(err).ToNot(HaveOccurred())
				_, err = tx.Exec(ctx,
					`insert into tenants (id, name, tenant, creator, data)
					values ('bad', 'bad', 'bad', 'system', '{}')`)
				Expect(err).ToNot(HaveOccurred())
				insertNamedRow("quota_limits", "bad-limit", "bad-limit", "bad", "",
					`{"spec":{"dimension":"storage.slow.gib","value":"oops"}}`)
			})
			enforcer, err := newQuotaEnforcer(logger, nil)
			Expect(err).ToNot(HaveOccurred())

			manual := newQuotaDefaultLimitScanner(logger, tm)
			manual.scanTenants([]string{"bad", "acme"}, "storage.slow.gib",
				pendingDefaultScan{enforcer: enforcer, oldDefault: 20})

			rows := outboxRows()
			Expect(rows).To(HaveLen(1))
			Expect(rows[0]["tenant"]).To(Equal("acme"))
		})
	})

	Describe("Metrics", func() {
		It("exposes the four quota metrics", func() {
			metricsServer := osactesting.NewMetricsServer()
			DeferCleanup(metricsServer.Close)

			metricVolumes, err := NewPrivateVolumesServer().
				SetLogger(logger).
				SetAttributionLogic(attribution).
				SetTenancyLogic(tenancy).
				SetTierResolver(quotaTierResolver).
				SetMetricsRegisterer(metricsServer.Registry()).
				Build()
			Expect(err).ToNot(HaveOccurred())

			createProject("team")
			insertQuotaLimit(testTenant, "", "storage.volumes", 100)
			insertQuotaLimit(testTenant, "team", "storage.fast.gib", 10)
			insertThreshold("team", "storage.fast.gib", 20)

			metricVolume := func(name string, sizeGib int64) error {
				_, err := metricVolumes.Create(ctx, privatev1.VolumesCreateRequest_builder{
					Object: privatev1.Volume_builder{
						Metadata: privatev1.Metadata_builder{Name: name, Tenant: testTenant, Project: "team"}.Build(),
						Spec: privatev1.VolumeSpec_builder{
							StorageTier: "fast",
							SizeGib:     sizeGib,
							AccessMode:  privatev1.VolumeAccessMode_VOLUME_ACCESS_MODE_READ_WRITE_ONCE,
						}.Build(),
					}.Build(),
				}.Build())
				return err
			}

			// One fitting create that crosses the threshold, one refused check:
			Expect(metricVolume("vol-1", 2)).To(Succeed())
			quotaFailure(metricVolume("vol-2", 20))

			// The refused request counts under its dimension, and the crossing wrote one
			// warning to the outbox. testutil reads the counters:
			Expect(outboxRows()).To(HaveLen(1))
			quotaMetrics := metricVolumes.generic.quota.metrics
			Expect(testutil.ToFloat64(quotaMetrics.refused.WithLabelValues("storage.fast.gib"))).
				To(BeNumerically(">=", 1))
			Expect(testutil.ToFloat64(quotaMetrics.warningsWritten)).To(BeNumerically(">=", 1))

			// A checked create records the check's duration and its wait for the tenant
			// lock. The sample count is the number of observations, which testutil doesn't
			// read for a histogram, so it comes from the exposition:
			metrics := metricsServer.Metrics()
			Expect(metrics).To(osactesting.MatchLine(`^quota_check_duration_seconds_count [1-9][0-9]*$`))
			Expect(metrics).To(osactesting.MatchLine(`^quota_tenant_lock_wait_seconds_count [1-9][0-9]*$`))
		})
	})
})

/*
Copyright (c) 2026 Red Hat Inc.

Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except in compliance with
the License. You may obtain a copy of the License at

  http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
specific language governing permissions and limitations under the License.
*/

package quotausage

import (
	"context"
	"log/slog"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/osac-project/osac/fulfillment-service/internal/database"
	"github.com/osac-project/osac/fulfillment-service/internal/logging"
)

func TestQuotaUsage(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "Quota usage package")
}

var (
	logger *slog.Logger
	server *database.Container
)

var _ = BeforeSuite(func(ctx context.Context) {
	var err error

	logger, err = logging.NewLogger().
		SetLevel(slog.LevelDebug.String()).
		SetWriter(GinkgoWriter).
		Build()
	Expect(err).ToNot(HaveOccurred())

	server, err = database.NewContainer().
		SetLogger(logger).
		Build()
	Expect(err).ToNot(HaveOccurred())
	DeferCleanup(func(ctx context.Context) {
		err = server.Stop(ctx)
		Expect(err).ToNot(HaveOccurred())
	})
	err = server.Start(ctx)
	Expect(err).ToNot(HaveOccurred())
})

// usageDatabase is a fresh database with the tenants, projects and resources each spec needs.
type usageDatabase struct {
	pool *pgxpool.Pool
	tx   database.Tx
	ctx  context.Context
}

func newUsageDatabase(ctx context.Context) *usageDatabase {
	db, err := server.NewInstance().Build()
	Expect(err).ToNot(HaveOccurred())
	DeferCleanup(db.Close)
	pool, err := db.Pool(ctx)
	Expect(err).ToNot(HaveOccurred())
	DeferCleanup(pool.Close)
	tm, err := database.NewTxManager().
		SetLogger(logger).
		SetPool(pool).
		Build()
	Expect(err).ToNot(HaveOccurred())
	tx, err := tm.Begin(ctx)
	Expect(err).ToNot(HaveOccurred())
	DeferCleanup(func() {
		Expect(tx.End(ctx)).To(Succeed())
	})
	result := &usageDatabase{pool: pool, tx: tx, ctx: database.TxIntoContext(ctx, tx)}
	result.exec(`insert into tenants (id, name, tenant, creator, data)
		values ('acme', 'acme', 'acme', 'system', '{}'), ('other', 'other', 'other', 'system', '{}')`)
	result.exec(`insert into projects (id, tenant, project, name, creator, data)
		values (uuidv7(), 'acme', ''::ltree, 'team'::ltree, 'system', '{}')`)
	return result
}

func (d *usageDatabase) exec(sql string, args ...any) {
	_, err := d.tx.Exec(d.ctx, sql, args...)
	Expect(err).ToNot(HaveOccurred())
}

// insertObject inserts one row of an object table with the standard columns.
func (d *usageDatabase) insertObject(table, id, tenant, project, creator string, labels, data string) {
	d.exec(
		`insert into `+table+` (id, name, tenant, project, creator, labels, data)
		 values ($1, $1, $2, $3::ltree, $4, $5::jsonb, $6::jsonb)`,
		id, tenant, project, creator, labels, data,
	)
}

var _ = Describe("Usage counter", func() {
	var (
		db      *usageDatabase
		counter *Counter
	)

	BeforeEach(func() {
		db = newUsageDatabase(context.Background())
		var err error
		counter, err = NewCounter().
			SetLogger(logger).
			Build()
		Expect(err).ToNot(HaveOccurred())

		// Types are platform-scoped:
		db.insertObject("instance_types", "it-large", "shared", "", "system", "{}",
			`{"spec": {"vcpus": 6, "memory_gib": 24}}`)
		db.insertObject("instance_types", "it-small", "shared", "", "system", "{}",
			`{"spec": {"vcpus": 2, "memory_gib": 8}}`)
		db.insertObject("instance_types", "it-gpu", "shared", "", "system", "{}",
			`{"spec": {"vcpus": 8, "memory_gib": 32, "gpu": {"resource_name": "nvidia.com/A100", "count": 4}}}`)
		db.insertObject("bare_metal_instance_types", "metal-1tb", "shared", "", "system", "{}", `{}`)

		// A VM mid-resize: it asks for large but still runs small, so it counts as large:
		db.insertObject("compute_instances", "vm-1", "acme", "", "user", "{}", `{
			"spec": {
				"instance_type": {"name": "it-large"},
				"boot_disk": {"size_gib": 100, "storage_tier": {"name": "fast"}},
				"additional_disks": {"items": [{"size_gib": 50, "storage_tier": {"name": "slow"}}]},
				"auto_external_ip_attachment": true
			},
			"status": {"instance_type": {"name": "it-small"}}
		}`)
		// A plain VM:
		db.insertObject("compute_instances", "vm-2", "acme", "", "user", "{}", `{
			"spec": {"instance_type": {"name": "it-small"}}
		}`)
		// A GPU VM in the team project:
		db.insertObject("compute_instances", "vm-3", "acme", "team", "user", "{}", `{
			"spec": {"instance_type": {"name": "it-gpu"}}
		}`)
		// Another tenant's VM, which must not count for acme:
		db.insertObject("compute_instances", "vm-other", "other", "", "user", "{}", `{
			"spec": {"instance_type": {"name": "it-large"}}
		}`)

		// The boot disk OSAC created for vm-1, and a standalone volume:
		db.insertObject("volumes", "ci-vm-1-boot", "acme", "", "system", "{}",
			`{"spec": {"size_gib": 100, "storage_tier": "fast"}}`)
		db.insertObject("volumes", "data-vol", "acme", "", "user", "{}",
			`{"spec": {"size_gib": 200, "storage_tier": "fast"}}`)

		// One automatic address for vm-1 and one manual one:
		db.insertObject("external_ips", "ip-auto", "acme", "", "system",
			`{"osac.openshift.io/auto-created-for": "vm-1", "osac.openshift.io/auto-created-kind": "ComputeInstance"}`,
			`{}`)
		db.insertObject("external_ips", "ip-manual", "acme", "", "user", "{}", `{}`)

		// A cluster that asks for 3 hosts and has 2, and asks for 2 addresses and has 1:
		db.insertObject("clusters", "prod", "acme", "team", "user", "{}", `{
			"spec": {
				"node_sets": {"compute": {"size": 3, "baremetal_instance_type": {"name": "metal-1tb"}}},
				"auto_external_ip_attachment": true
			}
		}`)
		db.insertObject("bare_metal_instances", "host-1", "acme", "team", "system", "{}", `{
			"spec": {"instance_type": {"name": "metal-1tb"}},
			"status": {"cluster": {"id": "prod"}}
		}`)
		db.insertObject("bare_metal_instances", "host-2", "acme", "team", "system", "{}", `{
			"spec": {"instance_type": {"name": "metal-1tb"}},
			"status": {"cluster": {"id": "prod"}}
		}`)
		db.insertObject("external_ips", "ip-cluster", "acme", "team", "system",
			`{"osac.openshift.io/auto-created-for": "prod", "osac.openshift.io/auto-created-kind": "Cluster"}`,
			`{}`)

		db.insertObject("nat_gateways", "ng-1", "acme", "team", "user", "{}", `{}`)
		db.insertObject("virtual_networks", "vn-1", "acme", "team", "user", "{}", `{}`)
		db.insertObject("disk_images", "image-1", "acme", "", "user", "{}", `{}`)
	})

	It("counts each dimension per project, priced by the registry", func(ctx context.Context) {
		usage, err := counter.Count(db.ctx, "acme")
		Expect(err).ToNot(HaveOccurred())

		defaultProject := usage[""]
		Expect(defaultProject).To(Equal(map[string]int64{
			"compute.vcpus":        8, // vm-1 as large (6) + vm-2 (2)
			"compute.memory_gib":   32,
			"storage.volumes":      3, // the created boot disk, the standalone volume, and vm-1's disk beyond them
			"storage.fast.gib":     300,
			"storage.slow.gib":     50, // vm-1 asks for a disk OSAC hasn't created
			"network.external_ips": 2,  // the automatic and the manual address
			"images.count":         1,
		}))
		teamProject := usage["team"]
		Expect(teamProject).To(Equal(map[string]int64{
			"compute.vcpus":                  8,
			"compute.memory_gib":             32,
			"compute.gpu.nvidia.com/A100":    4,
			"caas.control_planes":            1,
			"bare_metal.instances.metal-1tb": 3, // the 2 hosts and the 1 the cluster asks beyond
			"network.external_ips":           2, // the created address and the 1 the cluster asks beyond
			"network.nat_gateways":           1,
			"network.virtual_networks":       1,
		}))
	})

	It("counts only the tenant's rows", func(ctx context.Context) {
		usage, err := counter.Count(db.ctx, "other")
		Expect(err).ToNot(HaveOccurred())
		Expect(usage).To(Equal(map[string]map[string]int64{
			"": {
				"compute.vcpus":      6,
				"compute.memory_gib": 24,
			},
		}))
	})

	It("counts resources until they are archived, not until they are deleted", func(ctx context.Context) {
		db.exec(`update volumes set deletion_timestamp = now() where id = 'data-vol'`)
		usage, err := counter.Count(db.ctx, "acme")
		Expect(err).ToNot(HaveOccurred())
		Expect(usage[""]["storage.volumes"]).To(Equal(int64(3)))
		Expect(usage[""]["storage.fast.gib"]).To(Equal(int64(300)))
	})
})

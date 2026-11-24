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
	"fmt"
	"log/slog"
	"os"
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
			`{"osac.openshift.io/auto-created-for": "vm-1", "osac.openshift.io/auto-created-kind": "compute_instance"}`,
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
			`{"osac.openshift.io/auto-created-for": "prod", "osac.openshift.io/auto-created-kind": "cluster"}`,
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

		// The other tenant's VM counts for it and never for acme, whose counts above exclude it:
		other, err := counter.Count(db.ctx, "other")
		Expect(err).ToNot(HaveOccurred())
		Expect(other).To(Equal(map[string]map[string]int64{
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

var _ = Describe("Type resolution", func() {
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
		db.insertObject("instance_types", "it-small", "shared", "", "system", "{}",
			`{"spec": {"vcpus": 2, "memory_gib": 8}}`)
	})

	It("resolves the type the reference selects, not a same-name type of another scope", func(ctx context.Context) {
		// The tenant owns a type named like the shared one, with a different price:
		db.exec(`insert into instance_types (id, name, tenant, project, creator, labels, data)
			values ('it-small-acme', 'it-small', 'acme', ''::ltree, 'system', '{}'::jsonb, $1::jsonb)`,
			`{"spec": {"vcpus": 99, "memory_gib": 99}}`)
		// A VM whose stored reference carries the shared type's scope:
		db.insertObject("compute_instances", "vm-1", "acme", "", "user", "{}", `{
			"spec": {"instance_type": {"name": "it-small", "tenant": "shared", "shared": true}}
		}`)
		// A VM whose stored reference carries the tenant's own scope:
		db.insertObject("compute_instances", "vm-2", "acme", "", "user", "{}", `{
			"spec": {"instance_type": {"name": "it-small", "tenant": "acme"}}
		}`)
		usage, err := counter.Count(db.ctx, "acme")
		Expect(err).ToNot(HaveOccurred())
		Expect(usage[""]["compute.vcpus"]).To(Equal(int64(101)))
		Expect(usage[""]["compute.memory_gib"]).To(Equal(int64(107)))
	})

	It("falls back to the row's tenant, or the shared tenant, for a name-only reference", func(ctx context.Context) {
		db.insertObject("compute_instances", "vm-1", "acme", "", "user", "{}", `{
			"spec": {"instance_type": {"name": "it-small"}}
		}`)
		usage, err := counter.Count(db.ctx, "acme")
		Expect(err).ToNot(HaveOccurred())
		Expect(usage[""]["compute.vcpus"]).To(Equal(int64(2)))
	})

	It("fails loudly when an asked type resolves to no row", func(ctx context.Context) {
		db.insertObject("compute_instances", "vm-1", "acme", "", "user", "{}", `{
			"spec": {"instance_type": {"name": "it-absent"}}
		}`)
		_, err := counter.Count(db.ctx, "acme")
		Expect(err).To(MatchError(ContainSubstring(
			"a stored row references type 'it-absent', which no longer exists")))
	})

	It("fails loudly when a planned child's type resolves to no row", func(ctx context.Context) {
		db.insertObject("clusters", "prod", "acme", "", "user", "{}", `{
			"spec": {
				"node_sets": {"compute": {"size": 3, "baremetal_instance_type": {"name": "metal-absent"}}}
			}
		}`)
		_, err := counter.Count(db.ctx, "acme")
		Expect(err).To(MatchError(ContainSubstring(
			"a stored row references type 'metal-absent', which no longer exists")))
	})

	It("tolerates an absent running type, which the status has not reported yet", func(ctx context.Context) {
		// The larger-of rule reads the running type from the status; a resource that has
		// no status yet runs nothing, so only the asked type counts:
		db.insertObject("compute_instances", "vm-1", "acme", "", "user", "{}", `{
			"spec": {"instance_type": {"name": "it-small"}}
		}`)
		usage, err := counter.Count(db.ctx, "acme")
		Expect(err).ToNot(HaveOccurred())
		Expect(usage[""]["compute.vcpus"]).To(Equal(int64(2)))
	})

	It("fails loudly when a running type no longer resolves", func(ctx context.Context) {
		// A status that names a type the join cannot find means the resource runs a
		// type the counter cannot price: the deletion guard scans only the spec's
		// reference, so the type a resize left behind in the status can be gone:
		db.insertObject("compute_instances", "vm-1", "acme", "", "user", "{}", `{
			"spec": {"instance_type": {"name": "it-small"}},
			"status": {"instance_type": {"name": "it-absent"}}
		}`)
		_, err := counter.Count(db.ctx, "acme")
		Expect(err).To(MatchError(ContainSubstring(
			"a stored row references type 'it-absent', which no longer exists")))
	})
})

var _ = Describe("Counting rules", func() {
	var db *usageDatabase

	// insertHost inserts one host of the shared metal-1tb type with the given status.state.
	insertHost := func(id, state string) {
		data := `{"spec": {"instance_type": {"name": "metal-1tb"}}}`
		if state != "" {
			data = fmt.Sprintf(`{"spec": {"instance_type": {"name": "metal-1tb"}},
				"status": {"state": "%s"}}`, state)
		}
		db.insertObject("bare_metal_instances", id, "acme", "", "system", "{}", data)
	}

	// hostsCounted counts the bare_metal.instances.metal-1tb dimension of the default project.
	hostsCounted := func(counter *Counter) int64 {
		usage, err := counter.Count(db.ctx, "acme")
		Expect(err).ToNot(HaveOccurred())
		return usage[""]["bare_metal.instances.metal-1tb"]
	}

	BeforeEach(func() {
		db = newUsageDatabase(context.Background())
		db.insertObject("bare_metal_instance_types", "metal-1tb", "shared", "", "system", "{}", `{}`)
		insertHost("host-failed", "BARE_METAL_INSTANCE_STATE_FAILED")
		insertHost("host-running", "BARE_METAL_INSTANCE_STATE_RUNNING")
		insertHost("host-no-state", "")
	})

	It("stops counting rows in the mapped states", func() {
		released, err := NewCounter().
			SetLogger(logger).
			SetCountingRules(map[string][]string{
				"BareMetalInstance": {"BARE_METAL_INSTANCE_STATE_FAILED"},
			}).
			Build()
		Expect(err).ToNot(HaveOccurred())

		// The failed host releases; the running one and the stateless one keep counting:
		Expect(hostsCounted(released)).To(Equal(int64(2)))
	})

	It("changes nothing by default", func() {
		// No injected rules and no OSAC_QUOTA_COUNTING_RULES: the shipped default is empty.
		counter, err := NewCounter().SetLogger(logger).Build()
		Expect(err).ToNot(HaveOccurred())
		Expect(hostsCounted(counter)).To(Equal(int64(3)))
	})

	It("loads the rules from the file the environment variable names", func() {
		file, err := os.CreateTemp("", "quota-counting-rules-*.yaml")
		Expect(err).ToNot(HaveOccurred())
		DeferCleanup(func() {
			Expect(file.Close()).To(Succeed())
			Expect(os.Remove(file.Name())).To(Succeed())
		})
		_, err = file.WriteString(`
counting:
  BareMetalInstance:
    stops_counting_in: [BARE_METAL_INSTANCE_STATE_FAILED]
`)
		Expect(err).ToNot(HaveOccurred())

		Expect(os.Setenv(countingRulesEnvVar, file.Name())).To(Succeed())
		DeferCleanup(func() {
			Expect(os.Unsetenv(countingRulesEnvVar)).To(Succeed())
		})
		counter, err := NewCounter().SetLogger(logger).Build()
		Expect(err).ToNot(HaveOccurred())
		Expect(hostsCounted(counter)).To(Equal(int64(2)))
	})

	It("means no rules when the variable names a missing file", func() {
		Expect(os.Setenv(countingRulesEnvVar, "/tmp/opencode/no-such-quota-counting-rules.yaml")).To(Succeed())
		DeferCleanup(func() {
			Expect(os.Unsetenv(countingRulesEnvVar)).To(Succeed())
		})
		counter, err := NewCounter().SetLogger(logger).Build()
		Expect(err).ToNot(HaveOccurred())
		Expect(hostsCounted(counter)).To(Equal(int64(3)))
	})

	DescribeTable("reads the counting rules file the variable names",
		func(content, expectedError string) {
			file, err := os.CreateTemp("", "quota-counting-rules-*.yaml")
			Expect(err).ToNot(HaveOccurred())
			DeferCleanup(func() {
				Expect(file.Close()).To(Succeed())
				Expect(os.Remove(file.Name())).To(Succeed())
			})
			_, err = file.WriteString(content)
			Expect(err).ToNot(HaveOccurred())

			Expect(os.Setenv(countingRulesEnvVar, file.Name())).To(Succeed())
			DeferCleanup(func() {
				Expect(os.Unsetenv(countingRulesEnvVar)).To(Succeed())
			})
			counter, err := NewCounter().SetLogger(logger).Build()
			if expectedError == "" {
				// An empty file loads as no rules, so every host keeps counting:
				Expect(err).ToNot(HaveOccurred())
				Expect(hostsCounted(counter)).To(Equal(int64(3)))
				return
			}
			Expect(err).To(MatchError(ContainSubstring(expectedError)))
		},
		Entry("fails on a file that does not parse", "counting: [", "invalid counting rules file"),
		Entry("fails on an unknown key", "counting:\n  BareMetalInstance:\n    bogus: []",
			"field bogus not found"),
		Entry("loads an empty file as no rules", "", ""),
	)

	DescribeTable("refuses rules the counter can't apply",
		func(rules map[string][]string, message string) {
			_, err := NewCounter().
				SetLogger(logger).
				SetCountingRules(rules).
				Build()
			Expect(err).To(MatchError(ContainSubstring(message)))
		},
		Entry("an unknown kind", map[string][]string{
			"Subnet": {"SUBNET_STATE_FAILED"},
		}, "kind 'Subnet', which carries no quota annotations"),
		Entry("a kind whose status has no state enum", map[string][]string{
			"DiskImage": {"DISK_IMAGE_LIFECYCLE_DEPRECATED"},
		}, "whose status.state is not an enum"),
		Entry("a state the enum has no value of", map[string][]string{
			"BareMetalInstance": {"BARE_METAL_INSTANCE_STATE_BOGUS"},
		}, "which its status.state enum has no value of"),
	)
})

var _ = Describe("Parent counting", func() {
	var db *usageDatabase

	// insertCluster inserts a cluster whose compute node set asks for the given number of
	// metal-1tb hosts. Zero leaves no node set in the spec, the world a removed node set
	// leaves behind while its host rows remain.
	insertCluster := func(size int) {
		nodeSets := "{}"
		if size > 0 {
			nodeSets = fmt.Sprintf(`{"compute": {"size": %d,
				"baremetal_instance_type": {"name": "metal-1tb"}}}`, size)
		}
		db.insertObject("clusters", "prod", "acme", "team", "user", "{}",
			fmt.Sprintf(`{"spec": {"node_sets": %s}}`, nodeSets))
	}

	// insertHost inserts one system-created host linked to the cluster, with the given
	// status.state.
	insertHost := func(id, state string) {
		stateField := ""
		if state != "" {
			stateField = fmt.Sprintf(`, "state": "%s"`, state)
		}
		db.insertObject("bare_metal_instances", id, "acme", "team", "system", "{}", fmt.Sprintf(`{
			"spec": {"instance_type": {"name": "metal-1tb"}},
			"status": {"cluster": {"id": "prod"}%s}
		}`, stateField))
	}

	// hostsCounted counts the bare_metal.instances.metal-1tb dimension of the team project.
	hostsCounted := func(counter *Counter) int64 {
		usage, err := counter.Count(db.ctx, "acme")
		Expect(err).ToNot(HaveOccurred())
		return usage["team"]["bare_metal.instances.metal-1tb"]
	}

	BeforeEach(func() {
		db = newUsageDatabase(context.Background())
		db.insertObject("bare_metal_instance_types", "metal-1tb", "shared", "", "system", "{}", `{}`)
	})

	DescribeTable("counts the larger of the parent's ask and its countable children",
		func(ask int, states []string, rules map[string][]string, expectedTotal, expectedRoom int64) {
			counter, err := NewCounter().
				SetLogger(logger).
				SetCountingRules(rules).
				Build()
			Expect(err).ToNot(HaveOccurred())

			insertCluster(ask)
			for i, state := range states {
				insertHost(fmt.Sprintf("host-%d", i+1), state)
			}
			total := hostsCounted(counter)
			Expect(total).To(Equal(expectedTotal))

			// Removing the node set from the spec leaves the hosts counting on their own,
			// so what the total loses is exactly the room the parent demanded beyond them:
			db.exec(`update clusters set data = data #- '{spec,node_sets}' where id = 'prod'`)
			Expect(total - hostsCounted(counter)).To(Equal(expectedRoom))
		},
		Entry("no children yet", 3, nil, nil, int64(3), int64(3)),
		Entry("partial children", 3, []string{"", ""}, nil, int64(3), int64(1)),
		Entry("children beyond the ask", 1, []string{"", ""}, nil, int64(2), int64(0)),
		Entry("children stopped by counting rules", 3,
			[]string{"BARE_METAL_INSTANCE_STATE_RUNNING", "BARE_METAL_INSTANCE_STATE_FAILED"},
			map[string][]string{"BareMetalInstance": {"BARE_METAL_INSTANCE_STATE_FAILED"}},
			int64(3), int64(2)),
		Entry("a node set removed while the host rows remain", 0, []string{"", ""}, nil, int64(2), int64(0)),
	)
})

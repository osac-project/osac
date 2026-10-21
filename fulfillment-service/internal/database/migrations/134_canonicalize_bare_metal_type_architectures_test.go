/*
Copyright (c) 2026 Red Hat Inc.

Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except in compliance with the
License. You may obtain a copy of the License at

  http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on an
"AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
specific language governing permissions and limitations under the License.
*/

package migrations

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"net/url"
	"os"

	"github.com/golang-migrate/migrate/v4"
	"github.com/golang-migrate/migrate/v4/source/iofs"
	"github.com/jackc/pgx/v5"
	. "github.com/onsi/ginkgo/v2/dsl/core"
	. "github.com/onsi/gomega"

	"github.com/osac-project/osac/fulfillment-service/internal/database"
)

var _ = DescribeMigration("Canonicalize bare metal type architectures", func() {
	const (
		migrationFile         = "134_canonicalize_bare_metal_type_architectures.up.sql"
		migrationVersion uint = 134
		typeID                = "architecture-type"
		instanceID            = "referencing-instance"
		catalogItemID         = "referencing-item"
	)

	// seed requires a clean database at the version preceding the architecture migration.
	seed := func(ctx context.Context, conn *pgx.Conn) error {
		var version uint
		var dirty bool
		err := conn.QueryRow(ctx, "select version, dirty from schema_migrations").Scan(&version, &dirty)
		if err != nil {
			return err
		}
		if version != migrationVersion-1 || dirty {
			return fmt.Errorf("architecture fixtures require clean migration version %d", migrationVersion-1)
		}
		tx, err := conn.Begin(ctx)
		if err != nil {
			return err
		}
		defer func() { _ = tx.Rollback(ctx) }()
		_, err = tx.Exec(ctx, `insert into bare_metal_instance_types
			(id, name, tenant, creator, labels, annotations, data, version, creation_timestamp, finalizers)
			values ($1, $1, 'shared', 'system', '{"profile":"compute"}',
			'{"example.test/note":"preserve"}', $2::jsonb, 7, '2020-01-01 UTC', array['fixture-finalizer'])`, typeID,
			`{"spec":{"description":"Catalog architecture fixture",
			"host_label_selector":{"match_labels":{"profile":"compute"}},
			"hardware":{"cpu":{"cores":8,"threads_per_core":2,"architecture":"aarch64","model":"fixture-cpu"},
			"memory":{"total_gb":"32","type":"DDR5"},
			"disks":[{"type":"SSD","capacity_gb":"512","interface":"SATA"}],
			"accelerators":[{"type":"GPU","model":"fixture-gpu"}],
			"network_ports":[{"name":"data-0","role":"fabric","type":"Ethernet","speed":"25Gbps"}],
			"capabilities":{"boot":"uefi"}}},"status":{},"extra":{"preserve":true}}`)
		if err != nil {
			return err
		}
		_, err = tx.Exec(ctx, `insert into bare_metal_instances (id, name, tenant, data)
			values ($1, $1, 'shared', $2::jsonb)`, instanceID,
			`{"spec":{"instance_type":{"id":"architecture-type","name":"architecture-type","shared":true}}}`)
		if err != nil {
			return err
		}
		_, err = tx.Exec(ctx, `insert into bare_metal_instance_catalog_items (id, name, tenant, data)
			values ($1, $1, 'shared', $2::jsonb)`, catalogItemID,
			`{"fields":{"instance_type":{"locked":{"id":"architecture-type","name":"architecture-type","shared":true}}}}`)
		if err != nil {
			return err
		}
		return tx.Commit(ctx)
	}

	tables := []string{"bare_metal_instance_types", "archived_bare_metal_instance_types"}

	snapshot := func(ctx context.Context, connection *pgx.Conn, table string) map[string]string {
		GinkgoHelper()
		rows, err := connection.Query(ctx, fmt.Sprintf("select id, to_jsonb(t)::text from %s t order by id", table))
		Expect(err).ToNot(HaveOccurred())
		defer rows.Close()
		result := map[string]string{}
		for rows.Next() {
			var id, data string
			Expect(rows.Scan(&id, &data)).To(Succeed())
			result[id] = data
		}
		Expect(rows.Err()).ToNot(HaveOccurred())
		return result
	}

	expectMigrated := func(before, after map[string]string) {
		GinkgoHelper()
		Expect(after).To(HaveLen(len(before)))
		for id, original := range before {
			var row map[string]any
			Expect(json.Unmarshal([]byte(original), &row)).To(Succeed())
			value, ok := row["data"].(map[string]any)
			if !ok {
				value = map[string]any{}
				row["data"] = value
			}
			for _, field := range []string{"spec", "hardware", "cpu"} {
				nested, ok := value[field].(map[string]any)
				if !ok {
					nested = map[string]any{}
					value[field] = nested
				}
				value = nested
			}
			architecture, _ := value["architecture"].(string)
			if architecture != "amd64" && architecture != "arm64" && architecture != "s390x" {
				value["architecture"] = "amd64"
			}
			expected, err := json.Marshal(row)
			Expect(err).ToNot(HaveOccurred())
			Expect(after).To(HaveKey(id))
			Expect(after[id]).To(MatchJSON(string(expected)), id)
		}
	}

	seedRow := func(ctx context.Context, id, data, state string) {
		GinkgoHelper()
		if state == "archived" {
			_, err := conn.Exec(ctx, `insert into archived_bare_metal_instance_types
				(id, name, tenant, project, creator, labels, annotations, data, version,
				creation_timestamp, deletion_timestamp, archival_timestamp)
				select $1, $1, tenant, project, creator, labels, annotations, $2::jsonb, version,
				creation_timestamp, '2021-01-01 UTC', '2022-01-01 UTC'
				from bare_metal_instance_types where id = $3`, id, data, typeID)
			Expect(err).ToNot(HaveOccurred())
			return
		}
		deletion := "epoch"
		if state == "deleted" {
			deletion = "2021-01-01 UTC"
		}
		_, err := conn.Exec(ctx, `insert into bare_metal_instance_types
			(id, name, tenant, project, creator, labels, annotations, data, version,
			creation_timestamp, deletion_timestamp, finalizers)
			select $1, $1, tenant, project, creator, labels, annotations, $2::jsonb, version,
			creation_timestamp, $3::timestamptz, array['fixture-finalizer']
			from bare_metal_instance_types where id = $4`, id, data, deletion, typeID)
		Expect(err).ToNot(HaveOccurred())
	}

	It("canonicalizes active, soft-deleted and archived rows while preserving data and references", func(ctx context.Context) {
		Expect(seed(ctx, conn)).To(Succeed())
		var originalData string
		Expect(conn.QueryRow(ctx, "select data::text from bare_metal_instance_types where id = $1", typeID).
			Scan(&originalData)).To(Succeed())
		variants := []struct {
			name  string
			value any
		}{
			{"amd64", "amd64"}, {"arm64", "arm64"}, {"s390x", "s390x"},
			{"x86-alias", "x86_64"}, {"arm-alias", "aarch64"},
			{"uppercase-amd64", "AMD64"}, {"uppercase-arm64", "ARM64"}, {"uppercase-s390x", "S390X"},
			{"mixed-case", "Amd64"}, {"leading-space", " amd64"}, {"trailing-space", "amd64 "},
			{"arm-whitespace", " arm64 "}, {"s390x-whitespace", " s390x "}, {"newline", "amd64\n"},
			{"unknown", "ppc64le"}, {"empty", ""}, {"null", nil}, {"missing", nil},
			{"number", 64}, {"boolean", true}, {"array", []string{"amd64"}}, {"object", map[string]string{"name": "amd64"}},
		}
		for _, state := range []string{"active", "deleted", "archived"} {
			for _, variant := range variants {
				var data map[string]any
				Expect(json.Unmarshal([]byte(originalData), &data)).To(Succeed())
				cpu := data["spec"].(map[string]any)["hardware"].(map[string]any)["cpu"].(map[string]any)
				if variant.name == "missing" {
					delete(cpu, "architecture")
				} else {
					cpu["architecture"] = variant.value
				}
				encoded, err := json.Marshal(data)
				Expect(err).ToNot(HaveOccurred())
				seedRow(ctx, "architecture-"+state+"-"+variant.name, string(encoded), state)
			}
		}
		before := map[string]map[string]string{}
		for _, table := range append(tables, "bare_metal_instances", "bare_metal_instance_catalog_items") {
			before[table] = snapshot(ctx, conn, table)
		}
		Expect(tool.Migrate(ctx, migrationVersion)).To(Succeed())
		for _, table := range tables {
			expectMigrated(before[table], snapshot(ctx, conn, table))
		}
		for _, table := range []string{"bare_metal_instances", "bare_metal_instance_catalog_items"} {
			Expect(snapshot(ctx, conn, table)).To(Equal(before[table]))
		}
	})

	It("materializes omitted or null architecture paths without replacing unrelated data", func(ctx context.Context) {
		Expect(seed(ctx, conn)).To(Succeed())
		for i, data := range []string{
			`{"extra":"preserve"}`, `{"spec":{"description":"preserve"}}`,
			`{"spec":{"hardware":{"capabilities":{"boot":"uefi"}}}}`,
			`{"spec":{"hardware":{"cpu":null,"capabilities":{"boot":"uefi"}}}}`,
			`{"spec":null,"extra":"preserve"}`, `{"spec":{"hardware":null,"description":"preserve"}}`,
		} {
			for _, state := range []string{"active", "deleted", "archived"} {
				seedRow(ctx, fmt.Sprintf("architecture-%s-missing-%d", state, i), data, state)
			}
		}
		before := map[string]map[string]string{}
		for _, table := range tables {
			before[table] = snapshot(ctx, conn, table)
		}
		Expect(tool.Migrate(ctx, migrationVersion)).To(Succeed())
		for _, table := range tables {
			expectMigrated(before[table], snapshot(ctx, conn, table))
		}
	})

	It("normalizes non-object architecture parents while preserving unrelated data", func(ctx context.Context) {
		Expect(seed(ctx, conn)).To(Succeed())
		var originalData string
		Expect(conn.QueryRow(ctx, "select data::text from bare_metal_instance_types where id = $1", typeID).
			Scan(&originalData)).To(Succeed())
		for depth, path := range [][]string{nil, {"spec"}, {"spec", "hardware"}, {"spec", "hardware", "cpu"}} {
			for i, parent := range []any{nil, "legacy", 64, true, []any{}, []any{"legacy"}} {
				var data any
				Expect(json.Unmarshal([]byte(originalData), &data)).To(Succeed())
				if len(path) == 0 {
					data = parent
				} else {
					value := data.(map[string]any)
					for _, field := range path[:len(path)-1] {
						value = value[field].(map[string]any)
					}
					value[path[len(path)-1]] = parent
				}
				encoded, err := json.Marshal(data)
				Expect(err).ToNot(HaveOccurred())
				for _, state := range []string{"active", "deleted", "archived"} {
					seedRow(ctx, fmt.Sprintf("architecture-%s-parent-%d-%d", state, depth, i), string(encoded), state)
				}
			}
		}
		before := map[string]map[string]string{}
		for _, table := range tables {
			before[table] = snapshot(ctx, conn, table)
		}
		Expect(tool.Migrate(ctx, migrationVersion)).To(Succeed())
		for _, table := range tables {
			expectMigrated(before[table], snapshot(ctx, conn, table))
		}
	})

	It("makes no further data or version changes when repeated", func(ctx context.Context) {
		Expect(seed(ctx, conn)).To(Succeed())
		seedRow(ctx, "architecture-archived-repeat", `{"spec":{"hardware":{"cpu":{"architecture":"aarch64"}}}}`, "archived")
		Expect(tool.Migrate(ctx, migrationVersion)).To(Succeed())
		before := map[string]map[string]string{}
		for _, table := range tables {
			before[table] = snapshot(ctx, conn, table)
		}
		var changesBefore int
		Expect(conn.QueryRow(ctx, "select count(*) from changes").Scan(&changesBefore)).To(Succeed())
		data, err := os.ReadFile(migrationFile)
		Expect(err).ToNot(HaveOccurred())
		_, err = conn.Exec(ctx, string(data))
		Expect(err).ToNot(HaveOccurred())
		Expect(tool.Migrate(ctx, migrationVersion)).To(Succeed())
		for _, table := range tables {
			Expect(snapshot(ctx, conn, table)).To(Equal(before[table]))
		}
		var version uint
		var dirty bool
		Expect(conn.QueryRow(ctx, "select version, dirty from schema_migrations").Scan(&version, &dirty)).To(Succeed())
		Expect(version).To(Equal(migrationVersion))
		Expect(dirty).To(BeFalse())
		var changesAfter int
		Expect(conn.QueryRow(ctx, "select count(*) from changes").Scan(&changesAfter)).To(Succeed())
		Expect(changesAfter).To(Equal(changesBefore))
	})

	It("migrates a fresh database without creating type rows", func(ctx context.Context) {
		db, err := server.NewInstance().SetVersion(0).Build()
		Expect(err).ToNot(HaveOccurred())
		DeferCleanup(db.Close)
		connection, err := db.Connection(ctx)
		Expect(err).ToNot(HaveOccurred())
		DeferCleanup(connection.Close)
		databaseURL, err := db.Url(ctx)
		Expect(err).ToNot(HaveOccurred())
		freshTool, err := database.NewTool().SetLogger(logger).SetURL(databaseURL).Build()
		Expect(err).ToNot(HaveOccurred())
		Expect(freshTool.Migrate(ctx, math.MaxUint)).To(Succeed())
		for _, table := range tables {
			Expect(snapshot(ctx, connection, table)).To(BeEmpty())
		}
	})

	It("rolls back both table updates on an archive failure and retains dirty-version recovery", func(ctx context.Context) {
		Expect(seed(ctx, conn)).To(Succeed())
		seedRow(ctx, "architecture-archived-failure", `{"spec":{"hardware":{"cpu":{"architecture":"aarch64"}}}}`, "archived")
		before := map[string]map[string]string{}
		for _, table := range tables {
			before[table] = snapshot(ctx, conn, table)
		}
		_, err := conn.Exec(ctx, `create function reject_architecture_migration() returns trigger language plpgsql as $$
			begin
				if (select data #>> '{spec,hardware,cpu,architecture}' from bare_metal_instance_types
					where id = 'architecture-type') is distinct from 'amd64' then
					raise exception 'live update did not precede archive update';
				end if;
				raise exception 'fixture rejects archive architecture update';
			end; $$;
			create trigger reject_architecture_migration before update on archived_bare_metal_instance_types
			for each row execute function reject_architecture_migration()`)
		Expect(err).ToNot(HaveOccurred())
		err = tool.Migrate(ctx, migrationVersion)
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring("fixture rejects archive architecture update"))
		for _, table := range tables {
			Expect(snapshot(ctx, conn, table)).To(Equal(before[table]))
		}
		var version uint
		var dirty bool
		Expect(conn.QueryRow(ctx, "select version, dirty from schema_migrations").Scan(&version, &dirty)).To(Succeed())
		Expect(version).To(Equal(migrationVersion))
		Expect(dirty).To(BeTrue())
		err = tool.Migrate(ctx, migrationVersion)
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring("Dirty database"))
		_, err = conn.Exec(ctx, "drop trigger reject_architecture_migration on archived_bare_metal_instance_types; drop function reject_architecture_migration()")
		Expect(err).ToNot(HaveOccurred())
		source, err := iofs.New(os.DirFS("."), ".")
		Expect(err).ToNot(HaveOccurred())
		parsed, err := url.Parse(tool.URL())
		Expect(err).ToNot(HaveOccurred())
		parsed.Scheme = "pgx5"
		runner, err := migrate.NewWithSourceInstance("iofs", source, parsed.String())
		Expect(err).ToNot(HaveOccurred())
		Expect(runner.Force(int(migrationVersion - 1))).To(Succeed())
		sourceErr, databaseErr := runner.Close()
		Expect(sourceErr).ToNot(HaveOccurred())
		Expect(databaseErr).ToNot(HaveOccurred())
		Expect(tool.Migrate(ctx, migrationVersion)).To(Succeed())
		for _, table := range tables {
			expectMigrated(before[table], snapshot(ctx, conn, table))
		}
	})
})

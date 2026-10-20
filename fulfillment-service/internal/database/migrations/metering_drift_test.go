/*
Copyright (c) 2026 Red Hat Inc.

Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except in compliance with
the License. You may obtain a copy of the License at

  http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
specific language governing permissions and limitations under the License.
*/

package migrations

import (
	"context"
	"encoding/json"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"github.com/jackc/pgx/v5"
	. "github.com/onsi/ginkgo/v2/dsl/core"
	. "github.com/onsi/ginkgo/v2/dsl/decorators"
	. "github.com/onsi/gomega"

	"github.com/osac-project/osac/fulfillment-service/internal/database"
	"github.com/osac-project/osac/proto/metering"
)

// The installed trigger configuration and the metering annotations on the private protos must
// describe the same contract: same tables, fields, kinds and time fields. The annotations are
// the source of truth, so this guard fails when a migration change and an annotation change
// land without each other.
var _ = Describe("Metering source contract drift", Ordered, func() {
	var driftConn *pgx.Conn

	BeforeAll(func(ctx context.Context) {
		migrationFiles, err := filepath.Glob("*.up.sql")
		Expect(err).ToNot(HaveOccurred())

		var latest uint
		for _, file := range migrationFiles {
			parts := strings.SplitN(file, "_", 2)
			if len(parts) < 2 {
				continue
			}
			n, err := strconv.ParseUint(parts[0], 10, 64)
			if err != nil {
				continue
			}
			if uint(n) > latest {
				latest = uint(n)
			}
		}
		Expect(latest).To(BeNumerically(">", 0))

		driftDB, err := server.NewInstance().
			SetVersion(latest).
			Build()
		Expect(err).ToNot(HaveOccurred())
		DeferCleanup(driftDB.Close)

		driftConn, err = driftDB.Connection(ctx)
		Expect(err).ToNot(HaveOccurred())
		DeferCleanup(driftConn.Close)
	})

	It("The installed tracked fields match the proto annotations", func(ctx context.Context) {
		expected, err := database.MeteringTableFields()
		Expect(err).ToNot(HaveOccurred())
		Expect(expected).ToNot(BeEmpty())

		// Collect the tracked-field configuration installed by the migrations, and the tables
		// that carry the trigger, from the migrated database:
		rows, err := driftConn.Query(ctx, `
			select
				c.relname,
				metering_tracked_fields(c.relname),
				count(t.oid) as triggers
			from
				pg_catalog.pg_class c
			join
				pg_catalog.pg_namespace n on n.oid = c.relnamespace
			left join
				pg_catalog.pg_trigger t
				on t.tgrelid = c.oid and t.tgname = 'enforce_metering_fields' and not t.tgisinternal
			where
				n.nspname = 'public' and
				c.relkind = 'r' and
				c.relname not like 'active_%' and
				c.relname not like 'archived_%' and
				c.relname not in (
					'changes',
					'notifications',
					'project_membership_subjects',
					'schema_migrations',
					'storage_tier_backends',
					'tenant_domains'
				)
			group by c.relname
			order by c.relname
		`)
		Expect(err).ToNot(HaveOccurred())

		installedConfig := map[string][]metering.Field{}
		installedTriggers := map[string]int{}
		tables, err := pgx.CollectRows(rows, func(row pgx.CollectableRow) (string, error) {
			var table string
			var config []byte
			var triggers int
			err := row.Scan(&table, &config, &triggers)
			if err != nil {
				return "", err
			}
			installedTriggers[table] = triggers
			if config != nil {
				var fields []metering.Field
				err = json.Unmarshal(config, &fields)
				if err != nil {
					return "", err
				}
				sort.Slice(fields, func(i, j int) bool { return fields[i].Path < fields[j].Path })
				installedConfig[table] = fields
			}
			return table, nil
		})
		Expect(err).ToNot(HaveOccurred())
		Expect(tables).ToNot(BeEmpty())

		// The configuration must cover exactly the annotated tables:
		Expect(installedConfig).To(Equal(expected))

		// The trigger must be attached to exactly the configured tables:
		for table := range installedConfig {
			Expect(installedTriggers[table]).To(Equal(1), "table %s must have the trigger", table)
		}
		for table, triggers := range installedTriggers {
			if _, configured := installedConfig[table]; !configured {
				Expect(triggers).To(BeZero(), "table %s has the trigger without tracked fields", table)
			}
		}
	})
})

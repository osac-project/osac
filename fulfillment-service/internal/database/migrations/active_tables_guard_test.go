/*
Copyright (c) 2026 Red Hat Inc.

Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except in compliance with the
License. You may obtain a copy of the License at

  http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on an
"AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the specific
language governing permissions and limitations under the License.
*/

package migrations

import (
	"context"
	"fmt"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"github.com/jackc/pgx/v5"
	. "github.com/onsi/ginkgo/v2/dsl/core"
	. "github.com/onsi/ginkgo/v2/dsl/decorators"
	. "github.com/onsi/gomega"
)

var _ = Describe("Active object tables schema guard", Ordered, func() {
	var guardConn *pgx.Conn

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

		guardDB, err := server.NewInstance().
			SetVersion(latest).
			Build()
		Expect(err).ToNot(HaveOccurred())
		DeferCleanup(guardDB.Close)

		guardConn, err = guardDB.Connection(ctx)
		Expect(err).ToNot(HaveOccurred())
		DeferCleanup(guardConn.Close)
	})

	It("Every object table has an active_* companion table", func(ctx context.Context) {
		rows, err := guardConn.Query(ctx, `
			select c.relname
			from pg_catalog.pg_class c
			join pg_catalog.pg_namespace n on n.oid = c.relnamespace
			join pg_catalog.pg_attribute a on a.attrelid = c.oid
			where n.nspname = 'public'
			  and c.relkind = 'r'
			  and a.attname = 'deletion_timestamp'
			  and c.relname not like 'active_%'
			  and c.relname not like 'archived_%'
			order by c.relname
		`)
		Expect(err).ToNot(HaveOccurred())
		tables, err := pgx.CollectRows(rows, pgx.RowTo[string])
		Expect(err).ToNot(HaveOccurred())
		Expect(tables).ToNot(BeEmpty())

		var missing []string
		for _, table := range tables {
			activeTable := "active_" + table
			var exists bool
			err = guardConn.QueryRow(ctx, `
				select exists (
					select 1
					from pg_catalog.pg_class c
					join pg_catalog.pg_namespace n on n.oid = c.relnamespace
					where n.nspname = 'public'
					  and c.relkind = 'r'
					  and c.relname = $1
				)
			`, activeTable).Scan(&exists)
			Expect(err).ToNot(HaveOccurred())
			if !exists {
				missing = append(missing, table)
			}
		}
		sort.Strings(missing)
		Expect(missing).To(BeEmpty(), fmt.Sprintf(
			"object tables missing active_* companions: %v — "+
				"add active_<table> tables and materialize_active_objects triggers for these tables",
			missing,
		))
	})

	It("Every object table has a materialize_active_objects trigger", func(ctx context.Context) {
		rows, err := guardConn.Query(ctx, `
			select c.relname
			from pg_catalog.pg_class c
			join pg_catalog.pg_namespace n on n.oid = c.relnamespace
			join pg_catalog.pg_attribute a on a.attrelid = c.oid
			where n.nspname = 'public'
			  and c.relkind = 'r'
			  and a.attname = 'deletion_timestamp'
			  and c.relname not like 'active_%'
			  and c.relname not like 'archived_%'
			order by c.relname
		`)
		Expect(err).ToNot(HaveOccurred())
		tables, err := pgx.CollectRows(rows, pgx.RowTo[string])
		Expect(err).ToNot(HaveOccurred())

		var missing []string
		for _, table := range tables {
			var count int
			err = guardConn.QueryRow(ctx, `
				select count(*)
				from information_schema.triggers
				where trigger_name = 'materialize_active_objects'
				  and event_object_table = $1
			`, table).Scan(&count)
			Expect(err).ToNot(HaveOccurred())
			if count == 0 {
				missing = append(missing, table)
			}
		}
		sort.Strings(missing)
		Expect(missing).To(BeEmpty(), fmt.Sprintf(
			"object tables missing materialize_active_objects trigger: %v — "+
				"attach the trigger to these tables",
			missing,
		))
	})
})

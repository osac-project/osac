/*
Copyright (c) 2026 Red Hat Inc.

Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except in compliance with
the License. You may obtain a copy of the License at

  http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on an
"AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
specific language governing permissions and limitations under the License.
*/

package migrations

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/jackc/pgx/v5/pgconn"
	. "github.com/onsi/ginkgo/v2/dsl/core"
	. "github.com/onsi/gomega"
)

var _ = DescribeMigration("Remove cluster node-set host type", func() {
	It("removes obsolete node-set keys while retaining instance types and other fields", func(ctx context.Context) {
		rows := []struct{ table, id, data, path string }{
			{"clusters", "cluster", `{"spec":{"node_sets":{"workers":{"size":2,"host_type":{"id":"old"},"baremetal_instance_type":{"id":"new"}}}},"status":{"node_sets":{"workers":{"size":2,"host_type":{"id":"old"},"baremetal_instance_type":{"id":"new"}}}}}`, "{spec,node_sets,workers}"},
			{"cluster_templates", "template", `{"node_sets":{"workers":{"size":2,"host_type":{"id":"old"},"baremetal_instance_type":{"id":"new"}}}}`, "{node_sets,workers}"},
			{"cluster_catalog_items", "catalog", `{"fields":{"node_sets":{"locked":{"items":{"workers":{"size":2,"host_type":{"id":"old"},"baremetal_instance_type":{"id":"new"}}}},"editable":{"default_value":{"items":{"workers":{"size":2,"host_type":{"id":"old"},"baremetal_instance_type":{"id":"new"}}}}}}}}`, "{fields,node_sets,locked,items,workers}"},
		}
		for _, row := range rows {
			_, err := conn.Exec(ctx, fmt.Sprintf("insert into %s (id, name, tenant, data) values ($1, $1, 'system', $2::jsonb)", row.table), row.id, row.data)
			Expect(err).NotTo(HaveOccurred())
		}
		Expect(tool.Migrate(ctx, 120)).To(Succeed())
		for _, row := range rows {
			var hasOld bool
			var typeID string
			err := conn.QueryRow(ctx, fmt.Sprintf("select (data #> '%s') ? 'host_type', data #>> '%s' from %s where id = $1", row.path, row.path[:len(row.path)-1]+",baremetal_instance_type,id}", row.table), row.id).Scan(&hasOld, &typeID)
			Expect(err).NotTo(HaveOccurred())
			Expect(hasOld).To(BeFalse(), row.table)
			Expect(typeID).To(Equal("new"), row.table)
		}
		for _, path := range []string{"{status,node_sets,workers}", "{fields,node_sets,editable,default_value,items,workers}"} {
			table, id := "clusters", "cluster"
			if path == "{fields,node_sets,editable,default_value,items,workers}" {
				table, id = "cluster_catalog_items", "catalog"
			}
			var hasOld bool
			err := conn.QueryRow(ctx, fmt.Sprintf("select (data #> '%s') ? 'host_type' from %s where id = $1", path, table), id).Scan(&hasOld)
			Expect(err).NotTo(HaveOccurred())
			Expect(hasOld).To(BeFalse(), path)
		}
	})

	It("protects node-set instance types and preserves unrelated host type protection", func(ctx context.Context) {
		Expect(tool.Migrate(ctx, 120)).To(Succeed())
		cases := []struct{ table, data string }{
			{"clusters", `{"spec":{"node_sets":{"workers":{"baremetal_instance_type":{"id":"type"}}}}}`},
			{"cluster_templates", `{"node_sets":{"workers":{"baremetal_instance_type":{"id":"type"}}}}`},
			{"cluster_catalog_items", `{"fields":{"node_sets":{"locked":{"items":{"workers":{"baremetal_instance_type":{"id":"type"}}}}}}}`},
			{"cluster_catalog_items", `{"fields":{"node_sets":{"editable":{"default_value":{"items":{"workers":{"baremetal_instance_type":{"id":"type"}}}}}}}}`},
		}
		for i, tc := range cases {
			By(fmt.Sprintf("checking active %s reference %d", tc.table, i))
			id := fmt.Sprintf("type-%d", i)
			_, err := conn.Exec(ctx, "insert into bare_metal_instance_types (id, name, tenant, data) values ($1, $1, 'system', '{}')", id)
			Expect(err).NotTo(HaveOccurred())
			_, err = conn.Exec(ctx, fmt.Sprintf("insert into %s (id, name, tenant, data) values ($1, $1, 'system', $2::jsonb)", tc.table), fmt.Sprintf("ref-%d", i), strings.ReplaceAll(tc.data, `"type"`, fmt.Sprintf("%q", id)))
			Expect(err).NotTo(HaveOccurred())
			_, err = conn.Exec(ctx, "update bare_metal_instance_types set deletion_timestamp = now() where id = $1", id)
			var pgErr *pgconn.PgError
			Expect(errors.As(err, &pgErr)).To(BeTrue(), tc.table)
			Expect(pgErr.Code).To(Equal("Z0003"), tc.table)
		}
		_, err := conn.Exec(ctx, "insert into host_types (id, name, tenant, data) values ('host', 'host', 'system', '{}')")
		Expect(err).NotTo(HaveOccurred())
		_, err = conn.Exec(ctx, `insert into cluster_templates (id, name, tenant, data) values ('old-host-template', 'old-host-template', 'system', '{"node_sets":{"workers":{"host_type":{"id":"host"}}}}')`)
		Expect(err).NotTo(HaveOccurred())
		_, err = conn.Exec(ctx, "update host_types set deletion_timestamp = now() where id = 'host'")
		Expect(err).NotTo(HaveOccurred())

		_, err = conn.Exec(ctx, "insert into host_types (id, name, tenant, data) values ('bmi-host', 'bmi-host', 'system', '{}')")
		Expect(err).NotTo(HaveOccurred())
		_, err = conn.Exec(ctx, `insert into bare_metal_instance_templates (id, name, tenant, data) values ('bmi-template', 'bmi-template', 'system', '{"host_type":"bmi-host"}')`)
		Expect(err).NotTo(HaveOccurred())
		_, err = conn.Exec(ctx, "update host_types set deletion_timestamp = now() where id = 'bmi-host'")
		var pgErr *pgconn.PgError
		Expect(errors.As(err, &pgErr)).To(BeTrue())
		Expect(pgErr.Code).To(Equal("Z0003"))
	})
})

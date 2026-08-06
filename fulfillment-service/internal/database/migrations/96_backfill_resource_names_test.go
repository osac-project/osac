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
	"encoding/json"
	"regexp"
	"strings"

	. "github.com/onsi/ginkgo/v2/dsl/core"
	. "github.com/onsi/gomega"
)

var _ = DescribeMigration("Backfill resource names", func() {
	// RFC 1123 DNS label: lowercase alphanumeric and hyphens, must start and end with alphanumeric,
	// max 63 characters.
	dns1123LabelRegex := regexp.MustCompile(`^[a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?$`)

	BeforeEach(func(ctx context.Context) {
		_, err := conn.Exec(ctx, `
			insert into tenants (id, tenant, name, data)
			values ('my-tenant', 'my-tenant', 'my-tenant', '{}')
		`)
		Expect(err).ToNot(HaveOccurred())
	})

	It("Backfills empty cluster names with generated RFC 1123 names", func(ctx context.Context) {
		_, err := conn.Exec(ctx, `
			insert into clusters (id, tenant, name, data)
			values ('a1b2c3d4-e5f6-7890-abcd-ef1234567890', 'my-tenant', '', '{}')
		`)
		Expect(err).ToNot(HaveOccurred())

		err = tool.Migrate(ctx, 96)
		Expect(err).ToNot(HaveOccurred())

		var name string
		err = conn.QueryRow(ctx,
			`select name from clusters where id = $1`,
			"a1b2c3d4-e5f6-7890-abcd-ef1234567890",
		).Scan(&name)
		Expect(err).ToNot(HaveOccurred())
		Expect(name).To(Equal("cluster-a1b2c3d4"))
		Expect(dns1123LabelRegex.MatchString(name)).To(BeTrue())
	})

	It("Backfills empty role names with globally unique generated names", func(ctx context.Context) {
		_, err := conn.Exec(ctx, `
			insert into roles (id, tenant, name, data)
			values ('f0e1d2c3-b4a5-6789-0123-456789abcdef', 'my-tenant', '', '{}')
		`)
		Expect(err).ToNot(HaveOccurred())

		err = tool.Migrate(ctx, 96)
		Expect(err).ToNot(HaveOccurred())

		var name string
		err = conn.QueryRow(ctx,
			`select name from roles where id = $1`,
			"f0e1d2c3-b4a5-6789-0123-456789abcdef",
		).Scan(&name)
		Expect(err).ToNot(HaveOccurred())
		Expect(name).To(Equal("role-f0e1d2c3"))
		Expect(dns1123LabelRegex.MatchString(name)).To(BeTrue())
	})

	It("Deduplicates cluster names within tenant and project scope", func(ctx context.Context) {
		_, err := conn.Exec(ctx, `
			insert into clusters (id, tenant, name, creation_timestamp, data)
			values
				('oldest-id', 'my-tenant', 'dup-name', '2024-01-01T00:00:00Z', '{}'),
				('middle-id', 'my-tenant', 'dup-name', '2024-01-02T00:00:00Z', '{}'),
				('newest-id', 'my-tenant', 'dup-name', '2024-01-03T00:00:00Z', '{}')
		`)
		Expect(err).ToNot(HaveOccurred())

		err = tool.Migrate(ctx, 96)
		Expect(err).ToNot(HaveOccurred())

		rows, err := conn.Query(ctx,
			`select id, name from clusters where id in ('oldest-id', 'middle-id', 'newest-id') order by id`,
		)
		Expect(err).ToNot(HaveOccurred())
		defer rows.Close()

		names := map[string]string{}
		for rows.Next() {
			var id, name string
			Expect(rows.Scan(&id, &name)).To(Succeed())
			names[id] = name
		}
		Expect(rows.Err()).ToNot(HaveOccurred())

		Expect(names["oldest-id"]).To(Equal("dup-name"))
		Expect(names["middle-id"]).To(Equal("dup-name-2"))
		Expect(names["newest-id"]).To(Equal("dup-name-3"))
	})

	It("Deduplicates role names with global scope", func(ctx context.Context) {
		_, err := conn.Exec(ctx, `
			insert into tenants (id, tenant, name, data)
			values ('other-tenant', 'other-tenant', 'other-tenant', '{}')
		`)
		Expect(err).ToNot(HaveOccurred())

		_, err = conn.Exec(ctx, `
			insert into roles (id, tenant, name, creation_timestamp, data)
			values
				('role-old', 'my-tenant', 'test-role', '2024-01-01T00:00:00Z', '{}'),
				('role-new', 'other-tenant', 'test-role', '2024-01-02T00:00:00Z', '{}')
		`)
		Expect(err).ToNot(HaveOccurred())

		err = tool.Migrate(ctx, 96)
		Expect(err).ToNot(HaveOccurred())

		var oldName, newName string
		err = conn.QueryRow(ctx, `select name from roles where id = 'role-old'`).Scan(&oldName)
		Expect(err).ToNot(HaveOccurred())
		err = conn.QueryRow(ctx, `select name from roles where id = 'role-new'`).Scan(&newName)
		Expect(err).ToNot(HaveOccurred())

		Expect(oldName).To(Equal("test-role"))
		Expect(newName).To(Equal("test-role-2"))
	})

	It("Allows duplicate names across different tenants", func(ctx context.Context) {
		_, err := conn.Exec(ctx, `
			insert into tenants (id, tenant, name, data)
			values ('other-tenant', 'other-tenant', 'other-tenant', '{}')
		`)
		Expect(err).ToNot(HaveOccurred())

		_, err = conn.Exec(ctx, `
			insert into clusters (id, tenant, name, data)
			values
				('cluster-t1', 'my-tenant', 'same-name', '{}'),
				('cluster-t2', 'other-tenant', 'same-name', '{}')
		`)
		Expect(err).ToNot(HaveOccurred())

		err = tool.Migrate(ctx, 96)
		Expect(err).ToNot(HaveOccurred())

		var name1, name2 string
		err = conn.QueryRow(ctx, `select name from clusters where id = 'cluster-t1'`).Scan(&name1)
		Expect(err).ToNot(HaveOccurred())
		err = conn.QueryRow(ctx, `select name from clusters where id = 'cluster-t2'`).Scan(&name2)
		Expect(err).ToNot(HaveOccurred())

		Expect(name1).To(Equal("same-name"))
		Expect(name2).To(Equal("same-name"))
	})

	It("Syncs the data column metadata.name with the name column", func(ctx context.Context) {
		_, err := conn.Exec(ctx, `
			insert into clusters (id, tenant, name, data)
			values ('sync-test', 'my-tenant', '', '{"spec":{}}')
		`)
		Expect(err).ToNot(HaveOccurred())

		err = tool.Migrate(ctx, 96)
		Expect(err).ToNot(HaveOccurred())

		var name string
		var data json.RawMessage
		err = conn.QueryRow(ctx,
			`select name, data from clusters where id = 'sync-test'`,
		).Scan(&name, &data)
		Expect(err).ToNot(HaveOccurred())

		var parsed map[string]interface{}
		Expect(json.Unmarshal(data, &parsed)).To(Succeed())
		metadata := parsed["metadata"].(map[string]interface{})
		Expect(metadata["name"]).To(Equal(name))
	})

	It("Skips rows that already have names", func(ctx context.Context) {
		_, err := conn.Exec(ctx, `
			insert into clusters (id, tenant, name, data)
			values ('keep-name', 'my-tenant', 'my-existing-name', '{}')
		`)
		Expect(err).ToNot(HaveOccurred())

		err = tool.Migrate(ctx, 96)
		Expect(err).ToNot(HaveOccurred())

		var name string
		err = conn.QueryRow(ctx,
			`select name from clusters where id = 'keep-name'`,
		).Scan(&name)
		Expect(err).ToNot(HaveOccurred())
		Expect(name).To(Equal("my-existing-name"))
	})

	It("Skips soft-deleted rows during deduplication", func(ctx context.Context) {
		_, err := conn.Exec(ctx, `
			insert into clusters (id, tenant, name, creation_timestamp, deletion_timestamp, data)
			values
				('active-1', 'my-tenant', 'shared-name', '2024-01-01T00:00:00Z', 'epoch', '{}'),
				('deleted-1', 'my-tenant', 'shared-name', '2024-01-02T00:00:00Z', '2024-06-01T00:00:00Z', '{}')
		`)
		Expect(err).ToNot(HaveOccurred())

		err = tool.Migrate(ctx, 96)
		Expect(err).ToNot(HaveOccurred())

		var activeName, deletedName string
		err = conn.QueryRow(ctx, `select name from clusters where id = 'active-1'`).Scan(&activeName)
		Expect(err).ToNot(HaveOccurred())
		err = conn.QueryRow(ctx, `select name from clusters where id = 'deleted-1'`).Scan(&deletedName)
		Expect(err).ToNot(HaveOccurred())

		Expect(activeName).To(Equal("shared-name"))
		Expect(deletedName).To(Equal("shared-name"))
	})

	It("Is idempotent on a database with no violations", func(ctx context.Context) {
		_, err := conn.Exec(ctx, `
			insert into clusters (id, tenant, name, data)
			values
				('clean-1', 'my-tenant', 'unique-name-a', '{}'),
				('clean-2', 'my-tenant', 'unique-name-b', '{}')
		`)
		Expect(err).ToNot(HaveOccurred())

		err = tool.Migrate(ctx, 96)
		Expect(err).ToNot(HaveOccurred())

		var name1, name2 string
		err = conn.QueryRow(ctx, `select name from clusters where id = 'clean-1'`).Scan(&name1)
		Expect(err).ToNot(HaveOccurred())
		err = conn.QueryRow(ctx, `select name from clusters where id = 'clean-2'`).Scan(&name2)
		Expect(err).ToNot(HaveOccurred())

		Expect(name1).To(Equal("unique-name-a"))
		Expect(name2).To(Equal("unique-name-b"))
	})

	It("Handles the objects table with immutable name trigger", func(ctx context.Context) {
		_, err := conn.Exec(ctx, `
			insert into objects (id, tenant, name, data)
			values ('obj-empty', 'my-tenant', '', '{}')
		`)
		Expect(err).ToNot(HaveOccurred())

		err = tool.Migrate(ctx, 96)
		Expect(err).ToNot(HaveOccurred())

		var name string
		err = conn.QueryRow(ctx,
			`select name from objects where id = 'obj-empty'`,
		).Scan(&name)
		Expect(err).ToNot(HaveOccurred())
		Expect(name).To(Equal("object-objempt"))
		Expect(dns1123LabelRegex.MatchString(name)).To(BeTrue())
	})

	It("Leaves no empty names across multiple tables after migration", func(ctx context.Context) {
		_, err := conn.Exec(ctx, `
			insert into clusters (id, tenant, name, data)
			values ('empty-cluster', 'my-tenant', '', '{}')
		`)
		Expect(err).ToNot(HaveOccurred())
		_, err = conn.Exec(ctx, `
			insert into virtual_networks (id, tenant, name, data)
			values ('empty-vnet', 'my-tenant', '', '{}')
		`)
		Expect(err).ToNot(HaveOccurred())
		_, err = conn.Exec(ctx, `
			insert into roles (id, tenant, name, data)
			values ('empty-role', 'my-tenant', '', '{}')
		`)
		Expect(err).ToNot(HaveOccurred())

		err = tool.Migrate(ctx, 96)
		Expect(err).ToNot(HaveOccurred())

		var count int
		err = conn.QueryRow(ctx, `select count(*) from clusters where name = ''`).Scan(&count)
		Expect(err).ToNot(HaveOccurred())
		Expect(count).To(Equal(0))

		err = conn.QueryRow(ctx, `select count(*) from virtual_networks where name = ''`).Scan(&count)
		Expect(err).ToNot(HaveOccurred())
		Expect(count).To(Equal(0))

		err = conn.QueryRow(ctx, `select count(*) from roles where name = ''`).Scan(&count)
		Expect(err).ToNot(HaveOccurred())
		Expect(count).To(Equal(0))
	})

	It("Normalizes names with underscores, uppercase, and leading hyphens", func(ctx context.Context) {
		_, err := conn.Exec(ctx, `
			insert into clusters (id, tenant, name, data)
			values
				('underscore-id', 'my-tenant', 'my_cluster_name', '{}'),
				('uppercase-id', 'my-tenant', 'MyCluster', '{}'),
				('leading-hyphen-id', 'my-tenant', '-invalid-start', '{}')
		`)
		Expect(err).ToNot(HaveOccurred())

		err = tool.Migrate(ctx, 96)
		Expect(err).ToNot(HaveOccurred())

		var name string
		err = conn.QueryRow(ctx, `select name from clusters where id = 'underscore-id'`).Scan(&name)
		Expect(err).ToNot(HaveOccurred())
		Expect(name).To(Equal("my-cluster-name"))
		Expect(dns1123LabelRegex.MatchString(name)).To(BeTrue())

		err = conn.QueryRow(ctx, `select name from clusters where id = 'uppercase-id'`).Scan(&name)
		Expect(err).ToNot(HaveOccurred())
		Expect(name).To(Equal("mycluster"))
		Expect(dns1123LabelRegex.MatchString(name)).To(BeTrue())

		err = conn.QueryRow(ctx, `select name from clusters where id = 'leading-hyphen-id'`).Scan(&name)
		Expect(err).ToNot(HaveOccurred())
		Expect(name).To(Equal("invalid-start"))
		Expect(dns1123LabelRegex.MatchString(name)).To(BeTrue())
	})

	It("Falls back to generated name when normalization produces empty string", func(ctx context.Context) {
		_, err := conn.Exec(ctx, `
			insert into clusters (id, tenant, name, data)
			values ('all-invalid-id', 'my-tenant', '___', '{}')
		`)
		Expect(err).ToNot(HaveOccurred())

		err = tool.Migrate(ctx, 96)
		Expect(err).ToNot(HaveOccurred())

		var name string
		err = conn.QueryRow(ctx, `select name from clusters where id = 'all-invalid-id'`).Scan(&name)
		Expect(err).ToNot(HaveOccurred())
		Expect(name).To(Equal("cluster-allinva"))
		Expect(dns1123LabelRegex.MatchString(name)).To(BeTrue())
	})

	It("Deduplicates without colliding with existing suffixed names", func(ctx context.Context) {
		_, err := conn.Exec(ctx, `
			insert into clusters (id, tenant, name, creation_timestamp, data)
			values
				('dup-oldest', 'my-tenant', 'foo', '2024-01-01T00:00:00Z', '{}'),
				('dup-newer', 'my-tenant', 'foo', '2024-01-02T00:00:00Z', '{}'),
				('existing-2', 'my-tenant', 'foo-2', '2024-01-03T00:00:00Z', '{}')
		`)
		Expect(err).ToNot(HaveOccurred())

		err = tool.Migrate(ctx, 96)
		Expect(err).ToNot(HaveOccurred())

		var name string
		err = conn.QueryRow(ctx, `select name from clusters where id = 'dup-oldest'`).Scan(&name)
		Expect(err).ToNot(HaveOccurred())
		Expect(name).To(Equal("foo"))

		err = conn.QueryRow(ctx, `select name from clusters where id = 'dup-newer'`).Scan(&name)
		Expect(err).ToNot(HaveOccurred())
		Expect(name).To(Equal("foo-3"))

		err = conn.QueryRow(ctx, `select name from clusters where id = 'existing-2'`).Scan(&name)
		Expect(err).ToNot(HaveOccurred())
		Expect(name).To(Equal("foo-2"))
	})

	It("Deduplicates 63-character names within DNS label limits", func(ctx context.Context) {
		longName := strings.Repeat("a", 63)
		_, err := conn.Exec(ctx, `
			insert into clusters (id, tenant, name, creation_timestamp, data)
			values
				('long-oldest', 'my-tenant', $1, '2024-01-01T00:00:00Z', '{}'),
				('long-newer', 'my-tenant', $1, '2024-01-02T00:00:00Z', '{}')
		`, longName)
		Expect(err).ToNot(HaveOccurred())

		err = tool.Migrate(ctx, 96)
		Expect(err).ToNot(HaveOccurred())

		var name string
		err = conn.QueryRow(ctx, `select name from clusters where id = 'long-oldest'`).Scan(&name)
		Expect(err).ToNot(HaveOccurred())
		Expect(name).To(Equal(longName))

		err = conn.QueryRow(ctx, `select name from clusters where id = 'long-newer'`).Scan(&name)
		Expect(err).ToNot(HaveOccurred())
		Expect(name).To(Equal(strings.Repeat("a", 61) + "-2"))
		Expect(len(name)).To(BeNumerically("<=", 63))
		Expect(dns1123LabelRegex.MatchString(name)).To(BeTrue())
	})
})

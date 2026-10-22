package migrations

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5/pgconn"
	. "github.com/onsi/ginkgo/v2/dsl/core"
	. "github.com/onsi/gomega"
)

var _ = DescribeMigration("Hold ExternalIP capacity until archive", func() {
	migrate := func(ctx context.Context) {
		Expect(tool.Migrate(ctx, 126)).To(Succeed())
	}

	insertPool := func(ctx context.Context, id string, total, allocated, available int) {
		data := `{"status":{"total":"` + itoa(total) + `","allocated":"` + itoa(allocated) + `","available":"` + itoa(available) + `"}}`
		_, err := conn.Exec(ctx,
			`insert into external_ip_pools (id, name, tenant, data) values ($1, $1, 'system', $2::jsonb)`,
			id, data,
		)
		Expect(err).NotTo(HaveOccurred())
	}

	insertExternalIP := func(ctx context.Context, id, poolID string, deleting bool) {
		_, err := conn.Exec(ctx,
			`insert into external_ips (id, name, tenant, data) values ($1, $1, 'system', $2::jsonb)`,
			id, `{"spec":{"pool":{"id":"`+poolID+`"}}}`,
		)
		Expect(err).NotTo(HaveOccurred())
		if deleting {
			_, err = conn.Exec(ctx, `update external_ips set deletion_timestamp=now() where id=$1`, id)
			Expect(err).NotTo(HaveOccurred())
		}
	}

	It("indexes ExternalIP consumers across lifecycle states", func(ctx context.Context) {
		migrate(ctx)

		for _, indexName := range []string{
			"external_ip_attachments_by_external_ip_ref",
			"external_ip_attachments_by_external_ip_name_ref",
			"nat_gateways_by_external_ip_ref",
			"nat_gateways_by_external_ip_name_ref",
		} {
			var indexDef string
			err := conn.QueryRow(ctx,
				`select indexdef from pg_indexes where schemaname=current_schema() and indexname=$1`,
				indexName,
			).Scan(&indexDef)
			Expect(err).NotTo(HaveOccurred())
			Expect(indexDef).To(ContainSubstring("external_ip"))
			Expect(indexDef).NotTo(ContainSubstring(" WHERE "))
		}
	})

	It("backfills capacity from every unarchived ExternalIP", func(ctx context.Context) {
		insertPool(ctx, "pool-backfill", 4, 1, 3)
		insertExternalIP(ctx, "eip-active", "pool-backfill", false)
		insertExternalIP(ctx, "eip-deleting", "pool-backfill", true)
		migrate(ctx)

		var allocated, available int
		err := conn.QueryRow(ctx,
			`select (data->'status'->>'allocated')::int, (data->'status'->>'available')::int from external_ip_pools where id=$1`,
			"pool-backfill",
		).Scan(&allocated, &available)
		Expect(err).NotTo(HaveOccurred())
		Expect(allocated).To(Equal(2))
		Expect(available).To(Equal(2))
	})

	It("returns pool capacity only when an ExternalIP row is archived", func(ctx context.Context) {
		migrate(ctx)
		insertPool(ctx, "pool-archive", 4, 2, 2)
		insertExternalIP(ctx, "eip-one", "pool-archive", false)
		insertExternalIP(ctx, "eip-two", "pool-archive", true)

		var allocated, available int
		readCapacity := func() {
			err := conn.QueryRow(ctx,
				`select (data->'status'->>'allocated')::int, (data->'status'->>'available')::int from external_ip_pools where id=$1`,
				"pool-archive",
			).Scan(&allocated, &available)
			Expect(err).NotTo(HaveOccurred())
		}

		readCapacity()
		Expect(allocated).To(Equal(2))
		Expect(available).To(Equal(2))
		_, err := conn.Exec(ctx, `delete from external_ips where id=$1`, "eip-one")
		Expect(err).NotTo(HaveOccurred())
		readCapacity()
		Expect(allocated).To(Equal(1))
		Expect(available).To(Equal(3))
		_, err = conn.Exec(ctx, `delete from external_ips where id=$1`, "eip-one")
		Expect(err).NotTo(HaveOccurred())
		readCapacity()
		Expect(allocated).To(Equal(1))
		Expect(available).To(Equal(3))
	})

	It("keeps a terminating Attachment exclusive until archive", func(ctx context.Context) {
		migrate(ctx)
		insertPool(ctx, "pool-attachment", 4, 1, 3)
		insertExternalIP(ctx, "eip-attachment", "pool-attachment", false)
		_, err := conn.Exec(ctx, `insert into compute_instances (id, name, tenant, data) values ('ci-old', 'ci-old', 'system', '{}'::jsonb)`)
		Expect(err).NotTo(HaveOccurred())
		_, err = conn.Exec(ctx, `insert into virtual_networks (id, name, tenant, data) values ('vn-attachment', 'vn-attachment', 'system', '{}'::jsonb)`)
		Expect(err).NotTo(HaveOccurred())
		_, err = conn.Exec(ctx, `insert into external_ip_attachments (id, name, tenant, data) values ('att-old', 'att-old', 'system', $1::jsonb)`,
			`{"spec":{"external_ip":{"id":"eip-attachment"},"compute_instance":{"id":"ci-old"}}}`)
		Expect(err).NotTo(HaveOccurred())
		_, err = conn.Exec(ctx, `update external_ip_attachments set deletion_timestamp=now() where id='att-old'`)
		Expect(err).NotTo(HaveOccurred())

		_, err = conn.Exec(ctx,
			`insert into nat_gateways (id, name, tenant, data) values ('nat-new', 'nat-new', 'system', $1::jsonb)`,
			`{"spec":{"virtual_network":{"id":"vn-attachment"},"external_ip":{"id":"eip-attachment"}}}`,
		)
		expectExternalIPInUse(err, "att-old")
	})

	It("keeps a terminating NATGateway exclusive until archive", func(ctx context.Context) {
		migrate(ctx)
		insertPool(ctx, "pool-nat", 4, 1, 3)
		insertExternalIP(ctx, "eip-nat", "pool-nat", false)
		_, err := conn.Exec(ctx, `insert into virtual_networks (id, name, tenant, data) values ('vn-nat', 'vn-nat', 'system', '{}'::jsonb)`)
		Expect(err).NotTo(HaveOccurred())
		_, err = conn.Exec(ctx,
			`insert into nat_gateways (id, name, tenant, data) values ('nat-old', 'nat-old', 'system', $1::jsonb)`,
			`{"spec":{"virtual_network":{"id":"vn-nat"},"external_ip":{"id":"eip-nat"}}}`,
		)
		Expect(err).NotTo(HaveOccurred())
		_, err = conn.Exec(ctx, `update nat_gateways set deletion_timestamp=now() where id='nat-old'`)
		Expect(err).NotTo(HaveOccurred())
		_, err = conn.Exec(ctx, `insert into compute_instances (id, name, tenant, data) values ('ci-new', 'ci-new', 'system', '{}'::jsonb)`)
		Expect(err).NotTo(HaveOccurred())

		_, err = conn.Exec(ctx,
			`insert into external_ip_attachments (id, name, tenant, data) values ('att-new', 'att-new', 'system', $1::jsonb)`,
			`{"spec":{"external_ip":{"id":"eip-nat"},"compute_instance":{"id":"ci-new"}}}`,
		)
		expectExternalIPInUse(err, "nat-old")
	})

	It("blocks pool deletion while a terminating ExternalIP remains unarchived", func(ctx context.Context) {
		migrate(ctx)
		insertPool(ctx, "pool-live", 4, 1, 3)
		insertExternalIP(ctx, "eip-live", "pool-live", true)

		_, err := conn.Exec(ctx, `update external_ip_pools set deletion_timestamp=now() where id='pool-live'`)
		Expect(err).To(HaveOccurred())
		var pgErr *pgconn.PgError
		Expect(errors.As(err, &pgErr)).To(BeTrue())
		Expect(pgErr.Code).To(Equal("Z0003"))
		Expect(pgErr.Message).To(ContainSubstring("pool-live"))
	})
})

func expectExternalIPInUse(err error, expectedID string) {
	Expect(err).To(HaveOccurred())
	var pgErr *pgconn.PgError
	Expect(errors.As(err, &pgErr)).To(BeTrue())
	Expect(pgErr.Code).To(Equal("Z0004"))
	Expect(pgErr.Message).To(ContainSubstring(expectedID))
}

func itoa(value int) string {
	return fmt.Sprintf("%d", value)
}

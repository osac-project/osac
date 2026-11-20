/*
Copyright (c) 2026 Red Hat Inc.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

  http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on an
"AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the specific
language governing permissions and limitations under the License.
*/

package migrations

import (
	"context"
	"errors"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	. "github.com/onsi/ginkgo/v2/dsl/core"
	. "github.com/onsi/gomega"
)

var _ = DescribeMigration("Tenant lifecycle triggers", func() {
	It("closes active billing history at accepted deletion and finalizes its identity after physical removal", func(ctx context.Context) {
		_, err := conn.Exec(ctx, `
			insert into tenants (id, name, tenant, creation_timestamp, finalizers, data)
				values ('billed', 'billed', 'billed', '2026-01-01Z', array['cleanup'], '{}');
			insert into billing_accounts (id, name, tenant, data)
				values ('account', 'ba-' || left(encode(sha256(convert_to('account', 'UTF8')), 'hex'), 60), 'system',
				'{"spec":{},"status":{"provider_key":"m360","provider_account_reference":"native-account",
				"state":"BILLING_ACCOUNT_STATE_ACTIVE","state_observation_timestamp":"2026-01-01T00:00:00Z"}}');
			insert into tenant_billing_assignments (id, name, tenant, annotations, data)
				values ('interval', 'assignment', 'billed', '{"osac.openshift.io/tenant":"billed"}',
				jsonb_build_object('spec', jsonb_build_object(
				  'tenant', jsonb_build_object('id','billed','name','billed'),
				  'billing_account', jsonb_build_object('id','account','name',
				    'ba-' || left(encode(sha256(convert_to('account', 'UTF8')), 'hex'), 60))),
				  'status', jsonb_build_object('connection_state',
				    'TENANT_BILLING_ASSIGNMENT_CONNECTION_STATE_CONNECTED',
				    'effective_from_timestamp','2026-01-01T00:00:00Z')));`)
		Expect(err).NotTo(HaveOccurred())
		Expect(tool.Migrate(ctx, 163)).To(Succeed())

		tx, err := conn.Begin(ctx)
		Expect(err).NotTo(HaveOccurred())
		var actor string
		Expect(tx.QueryRow(ctx, `select set_config('osac.tenant_deletion_actor', 'first-admin', true)`).Scan(&actor)).To(Succeed())
		_, err = tx.Exec(ctx, `update tenants set deletion_timestamp = '2026-01-02Z' where id = 'billed'`)
		Expect(err).NotTo(HaveOccurred())
		Expect(tx.Commit(ctx)).To(Succeed())

		var deletion, effectiveTo *string
		var message string
		var deletedBy *string
		var finalizedAt *string
		var billingRetained bool
		Expect(conn.QueryRow(ctx, `select t.deletion_timestamp::text, h.effective_to::text,
			h.data#>>'{status,message}', d.deleted_by, d.finalized_at::text, d.billing_retained
			from tenants t join tenant_billing_assignments h on h.tenant_id = t.name
			join deleted_tenant_identities d on d.tenant_name = t.name where t.id = 'billed'`).
			Scan(&deletion, &effectiveTo, &message, &deletedBy, &finalizedAt, &billingRetained)).To(Succeed())
		Expect(deletion).NotTo(BeNil())
		Expect(effectiveTo).To(Equal(deletion))
		Expect(message).To(Equal("TENANT_DELETED: tenant deleted; assignment retained as billing history"))
		Expect(deletedBy).To(Equal(ptr("first-admin")))
		Expect(finalizedAt).To(BeNil(), "the tenant still has a finalizer")
		Expect(billingRetained).To(BeTrue())

		_, err = conn.Exec(ctx, `delete from projects where tenant = 'billed';
			update tenants set finalizers = '{}' where id = 'billed';
			delete from tenants where id = 'billed'`)
		Expect(err).NotTo(HaveOccurred())
		var tenantCount int
		Expect(conn.QueryRow(ctx, `select count(*) from tenants where id = 'billed'`).Scan(&tenantCount)).To(Succeed())
		Expect(tenantCount).To(BeZero())
		Expect(conn.QueryRow(ctx, `select finalized_at::text, deleted_by, billing_retained
			from deleted_tenant_identities where tenant_name = 'billed'`).
			Scan(&finalizedAt, &deletedBy, &billingRetained)).To(Succeed())
		Expect(finalizedAt).NotTo(BeNil())
		Expect(deletedBy).To(Equal(ptr("first-admin")), "finalizer identity must not replace the original deleter")
		Expect(billingRetained).To(BeTrue())
	})

	It("rejects new tenant-owned rows after deletion while allowing existing-row cleanup and serializing creates", func(ctx context.Context) {
		_, err := conn.Exec(ctx, `
			insert into tenants (id, name, tenant, creation_timestamp, data) values
				('live', 'live', 'live', '2026-01-01Z', '{}'),
				('deleting', 'deleting', 'deleting', '2026-01-01Z', '{}'),
				('racing', 'racing', 'racing', '2026-01-01Z', '{}');`)
		Expect(err).NotTo(HaveOccurred())
		Expect(tool.Migrate(ctx, 163)).To(Succeed())

		_, err = conn.Exec(ctx, `insert into objects (id, name, tenant, data) values ('live-object', 'live-object', 'live', '{}')`)
		Expect(err).NotTo(HaveOccurred())
		_, err = conn.Exec(ctx, `insert into objects (id, name, tenant, data) values ('deleting-object', 'deleting-object', 'deleting', '{}')`)
		Expect(err).NotTo(HaveOccurred())
		_, err = conn.Exec(ctx, `update tenants set deletion_timestamp = clock_timestamp() where name = 'deleting'`)
		Expect(err).NotTo(HaveOccurred())
		_, err = conn.Exec(ctx, `insert into objects (id, name, tenant, data) values ('blocked-object', 'blocked-object', 'deleting', '{}')`)
		expectSQLState(err, "Z0002")
		_, err = conn.Exec(ctx, `update objects set data = '{"cleaned":true}' where id = 'deleting-object'`)
		Expect(err).NotTo(HaveOccurred(), "finalizer cleanup may update an existing object")

		other, err := pgx.ConnectConfig(ctx, conn.Config().Copy())
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(other.Close)
		tx, err := conn.Begin(ctx)
		Expect(err).NotTo(HaveOccurred())
		_, err = tx.Exec(ctx, `insert into objects (id, name, tenant, data) values ('racing-object', 'racing-object', 'racing', '{}')`)
		Expect(err).NotTo(HaveOccurred())
		_, err = other.Exec(ctx, `set lock_timeout = '100ms'`)
		Expect(err).NotTo(HaveOccurred())
		_, err = other.Exec(ctx, `update tenants set deletion_timestamp = clock_timestamp() where name = 'racing'`)
		expectSQLState(err, "55P03")
		_, err = other.Exec(ctx, `set lock_timeout = '0'`)
		Expect(err).NotTo(HaveOccurred())
		Expect(tx.Commit(ctx)).To(Succeed())
		_, err = other.Exec(ctx, `update tenants set deletion_timestamp = clock_timestamp() where name = 'racing'`)
		Expect(err).NotTo(HaveOccurred())
		_, err = other.Exec(ctx, `insert into objects (id, name, tenant, data) values ('too-late', 'too-late', 'racing', '{}')`)
		expectSQLState(err, "Z0002")
	})

	It("captures direct physical tenant deletion and closes any active billing interval", func(ctx context.Context) {
		_, err := conn.Exec(ctx, `
			insert into tenants (id, name, tenant, creation_timestamp, data)
				values ('direct-delete', 'direct-delete', 'direct-delete', '2026-01-01Z', '{}');
			insert into billing_accounts (id, name, tenant, data)
				values ('direct-account', 'ba-' || left(encode(sha256(convert_to('direct-account', 'UTF8')), 'hex'), 60),
				'system', '{"spec":{},"status":{"provider_key":"m360","provider_account_reference":"native-direct",
				"state":"BILLING_ACCOUNT_STATE_ACTIVE","state_observation_timestamp":"2026-01-01T00:00:00Z"}}');
			insert into tenant_billing_assignments (id, name, tenant, annotations, data)
				values ('direct-interval', 'direct-assignment', 'direct-delete',
				'{"osac.openshift.io/tenant":"direct-delete"}',
				jsonb_build_object('spec', jsonb_build_object(
				  'tenant', jsonb_build_object('id','direct-delete','name','direct-delete'),
				  'billing_account', jsonb_build_object('id','direct-account','name',
				    'ba-' || left(encode(sha256(convert_to('direct-account', 'UTF8')), 'hex'), 60))),
				  'status', jsonb_build_object('connection_state',
				    'TENANT_BILLING_ASSIGNMENT_CONNECTION_STATE_CONNECTED',
				    'effective_from_timestamp','2026-01-01T00:00:00Z')));`)
		Expect(err).NotTo(HaveOccurred())
		Expect(tool.Migrate(ctx, 163)).To(Succeed())

		_, err = conn.Exec(ctx, `delete from projects where tenant = 'direct-delete';
			delete from tenants where id = 'direct-delete'`)
		Expect(err).NotTo(HaveOccurred())
		var tenantCount int
		Expect(conn.QueryRow(ctx, `select count(*) from tenants where id = 'direct-delete'`).Scan(&tenantCount)).To(Succeed())
		Expect(tenantCount).To(BeZero())

		var deletion, finalized, effectiveTo, deletedBy *string
		var message string
		var billingRetained bool
		Expect(conn.QueryRow(ctx, `select d.deletion_timestamp::text, d.finalized_at::text, d.deleted_by,
			d.billing_retained, h.effective_to::text, h.data#>>'{status,message}'
			from deleted_tenant_identities d join tenant_billing_assignments h on h.tenant_id = d.tenant_name
			where d.tenant_name = 'direct-delete'`).
			Scan(&deletion, &finalized, &deletedBy, &billingRetained, &effectiveTo, &message)).To(Succeed())
		Expect(deletion).NotTo(BeNil())
		Expect(finalized).NotTo(BeNil())
		Expect(deletedBy).To(BeNil(), "direct SQL deletion has no authenticated actor")
		Expect(billingRetained).To(BeTrue())
		Expect(effectiveTo).To(Equal(deletion))
		Expect(message).To(Equal("TENANT_DELETED: tenant deleted; assignment retained as billing history"))
	})

	It("closes billing history already left open by tenants deletion-marked before the upgrade", func(ctx context.Context) {
		_, err := conn.Exec(ctx, `
			insert into tenants (id, name, tenant, creation_timestamp, deletion_timestamp, data) values
				('predeleted', 'predeleted', 'predeleted', '2026-01-01Z', '2026-01-02Z', '{}'),
				('empty-deleted', 'empty-deleted', 'empty-deleted', '2026-01-01Z', '2026-01-02Z', '{}'),
				('live', 'live', 'live', '2026-01-01Z', 'epoch', '{}'),
				('reuse', 'reuse', 'reuse', '2026-01-01Z', 'epoch', '{}');
			insert into billing_accounts (id, name, tenant, data) values
				('released-account', 'ba-' || left(encode(sha256(convert_to('released-account', 'UTF8')), 'hex'), 60),
				'system', '{"spec":{},"status":{"provider_key":"m360","provider_account_reference":"native-released",
				"state":"BILLING_ACCOUNT_STATE_ACTIVE","state_observation_timestamp":"2026-01-01T00:00:00Z"}}'),
				('live-account', 'ba-' || left(encode(sha256(convert_to('live-account', 'UTF8')), 'hex'), 60),
				'system', '{"spec":{},"status":{"provider_key":"m360","provider_account_reference":"native-live",
				"state":"BILLING_ACCOUNT_STATE_ACTIVE","state_observation_timestamp":"2026-01-01T00:00:00Z"}}');
			insert into tenant_billing_assignments (id, name, tenant, annotations, data) values
				('predeleted-interval', 'predeleted-assignment', 'predeleted',
				'{"osac.openshift.io/tenant":"predeleted"}',
				jsonb_build_object('spec', jsonb_build_object(
				  'tenant', jsonb_build_object('id','predeleted','name','predeleted'),
				  'billing_account', jsonb_build_object('id','released-account','name',
				    'ba-' || left(encode(sha256(convert_to('released-account', 'UTF8')), 'hex'), 60))),
				  'status', jsonb_build_object('connection_state',
				    'TENANT_BILLING_ASSIGNMENT_CONNECTION_STATE_CONNECTED',
				    'effective_from_timestamp','2026-01-01T00:00:00Z'))),
				('live-interval', 'live-assignment', 'live',
				'{"osac.openshift.io/tenant":"live"}',
				jsonb_build_object('spec', jsonb_build_object(
				  'tenant', jsonb_build_object('id','live','name','live'),
				  'billing_account', jsonb_build_object('id','live-account','name',
				    'ba-' || left(encode(sha256(convert_to('live-account', 'UTF8')), 'hex'), 60))),
				  'status', jsonb_build_object('connection_state',
				    'TENANT_BILLING_ASSIGNMENT_CONNECTION_STATE_CONNECTED',
				    'effective_from_timestamp','2026-01-01T00:00:00Z')));`)
		Expect(err).NotTo(HaveOccurred())
		Expect(tool.Migrate(ctx, 163)).To(Succeed())

		var deletion, effectiveTo, effectiveToTimestamp *string
		Expect(conn.QueryRow(ctx, `select t.deletion_timestamp::text, h.effective_to::text,
			h.data#>>'{status,effective_to_timestamp}'
			from tenants t join tenant_billing_assignments h on h.tenant_id = t.name
			where t.id = 'predeleted'`).
			Scan(&deletion, &effectiveTo, &effectiveToTimestamp)).To(Succeed())
		Expect(deletion).NotTo(BeNil())
		Expect(effectiveTo).To(Equal(deletion), "the backfill must close the interval at the stored deletion boundary")
		Expect(effectiveToTimestamp).To(Equal(ptr("2026-01-02T00:00:00.000000Z")),
			"the status must report the same instant in the migration timestamp format")

		var liveEffectiveTo, liveEffectiveToTimestamp *string
		Expect(conn.QueryRow(ctx, `select h.effective_to::text, h.data#>>'{status,effective_to_timestamp}'
			from tenant_billing_assignments h where h.id = 'live-interval'`).
			Scan(&liveEffectiveTo, &liveEffectiveToTimestamp)).To(Succeed())
		Expect(liveEffectiveTo).To(BeNil(), "the backfill must not close live tenant intervals")
		Expect(liveEffectiveToTimestamp).To(BeNil())

		var emptyIntervals, emptyTenants int
		Expect(conn.QueryRow(ctx, `select count(*) from tenant_billing_assignments
			where tenant_id = 'empty-deleted'`).Scan(&emptyIntervals)).To(Succeed())
		Expect(emptyIntervals).To(BeZero(), "a tenant without intervals needs no backfill")
		Expect(conn.QueryRow(ctx, `select count(*) from tenants where id = 'empty-deleted'`).
			Scan(&emptyTenants)).To(Succeed())
		Expect(emptyTenants).To(Equal(1))

		// 'live' already owns an open interval, so reuse is proved on a live tenant that has none.
		tx, err := conn.Begin(ctx)
		Expect(err).NotTo(HaveOccurred())
		_, err = tx.Exec(ctx, `insert into tenant_billing_assignments (id, name, tenant, annotations, data)
			values ('reuse-interval', 'reuse-assignment', 'reuse',
			'{"osac.openshift.io/tenant":"reuse"}',
			jsonb_build_object('spec', jsonb_build_object(
			  'tenant', jsonb_build_object('id','reuse','name','reuse'),
			  'billing_account', jsonb_build_object('id','released-account','name',
			    'ba-' || left(encode(sha256(convert_to('released-account', 'UTF8')), 'hex'), 60))),
			  'status', jsonb_build_object('connection_state',
			    'TENANT_BILLING_ASSIGNMENT_CONNECTION_STATE_CONNECTED',
			    'effective_from_timestamp','2026-01-03T00:00:00Z')));`)
		Expect(err).NotTo(HaveOccurred(), "the released account must back a second active interval")
		Expect(tx.Rollback(ctx)).To(Succeed())
	})
})

func expectSQLState(err error, code string) {
	GinkgoHelper()
	var pgErr *pgconn.PgError
	Expect(errors.As(err, &pgErr)).To(BeTrue(), "expected PostgreSQL error %s, got %v", code, err)
	Expect(pgErr.Code).To(Equal(code))
}

func ptr(value string) *string { return &value }

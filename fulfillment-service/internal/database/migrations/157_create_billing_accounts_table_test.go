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
	"time"

	. "github.com/onsi/ginkgo/v2/dsl/core"
	. "github.com/onsi/gomega"
)

var _ = DescribeMigration("Create billing accounts table", func() {
	It("stores provider observations in data and owns standard creation defaults", func(ctx context.Context) {
		Expect(tool.Migrate(ctx, 157)).To(Succeed())
		for _, id := range []string{"new", "é/immutable-id"} {
			_, err := conn.Exec(ctx, insertBillingAccount, id, billingAccountName(id), billingAccountData(id, nil))
			Expect(err).ToNot(HaveOccurred())
		}
		var created time.Time
		var version int
		var name, tenant, creator, providerKey, providerReference, displayName string
		Expect(conn.QueryRow(ctx, `select creation_timestamp, version, name, tenant, creator,
			data -> 'status' ->> 'provider_key', data -> 'status' ->> 'provider_account_reference',
			data -> 'status' ->> 'display_name' from billing_accounts where id = 'new'`).
			Scan(&created, &version, &name, &tenant, &creator, &providerKey, &providerReference, &displayName)).To(Succeed())
		Expect(created.After(time.Unix(0, 0))).To(BeTrue())
		Expect(version).To(BeZero())
		Expect(name).To(Equal(billingAccountName("new")))
		Expect(tenant).To(Equal("system"))
		Expect(creator).To(Equal("system"))
		Expect(providerKey).To(Equal("m360"))
		Expect(providerReference).To(Equal("native-new"))
		Expect(displayName).To(Equal("New label"))

		// Provider observations remain in the JSON body when it changes.
		_, err := conn.Exec(ctx, `update billing_accounts set data = jsonb_set(data, '{status,display_name}', '"Changed"'),
			version = version + 1 where id = 'new'`)
		Expect(err).ToNot(HaveOccurred())
		Expect(conn.QueryRow(ctx, `select data -> 'status' ->> 'display_name', version from billing_accounts where id = 'new'`).
			Scan(&displayName, &version)).To(Succeed())
		Expect(displayName).To(Equal("Changed"))
		Expect(version).To(Equal(1))

		rejectStatement(ctx, "23514", "", insertBillingAccount, "wrong-name", "label-derived",
			billingAccountData("wrong-name", nil))
		rejectStatement(ctx, "23502", "", `update billing_accounts set labels = null where id = 'new'`)

		var tenantFK bool
		Expect(conn.QueryRow(ctx, `select exists (select 1 from pg_constraint
			where conrelid = 'billing_accounts'::regclass and conname = 'billing_accounts_tenant_fk' and contype = 'f')`).
			Scan(&tenantFK)).To(Succeed())
		Expect(tenantFK).To(BeTrue())
	})

	It("enforces provider account uniqueness using status JSON", func(ctx context.Context) {
		Expect(tool.Migrate(ctx, 157)).To(Succeed())
		_, err := conn.Exec(ctx, insertBillingAccount, "ba-1", billingAccountName("ba-1"), billingAccountData("ba-1", nil))
		Expect(err).ToNot(HaveOccurred())
		rejectStatement(ctx, "23505", "billing_accounts_unique_provider_account", insertBillingAccount,
			"ba-2", billingAccountName("ba-2"), billingAccountData("ba-1", nil))
	})

	It("allows the same provider_account_reference under a different provider_key", func(ctx context.Context) {
		Expect(tool.Migrate(ctx, 157)).To(Succeed())
		_, err := conn.Exec(ctx, insertBillingAccount, "ba-1", billingAccountName("ba-1"), billingAccountData("ba-1", nil))
		Expect(err).ToNot(HaveOccurred())
		_, err = conn.Exec(ctx, insertBillingAccount, "ba-2", billingAccountName("ba-2"),
			`{"spec":{},"status":{"provider_key":"fake","provider_account_reference":"native-ba-1",
			"state":"BILLING_ACCOUNT_STATE_ACTIVE","state_observation_timestamp":"2026-03-01T01:02:03.000004Z"}}`)
		Expect(err).ToNot(HaveOccurred())
	})

	It("rejects invalid provider observation payloads", func(ctx context.Context) {
		Expect(tool.Migrate(ctx, 157)).To(Succeed())
		validStatus := `"state":"BILLING_ACCOUNT_STATE_ACTIVE","state_observation_timestamp":"2026-03-01T01:02:03.000004Z"`
		rejectStatement(ctx, "23514", "invalid billing account payload", insertBillingAccount,
			"unknown-field", billingAccountName("unknown-field"), `{"spec":{},"status":{`+validStatus+`},"extra":true}`)
		rejectStatement(ctx, "23514", "invalid billing account field type", insertBillingAccount,
			"wrong-type", billingAccountName("wrong-type"), `{"spec":{},"status":{"provider_key":7,`+validStatus+`}}`)
		rejectStatement(ctx, "23514", "invalid billing account status", insertBillingAccount,
			"wrong-state", billingAccountName("wrong-state"), `{"spec":{},"status":{"provider_key":"m360",
			"provider_account_reference":"native","state":"OTHER","state_observation_timestamp":"2026-03-01T01:02:03Z"}}`)
		rejectStatement(ctx, "23514", "invalid protobuf billing timestamp", insertBillingAccount,
			"wrong-timestamp", billingAccountName("wrong-timestamp"), `{"spec":{},"status":{"provider_key":"m360",
			"provider_account_reference":"native","state":"BILLING_ACCOUNT_STATE_ACTIVE",
			"state_observation_timestamp":"2026-03-01 01:02:03Z"}}`)
		rejectStatement(ctx, "23514", "inconsistent billing account insert", `insert into billing_accounts
			(id, name, creator, data) values ('wrong-creator', $1, 'request-principal', $2::jsonb)`,
			billingAccountName("wrong-creator"), billingAccountData("wrong-creator", nil))
	})

	It("rejects invalid provider observation timestamps", func(ctx context.Context) {
		Expect(tool.Migrate(ctx, 157)).To(Succeed())
		for _, value := range invalidBillingTimestamps {
			pgErr := rejectStatement(ctx, "23514", "", `insert into billing_accounts (id, name, tenant, creator, data)
				values ($1, $2, 'system', 'system', jsonb_set($3::jsonb,
					'{status,state_observation_timestamp}', to_jsonb($4::text)))`,
				"invalid-timestamp", billingAccountName("invalid-timestamp"),
				billingAccountData("invalid-timestamp", nil), value)
			Expect(pgErr.Message).To(Equal("invalid protobuf billing timestamp"))
		}
	})

	It("enforces attribution uniqueness on status JSON and permits absent ids", func(ctx context.Context) {
		Expect(tool.Migrate(ctx, 157)).To(Succeed())
		for i, token := range []string{"ext-1", " ext-1 ", "", " \t "} {
			id := []string{"plain", "padded", "empty", "whitespace"}[i]
			_, err := conn.Exec(ctx, insertBillingAccount, id, billingAccountName(id), billingAccountData(id, &token))
			Expect(err).ToNot(HaveOccurred())
			duplicate := "duplicate-" + id
			rejectStatement(ctx, "23505", "billing_accounts_unique_external_billing_account_id",
				insertBillingAccount, duplicate, billingAccountName(duplicate), billingAccountData(duplicate, &token))
		}
		for _, id := range []string{"absent-a", "absent-b"} {
			_, err := conn.Exec(ctx, insertBillingAccount, id, billingAccountName(id), billingAccountData(id, nil))
			Expect(err).ToNot(HaveOccurred(), "multiple status payloads without an attribution id are allowed")
		}
	})

	It("rejects native identity changes and established token rotation without poisoning the transaction", func(ctx context.Context) {
		Expect(tool.Migrate(ctx, 157)).To(Succeed())
		token := " exact "
		_, err := conn.Exec(ctx, insertBillingAccount, "immutable", billingAccountName("immutable"),
			billingAccountData("immutable", &token))
		Expect(err).ToNot(HaveOccurred())

		rejectStatement(ctx, "23514", "immutable billing account identity", `update billing_accounts set data =
			jsonb_set(data, '{status,provider_key}', '"another"') where id = 'immutable'`)
		rejectStatement(ctx, "23514", "immutable billing account identity", `update billing_accounts set data =
			jsonb_set(data, '{status,provider_account_reference}', '"another"') where id = 'immutable'`)
		rejectStatement(ctx, "23514", "established billing attribution is immutable", `update billing_accounts set data =
			jsonb_set(data, '{status,external_billing_account_id}', '"exact"') where id = 'immutable'`)
		rejectStatement(ctx, "23514", "established billing attribution is immutable", `update billing_accounts set data =
			data #- '{status,external_billing_account_id}' where id = 'immutable'`)

		_, err = conn.Exec(ctx, `update billing_accounts set data = jsonb_set(data, '{status,display_name}', '"Updated"')
			where id = 'immutable'`)
		Expect(err).ToNot(HaveOccurred())
	})

	It("keys the active companion on deletion, not on observation", func(ctx context.Context) {
		Expect(tool.Migrate(ctx, 157)).To(Succeed())
		_, err := conn.Exec(ctx, insertBillingAccount, "ba-1", billingAccountName("ba-1"), billingAccountData("ba-1", nil))
		Expect(err).ToNot(HaveOccurred())
		var active bool
		Expect(conn.QueryRow(ctx, `select exists(select 1 from active_billing_accounts where id = 'ba-1')`).
			Scan(&active)).To(Succeed())
		Expect(active).To(BeTrue())
		_, err = conn.Exec(ctx, `update billing_accounts set deletion_timestamp = clock_timestamp() where id = 'ba-1'`)
		Expect(err).ToNot(HaveOccurred())
		Expect(conn.QueryRow(ctx, `select exists(select 1 from active_billing_accounts where id = 'ba-1')`).
			Scan(&active)).To(Succeed())
		Expect(active).To(BeFalse())
	})

	It("publishes billing account writes", func(ctx context.Context) {
		Expect(tool.Migrate(ctx, 157)).To(Succeed())
		var before, after int
		Expect(conn.QueryRow(ctx, `select count(*) from changes`).Scan(&before)).To(Succeed())
		_, err := conn.Exec(ctx, insertBillingAccount, "published", billingAccountName("published"),
			billingAccountData("published", nil))
		Expect(err).ToNot(HaveOccurred())
		_, err = conn.Exec(ctx, `update billing_accounts set data = jsonb_set(data, '{status,display_name}', '"Updated"')
			where id = 'published'`)
		Expect(err).ToNot(HaveOccurred())
		Expect(conn.QueryRow(ctx, `select count(*) from changes`).Scan(&after)).To(Succeed())
		Expect(after).To(Equal(before+2), "one event is emitted for insert and one for update")
		var triggers int
		Expect(conn.QueryRow(ctx, `select count(*) from pg_trigger t join pg_proc p on p.oid = t.tgfoid
			where t.tgrelid = 'billing_accounts'::regclass and p.proname = 'enqueue_change'
			and not t.tgisinternal`).Scan(&triggers)).To(Succeed())
		Expect(triggers).To(Equal(1))
	})

})

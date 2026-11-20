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
	"time"

	. "github.com/onsi/ginkgo/v2/dsl/core"
	. "github.com/onsi/gomega"
)

var _ = DescribeMigration("Create billing accounts table", func() {
	It("derives every provider projection from the payload and owns creation defaults", func(ctx context.Context) {
		Expect(tool.Migrate(ctx, 152)).To(Succeed())
		for _, id := range []string{"new", "é/immutable-id"} {
			_, err := conn.Exec(ctx, insertBillingAccount, id, billingAccountName(id), billingAccountData(id, nil))
			Expect(err).ToNot(HaveOccurred())
		}
		var created time.Time
		var version int
		var name, displayName, state, projection string
		Expect(conn.QueryRow(ctx, `select creation_timestamp, version, name, display_name, state,
			provider_account_reference from billing_accounts where id = 'new'`).
			Scan(&created, &version, &name, &displayName, &state, &projection)).To(Succeed())
		Expect(created.After(time.Unix(0, 0))).To(BeTrue())
		Expect(version).To(BeZero())
		Expect(name).To(Equal(billingAccountName("new")))
		Expect(displayName).To(Equal("New label"))
		Expect(state).To(Equal("ACTIVE"))
		Expect(projection).To(Equal("native-new"))

		// The payload is the only truth: changing it moves the projection one way.
		_, err := conn.Exec(ctx, `update billing_accounts set data = jsonb_set(data, '{status,display_name}', '"Changed"'),
			version = version + 1 where id = 'new'`)
		Expect(err).ToNot(HaveOccurred())
		Expect(conn.QueryRow(ctx, `select display_name, version from billing_accounts where id = 'new'`).
			Scan(&projection, &version)).To(Succeed())
		Expect(projection).To(Equal("Changed"))
		Expect(version).To(Equal(1))

		rejectStatement(ctx, "23514", "", `update billing_accounts set display_name = 'second truth' where id = 'new'`)
		rejectStatement(ctx, "23514", "", `update billing_accounts set data = jsonb_set(data,
			'{status,provider_account_reference}', '"different"') where id = 'new'`)
		rejectStatement(ctx, "23514", "", insertBillingAccount, "wrong-name", "label-derived",
			billingAccountData("wrong-name", nil))
		rejectStatement(ctx, "23502", "", `update billing_accounts set labels = null where id = 'new'`)
	})

	It("rejects a second account for the same provider_key and provider_account_reference", func(ctx context.Context) {
		Expect(tool.Migrate(ctx, 152)).To(Succeed())
		_, err := conn.Exec(ctx, insertBillingAccount, "ba-1", billingAccountName("ba-1"), billingAccountData("ba-1", nil))
		Expect(err).ToNot(HaveOccurred())
		rejectStatement(ctx, "23505", "billing_accounts_unique_provider_account", insertBillingAccount,
			"ba-2", billingAccountName("ba-2"), billingAccountData("ba-1", nil))
	})

	It("allows the same provider_account_reference under a different provider_key", func(ctx context.Context) {
		Expect(tool.Migrate(ctx, 152)).To(Succeed())
		_, err := conn.Exec(ctx, insertBillingAccount, "ba-1", billingAccountName("ba-1"), billingAccountData("ba-1", nil))
		Expect(err).ToNot(HaveOccurred())
		_, err = conn.Exec(ctx, insertBillingAccount, "ba-2", billingAccountName("ba-2"),
			`{"spec":{},"status":{"provider_key":"fake","provider_account_reference":"native-ba-1",
			"state":"BILLING_ACCOUNT_STATE_ACTIVE","state_observation_timestamp":"2026-03-01T01:02:03.000004Z"}}`)
		Expect(err).ToNot(HaveOccurred())
	})

	It("rejects a duplicate attribution id and permits several absent ones", func(ctx context.Context) {
		Expect(tool.Migrate(ctx, 152)).To(Succeed())
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
			Expect(err).ToNot(HaveOccurred(), "multiple SQL NULL attribution ids are allowed")
		}
	})

	It("rejects an empty provider_key or provider_account_reference", func(ctx context.Context) {
		Expect(tool.Migrate(ctx, 152)).To(Succeed())
		for _, field := range []string{"provider_key", "provider_account_reference"} {
			By("rejecting an empty " + field)
			status := map[string]any{
				"provider_key": "m360", "provider_account_reference": "native-ba-1",
				"state_observation_timestamp": "2026-03-01T01:02:03Z",
			}
			status[field] = ""
			payload, err := json.Marshal(map[string]any{"spec": map[string]any{}, "status": status})
			Expect(err).ToNot(HaveOccurred())
			rejectStatement(ctx, "23514", "", insertBillingAccount,
				"ba-1", billingAccountName("ba-1"), string(payload))
		}
	})

	It("validates observation timestamps without rewriting their JSON spelling", func(ctx context.Context) {
		Expect(tool.Migrate(ctx, 152)).To(Succeed())
		const insert = `insert into billing_accounts (id, name, tenant, data)
			values ($1, $2, 'system', jsonb_build_object('status', jsonb_build_object(
				'provider_key', 'm360', 'provider_account_reference', $1::text,
				'state_observation_timestamp', $3::text)))`
		_, err := conn.Exec(ctx, insert, "observation", billingAccountName("observation"), "0001-01-01T00:00:00Z")
		Expect(err).ToNot(HaveOccurred())
		for _, stamp := range invalidBillingTimestamps {
			By("rejecting observation " + stamp)
			rejectStatement(ctx, "23514", "", insert, "invalid", billingAccountName("invalid"), stamp)
		}
		// Two observations 50 ns apart keep distinct JSON values and the same microsecond mirror.
		for _, stamp := range []string{"9999-12-31T23:59:59.999999949Z", "9999-12-31T23:59:59.999999999Z"} {
			_, err = conn.Exec(ctx, `update billing_accounts set data = jsonb_set(data,
				'{status,state_observation_timestamp}', to_jsonb($1::text)) where id = 'observation'`, stamp)
			Expect(err).ToNot(HaveOccurred())
			var stored string
			var matches bool
			Expect(conn.QueryRow(ctx, `select data#>>'{status,state_observation_timestamp}',
				status_observed_at = '9999-12-31T23:59:59.999999Z'::timestamptz
				from billing_accounts where id = 'observation'`).Scan(&stored, &matches)).To(Succeed())
			Expect(stored).To(Equal(stamp))
			Expect(matches).To(BeTrue())
		}
	})

	It("keys the active companion on deletion, not on observation", func(ctx context.Context) {
		Expect(tool.Migrate(ctx, 152)).To(Succeed())
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

	It("keeps billing writes unpublished", func(ctx context.Context) {
		Expect(tool.Migrate(ctx, 152)).To(Succeed())
		var before, after int
		Expect(conn.QueryRow(ctx, `select count(*) from changes`).Scan(&before)).To(Succeed())
		_, err := conn.Exec(ctx, insertBillingAccount, "unpublished", billingAccountName("unpublished"),
			billingAccountData("unpublished", nil))
		Expect(err).ToNot(HaveOccurred())
		_, err = conn.Exec(ctx, `update billing_accounts set data = jsonb_set(data, '{status,display_name}', '"Update"')
			where id = 'unpublished'`)
		Expect(err).ToNot(HaveOccurred())
		Expect(conn.QueryRow(ctx, `select count(*) from changes`).Scan(&after)).To(Succeed())
		Expect(after).To(Equal(before))
		var triggers int
		Expect(conn.QueryRow(ctx, `select count(*) from pg_trigger t join pg_proc p on p.oid = t.tgfoid
			where t.tgrelid = 'billing_accounts'::regclass and p.proname = 'enqueue_change'
			and not t.tgisinternal`).Scan(&triggers)).To(Succeed())
		Expect(triggers).To(BeZero())
	})

	It("rejects a non-object payload and non-string field types", func(ctx context.Context) {
		Expect(tool.Migrate(ctx, 152)).To(Succeed())
		for _, payload := range []string{`[]`, `{"spec":{"extra":true},"status":{}}`, `{"status":{"extra":"x"}}`} {
			rejectStatement(ctx, "23514", "", insertBillingAccount, "bad", billingAccountName("bad"), payload)
		}
		for _, value := range []string{`1`, `"ACTIVE"`, `null`} {
			rejectStatement(ctx, "23514", "", `insert into billing_accounts (id, name, tenant, data)
				values ('typed', $1, 'system', jsonb_set(jsonb_build_object('status', jsonb_build_object(
					'provider_key', 'm360', 'provider_account_reference', 'native-typed',
					'state_observation_timestamp', '2026-03-01T01:02:03Z')),
					'{status,state}', $2::jsonb))`, billingAccountName("typed"), value)
		}
	})
})

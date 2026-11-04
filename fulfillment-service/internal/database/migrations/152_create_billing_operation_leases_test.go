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
	"os"
	"strings"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = DescribeMigration("Create billing operation leases", func() {
	It("creates guarded digest keys and can remove the table with the down migration", func(ctx context.Context) {
		Expect(tool.Migrate(ctx, 152)).To(Succeed())
		key := strings.Repeat("a", 64)
		_, err := conn.Exec(ctx, `insert into billing_operation_leases values ($1, 'owner', now())`, key)
		Expect(err).ToNot(HaveOccurred())
		_, err = conn.Exec(ctx, `insert into billing_operation_leases values ($1, 'second-owner', now())`, key)
		Expect(err).To(HaveOccurred(), "operation key must be unique")
		_, err = conn.Exec(ctx, `insert into billing_operation_leases values ('raw-correlation', 'owner', now())`)
		Expect(err).To(HaveOccurred(), "raw keys must not be persisted")
		down, err := os.ReadFile("rollback/152_create_billing_operation_leases.down.sql")
		Expect(err).ToNot(HaveOccurred())
		_, err = conn.Exec(ctx, string(down))
		Expect(err).ToNot(HaveOccurred())
		var missing bool
		Expect(conn.QueryRow(ctx, `select to_regclass('billing_operation_leases') is null`).Scan(&missing)).To(Succeed())
		Expect(missing).To(BeTrue())
	})
})

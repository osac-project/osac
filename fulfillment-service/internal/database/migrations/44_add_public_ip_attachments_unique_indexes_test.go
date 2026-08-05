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

	. "github.com/onsi/ginkgo/v2/dsl/core"
	. "github.com/onsi/gomega"
)

var _ = DescribeMigration("Add unique indexes for public IP attachments", func() {
	insert := func(ctx context.Context, id, publicIP, computeInstance string) error {
		_, err := conn.Exec(
			ctx,
			`insert into public_ip_attachments (id, data) values ($1, $2)`,
			id,
			`{"spec": {"public_ip": "`+publicIP+`", "compute_instance": "`+computeInstance+`"}}`,
		)
		return err
	}

	softDelete := func(ctx context.Context, id string) {
		_, err := conn.Exec(
			ctx,
			`update public_ip_attachments set deletion_timestamp = now() where id = $1`,
			id,
		)
		Expect(err).ToNot(HaveOccurred())
	}

	It("Rejects duplicate active public IP", func(ctx context.Context) {
		err := insert(ctx, "a1", "pip-1", "ci-1")
		Expect(err).ToNot(HaveOccurred())

		err = tool.Migrate(ctx, 44)
		Expect(err).ToNot(HaveOccurred())

		err = insert(ctx, "a2", "pip-1", "ci-2")
		Expect(err).To(HaveOccurred())
	})

	It("Rejects duplicate active compute instance", func(ctx context.Context) {
		err := insert(ctx, "a1", "pip-1", "ci-1")
		Expect(err).ToNot(HaveOccurred())

		err = tool.Migrate(ctx, 44)
		Expect(err).ToNot(HaveOccurred())

		err = insert(ctx, "a2", "pip-2", "ci-1")
		Expect(err).To(HaveOccurred())
	})

	It("Allows same public IP after soft delete", func(ctx context.Context) {
		err := insert(ctx, "a1", "pip-1", "ci-1")
		Expect(err).ToNot(HaveOccurred())

		err = tool.Migrate(ctx, 44)
		Expect(err).ToNot(HaveOccurred())

		softDelete(ctx, "a1")

		err = insert(ctx, "a2", "pip-1", "ci-2")
		Expect(err).ToNot(HaveOccurred())
	})

	It("Allows same compute instance after soft delete", func(ctx context.Context) {
		err := insert(ctx, "a1", "pip-1", "ci-1")
		Expect(err).ToNot(HaveOccurred())

		err = tool.Migrate(ctx, 44)
		Expect(err).ToNot(HaveOccurred())

		softDelete(ctx, "a1")

		err = insert(ctx, "a2", "pip-2", "ci-1")
		Expect(err).ToNot(HaveOccurred())
	})

	It("Allows different public IPs and compute instances", func(ctx context.Context) {
		err := insert(ctx, "a1", "pip-1", "ci-1")
		Expect(err).ToNot(HaveOccurred())

		err = tool.Migrate(ctx, 44)
		Expect(err).ToNot(HaveOccurred())

		err = insert(ctx, "a2", "pip-2", "ci-2")
		Expect(err).ToNot(HaveOccurred())
	})
})

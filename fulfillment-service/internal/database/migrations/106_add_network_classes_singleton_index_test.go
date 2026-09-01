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

var _ = DescribeMigration("Add singleton index for network classes", func() {
	insert := func(ctx context.Context, id string) error {
		_, err := conn.Exec(
			ctx,
			`insert into network_classes (id, tenant, name, data) values ($1, 'shared', $1, '{}')`,
			id,
		)
		return err
	}

	softDelete := func(ctx context.Context, id string) {
		_, err := conn.Exec(
			ctx,
			`update network_classes set deletion_timestamp = now() where id = $1`,
			id,
		)
		Expect(err).ToNot(HaveOccurred())
	}

	It("Rejects a second active NetworkClass", func(ctx context.Context) {
		err := insert(ctx, "nc-1")
		Expect(err).ToNot(HaveOccurred())

		err = tool.Migrate(ctx, 106)
		Expect(err).ToNot(HaveOccurred())

		err = insert(ctx, "nc-2")
		Expect(err).To(HaveOccurred())
	})

	It("Allows a second NetworkClass after the first is soft-deleted", func(ctx context.Context) {
		err := insert(ctx, "nc-1")
		Expect(err).ToNot(HaveOccurred())

		err = tool.Migrate(ctx, 106)
		Expect(err).ToNot(HaveOccurred())

		softDelete(ctx, "nc-1")

		err = insert(ctx, "nc-2")
		Expect(err).ToNot(HaveOccurred())
	})

	It("Allows the first NetworkClass when none exists yet", func(ctx context.Context) {
		err := tool.Migrate(ctx, 106)
		Expect(err).ToNot(HaveOccurred())

		err = insert(ctx, "nc-1")
		Expect(err).ToNot(HaveOccurred())
	})
})

/*
Copyright (c) 2026 Red Hat Inc.

Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except in compliance with the
License. You may obtain a copy of the License at

  http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on an
"AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
specific language governing permissions and limitations under the License.
*/

package migrations

import (
	"context"

	. "github.com/onsi/ginkgo/v2/dsl/core"
	. "github.com/onsi/gomega"
)

var _ = DescribeMigration("NetworkClass singleton on all rows", func() {
	BeforeEach(func(ctx context.Context) {
		Expect(tool.Migrate(ctx, 143)).To(Succeed())
	})

	insert := func(ctx context.Context, id string) error {
		_, err := conn.Exec(ctx,
			`insert into network_classes (id, tenant, name, data) values ($1, 'shared', $1, '{}')`, id)
		return err
	}

	It("rejects a second NetworkClass while the first one is being deleted", func(ctx context.Context) {
		Expect(insert(ctx, "nc-1")).To(Succeed())
		_, err := conn.Exec(ctx, `update network_classes set deletion_timestamp = now() where id = 'nc-1'`)
		Expect(err).ToNot(HaveOccurred())
		Expect(insert(ctx, "nc-2")).ToNot(Succeed())
	})

	It("accepts a new NetworkClass once the old one is archived", func(ctx context.Context) {
		Expect(insert(ctx, "nc-1")).To(Succeed())
		_, err := conn.Exec(ctx, `delete from network_classes where id = 'nc-1'`)
		Expect(err).ToNot(HaveOccurred())
		Expect(insert(ctx, "nc-2")).To(Succeed())
	})
})

var _ = DescribeMigration("NetworkClass singleton on a database with an old NetworkClass being deleted", func() {
	BeforeEach(func(ctx context.Context) {
		Expect(tool.Migrate(ctx, 142)).To(Succeed())
	})

	It("fails with an error that tells the operator what to do", func(ctx context.Context) {
		_, err := conn.Exec(ctx, `
			insert into network_classes (id, tenant, name, data, deletion_timestamp)
			values ('nc-old', 'shared', 'nc-old', '{}', now())
		`)
		Expect(err).ToNot(HaveOccurred())
		_, err = conn.Exec(ctx, `
			insert into network_classes (id, tenant, name, data)
			values ('nc-new', 'shared', 'nc-new', '{}')
		`)
		Expect(err).ToNot(HaveOccurred())

		err = tool.Migrate(ctx, 143)
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring("2 NetworkClass records exist"))
		Expect(err.Error()).To(ContainSubstring("finish deleting the old NetworkClass"))
		Expect(err.Error()).To(ContainSubstring("or reinstall"))
	})
})

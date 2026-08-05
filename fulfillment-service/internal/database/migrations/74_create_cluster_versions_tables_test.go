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
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5/pgconn"
	. "github.com/onsi/ginkgo/v2/dsl/core"
	. "github.com/onsi/ginkgo/v2/dsl/table"
	. "github.com/onsi/gomega"
)

var _ = DescribeMigration("Create cluster versions tables", func() {
	BeforeEach(func(ctx context.Context) {
		err := tool.Migrate(ctx, 74)
		Expect(err).ToNot(HaveOccurred())
	})

	It("Creates the cluster_versions table", func(ctx context.Context) {
		_, err := conn.Exec(ctx, `
			insert into cluster_versions (id, tenant, data)
			values ($1, $2, $3)`,
			"test-id", "system", `{
				"spec": {
					"version": "4.17.0",
					"image": "quay.io/test:4.17.0"
				}
			}`,
		)
		Expect(err).ToNot(HaveOccurred())

		var count int
		err = conn.QueryRow(ctx, `
			select count(*)
			from cluster_versions
			where id = $1`,
			"test-id",
		).Scan(&count)
		Expect(err).ToNot(HaveOccurred())
		Expect(count).To(Equal(1))

		_, err = conn.Exec(ctx, `
			insert into cluster_versions (id, tenant, data)
			values ($1, $2, $3)`,
			"bad-tenant-id", "no-such-tenant", `{
				"spec": {
					"version": "4.18.0",
					"image": "quay.io/test:4.18.0"
				}
			}`,
		)
		Expect(err).To(HaveOccurred())
	})

	It("Creates the archived_cluster_versions table", func(ctx context.Context) {
		_, err := conn.Exec(ctx, `
			insert into archived_cluster_versions
				(id, tenant, creation_timestamp, deletion_timestamp, data)
			values ($1, $2, now(), now(), $3)`,
			"test-id", "system", `{
				"spec": {
					"version": "4.17.0",
					"image": "quay.io/test:4.17.0"
				}
			}`,
		)
		Expect(err).ToNot(HaveOccurred())

		var count int
		err = conn.QueryRow(ctx, `
			select count(*)
			from archived_cluster_versions
			where id = $1`,
			"test-id",
		).Scan(&count)
		Expect(err).ToNot(HaveOccurred())
		Expect(count).To(Equal(1))
	})

	insertCV := func(ctx context.Context, id, name, version, image string, isDefault bool) error {
		data := fmt.Sprintf(`{
			"spec": {
				"version": "%s",
				"image": "%s",
				"is_default": %t
			}
		}`, version, image, isDefault)
		_, err := conn.Exec(ctx, `
			insert into cluster_versions (id, name, tenant, data)
			values ($1, $2, $3, $4)`,
			id, name, "system", data,
		)
		return err
	}

	insertRawCV := func(ctx context.Context, id, name, data string) error {
		_, err := conn.Exec(ctx, `
			insert into cluster_versions (id, name, tenant, data)
			values ($1, $2, $3, $4)`,
			id, name, "system", data,
		)
		return err
	}

	softDeleteCV := func(ctx context.Context, id string) {
		_, err := conn.Exec(ctx, `
			update cluster_versions
			set deletion_timestamp = now()
			where id = $1`,
			id,
		)
		Expect(err).ToNot(HaveOccurred())
	}

	// CHECK constraints: field presence and type validation.

	DescribeTable(
		"Rejects insert with invalid data",
		func(ctx context.Context, data, constraint string) {
			err := insertRawCV(ctx, "cv-chk", "chk", data)
			Expect(err).To(HaveOccurred())
			var pgErr *pgconn.PgError
			Expect(errors.As(err, &pgErr)).To(BeTrue())
			Expect(pgErr.Code).To(Equal("23514"))
			Expect(pgErr.ConstraintName).To(Equal(constraint))
		},
		Entry("missing spec.version",
			`{
				"spec": {
					"image": "quay.io/test:x"
				}
			}`,
			"cluster_versions_spec_version_chk",
		),
		Entry("empty spec.version",
			`{
				"spec": {
					"version": "",
					"image": "quay.io/test:x"
				}
			}`,
			"cluster_versions_spec_version_chk",
		),
		Entry("non-string spec.version",
			`{
				"spec": {
					"version": 417,
					"image": "quay.io/test:x"
				}
			}`,
			"cluster_versions_spec_version_chk",
		),
		Entry("whitespace-only spec.version",
			`{
				"spec": {
					"version": "   ",
					"image": "quay.io/test:x"
				}
			}`,
			"cluster_versions_spec_version_chk",
		),
		Entry("missing spec.image",
			`{
				"spec": {
					"version": "4.17.0"
				}
			}`,
			"cluster_versions_spec_image_chk",
		),
		Entry("empty spec.image",
			`{
				"spec": {
					"version": "4.17.0",
					"image": ""
				}
			}`,
			"cluster_versions_spec_image_chk",
		),
		Entry("non-string spec.image",
			`{
				"spec": {
					"version": "4.17.0",
					"image": true
				}
			}`,
			"cluster_versions_spec_image_chk",
		),
		Entry("whitespace-only spec.image",
			`{
				"spec": {
					"version": "4.17.0",
					"image": "   "
				}
			}`,
			"cluster_versions_spec_image_chk",
		),
		Entry("non-boolean spec.is_default",
			`{
				"spec": {
					"version": "4.17.0",
					"image": "quay.io/test:x",
					"is_default": "yes"
				}
			}`,
			"cluster_versions_spec_is_default_chk",
		),
	)

	// Unique indexes: name, spec.version, single default.

	It("Rejects duplicate active name for same tenant and project", func(ctx context.Context) {
		err := insertCV(ctx, "cv-1", "4-17-0", "4.17.0", "quay.io/test:4.17.0", false)
		Expect(err).ToNot(HaveOccurred())

		err = insertCV(ctx, "cv-2", "4-17-0", "4.17.1", "quay.io/test:4.17.1", false)
		Expect(err).To(HaveOccurred())
		var pgErr *pgconn.PgError
		Expect(errors.As(err, &pgErr)).To(BeTrue())
		Expect(pgErr.Code).To(Equal("23505"))
	})

	It("Allows same name after soft delete", func(ctx context.Context) {
		err := insertCV(ctx, "cv-3", "4-17-0", "4.17.0", "quay.io/test:4.17.0", false)
		Expect(err).ToNot(HaveOccurred())

		softDeleteCV(ctx, "cv-3")

		err = insertCV(ctx, "cv-4", "4-17-0", "4.17.0-reuse", "quay.io/test:4.17.0-reuse", false)
		Expect(err).ToNot(HaveOccurred())
	})

	It("Rejects duplicate active spec.version for same tenant and project", func(ctx context.Context) {
		err := insertCV(ctx, "cv-5", "name-a", "4.17.0", "quay.io/test:a", false)
		Expect(err).ToNot(HaveOccurred())

		err = insertCV(ctx, "cv-6", "name-b", "4.17.0", "quay.io/test:b", false)
		Expect(err).To(HaveOccurred())
		var pgErr *pgconn.PgError
		Expect(errors.As(err, &pgErr)).To(BeTrue())
		Expect(pgErr.Code).To(Equal("23505"))
	})

	It("Allows same spec.version after soft delete", func(ctx context.Context) {
		err := insertCV(ctx, "cv-7", "name-c", "4.18.0", "quay.io/test:c", false)
		Expect(err).ToNot(HaveOccurred())

		softDeleteCV(ctx, "cv-7")

		err = insertCV(ctx, "cv-8", "name-d", "4.18.0", "quay.io/test:d", false)
		Expect(err).ToNot(HaveOccurred())
	})

	It("Rejects second active default for same tenant and project", func(ctx context.Context) {
		err := insertCV(ctx, "cv-9", "name-e", "4.17.0", "quay.io/test:e", true)
		Expect(err).ToNot(HaveOccurred())

		err = insertCV(ctx, "cv-10", "name-f", "4.18.0", "quay.io/test:f", true)
		Expect(err).To(HaveOccurred())
		var pgErr *pgconn.PgError
		Expect(errors.As(err, &pgErr)).To(BeTrue())
		Expect(pgErr.Code).To(Equal("23505"))
	})

	It("Allows second default after first is soft-deleted", func(ctx context.Context) {
		err := insertCV(ctx, "cv-11", "name-g", "4.17.0", "quay.io/test:g", true)
		Expect(err).ToNot(HaveOccurred())

		softDeleteCV(ctx, "cv-11")

		err = insertCV(ctx, "cv-12", "name-h", "4.18.0", "quay.io/test:h", true)
		Expect(err).ToNot(HaveOccurred())
	})

	It("Allows multiple non-default versions", func(ctx context.Context) {
		err := insertCV(ctx, "cv-13", "name-i", "4.17.0", "quay.io/test:i", false)
		Expect(err).ToNot(HaveOccurred())

		err = insertCV(ctx, "cv-14", "name-j", "4.18.0", "quay.io/test:j", false)
		Expect(err).ToNot(HaveOccurred())
	})

	It("Allows multiple versions when is_default is absent from data", func(ctx context.Context) {
		_, err := conn.Exec(ctx, `
			insert into cluster_versions (id, name, tenant, data)
			values ($1, $2, $3, $4)`,
			"cv-19", "name-o", "system", `{
				"spec": {
					"version": "4.17.0",
					"image": "quay.io/test:o"
				}
			}`,
		)
		Expect(err).ToNot(HaveOccurred())

		_, err = conn.Exec(ctx, `
			insert into cluster_versions (id, name, tenant, data)
			values ($1, $2, $3, $4)`,
			"cv-20", "name-p", "system", `{
				"spec": {
					"version": "4.18.0",
					"image": "quay.io/test:p"
				}
			}`,
		)
		Expect(err).ToNot(HaveOccurred())
	})

	// Immutability: spec.version and spec.image cannot be changed after insert.

	DescribeTable(
		"Rejects update that changes an immutable field",
		func(ctx context.Context, updatedData, message string) {
			err := insertCV(ctx, "cv-imm", "name-imm", "4.17.0", "quay.io/test:imm", false)
			Expect(err).ToNot(HaveOccurred())

			_, err = conn.Exec(ctx, `
				update cluster_versions
				set data = $1
				where id = $2`,
				updatedData, "cv-imm",
			)
			Expect(err).To(HaveOccurred())
			var pgErr *pgconn.PgError
			Expect(errors.As(err, &pgErr)).To(BeTrue())
			Expect(pgErr.Code).To(Equal("Z0001"))
			Expect(pgErr.Message).To(Equal(message))
		},
		Entry("spec.version",
			`{
				"spec": {
					"version": "4.18.0",
					"image": "quay.io/test:imm"
				}
			}`,
			"the spec.version field of table 'cluster_versions' is immutable",
		),
		Entry("spec.image",
			`{
				"spec": {
					"version": "4.17.0",
					"image": "quay.io/test:imm-changed"
				}
			}`,
			"the spec.image field of table 'cluster_versions' is immutable",
		),
	)

	It("Reports both fields when spec.version and spec.image change", func(ctx context.Context) {
		err := insertCV(ctx, "cv-17", "name-m", "4.17.0", "quay.io/test:m", false)
		Expect(err).ToNot(HaveOccurred())

		_, err = conn.Exec(ctx, `
			update cluster_versions
			set data = $1
			where id = $2`,
			`{
				"spec": {
					"version": "4.18.0",
					"image": "quay.io/test:m-changed"
				}
			}`,
			"cv-17",
		)
		Expect(err).To(HaveOccurred())
		var pgErr *pgconn.PgError
		Expect(errors.As(err, &pgErr)).To(BeTrue())
		Expect(pgErr.Code).To(Equal("Z0001"))
		Expect(pgErr.Message).To(Equal(
			"the spec.version, spec.image fields of table 'cluster_versions' are immutable",
		))
	})

	It("Allows update of mutable fields", func(ctx context.Context) {
		err := insertCV(ctx, "cv-18", "name-n", "4.17.0", "quay.io/test:n", false)
		Expect(err).ToNot(HaveOccurred())

		_, err = conn.Exec(ctx, `
			update cluster_versions
			set data = $1
			where id = $2`,
			`{
				"spec": {
					"version": "4.17.0",
					"image": "quay.io/test:n",
					"state": "CLUSTER_VERSION_STATE_DEPRECATED",
					"is_default": true
				}
			}`,
			"cv-18",
		)
		Expect(err).ToNot(HaveOccurred())
	})
})

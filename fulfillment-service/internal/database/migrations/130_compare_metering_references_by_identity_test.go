/*
Copyright (c) 2026 Red Hat Inc.

Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except in compliance with
the License. You may obtain a copy of the License at

   http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
specific language governing permissions and limitations under the License.
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

var _ = DescribeMigration("Compare metering references by identity", func() {
	BeforeEach(func(ctx context.Context) {
		Expect(tool.Migrate(ctx, 130)).To(Succeed())
		_, err := conn.Exec(ctx, `
			insert into tenants (id, name, tenant, creator, data)
			values ('tenant-a', 'tenant-a', 'tenant-a', 'system', '{}');
			insert into projects (id, name, tenant, project, creator, data)
			values ('project-a', 'team-a', 'tenant-a', 'team-a', 'system', '{}')`)
		Expect(err).ToNot(HaveOccurred())
	})

	insert := func(ctx context.Context, table, id, data string) {
		_, err := conn.Exec(ctx, fmt.Sprintf(`
			insert into %s (id, name, tenant, project, data)
			values ($1, $1, 'tenant-a', 'team-a', $2::jsonb)`, table), id, data)
		Expect(err).ToNot(HaveOccurred())
	}

	// update replaces the whole data document, the way a writer that read the row, changed one
	// field and wrote it back would.
	update := func(ctx context.Context, table, id, data string) error {
		tag, err := conn.Exec(ctx, fmt.Sprintf(`
			update %s set data = $2::jsonb where id = $1`, table), id, data)
		if err == nil {
			Expect(tag.RowsAffected()).To(Equal(int64(1)))
		}
		return err
	}

	storedPath := func(ctx context.Context, table, id, path string) *string {
		var value *string
		err := conn.QueryRow(ctx, fmt.Sprintf(`
			select data #>> string_to_array($2, '.') from %s where id = $1`, table), id, path).Scan(&value)
		Expect(err).ToNot(HaveOccurred())
		return value
	}

	expectContractViolation := func(err error) {
		var pgErr *pgconn.PgError
		Expect(errors.As(err, &pgErr)).To(BeTrue())
		Expect(pgErr.Code).To(Equal("Z0006"))
	}

	DescribeTable("accepts a rewrite of the same reference",
		func(ctx context.Context, table, id, oldData, newData string) {
			insert(ctx, table, id, oldData)
			Expect(update(ctx, table, id, newData)).To(Succeed())
		},
		Entry("fixed field with different resolution details and key order",
			"compute_instances", "vm-1",
			`{
				"spec": {"disk_image": {"id": "img-1", "name": "img-1"}},
				"status": {"state": "COMPUTE_INSTANCE_STATE_RUNNING", "state_transition_time": "2026-01-01T10:00:00Z"}
			}`,
			`{
				"spec": {"disk_image": {"name": "img-1", "project": {"id": "project-a"}, "hints": {"vendor": "qcow2"}, "id": "img-1"}},
				"status": {"state": "COMPUTE_INSTANCE_STATE_RUNNING", "state_transition_time": "2026-01-01T10:00:00Z"}
			}`),
		Entry("fixed field with an added resolution detail",
			"clusters", "cluster-1",
			`{
				"spec": {"version": {"id": "v1"}, "template": {"id": "tpl-1", "name": "tpl-1"}},
				"status": {"state": "CLUSTER_STATE_READY", "state_transition_time": "2026-01-01T10:00:00Z"}
			}`,
			`{
				"spec": {"version": {"id": "v1"}, "template": {"id": "tpl-1", "name": "tpl-1", "project": {"id": "project-a"}}},
				"status": {"state": "CLUSTER_STATE_READY", "state_transition_time": "2026-01-01T10:00:00Z"}
			}`),
	)

	DescribeTable("rejects a different reference",
		func(ctx context.Context, table, id, oldData, newData string) {
			insert(ctx, table, id, oldData)
			expectContractViolation(update(ctx, table, id, newData))
		},
		Entry("different id",
			"compute_instances", "vm-1",
			`{
				"spec": {"disk_image": {"id": "img-1", "name": "img-1"}},
				"status": {"state": "COMPUTE_INSTANCE_STATE_RUNNING", "state_transition_time": "2026-01-01T10:00:00Z"}
			}`,
			`{
				"spec": {"disk_image": {"id": "img-2", "name": "img-1"}},
				"status": {"state": "COMPUTE_INSTANCE_STATE_RUNNING", "state_transition_time": "2026-01-01T10:00:00Z"}
			}`),
		Entry("different name",
			"compute_instances", "vm-1",
			`{
				"spec": {"disk_image": {"id": "img-1", "name": "img-1"}},
				"status": {"state": "COMPUTE_INSTANCE_STATE_RUNNING", "state_transition_time": "2026-01-01T10:00:00Z"}
			}`,
			`{
				"spec": {"disk_image": {"id": "img-1", "name": "img-renamed"}},
				"status": {"state": "COMPUTE_INSTANCE_STATE_RUNNING", "state_transition_time": "2026-01-01T10:00:00Z"}
			}`),
		Entry("id dropped",
			"compute_instances", "vm-1",
			`{
				"spec": {"disk_image": {"id": "img-1", "name": "img-1"}},
				"status": {"state": "COMPUTE_INSTANCE_STATE_RUNNING", "state_transition_time": "2026-01-01T10:00:00Z"}
			}`,
			`{
				"spec": {"disk_image": {"name": "img-1"}},
				"status": {"state": "COMPUTE_INSTANCE_STATE_RUNNING", "state_transition_time": "2026-01-01T10:00:00Z"}
			}`),
		Entry("non-reference field",
			"volumes", "volume-1",
			`{"spec": {"storage_tier": "tier-1"}, "status": {"state": "VOLUME_STATE_AVAILABLE", "state_transition_time": "2026-01-01T10:00:00Z"}}`,
			`{"spec": {"storage_tier": "tier-2"}, "status": {"state": "VOLUME_STATE_AVAILABLE", "state_transition_time": "2026-01-01T10:00:00Z"}}`),
	)

	It("treats the same changing reference as an unchanged value", func(ctx context.Context) {
		// The running instance type is a reference. Rewriting it with the same id and name but
		// different resolution details is not a change, so its transition time stays.
		insert(ctx, "compute_instances", "vm-1", `{
			"status": {
				"state": "COMPUTE_INSTANCE_STATE_RUNNING",
				"state_transition_time": "2026-01-01T10:00:00Z",
				"instance_type": {"id": "type-small", "name": "small"},
				"instance_type_transition_time": "2026-01-01T10:00:00Z"
			}
		}`)
		Expect(update(ctx, "compute_instances", "vm-1", `{
			"status": {
				"state": "COMPUTE_INSTANCE_STATE_RUNNING",
				"state_transition_time": "2026-01-01T10:00:00Z",
				"instance_type": {"name": "small", "project": {"id": "project-a"}, "id": "type-small"},
				"instance_type_transition_time": "2026-01-02T10:00:00Z"
			}
		}`)).To(Succeed())
		Expect(storedPath(ctx, "compute_instances", "vm-1", "status.instance_type_transition_time")).To(
			HaveValue(Equal("2026-01-01T10:00:00Z")),
		)
	})

	It("still treats a different changing reference as a change", func(ctx context.Context) {
		insert(ctx, "compute_instances", "vm-1", `{
			"status": {
				"state": "COMPUTE_INSTANCE_STATE_RUNNING",
				"state_transition_time": "2026-01-01T10:00:00Z",
				"instance_type": {"id": "type-small", "name": "small"},
				"instance_type_transition_time": "2026-01-01T10:00:00Z"
			}
		}`)
		Expect(update(ctx, "compute_instances", "vm-1", `{
			"status": {
				"state": "COMPUTE_INSTANCE_STATE_RUNNING",
				"state_transition_time": "2026-01-01T10:00:00Z",
				"instance_type": {"id": "type-small", "name": "smaller"},
				"instance_type_transition_time": "2026-01-02T10:00:00Z"
			}
		}`)).To(Succeed())
		Expect(storedPath(ctx, "compute_instances", "vm-1", "status.instance_type_transition_time")).To(
			HaveValue(Equal("2026-01-02T10:00:00Z")),
		)
	})
})

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

	"github.com/jackc/pgx/v5/pgconn"
	. "github.com/onsi/ginkgo/v2/dsl/core"
	. "github.com/onsi/gomega"
	"google.golang.org/protobuf/proto"
)

var _ = DescribeMigration("Round metering change times to seconds", func() {
	BeforeEach(func(ctx context.Context) {
		_, err := conn.Exec(ctx, `
			insert into tenants (id, name, tenant, creator, data)
			values ('tenant-a', 'tenant-a', 'tenant-a', 'system', '{}');
			insert into projects (id, name, tenant, project, creator, data)
			values ('project-a', 'team-a', 'tenant-a', 'team-a', 'system', '{}')`)
		Expect(err).ToNot(HaveOccurred())
	})

	insert := func(ctx context.Context, id, data string) {
		_, err := conn.Exec(ctx, `
			insert into compute_instances (id, name, tenant, project, data)
			values ($1, $1, 'tenant-a', 'team-a', $2::jsonb)`, id, data)
		Expect(err).ToNot(HaveOccurred())
	}

	// insertCreated inserts a resource created at a controlled creation timestamp: the state
	// the fold keys at the creation time.
	insertCreated := func(ctx context.Context, id, data string) {
		_, err := conn.Exec(ctx, `
			insert into compute_instances (id, name, tenant, project, creation_timestamp, data)
			values ($1, $1, 'tenant-a', 'team-a', '2026-01-01T10:00:00Z', $2::jsonb)`, id, data)
		Expect(err).ToNot(HaveOccurred())
	}

	// update replaces the whole data document, the way a writer that read the row, changed one
	// field and wrote it back would.
	update := func(ctx context.Context, id, data string) error {
		tag, err := conn.Exec(ctx, `
			update compute_instances set data = $2::jsonb where id = $1`, id, data)
		if err == nil {
			Expect(tag.RowsAffected()).To(Equal(int64(1)))
		}
		return err
	}

	expectRejection := func(ctx context.Context, id, timePath, newData string, wantStored *string) {
		err := update(ctx, id, newData)
		var pgErr *pgconn.PgError
		Expect(errors.As(err, &pgErr)).To(BeTrue())
		Expect(pgErr.Code).To(Equal("Z0006"))
		Expect(pgErr.Message).To(ContainSubstring(timePath))
		var value *string
		err = conn.QueryRow(ctx, `
			select data #>> string_to_array($2, '.') from compute_instances where id = $1`, id, timePath).Scan(&value)
		Expect(err).ToNot(HaveOccurred())
		Expect(value).To(Equal(wantStored))
	}

	storedPath := func(ctx context.Context, id, path string) *string {
		var value *string
		err := conn.QueryRow(ctx, `
			select data #>> string_to_array($2, '.') from compute_instances where id = $1`, id, path).Scan(&value)
		Expect(err).ToNot(HaveOccurred())
		return value
	}

	// A compute instance that is running, observed with its running instance type.
	runningVM := `{
		"status": {
			"state": "COMPUTE_INSTANCE_STATE_RUNNING",
			"state_transition_time": "2026-01-01T10:00:00Z",
			"instance_type": {"id": "type-small"},
			"instance_type_transition_time": "2026-01-01T10:00:00Z"
		}
	}`

	It("normalizes stored times written before the migration", func(ctx context.Context) {
		insert(ctx, "vm-1", `{
			"status": {
				"state": "COMPUTE_INSTANCE_STATE_RUNNING",
				"state_transition_time": "2026-01-01T10:00:00.500000Z",
				"instance_type": {"id": "type-small"},
				"instance_type_transition_time": "2026-01-01T09:59:59.000001Z"
			}
		}`)
		Expect(tool.Migrate(ctx, 138)).To(Succeed())
		Expect(storedPath(ctx, "vm-1", "status.state_transition_time")).To(
			HaveValue(Equal("2026-01-01T10:00:00Z")),
		)
		Expect(storedPath(ctx, "vm-1", "status.instance_type_transition_time")).To(
			HaveValue(Equal("2026-01-01T09:59:59Z")),
		)
	})

	It("normalizes a microsecond time supplied at insert", func(ctx context.Context) {
		Expect(tool.Migrate(ctx, 138)).To(Succeed())
		insert(ctx, "vm-1", `{
			"status": {
				"state": "COMPUTE_INSTANCE_STATE_RUNNING",
				"state_transition_time": "2026-01-01T10:00:00.959617599Z",
				"instance_type": {"id": "type-small"},
				"instance_type_transition_time": "2026-01-01T10:00:00.500000Z"
			}
		}`)
		Expect(storedPath(ctx, "vm-1", "status.state_transition_time")).To(
			HaveValue(Equal("2026-01-01T10:00:00Z")),
		)
		Expect(storedPath(ctx, "vm-1", "status.instance_type_transition_time")).To(
			HaveValue(Equal("2026-01-01T10:00:00Z")),
		)
	})

	It("normalizes a supplied microsecond time to whole seconds", func(ctx context.Context) {
		Expect(tool.Migrate(ctx, 138)).To(Succeed())
		insert(ctx, "vm-1", runningVM)
		Expect(update(ctx, "vm-1", `{
			"status": {
				"state": "COMPUTE_INSTANCE_STATE_STOPPED",
				"state_transition_time": "2026-01-01T11:00:00.900000Z",
				"instance_type": {"id": "type-small"},
				"instance_type_transition_time": "2026-01-01T10:00:00Z"
			}
		}`)).To(Succeed())
		Expect(storedPath(ctx, "vm-1", "status.state_transition_time")).To(
			HaveValue(Equal("2026-01-01T11:00:00Z")),
		)
	})

	It("accepts a time equal to the stored one without clamping", func(ctx context.Context) {
		Expect(tool.Migrate(ctx, 138)).To(Succeed())
		insert(ctx, "vm-1", runningVM)
		Expect(update(ctx, "vm-1", `{
			"status": {
				"state": "COMPUTE_INSTANCE_STATE_STOPPED",
				"state_transition_time": "2026-01-01T10:00:00Z",
				"instance_type": {"id": "type-small"},
				"instance_type_transition_time": "2026-01-01T10:00:00Z"
			}
		}`)).To(Succeed())
		Expect(storedPath(ctx, "vm-1", "status.state_transition_time")).To(
			HaveValue(Equal("2026-01-01T10:00:00Z")),
		)
	})

	It("accepts a supplied time truncated within the stored second", func(ctx context.Context) {
		Expect(tool.Migrate(ctx, 138)).To(Succeed())
		insert(ctx, "vm-1", `{
			"status": {
				"state": "COMPUTE_INSTANCE_STATE_RUNNING",
				"state_transition_time": "2026-01-01T10:00:05Z",
				"instance_type": {"id": "type-small"},
				"instance_type_transition_time": "2026-01-01T10:00:00Z"
			}
		}`)
		// A CR timestamp truncated down into the stored second: the whole-second
		// normalization makes it equal, and the write is accepted as-is.
		Expect(update(ctx, "vm-1", `{
			"status": {
				"state": "COMPUTE_INSTANCE_STATE_STOPPED",
				"state_transition_time": "2026-01-01T10:00:05.900000Z",
				"instance_type": {"id": "type-small"},
				"instance_type_transition_time": "2026-01-01T10:00:00Z"
			}
		}`)).To(Succeed())
		Expect(storedPath(ctx, "vm-1", "status.state_transition_time")).To(
			HaveValue(Equal("2026-01-01T10:00:05Z")),
		)
	})

	It("still rejects a changed varying field with an earlier time", func(ctx context.Context) {
		Expect(tool.Migrate(ctx, 138)).To(Succeed())
		insert(ctx, "vm-1", runningVM)
		expectRejection(ctx, "vm-1", "status.state_transition_time", `{
			"status": {
				"state": "COMPUTE_INSTANCE_STATE_STOPPED",
				"state_transition_time": "2026-01-01T09:00:00Z",
				"instance_type": {"id": "type-small"},
				"instance_type_transition_time": "2026-01-01T10:00:00Z"
			}
		}`, proto.String("2026-01-01T10:00:00Z"))
	})

	It("still rejects a changed varying field whose time is missing", func(ctx context.Context) {
		Expect(tool.Migrate(ctx, 138)).To(Succeed())
		insert(ctx, "vm-1", runningVM)
		expectRejection(ctx, "vm-1", "status.state_transition_time", `{
			"status": {
				"state": "COMPUTE_INSTANCE_STATE_FAILED",
				"instance_type": {"id": "type-small"},
				"instance_type_transition_time": "2026-01-01T10:00:00Z"
			}
		}`, proto.String("2026-01-01T10:00:00Z"))
	})

	It("stores a first change time as supplied, with no creation clamp", func(ctx context.Context) {
		Expect(tool.Migrate(ctx, 138)).To(Succeed())
		insertCreated(ctx, "vm-1", `{"status": {"state": "COMPUTE_INSTANCE_STATE_STARTING"}}`)
		Expect(update(ctx, "vm-1", `{
			"status": {
				"state": "COMPUTE_INSTANCE_STATE_RUNNING",
				"state_transition_time": "2025-12-31T09:00:00Z"
			}
		}`)).To(Succeed())
		Expect(storedPath(ctx, "vm-1", "status.state_transition_time")).To(
			HaveValue(Equal("2025-12-31T09:00:00Z")),
		)
	})

	It("keeps a supplied time that is later than the stored one", func(ctx context.Context) {
		Expect(tool.Migrate(ctx, 138)).To(Succeed())
		insert(ctx, "vm-1", runningVM)
		Expect(update(ctx, "vm-1", `{
			"status": {
				"state": "COMPUTE_INSTANCE_STATE_STOPPED",
				"state_transition_time": "2026-01-01T11:00:00Z",
				"instance_type": {"id": "type-small"},
				"instance_type_transition_time": "2026-01-01T10:00:00Z"
			}
		}`)).To(Succeed())
		Expect(storedPath(ctx, "vm-1", "status.state_transition_time")).To(
			HaveValue(Equal("2026-01-01T11:00:00Z")),
		)
	})

	It("keeps the old time when a write changes only the time", func(ctx context.Context) {
		Expect(tool.Migrate(ctx, 138)).To(Succeed())
		insert(ctx, "vm-1", runningVM)
		Expect(update(ctx, "vm-1", `{
			"status": {
				"state": "COMPUTE_INSTANCE_STATE_RUNNING",
				"state_transition_time": "2026-01-02T10:00:00Z",
				"instance_type": {"id": "type-small"},
				"instance_type_transition_time": "2026-01-01T10:00:00Z"
			}
		}`)).To(Succeed())
		Expect(storedPath(ctx, "vm-1", "status.state_transition_time")).To(
			HaveValue(Equal("2026-01-01T10:00:00Z")),
		)
	})
})

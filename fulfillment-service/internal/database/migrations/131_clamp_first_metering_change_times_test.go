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
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	. "github.com/onsi/ginkgo/v2/dsl/core"
	. "github.com/onsi/ginkgo/v2/dsl/table"
	. "github.com/onsi/gomega"
)

var _ = DescribeMigration("Clamp first metering change times", func() {
	BeforeEach(func(ctx context.Context) {
		Expect(tool.Migrate(ctx, 131)).To(Succeed())
		_, err := conn.Exec(ctx, `
			insert into tenants (id, name, tenant, creator, data)
			values ('tenant-a', 'tenant-a', 'tenant-a', 'system', '{}');
			insert into projects (id, name, tenant, project, creator, data)
			values ('project-a', 'team-a', 'tenant-a', 'team-a', 'system', '{}')`)
		Expect(err).ToNot(HaveOccurred())
	})

	// insertCreated inserts a resource created with a tracked value and no time, at a controlled
	// creation timestamp: the state the fold keys at the creation fallback.
	insertCreated := func(ctx context.Context, table, id, data string) {
		_, err := conn.Exec(ctx, fmt.Sprintf(`
			insert into %s (id, name, tenant, project, creation_timestamp, data)
			values ($1, $1, 'tenant-a', 'team-a', '2026-01-01T10:00:00Z', $2::jsonb)`, table), id, data)
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

	// A compute instance created in STARTING with no state transition time.
	createdStarting := `{"status": {"state": "COMPUTE_INSTANCE_STATE_STARTING"}}`

	DescribeTable("clamps a first change time that collides with the creation fallback",
		func(ctx context.Context, suppliedTime string) {
			insertCreated(ctx, "compute_instances", "vm-1", createdStarting)
			Expect(update(ctx, "compute_instances", "vm-1", fmt.Sprintf(`{
				"status": {
					"state": "COMPUTE_INSTANCE_STATE_RUNNING",
					"state_transition_time": %q
				}
			}`, suppliedTime))).To(Succeed())
			Expect(storedPath(ctx, "compute_instances", "vm-1", "status.state_transition_time")).To(
				HaveValue(Equal("2026-01-01T10:00:00.000001Z")),
			)
		},
		Entry("first time exactly at the creation timestamp", "2026-01-01T10:00:00Z"),
		Entry("first time before the creation timestamp", "2025-12-31T09:00:00Z"),
	)

	It("passes a first change time after creation unchanged", func(ctx context.Context) {
		insertCreated(ctx, "compute_instances", "vm-1", createdStarting)
		Expect(update(ctx, "compute_instances", "vm-1", `{
			"status": {
				"state": "COMPUTE_INSTANCE_STATE_RUNNING",
				"state_transition_time": "2026-01-01T11:00:00Z"
			}
		}`)).To(Succeed())
		Expect(storedPath(ctx, "compute_instances", "vm-1", "status.state_transition_time")).To(
			HaveValue(Equal("2026-01-01T11:00:00Z")),
		)
	})

	It("never stamps the database clock below the creation fallback", func(ctx context.Context) {
		// A value first observed in the same transaction that created the row: the database
		// clock is the creation timestamp, so the stamp must move past it.
		tx, err := conn.Begin(ctx)
		Expect(err).ToNot(HaveOccurred())
		_, err = tx.Exec(ctx, `
			insert into compute_instances (id, name, tenant, project, data)
			values ('vm-1', 'vm-1', 'tenant-a', 'team-a', '{}'::jsonb)`)
		Expect(err).ToNot(HaveOccurred())
		_, err = tx.Exec(ctx, `
			update compute_instances
			set data = '{"status": {"state": "COMPUTE_INSTANCE_STATE_STARTING"}}'::jsonb
			where id = 'vm-1'`)
		Expect(err).ToNot(HaveOccurred())
		Expect(tx.Commit(ctx)).To(Succeed())

		var creation, stamped time.Time
		err = conn.QueryRow(ctx, `
			select
				creation_timestamp,
				(data #>> string_to_array('status.state_transition_time', '.'))::timestamptz
			from compute_instances where id = 'vm-1'`).Scan(&creation, &stamped)
		Expect(err).ToNot(HaveOccurred())
		Expect(stamped).To(Equal(creation.Add(time.Microsecond)))
	})

	It("warns when it clamps a first change time", func(ctx context.Context) {
		insertCreated(ctx, "compute_instances", "vm-1", createdStarting)

		// A separate connection, so the warning the trigger raises reaches the test:
		url, err := db.Url(ctx)
		Expect(err).ToNot(HaveOccurred())
		config, err := pgx.ParseConfig(url)
		Expect(err).ToNot(HaveOccurred())
		notices := make([]*pgconn.Notice, 0, 1)
		config.OnNotice = func(_ *pgconn.PgConn, notice *pgconn.Notice) {
			notices = append(notices, notice)
		}
		noticeConn, err := pgx.ConnectConfig(ctx, config)
		Expect(err).ToNot(HaveOccurred())
		DeferCleanup(noticeConn.Close)

		_, err = noticeConn.Exec(ctx, `
			update compute_instances set data = $2::jsonb where id = $1`, "vm-1", `{
				"status": {
					"state": "COMPUTE_INSTANCE_STATE_RUNNING",
					"state_transition_time": "2026-01-01T10:00:00Z"
				}
			}`)
		Expect(err).ToNot(HaveOccurred())

		Expect(notices).To(HaveLen(1))
		Expect(notices[0].Severity).To(Equal("WARNING"))
		Expect(notices[0].Message).To(ContainSubstring("compute_instances"))
		Expect(notices[0].Message).To(ContainSubstring("'vm-1'"))
		Expect(notices[0].Message).To(ContainSubstring("state_transition_time"))
		Expect(notices[0].Message).To(ContainSubstring("created 2026-01-01T10:00:00.000000Z"))
		Expect(notices[0].Message).To(ContainSubstring("2026-01-01T10:00:00.000001Z"))
	})
})

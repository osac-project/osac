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
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	. "github.com/onsi/ginkgo/v2/dsl/core"
	. "github.com/onsi/ginkgo/v2/dsl/table"
	. "github.com/onsi/gomega"
)

var _ = DescribeMigration("Clamp metering change times", func() {
	BeforeEach(func(ctx context.Context) {
		Expect(tool.Migrate(ctx, 129)).To(Succeed())
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

	// A compute instance that is running, observed with its running instance type.
	runningVM := `{
		"status": {
			"state": "COMPUTE_INSTANCE_STATE_RUNNING",
			"state_transition_time": "2026-01-01T10:00:00Z",
			"instance_type": {"id": "type-small"},
			"instance_type_transition_time": "2026-01-01T10:00:00Z"
		}
	}`

	DescribeTable("accepts a changed varying field without a later time and clamps the stored time",
		func(ctx context.Context, table, id, timePath, oldData, newData string) {
			insert(ctx, table, id, oldData)
			Expect(update(ctx, table, id, newData)).To(Succeed())
			Expect(storedPath(ctx, table, id, timePath)).To(
				HaveValue(Equal("2026-01-01T10:00:00.000001Z")),
			)
		},
		Entry("earlier time",
			"compute_instances", "vm-1", "status.state_transition_time", runningVM,
			`{
				"status": {
					"state": "COMPUTE_INSTANCE_STATE_STOPPED",
					"state_transition_time": "2026-01-01T09:00:00Z",
					"instance_type": {"id": "type-small"},
					"instance_type_transition_time": "2026-01-01T10:00:00Z"
				}
			}`),
		Entry("same time",
			"compute_instances", "vm-1", "status.state_transition_time", runningVM,
			`{
				"status": {
					"state": "COMPUTE_INSTANCE_STATE_STOPPED",
					"state_transition_time": "2026-01-01T10:00:00Z",
					"instance_type": {"id": "type-small"},
					"instance_type_transition_time": "2026-01-01T10:00:00Z"
				}
			}`),
		Entry("missing time",
			"compute_instances", "vm-1", "status.state_transition_time", runningVM,
			`{
				"status": {
					"state": "COMPUTE_INSTANCE_STATE_STOPPED",
					"instance_type": {"id": "type-small"},
					"instance_type_transition_time": "2026-01-01T10:00:00Z"
				}
			}`),
		Entry("external ip attachment with an earlier time",
			"external_ips", "ip-1", "status.attachment_transition_time",
			`{
				"status": {
					"state": "EXTERNAL_IP_STATE_ALLOCATED",
					"state_transition_time": "2026-01-01T10:00:00Z",
					"attached": true,
					"attachment_transition_time": "2026-01-01T10:00:00Z"
				}
			}`,
			`{
				"status": {
					"state": "EXTERNAL_IP_STATE_ALLOCATED",
					"state_transition_time": "2026-01-01T10:00:00Z",
					"attached": false,
					"attachment_transition_time": "2026-01-01T09:00:00Z"
				}
			}`),
	)

	It("keeps a supplied time that is later than the stored one", func(ctx context.Context) {
		insert(ctx, "compute_instances", "vm-1", runningVM)
		Expect(update(ctx, "compute_instances", "vm-1", `{
			"status": {
				"state": "COMPUTE_INSTANCE_STATE_STOPPED",
				"state_transition_time": "2026-01-01T11:00:00Z",
				"instance_type": {"id": "type-small"},
				"instance_type_transition_time": "2026-01-01T10:00:00Z"
			}
		}`)).To(Succeed())
		Expect(storedPath(ctx, "compute_instances", "vm-1", "status.state_transition_time")).To(
			HaveValue(Equal("2026-01-01T11:00:00Z")),
		)
	})

	It("stamps the database clock when a first set carries no time", func(ctx context.Context) {
		insert(ctx, "compute_instances", "vm-1", `{}`)
		Expect(update(ctx, "compute_instances", "vm-1",
			`{"status": {"state": "COMPUTE_INSTANCE_STATE_STARTING"}}`)).To(Succeed())

		var stamped, databaseClock time.Time
		err := conn.QueryRow(ctx, `
			select
				now(),
				(data #>> string_to_array('status.state_transition_time', '.'))::timestamptz
			from compute_instances where id = 'vm-1'`).Scan(&databaseClock, &stamped)
		Expect(err).ToNot(HaveOccurred())
		Expect(stamped).To(BeTemporally("~", databaseClock, time.Minute))
	})

	It("keeps the old time when a write changes only the time", func(ctx context.Context) {
		insert(ctx, "compute_instances", "vm-1", runningVM)
		Expect(update(ctx, "compute_instances", "vm-1", `{
			"status": {
				"state": "COMPUTE_INSTANCE_STATE_RUNNING",
				"state_transition_time": "2026-01-02T10:00:00Z",
				"instance_type": {"id": "type-small"},
				"instance_type_transition_time": "2026-01-01T10:00:00Z"
			}
		}`)).To(Succeed())
		Expect(storedPath(ctx, "compute_instances", "vm-1", "status.state_transition_time")).To(
			HaveValue(Equal("2026-01-01T10:00:00Z")),
		)
	})

	It("warns when it clamps a change time", func(ctx context.Context) {
		insert(ctx, "compute_instances", "vm-1", runningVM)

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
					"state": "COMPUTE_INSTANCE_STATE_STOPPED",
					"state_transition_time": "2026-01-01T09:00:00Z",
					"instance_type": {"id": "type-small"},
					"instance_type_transition_time": "2026-01-01T10:00:00Z"
				}
			}`)
		Expect(err).ToNot(HaveOccurred())

		Expect(notices).To(HaveLen(1))
		Expect(notices[0].Severity).To(Equal("WARNING"))
		Expect(notices[0].Message).To(ContainSubstring("compute_instances"))
		Expect(notices[0].Message).To(ContainSubstring("'vm-1'"))
		Expect(notices[0].Message).To(ContainSubstring("state_transition_time"))
		Expect(notices[0].Message).To(ContainSubstring("2026-01-01T09:00:00Z"))
		Expect(notices[0].Message).To(ContainSubstring("2026-01-01T10:00:00.000001Z"))
	})

	It("still rejects changing a field that never changes after it is set", func(ctx context.Context) {
		insert(ctx, "compute_instances", "vm-1",
			`{"spec": {"disk_image": {"id": "img-1"}}, "status": {"state": "COMPUTE_INSTANCE_STATE_RUNNING", "state_transition_time": "2026-01-01T10:00:00Z"}}`)
		err := update(ctx, "compute_instances", "vm-1",
			`{"spec": {"disk_image": {"id": "img-2"}}, "status": {"state": "COMPUTE_INSTANCE_STATE_RUNNING", "state_transition_time": "2026-01-01T10:00:00Z"}}`)
		var pgErr *pgconn.PgError
		Expect(errors.As(err, &pgErr)).To(BeTrue())
		Expect(pgErr.Code).To(Equal("Z0006"))
	})
})

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
	"google.golang.org/protobuf/proto"
)

var _ = DescribeMigration("Reject earlier metering change times", func() {
	BeforeEach(func(ctx context.Context) {
		Expect(tool.Migrate(ctx, 137)).To(Succeed())
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

	// insertCreated inserts a resource created with a tracked value and no time, at a controlled
	// creation timestamp: the state the fold keys at the creation fallback.
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
		// The rejected write left the stored time untouched:
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

	// The backdating regression this migration exists for: a writer that changes the instance
	// type and carries a timestamp earlier than the previous type's (the operator reused a stale
	// condition transition time) must be rejected, not silently rewritten just past the previous
	// type's time, which would move the span between the two times to the new pricing dimension.
	It("rejects a changed varying field with an earlier time", func(ctx context.Context) {
		insert(ctx, "vm-1", runningVM)
		expectRejection(ctx, "vm-1", "status.instance_type_transition_time", `{
			"status": {
				"state": "COMPUTE_INSTANCE_STATE_RUNNING",
				"state_transition_time": "2026-01-01T10:00:00Z",
				"instance_type": {"id": "type-large"},
				"instance_type_transition_time": "2026-01-01T09:00:00Z"
			}
		}`, proto.String("2026-01-01T10:00:00Z"))
	})

	It("rejects a changed varying field with an earlier state time", func(ctx context.Context) {
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

	DescribeTable("rejects a changed varying field whose time is missing",
		func(ctx context.Context, id, timePath, oldData, newData string, wantStored *string) {
			insert(ctx, id, oldData)
			expectRejection(ctx, id, timePath, newData, wantStored)
		},
		// The backdating regression: RUNNING since 10:00, FAILED written at 15:00 with no time,
		// must be rejected rather than stored as FAILED since 10:00:00.000001, which would erase
		// five hours of billing.
		Entry("missing time",
			"vm-1", "status.state_transition_time", runningVM,
			`{
				"status": {
					"state": "COMPUTE_INSTANCE_STATE_FAILED",
					"instance_type": {"id": "type-small"},
					"instance_type_transition_time": "2026-01-01T10:00:00Z"
				}
			}`,
			proto.String("2026-01-01T10:00:00Z")),
		Entry("missing time on the first change of a value",
			"vm-1", "status.state_transition_time",
			`{"status": {"state": "COMPUTE_INSTANCE_STATE_STARTING"}}`,
			`{"status": {"state": "COMPUTE_INSTANCE_STATE_RUNNING"}}`,
			nil),
	)

	// The granularity collision kept from migration 133: two changes of a whole-second CR
	// timestamp land on the same second, so the supplied time equals the stored one.
	It("clamps a changed varying field with a time equal to the stored one", func(ctx context.Context) {
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
			HaveValue(Equal("2026-01-01T10:00:00.000001Z")),
		)
	})

	// The whole-second truncation tolerance: Kubernetes CR timestamps carry no fractional
	// part, so a change observed in the same second as a stored microsecond time arrives
	// truncated down past it. Rejecting that as backdated wedged every resource whose
	// feedback controller reports CR times against a stored microsecond time: the writer
	// retried the same fixed CR time forever and the resource never finished deleting.
	It("clamps a CR-truncated time earlier within the stored second", func(ctx context.Context) {
		insert(ctx, "vm-1", `{
			"status": {
				"state": "COMPUTE_INSTANCE_STATE_RUNNING",
				"state_transition_time": "2026-01-01T10:00:00.500000Z",
				"instance_type": {"id": "type-small"},
				"instance_type_transition_time": "2026-01-01T10:00:00Z"
			}
		}`)
		Expect(update(ctx, "vm-1", `{
			"status": {
				"state": "COMPUTE_INSTANCE_STATE_STOPPED",
				"state_transition_time": "2026-01-01T10:00:00Z",
				"instance_type": {"id": "type-small"},
				"instance_type_transition_time": "2026-01-01T10:00:00Z"
			}
		}`)).To(Succeed())
		Expect(storedPath(ctx, "vm-1", "status.state_transition_time")).To(
			HaveValue(Equal("2026-01-01T10:00:00.500001Z")),
		)
	})

	It("still rejects a time from before the stored second", func(ctx context.Context) {
		insert(ctx, "vm-1", `{
			"status": {
				"state": "COMPUTE_INSTANCE_STATE_RUNNING",
				"state_transition_time": "2026-01-01T10:00:00.500000Z",
				"instance_type": {"id": "type-small"},
				"instance_type_transition_time": "2026-01-01T10:00:00Z"
			}
		}`)
		expectRejection(ctx, "vm-1", "status.state_transition_time", `{
			"status": {
				"state": "COMPUTE_INSTANCE_STATE_STOPPED",
				"state_transition_time": "2026-01-01T09:59:59Z",
				"instance_type": {"id": "type-small"},
				"instance_type_transition_time": "2026-01-01T10:00:00Z"
			}
		}`, proto.String("2026-01-01T10:00:00.500000Z"))
	})

	It("keeps a supplied time that is later than the stored one", func(ctx context.Context) {
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

	DescribeTable("clamps a first change time that collides with the creation fallback",
		func(ctx context.Context, suppliedTime string) {
			insertCreated(ctx, "vm-1", `{"status": {"state": "COMPUTE_INSTANCE_STATE_STARTING"}}`)
			Expect(update(ctx, "vm-1", fmt.Sprintf(`{
				"status": {
					"state": "COMPUTE_INSTANCE_STATE_RUNNING",
					"state_transition_time": %q
				}
			}`, suppliedTime))).To(Succeed())
			Expect(storedPath(ctx, "vm-1", "status.state_transition_time")).To(
				HaveValue(Equal("2026-01-01T10:00:00.000001Z")),
			)
		},
		Entry("first time exactly at the creation timestamp", "2026-01-01T10:00:00Z"),
		Entry("first time before the creation timestamp", "2025-12-31T09:00:00Z"),
	)

	It("passes a first change time after creation unchanged", func(ctx context.Context) {
		insertCreated(ctx, "vm-1", `{"status": {"state": "COMPUTE_INSTANCE_STATE_STARTING"}}`)
		Expect(update(ctx, "vm-1", `{
			"status": {
				"state": "COMPUTE_INSTANCE_STATE_RUNNING",
				"state_transition_time": "2026-01-01T11:00:00Z"
			}
		}`)).To(Succeed())
		Expect(storedPath(ctx, "vm-1", "status.state_transition_time")).To(
			HaveValue(Equal("2026-01-01T11:00:00Z")),
		)
	})

	It("keeps the old time when a write changes only the time", func(ctx context.Context) {
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

	It("still rejects changing a field that never changes after it is set", func(ctx context.Context) {
		insert(ctx, "vm-1",
			`{"spec": {"disk_image": {"id": "img-1"}}, "status": {"state": "COMPUTE_INSTANCE_STATE_RUNNING", "state_transition_time": "2026-01-01T10:00:00Z"}}`)
		err := update(ctx, "vm-1",
			`{"spec": {"disk_image": {"id": "img-2"}}, "status": {"state": "COMPUTE_INSTANCE_STATE_RUNNING", "state_transition_time": "2026-01-01T10:00:00Z"}}`)
		var pgErr *pgconn.PgError
		Expect(errors.As(err, &pgErr)).To(BeTrue())
		Expect(pgErr.Code).To(Equal("Z0006"))
	})
})

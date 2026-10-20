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

var _ = DescribeMigration("Add metering source contract", func() {
	BeforeEach(func(ctx context.Context) {
		Expect(tool.Migrate(ctx, 128)).To(Succeed())
		_, err := conn.Exec(ctx, `
			insert into tenants (id, name, tenant, creator, data)
			values ('tenant-a', 'tenant-a', 'tenant-a', 'system', '{}');
			insert into projects (id, name, tenant, project, creator, data)
			values ('project-a', 'team-a', 'tenant-a', 'team-a', 'system', '{}');
			insert into external_ip_pools (id, name, tenant, project, data)
			values ('pool-1', 'pool-1', 'tenant-a', 'team-a', '{}')`)
		Expect(err).ToNot(HaveOccurred())
		_, err = conn.Exec(ctx, `
			insert into external_ips (id, name, tenant, project, data)
			values ('nat-eip', 'nat-eip', 'tenant-a', 'team-a', $1::jsonb)`,
			`{"spec": {"pool": {"id": "pool-1"}}}`)
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

	// A compute instance that is running, observed with its running instance type.
	runningVM := `{
		"status": {
			"state": "COMPUTE_INSTANCE_STATE_RUNNING",
			"state_transition_time": "2026-01-01T10:00:00Z",
			"instance_type": {"id": "type-small"},
			"instance_type_transition_time": "2026-01-01T10:00:00Z"
		}
	}`

	DescribeTable("accepts a changed varying field with a later time",
		func(ctx context.Context, table, id, oldData, newData string) {
			insert(ctx, table, id, oldData)
			Expect(update(ctx, table, id, newData)).To(Succeed())
		},
		Entry("state",
			"compute_instances", "vm-1", runningVM,
			`{
				"status": {
					"state": "COMPUTE_INSTANCE_STATE_STOPPED",
					"state_transition_time": "2026-01-01T11:00:00Z",
					"instance_type": {"id": "type-small"},
					"instance_type_transition_time": "2026-01-01T10:00:00Z"
				}
			}`),
		Entry("instance type",
			"compute_instances", "vm-1", runningVM,
			`{
				"status": {
					"state": "COMPUTE_INSTANCE_STATE_RUNNING",
					"state_transition_time": "2026-01-01T10:00:00Z",
					"instance_type": {"id": "type-large"},
					"instance_type_transition_time": "2026-01-01T12:00:00Z"
				}
			}`),
		Entry("first state set with its time",
			"compute_instances", "vm-1", `{}`,
			`{"status": {"state": "COMPUTE_INSTANCE_STATE_STARTING", "state_transition_time": "2026-01-01T09:00:00Z"}}`),
		Entry("cluster version",
			"clusters", "cluster-1",
			`{
				"spec": {"version": {"id": "v1"}},
				"status": {"state": "CLUSTER_STATE_READY", "state_transition_time": "2026-01-01T10:00:00Z", "version_transition_time": "2026-01-01T10:00:00Z"}
			}`,
			`{
				"spec": {"version": {"id": "v2"}},
				"status": {"state": "CLUSTER_STATE_READY", "state_transition_time": "2026-01-01T10:00:00Z", "version_transition_time": "2026-01-01T11:00:00Z"}
			}`),
		Entry("external ip attachment",
			"external_ips", "ip-1",
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
					"attachment_transition_time": "2026-01-01T11:00:00Z"
				}
			}`),
		Entry("external ip attribution",
			"external_ips", "ip-1",
			`{
				"status": {
					"state": "EXTERNAL_IP_STATE_ALLOCATED",
					"state_transition_time": "2026-01-01T10:00:00Z",
					"attribution": {"resource_type": "COMPUTE_INSTANCE", "resource_id": "vm-1"},
					"attachment_transition_time": "2026-01-01T10:00:00Z"
				}
			}`,
			`{
				"status": {
					"state": "EXTERNAL_IP_STATE_ALLOCATED",
					"state_transition_time": "2026-01-01T10:00:00Z",
					"attribution": {"resource_type": "COMPUTE_INSTANCE", "resource_id": "vm-2"},
					"attachment_transition_time": "2026-01-01T11:00:00Z"
				}
			}`),
		Entry("external ip attachment and attribution together",
			"external_ips", "ip-1",
			`{
				"status": {
					"state": "EXTERNAL_IP_STATE_ALLOCATED",
					"state_transition_time": "2026-01-01T10:00:00Z",
					"attached": false,
					"attribution": {"resource_type": "COMPUTE_INSTANCE", "resource_id": "vm-1"},
					"attachment_transition_time": "2026-01-01T10:00:00Z"
				}
			}`,
			`{
				"status": {
					"state": "EXTERNAL_IP_STATE_ALLOCATED",
					"state_transition_time": "2026-01-01T10:00:00Z",
					"attached": true,
					"attribution": {"resource_type": "COMPUTE_INSTANCE", "resource_id": "vm-2"},
					"attachment_transition_time": "2026-01-01T11:00:00Z"
				}
			}`),
		Entry("volume state",
			"volumes", "volume-1",
			`{"status": {"state": "VOLUME_STATE_CREATING", "state_transition_time": "2026-01-01T10:00:00Z"}}`,
			`{"status": {"state": "VOLUME_STATE_AVAILABLE", "state_transition_time": "2026-01-01T11:00:00Z"}}`),
		Entry("bare metal instance state",
			"bare_metal_instances", "bmi-1",
			`{"status": {"state": "BARE_METAL_INSTANCE_STATE_STARTING", "state_transition_time": "2026-01-01T10:00:00Z"}}`,
			`{"status": {"state": "BARE_METAL_INSTANCE_STATE_RUNNING", "state_transition_time": "2026-01-01T11:00:00Z"}}`),
		Entry("nat gateway state",
			"nat_gateways", "nat-1",
			`{"status": {"state": "NAT_GATEWAY_STATE_PENDING", "state_transition_time": "2026-01-01T10:00:00Z"}}`,
			`{"status": {"state": "NAT_GATEWAY_STATE_READY", "state_transition_time": "2026-01-01T11:00:00Z"}}`),
	)

	DescribeTable("rejects a changed varying field without a later time",
		func(ctx context.Context, table, id, oldData, newData string) {
			insert(ctx, table, id, oldData)
			expectContractViolation(update(ctx, table, id, newData))
		},
		Entry("same time",
			"compute_instances", "vm-1", runningVM,
			`{
				"status": {
					"state": "COMPUTE_INSTANCE_STATE_STOPPED",
					"state_transition_time": "2026-01-01T10:00:00Z",
					"instance_type": {"id": "type-small"},
					"instance_type_transition_time": "2026-01-01T10:00:00Z"
				}
			}`),
		Entry("earlier time",
			"compute_instances", "vm-1", runningVM,
			`{
				"status": {
					"state": "COMPUTE_INSTANCE_STATE_STOPPED",
					"state_transition_time": "2026-01-01T09:00:00Z",
					"instance_type": {"id": "type-small"},
					"instance_type_transition_time": "2026-01-01T10:00:00Z"
				}
			}`),
		Entry("missing time",
			"compute_instances", "vm-1", runningVM,
			`{
				"status": {
					"state": "COMPUTE_INSTANCE_STATE_STOPPED",
					"instance_type": {"id": "type-small"},
					"instance_type_transition_time": "2026-01-01T10:00:00Z"
				}
			}`),
		Entry("missing time on the first set",
			"compute_instances", "vm-1", `{}`,
			`{"status": {"state": "COMPUTE_INSTANCE_STATE_STARTING"}}`),
		Entry("instance type with an earlier time",
			"compute_instances", "vm-1", runningVM,
			`{
				"status": {
					"state": "COMPUTE_INSTANCE_STATE_RUNNING",
					"state_transition_time": "2026-01-01T10:00:00Z",
					"instance_type": {"id": "type-large"},
					"instance_type_transition_time": "2026-01-01T09:00:00Z"
				}
			}`),
		Entry("cluster version without a later time",
			"clusters", "cluster-1",
			`{
				"spec": {"version": {"id": "v1"}},
				"status": {"state": "CLUSTER_STATE_READY", "state_transition_time": "2026-01-01T10:00:00Z", "version_transition_time": "2026-01-01T10:00:00Z"}
			}`,
			`{
				"spec": {"version": {"id": "v2"}},
				"status": {"state": "CLUSTER_STATE_READY", "state_transition_time": "2026-01-01T10:00:00Z", "version_transition_time": "2026-01-01T10:00:00Z"}
			}`),
		Entry("external ip attachment without a later time",
			"external_ips", "ip-1",
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
					"attachment_transition_time": "2026-01-01T10:00:00Z"
				}
			}`),
		Entry("external ip attribution without a later time",
			"external_ips", "ip-1",
			`{
				"status": {
					"state": "EXTERNAL_IP_STATE_ALLOCATED",
					"state_transition_time": "2026-01-01T10:00:00Z",
					"attached": true,
					"attribution": {"resource_type": "COMPUTE_INSTANCE", "resource_id": "vm-1"},
					"attachment_transition_time": "2026-01-01T10:00:00Z"
				}
			}`,
			`{
				"status": {
					"state": "EXTERNAL_IP_STATE_ALLOCATED",
					"state_transition_time": "2026-01-01T10:00:00Z",
					"attached": true,
					"attribution": {"resource_type": "COMPUTE_INSTANCE", "resource_id": "vm-2"},
					"attachment_transition_time": "2026-01-01T10:00:00Z"
				}
			}`),
		Entry("bare metal instance state without a later time",
			"bare_metal_instances", "bmi-1",
			`{"status": {"state": "BARE_METAL_INSTANCE_STATE_STARTING", "state_transition_time": "2026-01-01T10:00:00Z"}}`,
			`{"status": {"state": "BARE_METAL_INSTANCE_STATE_RUNNING", "state_transition_time": "2026-01-01T10:00:00Z"}}`),
	)

	It("rejects changing a bare metal instance node set with the owner-reference error", func(ctx context.Context) {
		// The owner-reference trigger of migration 125 guards the same status fields and, by
		// trigger name order, fires before the metering trigger, so the caller sees its error.
		insert(ctx, "bare_metal_instances", "bmi-1",
			`{"status": {"state": "BARE_METAL_INSTANCE_STATE_RUNNING", "state_transition_time": "2026-01-01T10:00:00Z", "node_set": "compute"}}`)
		err := update(ctx, "bare_metal_instances", "bmi-1",
			`{"status": {"state": "BARE_METAL_INSTANCE_STATE_RUNNING", "state_transition_time": "2026-01-01T10:00:00Z", "node_set": "gpu"}}`)
		var pgErr *pgconn.PgError
		Expect(errors.As(err, &pgErr)).To(BeTrue())
		Expect(pgErr.Code).To(Equal("Z0005"))
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
		Expect(storedPath(ctx, "compute_instances", "vm-1", "status.state")).To(
			HaveValue(Equal("COMPUTE_INSTANCE_STATE_RUNNING")),
		)
		Expect(storedPath(ctx, "compute_instances", "vm-1", "status.state_transition_time")).To(
			HaveValue(Equal("2026-01-01T10:00:00Z")),
		)
	})

	It("keeps the old attachment time when a write changes only the time", func(ctx context.Context) {
		// Attached and attribution share attachment_transition_time, so the time belongs to the
		// group, not to either field.
		insert(ctx, "external_ips", "ip-1", `{
			"status": {
				"state": "EXTERNAL_IP_STATE_ALLOCATED",
				"state_transition_time": "2026-01-01T10:00:00Z",
				"attached": true,
				"attribution": {"resource_type": "COMPUTE_INSTANCE", "resource_id": "vm-1"},
				"attachment_transition_time": "2026-01-01T10:00:00Z"
			}
		}`)
		Expect(update(ctx, "external_ips", "ip-1", `{
			"status": {
				"state": "EXTERNAL_IP_STATE_ALLOCATED",
				"state_transition_time": "2026-01-01T10:00:00Z",
				"attached": true,
				"attribution": {"resource_type": "COMPUTE_INSTANCE", "resource_id": "vm-1"},
				"attachment_transition_time": "2026-01-02T10:00:00Z"
			}
		}`)).To(Succeed())
		Expect(storedPath(ctx, "external_ips", "ip-1", "status.attached")).To(HaveValue(Equal("true")))
		Expect(storedPath(ctx, "external_ips", "ip-1", "status.attachment_transition_time")).To(
			HaveValue(Equal("2026-01-01T10:00:00Z")),
		)
	})

	It("drops a new time when the value has none yet and doesn't change", func(ctx context.Context) {
		insert(ctx, "compute_instances", "vm-1",
			`{"status": {"state": "COMPUTE_INSTANCE_STATE_RUNNING"}}`)
		Expect(update(ctx, "compute_instances", "vm-1", `{
			"status": {
				"state": "COMPUTE_INSTANCE_STATE_RUNNING",
				"state_transition_time": "2026-01-01T10:00:00Z"
			}
		}`)).To(Succeed())
		Expect(storedPath(ctx, "compute_instances", "vm-1", "status.state_transition_time")).To(BeNil())
	})

	DescribeTable("rejects changing a field that never changes after it is set",
		func(ctx context.Context, table, id, oldData, newData string) {
			insert(ctx, table, id, oldData)
			expectContractViolation(update(ctx, table, id, newData))
		},
		Entry("compute instance image",
			"compute_instances", "vm-1",
			`{"spec": {"disk_image": {"id": "img-1"}}, "status": {"state": "COMPUTE_INSTANCE_STATE_RUNNING", "state_transition_time": "2026-01-01T10:00:00Z"}}`,
			`{"spec": {"disk_image": {"id": "img-2"}}, "status": {"state": "COMPUTE_INSTANCE_STATE_RUNNING", "state_transition_time": "2026-01-01T10:00:00Z"}}`),
		Entry("compute instance boot disk size",
			"compute_instances", "vm-1",
			`{"spec": {"boot_disk": {"size_gib": 10}}, "status": {"state": "COMPUTE_INSTANCE_STATE_RUNNING", "state_transition_time": "2026-01-01T10:00:00Z"}}`,
			`{"spec": {"boot_disk": {"size_gib": 20}}, "status": {"state": "COMPUTE_INSTANCE_STATE_RUNNING", "state_transition_time": "2026-01-01T10:00:00Z"}}`),
		Entry("compute instance boot disk size set after creation",
			"compute_instances", "vm-1",
			`{"spec": {}, "status": {"state": "COMPUTE_INSTANCE_STATE_RUNNING", "state_transition_time": "2026-01-01T10:00:00Z"}}`,
			`{"spec": {"boot_disk": {"size_gib": 10}}, "status": {"state": "COMPUTE_INSTANCE_STATE_RUNNING", "state_transition_time": "2026-01-01T10:00:00Z"}}`),
		Entry("cluster template",
			"clusters", "cluster-1",
			`{"spec": {"template": {"id": "tpl-1"}}, "status": {"state": "CLUSTER_STATE_READY", "state_transition_time": "2026-01-01T10:00:00Z"}}`,
			`{"spec": {"template": {"id": "tpl-2"}}, "status": {"state": "CLUSTER_STATE_READY", "state_transition_time": "2026-01-01T10:00:00Z"}}`),
		Entry("bare metal instance type",
			"bare_metal_instances", "bmi-1",
			`{"spec": {"instance_type": {"id": "type-1"}}, "status": {"state": "BARE_METAL_INSTANCE_STATE_RUNNING", "state_transition_time": "2026-01-01T10:00:00Z"}}`,
			`{"spec": {"instance_type": {"id": "type-2"}}, "status": {"state": "BARE_METAL_INSTANCE_STATE_RUNNING", "state_transition_time": "2026-01-01T10:00:00Z"}}`),
		Entry("volume storage tier",
			"volumes", "volume-1",
			`{"spec": {"storage_tier": "tier-1"}, "status": {"state": "VOLUME_STATE_AVAILABLE", "state_transition_time": "2026-01-01T10:00:00Z"}}`,
			`{"spec": {"storage_tier": "tier-2"}, "status": {"state": "VOLUME_STATE_AVAILABLE", "state_transition_time": "2026-01-01T10:00:00Z"}}`),
		Entry("volume vendor volume id",
			"volumes", "volume-1",
			`{"spec": {"storage_tier": "tier-1"}, "status": {"state": "VOLUME_STATE_AVAILABLE", "state_transition_time": "2026-01-01T10:00:00Z", "vendor_volume_id": "vendor-1"}}`,
			`{"spec": {"storage_tier": "tier-1"}, "status": {"state": "VOLUME_STATE_AVAILABLE", "state_transition_time": "2026-01-01T10:00:00Z", "vendor_volume_id": "vendor-2"}}`),
		Entry("volume vendor volume id cleared",
			"volumes", "volume-1",
			`{"spec": {"storage_tier": "tier-1"}, "status": {"state": "VOLUME_STATE_AVAILABLE", "state_transition_time": "2026-01-01T10:00:00Z", "vendor_volume_id": "vendor-1"}}`,
			`{"spec": {"storage_tier": "tier-1"}, "status": {"state": "VOLUME_STATE_AVAILABLE", "state_transition_time": "2026-01-01T10:00:00Z"}}`),
		Entry("volume protocol",
			"volumes", "volume-1",
			`{"spec": {"storage_tier": "tier-1"}, "status": {"state": "VOLUME_STATE_AVAILABLE", "state_transition_time": "2026-01-01T10:00:00Z", "protocol": "STORAGE_PROTOCOL_BLOCK"}}`,
			`{"spec": {"storage_tier": "tier-1"}, "status": {"state": "VOLUME_STATE_AVAILABLE", "state_transition_time": "2026-01-01T10:00:00Z", "protocol": "STORAGE_PROTOCOL_NFS"}}`),
		Entry("volume provisioned size",
			"volumes", "volume-1",
			`{"spec": {"storage_tier": "tier-1"}, "status": {"state": "VOLUME_STATE_AVAILABLE", "state_transition_time": "2026-01-01T10:00:00Z", "provisioned_size_gib": 100}}`,
			`{"spec": {"storage_tier": "tier-1"}, "status": {"state": "VOLUME_STATE_AVAILABLE", "state_transition_time": "2026-01-01T10:00:00Z", "provisioned_size_gib": 200}}`),
		Entry("external ip address",
			"external_ips", "ip-1",
			`{
				"status": {
					"state": "EXTERNAL_IP_STATE_ALLOCATED",
					"state_transition_time": "2026-01-01T10:00:00Z",
					"address": "192.0.2.10"
				}
			}`,
			`{
				"status": {
					"state": "EXTERNAL_IP_STATE_ALLOCATED",
					"state_transition_time": "2026-01-01T10:00:00Z",
					"address": "192.0.2.11"
				}
			}`),
		Entry("external ip address cleared",
			"external_ips", "ip-1",
			`{
				"status": {
					"state": "EXTERNAL_IP_STATE_ALLOCATED",
					"state_transition_time": "2026-01-01T10:00:00Z",
					"address": "192.0.2.10"
				}
			}`,
			`{
				"status": {
					"state": "EXTERNAL_IP_STATE_ALLOCATED",
					"state_transition_time": "2026-01-01T10:00:00Z"
				}
			}`),
		Entry("external ip pool",
			"external_ips", "ip-1",
			`{"spec": {"pool": {"id": "pool-1"}}, "status": {"state": "EXTERNAL_IP_STATE_ALLOCATED", "state_transition_time": "2026-01-01T10:00:00Z"}}`,
			`{"spec": {"pool": {"id": "pool-2"}}, "status": {"state": "EXTERNAL_IP_STATE_ALLOCATED", "state_transition_time": "2026-01-01T10:00:00Z"}}`),
		Entry("nat gateway virtual network",
			"nat_gateways", "nat-1",
			`{"spec": {"virtual_network": {"id": "vn-1"}}, "status": {"state": "NAT_GATEWAY_STATE_READY", "state_transition_time": "2026-01-01T10:00:00Z"}}`,
			`{"spec": {"virtual_network": {"id": "vn-2"}}, "status": {"state": "NAT_GATEWAY_STATE_READY", "state_transition_time": "2026-01-01T10:00:00Z"}}`),
		Entry("nat gateway external ip",
			"nat_gateways", "nat-1",
			`{"spec": {"external_ip": {"id": "nat-eip"}}, "status": {"state": "NAT_GATEWAY_STATE_READY", "state_transition_time": "2026-01-01T10:00:00Z"}}`,
			`{"spec": {"external_ip": {"id": "ip-2"}}, "status": {"state": "NAT_GATEWAY_STATE_READY", "state_transition_time": "2026-01-01T10:00:00Z"}}`),
	)

	DescribeTable("accepts setting a set-once field that is still empty",
		func(ctx context.Context, table, id, oldData, newData string) {
			insert(ctx, table, id, oldData)
			Expect(update(ctx, table, id, newData)).To(Succeed())
		},
		Entry("external ip address",
			"external_ips", "ip-1",
			`{
				"status": {
					"state": "EXTERNAL_IP_STATE_PENDING",
					"state_transition_time": "2026-01-01T10:00:00Z"
				}
			}`,
			`{
				"status": {
					"state": "EXTERNAL_IP_STATE_PENDING",
					"state_transition_time": "2026-01-01T10:00:00Z",
					"address": "192.0.2.10"
				}
			}`),
		Entry("volume vendor volume id",
			"volumes", "volume-1",
			`{"spec": {"storage_tier": "tier-1"}, "status": {"state": "VOLUME_STATE_CREATING", "state_transition_time": "2026-01-01T10:00:00Z"}}`,
			`{"spec": {"storage_tier": "tier-1"}, "status": {"state": "VOLUME_STATE_CREATING", "state_transition_time": "2026-01-01T10:00:00Z", "vendor_volume_id": "vendor-1"}}`),
		Entry("volume provisioned size",
			"volumes", "volume-1",
			`{"spec": {"storage_tier": "tier-1"}, "status": {"state": "VOLUME_STATE_CREATING", "state_transition_time": "2026-01-01T10:00:00Z"}}`,
			`{"spec": {"storage_tier": "tier-1"}, "status": {"state": "VOLUME_STATE_CREATING", "state_transition_time": "2026-01-01T10:00:00Z", "provisioned_size_gib": 100}}`),
	)

	It("accepts writes that touch no tracked field", func(ctx context.Context) {
		insert(ctx, "compute_instances", "vm-1", runningVM)
		Expect(update(ctx, "compute_instances", "vm-1", `{
			"status": {
				"state": "COMPUTE_INSTANCE_STATE_RUNNING",
				"state_transition_time": "2026-01-01T10:00:00Z",
				"internal_ip_address": "192.168.1.10",
				"instance_type": {"id": "type-small"},
				"instance_type_transition_time": "2026-01-01T10:00:00Z"
			}
		}`)).To(Succeed())

		// Restating the same values with the same times is also a legal write:
		Expect(update(ctx, "compute_instances", "vm-1", runningVM)).To(Succeed())
	})
})

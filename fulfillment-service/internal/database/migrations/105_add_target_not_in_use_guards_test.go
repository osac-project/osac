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

	"github.com/jackc/pgx/v5/pgconn"
	. "github.com/onsi/ginkgo/v2/dsl/core"
	. "github.com/onsi/gomega"
)

var _ = DescribeMigration("Networking dependency guards", func() {
	BeforeEach(func(ctx context.Context) {
		err := tool.Migrate(ctx, 105)
		Expect(err).ToNot(HaveOccurred())

		_, err = conn.Exec(ctx,
			`insert into external_ip_pools (id, name, tenant, data) values ('pool-1', 'pool-1', 'shared', '{}')`)
		Expect(err).ToNot(HaveOccurred())
	})

	// -- helpers --

	insertVN := func(ctx context.Context, id string) {
		_, err := conn.Exec(ctx,
			`insert into virtual_networks (id, name, tenant, data) values ($1, $1, 'system', '{}')`, id)
		Expect(err).ToNot(HaveOccurred())
	}

	insertSubnet := func(ctx context.Context, id, vnID string) {
		_, err := conn.Exec(ctx,
			`insert into subnets (id, name, tenant, data) values ($1, $1, 'system', $2::jsonb)`,
			id, `{"spec":{"virtual_network":{"id":"`+vnID+`"}}}`)
		Expect(err).ToNot(HaveOccurred())
	}

	insertBMI := func(ctx context.Context, id, subnetID string) {
		data := `{}`
		if subnetID != "" {
			data = `{"spec":{"network_attachments":[{"subnet":{"id":"` + subnetID + `"}}]}}`
		}
		_, err := conn.Exec(ctx,
			`insert into bare_metal_instances (id, name, tenant, data) values ($1, $1, 'system', $2::jsonb)`,
			id, data)
		Expect(err).ToNot(HaveOccurred())
	}

	insertCI := func(ctx context.Context, id string) {
		_, err := conn.Exec(ctx,
			`insert into compute_instances (id, name, tenant, data) values ($1, $1, 'system', '{}')`, id)
		Expect(err).ToNot(HaveOccurred())
	}

	insertCluster := func(ctx context.Context, id string) {
		_, err := conn.Exec(ctx,
			`insert into clusters (id, name, tenant, data) values ($1, $1, 'system', '{}')`, id)
		Expect(err).ToNot(HaveOccurred())
	}

	insertEIP := func(ctx context.Context, id string) {
		_, err := conn.Exec(ctx,
			`insert into external_ips (id, name, tenant, data) values ($1, $1, 'shared', '{"spec":{"pool":{"id":"pool-1"}},"status":{"state":"EXTERNAL_IP_STATE_ALLOCATED"}}')`, id)
		Expect(err).ToNot(HaveOccurred())
	}

	insertEIPA := func(ctx context.Context, id, eipID, targetField, targetID string) {
		data := `{"spec":{"external_ip":{"id":"` + eipID + `"},"` + targetField + `":{"id":"` + targetID + `"}}}`
		_, err := conn.Exec(ctx,
			`insert into external_ip_attachments (id, name, tenant, data) values ($1, $1, 'system', $2::jsonb)`,
			id, data)
		Expect(err).ToNot(HaveOccurred())
	}

	softDelete := func(ctx context.Context, table, id string) error {
		_, err := conn.Exec(ctx,
			`update `+table+` set deletion_timestamp = now() where id = $1`, id)
		return err
	}

	expectPgErr := func(err error, code string) *pgconn.PgError {
		Expect(err).To(HaveOccurred())
		var pgErr *pgconn.PgError
		ExpectWithOffset(1, errors.As(err, &pgErr)).To(BeTrue())
		ExpectWithOffset(1, pgErr.Code).To(Equal(code))
		return pgErr
	}

	// -- 1. Subnet → BMI soft-delete guard --

	It("Prevents deleting a Subnet when active BareMetalInstances reference it", func(ctx context.Context) {
		insertVN(ctx, "vn-1")
		insertSubnet(ctx, "subnet-1", "vn-1")
		insertBMI(ctx, "bmi-1", "subnet-1")

		err := softDelete(ctx, "subnets", "subnet-1")
		pgErr := expectPgErr(err, "Z0003")
		Expect(pgErr.Message).To(ContainSubstring("subnet-1"))
		Expect(pgErr.Message).To(ContainSubstring("bare metal instance"))
	})

	It("Allows deleting a Subnet when BMIs referencing it are soft-deleted", func(ctx context.Context) {
		insertVN(ctx, "vn-2")
		insertSubnet(ctx, "subnet-2", "vn-2")
		insertBMI(ctx, "bmi-2", "subnet-2")

		err := softDelete(ctx, "bare_metal_instances", "bmi-2")
		Expect(err).ToNot(HaveOccurred())

		err = softDelete(ctx, "subnets", "subnet-2")
		Expect(err).ToNot(HaveOccurred())
	})

	// -- 2. BMI subnet ref insert validation --

	It("Rejects creating a BMI with a non-existent Subnet ref", func(ctx context.Context) {
		_, err := conn.Exec(ctx,
			`insert into bare_metal_instances (id, name, tenant, data) values ('bmi-bad', 'bmi-bad', 'system', $1::jsonb)`,
			`{"spec":{"network_attachments":[{"subnet":{"id":"no-such-subnet"}}]}}`)
		pgErr := expectPgErr(err, "Z0002")
		Expect(pgErr.Message).To(ContainSubstring("no-such-subnet"))
	})

	It("Allows creating a BMI with a valid Subnet ref", func(ctx context.Context) {
		insertVN(ctx, "vn-10")
		insertSubnet(ctx, "subnet-10", "vn-10")
		insertBMI(ctx, "bmi-10", "subnet-10")
	})

	It("Allows creating a BMI with no network_attachments", func(ctx context.Context) {
		insertBMI(ctx, "bmi-11", "")
	})

	// -- 3. EIPA target ref insert validation --

	It("Rejects creating an EIPA targeting a non-existent BMI", func(ctx context.Context) {
		insertEIP(ctx, "eip-20")
		_, err := conn.Exec(ctx,
			`insert into external_ip_attachments (id, name, tenant, data) values ('eipa-bad-bmi', 'eipa-bad-bmi', 'system', $1::jsonb)`,
			`{"spec":{"external_ip":{"id":"eip-20"},"baremetal_instance":{"id":"no-such-bmi"}}}`)
		pgErr := expectPgErr(err, "Z0002")
		Expect(pgErr.Message).To(ContainSubstring("no-such-bmi"))
	})

	It("Rejects creating an EIPA targeting a non-existent CI", func(ctx context.Context) {
		insertEIP(ctx, "eip-21")
		_, err := conn.Exec(ctx,
			`insert into external_ip_attachments (id, name, tenant, data) values ('eipa-bad-ci', 'eipa-bad-ci', 'system', $1::jsonb)`,
			`{"spec":{"external_ip":{"id":"eip-21"},"compute_instance":{"id":"no-such-ci"}}}`)
		pgErr := expectPgErr(err, "Z0002")
		Expect(pgErr.Message).To(ContainSubstring("no-such-ci"))
	})

	It("Rejects creating an EIPA targeting a non-existent Cluster", func(ctx context.Context) {
		insertEIP(ctx, "eip-22")
		_, err := conn.Exec(ctx,
			`insert into external_ip_attachments (id, name, tenant, data) values ('eipa-bad-co', 'eipa-bad-co', 'system', $1::jsonb)`,
			`{"spec":{"external_ip":{"id":"eip-22"},"cluster":{"id":"no-such-cluster"}}}`)
		pgErr := expectPgErr(err, "Z0002")
		Expect(pgErr.Message).To(ContainSubstring("no-such-cluster"))
	})

	It("Allows creating an EIPA with a valid BMI target", func(ctx context.Context) {
		insertBMI(ctx, "bmi-20", "")
		insertEIP(ctx, "eip-30")
		insertEIPA(ctx, "eipa-30", "eip-30", "baremetal_instance", "bmi-20")
	})

	It("Allows creating an EIPA with a valid CI target", func(ctx context.Context) {
		insertCI(ctx, "ci-20")
		insertEIP(ctx, "eip-31")
		insertEIPA(ctx, "eipa-31", "eip-31", "compute_instance", "ci-20")
	})

	It("Allows creating an EIPA with a valid Cluster target", func(ctx context.Context) {
		insertCluster(ctx, "cluster-20")
		insertEIP(ctx, "eip-32")
		insertEIPA(ctx, "eipa-32", "eip-32", "cluster", "cluster-20")
	})
})

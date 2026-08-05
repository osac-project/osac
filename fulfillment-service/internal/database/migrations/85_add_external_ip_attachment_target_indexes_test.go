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

var _ = DescribeMigration("Add ExternalIPAttachment target indexes", func() {
	insertClusterAttachment := func(ctx context.Context, id, externalIP, cluster, endpoint string) error {
		_, err := conn.Exec(
			ctx,
			`insert into external_ip_attachments (id, tenant, data) values ($1, $2, $3)`,
			id, "system",
			`{"spec":{"external_ip":"`+externalIP+`","cluster":"`+cluster+`","target_endpoint":"`+endpoint+`"}}`,
		)
		return err
	}

	insertBMIAttachment := func(ctx context.Context, id, externalIP, bmi string) error {
		_, err := conn.Exec(
			ctx,
			`insert into external_ip_attachments (id, tenant, data) values ($1, $2, $3)`,
			id, "system",
			`{"spec":{"external_ip":"`+externalIP+`","baremetal_instance":"`+bmi+`"}}`,
		)
		return err
	}

	softDelete := func(ctx context.Context, id string) {
		_, err := conn.Exec(
			ctx,
			`update external_ip_attachments set deletion_timestamp = now() where id = $1`,
			id,
		)
		Expect(err).ToNot(HaveOccurred())
	}

	It("Rejects duplicate active cluster with same endpoint", func(ctx context.Context) {
		err := tool.Migrate(ctx, 85)
		Expect(err).ToNot(HaveOccurred())

		err = insertClusterAttachment(ctx, "a1", "eip-1", "cluster-1", "EXTERNAL_IP_ATTACHMENT_ENDPOINT_API")
		Expect(err).ToNot(HaveOccurred())

		err = insertClusterAttachment(ctx, "a2", "eip-2", "cluster-1", "EXTERNAL_IP_ATTACHMENT_ENDPOINT_API")
		Expect(err).To(HaveOccurred())
	})

	It("Allows same cluster with different endpoint", func(ctx context.Context) {
		err := tool.Migrate(ctx, 85)
		Expect(err).ToNot(HaveOccurred())

		err = insertClusterAttachment(ctx, "a1", "eip-1", "cluster-1", "EXTERNAL_IP_ATTACHMENT_ENDPOINT_API")
		Expect(err).ToNot(HaveOccurred())

		err = insertClusterAttachment(ctx, "a2", "eip-2", "cluster-1", "EXTERNAL_IP_ATTACHMENT_ENDPOINT_INGRESS")
		Expect(err).ToNot(HaveOccurred())
	})

	It("Allows same cluster and endpoint after soft delete", func(ctx context.Context) {
		err := tool.Migrate(ctx, 85)
		Expect(err).ToNot(HaveOccurred())

		err = insertClusterAttachment(ctx, "a1", "eip-1", "cluster-1", "EXTERNAL_IP_ATTACHMENT_ENDPOINT_API")
		Expect(err).ToNot(HaveOccurred())

		softDelete(ctx, "a1")

		err = insertClusterAttachment(ctx, "a2", "eip-2", "cluster-1", "EXTERNAL_IP_ATTACHMENT_ENDPOINT_API")
		Expect(err).ToNot(HaveOccurred())
	})

	It("Rejects duplicate active baremetal instance", func(ctx context.Context) {
		err := tool.Migrate(ctx, 85)
		Expect(err).ToNot(HaveOccurred())

		err = insertBMIAttachment(ctx, "a1", "eip-1", "bmi-1")
		Expect(err).ToNot(HaveOccurred())

		err = insertBMIAttachment(ctx, "a2", "eip-2", "bmi-1")
		Expect(err).To(HaveOccurred())
	})

	It("Allows same baremetal instance after soft delete", func(ctx context.Context) {
		err := tool.Migrate(ctx, 85)
		Expect(err).ToNot(HaveOccurred())

		err = insertBMIAttachment(ctx, "a1", "eip-1", "bmi-1")
		Expect(err).ToNot(HaveOccurred())

		softDelete(ctx, "a1")

		err = insertBMIAttachment(ctx, "a2", "eip-2", "bmi-1")
		Expect(err).ToNot(HaveOccurred())
	})

	It("Allows different clusters and baremetal instances", func(ctx context.Context) {
		err := tool.Migrate(ctx, 85)
		Expect(err).ToNot(HaveOccurred())

		err = insertClusterAttachment(ctx, "a1", "eip-1", "cluster-1", "EXTERNAL_IP_ATTACHMENT_ENDPOINT_API")
		Expect(err).ToNot(HaveOccurred())

		err = insertClusterAttachment(ctx, "a2", "eip-2", "cluster-2", "EXTERNAL_IP_ATTACHMENT_ENDPOINT_API")
		Expect(err).ToNot(HaveOccurred())

		err = insertBMIAttachment(ctx, "a3", "eip-3", "bmi-1")
		Expect(err).ToNot(HaveOccurred())

		err = insertBMIAttachment(ctx, "a4", "eip-4", "bmi-2")
		Expect(err).ToNot(HaveOccurred())
	})
})

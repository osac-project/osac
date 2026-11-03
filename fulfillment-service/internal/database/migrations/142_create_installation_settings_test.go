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

var _ = DescribeMigration("Create installation settings", func() {
	BeforeEach(func(ctx context.Context) {
		Expect(tool.Migrate(ctx, 142)).To(Succeed())
	})

	It("stores one row of settings", func(ctx context.Context) {
		_, err := conn.Exec(ctx, `
			insert into installation_settings (provisioning_enabled, enabled_services, release)
			values (true, '{vmaas,bmaas}', '0.2.0')`)
		Expect(err).ToNot(HaveOccurred())

		var enabled bool
		var services []string
		var release string
		err = conn.QueryRow(ctx,
			`select provisioning_enabled, enabled_services, release from installation_settings`,
		).Scan(&enabled, &services, &release)
		Expect(err).ToNot(HaveOccurred())
		Expect(enabled).To(BeTrue())
		Expect(services).To(Equal([]string{"vmaas", "bmaas"}))
		Expect(release).To(Equal("0.2.0"))
	})

	It("rejects a second row", func(ctx context.Context) {
		_, err := conn.Exec(ctx, `insert into installation_settings (provisioning_enabled) values (true)`)
		Expect(err).ToNot(HaveOccurred())
		_, err = conn.Exec(ctx, `insert into installation_settings (id, provisioning_enabled) values (2, false)`)
		Expect(err).To(HaveOccurred())
		_, err = conn.Exec(ctx, `insert into installation_settings (provisioning_enabled) values (false)`)
		Expect(err).To(HaveOccurred())
	})
})

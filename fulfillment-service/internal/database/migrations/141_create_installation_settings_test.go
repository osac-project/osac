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
	"sync"

	. "github.com/onsi/ginkgo/v2/dsl/core"
	. "github.com/onsi/gomega"

	"github.com/osac-project/osac/fulfillment-service/internal/networking"
)

var _ = DescribeMigration("Installation settings", func() {
	It("creates a unique, persistent boolean setting", func(ctx context.Context) {
		Expect(tool.Migrate(ctx, 141)).To(Succeed())
		_, err := conn.Exec(ctx, `insert into installation_settings (name, bool_value)
			values ('global.networking.provisioningEnabled', false)`)
		Expect(err).ToNot(HaveOccurred())
		_, err = conn.Exec(ctx, `insert into installation_settings (name, bool_value)
			values ('global.networking.provisioningEnabled', true)`)
		Expect(err).To(HaveOccurred())
		var saved bool
		Expect(conn.QueryRow(ctx, `select bool_value from installation_settings
			where name = 'global.networking.provisioningEnabled'`).Scan(&saved)).To(Succeed())
		Expect(saved).To(BeFalse())
	})

	It("keeps the first value under concurrent starts", func(ctx context.Context) {
		Expect(tool.Migrate(ctx, 141)).To(Succeed())
		pool, err := db.Pool(ctx)
		Expect(err).ToNot(HaveOccurred())
		DeferCleanup(pool.Close)
		results := make([]error, 20)
		var wg sync.WaitGroup
		for i := range results {
			wg.Add(1)
			go func(i int) {
				defer wg.Done()
				results[i] = networking.CheckInstallationProvisioning(ctx, pool, i%2 == 0)
			}(i)
		}
		wg.Wait()
		saved, err := networking.ReadInstalledProvisioning(ctx, pool)
		Expect(err).ToNot(HaveOccurred())
		for i, result := range results {
			if (i%2 == 0) == saved {
				Expect(result).To(Succeed())
			} else {
				Expect(result).To(MatchError(networking.ProvisioningSettingChangedMessage))
			}
		}
	})
})

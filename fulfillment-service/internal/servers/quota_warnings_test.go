/*
Copyright (c) 2026 Red Hat Inc.

Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except in
compliance with the License. You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software distributed under the License is
distributed on an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or
implied. See the License for the specific language governing permissions and limitations under the
License.
*/

package servers

import (
	"fmt"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/osac-project/osac/fulfillment-service/internal/database"
)

var _ = Describe("Quota warnings", func() {
	const dimension = "storage.slow.gib"

	// seedTightenedProject seeds the world a new default can tighten: the project team with
	// its own limit of twenty of the dimension, a threshold of eighty percent, and a volume of
	// sixteen above that threshold. No default covers the dimension, so the project's own
	// limit is the one that binds it.
	seedTightenedProject := func() {
		tx, err := database.TxFromContext(ctx)
		Expect(err).ToNot(HaveOccurred())
		_, err = tx.Exec(ctx,
			`insert into projects (id, tenant, project, name, creator, data)
			values (uuidv7(), $1, ''::ltree, 'team'::ltree, 'system', '{}')`, testTenant)
		Expect(err).ToNot(HaveOccurred())
		insertQuotaLimit(testTenant, "team", dimension, 20)
		insertRow("quota_thresholds", "threshold-team", testTenant, "team",
			`{"spec":{"dimension":"`+dimension+`","percent":80}}`)
		insertRow("volumes", "vol-team", testTenant, "team",
			`{"spec":{"storage_tier":"slow","size_gib":16}}`)
	}

	// setDefault writes the default limit of the dimension, the way a provider seeds one.
	setDefault := func(value int64) {
		tx, err := database.TxFromContext(ctx)
		Expect(err).ToNot(HaveOccurred())
		_, err = tx.Exec(ctx,
			`insert into quota_default_limits (id, name, creator, data)
			values ($1, $2, 'system', jsonb_build_object('spec', jsonb_build_object(
			'dimension', $3::text, 'value', $4::text)))`,
			"default-"+dimension, quotaNameFromDimension(dimension), dimension,
			fmt.Sprint(value))
		Expect(err).ToNot(HaveOccurred())
	}

	outbox := func() []map[string]any {
		tx, err := database.TxFromContext(ctx)
		Expect(err).ToNot(HaveOccurred())
		rows, err := tx.Query(ctx,
			`select tenant, project::text, data from quota_outbox order by created_at, id`)
		Expect(err).ToNot(HaveOccurred())
		defer rows.Close()
		var result []map[string]any
		for rows.Next() {
			var tenant, project, data string
			Expect(rows.Scan(&tenant, &project, &data)).To(Succeed())
			result = append(result, map[string]any{
				"tenant": tenant, "project": project, "data": data,
			})
		}
		Expect(rows.Err()).To(Succeed())
		return result
	}

	It("warns the project a new default tightens below its own limit", func() {
		seedTightenedProject()
		// The default arrives where none existed, and its ten is tighter than the
		// project's own twenty, so the usage of sixteen crosses the eighty percent:
		setDefault(10)
		enforcer, err := newQuotaEnforcer(logger, nil)
		Expect(err).ToNot(HaveOccurred())
		Expect(enforcer.warnTenantAfterDefaultChange(ctx, testTenant, dimension, 0)).To(Succeed())

		rows := outbox()
		Expect(rows).To(HaveLen(1))
		Expect(rows[0]["tenant"]).To(Equal(testTenant))
		Expect(rows[0]["project"]).To(Equal("team"))
		Expect(rows[0]["data"]).To(MatchJSON(
			`{"dimension":"` + dimension + `","percent":80,"usage":16,"limit":10}`))
	})

	It("writes no warning when the new default is looser than the project's own limit", func() {
		seedTightenedProject()
		// The default's twenty-five leaves the project's own twenty binding it, exactly as
		// before the change:
		setDefault(25)
		enforcer, err := newQuotaEnforcer(logger, nil)
		Expect(err).ToNot(HaveOccurred())
		Expect(enforcer.warnTenantAfterDefaultChange(ctx, testTenant, dimension, 0)).To(Succeed())
		Expect(outbox()).To(BeEmpty())
	})

	It("writes no warning for a tenant whose override keeps its limit", func() {
		// The tenant's own override replaces the default, so a smaller default moves
		// nothing the tenant is held to. The tenant-scoped threshold and the usage above it
		// would warn if the default applied:
		insertQuotaLimit(testTenant, "", dimension, 30)
		insertRow("quota_thresholds", "threshold-tenant", testTenant, "",
			`{"spec":{"dimension":"`+dimension+`","percent":80}}`)
		insertRow("volumes", "vol-tenant", testTenant, "",
			`{"spec":{"storage_tier":"slow","size_gib":16}}`)
		setDefault(10)
		enforcer, err := newQuotaEnforcer(logger, nil)
		Expect(err).ToNot(HaveOccurred())
		Expect(enforcer.warnTenantAfterDefaultChange(ctx, testTenant, dimension, 0)).To(Succeed())
		Expect(outbox()).To(BeEmpty())
	})

	It("warns when a lowered default tightens a scope it already bound", func() {
		seedTightenedProject()
		// The default of ten binds the project, below its own twenty, and the change lowers
		// it to five:
		setDefault(5)
		enforcer, err := newQuotaEnforcer(logger, nil)
		Expect(err).ToNot(HaveOccurred())
		Expect(enforcer.warnTenantAfterDefaultChange(ctx, testTenant, dimension, 10)).To(Succeed())

		rows := outbox()
		Expect(rows).To(HaveLen(1))
		Expect(rows[0]["data"]).To(MatchJSON(
			`{"dimension":"` + dimension + `","percent":80,"usage":16,"limit":5}`))
	})
})

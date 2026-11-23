// Copyright (c) 2026 Red Hat Inc.
// SPDX-License-Identifier: Apache-2.0

package fixtures

import (
	"context"
	"log/slog"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	fulfillmentit "github.com/osac-project/osac/fulfillment-service/it"
	privatev1 "github.com/osac-project/osac/proto/gen/osac/private/v1"
)

// Tenant waits for the normal user's tenant and default networking before workload creation.
func Tenant(ctx context.Context, tool *fulfillmentit.Tool) string {
	GinkgoHelper()

	const id = "users"

	client := privatev1.NewTenantsClient(tool.InternalView().AdminConn())

	waitCtx, cancel := context.WithTimeout(ctx, 10*time.Minute)
	defer cancel()

	Eventually(func(g Gomega) {
		requestCtx, cancel := context.WithTimeout(waitCtx, 30*time.Second)
		defer cancel()

		response, err := client.Get(requestCtx, privatev1.TenantsGetRequest_builder{Id: id}.Build())
		g.Expect(err).NotTo(HaveOccurred())

		tenant := response.GetObject()
		slog.DebugContext(ctx, "observed setup tenant state", "tenant", id, "state", tenant.GetStatus().GetState().String())
		g.Expect(tenant.GetStatus().GetState()).To(Equal(privatev1.TenantState_TENANT_STATE_SYNCED))

		ready := false

		for _, condition := range tenant.GetStatus().GetConditions() {
			if condition.GetType() == privatev1.TenantConditionType_TENANT_CONDITION_TYPE_DEFAULT_NETWORKING_READY &&
				condition.GetStatus() == privatev1.ConditionStatus_CONDITION_STATUS_TRUE {
				ready = true
			}
		}

		slog.DebugContext(ctx, "observed tenant default networking", "tenant", id, "ready", ready)
		g.Expect(ready).To(BeTrue(), "tenant %s networking must finish before workload creation", id)
	}).WithContext(waitCtx).WithPolling(2 * time.Second).Should(Succeed())
	slog.InfoContext(ctx, "setup tenant and default networking ready", "tenant", id)

	return id
}

/*
Copyright (c) 2026 Red Hat Inc.

Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except in compliance with the
License. You may obtain a copy of the License at

  http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on an
"AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the specific
language governing permissions and limitations under the License.
*/

package dao

import (
	"context"
	"errors"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"go.uber.org/mock/gomock"

	privatev1 "github.com/osac-project/fulfillment-service/internal/api/osac/private/v1"
	"github.com/osac-project/fulfillment-service/internal/auth"
	"github.com/osac-project/fulfillment-service/internal/database"
)

var _ = Describe("Referential integrity", func() {
	var (
		ctx     context.Context
		ctrl    *gomock.Controller
		tenancy *auth.MockTenancyLogic
		generic *GenericDAO[*privatev1.Cluster]
	)

	BeforeEach(func() {
		var err error

		// Create a context:
		ctx = context.Background()

		// Create the mock controller:
		ctrl = gomock.NewController(GinkgoT())
		DeferCleanup(ctrl.Finish)

		// Prepare the database pool (all migrations are applied, so the clusters and tenants
		// tables exist with the tenant FK constraint already in place):
		db, err := server.NewInstance().Build()
		Expect(err).ToNot(HaveOccurred())
		DeferCleanup(db.Close)
		pool, err := db.Pool(ctx)
		Expect(err).ToNot(HaveOccurred())
		DeferCleanup(pool.Close)

		// Create the transaction manager:
		tm, err := database.NewTxManager().
			SetLogger(logger).
			SetPool(pool).
			Build()
		Expect(err).ToNot(HaveOccurred())

		// Start a transaction and add it to the context:
		tx, err := tm.Begin(ctx)
		Expect(err).ToNot(HaveOccurred())
		DeferCleanup(func() {
			err := tx.End(ctx)
			Expect(err).ToNot(HaveOccurred())
		})
		ctx = database.TxIntoContext(ctx, tx)

		// Create a tenancy logic without restrictions:
		tenancy = auth.NewMockTenancyLogic(ctrl)
		tenancy.EXPECT().DetermineVisibleTenants(gomock.Any()).
			Return(auth.AllTenants, nil).
			AnyTimes()

		// Create the DAO:
		generic, err = NewGenericDAO[*privatev1.Cluster]().
			SetLogger(logger).
			SetTenancyLogic(tenancy).
			Build()
		Expect(err).ToNot(HaveOccurred())
	})

	It("Returns 'reference' error when creating object with non-existent tenant", func() {
		_, err := generic.Create().
			SetObject(&privatev1.Cluster{
				Metadata: privatev1.Metadata_builder{
					Tenant: "my-tenant",
				}.Build(),
			}).
			Do(ctx)
		Expect(err).To(HaveOccurred())
		var referenceErr *ErrReference
		Expect(errors.As(err, &referenceErr)).To(BeTrue())
		Expect(referenceErr.Reason).To(Equal("tenant 'my-tenant' doesn't exist"))
	})
})

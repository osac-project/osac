/*
Copyright (c) 2025 Red Hat Inc.

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

var _ = Describe("Immutable fields", func() {
	var (
		ctx     context.Context
		ctrl    *gomock.Controller
		tenancy *auth.MockTenancyLogic
		tx      database.Tx
		generic *GenericDAO[*privatev1.Tenant]
	)

	BeforeEach(func() {
		var err error

		// Create a context:
		ctx = context.Background()

		// Create the mock controller:
		ctrl = gomock.NewController(GinkgoT())
		DeferCleanup(ctrl.Finish)

		// Prepare the database pool:
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
		tx, err = tm.Begin(ctx)
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
		generic, err = NewGenericDAO[*privatev1.Tenant]().
			SetLogger(logger).
			SetTableName("tenants").
			SetTenancyLogic(tenancy).
			Build()
		Expect(err).ToNot(HaveOccurred())

		// Create a test object:
		_, err = generic.Create().
			SetObject(privatev1.Tenant_builder{
				Id: "my-tenant",
				Metadata: privatev1.Metadata_builder{
					Name:   "my-tenant",
					Tenant: "my-tenant",
				}.Build(),
			}.Build()).
			Do(ctx)
		Expect(err).ToNot(HaveOccurred())
	})

	It("Rejects update that changes one immutable field", func() {
		_, err := generic.Update().
			SetObject(privatev1.Tenant_builder{
				Id: "my-tenant",
				Metadata: privatev1.Metadata_builder{
					Name:   "your-name",
					Tenant: "my-tenant",
				}.Build(),
			}.Build()).
			Do(ctx)
		Expect(err).To(HaveOccurred())
		errImmutable, ok := errors.AsType[*ErrImmutable](err)
		Expect(ok).To(BeTrue())
		Expect(errImmutable.Fields).To(ConsistOf(
			"metadata.name",
		))
	})

	It("Rejects update that changes two immutable field", func() {
		_, err := generic.Update().
			SetObject(privatev1.Tenant_builder{
				Id: "my-tenant",
				Metadata: privatev1.Metadata_builder{
					Name:   "your-name",
					Tenant: "your-tenant",
				}.Build(),
			}.Build()).
			Do(ctx)
		Expect(err).To(HaveOccurred())
		errImmutable, ok := errors.AsType[*ErrImmutable](err)
		Expect(ok).To(BeTrue())
		Expect(errImmutable.Fields).To(ConsistOf(
			"metadata.name",
			"metadata.tenant",
		))
	})

	It("Allows update that includes but doesn't change an immutable field", func() {
		_, err := generic.Update().
			SetObject(privatev1.Tenant_builder{
				Id: "my-tenant",
				Metadata: privatev1.Metadata_builder{
					Name:   "my-tenant",
					Tenant: "my-tenant",
				}.Build(),
			}.Build()).
			Do(ctx)
		Expect(err).ToNot(HaveOccurred())
	})

	It("Allows update of other fields", func() {
		_, err := generic.Update().
			SetObject(privatev1.Tenant_builder{
				Id: "my-tenant",
				Metadata: privatev1.Metadata_builder{
					Name:   "my-tenant",
					Tenant: "my-tenant",
					Labels: map[string]string{
						"my-label": "my-value",
					},
				}.Build(),
			}.Build()).
			Do(ctx)
		Expect(err).ToNot(HaveOccurred())
	})
})

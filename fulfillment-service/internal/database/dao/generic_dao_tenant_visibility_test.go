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
	"fmt"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"go.uber.org/mock/gomock"

	testsv1 "github.com/osac-project/fulfillment-service/internal/api/osac/tests/v1"
	"github.com/osac-project/fulfillment-service/internal/auth"
	"github.com/osac-project/fulfillment-service/internal/collections"
	"github.com/osac-project/fulfillment-service/internal/database"
)

var _ = Describe("Tenant visibility", func() {
	var (
		ctx  context.Context
		tx   database.Tx
		ctrl *gomock.Controller
	)

	BeforeEach(func() {
		var err error

		// Create a context:
		ctx = context.Background()

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

		// Create the tenants used in the tests:
		createTenant := func(name string) {
			_, err = pool.Exec(ctx,
				`
				insert into tenants (
					id,
					tenant,
					name,
					data
				)
				values (
					$1,
					$2,
					$3,
					'{}'
				)
				`,
				name, name, name,
			)
			Expect(err).ToNot(HaveOccurred())
		}
		createTenant("tenant-a")
		createTenant("tenant-b")
		createTenant("tenant-c")
		createTenant("tenant-x")
		createTenant("tenant-y")

		// Create the mock controller:
		ctrl = gomock.NewController(GinkgoT())
		DeferCleanup(ctrl.Finish)
	})

	It("Filters field based on user visibility", func() {
		// Create a tenancy logic that makes certain tenants visible to the user:
		tenancy := auth.NewMockTenancyLogic(ctrl)
		tenancy.EXPECT().DetermineVisibleTenants(gomock.Any()).
			Return(collections.NewSet("tenant-a", "tenant-c"), nil).
			AnyTimes()

		// Create the DAO:
		dao, err := NewGenericDAO[*testsv1.Object]().
			SetLogger(logger).
			SetTenancyLogic(tenancy).
			Build()
		Expect(err).ToNot(HaveOccurred())

		// Create an object with a visible tenant, verify the response shows it:
		createResponse, err := dao.Create().
			SetObject(testsv1.Object_builder{
				Metadata: testsv1.Metadata_builder{
					Tenant: "tenant-a",
				}.Build(),
			}.Build()).
			Do(ctx)
		Expect(err).ToNot(HaveOccurred())
		object := createResponse.GetObject()
		Expect(object.GetMetadata().GetTenant()).To(Equal("tenant-a"))

		// Retrieve the object by identifier and verify it still shows the tenant:
		getResponse, err := dao.Get().
			SetId(object.GetId()).
			Do(ctx)
		Expect(err).ToNot(HaveOccurred())
		object = getResponse.GetObject()
		Expect(object.GetMetadata().GetTenant()).To(Equal("tenant-a"))

		// Retrieve the object as part of a list and verify it still shows the tenant:
		listResponse, err := dao.List().
			SetFilter(fmt.Sprintf("this.id == %q", object.GetId())).
			SetLimit(1).
			Do(ctx)
		Expect(err).ToNot(HaveOccurred())
		Expect(listResponse.GetItems()).To(HaveLen(1))
		Expect(listResponse.GetItems()[0].GetMetadata().GetTenant()).To(Equal("tenant-a"))

		// Update the object and verify the response still shows the tenant:
		object.SetMyString("hello")
		object.GetMetadata().SetTenant("tenant-a")
		updateResponse, err := dao.Update().
			SetObject(object).
			Do(ctx)
		Expect(err).ToNot(HaveOccurred())
		object = updateResponse.GetObject()
		Expect(object.GetMetadata().GetTenant()).To(Equal("tenant-a"))

		// Verify the actual database contains the tenant:
		var tenant string
		row := tx.QueryRow(ctx, "select tenant from objects where id = $1", object.GetId())
		err = row.Scan(&tenant)
		Expect(err).ToNot(HaveOccurred())
		Expect(tenant).To(Equal("tenant-a"))
	})

	It("Shows all tenants when user has no tenant restrictions", func() {
		// Create a tenancy logic that makes all tenants visible to the user:
		tenancy := auth.NewMockTenancyLogic(ctrl)
		tenancy.EXPECT().DetermineVisibleTenants(gomock.Any()).
			Return(auth.AllTenants, nil).
			AnyTimes()

		// Create the DAO:
		dao, err := NewGenericDAO[*testsv1.Object]().
			SetLogger(logger).
			SetTenancyLogic(tenancy).
			Build()
		Expect(err).ToNot(HaveOccurred())

		// Create an object and verify that it shows the tenant:
		createResponse, err := dao.Create().
			SetObject(testsv1.Object_builder{
				Metadata: testsv1.Metadata_builder{
					Tenant: "tenant-a",
				}.Build(),
			}.Build()).
			Do(ctx)
		Expect(err).ToNot(HaveOccurred())
		object := createResponse.GetObject()
		Expect(object.GetMetadata().GetTenant()).To(Equal("tenant-a"))
	})

	It("Shows no tenants when user has no visible tenants that intersect with object tenants", func() {
		// Create a tenancy logic that makes only one tenant visible to the user:
		tenancy := auth.NewMockTenancyLogic(ctrl)
		tenancy.EXPECT().DetermineVisibleTenants(gomock.Any()).
			Return(collections.NewSet("tenant-x"), nil).
			AnyTimes()

		// Create the DAO:
		dao, err := NewGenericDAO[*testsv1.Object]().
			SetLogger(logger).
			SetTenancyLogic(tenancy).
			Build()
		Expect(err).ToNot(HaveOccurred())

		// Create an object with a tenant that doesn't overlap with visible tenants:
		createResponse, err := dao.Create().
			SetObject(testsv1.Object_builder{
				Metadata: testsv1.Metadata_builder{
					Tenant: "tenant-y",
				}.Build(),
			}.Build()).
			Do(ctx)
		Expect(err).ToNot(HaveOccurred())
		object := createResponse.GetObject()
		Expect(object.GetMetadata().GetTenant()).To(BeEmpty())

		// Verify the object is not found via Get because the SQL tenant filter excludes it:
		_, err = dao.Get().
			SetId(object.GetId()).
			Do(ctx)
		var notFoundErr *ErrNotFound
		Expect(err).To(HaveOccurred())
		Expect(err).To(BeAssignableToTypeOf(notFoundErr))

		// Verify the object is not returned via List either:
		listResponse, err := dao.List().
			Do(ctx)
		Expect(err).ToNot(HaveOccurred())
		Expect(listResponse.GetItems()).To(BeEmpty())
	})

	It("Allows a tenant to delete an object it created", func() {
		// Create a DAO with visibility for tenant A:
		tenancy := auth.NewMockTenancyLogic(ctrl)
		tenancy.EXPECT().DetermineVisibleTenants(gomock.Any()).
			Return(collections.NewSet("tenant-a"), nil).
			AnyTimes()
		dao, err := NewGenericDAO[*testsv1.Object]().
			SetLogger(logger).
			SetTenancyLogic(tenancy).
			Build()
		Expect(err).ToNot(HaveOccurred())

		// Create the object:
		createResponse, err := dao.Create().
			SetObject(testsv1.Object_builder{
				Metadata: testsv1.Metadata_builder{
					Tenant: "tenant-a",
				}.Build(),
			}.Build()).
			Do(ctx)
		Expect(err).ToNot(HaveOccurred())
		object := createResponse.GetObject()

		// Verify that the object can be deleted:
		_, err = dao.Delete().
			SetId(object.GetId()).
			Do(ctx)
		Expect(err).ToNot(HaveOccurred())

		// Verify that the object is no longer retrievable:
		_, err = dao.Get().
			SetId(object.GetId()).
			Do(ctx)
		var notFoundErr *ErrNotFound
		Expect(err).To(HaveOccurred())
		Expect(err).To(BeAssignableToTypeOf(notFoundErr))
	})

	It("Rejects deletion of an object belonging to an invisible tenant as not found", func() {
		// Create the DAO with visibility for tenant A and insert the object:
		tenancyA := auth.NewMockTenancyLogic(ctrl)
		tenancyA.EXPECT().DetermineVisibleTenants(gomock.Any()).
			Return(collections.NewSet("tenant-a"), nil).
			AnyTimes()
		daoA, err := NewGenericDAO[*testsv1.Object]().
			SetLogger(logger).
			SetTenancyLogic(tenancyA).
			Build()
		Expect(err).ToNot(HaveOccurred())

		// Create the object with tenant A:
		createResponse, err := daoA.Create().
			SetObject(testsv1.Object_builder{
				Metadata: testsv1.Metadata_builder{
					Tenant: "tenant-a",
				}.Build(),
			}.Build()).
			Do(ctx)
		Expect(err).ToNot(HaveOccurred())
		object := createResponse.GetObject()

		// Create a DAO with visibility for tenant B and verify that it can't delete the object of
		// tenant A:
		tenancyB := auth.NewMockTenancyLogic(ctrl)
		tenancyB.EXPECT().DetermineVisibleTenants(gomock.Any()).
			Return(collections.NewSet("tenant-b"), nil).
			AnyTimes()
		daoB, err := NewGenericDAO[*testsv1.Object]().
			SetLogger(logger).
			SetTenancyLogic(tenancyB).
			Build()
		Expect(err).ToNot(HaveOccurred())
		_, err = daoB.Delete().
			SetId(object.GetId()).
			Do(ctx)
		var notFoundErr *ErrNotFound
		Expect(err).To(HaveOccurred())
		Expect(err).To(BeAssignableToTypeOf(notFoundErr))

		// Verify that the object still exists using the DAO for tenant A:
		getResponse, err := daoA.Get().
			SetId(object.GetId()).
			Do(ctx)
		Expect(err).ToNot(HaveOccurred())
		Expect(getResponse.GetObject().GetId()).To(Equal(object.GetId()))
	})

	It("Allows a tenant to update an object it created", func() {
		// Create a DAO with visibility for tenant A:
		tenancy := auth.NewMockTenancyLogic(ctrl)
		tenancy.EXPECT().DetermineVisibleTenants(gomock.Any()).
			Return(collections.NewSet("tenant-a"), nil).
			AnyTimes()
		dao, err := NewGenericDAO[*testsv1.Object]().
			SetLogger(logger).
			SetTenancyLogic(tenancy).
			Build()
		Expect(err).ToNot(HaveOccurred())

		// Create the object:
		createResponse, err := dao.Create().
			SetObject(testsv1.Object_builder{
				Metadata: testsv1.Metadata_builder{
					Tenant: "tenant-a",
				}.Build(),
			}.Build()).
			Do(ctx)
		Expect(err).ToNot(HaveOccurred())
		object := createResponse.GetObject()

		// Verify that the object can be updated:
		object.SetMyString("updated")
		updateResponse, err := dao.Update().
			SetObject(object).
			Do(ctx)
		Expect(err).ToNot(HaveOccurred())
		Expect(updateResponse.GetObject().GetMyString()).To(Equal("updated"))

		// Retrieve the object and verify the update persisted:
		getResponse, err := dao.Get().
			SetId(object.GetId()).
			Do(ctx)
		Expect(err).ToNot(HaveOccurred())
		Expect(getResponse.GetObject().GetMyString()).To(Equal("updated"))
	})

	It("Rejects update of an object belonging to an invisible tenant as not found", func() {
		// Create a tenancy logic that makes all tenants visible, used to create the object:
		tenancyA := auth.NewMockTenancyLogic(ctrl)
		tenancyA.EXPECT().DetermineVisibleTenants(gomock.Any()).
			Return(collections.NewSet("tenant-a"), nil).
			AnyTimes()

		// Create the DAO for tenant A and insert the object:
		daoA, err := NewGenericDAO[*testsv1.Object]().
			SetLogger(logger).
			SetTenancyLogic(tenancyA).
			Build()
		Expect(err).ToNot(HaveOccurred())
		createResponse, err := daoA.Create().
			SetObject(testsv1.Object_builder{
				Metadata: testsv1.Metadata_builder{
					Tenant: "tenant-a",
				}.Build(),
			}.Build()).
			Do(ctx)
		Expect(err).ToNot(HaveOccurred())
		object := createResponse.GetObject()

		// Create a tenancy logic that only sees tenant B and verify that it can't update the object of
		// tenant A:
		tenancyB := auth.NewMockTenancyLogic(ctrl)
		tenancyB.EXPECT().DetermineVisibleTenants(gomock.Any()).
			Return(collections.NewSet("tenant-b"), nil).
			AnyTimes()
		daoB, err := NewGenericDAO[*testsv1.Object]().
			SetLogger(logger).
			SetTenancyLogic(tenancyB).
			Build()
		Expect(err).ToNot(HaveOccurred())
		object.SetMyString("updated")
		_, err = daoB.Update().
			SetObject(object).
			Do(ctx)
		var notFoundErr *ErrNotFound
		Expect(err).To(HaveOccurred())
		Expect(err).To(BeAssignableToTypeOf(notFoundErr))

		// Verify the object was not modified using the DAO for tenant A:
		getResponse, err := daoA.Get().
			SetId(object.GetId()).
			Do(ctx)
		Expect(err).ToNot(HaveOccurred())
		Expect(getResponse.GetObject().GetMyString()).To(BeEmpty())
	})
})

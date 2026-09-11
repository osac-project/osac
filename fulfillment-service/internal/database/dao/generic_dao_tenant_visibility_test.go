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

	"github.com/jackc/pgx/v5/pgxpool"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"go.uber.org/mock/gomock"

	"github.com/osac-project/osac/fulfillment-service/internal/auth"
	"github.com/osac-project/osac/fulfillment-service/internal/database"
	testsv1 "github.com/osac-project/osac/proto/gen/osac/tests/v1"
)

var _ = Describe("Generic DAO visibility", func() {
	var (
		ctx  context.Context
		tx   database.Tx
		ctrl *gomock.Controller
		pool *pgxpool.Pool
	)

	BeforeEach(func() {
		var err error

		// Create a context:
		ctx = context.Background()

		// Prepare the database pool:
		db, err := server.NewInstance().Build()
		Expect(err).ToNot(HaveOccurred())
		DeferCleanup(db.Close)
		pool, err = db.Pool(ctx)
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

	Describe("Tenant visibility", func() {
		It("Filters based on user visibility", func() {
			// Create a tenancy logic that makes certain tenants visible to the user:
			visibility, err := auth.NewVisibility().
				AddVisibleTenants("tenant-a", "tenant-c").
				Build()
			Expect(err).ToNot(HaveOccurred())
			tenancy := auth.NewMockTenancyLogic(ctrl)
			tenancy.EXPECT().DetermineVisibility(gomock.Any()).
				Return(visibility, nil).
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
			visibility, err := auth.NewVisibility().
				SetTotal(true).
				Build()
			Expect(err).ToNot(HaveOccurred())
			tenancy := auth.NewMockTenancyLogic(ctrl)
			tenancy.EXPECT().DetermineVisibility(gomock.Any()).
				Return(visibility, nil).
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
			visibility, err := auth.NewVisibility().
				AddVisibleTenants("tenant-x").
				Build()
			Expect(err).ToNot(HaveOccurred())
			tenancy := auth.NewMockTenancyLogic(ctrl)
			tenancy.EXPECT().DetermineVisibility(gomock.Any()).
				Return(visibility, nil).
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
			// Create a DAO with visibility of all projects for tenant A:
			visibility, err := auth.NewVisibility().
				AddVisibleTenants("tenant-a").
				Build()
			Expect(err).ToNot(HaveOccurred())
			tenancy := auth.NewMockTenancyLogic(ctrl)
			tenancy.EXPECT().DetermineVisibility(gomock.Any()).
				Return(visibility, nil).
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
			// Create the DAO with visibility for all projects for tenant A and insert the object:
			visibilityA, err := auth.NewVisibility().
				AddVisibleTenants("tenant-a").
				Build()
			Expect(err).ToNot(HaveOccurred())
			tenancyA := auth.NewMockTenancyLogic(ctrl)
			tenancyA.EXPECT().DetermineVisibility(gomock.Any()).
				Return(visibilityA, nil).
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
			visibilityB, err := auth.NewVisibility().
				AddVisibleTenants("tenant-b").
				Build()
			Expect(err).ToNot(HaveOccurred())
			tenancyB := auth.NewMockTenancyLogic(ctrl)
			tenancyB.EXPECT().DetermineVisibility(gomock.Any()).
				Return(visibilityB, nil).
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
			visibility, err := auth.NewVisibility().
				AddVisibleTenants("tenant-a").
				Build()
			Expect(err).ToNot(HaveOccurred())
			tenancy := auth.NewMockTenancyLogic(ctrl)
			tenancy.EXPECT().DetermineVisibility(gomock.Any()).
				Return(visibility, nil).
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

		It("Isolates tenant rows when filter has a top-level OR", func() {
			// Create an unrestricted tenancy to seed objects in different tenants:
			tenancyAll := auth.NewMockTenancyLogic(ctrl)
			tenancyAll.EXPECT().DetermineVisibility(gomock.Any()).
				Return(auth.TotalVisibility(), nil).
				AnyTimes()
			daoAll, err := NewGenericDAO[*testsv1.Object]().
				SetLogger(logger).
				SetTenancyLogic(tenancyAll).
				Build()
			Expect(err).ToNot(HaveOccurred())

			// Create an object in tenant-a (will be visible):
			_, err = daoAll.Create().
				SetObject(testsv1.Object_builder{
					Metadata: testsv1.Metadata_builder{
						Tenant: "tenant-a",
						Name:   "shared-name",
					}.Build(),
				}.Build()).
				Do(ctx)
			Expect(err).ToNot(HaveOccurred())

			// Create an object in tenant-b with the same name (should be invisible to tenant-a):
			_, err = daoAll.Create().
				SetObject(testsv1.Object_builder{
					Metadata: testsv1.Metadata_builder{
						Tenant: "tenant-b",
						Name:   "shared-name",
					}.Build(),
				}.Build()).
				Do(ctx)
			Expect(err).ToNot(HaveOccurred())

			// Create a restricted DAO that can only see tenant-a:
			visibilityRestricted, err := auth.NewVisibility().
				AddVisibleTenants("tenant-a").
				Build()
			Expect(err).ToNot(HaveOccurred())
			tenancyRestricted := auth.NewMockTenancyLogic(ctrl)
			tenancyRestricted.EXPECT().DetermineVisibility(gomock.Any()).
				Return(visibilityRestricted, nil).
				AnyTimes()
			daoRestricted, err := NewGenericDAO[*testsv1.Object]().
				SetLogger(logger).
				SetTenancyLogic(tenancyRestricted).
				Build()
			Expect(err).ToNot(HaveOccurred())

			// List with a top-level OR filter (id-or-name pattern). The tenancy clause
			// must apply to the entire filter, not just the first branch:
			listResponse, err := daoRestricted.List().
				SetFilter(`this.id == "nonexistent" || this.metadata.name == "shared-name"`).
				Do(ctx)
			Expect(err).ToNot(HaveOccurred())
			Expect(listResponse.GetTotal()).To(Equal(int32(1)))
			Expect(listResponse.GetItems()).To(HaveLen(1))
			Expect(listResponse.GetItems()[0].GetMetadata().GetTenant()).To(Equal("tenant-a"))
		})

		It("Rejects update of an object belonging to an invisible tenant as not found", func() {
			// Create a tenancy logic that makes all tenants visible, used to create the object:
			visibilityA, err := auth.NewVisibility().
				AddVisibleTenants("tenant-a").
				Build()
			Expect(err).ToNot(HaveOccurred())
			tenancyA := auth.NewMockTenancyLogic(ctrl)
			tenancyA.EXPECT().DetermineVisibility(gomock.Any()).
				Return(visibilityA, nil).
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
			visibilityB, err := auth.NewVisibility().
				AddVisibleTenants("tenant-b").
				Build()
			Expect(err).ToNot(HaveOccurred())
			tenancyB := auth.NewMockTenancyLogic(ctrl)
			tenancyB.EXPECT().DetermineVisibility(gomock.Any()).
				Return(visibilityB, nil).
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

	Describe("Project visibility", func() {
		BeforeEach(func() {
			createProject := func(tenant, parent, name string) {
				_, err := pool.Exec(ctx,
					`
					insert into projects (
						id,
						tenant,
						project,
						name,
						data
					)
					values (
						$1,
						$2,
						$3,
						$4,
						'{}'
					)
					`,
					fmt.Sprintf("%s-%s", tenant, name), tenant, parent, name,
				)
				Expect(err).ToNot(HaveOccurred())
			}
			createProject("tenant-a", "", "p1")
			createProject("tenant-a", "", "p2")
			createProject("tenant-a", "p1", "p1.child")
			createProject("tenant-a", "p1.child", "p1.child.grandchild")
		})

		unrestrictedDAO := func() *GenericDAO[*testsv1.Object] {
			tenancy := auth.NewMockTenancyLogic(ctrl)
			tenancy.EXPECT().DetermineVisibility(gomock.Any()).
				Return(auth.TotalVisibility(), nil).
				AnyTimes()
			dao, err := NewGenericDAO[*testsv1.Object]().
				SetLogger(logger).
				SetTenancyLogic(tenancy).
				Build()
			Expect(err).ToNot(HaveOccurred())
			return dao
		}

		daoWithVisibility := func(visibility *auth.Visibility) *GenericDAO[*testsv1.Object] {
			tenancy := auth.NewMockTenancyLogic(ctrl)
			tenancy.EXPECT().DetermineVisibility(gomock.Any()).
				Return(visibility, nil).
				AnyTimes()
			dao, err := NewGenericDAO[*testsv1.Object]().
				SetLogger(logger).
				SetTenancyLogic(tenancy).
				Build()
			Expect(err).ToNot(HaveOccurred())
			return dao
		}

		createObject := func(dao *GenericDAO[*testsv1.Object], project, name string) string {
			response, err := dao.Create().
				SetObject(testsv1.Object_builder{
					Metadata: testsv1.Metadata_builder{
						Tenant:  "tenant-a",
						Project: project,
						Name:    name,
					}.Build(),
				}.Build()).
				Do(ctx)
			Expect(err).ToNot(HaveOccurred())
			return response.GetObject().GetId()
		}

		listedNames := func(dao *GenericDAO[*testsv1.Object]) []string {
			response, err := dao.List().Do(ctx)
			Expect(err).ToNot(HaveOccurred())
			names := make([]string, 0, len(response.GetItems()))
			for _, item := range response.GetItems() {
				names = append(names, item.GetMetadata().GetName())
			}
			return names
		}

		It("Includes the default project and excludes ungranted named projects", func() {
			seed := unrestrictedDAO()
			defaultID := createObject(seed, "", "default-obj")
			namedID := createObject(seed, "p1", "p1-obj")

			visibility, err := auth.NewVisibility().
				AddVisibleTenants("tenant-a").
				Build()
			Expect(err).ToNot(HaveOccurred())
			dao := daoWithVisibility(visibility)

			Expect(listedNames(dao)).To(ConsistOf("default-obj"))

			_, err = dao.Get().SetId(defaultID).Do(ctx)
			Expect(err).ToNot(HaveOccurred())

			_, err = dao.Get().SetId(namedID).Do(ctx)
			var notFoundErr *ErrNotFound
			Expect(err).To(HaveOccurred())
			Expect(err).To(BeAssignableToTypeOf(notFoundErr))

			existsResponse, err := dao.Exists().SetId(namedID).Do(ctx)
			Expect(err).ToNot(HaveOccurred())
			Expect(existsResponse.GetExists()).To(BeFalse())
		})

		It("Includes granted named projects and their descendants", func() {
			seed := unrestrictedDAO()
			createObject(seed, "", "default-obj")
			p1ID := createObject(seed, "p1", "p1-obj")
			childID := createObject(seed, "p1.child", "p1-child-obj")
			grandchildID := createObject(seed, "p1.child.grandchild", "p1-grandchild-obj")
			p2ID := createObject(seed, "p2", "p2-obj")

			visibility, err := auth.NewVisibility().
				AddVisibleProject("tenant-a", "p1").
				Build()
			Expect(err).ToNot(HaveOccurred())
			dao := daoWithVisibility(visibility)

			Expect(listedNames(dao)).To(ConsistOf("default-obj", "p1-obj", "p1-child-obj", "p1-grandchild-obj"))

			_, err = dao.Get().SetId(p1ID).Do(ctx)
			Expect(err).ToNot(HaveOccurred())

			_, err = dao.Get().SetId(childID).Do(ctx)
			Expect(err).ToNot(HaveOccurred())

			_, err = dao.Get().SetId(grandchildID).Do(ctx)
			Expect(err).ToNot(HaveOccurred())

			_, err = dao.Get().SetId(p2ID).Do(ctx)
			var notFoundErr *ErrNotFound
			Expect(err).To(HaveOccurred())
			Expect(err).To(BeAssignableToTypeOf(notFoundErr))

			existsResponse, err := dao.Exists().SetId(p1ID).Do(ctx)
			Expect(err).ToNot(HaveOccurred())
			Expect(existsResponse.GetExists()).To(BeTrue())

			existsResponse, err = dao.Exists().SetId(grandchildID).Do(ctx)
			Expect(err).ToNot(HaveOccurred())
			Expect(existsResponse.GetExists()).To(BeTrue())

			existsResponse, err = dao.Exists().SetId(p2ID).Do(ctx)
			Expect(err).ToNot(HaveOccurred())
			Expect(existsResponse.GetExists()).To(BeFalse())
		})

		It("Does not include ancestor projects of a granted descendant", func() {
			seed := unrestrictedDAO()
			createObject(seed, "", "default-obj")
			createObject(seed, "p1", "p1-obj")
			createObject(seed, "p1.child", "p1-child-obj")
			createObject(seed, "p2", "p2-obj")

			visibility, err := auth.NewVisibility().
				AddVisibleProject("tenant-a", "p1.child").
				Build()
			Expect(err).ToNot(HaveOccurred())
			dao := daoWithVisibility(visibility)

			Expect(listedNames(dao)).To(ConsistOf("default-obj", "p1-child-obj"))
		})
	})
})

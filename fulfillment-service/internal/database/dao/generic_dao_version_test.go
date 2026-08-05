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

var _ = Describe("Version", func() {
	var (
		ctx     context.Context
		ctrl    *gomock.Controller
		tenancy *auth.MockTenancyLogic
		tx      database.Tx
		generic *GenericDAO[*testsv1.Object]
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
			Return(collections.NewUniversalSet[string](), nil).
			AnyTimes()

		// Create the tenants used in the tests:
		createTenant := func(name string) {
			_, err = pool.Exec(ctx, `
				insert into tenants (
					id,
					name,
					tenant,
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
		createTenant("my-tenant")

		// Create the DAO:
		generic, err = NewGenericDAO[*testsv1.Object]().
			SetLogger(logger).
			SetTenancyLogic(tenancy).
			Build()
		Expect(err).ToNot(HaveOccurred())
	})

	// checkDatabaseVersion checks the version of the object in the database.
	checkDatabaseVersion := func(id string, expected int32) {
		row := tx.QueryRow(ctx, "select version from objects where id = $1", id)
		var actual int32
		err := row.Scan(&actual)
		Expect(err).ToNot(HaveOccurred())
		Expect(actual).To(Equal(expected))
	}

	It("Is zero on create", func() {
		response, err := generic.Create().
			SetObject(
				testsv1.Object_builder{
					Metadata: testsv1.Metadata_builder{
						Tenant: "my-tenant",
					}.Build(),
					MyString: "my_value",
				}.Build(),
			).
			Do(ctx)
		Expect(err).ToNot(HaveOccurred())
		object := response.GetObject()
		Expect(object.GetMetadata().GetVersion()).To(Equal(int32(0)))
		checkDatabaseVersion(object.GetId(), 0)
	})

	It("Is zero when retrieved after create", func() {
		createResponse, err := generic.Create().
			SetObject(
				testsv1.Object_builder{
					Metadata: testsv1.Metadata_builder{
						Tenant: "my-tenant",
					}.Build(),
					MyString: "my_value",
				}.Build(),
			).
			Do(ctx)
		Expect(err).ToNot(HaveOccurred())
		object := createResponse.GetObject()

		getResponse, err := generic.Get().
			SetId(object.GetId()).
			Do(ctx)
		Expect(err).ToNot(HaveOccurred())
		object = getResponse.GetObject()
		Expect(object.GetMetadata().GetVersion()).To(Equal(int32(0)))
	})

	It("Is zero when listed after create", func() {
		createResponse, err := generic.Create().
			SetObject(
				testsv1.Object_builder{
					Metadata: testsv1.Metadata_builder{
						Tenant: "my-tenant",
					}.Build(),
					MyString: "my_value",
				}.Build(),
			).
			Do(ctx)
		Expect(err).ToNot(HaveOccurred())
		id := createResponse.GetObject().GetId()

		listResponse, err := generic.List().
			SetFilter(fmt.Sprintf("this.id == '%s'", id)).
			Do(ctx)
		Expect(err).ToNot(HaveOccurred())
		items := listResponse.GetItems()
		Expect(items).To(HaveLen(1))
		Expect(items[0].GetMetadata().GetVersion()).To(Equal(int32(0)))
	})

	It("Increments on update", func() {
		createResponse, err := generic.Create().
			SetObject(
				testsv1.Object_builder{
					Metadata: testsv1.Metadata_builder{
						Tenant: "my-tenant",
					}.Build(),
					MyString: "my_value",
				}.Build(),
			).
			Do(ctx)
		Expect(err).ToNot(HaveOccurred())
		object := createResponse.GetObject()
		Expect(object.GetMetadata().GetVersion()).To(Equal(int32(0)))

		// First update:
		object.MyString = "your_value"
		updateResponse, err := generic.Update().
			SetObject(object).
			Do(ctx)
		Expect(err).ToNot(HaveOccurred())
		object = updateResponse.GetObject()
		Expect(object.GetMetadata().GetVersion()).To(Equal(int32(1)))
		checkDatabaseVersion(object.GetId(), 1)

		// Second update:
		object.MyString = "another_value"
		updateResponse, err = generic.Update().
			SetObject(object).
			Do(ctx)
		Expect(err).ToNot(HaveOccurred())
		object = updateResponse.GetObject()
		Expect(object.GetMetadata().GetVersion()).To(Equal(int32(2)))
		checkDatabaseVersion(object.GetId(), 2)
	})

	It("Increments on each distinct update", func() {
		createResponse, err := generic.Create().
			SetObject(
				testsv1.Object_builder{
					Metadata: testsv1.Metadata_builder{
						Tenant: "my-tenant",
					}.Build(),
					MyInt32: 0,
				}.Build(),
			).
			Do(ctx)
		Expect(err).ToNot(HaveOccurred())
		object := createResponse.GetObject()

		for i := int32(1); i <= 5; i++ {
			object.MyInt32 = i
			updateResponse, err := generic.Update().
				SetObject(object).
				Do(ctx)
			Expect(err).ToNot(HaveOccurred())
			object = updateResponse.GetObject()
			Expect(object.GetMetadata().GetVersion()).To(Equal(i))
		}
		checkDatabaseVersion(object.GetId(), 5)
	})

	It("Matches database after get", func() {
		createResponse, err := generic.Create().
			SetObject(
				testsv1.Object_builder{
					Metadata: testsv1.Metadata_builder{
						Tenant: "my-tenant",
					}.Build(),
					MyString: "my_value",
				}.Build(),
			).
			Do(ctx)
		Expect(err).ToNot(HaveOccurred())
		object := createResponse.GetObject()

		// Perform a couple of updates:
		object.MyString = "value_1"
		updateResponse, err := generic.Update().
			SetObject(object).
			Do(ctx)
		Expect(err).ToNot(HaveOccurred())
		object = updateResponse.GetObject()

		object.MyString = "value_2"
		updateResponse, err = generic.Update().
			SetObject(object).
			Do(ctx)
		Expect(err).ToNot(HaveOccurred())
		object = updateResponse.GetObject()

		// Get and verify the version matches:
		getResponse, err := generic.Get().
			SetId(object.GetId()).
			Do(ctx)
		Expect(err).ToNot(HaveOccurred())
		retrieved := getResponse.GetObject()
		Expect(retrieved.GetMetadata().GetVersion()).To(Equal(int32(2)))
		checkDatabaseVersion(retrieved.GetId(), 2)
	})

	It("Ignores user-provided version and increments correctly", func() {
		// Create an object (version 0):
		createResponse, err := generic.Create().
			SetObject(
				testsv1.Object_builder{
					Metadata: testsv1.Metadata_builder{
						Tenant: "my-tenant",
					}.Build(),
					MyString: "v0",
				}.Build(),
			).
			Do(ctx)
		Expect(err).ToNot(HaveOccurred())
		object := createResponse.GetObject()
		Expect(object.GetMetadata().GetVersion()).To(Equal(int32(0)))

		// Perform three updates to reach version 3:
		for i := int32(1); i <= 3; i++ {
			object.MyString = fmt.Sprintf("v%d", i)
			updateResponse, err := generic.Update().
				SetObject(object).
				Do(ctx)
			Expect(err).ToNot(HaveOccurred())
			object = updateResponse.GetObject()
		}
		Expect(object.GetMetadata().GetVersion()).To(Equal(int32(3)))

		// Now try to set the version to 2 while making a real change. The user-provided
		// version should be ignored and the database should increment to 4:
		object.GetMetadata().SetVersion(2)
		object.MyString = "v4"
		updateResponse, err := generic.Update().
			SetObject(object).
			Do(ctx)
		Expect(err).ToNot(HaveOccurred())
		object = updateResponse.GetObject()
		Expect(object.GetMetadata().GetVersion()).To(Equal(int32(4)))
		checkDatabaseVersion(object.GetId(), 4)
	})
})

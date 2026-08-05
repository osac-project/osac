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
	"github.com/osac-project/fulfillment-service/internal/database"
	. "github.com/osac-project/fulfillment-service/internal/testing"
)

var _ = Describe("Metrics", func() {
	var (
		ctx           context.Context
		ctrl          *gomock.Controller
		tenancy       *auth.MockTenancyLogic
		metricsServer *MetricsServer
		dao           *GenericDAO[*testsv1.Object]
	)

	BeforeEach(func() {
		var err error

		// Create a context:
		ctx = context.Background()

		// Create the mock controller:
		ctrl = gomock.NewController(GinkgoT())
		DeferCleanup(ctrl.Finish)

		// Prepare the database:
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

		// Create the metrics server:
		metricsServer = NewMetricsServer()
		DeferCleanup(metricsServer.Close)

		// Create a tenancy logic without restrictions:
		tenancy = auth.NewMockTenancyLogic(ctrl)
		tenancy.EXPECT().DetermineVisibleTenants(gomock.Any()).
			Return(auth.AllTenants, nil).
			AnyTimes()

		// Create the tenant used in the tests:
		createTenant := func(name string) {
			_, err = pool.Exec(ctx, `
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
		createTenant("my-tenant")

		// Create the DAO with metrics enabled:
		dao, err = NewGenericDAO[*testsv1.Object]().
			SetLogger(logger).
			SetTenancyLogic(tenancy).
			SetMetricsRegisterer(metricsServer.Registry()).
			Build()
		Expect(err).ToNot(HaveOccurred())
	})

	Describe("SQL duration", func() {
		It("Honours subsystem", func() {
			_, err := dao.Create().
				SetObject(
					testsv1.Object_builder{
						Metadata: testsv1.Metadata_builder{
							Tenant: "my-tenant",
							Name:   "my-object",
						}.Build(),
					}.Build(),
				).
				Do(ctx)
			Expect(err).ToNot(HaveOccurred())

			metrics := metricsServer.Metrics()
			Expect(metrics).To(MatchLine(`^db_operation_duration_bucket\{.*\} .*$`))
			Expect(metrics).To(MatchLine(`^db_operation_duration_sum\{.*\} .*$`))
			Expect(metrics).To(MatchLine(`^db_operation_duration_count\{.*\} .*$`))
		})

		It("Includes table label", func() {
			_, err := dao.Create().
				SetObject(
					testsv1.Object_builder{
						Metadata: testsv1.Metadata_builder{
							Tenant: "my-tenant",
							Name:   "my-object",
						}.Build(),
					}.Build(),
				).
				Do(ctx)
			Expect(err).ToNot(HaveOccurred())

			metrics := metricsServer.Metrics()
			Expect(metrics).To(MatchLine(
				`^\w+_operation_duration_count\{.*table="objects".*\} .*$`,
			))
		})

		It("Includes empty error label for successful operations", func() {
			_, err := dao.Create().
				SetObject(
					testsv1.Object_builder{
						Metadata: testsv1.Metadata_builder{
							Tenant: "my-tenant",
							Name:   "my-object",
						}.Build(),
					}.Build(),
				).
				Do(ctx)
			Expect(err).ToNot(HaveOccurred())

			metrics := metricsServer.Metrics()
			Expect(metrics).To(MatchLine(
				`^\w+_operation_duration_count\{.*error="".*\} .*$`,
			))
		})

		It("Includes error code label for failed operations", func() {
			object := testsv1.Object_builder{
				Metadata: testsv1.Metadata_builder{
					Tenant: "my-tenant",
					Name:   "my-object",
				}.Build(),
			}.Build()
			object.SetId("duplicate")
			_, err := dao.Create().
				SetObject(object).
				Do(ctx)
			Expect(err).ToNot(HaveOccurred())
			_, err = dao.Create().
				SetObject(object).
				Do(ctx)
			Expect(err).To(HaveOccurred())

			metrics := metricsServer.Metrics()
			Expect(metrics).To(MatchLine(
				`^\w+_operation_duration_count\{.*error="23505".*\} .*$`,
			))
		})

		DescribeTable(
			"Includes operation label",
			func(operation string, action func()) {
				action()

				metrics := metricsServer.Metrics()
				Expect(metrics).To(MatchLine(
					`^\w+_operation_duration_count\{.*type="%s".*\} .*$`, operation,
				))
			},
			Entry(
				"Create",
				"create",
				func() {
					_, err := dao.Create().
						SetObject(
							testsv1.Object_builder{
								Metadata: testsv1.Metadata_builder{
									Tenant: "my-tenant",
									Name:   "my-object",
								}.Build(),
							}.Build(),
						).
						Do(ctx)
					Expect(err).ToNot(HaveOccurred())
				},
			),
			Entry(
				"Get",
				"get",
				func() {
					response, err := dao.Create().
						SetObject(
							testsv1.Object_builder{
								Metadata: testsv1.Metadata_builder{
									Tenant: "my-tenant",
									Name:   "my-object",
								}.Build(),
							}.Build(),
						).
						Do(ctx)
					Expect(err).ToNot(HaveOccurred())
					_, err = dao.Get().
						SetId(response.GetObject().GetId()).
						Do(ctx)
					Expect(err).ToNot(HaveOccurred())
				},
			),
			Entry(
				"List",
				"list",
				func() {
					_, err := dao.List().
						Do(ctx)
					Expect(err).ToNot(HaveOccurred())
				},
			),
			Entry(
				"Count",
				"count",
				func() {
					_, err := dao.List().
						Do(ctx)
					Expect(err).ToNot(HaveOccurred())
				},
			),
			Entry(
				"Update",
				"update",
				func() {
					response, err := dao.Create().
						SetObject(
							testsv1.Object_builder{
								Metadata: testsv1.Metadata_builder{
									Tenant: "my-tenant",
									Name:   "my-object",
								}.Build(),
							}.Build(),
						).
						Do(ctx)
					Expect(err).ToNot(HaveOccurred())
					object := response.GetObject()
					object.SetMyString("my_value")
					_, err = dao.Update().
						SetObject(object).
						Do(ctx)
					Expect(err).ToNot(HaveOccurred())
				},
			),
			Entry(
				"Delete",
				"delete",
				func() {
					response, err := dao.Create().
						SetObject(
							testsv1.Object_builder{
								Metadata: testsv1.Metadata_builder{
									Tenant: "my-tenant",
									Name:   "my-object",
								}.Build(),
							}.Build(),
						).
						Do(ctx)
					Expect(err).ToNot(HaveOccurred())
					_, err = dao.Delete().
						SetId(response.GetObject().GetId()).
						Do(ctx)
					Expect(err).ToNot(HaveOccurred())
				},
			),
			Entry(
				"Exists",
				"exists",
				func() {
					response, err := dao.Create().
						SetObject(
							testsv1.Object_builder{
								Metadata: testsv1.Metadata_builder{
									Tenant: "my-tenant",
									Name:   "my-object",
								}.Build(),
							}.Build(),
						).
						Do(ctx)
					Expect(err).ToNot(HaveOccurred())
					_, err = dao.Exists().
						SetId(response.GetObject().GetId()).
						Do(ctx)
					Expect(err).ToNot(HaveOccurred())
				},
			),
			Entry(
				"Lock",
				"lock",
				func() {
					response, err := dao.Create().
						SetObject(
							testsv1.Object_builder{
								Metadata: testsv1.Metadata_builder{
									Tenant: "my-tenant",
									Name:   "my-object",
								}.Build(),
							}.Build(),
						).
						Do(ctx)
					Expect(err).ToNot(HaveOccurred())
					_, err = dao.Lock().
						AddId(response.GetObject().GetId()).
						Do(ctx)
					Expect(err).ToNot(HaveOccurred())
				},
			),
			Entry(
				"Archive",
				"archive",
				func() {
					response, err := dao.Create().
						SetObject(
							testsv1.Object_builder{
								Metadata: testsv1.Metadata_builder{
									Tenant: "my-tenant",
									Name:   "my-object",
								}.Build(),
							}.Build(),
						).
						Do(ctx)
					Expect(err).ToNot(HaveOccurred())
					_, err = dao.Delete().
						SetId(response.GetObject().GetId()).
						Do(ctx)
					Expect(err).ToNot(HaveOccurred())
				},
			),
		)

		It("Counts multiple operations correctly", func() {
			for i := range 3 {
				_, err := dao.Create().
					SetObject(
						testsv1.Object_builder{
							Metadata: testsv1.Metadata_builder{
								Tenant: "my-tenant",
								Name:   fmt.Sprintf("my-object-%d", i),
							}.Build(),
						}.Build(),
					).
					Do(ctx)
				Expect(err).ToNot(HaveOccurred())
			}

			metrics := metricsServer.Metrics()
			Expect(metrics).To(MatchLine(
				`^\w+_operation_duration_count\{.*type="create".*\} 3$`,
			))
		})

		It("Does not record metrics when subsystem is not set", func() {
			noMetricsDao, err := NewGenericDAO[*testsv1.Object]().
				SetLogger(logger).
				SetTenancyLogic(tenancy).
				Build()
			Expect(err).ToNot(HaveOccurred())

			_, err = noMetricsDao.Create().
				SetObject(
					testsv1.Object_builder{
						Metadata: testsv1.Metadata_builder{
							Tenant: "my-tenant",
							Name:   "my-object",
						}.Build(),
					}.Build(),
				).
				Do(ctx)
			Expect(err).ToNot(HaveOccurred())

			metrics := metricsServer.Metrics()
			Expect(metrics).ToNot(MatchLine(`.*operation_duration.*`))
		})

		It("Handles already registered metrics gracefully", func() {
			registry := metricsServer.Registry()

			dao1, err := NewGenericDAO[*testsv1.Object]().
				SetLogger(logger).
				SetTenancyLogic(tenancy).
				SetMetricsRegisterer(registry).
				Build()
			Expect(err).ToNot(HaveOccurred())
			Expect(dao1).ToNot(BeNil())

			dao2, err := NewGenericDAO[*testsv1.Object]().
				SetLogger(logger).
				SetTenancyLogic(tenancy).
				SetMetricsRegisterer(registry).
				Build()
			Expect(err).ToNot(HaveOccurred())
			Expect(dao2).ToNot(BeNil())
		})
	})
})

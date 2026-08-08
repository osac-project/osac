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
	"sync"

	"github.com/jackc/pgx/v5/pgxpool"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"go.uber.org/mock/gomock"

	privatev1 "github.com/osac-project/osac/fulfillment-service/internal/api/osac/private/v1"
	testsv1 "github.com/osac-project/osac/fulfillment-service/internal/api/osac/tests/v1"
	"github.com/osac-project/osac/fulfillment-service/internal/auth"
	"github.com/osac-project/osac/fulfillment-service/internal/collections"
	"github.com/osac-project/osac/fulfillment-service/internal/database"
)

var _ = Describe("Name uniqueness", func() {
	var (
		ctx         context.Context
		ctrl        *gomock.Controller
		tenancy     *auth.MockTenancyLogic
		db          *database.Instance
		pool        *pgxpool.Pool
		tm          database.TxManager
		tenantsDao  *GenericDAO[*privatev1.Tenant]
		projectsDao *GenericDAO[*privatev1.Project]
	)

	createTenant := func(ctx context.Context, name string) {
		_, err := tenantsDao.Create().
			SetObject(privatev1.Tenant_builder{
				Id: name,
				Metadata: privatev1.Metadata_builder{
					Tenant: name,
					Name:   name,
				}.Build(),
			}.Build()).
			Do(ctx)
		Expect(err).ToNot(HaveOccurred())
	}

	createProject := func(ctx context.Context, tenant, name string) {
		_, err := projectsDao.Create().
			SetObject(privatev1.Project_builder{
				Metadata: privatev1.Metadata_builder{
					Tenant: tenant,
					Name:   name,
				}.Build(),
			}.Build()).
			Do(ctx)
		Expect(err).ToNot(HaveOccurred())
	}

	BeforeEach(func() {
		var err error

		ctx = context.Background()

		ctrl = gomock.NewController(GinkgoT())
		DeferCleanup(ctrl.Finish)

		db, err = server.NewInstance().Build()
		Expect(err).ToNot(HaveOccurred())
		DeferCleanup(db.Close)
		pool, err = db.Pool(ctx)
		Expect(err).ToNot(HaveOccurred())
		DeferCleanup(pool.Close)

		tm, err = database.NewTxManager().
			SetLogger(logger).
			SetPool(pool).
			Build()
		Expect(err).ToNot(HaveOccurred())

		tenancy = auth.NewMockTenancyLogic(ctrl)
		tenancy.EXPECT().DetermineVisibleTenants(gomock.Any()).
			Return(collections.NewUniversalSet[string](), nil).
			AnyTimes()

		tenantsDao, err = NewGenericDAO[*privatev1.Tenant]().
			SetLogger(logger).
			SetTenancyLogic(tenancy).
			Build()
		Expect(err).ToNot(HaveOccurred())
		projectsDao, err = NewGenericDAO[*privatev1.Project]().
			SetLogger(logger).
			SetTenancyLogic(tenancy).
			Build()
		Expect(err).ToNot(HaveOccurred())

		// Set up the shared transaction for tenant/project creation:
		tx, err := tm.Begin(ctx)
		Expect(err).ToNot(HaveOccurred())
		txCtx := database.TxIntoContext(ctx, tx)
		createTenant(txCtx, "t1")
		createTenant(txCtx, "t2")
		createProject(txCtx, "t1", "p1")
		createProject(txCtx, "t1", "p2")
		createProject(txCtx, "t2", "p1")
		err = tx.End(txCtx)
		Expect(err).ToNot(HaveOccurred())
	})

	Describe("Project-scoped resources", func() {
		var generic *GenericDAO[*testsv1.Object]

		BeforeEach(func() {
			var err error
			generic, err = NewGenericDAO[*testsv1.Object]().
				SetLogger(logger).
				SetTenancyLogic(tenancy).
				Build()
			Expect(err).ToNot(HaveOccurred())
		})

		createObject := func(ctx context.Context, tenant, project, name string) error {
			tx, err := tm.Begin(ctx)
			if err != nil {
				return err
			}
			txCtx := database.TxIntoContext(ctx, tx)
			_, err = generic.Create().
				SetObject(testsv1.Object_builder{
					Metadata: testsv1.Metadata_builder{
						Tenant:  tenant,
						Project: project,
						Name:    name,
					}.Build(),
				}.Build()).
				Do(txCtx)
			endErr := tx.End(txCtx)
			if err != nil {
				return err
			}
			return endErr
		}

		It("Rejects duplicate name within same tenant and project", func() {
			err := createObject(ctx, "t1", "p1", "my-obj")
			Expect(err).ToNot(HaveOccurred())

			err = createObject(ctx, "t1", "p1", "my-obj")
			Expect(err).To(HaveOccurred())
			var alreadyExists *ErrAlreadyExists
			Expect(errors.As(err, &alreadyExists)).To(BeTrue())
		})

		It("Allows same name in different tenants", func() {
			err := createObject(ctx, "t1", "p1", "my-obj")
			Expect(err).ToNot(HaveOccurred())

			err = createObject(ctx, "t2", "p1", "my-obj")
			Expect(err).ToNot(HaveOccurred())
		})

		It("Allows same name in different projects", func() {
			err := createObject(ctx, "t1", "p1", "my-obj")
			Expect(err).ToNot(HaveOccurred())

			err = createObject(ctx, "t1", "p2", "my-obj")
			Expect(err).ToNot(HaveOccurred())
		})

		It("Rejects name reuse after soft-delete", func() {
			// Create with finalizers so delete doesn't auto-archive:
			tx, err := tm.Begin(ctx)
			Expect(err).ToNot(HaveOccurred())
			txCtx := database.TxIntoContext(ctx, tx)
			response, err := generic.Create().
				SetObject(testsv1.Object_builder{
					Metadata: testsv1.Metadata_builder{
						Tenant:     "t1",
						Project:    "p1",
						Name:       "my-obj",
						Finalizers: []string{"prevent-archive"},
					}.Build(),
				}.Build()).
				Do(txCtx)
			Expect(err).ToNot(HaveOccurred())

			_, err = generic.Delete().
				SetId(response.GetObject().GetId()).
				Do(txCtx)
			Expect(err).ToNot(HaveOccurred())
			err = tx.End(txCtx)
			Expect(err).ToNot(HaveOccurred())

			err = createObject(ctx, "t1", "p1", "my-obj")
			Expect(err).To(HaveOccurred())
			var alreadyExists *ErrAlreadyExists
			Expect(errors.As(err, &alreadyExists)).To(BeTrue())
		})

		It("Allows name reuse after soft-delete and archive", func() {
			// Create without finalizers so delete auto-archives:
			tx, err := tm.Begin(ctx)
			Expect(err).ToNot(HaveOccurred())
			txCtx := database.TxIntoContext(ctx, tx)
			response, err := generic.Create().
				SetObject(testsv1.Object_builder{
					Metadata: testsv1.Metadata_builder{
						Tenant:  "t1",
						Project: "p1",
						Name:    "my-obj",
					}.Build(),
				}.Build()).
				Do(txCtx)
			Expect(err).ToNot(HaveOccurred())

			_, err = generic.Delete().
				SetId(response.GetObject().GetId()).
				Do(txCtx)
			Expect(err).ToNot(HaveOccurred())
			err = tx.End(txCtx)
			Expect(err).ToNot(HaveOccurred())

			err = createObject(ctx, "t1", "p1", "my-obj")
			Expect(err).ToNot(HaveOccurred())
		})

		It("Rejects name update with ErrImmutable", func() {
			tx, err := tm.Begin(ctx)
			Expect(err).ToNot(HaveOccurred())
			txCtx := database.TxIntoContext(ctx, tx)
			response, err := generic.Create().
				SetObject(testsv1.Object_builder{
					Metadata: testsv1.Metadata_builder{
						Tenant:  "t1",
						Project: "p1",
						Name:    "original-name",
					}.Build(),
				}.Build()).
				Do(txCtx)
			Expect(err).ToNot(HaveOccurred())

			object := response.GetObject()
			object.GetMetadata().SetName("new-name")
			_, err = generic.Update().
				SetObject(object).
				Do(txCtx)
			Expect(err).To(HaveOccurred())
			var errImmutable *ErrImmutable
			Expect(errors.As(err, &errImmutable)).To(BeTrue())
			Expect(errImmutable.Fields).To(ContainElement("metadata.name"))
			err = tx.End(txCtx)
			Expect(err).ToNot(HaveOccurred())
		})

		It("Allows at most one success under concurrent creation", func() {
			const goroutines = 10
			errs := make([]error, goroutines)
			var wg sync.WaitGroup
			wg.Add(goroutines)
			for i := 0; i < goroutines; i++ {
				go func(i int) {
					defer GinkgoRecover()
					defer wg.Done()
					errs[i] = createObject(ctx, "t1", "p1", "contested-name")
				}(i)
			}
			wg.Wait()

			successes := 0
			for _, err := range errs {
				if err == nil {
					successes++
				}
			}
			Expect(successes).To(Equal(1))
		})
	})

	Describe("Globally unique resources", func() {
		var rolesDao *GenericDAO[*privatev1.Role]

		BeforeEach(func() {
			var err error
			rolesDao, err = NewGenericDAO[*privatev1.Role]().
				SetLogger(logger).
				SetTenancyLogic(tenancy).
				Build()
			Expect(err).ToNot(HaveOccurred())
		})

		It("Rejects same role name across different tenants", func() {
			tx1, err := tm.Begin(ctx)
			Expect(err).ToNot(HaveOccurred())
			txCtx := database.TxIntoContext(ctx, tx1)
			_, err = rolesDao.Create().
				SetObject(privatev1.Role_builder{
					Metadata: privatev1.Metadata_builder{
						Tenant: "t1",
						Name:   "custom-role",
					}.Build(),
				}.Build()).
				Do(txCtx)
			Expect(err).ToNot(HaveOccurred())
			err = tx1.End(txCtx)
			Expect(err).ToNot(HaveOccurred())

			tx2, err := tm.Begin(ctx)
			Expect(err).ToNot(HaveOccurred())
			txCtx = database.TxIntoContext(ctx, tx2)
			_, err = rolesDao.Create().
				SetObject(privatev1.Role_builder{
					Metadata: privatev1.Metadata_builder{
						Tenant: "t2",
						Name:   "custom-role",
					}.Build(),
				}.Build()).
				Do(txCtx)
			Expect(err).To(HaveOccurred())
			var alreadyExists *ErrAlreadyExists
			Expect(errors.As(err, &alreadyExists)).To(BeTrue())
			err = tx2.End(txCtx)
			Expect(err).ToNot(HaveOccurred())
		})
	})

	Describe("Tenant-unique resources", func() {
		var usersDao *GenericDAO[*privatev1.User]

		BeforeEach(func() {
			var err error
			usersDao, err = NewGenericDAO[*privatev1.User]().
				SetLogger(logger).
				SetTenancyLogic(tenancy).
				Build()
			Expect(err).ToNot(HaveOccurred())
		})

		It("Rejects duplicate user name within same tenant", func() {
			tx1, err := tm.Begin(ctx)
			Expect(err).ToNot(HaveOccurred())
			txCtx := database.TxIntoContext(ctx, tx1)
			_, err = usersDao.Create().
				SetObject(privatev1.User_builder{
					Metadata: privatev1.Metadata_builder{
						Tenant: "t1",
						Name:   "alice",
					}.Build(),
				}.Build()).
				Do(txCtx)
			Expect(err).ToNot(HaveOccurred())
			err = tx1.End(txCtx)
			Expect(err).ToNot(HaveOccurred())

			tx2, err := tm.Begin(ctx)
			Expect(err).ToNot(HaveOccurred())
			txCtx = database.TxIntoContext(ctx, tx2)
			_, err = usersDao.Create().
				SetObject(privatev1.User_builder{
					Metadata: privatev1.Metadata_builder{
						Tenant: "t1",
						Name:   "alice",
					}.Build(),
				}.Build()).
				Do(txCtx)
			Expect(err).To(HaveOccurred())
			var alreadyExists *ErrAlreadyExists
			Expect(errors.As(err, &alreadyExists)).To(BeTrue())
			err = tx2.End(txCtx)
			Expect(err).ToNot(HaveOccurred())
		})

		It("Allows same user name in different tenants", func() {
			tx1, err := tm.Begin(ctx)
			Expect(err).ToNot(HaveOccurred())
			txCtx := database.TxIntoContext(ctx, tx1)
			_, err = usersDao.Create().
				SetObject(privatev1.User_builder{
					Metadata: privatev1.Metadata_builder{
						Tenant: "t1",
						Name:   "alice",
					}.Build(),
				}.Build()).
				Do(txCtx)
			Expect(err).ToNot(HaveOccurred())
			err = tx1.End(txCtx)
			Expect(err).ToNot(HaveOccurred())

			tx2, err := tm.Begin(ctx)
			Expect(err).ToNot(HaveOccurred())
			txCtx = database.TxIntoContext(ctx, tx2)
			_, err = usersDao.Create().
				SetObject(privatev1.User_builder{
					Metadata: privatev1.Metadata_builder{
						Tenant: "t2",
						Name:   "alice",
					}.Build(),
				}.Build()).
				Do(txCtx)
			Expect(err).ToNot(HaveOccurred())
			err = tx2.End(txCtx)
			Expect(err).ToNot(HaveOccurred())
		})
	})
})

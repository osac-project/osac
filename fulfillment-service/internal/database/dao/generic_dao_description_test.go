/*
Copyright (c) 2026 Red Hat Inc.

Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except in compliance with
the License. You may obtain a copy of the License at

   http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
specific language governing permissions and limitations under the License.
*/

package dao

import (
	"context"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"go.uber.org/mock/gomock"

	"github.com/osac-project/osac/fulfillment-service/internal/auth"
	"github.com/osac-project/osac/fulfillment-service/internal/database"
	privatev1 "github.com/osac-project/osac/proto/gen/osac/private/v1"
)

var _ = Describe("Generic DAO description metadata", func() {
	var (
		ctx         context.Context
		ctrl        *gomock.Controller
		tenancy     *auth.MockTenancyLogic
		tx          database.Tx
		tenantsDao  *GenericDAO[*privatev1.Tenant]
		projectsDao *GenericDAO[*privatev1.Project]
	)

	BeforeEach(func() {
		var err error

		ctx = context.Background()
		ctrl = gomock.NewController(GinkgoT())
		DeferCleanup(ctrl.Finish)

		db, err := server.NewInstance().Build()
		Expect(err).ToNot(HaveOccurred())
		DeferCleanup(db.Close)
		pool, err := db.Pool(ctx)
		Expect(err).ToNot(HaveOccurred())
		DeferCleanup(pool.Close)

		manager, err := database.NewTxManager().
			SetLogger(logger).
			SetPool(pool).
			Build()
		Expect(err).ToNot(HaveOccurred())
		tx, err = manager.Begin(ctx)
		Expect(err).ToNot(HaveOccurred())
		DeferCleanup(func() {
			Expect(tx.End(ctx)).To(Succeed())
		})
		ctx = database.TxIntoContext(ctx, tx)

		tenancy = auth.NewMockTenancyLogic(ctrl)
		tenancy.EXPECT().DetermineVisibility(gomock.Any()).
			Return(auth.TotalVisibility(), nil).
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

		_, err = tenantsDao.Create().
			SetObject(privatev1.Tenant_builder{
				Id: "desc-tenant",
				Metadata: privatev1.Metadata_builder{
					Tenant: "desc-tenant",
					Name:   "desc-tenant",
				}.Build(),
			}.Build()).
			Do(ctx)
		Expect(err).ToNot(HaveOccurred())
	})

	ptr := func(value string) *string {
		return &value
	}

	createProject := func(ctx context.Context, id, description string) *privatev1.Project {
		response, err := projectsDao.Create().
			SetObject(privatev1.Project_builder{
				Id: id,
				Metadata: privatev1.Metadata_builder{
					Tenant:      "desc-tenant",
					Name:        id,
					Project:     "",
					Description: &description,
				}.Build(),
			}.Build()).
			Do(ctx)
		Expect(err).ToNot(HaveOccurred())
		return response.GetObject()
	}

	It("persists description through create, get, and list", func() {
		created := createProject(ctx, "desc-project", "doc-123")

		Expect(created.GetMetadata().GetDescription()).To(Equal("doc-123"))

		getResponse, err := projectsDao.Get().SetId("desc-project").Do(ctx)
		Expect(err).ToNot(HaveOccurred())
		Expect(getResponse.GetObject().GetMetadata().GetDescription()).To(Equal("doc-123"))

		listResponse, err := projectsDao.List().
			SetFilter("this.metadata.name == 'desc-project'").
			Do(ctx)
		Expect(err).ToNot(HaveOccurred())
		Expect(listResponse.GetItems()).To(HaveLen(1))
		Expect(listResponse.GetItems()[0].GetMetadata().GetDescription()).To(Equal("doc-123"))
	})

	It("updates description", func() {
		createProject(ctx, "desc-project", "doc-123")

		updateResponse, err := projectsDao.Update().
			SetObject(privatev1.Project_builder{
				Id: "desc-project",
				Metadata: privatev1.Metadata_builder{
					Tenant:      "desc-tenant",
					Name:        "desc-project",
					Project:     "",
					Description: ptr("doc-456"),
				}.Build(),
			}.Build()).
			Do(ctx)
		Expect(err).ToNot(HaveOccurred())
		Expect(updateResponse.GetObject().GetMetadata().GetDescription()).To(Equal("doc-456"))

		getResponse, err := projectsDao.Get().SetId("desc-project").Do(ctx)
		Expect(err).ToNot(HaveOccurred())
		Expect(getResponse.GetObject().GetMetadata().GetDescription()).To(Equal("doc-456"))
	})

	It("archives description", func() {
		createProject(ctx, "desc-project", "doc-123")

		deleteResponse, err := projectsDao.Delete().SetId("desc-project").Do(ctx)
		Expect(err).ToNot(HaveOccurred())
		Expect(deleteResponse.GetObject().GetMetadata().GetDescription()).To(Equal("doc-123"))

		var description *string
		err = tx.QueryRow(ctx, `select description from archived_projects where id = 'desc-project'`).Scan(&description)
		Expect(err).ToNot(HaveOccurred())
		Expect(description).NotTo(BeNil())
		Expect(*description).To(Equal("doc-123"))
	})
})

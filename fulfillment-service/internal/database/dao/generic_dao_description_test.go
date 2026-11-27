/*
Copyright (c) 2026 Red Hat Inc.

Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except in compliance with the
License. You may obtain a copy of the License at

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

	project := func(id string, description *string) *privatev1.Project {
		return privatev1.Project_builder{
			Id: id,
			Metadata: privatev1.Metadata_builder{
				Tenant:      "desc-tenant",
				Name:        id,
				Project:     "",
				Description: description,
			}.Build(),
		}.Build()
	}

	createProject := func(ctx context.Context, id string, description *string) *privatev1.Project {
		response, err := projectsDao.Create().
			SetObject(project(id, description)).
			Do(ctx)
		Expect(err).ToNot(HaveOccurred())
		return response.GetObject()
	}

	descriptionCases := []struct {
		name           string
		description    *string
		expectedValue  string
		expectedExists bool
		expectedSQL    *string
	}{
		{name: "absent", description: nil, expectedExists: false},
		{name: "empty", description: ptr(""), expectedValue: "", expectedExists: true, expectedSQL: ptr("")},
		{name: "nonempty", description: ptr("doc-123"), expectedValue: "doc-123", expectedExists: true, expectedSQL: ptr("doc-123")},
	}

	assertDescription := func(metadata *privatev1.Metadata, testCase struct {
		name           string
		description    *string
		expectedValue  string
		expectedExists bool
		expectedSQL    *string
	}) {
		Expect(metadata.HasDescription()).To(Equal(testCase.expectedExists), testCase.name)
		Expect(metadata.GetDescription()).To(Equal(testCase.expectedValue), testCase.name)
	}

	assertSQLDescription := func(id string, expected *string) {
		var actual *string
		err := tx.QueryRow(ctx, `select description from projects where id = $1`, id).Scan(&actual)
		Expect(err).ToNot(HaveOccurred())
		if expected == nil {
			Expect(actual).To(BeNil())
			return
		}
		Expect(actual).NotTo(BeNil())
		Expect(*actual).To(Equal(*expected))
	}

	DescribeTable("preserves description presence through create, get, and list",
		func(testCase struct {
			name           string
			description    *string
			expectedValue  string
			expectedExists bool
			expectedSQL    *string
		}) {
			id := "desc-project-" + testCase.name
			created := createProject(ctx, id, testCase.description)
			assertDescription(created.GetMetadata(), testCase)
			assertSQLDescription(id, testCase.expectedSQL)

			getResponse, err := projectsDao.Get().SetId(id).Do(ctx)
			Expect(err).ToNot(HaveOccurred())
			assertDescription(getResponse.GetObject().GetMetadata(), testCase)

			listResponse, err := projectsDao.List().
				SetFilter("this.metadata.name == '" + id + "'").
				Do(ctx)
			Expect(err).ToNot(HaveOccurred())
			Expect(listResponse.GetItems()).To(HaveLen(1))
			assertDescription(listResponse.GetItems()[0].GetMetadata(), testCase)
		},
		Entry("absent", descriptionCases[0]),
		Entry("empty", descriptionCases[1]),
		Entry("nonempty", descriptionCases[2]),
	)

	It("preserves description presence through update transitions", func() {
		id := "desc-project-update-transitions"
		createProject(ctx, id, nil)

		updated, err := projectsDao.Update().
			SetObject(project(id, ptr(""))).
			Do(ctx)
		Expect(err).ToNot(HaveOccurred())
		Expect(updated.GetObject().GetMetadata().HasDescription()).To(BeTrue())
		Expect(updated.GetObject().GetMetadata().GetDescription()).To(BeEmpty())
		assertSQLDescription(id, ptr(""))

		updated, err = projectsDao.Update().
			SetObject(project(id, ptr("doc-123"))).
			Do(ctx)
		Expect(err).ToNot(HaveOccurred())
		Expect(updated.GetObject().GetMetadata().HasDescription()).To(BeTrue())
		Expect(updated.GetObject().GetMetadata().GetDescription()).To(Equal("doc-123"))
		assertSQLDescription(id, ptr("doc-123"))

		updated, err = projectsDao.Update().
			SetObject(project(id, nil)).
			Do(ctx)
		Expect(err).ToNot(HaveOccurred())
		Expect(updated.GetObject().GetMetadata().HasDescription()).To(BeFalse())
		assertSQLDescription(id, nil)

		getResponse, err := projectsDao.Get().SetId(id).Do(ctx)
		Expect(err).ToNot(HaveOccurred())
		Expect(getResponse.GetObject().GetMetadata().HasDescription()).To(BeFalse())
	})

	DescribeTable("preserves description presence through deletion and archival",
		func(testCase struct {
			name           string
			description    *string
			expectedValue  string
			expectedExists bool
			expectedSQL    *string
		}) {
			id := "desc-project-delete-" + testCase.name
			createProject(ctx, id, testCase.description)

			deleteResponse, err := projectsDao.Delete().SetId(id).Do(ctx)
			Expect(err).ToNot(HaveOccurred())
			assertDescription(deleteResponse.GetObject().GetMetadata(), testCase)

			var archived *string
			err = tx.QueryRow(ctx, `select description from archived_projects where id = $1`, id).Scan(&archived)
			Expect(err).ToNot(HaveOccurred())
			if testCase.expectedSQL == nil {
				Expect(archived).To(BeNil())
				return
			}
			Expect(archived).NotTo(BeNil())
			Expect(*archived).To(Equal(*testCase.expectedSQL))
		},
		Entry("absent", descriptionCases[0]),
		Entry("empty", descriptionCases[1]),
		Entry("nonempty", descriptionCases[2]),
	)

	It("reconstructs an independently stored SQL NULL description as absent", func() {
		id := "desc-project-sql-null"
		createProject(ctx, id, ptr("doc-123"))

		_, err := tx.Exec(ctx, `update projects set description = null where id = $1`, id)
		Expect(err).ToNot(HaveOccurred())

		getResponse, err := projectsDao.Get().SetId(id).Do(ctx)
		Expect(err).ToNot(HaveOccurred())
		Expect(getResponse.GetObject().GetMetadata().HasDescription()).To(BeFalse())
	})
})

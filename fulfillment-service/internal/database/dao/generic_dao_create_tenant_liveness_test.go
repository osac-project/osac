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

	"github.com/osac-project/osac/fulfillment-service/internal/auth"
	"github.com/osac-project/osac/fulfillment-service/internal/database"
	privatev1 "github.com/osac-project/osac/proto/gen/osac/private/v1"
	testsv1 "github.com/osac-project/osac/proto/gen/osac/tests/v1"
)

var _ = Describe("Generic DAO create tenant liveness", func() {
	var (
		ctx      context.Context
		tx       database.Tx
		tenants  *GenericDAO[*privatev1.Tenant]
		objects  *GenericDAO[*testsv1.Object]
		accounts *GenericDAO[*privatev1.ServiceAccount]
	)
	BeforeEach(func() {
		ctx = context.Background()
		db, err := server.NewInstance().Build()
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(db.Close)
		pool, err := db.Pool(ctx)
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(pool.Close)
		tm, err := database.NewTxManager().SetLogger(logger).SetPool(pool).Build()
		Expect(err).NotTo(HaveOccurred())
		tx, err = tm.Begin(ctx)
		Expect(err).NotTo(HaveOccurred())
		ctx = database.TxIntoContext(ctx, tx)
		DeferCleanup(func() { Expect(tx.End(ctx)).To(Succeed()) })
		ctrl := gomock.NewController(GinkgoT())
		DeferCleanup(ctrl.Finish)
		tenancy := auth.NewMockTenancyLogic(ctrl)
		tenancy.EXPECT().DetermineVisibility(gomock.Any()).Return(auth.TotalVisibility(), nil).AnyTimes()
		tenants, err = NewGenericDAO[*privatev1.Tenant]().SetLogger(logger).SetTenancyLogic(tenancy).Build()
		Expect(err).NotTo(HaveOccurred())
		objects, err = NewGenericDAO[*testsv1.Object]().SetLogger(logger).SetTenancyLogic(tenancy).Build()
		Expect(err).NotTo(HaveOccurred())
		accounts, err = NewGenericDAO[*privatev1.ServiceAccount]().SetLogger(logger).SetTenancyLogic(tenancy).Build()
		Expect(err).NotTo(HaveOccurred())
		_, err = tenants.Create().SetObject(privatev1.Tenant_builder{
			Id: "self-created", Metadata: privatev1.Metadata_builder{
				Name: "self-created", Tenant: "self-created",
			}.Build(),
		}.Build()).Do(ctx)
		Expect(err).NotTo(HaveOccurred(), "self-referencing tenant creation must bootstrap without an existing parent")
	})

	DescribeTable("permits normal generic creates in live tenants",
		func(tenant string) {
			_, err := objects.Create().SetObject(&testsv1.Object{
				Metadata: &testsv1.Metadata{Name: "object", Tenant: tenant},
			}).Do(ctx)
			Expect(err).NotTo(HaveOccurred())
		},
		Entry("newly bootstrapped tenant and its automatic default project", "self-created"),
		Entry("reserved shared tenant", auth.SharedTenant),
		Entry("reserved system tenant", auth.SystemTenant),
	)

	DescribeTable("rejects non-service-account generic creates in a deleted tenant",
		func(finalizers []string) {
			_, err := tx.Exec(ctx, `update tenants set deletion_timestamp = now(), finalizers = $1 where name = 'self-created'`,
				finalizers)
			Expect(err).NotTo(HaveOccurred())
			_, err = objects.Create().SetObject(&testsv1.Object{
				Metadata: &testsv1.Metadata{Name: "object", Tenant: "self-created"},
			}).Do(ctx)
			var reference *ErrReference
			Expect(errors.As(err, &reference)).To(BeTrue())
			Expect(reference.Reason).To(Equal("tenant 'self-created' is being deleted"))
		},
		Entry("cleanup still pending", []string{"cleanup"}),
		Entry("cleanup finished with a retained identity", []string{}),
	)

	It("keeps a missing tenant a reference error", func() {
		_, err := objects.Create().SetObject(&testsv1.Object{
			Metadata: &testsv1.Metadata{Name: "object", Tenant: "missing"},
		}).Do(ctx)
		var reference *ErrReference
		Expect(errors.As(err, &reference)).To(BeTrue())
		Expect(reference.Reason).To(Equal("tenant 'missing' doesn't exist"))
	})

	It("allows controller cleanup updates and archival after the tenant is deletion-marked", func() {
		created, err := accounts.Create().SetObject(privatev1.ServiceAccount_builder{
			Metadata: privatev1.Metadata_builder{
				Name: "automation", Tenant: "self-created", Finalizers: []string{"cleanup"},
			}.Build(),
		}.Build()).Do(ctx)
		Expect(err).NotTo(HaveOccurred())
		_, err = tx.Exec(ctx, `update tenants set deletion_timestamp = now() where name = 'self-created'`)
		Expect(err).NotTo(HaveOccurred())
		_, err = accounts.Delete().SetId(created.GetObject().GetId()).Do(ctx)
		Expect(err).NotTo(HaveOccurred())
		read, err := accounts.Get().SetId(created.GetObject().GetId()).Do(ctx)
		Expect(err).NotTo(HaveOccurred())
		read.GetObject().SetStatus(privatev1.ServiceAccountStatus_builder{
			Message: new("cleanup complete"),
		}.Build())
		read.GetObject().GetMetadata().SetFinalizers(nil)
		_, err = accounts.Update().SetObject(read.GetObject()).Do(ctx)
		Expect(err).NotTo(HaveOccurred())
		var archived int
		Expect(tx.QueryRow(ctx, `select count(*) from archived_service_accounts where id = $1`,
			created.GetObject().GetId()).Scan(&archived)).To(Succeed())
		Expect(archived).To(Equal(1))
	})
})

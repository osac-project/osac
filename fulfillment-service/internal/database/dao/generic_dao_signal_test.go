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

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"go.uber.org/mock/gomock"

	"github.com/osac-project/osac/fulfillment-service/internal/auth"
	"github.com/osac-project/osac/fulfillment-service/internal/database"
	testsv1 "github.com/osac-project/osac/proto/gen/osac/tests/v1"
)

var _ = Describe("Generic DAO signal", func() {
	var (
		ctx     context.Context
		tx      database.Tx
		generic *GenericDAO[*testsv1.Object]
	)

	BeforeEach(func() {
		var err error
		ctx = context.Background()

		db, err := server.NewInstance().Build()
		Expect(err).ToNot(HaveOccurred())
		DeferCleanup(db.Close)
		pool, err := db.Pool(ctx)
		Expect(err).ToNot(HaveOccurred())
		DeferCleanup(pool.Close)

		tm, err := database.NewTxManager().
			SetLogger(logger).
			SetPool(pool).
			Build()
		Expect(err).ToNot(HaveOccurred())
		tx, err = tm.Begin(ctx)
		Expect(err).ToNot(HaveOccurred())
		DeferCleanup(func() {
			Expect(tx.End(ctx)).To(Succeed())
		})
		ctx = database.TxIntoContext(ctx, tx)

		ctrl := gomock.NewController(GinkgoT())
		DeferCleanup(ctrl.Finish)
		tenancy := auth.NewMockTenancyLogic(ctrl)
		tenancy.EXPECT().DetermineVisibility(gomock.Any()).
			Return(auth.TotalVisibility(), nil).
			AnyTimes()
		_, err = tx.Exec(ctx, `
			insert into tenants (id, tenant, name, data)
			values ('my-tenant', 'my-tenant', 'my-tenant', '{}')
		`)
		Expect(err).ToNot(HaveOccurred())
		generic, err = NewGenericDAO[*testsv1.Object]().
			SetLogger(logger).
			SetTenancyLogic(tenancy).
			Build()
		Expect(err).ToNot(HaveOccurred())
	})

	It("requires an identifier", func() {
		response, err := generic.Signal().Do(ctx)
		Expect(err).To(MatchError("object identifier is mandatory"))
		Expect(response).To(BeNil())
	})

	It("preserves the version and adds a signal change", func() {
		createResponse, err := generic.Create().
			SetObject(testsv1.Object_builder{
				Metadata: testsv1.Metadata_builder{
					Name:   "my-object",
					Tenant: "my-tenant",
				}.Build(),
			}.Build()).
			Do(ctx)
		Expect(err).ToNot(HaveOccurred())
		id := createResponse.GetObject().GetId()

		response, err := generic.Signal().
			SetId(id).
			Do(ctx)
		Expect(err).ToNot(HaveOccurred())
		Expect(response).ToNot(BeNil())

		getResponse, err := generic.Get().SetId(id).Do(ctx)
		Expect(err).ToNot(HaveOccurred())
		Expect(getResponse.GetObject().GetMetadata().GetVersion()).To(Equal(int32(0)))

		var table, op, storedID string
		var version int32
		err = tx.QueryRow(ctx, `
			select "table", op, data->>'id', (data->>'version')::integer
			from changes
			where "table" = 'objects' and op = 'SIGNAL' and data->>'id' = $1
		`, id).Scan(&table, &op, &storedID, &version)
		Expect(err).ToNot(HaveOccurred())
		Expect(table).To(Equal("objects"))
		Expect(op).To(Equal("SIGNAL"))
		Expect(storedID).To(Equal(id))
		Expect(version).To(Equal(int32(0)))
	})

	It("returns not found for an unknown identifier", func() {
		response, err := generic.Signal().SetId("missing").Do(ctx)
		Expect(err).To(MatchError(&ErrNotFound{IDs: []string{"missing"}}))
		Expect(response).To(BeNil())
	})
})

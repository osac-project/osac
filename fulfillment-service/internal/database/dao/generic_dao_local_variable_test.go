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
	"encoding/json"
	"errors"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"go.uber.org/mock/gomock"
	"google.golang.org/protobuf/types/known/timestamppb"

	"github.com/osac-project/osac/fulfillment-service/internal/auth"
	"github.com/osac-project/osac/fulfillment-service/internal/database"
	testsv1 "github.com/osac-project/osac/proto/gen/osac/tests/v1"
)

var _ = Describe("Generic DAO local variables", func() {
	const setConfig = "select set_config($1, $2, true)"
	var (
		ctx     context.Context
		builder *GenericDAOBuilder[*testsv1.Object]
		tenancy *auth.MockTenancyLogic
		tx      *database.MockTx
		object  *testsv1.Object
	)

	BeforeEach(func(specCtx SpecContext) {
		ctrl := gomock.NewController(GinkgoT())
		tenancy = auth.NewMockTenancyLogic(ctrl)
		tenancy.EXPECT().DetermineVisibility(gomock.Any()).Return(auth.TotalVisibility(), nil).AnyTimes()
		builder = NewGenericDAO[*testsv1.Object]().SetLogger(logger).SetTenancyLogic(tenancy)
		tx = database.NewMockTx(ctrl)
		ctx = database.TxIntoContext(specCtx, tx)
		object = testsv1.Object_builder{
			Id: "object-id",
			Metadata: testsv1.Metadata_builder{
				Name:   "object",
				Tenant: "tenant",
			}.Build(),
		}.Build()
	})

	It("sets multiple JSON values before create", func() {
		cases := []struct {
			name  string
			value any
			json  string
		}{
			{"message", testsv1.Metadata_builder{CreationTimestamp: &timestamppb.Timestamp{}}.Build(),
				`{"creation_timestamp":"1970-01-01T00:00:00Z"}`},
			{"text", "O'Reilly", `"O'Reilly"`},
			{"number", 42, `42`},
			{"flag", true, `true`},
			{"cleared", nil, ""},
			{"nil_pointer", (*string)(nil), ""},
			{"nil_message", (*testsv1.Metadata)(nil), ""},
			{"json_null", json.RawMessage("null"), "null"},
		}
		var settings []any
		var calls, wantCalls []string
		for _, tc := range cases {
			wantCalls = append(wantCalls, tc.name)
			builder.AddLocalVariable(tc.name, func(callbackCtx context.Context) (any, error) {
				Expect(callbackCtx).To(BeIdenticalTo(ctx))
				calls = append(calls, tc.name)
				return tc.value, nil
			})
			settings = append(settings, tx.EXPECT().Exec(ctx, setConfig, "osac."+tc.name, tc.json))
		}
		settings = append(settings, tx.EXPECT().QueryRow(ctx, gomock.Regex(`insert into objects`), gomock.Any()).
			Return(localVariableRow(func(...any) error { return nil })))
		gomock.InOrder(settings...)
		tx.EXPECT().ReportError(gomock.Any())
		dao, err := builder.Build()
		Expect(err).NotTo(HaveOccurred())
		_, err = dao.Create().SetObject(object).Do(ctx)
		Expect(err).NotTo(HaveOccurred())
		Expect(calls).To(Equal(wantCalls))
	})

	It("applies the outer settings after a callback writes in the same transaction", func() {
		inner, err := builder.AddLocalVariable("test", func(context.Context) (any, error) {
			return "inner", nil
		}).Build()
		Expect(err).NotTo(HaveOccurred())

		outer, err := NewGenericDAO[*testsv1.Object]().SetLogger(logger).
			SetTenancyLogic(tenancy).
			AddLocalVariable("test", func(context.Context) (any, error) {
				return "outer", nil
			}).
			AddLocalVariable("nested", func(callbackCtx context.Context) (any, error) {
				_, err := inner.Signal().SetId(object.GetId()).Do(callbackCtx)
				return true, err
			}).Build()
		Expect(err).NotTo(HaveOccurred())

		gomock.InOrder(
			tx.EXPECT().Exec(ctx, setConfig, "osac.test", `"inner"`),
			tx.EXPECT().Exec(ctx, "set local osac.signal = 'on'"),
			tx.EXPECT().Exec(ctx, gomock.Regex(`update objects`), object.GetId()).
				Return(pgconn.NewCommandTag("UPDATE 1"), nil),
			tx.EXPECT().Exec(ctx, "set local osac.signal = 'off'"),
			tx.EXPECT().Exec(ctx, setConfig, "osac.test", `"outer"`),
			tx.EXPECT().Exec(ctx, setConfig, "osac.nested", "true"),
			tx.EXPECT().QueryRow(ctx, gomock.Regex(`insert into objects`), gomock.Any()).
				Return(localVariableRow(func(...any) error { return nil })),
		)
		tx.EXPECT().ReportError(gomock.Any()).Times(2)
		_, err = outer.Create().SetObject(object).Do(ctx)
		Expect(err).NotTo(HaveOccurred())
	})

	Context("with a callback that clears a setting", func() {
		var (
			dao   *GenericDAO[*testsv1.Object]
			calls int
		)

		BeforeEach(func() {
			calls = 0
			var err error
			dao, err = builder.AddLocalVariable("test", func(context.Context) (any, error) {
				calls++
				return nil, nil
			}).Build()
			Expect(err).NotTo(HaveOccurred())
			tx.EXPECT().ReportError(gomock.Any())
		})

		It("clears the setting once before update", func() {
			gomock.InOrder(
				tx.EXPECT().Exec(ctx, setConfig, "osac.test", ""),
				tx.EXPECT().QueryRow(ctx, gomock.Regex(`update objects`), gomock.Any()).
					Return(localVariableRow(func(...any) error { return pgx.ErrNoRows })),
			)
			_, err := dao.Update().SetObject(object).Do(ctx)
			Expect(err).To(MatchError(&ErrNotFound{IDs: []string{object.GetId()}}))
			Expect(calls).To(Equal(1))
		})

		It("clears the setting once for delete and archive", func() {
			gomock.InOrder(
				tx.EXPECT().Exec(ctx, setConfig, "osac.test", ""),
				tx.EXPECT().QueryRow(ctx, gomock.Regex(`update objects`), object.GetId()).
					Return(localVariableRow(func(dest ...any) error {
						*dest[10].(*[]byte) = []byte("{}")
						return nil
					})),
				tx.EXPECT().Exec(ctx, gomock.Regex(`insert into archived_objects`), gomock.Any()),
				tx.EXPECT().Exec(ctx, gomock.Regex(`delete from objects`), object.GetId()),
			)
			_, err := dao.Delete().SetId(object.GetId()).Do(ctx)
			Expect(err).NotTo(HaveOccurred())
			Expect(calls).To(Equal(1))
		})

		It("clears the setting once before signal", func() {
			gomock.InOrder(
				tx.EXPECT().Exec(ctx, setConfig, "osac.test", ""),
				tx.EXPECT().Exec(ctx, "set local osac.signal = 'on'"),
				tx.EXPECT().Exec(ctx, gomock.Regex(`update objects`), object.GetId()).
					Return(pgconn.NewCommandTag("UPDATE 1"), nil),
				tx.EXPECT().Exec(ctx, "set local osac.signal = 'off'"),
			)
			_, err := dao.Signal().SetId(object.GetId()).Do(ctx)
			Expect(err).NotTo(HaveOccurred())
			Expect(calls).To(Equal(1))
		})

		It("does not call the callback for exists", func() {
			tx.EXPECT().QueryRow(ctx, gomock.Regex(`select count\(\*\) from objects`), object.GetId()).
				Return(localVariableRow(func(...any) error { return nil }))
			_, err := dao.Exists().SetId(object.GetId()).Do(ctx)
			Expect(err).NotTo(HaveOccurred())
			Expect(calls).To(BeZero())
		})
	})

	failure := errors.New("injected failure")
	DescribeTable("prevents mutation on failure",
		func(value any, callbackErr, sqlErr error, want string) {
			dao, err := builder.AddLocalVariable("test", func(context.Context) (any, error) {
				return value, callbackErr
			}).Build()
			Expect(err).NotTo(HaveOccurred())
			if sqlErr != nil {
				tx.EXPECT().Exec(ctx, setConfig, "osac.test", "true").Return(pgconn.CommandTag{}, sqlErr)
			}
			tx.EXPECT().ReportError(gomock.Any()).Do(func(err *error) {
				Expect(*err).To(MatchError(ContainSubstring(want)))
			})
			response, err := dao.Create().SetObject(object).Do(ctx)
			Expect(response).To(BeNil())
			Expect(err).To(MatchError(ContainSubstring(want)))
			if callbackErr != nil || sqlErr != nil {
				Expect(errors.Is(err, failure)).To(BeTrue())
			}
		},
		Entry("callback error", nil, failure, nil, "failed to evaluate"),
		Entry("unsupported JSON value", make(chan int), nil, nil, "failed to encode"),
		Entry("invalid protobuf timestamp", &timestamppb.Timestamp{Nanos: -1}, nil, nil, "failed to encode"),
		Entry("SQL error", true, nil, failure, "failed to set"),
	)

	callback := func(context.Context) (any, error) { return nil, nil }
	DescribeTable("rejects invalid names at build time",
		func(name string) {
			_, err := builder.AddLocalVariable(name, callback).Build()
			Expect(err).To(MatchError(ContainSubstring("invalid DAO local variable name")))
		},
		Entry("empty", ""),
		Entry("already qualified", "osac.actor"),
		Entry("starts with a digit", "1actor"),
		Entry("contains whitespace", "actor name"),
		Entry("reserved", "signal"),
	)

	It("rejects duplicate names at build time", func() {
		_, err := builder.AddLocalVariable("test", callback).AddLocalVariable("test", callback).Build()
		Expect(err).To(MatchError(ContainSubstring("duplicate DAO local variable name")))
	})

	It("rejects missing callbacks at build time", func() {
		_, err := builder.AddLocalVariable("test", nil).Build()
		Expect(err).To(MatchError(ContainSubstring("missing callback")))
	})
})

type localVariableRow func(...any) error

func (row localVariableRow) Scan(dest ...any) error { return row(dest...) }

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
	"log/slog"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	. "github.com/onsi/gomega"
	"go.uber.org/mock/gomock"
	"google.golang.org/protobuf/types/known/timestamppb"

	"github.com/osac-project/osac/fulfillment-service/internal/auth"
	"github.com/osac-project/osac/fulfillment-service/internal/database"
	testsv1 "github.com/osac-project/osac/proto/gen/osac/tests/v1"
)

// These tests use a mock transaction and don't need the Ginkgo suite's PostgreSQL container.
func TestGenericDAOLocalVariables(t *testing.T) {
	const setConfig = "select set_config($1, $2, true)"
	tag := pgconn.NewCommandTag("UPDATE 1")
	object := testsv1.Object_builder{
		Id: "object-id",
		Metadata: testsv1.Metadata_builder{
			Name:   "object",
			Tenant: "tenant",
		}.Build(),
	}.Build()

	t.Run("sets multiple JSON values before create", func(t *testing.T) {
		g := NewWithT(t)
		builder, tx, ctx := newLocalVariableTest(t)
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
			{"nil_message", (*testsv1.Metadata)(nil), ""},
		}
		var settings []any
		calls := map[string]int{}
		for _, tc := range cases {
			builder.AddLocalVariable(tc.name, func(callbackCtx context.Context) (any, error) {
				g.Expect(callbackCtx).To(BeIdenticalTo(ctx))
				calls[tc.name]++
				return tc.value, nil
			})
			settings = append(settings, tx.EXPECT().Exec(ctx, setConfig, "osac."+tc.name, tc.json))
		}
		settings = append(settings, tx.EXPECT().QueryRow(ctx, gomock.Regex(`insert into objects`), gomock.Any()).
			Return(localVariableRow(func(...any) error { return nil })))
		gomock.InOrder(settings...)
		tx.EXPECT().ReportError(gomock.Any())
		dao, err := builder.Build()
		g.Expect(err).NotTo(HaveOccurred())
		_, err = dao.Create().SetObject(object).Do(ctx)
		g.Expect(err).NotTo(HaveOccurred())
		for _, tc := range cases {
			g.Expect(calls[tc.name]).To(Equal(1), tc.name)
		}
	})

	for _, operation := range []string{"update", "delete/archive", "signal", "exists"} {
		t.Run(operation, func(t *testing.T) {
			g := NewWithT(t)
			builder, tx, ctx := newLocalVariableTest(t)
			calls := 0
			dao, err := builder.AddLocalVariable("test", func(context.Context) (any, error) {
				calls++
				return nil, nil
			}).Build()
			g.Expect(err).NotTo(HaveOccurred())
			tx.EXPECT().ReportError(gomock.Any())
			var setting *gomock.Call
			if operation != "exists" {
				setting = tx.EXPECT().Exec(ctx, setConfig, "osac.test", "")
			}
			switch operation {
			case "update":
				gomock.InOrder(
					setting,
					tx.EXPECT().QueryRow(ctx, gomock.Regex(`update objects`), gomock.Any()).
						Return(localVariableRow(func(...any) error { return pgx.ErrNoRows })),
				)
				_, err = dao.Update().SetObject(object).Do(ctx)
				g.Expect(err).To(MatchError(&ErrNotFound{IDs: []string{object.GetId()}}))
			case "delete/archive":
				gomock.InOrder(
					setting,
					tx.EXPECT().QueryRow(ctx, gomock.Regex(`update objects`), object.GetId()).
						Return(localVariableRow(func(dest ...any) error {
							*dest[10].(*[]byte) = []byte("{}")
							return nil
						})),
					tx.EXPECT().Exec(ctx, gomock.Regex(`insert into archived_objects`), gomock.Any()),
					tx.EXPECT().Exec(ctx, gomock.Regex(`delete from objects`), object.GetId()),
				)
				_, err = dao.Delete().SetId(object.GetId()).Do(ctx)
				g.Expect(err).NotTo(HaveOccurred())
			case "signal":
				gomock.InOrder(
					setting,
					tx.EXPECT().Exec(ctx, "set local osac.signal = 'on'"),
					tx.EXPECT().Exec(ctx, gomock.Regex(`update objects`), object.GetId()).Return(tag, nil),
					tx.EXPECT().Exec(ctx, "set local osac.signal = 'off'"),
				)
				_, err = dao.Signal().SetId(object.GetId()).Do(ctx)
				g.Expect(err).NotTo(HaveOccurred())
			case "exists":
				tx.EXPECT().QueryRow(ctx, gomock.Regex(`select count\(\*\) from objects`), object.GetId()).
					Return(localVariableRow(func(...any) error { return nil }))
				_, err = dao.Exists().SetId(object.GetId()).Do(ctx)
				g.Expect(err).NotTo(HaveOccurred())
			}
			if operation == "exists" {
				g.Expect(calls).To(BeZero())
			} else {
				g.Expect(calls).To(Equal(1))
			}
		})
	}

	failure := errors.New("injected failure")
	for _, tc := range []struct {
		name        string
		value       any
		callbackErr error
		sqlErr      error
		want        string
	}{
		{name: "callback", callbackErr: failure, want: "failed to evaluate"},
		{name: "unsupported JSON value", value: make(chan int), want: "failed to encode"},
		{name: "invalid protobuf timestamp", value: &timestamppb.Timestamp{Nanos: -1}, want: "failed to encode"},
		{name: "SQL", value: true, sqlErr: failure, want: "failed to set"},
	} {
		t.Run(tc.name+" failure prevents mutation", func(t *testing.T) {
			g := NewWithT(t)
			builder, tx, ctx := newLocalVariableTest(t)
			dao, err := builder.AddLocalVariable("test", func(context.Context) (any, error) {
				return tc.value, tc.callbackErr
			}).Build()
			g.Expect(err).NotTo(HaveOccurred())
			if tc.sqlErr != nil {
				tx.EXPECT().Exec(ctx, setConfig, "osac.test", "true").Return(pgconn.CommandTag{}, tc.sqlErr)
			}
			tx.EXPECT().ReportError(gomock.Any()).Do(func(err *error) {
				g.Expect(*err).To(MatchError(ContainSubstring(tc.want)))
			})
			response, err := dao.Create().SetObject(object).Do(ctx)
			g.Expect(response).To(BeNil())
			g.Expect(err).To(MatchError(ContainSubstring(tc.want)))
			if tc.callbackErr != nil || tc.sqlErr != nil {
				g.Expect(err).To(MatchError(failure))
			}
		})
	}

	t.Run("rejects invalid registrations at build time", func(t *testing.T) {
		g := NewWithT(t)
		callback := func(context.Context) (any, error) { return nil, nil }
		for _, name := range []string{"", "osac.actor", "1actor", "actor name", "signal"} {
			builder, _, _ := newLocalVariableTest(t)
			_, err := builder.AddLocalVariable(name, callback).Build()
			g.Expect(err).To(MatchError(ContainSubstring("invalid DAO local variable name")))
		}
		builder, _, _ := newLocalVariableTest(t)
		_, err := builder.AddLocalVariable("test", callback).AddLocalVariable("test", callback).Build()
		g.Expect(err).To(MatchError(ContainSubstring("duplicate DAO local variable name")))
		builder, _, _ = newLocalVariableTest(t)
		_, err = builder.AddLocalVariable("test", nil).Build()
		g.Expect(err).To(MatchError(ContainSubstring("missing callback")))
	})
}

func newLocalVariableTest(t *testing.T) (*GenericDAOBuilder[*testsv1.Object], *database.MockTx, context.Context) {
	t.Helper()
	ctrl := gomock.NewController(t)
	tenancy := auth.NewMockTenancyLogic(ctrl)
	tenancy.EXPECT().DetermineVisibility(gomock.Any()).Return(auth.TotalVisibility(), nil).AnyTimes()
	builder := NewGenericDAO[*testsv1.Object]().SetLogger(slog.Default()).SetTenancyLogic(tenancy)
	tx := database.NewMockTx(ctrl)
	return builder, tx, database.TxIntoContext(t.Context(), tx)
}

type localVariableRow func(...any) error

func (row localVariableRow) Scan(dest ...any) error { return row(dest...) }

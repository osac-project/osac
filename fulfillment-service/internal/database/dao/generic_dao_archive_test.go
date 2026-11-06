/*
Copyright (c) 2026 Red Hat Inc.

Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except in compliance with the
License. You may obtain a copy of the License at

  http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on an
"AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
specific language governing permissions and limitations under the License.
*/

package dao

import (
	"context"
	"errors"

	"github.com/jackc/pgerrcode"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"go.uber.org/mock/gomock"

	"github.com/osac-project/osac/fulfillment-service/internal/auth"
	"github.com/osac-project/osac/fulfillment-service/internal/database"
	privatev1 "github.com/osac-project/osac/proto/gen/osac/private/v1"
)

// archiveStubTx simulates the transaction states archive() must handle when the deferred assignment
// constraint fires: the statements inside the savepoint fail with execErr (the constraint violation),
// and the recovery probe that checkDeferredArchiveBlockers runs after the savepoint rollback fails with
// queryErr. Savepoint just runs the function and returns its error, which is what a rolled back
// savepoint looks like to the caller.
type archiveStubTx struct {
	database.Tx
	execErr  error
	queryErr error
}

func (t *archiveStubTx) Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error) {
	return pgconn.CommandTag{}, t.execErr
}

func (t *archiveStubTx) QueryRow(ctx context.Context, sql string, args ...any) pgx.Row {
	return archiveStubRow{err: t.queryErr}
}

func (t *archiveStubTx) Savepoint(ctx context.Context, fn func(context.Context) error) error {
	inner := &archiveStubTx{Tx: t.Tx, execErr: t.execErr, queryErr: t.queryErr}
	return fn(database.TxIntoContext(ctx, inner))
}

// archiveStubRow is a pgx.Row whose Scan always returns the given error.
type archiveStubRow struct {
	err error
}

func (r archiveStubRow) Scan(dest ...any) error {
	return r.err
}

var _ = Describe("Archive deferred constraint", func() {
	var ctx context.Context

	BeforeEach(func() {
		ctx = context.Background()
	})

	It("Fails closed when the projects recovery probe fails after the deferred constraint", func() {
		ctrl := gomock.NewController(GinkgoT())
		DeferCleanup(ctrl.Finish)

		tenantsDao, err := NewGenericDAO[*privatev1.Tenant]().
			SetLogger(logger).
			SetTenancyLogic(auth.NewMockTenancyLogic(ctrl)).
			Build()
		Expect(err).ToNot(HaveOccurred())

		// The archive insert fails with the deferred assignment constraint, and the probe that
		// must run afterwards fails too: archive() must return an error, not a silent success.
		probeErr := errors.New("connection refused")
		request := tenantsDao.Delete()
		request.tx = &archiveStubTx{
			execErr: &pgconn.PgError{
				Code:           pgerrcode.ForeignKeyViolation,
				ConstraintName: archiveDeferredConstraint,
			},
			queryErr: probeErr,
		}

		err = request.archive(ctx, archiveArgs{name: "blocked-tenant", tenant: "blocked-tenant"})
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring(
			"failed to check projects for tenant 'blocked-tenant' after deferred archive",
		))
		Expect(errors.Is(err, probeErr)).To(BeTrue())
	})

	It("Fails closed when the deferred constraint is observed for a table other than tenants", func() {
		ctrl := gomock.NewController(GinkgoT())
		DeferCleanup(ctrl.Finish)

		projectsDao, err := NewGenericDAO[*privatev1.Project]().
			SetLogger(logger).
			SetTenancyLogic(auth.NewMockTenancyLogic(ctrl)).
			Build()
		Expect(err).ToNot(HaveOccurred())

		request := projectsDao.Delete()
		request.tx = &archiveStubTx{
			execErr: &pgconn.PgError{
				Code:           pgerrcode.ForeignKeyViolation,
				ConstraintName: archiveDeferredConstraint,
			},
		}

		err = request.archive(ctx, archiveArgs{name: "blocked-tenant", tenant: "blocked-tenant"})
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring("only expected while archiving a tenant"))
	})
})

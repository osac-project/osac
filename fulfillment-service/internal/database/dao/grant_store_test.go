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

	"github.com/jackc/pgx/v5"
	. "github.com/onsi/ginkgo/v2/dsl/core"
	. "github.com/onsi/ginkgo/v2/dsl/table"
	. "github.com/onsi/gomega"
	"go.uber.org/mock/gomock"

	"github.com/osac-project/osac/fulfillment-service/internal/database"
)

// grantRows supplies controlled row and decoding failures without a PostgreSQL server.
type grantRows struct {
	pgx.Rows
	remaining int
	data      string
	scanErr   error
	rowErr    error
	closed    bool
}

func (r *grantRows) Next() bool { r.remaining--; return r.remaining >= 0 }
func (r *grantRows) Close()     { r.closed = true }
func (r *grantRows) Err() error { return r.rowErr }
func (r *grantRows) Scan(dest ...any) error {
	if r.scanErr != nil {
		return r.scanErr
	}
	for _, field := range dest[:4] {
		*field.(*string) = "test"
	}
	*dest[4].(*[]byte) = []byte(r.data)
	return nil
}

var _ = Describe("GrantStore streaming errors", func() {
	var tx *database.MockTx
	var ctx context.Context
	var rows *grantRows

	BeforeEach(func() {
		tx = database.NewMockTx(gomock.NewController(GinkgoT()))
		ctx = database.TxIntoContext(context.Background(), tx)
		rows = &grantRows{remaining: 1, data: `{"spec":{}}`}
	})

	DescribeTable("closes rows on every iteration exit", func(kind string) {
		switch kind {
		case "scan":
			rows.scanErr = errors.New("scan failed")
		case "decode":
			rows.data = `{"spec":{"permissions":"invalid"}}`
		case "rows":
			rows.rowErr = errors.New("stream failed")
		}
		tx.EXPECT().Query(ctx, serviceAccountGrantsQuery, "tenant", "account").Return(rows, nil)
		tx.EXPECT().ReportError(gomock.Any())
		var gotErr error
		for _, err := range (GrantStore{}).ListServiceAccountGrants(ctx, "tenant", "account") {
			gotErr = err
			if kind == "early" {
				break
			}
		}
		if kind == "early" || kind == "complete" {
			Expect(gotErr).ToNot(HaveOccurred())
		} else {
			Expect(gotErr).To(HaveOccurred())
		}
		Expect(rows.closed).To(BeTrue())
	}, Entry("normal exhaustion", "complete"), Entry("early stop", "early"),
		Entry("scan failure", "scan"), Entry("decode failure", "decode"), Entry("row failure", "rows"))

	It("returns query errors once without yielding grants", func() {
		failure := errors.New("query failed")
		tx.EXPECT().Query(ctx, userGrantsQuery, "tenant", "alice").Return(nil, failure)
		tx.EXPECT().ReportError(gomock.Any())
		calls := 0
		for grant, err := range (GrantStore{}).ListUserGrants(ctx, "tenant", "alice") {
			calls++
			Expect(err).To(MatchError(failure))
			Expect(grant).To(BeZero())
		}
		Expect(calls).To(Equal(1))
	})

	It("returns a missing transaction error once", func() {
		calls := 0
		for _, err := range (GrantStore{}).ListUserGrants(context.Background(), "tenant", "alice") {
			calls++
			Expect(err).To(HaveOccurred())
		}
		Expect(calls).To(Equal(1))
	})
})

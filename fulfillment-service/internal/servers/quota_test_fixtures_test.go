/*
Copyright (c) 2026 Red Hat Inc.

Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except in
compliance with the License. You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software distributed under the License is
distributed on an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or
implied. See the License for the specific language governing permissions and limitations under the
License.
*/

package servers

import (
	"fmt"

	. "github.com/onsi/gomega"
	"google.golang.org/genproto/googleapis/rpc/errdetails"
	grpccodes "google.golang.org/grpc/codes"
	grpcstatus "google.golang.org/grpc/status"

	"github.com/osac-project/osac/fulfillment-service/internal/database"
	publicv1 "github.com/osac-project/osac/proto/gen/osac/public/v1"
)

// insertRow inserts one row of an object table with the standard columns.
func insertRow(table, id, tenant, project, data string) {
	tx, err := database.TxFromContext(ctx)
	Expect(err).ToNot(HaveOccurred())
	_, err = tx.Exec(ctx,
		`insert into `+table+` (id, name, tenant, project, creator, data)
		values ($1, $1, $2, $3::ltree, 'system', $4::jsonb)`,
		id, tenant, project, data,
	)
	Expect(err).ToNot(HaveOccurred())
}

// insertQuotaLimit inserts one quota limit row: a project limit when project names a
// project, a tenant override when it is empty. The row's id encodes the dimension, the
// project and, for a tenant other than the suite's, the tenant, so a test can delete the
// row by the id it derived.
func insertQuotaLimit(tenant, project, dimension string, value int64) {
	id := "limit-" + dimension + "-" + project
	if tenant != testTenant {
		id += "-" + tenant
	}
	insertRow("quota_limits", id, tenant, project,
		`{"spec":{"dimension":"`+dimension+`","value":`+fmt.Sprint(value)+`}}`)
}

// quotaFailure extracts the ErrorInfo reason and the QuotaFailure detail of a refusal.
func quotaFailure(err error) (reason string, failure *publicv1.QuotaFailure) {
	status := grpcstatus.Convert(err)
	ExpectWithOffset(1, status.Code()).To(Equal(grpccodes.FailedPrecondition))
	for _, detail := range status.Details() {
		switch detail := detail.(type) {
		case *errdetails.ErrorInfo:
			reason = detail.GetReason()
		case *publicv1.QuotaFailure:
			failure = detail
		}
	}
	return reason, failure
}

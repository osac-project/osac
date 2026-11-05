/*
Copyright (c) 2026 Red Hat Inc.

Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except in compliance with the
License. You may obtain a copy of the License at

  http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on an
"AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the specific
language governing permissions and limitations under the License.
*/

package tenantbillingassignment

import (
	"bytes"
	"time"

	. "github.com/onsi/ginkgo/v2/dsl/core"
	. "github.com/onsi/gomega"
	"google.golang.org/protobuf/types/known/timestamppb"

	"github.com/osac-project/osac/fulfillment-service/internal/terminal"
	publicv1 "github.com/osac-project/osac/proto/gen/osac/public/v1"
)

var _ = Describe("render", func() {
	run := func(format string, response *publicv1.TenantBillingAssignmentsListResponse) string {
		buffer := &bytes.Buffer{}
		console, err := terminal.NewConsole().SetLogger(logger).SetStdout(buffer).Build()
		Expect(err).ToNot(HaveOccurred())
		Expect(render(ctx, console, format, response)).To(Succeed())
		return buffer.String()
	}

	listWith := func(items ...*publicv1.TenantBillingAssignment) *publicv1.TenantBillingAssignmentsListResponse {
		response := &publicv1.TenantBillingAssignmentsListResponse{}
		response.SetSize(int32(len(items)))
		response.SetTotal(int32(len(items)))
		response.SetItems(items)
		return response
	}

	connected := publicv1.TenantBillingAssignment_builder{
		Id: "asg-1",
		Spec: publicv1.TenantBillingAssignmentSpec_builder{
			TenantId: "tenant-a", BillingAccountId: "ba-1",
		}.Build(),
		Status: publicv1.TenantBillingAssignmentStatus_builder{
			ConnectionState: publicv1.TenantBillingAssignmentConnectionState_TENANT_BILLING_ASSIGNMENT_CONNECTION_STATE_CONNECTED,
			EffectiveFrom:   timestamppb.New(time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)),
		}.Build(),
	}.Build()

	pending := publicv1.TenantBillingAssignment_builder{
		Id: "asg-2",
		Spec: publicv1.TenantBillingAssignmentSpec_builder{
			TenantId: "tenant-b", BillingAccountId: "ba-2",
		}.Build(),
		Status: publicv1.TenantBillingAssignmentStatus_builder{
			ConnectionState: publicv1.TenantBillingAssignmentConnectionState_TENANT_BILLING_ASSIGNMENT_CONNECTION_STATE_PENDING,
			EffectiveFrom:   timestamppb.New(time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)),
			Message: "BILLING_PROVIDER_CORRELATION_WRITE_UNSUPPORTED: set externalId on the account in the " +
				"provider to the correlation_id shown by BillingAccounts.List",
		}.Build(),
	}.Build()

	failed := publicv1.TenantBillingAssignment_builder{
		Id: "asg-3",
		Spec: publicv1.TenantBillingAssignmentSpec_builder{
			TenantId: "tenant-c", BillingAccountId: "ba-3",
		}.Build(),
		Status: publicv1.TenantBillingAssignmentStatus_builder{
			ConnectionState: publicv1.TenantBillingAssignmentConnectionState_TENANT_BILLING_ASSIGNMENT_CONNECTION_STATE_FAILED,
		}.Build(),
	}.Build()

	It("renders a CONNECTED assignment without an enum prefix and a dash for the unset effective_to", func() {
		output := run("table", listWith(connected))
		Expect(output).To(MatchRegexp(`\bCONNECTED\b`))
		Expect(output).ToNot(ContainSubstring("TENANT_BILLING_ASSIGNMENT_CONNECTION_STATE_"))
		Expect(output).To(ContainSubstring("tenant-a"))
		Expect(output).To(ContainSubstring("ba-1"))
		Expect(output).To(MatchRegexp(`-\s*\n`))
	})

	It("shows the PENDING remediation message", func() {
		output := run("table", listWith(pending))
		Expect(output).To(MatchRegexp(`\bPENDING\b`))
		Expect(output).To(ContainSubstring("BILLING_PROVIDER_CORRELATION_WRITE_UNSUPPORTED"))
	})

	It("renders the FAILED state, though this increment never produces it", func() {
		output := run("table", listWith(failed))
		Expect(output).To(MatchRegexp(`\bFAILED\b`))
	})

	It("says so when there are no assignments, in table mode", func() {
		output := run("table", listWith())
		Expect(output).To(ContainSubstring("No tenant billing assignments found"))
	})

	It("renders JSON with API field names", func() {
		output := run("json", listWith(connected))
		Expect(output).To(MatchJSON(`{
			"size": 1,
			"total": 1,
			"items": [{
				"id": "asg-1",
				"spec": {"tenant_id": "tenant-a", "billing_account_id": "ba-1"},
				"status": {
					"connection_state": "TENANT_BILLING_ASSIGNMENT_CONNECTION_STATE_CONNECTED",
					"effective_from": "2026-01-02T03:04:05Z"
				}
			}]
		}`))
	})

	It("renders YAML with API field names", func() {
		output := run("yaml", listWith(pending))
		Expect(output).To(ContainSubstring("tenant_id: tenant-b"))
		Expect(output).To(ContainSubstring("BILLING_PROVIDER_CORRELATION_WRITE_UNSUPPORTED"))
	})
})

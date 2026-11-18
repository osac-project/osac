/*
Copyright (c) 2026 Red Hat Inc.

Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except in compliance with the
License. You may obtain a copy of the License at

  http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on an
"AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
specific language governing permissions and limitations under the License.
*/

package it

import (
	"context"
	"encoding/json"
	"os"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/encoding/protojson"

	publicv1 "github.com/osac-project/osac/proto/gen/osac/public/v1"
)

// The dev deployment configures no billing provider, so these specs exercise disabled mode end to end. The
// assignments List has no success shape for disabled mode: it fails on every path, so the CLI command fails
// too.
var _ = Describe("Tenant billing assignments", Label("billing"), func() {
	It("fails with BILLING_NOT_CONFIGURED over public gRPC when no provider is configured", func(ctx context.Context) {
		client := publicv1.NewTenantBillingAssignmentsClient(tool.ExternalView().AdminConn())
		response, err := client.List(ctx, publicv1.TenantBillingAssignmentsListRequest_builder{}.Build())
		Expect(response).To(BeNil())
		Expect(status.Code(err)).To(Equal(codes.FailedPrecondition))
		Expect(status.Convert(err).Message()).To(ContainSubstring("billing is not configured"))
	})

	It("fails from the CLI with the not-configured error", func(ctx context.Context) {
		homeDir, err := tool.NewCLIHomeDir()
		Expect(err).ToNot(HaveOccurred())
		DeferCleanup(func() { Expect(os.RemoveAll(homeDir)).To(Succeed()) })
		_, _, exitCode := tool.LoginCLI(ctx, homeDir, adminUsername, adminsPassword)
		Expect(exitCode).To(Equal(0), "login should succeed")

		stdout, stderr, exitCode := tool.RunCLI(ctx, homeDir, "get", "tenantbillingassignment", "-o", "json")
		Expect(exitCode).ToNot(Equal(0))
		Expect(stdout + stderr).To(ContainSubstring("billing is not configured"))
	})
})

// Compare CLI items with the actual authenticated RPC objects, including complete nested
// spec/status and metadata. Private metadata may additionally expose its own finalizers.
func expectBillingAssignmentCLI(ctx context.Context, home string, private bool, expected []*publicv1.TenantBillingAssignment, args ...string) {
	GinkgoHelper()
	typeName := "osac.public.v1.TenantBillingAssignment"
	if private {
		typeName = "osac.private.v1.TenantBillingAssignment"
	}
	for _, format := range []string{"json", "yaml"} {
		items := billingCLIItems(ctx, home, "tenantbillingassignment", format, typeName, args...)
		Expect(items).To(HaveLen(len(expected)))
		for _, object := range expected {
			encoded, err := (protojson.MarshalOptions{UseProtoNames: true}).Marshal(object)
			Expect(err).ToNot(HaveOccurred())
			var value map[string]any
			Expect(json.Unmarshal(encoded, &value)).To(Succeed())
			var actual map[string]any
			for _, item := range items {
				if item["id"] == object.GetId() {
					Expect(actual).To(BeNil(), "each interval must appear exactly once")
					actual = item
				}
			}
			Expect(actual).ToNot(BeNil(), "missing interval %s", object.GetId())
			Expect(actual["spec"]).To(Equal(value["spec"]))
			Expect(actual["status"]).To(Equal(value["status"]))
			metadata := actual["metadata"].(map[string]any)
			for key, field := range value["metadata"].(map[string]any) {
				Expect(metadata).To(HaveKeyWithValue(key, field))
			}
			Expect(metadata).ToNot(HaveKey("project"), "selected project must not hide tenant-only assignments")
			if !private {
				Expect(metadata).ToNot(HaveKey("finalizers"))
			}
			Expect(actual).ToNot(HaveKey("connection_state"), "status fields must stay nested")
		}
	}
	table := expectBillingCLITable(ctx, home, "tenantbillingassignment", []string{
		"ID", "DELETING", "NAME", "TENANT", "ACCOUNT", "STATE", "EFFECTIVE-FROM", "EFFECTIVE-TO", "MESSAGE",
	}, len(expected), args...)
	for _, object := range expected {
		Expect(table).To(ContainSubstring(object.GetId()))
		Expect(table).To(ContainSubstring(object.GetMetadata().GetName()))
		Expect(table).To(ContainSubstring(object.GetSpec().GetTenant().GetId()))
		Expect(table).To(ContainSubstring(object.GetSpec().GetBillingAccount().GetId()))
		if object.GetStatus().GetMessage() != "" {
			Expect(table).To(ContainSubstring(object.GetStatus().GetMessage()))
		}
	}
}

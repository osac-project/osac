//
// Copyright (c) 2026 Red Hat, Inc.
//
// Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except in compliance with the
// License. You may obtain a copy of the License at
//
//   http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on an
// "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.
//

package logging

import (
	"bytes"
	"context"
	"log/slog"
	"strings"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"google.golang.org/grpc"
	"google.golang.org/protobuf/proto"

	privatev1 "github.com/osac-project/osac/proto/gen/osac/private/v1"
	publicv1 "github.com/osac-project/osac/proto/gen/osac/public/v1"
)

var _ = Describe("Role permission rule logging", func() {
	DescribeTable("omits rule values without modifying the delivered message", func(request proto.Message, response proto.Message) {
		var buffer bytes.Buffer
		logger := slog.New(slog.NewJSONHandler(&buffer, &slog.HandlerOptions{Level: slog.LevelDebug}))
		interceptor, err := NewInterceptor().SetLogger(logger).SetBodies(true).Build()
		Expect(err).ToNot(HaveOccurred())
		original := proto.Clone(response)
		result, err := interceptor.UnaryServer(context.Background(), request, &grpc.UnaryServerInfo{FullMethod: "/osac.public.v1.Roles/Create"},
			func(context.Context, any) (any, error) { return response, nil })
		Expect(err).ToNot(HaveOccurred())
		Expect(proto.Equal(result.(proto.Message), original)).To(BeTrue())
		Expect(strings.Contains(buffer.String(), "test-only-rule-marker")).To(BeFalse(),
			"request and response logs must omit the permission rule values")
		Expect(buffer.String()).To(ContainSubstring("permissions"))
		Expect(buffer.String()).To(ContainSubstring(redactMark))
	}, Entry("public Create", publicv1.RolesCreateRequest_builder{Object: publicv1.Role_builder{
		Metadata: publicv1.Metadata_builder{Name: "my-role"}.Build(),
		Spec: publicv1.RoleSpec_builder{
			Permissions: []*publicv1.PermissionRule{
				publicv1.PermissionRule_builder{
					Service: "test-only-rule-marker",
					Methods: []string{"test-only-rule-marker"},
				}.Build(),
			},
		}.Build(),
	}.Build()}.Build(), publicv1.RolesCreateResponse_builder{Object: publicv1.Role_builder{
		Metadata: publicv1.Metadata_builder{Name: "my-role"}.Build(),
		Spec: publicv1.RoleSpec_builder{
			Permissions: []*publicv1.PermissionRule{
				publicv1.PermissionRule_builder{
					Service: "test-only-rule-marker",
					Methods: []string{"test-only-rule-marker"},
				}.Build(),
			},
		}.Build(),
	}.Build()}.Build()),
		Entry("private Create", privatev1.RolesCreateRequest_builder{Object: privatev1.Role_builder{
			Metadata: privatev1.Metadata_builder{Name: "my-role"}.Build(),
			Spec: privatev1.RoleSpec_builder{
				Permissions: []*privatev1.PermissionRule{
					privatev1.PermissionRule_builder{
						Service: "test-only-rule-marker",
						Methods: []string{"test-only-rule-marker"},
					}.Build(),
				},
			}.Build(),
		}.Build()}.Build(), privatev1.RolesCreateResponse_builder{Object: privatev1.Role_builder{
			Metadata: privatev1.Metadata_builder{Name: "my-role"}.Build(),
			Spec: privatev1.RoleSpec_builder{
				Permissions: []*privatev1.PermissionRule{
					privatev1.PermissionRule_builder{
						Service: "test-only-rule-marker",
						Methods: []string{"test-only-rule-marker"},
					}.Build(),
				},
			}.Build(),
		}.Build()}.Build()))
})

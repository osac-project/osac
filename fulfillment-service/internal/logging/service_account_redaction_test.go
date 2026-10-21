//
// Copyright (c) 2026 Red Hat, Inc.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//   http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.
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

var _ = Describe("Service account credential logging", func() {
	DescribeTable("omits credential bytes without modifying the delivered response", func(response proto.Message, expectRedaction bool) {
		var buffer bytes.Buffer
		logger := slog.New(slog.NewJSONHandler(&buffer, &slog.HandlerOptions{Level: slog.LevelDebug}))
		interceptor, err := NewInterceptor().SetLogger(logger).SetBodies(true).Build()
		Expect(err).ToNot(HaveOccurred())
		original := proto.Clone(response)
		result, err := interceptor.UnaryServer(context.Background(), nil, &grpc.UnaryServerInfo{FullMethod: "/osac.public.v1.ServiceAccounts/Create"},
			func(context.Context, any) (any, error) { return response, nil })
		Expect(err).ToNot(HaveOccurred())
		Expect(proto.Equal(result.(proto.Message), original)).To(BeTrue())
		Expect(strings.Contains(buffer.String(), "test-only-credential-marker")).To(BeFalse(), "response logs must omit the credential")
		Expect(buffer.String()).To(ContainSubstring("clientSecret"))
		if expectRedaction {
			Expect(buffer.String()).To(ContainSubstring(redactMark))
		} else {
			Expect(buffer.String()).ToNot(ContainSubstring(redactMark))
		}
	}, Entry("public Create reference", publicv1.ServiceAccountsCreateResponse_builder{Object: publicv1.ServiceAccount_builder{
		Status: publicv1.ServiceAccountStatus_builder{ClientSecret: publicv1.SecretLocalReference_builder{
			Id: "secret-id", Name: "service-account-secret",
		}.Build()}.Build(),
	}.Build()}.Build(), false),
		Entry("private Create reference", privatev1.ServiceAccountsCreateResponse_builder{Object: privatev1.ServiceAccount_builder{
			Status: privatev1.ServiceAccountStatus_builder{ClientSecret: privatev1.SecretLocalReference_builder{
				Id: "secret-id", Name: "service-account-secret",
			}.Build()}.Build(),
		}.Build()}.Build(), false))
})

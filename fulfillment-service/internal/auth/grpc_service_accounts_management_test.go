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

package auth

import (
	"context"
	"fmt"

	"github.com/golang-jwt/jwt/v5"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/osac-project/osac/fulfillment-service/internal/testing"
	publicv1 "github.com/osac-project/osac/proto/gen/osac/public/v1"
)

var _ = Describe("Service account management authorization", func() {
	DescribeTable("gates management before the resource handler", func(username, role string, admin, tenantAdmin bool) {
		evaluator, err := NewEvaluator().Build()
		Expect(err).ToNot(HaveOccurred())
		interceptor, err := NewGrpcAuthzInterceptor().SetLogger(logger).SetEvaluator(evaluator).Build()
		Expect(err).ToNot(HaveOccurred())
		token := testing.MakeTokenObject(nil, jwt.MapClaims{
			"username": username, "organization": []any{"tenant-a"},
			"realm_access": map[string]any{"roles": []any{role}},
		})
		ctx := ContextWithToken(context.Background(), token)
		for _, api := range []string{"public", "private"} {
			for _, method := range []string{"Create", "Get", "List", "Update", "Delete"} {
				for _, id := range []string{"d3c4e3db-95b8-481d-8b5c-963da477da12", "missing"} {
					called := false
					_, err := interceptor.UnaryServer(ctx, publicv1.ServiceAccountsGetRequest_builder{Id: id}.Build(),
						&grpc.UnaryServerInfo{FullMethod: fmt.Sprintf("/osac.%s.v1.ServiceAccounts/%s", api, method)},
						func(ctx context.Context, request any) (any, error) {
							called = true
							if !admin {
								Expect(SubjectFromContext(ctx).Tenants.Inclusions()).To(ConsistOf("tenant-a"))
							}
							return nil, nil
						})
					allowed := admin || (tenantAdmin && api == "public")
					Expect(called).To(Equal(allowed), api+"/"+method)
					if allowed {
						Expect(err).ToNot(HaveOccurred())
					} else {
						Expect(status.Code(err)).To(Equal(codes.PermissionDenied))
					}
				}
			}
		}
	}, Entry("tenant admin", "user", "tenant-admin", false, true),
		Entry("ordinary user", "user", "", false, false),
		Entry("IdP manager", "user", "tenant-idp-manager", false, false),
		Entry("automation", "robot", "tenant-automation", false, false),
		Entry("CSI", "robot", "osac-csi-driver", false, false),
		Entry("trusted controller", "service-account-osac-controller", "", true, false),
		Entry("trusted administrator", "service-account-osac-admin", "", true, false))
})

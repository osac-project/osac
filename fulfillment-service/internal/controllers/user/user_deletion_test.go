/*
Copyright (c) 2026 Red Hat Inc.

Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except in compliance with the
License. You may obtain a copy of the License at

  http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on an
"AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the specific
language governing permissions and limitations under the License.
*/

package user

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"

	"github.com/osac-project/osac/fulfillment-service/internal/auth"
	"github.com/osac-project/osac/fulfillment-service/internal/controllers/finalizers"
	"github.com/osac-project/osac/fulfillment-service/internal/idp"
	privatev1 "github.com/osac-project/osac/proto/gen/osac/private/v1"
)

// deletionUsersClient records the finalizer write; no database is involved.
type deletionUsersClient struct {
	privatev1.UsersClient
	updates []*privatev1.UsersUpdateRequest
	err     error
}

func (c *deletionUsersClient) Update(_ context.Context, request *privatev1.UsersUpdateRequest, _ ...grpc.CallOption) (*privatev1.UsersUpdateResponse, error) {
	c.updates = append(c.updates, proto.Clone(request).(*privatev1.UsersUpdateRequest))
	return &privatev1.UsersUpdateResponse{}, c.err
}

func deletingUser(tenant, id string) *privatev1.User {
	return privatev1.User_builder{
		Id: "fulfillment-user",
		Metadata: privatev1.Metadata_builder{
			Tenant:            tenant,
			Version:           17,
			DeletionTimestamp: timestamppb.Now(),
			Finalizers:        []string{finalizers.Controller, "another-controller"},
		}.Build(),
		Spec:   privatev1.UserSpec_builder{Username: "same-username"}.Build(),
		Status: privatev1.UserStatus_builder{KeycloakUserId: id}.Build(),
	}.Build()
}

func deletionFunction(baseURL, realm, token string, users *deletionUsersClient) *function {
	source, err := auth.NewStaticTokenSource().SetLogger(slog.Default()).SetToken(&auth.Token{Access: token}).Build()
	Expect(err).ToNot(HaveOccurred())
	client, err := idp.NewClient().SetLogger(slog.Default()).SetBaseURL(baseURL).
		SetRealmName(realm).SetTokenSource(source).Build()
	Expect(err).ToNot(HaveOccurred())
	result, err := NewFunction().SetLogger(slog.Default()).SetConnection(&grpc.ClientConn{}).SetIdpClient(client).Build()
	Expect(err).ToNot(HaveOccurred())
	result.usersClient = users
	return result
}

func expectFinalizerUpdate(users *deletionUsersClient) {
	Expect(users.updates).To(HaveLen(1))
	update := users.updates[0]
	Expect(update.GetObject().GetMetadata().GetFinalizers()).To(Equal([]string{"another-controller"}))
	Expect(update.GetObject().GetMetadata().GetVersion()).To(Equal(int32(17)))
	Expect(update.GetUpdateMask().GetPaths()).To(Equal([]string{"metadata.finalizers"}))
}

var _ = Describe("Cached identity deletion", func() {
	DescribeTable("handles only confirmed absence as successful cleanup",
		func(code int, body string, succeeds bool) {
			requests := make(chan string, 4)
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				defer GinkgoRecover()
				requests <- r.Method + " " + r.URL.EscapedPath()
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(code)
				_, err := w.Write([]byte(body))
				Expect(err).ToNot(HaveOccurred())
			}))
			DeferCleanup(server.Close)
			if code == 0 {
				server.Close() // A transport failure leaves the outcome unknown.
			}
			users := &deletionUsersClient{}
			reconciler := deletionFunction(server.URL, "configured-realm", "test-token", users)
			user := deletingUser("tenant-a", "cached-id")
			err := reconciler.Run(context.Background(), user)
			if succeeds {
				Expect(err).ToNot(HaveOccurred())
				expectFinalizerUpdate(users)
			} else {
				Expect(err).To(HaveOccurred())
				Expect(user.GetMetadata().GetFinalizers()).To(ContainElement(finalizers.Controller))
				Expect(users.updates).To(BeEmpty())
			}
			if code != 0 {
				Expect(requests).To(HaveLen(1))
				Expect(<-requests).To(Equal("DELETE /admin/realms/configured-realm/users/cached-id"))
			}
		},
		Entry("present account", http.StatusNoContent, "", true),
		Entry("absent exact account", http.StatusNotFound, `{"error":"User not found"}`, true),
		Entry("missing realm", http.StatusNotFound, `{"error":"Realm not found."}`, false),
		Entry("generic route 404", http.StatusNotFound, `{"error":"HTTP 404 Not Found"}`, false),
		Entry("empty 404", http.StatusNotFound, "", false),
		Entry("proxy HTML 404", http.StatusNotFound, "<html>User not found</html>", false),
		Entry("malformed JSON", http.StatusNotFound, `{"error":"User not found"`, false),
		Entry("trailing garbage", http.StatusNotFound, `{"error":"User not found"}garbage`, false),
		Entry("unauthorised", http.StatusUnauthorized, `{"error":"User not found"}`, false),
		Entry("forbidden", http.StatusForbidden, `{"error":"User not found"}`, false),
		Entry("server failure", http.StatusInternalServerError, `{"error":"User not found"}`, false),
		Entry("gateway failure", http.StatusBadGateway, "", false),
		Entry("transport failure", 0, "", false),
	)

	It("retries an uncertain deletion without looking up a replacement username", func() {
		responses := make(chan int, 2)
		responses <- http.StatusInternalServerError
		responses <- http.StatusNotFound
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			defer GinkgoRecover()
			Expect(r.Method).To(Equal(http.MethodDelete))
			Expect(r.URL.Path).To(Equal("/admin/realms/configured-realm/users/cached-id"))
			w.WriteHeader(<-responses)
			_, err := w.Write([]byte(`{"error":"User not found"}`))
			Expect(err).ToNot(HaveOccurred())
		}))
		DeferCleanup(server.Close)
		users := &deletionUsersClient{}
		reconciler := deletionFunction(server.URL, "configured-realm", "test-token", users)
		original := deletingUser("tenant-a", "cached-id")
		Expect(reconciler.Run(context.Background(), proto.Clone(original).(*privatev1.User))).ToNot(Succeed())
		Expect(users.updates).To(BeEmpty())
		Expect(reconciler.Run(context.Background(), proto.Clone(original).(*privatev1.User))).To(Succeed())
		expectFinalizerUpdate(users)
		Expect(responses).To(BeEmpty())
	})

	It("retries finalizer persistence with the cached ID after an update conflict", func() {
		requests := make(chan string, 4)
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			defer GinkgoRecover()
			requests <- r.Method + " " + r.URL.Path
			w.WriteHeader(http.StatusNotFound)
			_, err := w.Write([]byte(`{"error":"User not found"}`))
			Expect(err).ToNot(HaveOccurred())
		}))
		DeferCleanup(server.Close)
		users := &deletionUsersClient{err: status.Error(codes.Aborted, "stale version")}
		reconciler := deletionFunction(server.URL, "configured-realm", "test-token", users)
		original := deletingUser("tenant-a", "cached-id")
		Expect(status.Code(reconciler.Run(context.Background(), proto.Clone(original).(*privatev1.User)))).To(Equal(codes.Aborted))
		expectFinalizerUpdate(users)
		users.updates = nil
		users.err = nil
		Expect(reconciler.Run(context.Background(), proto.Clone(original).(*privatev1.User))).To(Succeed())
		expectFinalizerUpdate(users)
		Expect(requests).To(HaveLen(2))
		for range 2 {
			Expect(<-requests).To(Equal("DELETE /admin/realms/configured-realm/users/cached-id"))
		}
	})

	It("does not delete a cached identity without an explicit deletion request", func() {
		users := &deletionUsersClient{err: errors.New("unexpected update")}
		reconciler := deletionFunction("http://127.0.0.1:1", "configured-realm", "test-token", users)
		user := deletingUser("tenant-b", "unaffected-id")
		user.GetMetadata().ClearDeletionTimestamp()
		Expect(reconciler.Run(context.Background(), user)).To(Succeed())
		Expect(users.updates).To(BeEmpty())
		Expect(user.GetMetadata().GetFinalizers()).To(ContainElement(finalizers.Controller))
	})
})

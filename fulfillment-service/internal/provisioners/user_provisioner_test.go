/*
Copyright (c) 2026 Red Hat Inc.

Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except in compliance with the
License. You may obtain a copy of the License at

  http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on an
"AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the specific
language governing permissions and limitations under the License.
*/

package provisioners

import (
	"context"
	"log/slog"
	"testing"

	"github.com/golang-jwt/jwt/v5"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"go.uber.org/mock/gomock"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	privatev1 "github.com/osac-project/fulfillment-service/internal/api/osac/private/v1"
	"github.com/osac-project/fulfillment-service/internal/auth"
	"github.com/osac-project/fulfillment-service/internal/logging"
)

func TestUserProvisioner(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "User Provisioner Suite")
}

var (
	ctx         context.Context
	ctrl        *gomock.Controller
	logger      *slog.Logger
	usersServer *MockUsersServer
	prov        *UserProvisioner
)

var _ = BeforeSuite(func() {
	var err error

	// Create the mock controller:
	ctrl = gomock.NewController(GinkgoT())
	DeferCleanup(ctrl.Finish)

	// Create logger:
	logger, err = logging.NewLogger().
		SetLevel(slog.LevelDebug.String()).
		SetWriter(GinkgoWriter).
		Build()
	Expect(err).ToNot(HaveOccurred())
})

var _ = BeforeEach(func() {
	var err error

	// Create a context:
	ctx = context.Background()

	// Create mock users server:
	usersServer = NewMockUsersServer(ctrl)

	// Create provisioner:
	prov, err = NewUserProvisioner().
		SetLogger(logger).
		SetUsersServer(usersServer).
		Build()
	Expect(err).ToNot(HaveOccurred())
})

var _ = Describe("UserProvisioner", func() {

	Describe("Builder validation", func() {
		It("Requires users server", func() {
			_, err := NewUserProvisioner().
				SetLogger(logger).
				Build()
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("users server is mandatory"))
		})

		It("Builds successfully with all required parameters", func() {
			p, err := NewUserProvisioner().
				SetLogger(logger).
				SetUsersServer(usersServer).
				Build()
			Expect(err).ToNot(HaveOccurred())
			Expect(p).ToNot(BeNil())
		})
	})

	Describe("Provision", func() {
		It("Creates a new user with all fields populated", func() {
			claims := jwt.MapClaims{
				"email": "alice@example.com",
			}

			// Mock List to return no existing users
			usersServer.EXPECT().
				List(gomock.Any(), gomock.Any()).
				Return(&privatev1.UsersListResponse{Size: 0}, nil)

			// Mock Create to succeed
			var capturedRequest *privatev1.UsersCreateRequest
			usersServer.EXPECT().
				Create(gomock.Any(), gomock.Any()).
				DoAndReturn(func(_ context.Context, req *privatev1.UsersCreateRequest) (*privatev1.UsersCreateResponse, error) {
					capturedRequest = req
					return &privatev1.UsersCreateResponse{Object: req.GetObject()}, nil
				})

			err := prov.Provision(ctx, "alice", auth.SystemTenant, claims)
			Expect(err).ToNot(HaveOccurred())

			// Verify the created user had correct fields:
			Expect(capturedRequest).ToNot(BeNil())
			user := capturedRequest.GetObject()
			Expect(user.GetMetadata().GetName()).To(Equal("alice"))
			Expect(user.GetMetadata().GetTenant()).To(Equal(auth.SystemTenant))
			Expect(user.GetSpec().GetUsername()).To(Equal("alice"))
			Expect(user.GetSpec().GetEmail()).To(Equal("alice@example.com"))
			Expect(user.GetSpec().GetEnabled()).To(BeTrue())
		})

		It("Creates a user with minimal claims", func() {
			claims := jwt.MapClaims{}

			// Mock List to return no existing users
			usersServer.EXPECT().
				List(gomock.Any(), gomock.Any()).
				Return(&privatev1.UsersListResponse{Size: 0}, nil)

			// Mock Create to succeed
			var capturedRequest *privatev1.UsersCreateRequest
			usersServer.EXPECT().
				Create(gomock.Any(), gomock.Any()).
				DoAndReturn(func(_ context.Context, req *privatev1.UsersCreateRequest) (*privatev1.UsersCreateResponse, error) {
					capturedRequest = req
					return &privatev1.UsersCreateResponse{Object: req.GetObject()}, nil
				})

			err := prov.Provision(ctx, "bob", auth.SystemTenant, claims)
			Expect(err).ToNot(HaveOccurred())

			// Verify the created user had correct fields:
			Expect(capturedRequest).ToNot(BeNil())
			user := capturedRequest.GetObject()
			Expect(user.GetMetadata().GetName()).To(Equal("bob"))
			Expect(user.GetMetadata().GetTenant()).To(Equal(auth.SystemTenant))
			Expect(user.GetSpec().GetUsername()).To(Equal("bob"))
			Expect(user.GetSpec().GetEmail()).To(Equal(""))
			Expect(user.GetSpec().GetEnabled()).To(BeTrue())
		})

		It("Does not create duplicate user if user already exists", func() {
			claims := jwt.MapClaims{
				"email": "eve@example.com",
			}

			// Mock List to return an existing user
			existingUser := privatev1.User_builder{
				Metadata: privatev1.Metadata_builder{Name: "eve"}.Build(),
				Spec:     privatev1.UserSpec_builder{Username: "eve"}.Build(),
			}.Build()
			usersServer.EXPECT().
				List(gomock.Any(), gomock.Any()).
				Return(&privatev1.UsersListResponse{
					Size:  1,
					Items: []*privatev1.User{existingUser},
				}, nil)

			// Create should not be called since user already exists
			err := prov.Provision(ctx, "eve", auth.SystemTenant, claims)
			Expect(err).ToNot(HaveOccurred())
		})

		It("Handles email claim as non-string gracefully", func() {
			claims := jwt.MapClaims{
				"email": 12345, // Number instead of string
			}

			// Mock List to return no existing users
			usersServer.EXPECT().
				List(gomock.Any(), gomock.Any()).
				Return(&privatev1.UsersListResponse{Size: 0}, nil)

			// Mock Create to succeed
			var capturedRequest *privatev1.UsersCreateRequest
			usersServer.EXPECT().
				Create(gomock.Any(), gomock.Any()).
				DoAndReturn(func(_ context.Context, req *privatev1.UsersCreateRequest) (*privatev1.UsersCreateResponse, error) {
					capturedRequest = req
					return &privatev1.UsersCreateResponse{Object: req.GetObject()}, nil
				})

			err := prov.Provision(ctx, "harry", auth.SystemTenant, claims)
			Expect(err).ToNot(HaveOccurred())

			// Verify email defaults to empty string for non-string
			Expect(capturedRequest).ToNot(BeNil())
			user := capturedRequest.GetObject()
			Expect(user.GetSpec().GetEmail()).To(Equal(""))
		})

		It("Handles AlreadyExists error during concurrent creation", func() {
			claims := jwt.MapClaims{
				"email": "concurrent@example.com",
			}

			// Mock List to return no users (race window)
			usersServer.EXPECT().
				List(gomock.Any(), gomock.Any()).
				Return(&privatev1.UsersListResponse{Size: 0}, nil)

			// Mock Create to return AlreadyExists (another request won the race)
			usersServer.EXPECT().
				Create(gomock.Any(), gomock.Any()).
				Return(nil, status.Error(codes.AlreadyExists, "user already exists"))

			// Should not return error (treat race condition as success)
			err := prov.Provision(ctx, "concurrent", auth.SystemTenant, claims)
			Expect(err).ToNot(HaveOccurred())
		})

		It("Sanitizes username for DNS-1123 compliance in metadata.name", func() {
			claims := jwt.MapClaims{
				"email": "service@example.com",
			}

			// Mock List to return no existing users
			usersServer.EXPECT().
				List(gomock.Any(), gomock.Any()).
				Return(&privatev1.UsersListResponse{Size: 0}, nil)

			// Mock Create to succeed
			var capturedRequest *privatev1.UsersCreateRequest
			usersServer.EXPECT().
				Create(gomock.Any(), gomock.Any()).
				DoAndReturn(func(_ context.Context, req *privatev1.UsersCreateRequest) (*privatev1.UsersCreateResponse, error) {
					capturedRequest = req
					return &privatev1.UsersCreateResponse{Object: req.GetObject()}, nil
				})

			err := prov.Provision(ctx, "service_account", auth.SystemTenant, claims)
			Expect(err).ToNot(HaveOccurred())

			// Verify metadata.name has underscores replaced with hyphens
			Expect(capturedRequest).ToNot(BeNil())
			user := capturedRequest.GetObject()
			Expect(user.GetMetadata().GetName()).To(Equal("service-account"))
			// But spec.username preserves the original
			Expect(user.GetSpec().GetUsername()).To(Equal("service_account"))
		})

		It("Converts uppercase to lowercase in metadata.name", func() {
			claims := jwt.MapClaims{}

			usersServer.EXPECT().
				List(gomock.Any(), gomock.Any()).
				Return(&privatev1.UsersListResponse{Size: 0}, nil)

			var capturedRequest *privatev1.UsersCreateRequest
			usersServer.EXPECT().
				Create(gomock.Any(), gomock.Any()).
				DoAndReturn(func(_ context.Context, req *privatev1.UsersCreateRequest) (*privatev1.UsersCreateResponse, error) {
					capturedRequest = req
					return &privatev1.UsersCreateResponse{Object: req.GetObject()}, nil
				})

			err := prov.Provision(ctx, "John.Doe", auth.SystemTenant, claims)
			Expect(err).ToNot(HaveOccurred())

			user := capturedRequest.GetObject()
			Expect(user.GetMetadata().GetName()).To(Equal("john-doe"))
			Expect(user.GetSpec().GetUsername()).To(Equal("John.Doe"))
		})

		It("Removes leading and trailing hyphens from metadata.name", func() {
			claims := jwt.MapClaims{}

			usersServer.EXPECT().
				List(gomock.Any(), gomock.Any()).
				Return(&privatev1.UsersListResponse{Size: 0}, nil)

			var capturedRequest *privatev1.UsersCreateRequest
			usersServer.EXPECT().
				Create(gomock.Any(), gomock.Any()).
				DoAndReturn(func(_ context.Context, req *privatev1.UsersCreateRequest) (*privatev1.UsersCreateResponse, error) {
					capturedRequest = req
					return &privatev1.UsersCreateResponse{Object: req.GetObject()}, nil
				})

			err := prov.Provision(ctx, "_user@domain_", auth.SystemTenant, claims)
			Expect(err).ToNot(HaveOccurred())

			user := capturedRequest.GetObject()
			Expect(user.GetMetadata().GetName()).To(Equal("user-domain"))
			Expect(user.GetSpec().GetUsername()).To(Equal("_user@domain_"))
		})

	})
})

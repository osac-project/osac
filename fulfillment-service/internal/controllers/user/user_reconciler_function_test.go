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
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"go.uber.org/mock/gomock"
	"google.golang.org/grpc"
	"google.golang.org/protobuf/types/known/timestamppb"

	privatev1 "github.com/osac-project/fulfillment-service/internal/api/osac/private/v1"
	"github.com/osac-project/fulfillment-service/internal/controllers/finalizers"
	"github.com/osac-project/fulfillment-service/internal/idp"
)

var _ = Describe("User Reconciler", func() {
	var (
		ctx        context.Context
		ctrl       *gomock.Controller
		mockClient *idp.MockClientInterface
		function   *function
	)

	BeforeEach(func() {
		ctx = context.Background()
		ctrl = gomock.NewController(GinkgoT())
		mockClient = idp.NewMockClientInterface(ctrl)

		// Create a minimal gRPC connection (won't be used in these tests)
		conn := &grpc.ClientConn{}

		var err error
		function, err = NewFunction().
			SetLogger(slog.Default()).
			SetConnection(conn).
			SetIdpClient(mockClient).
			Build()
		Expect(err).ToNot(HaveOccurred())
	})

	AfterEach(func() {
		ctrl.Finish()
	})

	Describe("Builder validation", func() {
		It("should require logger", func() {
			conn := &grpc.ClientConn{}
			_, err := NewFunction().
				SetConnection(conn).
				SetIdpClient(mockClient).
				Build()
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("logger is mandatory"))
		})

		It("should require connection", func() {
			_, err := NewFunction().
				SetLogger(slog.Default()).
				SetIdpClient(mockClient).
				Build()
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("connection is mandatory"))
		})

		It("should require IDP client", func() {
			conn := &grpc.ClientConn{}
			_, err := NewFunction().
				SetLogger(slog.Default()).
				SetConnection(conn).
				Build()
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("IDP client is mandatory"))
		})
	})

	Describe("Reconcile logic", func() {
		Context("when user has no keycloak_user_id", func() {
			It("should look up and set the keycloak_user_id", func() {
				user := privatev1.User_builder{
					Id: "user-123",
					Metadata: privatev1.Metadata_builder{
						Name:       "testuser",
						Tenant:     "test-tenant",
						Finalizers: []string{finalizers.Controller},
					}.Build(),
					Spec: privatev1.UserSpec_builder{
						Username: "testuser",
						Email:    "test@example.com",
					}.Build(),
					Status: privatev1.UserStatus_builder{}.Build(),
				}.Build()

				// Mock the IDP client to return a user with ID
				mockClient.EXPECT().
					GetUserByUsername(ctx, "test-tenant", "testuser").
					Return(&idp.User{
						ID:       "keycloak-user-id-456",
						Username: "testuser",
						Email:    "test@example.com",
					}, nil)

				task := &task{
					r:    function,
					user: user,
				}

				err := task.reconcile(ctx)
				Expect(err).ToNot(HaveOccurred())
				Expect(user.GetStatus().GetKeycloakUserId()).To(Equal("keycloak-user-id-456"))
			})

			It("should initialize status if it doesn't exist", func() {
				user := privatev1.User_builder{
					Id: "user-123",
					Metadata: privatev1.Metadata_builder{
						Name:       "testuser",
						Tenant:     "test-tenant",
						Finalizers: []string{finalizers.Controller},
					}.Build(),
					Spec: privatev1.UserSpec_builder{
						Username: "testuser",
						Email:    "test@example.com",
					}.Build(),
					// No status field
				}.Build()

				mockClient.EXPECT().
					GetUserByUsername(ctx, "test-tenant", "testuser").
					Return(&idp.User{
						ID:       "keycloak-user-id-789",
						Username: "testuser",
						Email:    "test@example.com",
					}, nil)

				task := &task{
					r:    function,
					user: user,
				}

				err := task.reconcile(ctx)
				Expect(err).ToNot(HaveOccurred())
				Expect(user.HasStatus()).To(BeTrue())
				Expect(user.GetStatus().GetKeycloakUserId()).To(Equal("keycloak-user-id-789"))
			})
		})

		Context("when user already has keycloak_user_id", func() {
			It("should not modify the keycloak_user_id", func() {
				user := privatev1.User_builder{
					Id: "user-123",
					Metadata: privatev1.Metadata_builder{
						Name:       "testuser",
						Tenant:     "test-tenant",
						Finalizers: []string{finalizers.Controller},
					}.Build(),
					Spec: privatev1.UserSpec_builder{
						Username: "testuser",
						Email:    "test@example.com",
					}.Build(),
					Status: privatev1.UserStatus_builder{
						KeycloakUserId: "existing-keycloak-id",
					}.Build(),
				}.Build()

				// Should not call GetUserByUsername since keycloak_user_id already exists
				// No mock expectations set

				task := &task{
					r:    function,
					user: user,
				}

				err := task.reconcile(ctx)
				Expect(err).ToNot(HaveOccurred())
				Expect(user.GetStatus().GetKeycloakUserId()).To(Equal("existing-keycloak-id"))
			})
		})

		Context("when user is not found in IDP", func() {
			It("should not set keycloak_user_id and not return error", func() {
				user := privatev1.User_builder{
					Id: "user-123",
					Metadata: privatev1.Metadata_builder{
						Name:       "testuser",
						Tenant:     "test-tenant",
						Finalizers: []string{finalizers.Controller},
					}.Build(),
					Spec: privatev1.UserSpec_builder{
						Username: "testuser",
						Email:    "test@example.com",
					}.Build(),
					Status: privatev1.UserStatus_builder{}.Build(),
				}.Build()

				// Mock the IDP client to return nil (user not found)
				mockClient.EXPECT().
					GetUserByUsername(ctx, "test-tenant", "testuser").
					Return(nil, nil)

				task := &task{
					r:    function,
					user: user,
				}

				err := task.reconcile(ctx)
				Expect(err).ToNot(HaveOccurred())
				Expect(user.GetStatus().GetKeycloakUserId()).To(BeEmpty())
			})
		})

		Context("when IDP lookup fails with error", func() {
			It("should return error to trigger retry", func() {
				user := privatev1.User_builder{
					Id: "user-123",
					Metadata: privatev1.Metadata_builder{
						Name:       "testuser",
						Tenant:     "test-tenant",
						Finalizers: []string{finalizers.Controller},
					}.Build(),
					Spec: privatev1.UserSpec_builder{
						Username: "testuser",
						Email:    "test@example.com",
					}.Build(),
					Status: privatev1.UserStatus_builder{}.Build(),
				}.Build()

				idpError := errors.New("IDP connection failed")

				// Mock the IDP client to return an error
				mockClient.EXPECT().
					GetUserByUsername(ctx, "test-tenant", "testuser").
					Return(nil, idpError)

				task := &task{
					r:    function,
					user: user,
				}

				err := task.reconcile(ctx)
				Expect(err).To(HaveOccurred())
				Expect(err).To(Equal(idpError))
				Expect(user.GetStatus().GetKeycloakUserId()).To(BeEmpty())
			})
		})

		Context("when user has no finalizer", func() {
			It("should add the finalizer and return early", func() {
				user := privatev1.User_builder{
					Id: "user-123",
					Metadata: privatev1.Metadata_builder{
						Name:   "testuser",
						Tenant: "test-tenant",
					}.Build(),
					Spec: privatev1.UserSpec_builder{
						Username: "testuser",
						Email:    "test@example.com",
					}.Build(),
					Status: privatev1.UserStatus_builder{}.Build(),
				}.Build()

				task := &task{
					r:    function,
					user: user,
				}

				err := task.reconcile(ctx)
				Expect(err).ToNot(HaveOccurred())
				Expect(user.GetMetadata().GetFinalizers()).To(ContainElement(finalizers.Controller))
				Expect(user.GetStatus().GetKeycloakUserId()).To(BeEmpty())
			})
		})

		Context("when user has no username", func() {
			It("should not attempt lookup", func() {
				user := privatev1.User_builder{
					Id: "user-123",
					Metadata: privatev1.Metadata_builder{
						Name:       "testuser",
						Tenant:     "test-tenant",
						Finalizers: []string{finalizers.Controller},
					}.Build(),
					Spec: privatev1.UserSpec_builder{
						// No username
						Email: "test@example.com",
					}.Build(),
					Status: privatev1.UserStatus_builder{}.Build(),
				}.Build()

				// Should not call GetUserByUsername
				// No mock expectations set

				task := &task{
					r:    function,
					user: user,
				}

				err := task.reconcile(ctx)
				Expect(err).ToNot(HaveOccurred())
				Expect(user.GetStatus().GetKeycloakUserId()).To(BeEmpty())
			})
		})

		Context("when user has no tenant", func() {
			It("should not attempt lookup", func() {
				user := privatev1.User_builder{
					Id: "user-123",
					Metadata: privatev1.Metadata_builder{
						Name:       "testuser",
						Finalizers: []string{finalizers.Controller},
						// No tenant
					}.Build(),
					Spec: privatev1.UserSpec_builder{
						Username: "testuser",
						Email:    "test@example.com",
					}.Build(),
					Status: privatev1.UserStatus_builder{}.Build(),
				}.Build()

				// Should not call GetUserByUsername
				// No mock expectations set

				task := &task{
					r:    function,
					user: user,
				}

				err := task.reconcile(ctx)
				Expect(err).ToNot(HaveOccurred())
				Expect(user.GetStatus().GetKeycloakUserId()).To(BeEmpty())
			})
		})

		Context("when user has no metadata", func() {
			It("should add metadata with finalizer", func() {
				user := privatev1.User_builder{
					Id: "user-123",
					// No metadata
					Spec: privatev1.UserSpec_builder{
						Username: "testuser",
						Email:    "test@example.com",
					}.Build(),
					Status: privatev1.UserStatus_builder{}.Build(),
				}.Build()

				task := &task{
					r:    function,
					user: user,
				}

				err := task.reconcile(ctx)
				Expect(err).ToNot(HaveOccurred())
				Expect(user.HasMetadata()).To(BeTrue())
				Expect(user.GetMetadata().GetFinalizers()).To(ContainElement(finalizers.Controller))
			})
		})
	})

	Describe("Delete logic", func() {
		Context("when user has keycloak_user_id", func() {
			It("should delete user from IDP and remove finalizer", func() {
				now := timestamppb.New(time.Now())
				user := privatev1.User_builder{
					Id: "user-123",
					Metadata: privatev1.Metadata_builder{
						Name:              "testuser",
						Tenant:            "test-tenant",
						DeletionTimestamp: now,
						Finalizers:        []string{finalizers.Controller},
					}.Build(),
					Spec: privatev1.UserSpec_builder{
						Username: "testuser",
						Email:    "test@example.com",
					}.Build(),
					Status: privatev1.UserStatus_builder{
						KeycloakUserId: "keycloak-user-id-456",
					}.Build(),
				}.Build()

				mockClient.EXPECT().
					DeleteUser(ctx, "test-tenant", "keycloak-user-id-456").
					Return(nil)

				task := &task{
					r:    function,
					user: user,
				}

				err := task.delete(ctx)
				Expect(err).ToNot(HaveOccurred())
				Expect(user.GetMetadata().GetFinalizers()).ToNot(ContainElement(finalizers.Controller))
			})
		})

		Context("when IDP deletion fails", func() {
			It("should return error and keep finalizer", func() {
				now := timestamppb.New(time.Now())
				user := privatev1.User_builder{
					Id: "user-123",
					Metadata: privatev1.Metadata_builder{
						Name:              "testuser",
						Tenant:            "test-tenant",
						DeletionTimestamp: now,
						Finalizers:        []string{finalizers.Controller},
					}.Build(),
					Spec: privatev1.UserSpec_builder{
						Username: "testuser",
						Email:    "test@example.com",
					}.Build(),
					Status: privatev1.UserStatus_builder{
						KeycloakUserId: "keycloak-user-id-456",
					}.Build(),
				}.Build()

				mockClient.EXPECT().
					DeleteUser(ctx, "test-tenant", "keycloak-user-id-456").
					Return(errors.New("IDP connection failed"))

				task := &task{
					r:    function,
					user: user,
				}

				err := task.delete(ctx)
				Expect(err).To(HaveOccurred())
				Expect(user.GetMetadata().GetFinalizers()).To(ContainElement(finalizers.Controller))
			})
		})

		Context("when user has no keycloak_user_id", func() {
			It("should look up user by username and delete from IDP", func() {
				now := timestamppb.New(time.Now())
				user := privatev1.User_builder{
					Id: "user-123",
					Metadata: privatev1.Metadata_builder{
						Name:              "testuser",
						Tenant:            "test-tenant",
						DeletionTimestamp: now,
						Finalizers:        []string{finalizers.Controller},
					}.Build(),
					Spec: privatev1.UserSpec_builder{
						Username: "testuser",
						Email:    "test@example.com",
					}.Build(),
					Status: privatev1.UserStatus_builder{}.Build(),
				}.Build()

				gomock.InOrder(
					mockClient.EXPECT().
						GetUserByUsername(ctx, "test-tenant", "testuser").
						Return(&idp.User{
							ID:       "resolved-keycloak-id",
							Username: "testuser",
						}, nil),
					mockClient.EXPECT().
						DeleteUser(ctx, "test-tenant", "resolved-keycloak-id").
						Return(nil),
				)

				task := &task{
					r:    function,
					user: user,
				}

				err := task.delete(ctx)
				Expect(err).ToNot(HaveOccurred())
				Expect(user.GetMetadata().GetFinalizers()).ToNot(ContainElement(finalizers.Controller))
			})

			It("should remove finalizer when user not found in IDP", func() {
				now := timestamppb.New(time.Now())
				user := privatev1.User_builder{
					Id: "user-123",
					Metadata: privatev1.Metadata_builder{
						Name:              "testuser",
						Tenant:            "test-tenant",
						DeletionTimestamp: now,
						Finalizers:        []string{finalizers.Controller},
					}.Build(),
					Spec: privatev1.UserSpec_builder{
						Username: "testuser",
						Email:    "test@example.com",
					}.Build(),
					Status: privatev1.UserStatus_builder{}.Build(),
				}.Build()

				mockClient.EXPECT().
					GetUserByUsername(ctx, "test-tenant", "testuser").
					Return(nil, nil)

				task := &task{
					r:    function,
					user: user,
				}

				err := task.delete(ctx)
				Expect(err).ToNot(HaveOccurred())
				Expect(user.GetMetadata().GetFinalizers()).ToNot(ContainElement(finalizers.Controller))
			})

			It("should return error when IDP lookup fails", func() {
				now := timestamppb.New(time.Now())
				user := privatev1.User_builder{
					Id: "user-123",
					Metadata: privatev1.Metadata_builder{
						Name:              "testuser",
						Tenant:            "test-tenant",
						DeletionTimestamp: now,
						Finalizers:        []string{finalizers.Controller},
					}.Build(),
					Spec: privatev1.UserSpec_builder{
						Username: "testuser",
						Email:    "test@example.com",
					}.Build(),
					Status: privatev1.UserStatus_builder{}.Build(),
				}.Build()

				mockClient.EXPECT().
					GetUserByUsername(ctx, "test-tenant", "testuser").
					Return(nil, errors.New("IDP connection failed"))

				task := &task{
					r:    function,
					user: user,
				}

				err := task.delete(ctx)
				Expect(err).To(HaveOccurred())
				Expect(user.GetMetadata().GetFinalizers()).To(ContainElement(finalizers.Controller))
			})
		})

		Context("when user has no tenant", func() {
			It("should skip IDP deletion and remove finalizer", func() {
				now := timestamppb.New(time.Now())
				user := privatev1.User_builder{
					Id: "user-123",
					Metadata: privatev1.Metadata_builder{
						Name:              "testuser",
						DeletionTimestamp: now,
						Finalizers:        []string{finalizers.Controller},
					}.Build(),
					Spec: privatev1.UserSpec_builder{
						Username: "testuser",
						Email:    "test@example.com",
					}.Build(),
					Status: privatev1.UserStatus_builder{
						KeycloakUserId: "keycloak-user-id-456",
					}.Build(),
				}.Build()

				task := &task{
					r:    function,
					user: user,
				}

				err := task.delete(ctx)
				Expect(err).ToNot(HaveOccurred())
				Expect(user.GetMetadata().GetFinalizers()).ToNot(ContainElement(finalizers.Controller))
			})
		})
	})
})

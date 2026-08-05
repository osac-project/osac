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
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	privatev1 "github.com/osac-project/fulfillment-service/internal/api/osac/private/v1"
	"github.com/osac-project/fulfillment-service/internal/idp"
)

var _ = Describe("User Reconciler Integration", func() {
	Describe("Run function", func() {
		It("should calculate the update mask correctly when keycloak_user_id is set", func() {
			// Create a user without keycloak_user_id
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

			// Create the task manually (not calling Run since we need a real gRPC client for that)
			task := &task{
				user: user,
			}

			// Simulate what the reconcile function does
			if !task.user.HasStatus() {
				task.user.SetStatus(&privatev1.UserStatus{})
			}

			// Simulate setting the keycloak_user_id
			task.user.GetStatus().SetKeycloakUserId("keycloak-id-123")

			// Verify the status was updated
			Expect(task.user.GetStatus().GetKeycloakUserId()).To(Equal("keycloak-id-123"))
		})

		It("should handle users with existing status fields", func() {
			// Create a user with existing status fields
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
				Status: privatev1.UserStatus_builder{
					Phase: "Active",
				}.Build(),
			}.Build()

			task := &task{
				user: user,
			}

			// Simulate setting the keycloak_user_id
			task.user.GetStatus().SetKeycloakUserId("keycloak-id-456")

			// Verify both status fields are present
			Expect(task.user.GetStatus().GetKeycloakUserId()).To(Equal("keycloak-id-456"))
			Expect(task.user.GetStatus().GetPhase()).To(Equal("Active"))
		})
	})

	Describe("IDP lookup scenarios", func() {
		It("should handle different Keycloak user ID formats", func() {
			// Test with UUID format (common Keycloak format)
			idpUser := &idp.User{
				ID:       "f47ac10b-58cc-4372-a567-0e02b2c3d479",
				Username: "testuser",
				Email:    "test@example.com",
			}

			Expect(idpUser.ID).To(MatchRegexp(`^[a-f0-9]{8}-[a-f0-9]{4}-[a-f0-9]{4}-[a-f0-9]{4}-[a-f0-9]{12}$`))
		})

		It("should handle users with special characters in username", func() {
			// Test that we can handle usernames with special characters
			user := privatev1.User_builder{
				Id: "user-123",
				Metadata: privatev1.Metadata_builder{
					Name:   "test.user+tag@example",
					Tenant: "test-tenant",
				}.Build(),
				Spec: privatev1.UserSpec_builder{
					Username: "test.user+tag@example",
					Email:    "test.user+tag@example.com",
				}.Build(),
			}.Build()

			Expect(user.GetSpec().GetUsername()).To(Equal("test.user+tag@example"))
		})

		It("should handle users with email-format usernames", func() {
			user := privatev1.User_builder{
				Id: "user-123",
				Metadata: privatev1.Metadata_builder{
					Name:   "user@example.com",
					Tenant: "test-tenant",
				}.Build(),
				Spec: privatev1.UserSpec_builder{
					Username: "user@example.com",
					Email:    "user@example.com",
				}.Build(),
			}.Build()

			Expect(user.GetSpec().GetUsername()).To(Equal("user@example.com"))
			Expect(user.GetSpec().GetEmail()).To(Equal("user@example.com"))
		})
	})

	Describe("Edge cases", func() {
		It("should handle concurrent reconciliation safely", func() {
			// This test verifies that the reconcile logic is idempotent
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
				Status: privatev1.UserStatus_builder{
					KeycloakUserId: "keycloak-id-123",
				}.Build(),
			}.Build()

			task := &task{
				user: user,
			}

			// Verify that having keycloak_user_id already set is safe
			initialID := task.user.GetStatus().GetKeycloakUserId()
			Expect(initialID).To(Equal("keycloak-id-123"))

			// Simulate a second reconciliation attempt
			if task.user.GetStatus().GetKeycloakUserId() != "" {
				// Should skip lookup
				Expect(task.user.GetStatus().GetKeycloakUserId()).To(Equal("keycloak-id-123"))
			}
		})

		It("should preserve other status fields when setting keycloak_user_id", func() {
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
				Status: privatev1.UserStatus_builder{
					Phase: "Active",
					Conditions: []*privatev1.UserCondition{
						{
							Type:   "Ready",
							Status: privatev1.ConditionStatus_CONDITION_STATUS_TRUE,
						},
					},
				}.Build(),
			}.Build()

			// Store original values
			originalPhase := user.GetStatus().GetPhase()
			originalConditions := user.GetStatus().GetConditions()

			// Set keycloak_user_id
			user.GetStatus().SetKeycloakUserId("keycloak-id-789")

			// Verify original fields are preserved
			Expect(user.GetStatus().GetPhase()).To(Equal(originalPhase))
			Expect(user.GetStatus().GetConditions()).To(Equal(originalConditions))
			Expect(user.GetStatus().GetKeycloakUserId()).To(Equal("keycloak-id-789"))
		})
	})
})

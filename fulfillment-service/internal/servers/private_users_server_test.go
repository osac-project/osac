/*
Copyright (c) 2026 Red Hat Inc.

Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except in compliance with the
License. You may obtain a copy of the License at

  http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on an
"AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the specific
language governing permissions and limitations under the License.
*/

package servers

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	privatev1 "github.com/osac-project/fulfillment-service/internal/api/osac/private/v1"
)

var _ = Describe("Users Server", func() {
	var privateServer *PrivateUsersServer

	BeforeEach(func() {
		var err error

		// Create server (without notifier for testing):
		privateServer, err = NewPrivateUsersServer().
			SetLogger(logger).
			SetAttributionLogic(attribution).
			SetTenancyLogic(tenancy).
			Build()
		Expect(err).ToNot(HaveOccurred())
	})

	It("Creates a user", func() {
		// Create request:
		request := &privatev1.UsersCreateRequest{
			Object: &privatev1.User{
				Metadata: &privatev1.Metadata{
					Name: "test-user",
				},
				Spec: &privatev1.UserSpec{
					Username: "testuser",
					Email:    "test@example.com",
					Enabled:  true,
				},
			},
		}

		// Create user:
		response, err := privateServer.Create(ctx, request)
		Expect(err).ToNot(HaveOccurred())
		Expect(response).ToNot(BeNil())
		Expect(response.Object).ToNot(BeNil())
		Expect(response.Object.Id).ToNot(BeEmpty())
		Expect(response.Object.Metadata.Name).To(Equal("test-user"))
		Expect(response.Object.Spec.Username).To(Equal("testuser"))
	})

	It("Lists users", func() {
		// Create a user first:
		createReq := &privatev1.UsersCreateRequest{
			Object: &privatev1.User{
				Metadata: &privatev1.Metadata{
					Name: "test-user",
				},
				Spec: &privatev1.UserSpec{
					Username: "testuser",
					Email:    "test@example.com",
				},
			},
		}
		_, err := privateServer.Create(ctx, createReq)
		Expect(err).ToNot(HaveOccurred())

		// List users:
		listResp, err := privateServer.List(ctx, &privatev1.UsersListRequest{})
		Expect(err).ToNot(HaveOccurred())
		Expect(listResp.Size).To(Equal(int32(1)))
		Expect(listResp.Items).To(HaveLen(1))
		Expect(listResp.Items[0].Metadata.Name).To(Equal("test-user"))
	})

	It("Gets a user by ID", func() {
		// Create a user:
		createReq := &privatev1.UsersCreateRequest{
			Object: &privatev1.User{
				Metadata: &privatev1.Metadata{
					Name: "test-user",
				},
				Spec: &privatev1.UserSpec{
					Username: "testuser",
					Email:    "test@example.com",
				},
			},
		}
		createResp, err := privateServer.Create(ctx, createReq)
		Expect(err).ToNot(HaveOccurred())

		// Get the user:
		getResp, err := privateServer.Get(ctx, &privatev1.UsersGetRequest{
			Id: createResp.Object.Id,
		})
		Expect(err).ToNot(HaveOccurred())
		Expect(getResp.Object.Id).To(Equal(createResp.Object.Id))
		Expect(getResp.Object.Metadata.Name).To(Equal("test-user"))
	})

	It("Deletes a user", func() {
		// Create a user:
		createReq := &privatev1.UsersCreateRequest{
			Object: &privatev1.User{
				Metadata: &privatev1.Metadata{
					Name: "test-user",
				},
				Spec: &privatev1.UserSpec{
					Username: "testuser",
					Email:    "test@example.com",
				},
			},
		}
		createResp, err := privateServer.Create(ctx, createReq)
		Expect(err).ToNot(HaveOccurred())

		// Delete the user:
		_, err = privateServer.Delete(ctx, &privatev1.UsersDeleteRequest{
			Id: createResp.Object.Id,
		})
		Expect(err).ToNot(HaveOccurred())
	})

	It("Prunes credentials from listed users", func() {
		// Create a user with credentials:
		password := "secret123"
		_, err := privateServer.Create(ctx, &privatev1.UsersCreateRequest{
			Object: &privatev1.User{
				Metadata: &privatev1.Metadata{
					Name: "cred-user",
				},
				Spec: &privatev1.UserSpec{
					Username: "creduser",
					Email:    "cred@example.com",
					Credentials: &privatev1.UserCredentials{
						Password: &password,
					},
				},
			},
		})
		Expect(err).ToNot(HaveOccurred())

		// List and verify credentials are pruned:
		listResp, err := privateServer.List(ctx, &privatev1.UsersListRequest{})
		Expect(err).ToNot(HaveOccurred())
		Expect(listResp.Items).To(HaveLen(1))
		Expect(listResp.Items[0].Spec.HasCredentials()).To(BeFalse())
	})

	It("Preserves credentials in fetched user", func() {
		// Create a user with credentials:
		password := "secret123"
		createResp, err := privateServer.Create(ctx, &privatev1.UsersCreateRequest{
			Object: &privatev1.User{
				Metadata: &privatev1.Metadata{
					Name: "cred-user",
				},
				Spec: &privatev1.UserSpec{
					Username: "creduser",
					Email:    "cred@example.com",
					Credentials: &privatev1.UserCredentials{
						Password: &password,
					},
				},
			},
		})
		Expect(err).ToNot(HaveOccurred())

		// Get and verify credentials are preserved so controllers can access them:
		getResp, err := privateServer.Get(ctx, &privatev1.UsersGetRequest{
			Id: createResp.Object.Id,
		})
		Expect(err).ToNot(HaveOccurred())
		Expect(getResp.Object.Spec.HasCredentials()).To(BeTrue())
	})

	It("Preserves credentials in updated user", func() {
		// Create a user with credentials:
		password := "secret123"
		createResp, err := privateServer.Create(ctx, &privatev1.UsersCreateRequest{
			Object: &privatev1.User{
				Metadata: &privatev1.Metadata{
					Name: "cred-user",
				},
				Spec: &privatev1.UserSpec{
					Username: "creduser",
					Email:    "cred@example.com",
				},
			},
		})
		Expect(err).ToNot(HaveOccurred())

		// Update with credentials and verify they are preserved so controllers can access them:
		updateResp, err := privateServer.Update(ctx, &privatev1.UsersUpdateRequest{
			Object: &privatev1.User{
				Id: createResp.Object.Id,
				Spec: &privatev1.UserSpec{
					Email: "updated@example.com",
					Credentials: &privatev1.UserCredentials{
						Password: &password,
					},
				},
			},
		})
		Expect(err).ToNot(HaveOccurred())
		Expect(updateResp.Object.Spec.HasCredentials()).To(BeTrue())
	})

	It("Updates a user", func() {
		// Create a user:
		createReq := &privatev1.UsersCreateRequest{
			Object: &privatev1.User{
				Metadata: &privatev1.Metadata{
					Name: "test-user",
				},
				Spec: &privatev1.UserSpec{
					Username: "testuser",
					Email:    "test@example.com",
				},
			},
		}
		createResp, err := privateServer.Create(ctx, createReq)
		Expect(err).ToNot(HaveOccurred())

		// Update the user:
		updateReq := &privatev1.UsersUpdateRequest{
			Object: &privatev1.User{
				Id: createResp.Object.Id,
				Spec: &privatev1.UserSpec{
					Email: "updated@example.com",
				},
			},
		}
		updateResp, err := privateServer.Update(ctx, updateReq)
		Expect(err).ToNot(HaveOccurred())
		Expect(updateResp.Object.Spec.Email).To(Equal("updated@example.com"))
	})
})

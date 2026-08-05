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
	"fmt"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	publicv1 "github.com/osac-project/fulfillment-service/internal/api/osac/public/v1"
)

var _ = Describe("Public Users Server", func() {
	var publicServer *UsersServer

	BeforeEach(func() {
		var err error

		// Create public server:
		publicServer, err = NewUsersServer().
			SetLogger(logger).
			SetAttributionLogic(attribution).
			SetTenancyLogic(tenancy).
			Build()
		Expect(err).ToNot(HaveOccurred())
	})

	It("Creates a user", func() {
		// Create request:
		request := &publicv1.UsersCreateRequest{
			Object: &publicv1.User{
				Metadata: &publicv1.Metadata{
					Name: "test-user",
				},
				Spec: &publicv1.UserSpec{
					Username: "testuser",
					Email:    "test@example.com",
					Enabled:  true,
				},
			},
		}

		// Create user:
		response, err := publicServer.Create(ctx, request)
		Expect(err).ToNot(HaveOccurred())
		Expect(response).ToNot(BeNil())
		Expect(response.Object).ToNot(BeNil())
		Expect(response.Object.Id).ToNot(BeEmpty())
		Expect(response.Object.Metadata.Name).To(Equal("test-user"))
		Expect(response.Object.Spec.Username).To(Equal("testuser"))
	})

	It("Prunes credentials from created user", func() {
		password := "secret123"
		response, err := publicServer.Create(ctx, &publicv1.UsersCreateRequest{
			Object: &publicv1.User{
				Metadata: &publicv1.Metadata{
					Name: "cred-user",
				},
				Spec: &publicv1.UserSpec{
					Username: "creduser",
					Email:    "cred@example.com",
					Credentials: &publicv1.UserCredentials{
						Password: &password,
					},
				},
			},
		})
		Expect(err).ToNot(HaveOccurred())
		Expect(response.Object.Spec.HasCredentials()).To(BeFalse())
	})

	It("Lists users", func() {
		// Create a user first:
		createReq := &publicv1.UsersCreateRequest{
			Object: &publicv1.User{
				Metadata: &publicv1.Metadata{
					Name: "test-user",
				},
				Spec: &publicv1.UserSpec{
					Username: "testuser",
					Email:    "test@example.com",
				},
			},
		}
		_, err := publicServer.Create(ctx, createReq)
		Expect(err).ToNot(HaveOccurred())

		// List users:
		listResp, err := publicServer.List(ctx, &publicv1.UsersListRequest{})
		Expect(err).ToNot(HaveOccurred())
		Expect(listResp.Size).To(Equal(int32(1)))
		Expect(listResp.Items).To(HaveLen(1))
		Expect(listResp.Items[0].Metadata.Name).To(Equal("test-user"))
	})

	It("Gets a user by ID", func() {
		// Create a user:
		createReq := &publicv1.UsersCreateRequest{
			Object: &publicv1.User{
				Metadata: &publicv1.Metadata{
					Name: "test-user",
				},
				Spec: &publicv1.UserSpec{
					Username: "testuser",
					Email:    "test@example.com",
				},
			},
		}
		createResp, err := publicServer.Create(ctx, createReq)
		Expect(err).ToNot(HaveOccurred())

		// Get the user:
		getResp, err := publicServer.Get(ctx, &publicv1.UsersGetRequest{
			Id: createResp.Object.Id,
		})
		Expect(err).ToNot(HaveOccurred())
		Expect(getResp.Object.Id).To(Equal(createResp.Object.Id))
		Expect(getResp.Object.Metadata.Name).To(Equal("test-user"))
	})

	It("Deletes a user", func() {
		// Create a user:
		createReq := &publicv1.UsersCreateRequest{
			Object: &publicv1.User{
				Metadata: &publicv1.Metadata{
					Name: "test-user",
				},
				Spec: &publicv1.UserSpec{
					Username: "testuser",
					Email:    "test@example.com",
				},
			},
		}
		createResp, err := publicServer.Create(ctx, createReq)
		Expect(err).ToNot(HaveOccurred())

		// Delete the user:
		_, err = publicServer.Delete(ctx, &publicv1.UsersDeleteRequest{
			Id: createResp.Object.Id,
		})
		Expect(err).ToNot(HaveOccurred())
	})

	It("Updates a user", func() {
		// Create a user:
		createReq := &publicv1.UsersCreateRequest{
			Object: &publicv1.User{
				Metadata: &publicv1.Metadata{
					Name: "test-user",
				},
				Spec: &publicv1.UserSpec{
					Username: "testuser",
					Email:    "test@example.com",
				},
			},
		}
		createResp, err := publicServer.Create(ctx, createReq)
		Expect(err).ToNot(HaveOccurred())

		// Update the user:
		updateReq := &publicv1.UsersUpdateRequest{
			Object: &publicv1.User{
				Id: createResp.Object.Id,
				Spec: &publicv1.UserSpec{
					Email: "updated@example.com",
				},
			},
		}
		updateResp, err := publicServer.Update(ctx, updateReq)
		Expect(err).ToNot(HaveOccurred())
		Expect(updateResp.Object.Spec.Email).To(Equal("updated@example.com"))
	})

	It("Prunes credentials from listed users", func() {
		// Create a user with credentials:
		password := "secret123"
		_, err := publicServer.Create(ctx, &publicv1.UsersCreateRequest{
			Object: &publicv1.User{
				Metadata: &publicv1.Metadata{
					Name: "cred-user",
				},
				Spec: &publicv1.UserSpec{
					Username: "creduser",
					Email:    "cred@example.com",
					Credentials: &publicv1.UserCredentials{
						Password: &password,
					},
				},
			},
		})
		Expect(err).ToNot(HaveOccurred())

		// List and verify credentials are pruned:
		listResp, err := publicServer.List(ctx, &publicv1.UsersListRequest{})
		Expect(err).ToNot(HaveOccurred())
		Expect(listResp.Items).To(HaveLen(1))
		Expect(listResp.Items[0].Spec.HasCredentials()).To(BeFalse())
	})

	It("Prunes credentials from fetched user", func() {
		// Create a user with credentials:
		password := "secret123"
		createResp, err := publicServer.Create(ctx, &publicv1.UsersCreateRequest{
			Object: &publicv1.User{
				Metadata: &publicv1.Metadata{
					Name: "cred-user",
				},
				Spec: &publicv1.UserSpec{
					Username: "creduser",
					Email:    "cred@example.com",
					Credentials: &publicv1.UserCredentials{
						Password: &password,
					},
				},
			},
		})
		Expect(err).ToNot(HaveOccurred())

		// Get and verify credentials are pruned:
		getResp, err := publicServer.Get(ctx, &publicv1.UsersGetRequest{
			Id: createResp.Object.Id,
		})
		Expect(err).ToNot(HaveOccurred())
		Expect(getResp.Object.Spec.HasCredentials()).To(BeFalse())
	})

	It("Prunes credentials from updated user", func() {
		// Create a user with credentials:
		password := "secret123"
		createResp, err := publicServer.Create(ctx, &publicv1.UsersCreateRequest{
			Object: &publicv1.User{
				Metadata: &publicv1.Metadata{
					Name: "cred-user",
				},
				Spec: &publicv1.UserSpec{
					Username: "creduser",
					Email:    "cred@example.com",
				},
			},
		})
		Expect(err).ToNot(HaveOccurred())

		// Update with credentials and verify they are pruned from the response:
		updateResp, err := publicServer.Update(ctx, &publicv1.UsersUpdateRequest{
			Object: &publicv1.User{
				Id: createResp.Object.Id,
				Spec: &publicv1.UserSpec{
					Email: "updated@example.com",
					Credentials: &publicv1.UserCredentials{
						Password: &password,
					},
				},
			},
		})
		Expect(err).ToNot(HaveOccurred())
		Expect(updateResp.Object.Spec.HasCredentials()).To(BeFalse())
	})

	It("Lists users filtered by username", func() {
		// Create users with different username prefixes:
		for i := range 2 {
			_, err := publicServer.Create(ctx, &publicv1.UsersCreateRequest{
				Object: &publicv1.User{
					Metadata: &publicv1.Metadata{
						Name: fmt.Sprintf("group-a-user-%d", i),
					},
					Spec: &publicv1.UserSpec{
						Username: fmt.Sprintf("groupa-user-%d", i),
						Email:    fmt.Sprintf("user-%d@group-a.com", i),
					},
				},
			})
			Expect(err).ToNot(HaveOccurred())
		}
		for i := range 3 {
			_, err := publicServer.Create(ctx, &publicv1.UsersCreateRequest{
				Object: &publicv1.User{
					Metadata: &publicv1.Metadata{
						Name: fmt.Sprintf("group-b-user-%d", i),
					},
					Spec: &publicv1.UserSpec{
						Username: fmt.Sprintf("groupb-user-%d", i),
						Email:    fmt.Sprintf("user-%d@group-b.com", i),
					},
				},
			})
			Expect(err).ToNot(HaveOccurred())
		}

		// List users filtering by username prefix "groupa-":
		listResp, err := publicServer.List(ctx, publicv1.UsersListRequest_builder{
			Filter: new("this.spec.username.startsWith('groupa-')"),
		}.Build())
		Expect(err).ToNot(HaveOccurred())
		Expect(listResp.GetSize()).To(Equal(int32(2)))
		Expect(listResp.GetItems()).To(HaveLen(2))
		for _, item := range listResp.GetItems() {
			Expect(item.GetSpec().GetUsername()).To(HavePrefix("groupa-"))
		}

		// List users filtering by username prefix "groupb-":
		listResp, err = publicServer.List(ctx, publicv1.UsersListRequest_builder{
			Filter: new("this.spec.username.startsWith('groupb-')"),
		}.Build())
		Expect(err).ToNot(HaveOccurred())
		Expect(listResp.GetSize()).To(Equal(int32(3)))
		Expect(listResp.GetItems()).To(HaveLen(3))
		for _, item := range listResp.GetItems() {
			Expect(item.GetSpec().GetUsername()).To(HavePrefix("groupb-"))
		}
	})
})

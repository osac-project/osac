/*
Copyright (c) 2026 Red Hat Inc.

Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except in compliance with the
License. You may obtain a copy of the License at

  http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on an
"AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the specific
language governing permissions and limitations under the License.
*/

package config

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	. "github.com/onsi/ginkgo/v2/dsl/core"
	. "github.com/onsi/gomega"
	"github.com/zalando/go-keyring"

	"github.com/osac-project/fulfillment-service/internal/logging"
)

var _ = Describe("Secret store", func() {
	var ctx context.Context

	// mySecret is a simple struct that used to test the secret store.
	type mySecret struct {
		User     string `json:"user,omitempty"`
		Password string `json:"password,omitempty"`
	}

	BeforeEach(func() {
		// Create the context:
		ctx = logging.LoggerIntoContext(context.Background(), logger)
	})

	Describe("Store selection", func() {
		It("Selects the keyring store when the keyring is available", func() {
			keyring.MockInit()
			store, err := NewSecretStore().
				SetLogger(logger).
				SetDir("/some/dir").
				Build()
			Expect(err).ToNot(HaveOccurred())
			Expect(store).To(BeAssignableToTypeOf(&keyringSecretStore{}))
		})

		It("Falls back to the file store when the keyring is not available", func() {
			keyring.MockInitWithError(fmt.Errorf("keyring backend not available"))
			store, err := NewSecretStore().
				SetLogger(logger).
				SetDir("/some/dir").
				Build()
			Expect(err).ToNot(HaveOccurred())
			Expect(store).To(BeAssignableToTypeOf(&fileSecretStore{}))
		})

		It("Passes the directory to the file store when the keyring is not available", func() {
			// Create the store using a temporary directory::
			keyring.MockInitWithError(fmt.Errorf("keyring backend not available"))
			tmp, err := os.MkdirTemp("", "*.test")
			Expect(err).ToNot(HaveOccurred())
			defer os.RemoveAll(tmp)
			store, err := NewSecretStore().
				SetLogger(logger).
				SetDir(tmp).
				Build()
			Expect(err).ToNot(HaveOccurred())

			// Save a secret to force the creation of the secrets file:
			err = store.Save(ctx, &mySecret{
				User:     "my-user",
				Password: "my-pass",
			})
			Expect(err).ToNot(HaveOccurred())

			// Check that the secrets file was created in the temporary directory::
			file := filepath.Join(tmp, "secrets.json")
			Expect(file).To(BeAnExistingFile())
		})

		It("Passes the directory to the keyring store when the keyring is available", func() {
			// Create the store using a temporary directory:
			keyring.MockInit()
			tmp, err := os.MkdirTemp("", "*.test")
			Expect(err).ToNot(HaveOccurred())
			store, err := NewSecretStore().
				SetLogger(logger).
				SetDir(tmp).
				Build()
			Expect(err).ToNot(HaveOccurred())

			// Save a secret to force the creation of the secrets file:
			err = store.Save(ctx, &mySecret{
				User:     "my-user",
				Password: "my-pass",
			})
			Expect(err).ToNot(HaveOccurred())

			// Check that the secret was saved to the keyring under the key 'secrets:...':
			data, err := keyring.Get("osac", "secrets:"+tmp)
			Expect(err).ToNot(HaveOccurred())
			Expect(data).To(MatchJSON(`{
				"user": "my-user",
				"password": "my-pass"
			}`))
		})
	})

	Describe("Keyring store", func() {
		var store SecretStore

		BeforeEach(func() {
			var err error

			// Make sure that the keyring is available:
			keyring.MockInit()

			// Create the store
			store, err = NewKeyringSecretStore().
				SetLogger(logger).
				SetDir("/my/config").
				Build()
			Expect(err).ToNot(HaveOccurred())
		})

		It("Fails if directory is empty", func() {
			_, err := NewKeyringSecretStore().
				SetLogger(logger).
				Build()
			Expect(err).To(HaveOccurred())
		})

		It("Saves data", func() {
			// Save the secret:
			secret := &mySecret{
				User:     "my-user",
				Password: "my-pass",
			}
			err := store.Save(ctx, secret)
			Expect(err).ToNot(HaveOccurred())

			// Verify that the data has been saved to the keyring:
			data, err := keyring.Get("osac", "secrets:/my/config")
			Expect(err).ToNot(HaveOccurred())
			Expect(data).To(MatchJSON(`{
				"user": "my-user",
				"password": "my-pass"
			}`))
		})

		It("Loads data", func() {
			// Save the data to the keyring directly:
			err := keyring.Set("osac", "secrets:/my/config", `{
				"user": "my-user",
				"password": "my-pass"
			}`)
			Expect(err).ToNot(HaveOccurred())

			// Load the data:
			var secret mySecret
			err = store.Load(ctx, &secret)
			Expect(err).ToNot(HaveOccurred())
			Expect(secret.User).To(Equal("my-user"))
			Expect(secret.Password).To(Equal("my-pass"))
		})

		It("Clears data when saving nil", func() {
			// Save the secret:
			secret := &mySecret{
				User:     "my-user",
				Password: "my-pass",
			}
			err := store.Save(ctx, secret)
			Expect(err).ToNot(HaveOccurred())

			// Save again, with nil:
			err = store.Save(ctx, nil)
			Expect(err).ToNot(HaveOccurred())

			// Verify that the keyring is empty:
			_, err = keyring.Get("osac", "secrets:/my/config")
			Expect(err).To(MatchError(keyring.ErrNotFound))

		})

		It("Does not fail when clearing an already empty store", func() {
			err := store.Save(ctx, nil)
			Expect(err).ToNot(HaveOccurred())
		})
	})

	Describe("Keyring store isolation", func() {
		BeforeEach(func() {
			keyring.MockInit()
		})

		It("Isolates secrets between different directories", func() {
			storeA, err := NewKeyringSecretStore().
				SetLogger(logger).
				SetDir("/dir-a").
				Build()
			Expect(err).ToNot(HaveOccurred())

			storeB, err := NewKeyringSecretStore().
				SetLogger(logger).
				SetDir("/dir-b").
				Build()
			Expect(err).ToNot(HaveOccurred())

			err = storeA.Save(ctx, &mySecret{User: "user-a", Password: "pass-a"})
			Expect(err).ToNot(HaveOccurred())

			err = storeB.Save(ctx, &mySecret{User: "user-b", Password: "pass-b"})
			Expect(err).ToNot(HaveOccurred())

			var secretA mySecret
			err = storeA.Load(ctx, &secretA)
			Expect(err).ToNot(HaveOccurred())
			Expect(secretA.User).To(Equal("user-a"))

			var secretB mySecret
			err = storeB.Load(ctx, &secretB)
			Expect(err).ToNot(HaveOccurred())
			Expect(secretB.User).To(Equal("user-b"))
		})
	})

	Describe("File store", func() {
		Context("With store using a temporary directory", func() {
			var (
				dir   string
				file  string
				store SecretStore
			)

			BeforeEach(func() {
				var err error

				// Create a temporary directory:
				dir, err = os.MkdirTemp("", "*.test")
				Expect(err).ToNot(HaveOccurred())
				DeferCleanup(os.RemoveAll, dir)

				// Calculate the name of the file:
				file = filepath.Join(dir, "secrets.json")

				// Create the store:
				store, err = NewFileSecretStore().
					SetLogger(logger).
					SetDir(dir).
					Build()
				Expect(err).ToNot(HaveOccurred())
			})

			It("Loads data", func() {
				// Save the data to the file directly:
				err := os.WriteFile(
					file,
					[]byte(`{
						"user": "my-user",
						"password": "my-pass"
					}`),
					0600,
				)
				Expect(err).ToNot(HaveOccurred())

				// Load the data:
				var secret mySecret
				err = store.Load(ctx, &secret)
				Expect(err).ToNot(HaveOccurred())
				Expect(secret.User).To(Equal("my-user"))
				Expect(secret.Password).To(Equal("my-pass"))
			})

			It("Saves data", func() {
				// Save the data:
				secret := &mySecret{
					User:     "my-user",
					Password: "my-pass",
				}
				err := store.Save(ctx, secret)
				Expect(err).ToNot(HaveOccurred())

				// Verify that the data has been saved to the file:
				data, err := os.ReadFile(file)
				Expect(err).ToNot(HaveOccurred())
				Expect(data).To(MatchJSON(`{
					"user": "my-user",
					"password": "my-pass"
				}`))
			})

			It("Sets restrictive permissions on the secrets file", func() {
				// Save data to force the creation of the file:
				secret := &mySecret{
					User:     "my-user",
					Password: "my-pass",
				}
				err := store.Save(ctx, secret)
				Expect(err).ToNot(HaveOccurred())

				// Verify that the file has restrictive permissions:
				info, err := os.Stat(file)
				Expect(err).ToNot(HaveOccurred())
				Expect(info.Mode().Perm()).To(Equal(os.FileMode(0600)))
			})

			It("Removes the secrets file when saving nil", func() {
				// Save data to force the creation of the file:
				secret := &mySecret{
					User:     "my-user",
					Password: "my-pass",
				}
				err := store.Save(ctx, secret)
				Expect(err).ToNot(HaveOccurred())
				Expect(file).To(BeAnExistingFile())

				// Save nil to remove the file:
				err = store.Save(ctx, nil)
				Expect(err).ToNot(HaveOccurred())
				Expect(file).ToNot(BeAnExistingFile())
			})
		})

		It("Creates the directory if it does not exist", func() {
			// Create the store using a directory that doesn't exist:
			tmp, err := os.MkdirTemp("", "*.test")
			Expect(err).ToNot(HaveOccurred())
			defer os.RemoveAll(tmp)
			dir := filepath.Join(tmp, "store")
			store, err := NewFileSecretStore().
				SetLogger(logger).
				SetDir(dir).
				Build()
			Expect(err).ToNot(HaveOccurred())

			// Save data to force the creation of the directory:
			secret := &mySecret{
				User:     "my-user",
				Password: "my-pass",
			}
			err = store.Save(ctx, secret)
			Expect(err).ToNot(HaveOccurred())

			// Verify that the directory has been created:
			info, err := os.Stat(dir)
			Expect(err).ToNot(HaveOccurred())
			Expect(info.IsDir()).To(BeTrue())
		})
	})
})

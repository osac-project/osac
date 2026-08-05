/*
Copyright (c) 2025 Red Hat Inc.

Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except in compliance with the
License. You may obtain a copy of the License at

  http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on an
"AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the specific
language governing permissions and limitations under the License.
*/

package auth

import (
	"context"
	"time"

	. "github.com/onsi/ginkgo/v2/dsl/core"
	. "github.com/onsi/gomega"
)

var _ = Describe("Memory token store", func() {
	var ctx context.Context

	BeforeEach(func() {
		ctx = context.Background()
	})

	It("Fails to build without logger", func() {
		store, err := NewMemoryTokenStore().
			Build()
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring("logger is mandatory"))
		Expect(store).To(BeNil())
	})

	It("Returns nil when no token is stored", func() {
		store, err := NewMemoryTokenStore().
			SetLogger(logger).
			Build()
		Expect(err).ToNot(HaveOccurred())
		token, err := store.Load(ctx)
		Expect(err).ToNot(HaveOccurred())
		Expect(token).To(BeNil())
	})

	It("Saves and loads a token", func() {
		// Create the store:
		store, err := NewMemoryTokenStore().
			SetLogger(logger).
			Build()
		Expect(err).ToNot(HaveOccurred())

		// Save a token:
		original := &Token{
			Access:  "my-access-token",
			Refresh: "my-refresh-token",
			Expiry:  time.Now().Add(1 * time.Hour),
		}
		err = store.Save(ctx, original)
		Expect(err).ToNot(HaveOccurred())

		// Load the token:
		loaded, err := store.Load(ctx)
		Expect(err).ToNot(HaveOccurred())
		Expect(loaded).ToNot(BeNil())
		Expect(loaded.Access).To(Equal(original.Access))
		Expect(loaded.Refresh).To(Equal(original.Refresh))
		Expect(loaded.Expiry).To(Equal(original.Expiry))
	})

	It("Fails to save a nil token", func() {
		store, err := NewMemoryTokenStore().
			SetLogger(logger).
			Build()
		Expect(err).ToNot(HaveOccurred())
		err = store.Save(ctx, nil)
		Expect(err).To(HaveOccurred())
		Expect(err).To(MatchError("token cannot be nil"))
	})

	It("Clones token on save to prevent side effects", func() {
		// Create the store:
		store, err := NewMemoryTokenStore().
			SetLogger(logger).
			Build()
		Expect(err).ToNot(HaveOccurred())

		// Create and save a token:
		original := &Token{
			Access:  "my-access-token",
			Refresh: "my-refresh-token",
			Expiry:  time.Now().Add(1 * time.Hour),
		}
		err = store.Save(ctx, original)
		Expect(err).ToNot(HaveOccurred())

		// Modify the original token:
		original.Access = "modified-access-token"
		original.Refresh = "modified-refresh-token"

		// Load the token and verify it wasn't affected:
		loaded, err := store.Load(ctx)
		Expect(err).ToNot(HaveOccurred())
		Expect(loaded).ToNot(BeNil())
		Expect(loaded.Access).To(Equal("my-access-token"))
		Expect(loaded.Refresh).To(Equal("my-refresh-token"))
	})

	It("Clones token on load to prevent side effects", func() {
		// Create the store:
		store, err := NewMemoryTokenStore().
			SetLogger(logger).
			Build()
		Expect(err).ToNot(HaveOccurred())

		// Save a token:
		original := &Token{
			Access:  "my-access-token",
			Refresh: "my-refresh-token",
			Expiry:  time.Now().Add(1 * time.Hour),
		}
		err = store.Save(ctx, original)
		Expect(err).ToNot(HaveOccurred())

		// Load the token:
		loaded, err := store.Load(ctx)
		Expect(err).ToNot(HaveOccurred())
		Expect(loaded).ToNot(BeNil())

		// Modify the loaded token:
		loaded.Access = "modified-access-token"
		loaded.Refresh = "modified-refresh-token"

		// Load the token again and verify it wasn't affected:
		loaded, err = store.Load(ctx)
		Expect(err).ToNot(HaveOccurred())
		Expect(loaded).ToNot(BeNil())
		Expect(loaded.Access).To(Equal("my-access-token"))
		Expect(loaded.Refresh).To(Equal("my-refresh-token"))
	})

	It("Overwrites existing token when saving", func() {
		// Create the store:
		store, err := NewMemoryTokenStore().
			SetLogger(logger).
			Build()
		Expect(err).ToNot(HaveOccurred())

		// Save the first token:
		first := &Token{
			Access:  "first-access-token",
			Refresh: "first-refresh-token",
			Expiry:  time.Now().Add(1 * time.Hour),
		}
		err = store.Save(ctx, first)
		Expect(err).ToNot(HaveOccurred())

		// Save the second token:
		second := &Token{
			Access:  "second-access-token",
			Refresh: "second-refresh-token",
			Expiry:  time.Now().Add(2 * time.Hour),
		}
		err = store.Save(ctx, second)
		Expect(err).ToNot(HaveOccurred())

		// Load the token and verify it's the second one:
		loaded, err := store.Load(ctx)
		Expect(err).ToNot(HaveOccurred())
		Expect(loaded).ToNot(BeNil())
		Expect(loaded.Access).To(Equal(second.Access))
		Expect(loaded.Refresh).To(Equal(second.Refresh))
		Expect(loaded.Expiry).To(Equal(second.Expiry))
	})
})

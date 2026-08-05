/*
Copyright (c) 2025 Red Hat Inc.

Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except in compliance with the
License. You may obtain a copy of the License at

  http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on an
"AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the specific
language governing permissions and limitations under the License.
*/

package controllers

import (
	"context"
	"errors"
	"log/slog"
	"sync"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"go.uber.org/mock/gomock"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"k8s.io/apimachinery/pkg/runtime"

	privatev1 "github.com/osac-project/fulfillment-service/internal/api/osac/private/v1"
)

var _ = Describe("HubCache", func() {
	var (
		ctrl       *gomock.Controller
		logger     *slog.Logger
		scheme     *runtime.Scheme
		ctx        context.Context
		mockClient *MockHubsClient
	)

	BeforeEach(func() {
		ctrl = gomock.NewController(GinkgoT())
		DeferCleanup(ctrl.Finish)

		logger = slog.Default()
		scheme = runtime.NewScheme()
		ctx = context.Background()
		mockClient = NewMockHubsClient(ctrl)
	})

	Describe("Get", func() {
		Context("when gRPC returns NotFound", func() {
			It("should return ErrHubNotFound", func() {
				mockClient.EXPECT().
					Get(gomock.Any(), gomock.Any()).
					Return(nil, status.Error(codes.NotFound, "hub not found in database"))

				cache := &hubCache{
					logger:      logger,
					client:      mockClient,
					scheme:      scheme,
					entries:     map[string]*cachedHubEntry{},
					entriesLock: &sync.Mutex{},
					ttl:         DefaultHubCacheTTL,
				}

				_, err := cache.Get(ctx, "test-hub-id")

				Expect(err).To(HaveOccurred())
				Expect(errors.Is(err, ErrHubNotFound)).To(BeTrue())
			})
		})

		Context("when gRPC returns transient error", func() {
			It("should return original error without classification", func() {
				mockClient.EXPECT().
					Get(gomock.Any(), gomock.Any()).
					Return(nil, status.Error(codes.Unavailable, "connection refused"))

				cache := &hubCache{
					logger:      logger,
					client:      mockClient,
					scheme:      scheme,
					entries:     map[string]*cachedHubEntry{},
					entriesLock: &sync.Mutex{},
					ttl:         DefaultHubCacheTTL,
				}

				_, err := cache.Get(ctx, "test-hub-id")

				Expect(err).To(HaveOccurred())
				Expect(errors.Is(err, ErrHubNotFound)).To(BeFalse())

				// Verify it's the Unavailable error
				st, ok := status.FromError(err)
				Expect(ok).To(BeTrue())
				Expect(st.Code()).To(Equal(codes.Unavailable))
			})
		})

		Context("when entry exists in cache", func() {
			It("should return cached entry without calling gRPC client", func() {
				// Mock should NEVER be called because entry is pre-cached
				// (no EXPECT set, so any call will fail the test)

				// Pre-populate cache with an entry
				cachedEntry := &cachedHubEntry{
					HubEntry: &HubEntry{
						Namespace: "cached-namespace",
						Client:    nil, // Not used in this test
					},
					createdAt: time.Now(),
				}

				cache := &hubCache{
					logger:      logger,
					client:      mockClient,
					scheme:      scheme,
					entries:     map[string]*cachedHubEntry{"cached-hub": cachedEntry},
					entriesLock: &sync.Mutex{},
					ttl:         DefaultHubCacheTTL,
				}

				entry, err := cache.Get(ctx, "cached-hub")

				Expect(err).ToNot(HaveOccurred())
				Expect(entry.Namespace).To(Equal("cached-namespace"))
				Expect(entry).To(BeIdenticalTo(cachedEntry.HubEntry))
			})
		})

		Context("when create() fails", func() {
			It("should not cache errors and retry on next call", func() {
				// Mock will be called twice because errors aren't cached
				mockClient.EXPECT().
					Get(gomock.Any(), gomock.Any()).
					Return(&privatev1.HubsGetResponse{
						Object: privatev1.Hub_builder{
							Spec: privatev1.HubSpec_builder{
								Namespace:  "test-namespace",
								Kubeconfig: []byte("invalid-kubeconfig"),
							}.Build(),
						}.Build(),
					}, nil).
					Times(2) // Called twice - errors aren't cached

				cache := &hubCache{
					logger:      logger,
					client:      mockClient,
					scheme:      scheme,
					entries:     map[string]*cachedHubEntry{},
					entriesLock: &sync.Mutex{},
					ttl:         DefaultHubCacheTTL,
				}

				// First call hits mock, fails on kubeconfig parsing, NOT cached
				_, err1 := cache.Get(ctx, "test-hub-id")
				Expect(err1).To(HaveOccurred())

				// Second call hits mock again (errors not cached)
				_, err2 := cache.Get(ctx, "test-hub-id")
				Expect(err2).To(HaveOccurred())

				// gomock will verify Times(2) - proving errors aren't cached
			})
		})

		Context("TTL expiration", func() {
			It("should evict expired entries and attempt to recreate them", func() {
				ttl := 50 * time.Millisecond

				// Pre-populate cache with an entry that has already expired
				expiredEntry := &cachedHubEntry{
					HubEntry: &HubEntry{
						Namespace: "old-namespace",
						Client:    nil,
					},
					createdAt: time.Now().Add(-ttl - 20*time.Millisecond),
				}

				// When Get() detects the expired entry, it will try to recreate it.
				// We return NotFound to avoid kubeconfig parsing issues in the test.
				mockClient.EXPECT().
					Get(gomock.Any(), gomock.Any()).
					Return(nil, status.Error(codes.NotFound, "hub not found")).
					Times(1)

				cache := &hubCache{
					logger:      logger,
					client:      mockClient,
					scheme:      scheme,
					entries:     map[string]*cachedHubEntry{"test-hub-id": expiredEntry},
					entriesLock: &sync.Mutex{},
					ttl:         ttl,
				}

				// Get() should detect the expired entry, evict it, and try to recreate it
				_, err := cache.Get(ctx, "test-hub-id")
				Expect(errors.Is(err, ErrHubNotFound)).To(BeTrue())

				// Verify the expired entry was evicted from cache
				cache.entriesLock.Lock()
				_, stillCached := cache.entries["test-hub-id"]
				cache.entriesLock.Unlock()
				Expect(stillCached).To(BeFalse(), "expired entry should be evicted and error not cached")

				// gomock will verify Times(1) - proving the recreation attempt was made
			})
		})
	})
})

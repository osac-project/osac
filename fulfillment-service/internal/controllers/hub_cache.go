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

	"google.golang.org/grpc"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/tools/clientcmd"
	clnt "sigs.k8s.io/controller-runtime/pkg/client"

	privatev1 "github.com/osac-project/fulfillment-service/internal/api/osac/private/v1"
)

const (
	// DefaultHubCacheTTL is the default time-to-live for cached hub entries.
	DefaultHubCacheTTL = 5 * time.Minute
)

// HubCache is the interface for accessing hub connections. Consumers should
// depend on this interface to allow mocking in unit tests.
//
//go:generate mockgen -destination=hub_cache_mock.go -package=controllers . HubCache
//go:generate mockgen -source=../api/osac/private/v1/hubs_service_grpc.pb.go -destination=hubs_client_mock.go -package=controllers HubsClient
type HubCache interface {
	Get(ctx context.Context, id string) (*HubEntry, error)
}

// HubCacheBuilder contains the data and logic needed to build a cache of hub client.
type HubCacheBuilder struct {
	logger     *slog.Logger
	connection *grpc.ClientConn
	scheme     *runtime.Scheme
	ttl        time.Duration
}

// hubCache caches the information and connections to the hubs.
type hubCache struct {
	logger      *slog.Logger
	client      privatev1.HubsClient
	scheme      *runtime.Scheme
	entries     map[string]*cachedHubEntry
	entriesLock *sync.Mutex
	ttl         time.Duration
}

type HubEntry struct {
	Namespace string
	Client    clnt.Client
}

// cachedHubEntry wraps HubEntry with TTL tracking.
type cachedHubEntry struct {
	*HubEntry
	createdAt time.Time
}

// NewHubCache creates a new builder that can then be used to create a new cluster order reconciler function.
func NewHubCache() *HubCacheBuilder {
	return &HubCacheBuilder{}
}

// SetLogger sets the logger. This is mandatory.
func (b *HubCacheBuilder) SetLogger(value *slog.Logger) *HubCacheBuilder {
	b.logger = value
	return b
}

// SetConnection sets the gRPC client connection. This is mandatory.
func (b *HubCacheBuilder) SetConnection(value *grpc.ClientConn) *HubCacheBuilder {
	b.connection = value
	return b
}

// SetScheme sets the Kubernetes scheme used for typed object support. This is mandatory.
func (b *HubCacheBuilder) SetScheme(value *runtime.Scheme) *HubCacheBuilder {
	b.scheme = value
	return b
}

// SetTTL sets the time-to-live for cached hub entries. Optional, defaults to DefaultHubCacheTTL.
func (b *HubCacheBuilder) SetTTL(value time.Duration) *HubCacheBuilder {
	b.ttl = value
	return b
}

// Build uses the information stored in the buidler to create a new hub client cache.
func (b *HubCacheBuilder) Build() (result HubCache, err error) {
	// Check parameters:
	if b.logger == nil {
		err = errors.New("logger is mandatory")
		return
	}
	if b.connection == nil {
		err = errors.New("gRPC connection is mandatory")
		return
	}
	if b.scheme == nil {
		err = errors.New("scheme is mandatory")
		return
	}

	// Set default TTL if not specified:
	ttl := b.ttl
	if ttl == 0 {
		ttl = DefaultHubCacheTTL
	}

	// Create and populate the object:
	result = &hubCache{
		logger:      b.logger,
		client:      privatev1.NewHubsClient(b.connection),
		scheme:      b.scheme,
		entries:     map[string]*cachedHubEntry{},
		entriesLock: &sync.Mutex{},
		ttl:         ttl,
	}
	return
}

func (r *hubCache) Get(ctx context.Context, id string) (result *HubEntry, err error) {
	r.entriesLock.Lock()
	defer r.entriesLock.Unlock()

	cached, ok := r.entries[id]
	if ok {
		// Check if entry has expired
		if time.Since(cached.createdAt) < r.ttl {
			return cached.HubEntry, nil
		}
		// Entry expired, delete and recreate
		delete(r.entries, id)
	}

	hubEntry, err := r.create(ctx, id)
	if err != nil {
		// Classify the error to distinguish decommissioned hubs (NotFound)
		// from transient failures (network, timeout, etc.)
		err = ClassifyHubError(err)
		return
	}

	r.entries[id] = &cachedHubEntry{
		HubEntry:  hubEntry,
		createdAt: time.Now(),
	}

	return hubEntry, nil
}

func (r *hubCache) create(ctx context.Context, id string) (result *HubEntry, err error) {
	response, err := r.client.Get(ctx, privatev1.HubsGetRequest_builder{
		Id: id,
	}.Build())
	if err != nil {
		return
	}
	hub := response.GetObject()
	config, err := clientcmd.RESTConfigFromKubeConfig(hub.GetSpec().GetKubeconfig())
	if err != nil {
		return
	}
	client, err := clnt.New(config, clnt.Options{Scheme: r.scheme})
	if err != nil {
		return
	}
	result = &HubEntry{
		Namespace: hub.GetSpec().GetNamespace(),
		Client:    client,
	}
	return
}

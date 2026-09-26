/*
Copyright (c) 2026 Red Hat Inc.

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
	"fmt"
	"sync"
	"time"

	"golang.org/x/sync/singleflight"
	"google.golang.org/grpc"
	clnt "sigs.k8s.io/controller-runtime/pkg/client"

	privatev1 "github.com/osac-project/osac/proto/gen/osac/private/v1"
)

var (
	ErrNoNetworkClass          = errors.New("no active network class")
	ErrMultipleNetworkClasses  = errors.New("multiple active network classes")
	ErrNoNetworkingHubs        = errors.New("no active networking hubs")
	ErrMultipleNetworkingHubs  = errors.New("multiple active networking hubs")
	ErrCanonicalHubNotFound    = errors.New("canonical networking hub not found")
	ErrCanonicalHubNotReady    = errors.New("canonical networking hub is not ready")
	ErrCanonicalHubUnavailable = errors.New("canonical networking hub unavailable")
)

const (
	canonicalHubNegativeCacheTTL    = time.Second
	activeResourceFilter            = "!has(this.metadata.deletion_timestamp)"
	activeResourceLimit             = 2
	canonicalHubNoCandidatesMessage = "expected exactly one active networking hub, found none"
	canonicalHubMultipleMessage     = "expected exactly one active networking hub, found multiple"
)

type networkClassesClient interface {
	List(ctx context.Context, in *privatev1.NetworkClassesListRequest, opts ...grpc.CallOption) (*privatev1.NetworkClassesListResponse, error)
}

// NetworkClassStatusClient is the status-only client owned by the NetworkClass reconciler.
type NetworkClassStatusClient interface {
	Update(ctx context.Context, in *privatev1.NetworkClassesUpdateRequest, opts ...grpc.CallOption) (*privatev1.NetworkClassesUpdateResponse, error)
}

type hubsListClient interface {
	List(ctx context.Context, in *privatev1.HubsListRequest, opts ...grpc.CallOption) (*privatev1.HubsListResponse, error)
}

// NetworkClassHubResolver resolves the canonical networking Hub and returns the status that the
// NetworkClass reconciler must persist. It never writes NetworkClass status itself.
type NetworkClassHubResolver interface {
	Resolve(ctx context.Context) (NetworkingHubResolution, error)
}

// NetworkingHubReader resolves the already-persisted canonical networking Hub for consumers.
// Readers never mutate NetworkClass status; the NetworkClass reconciler owns that lifecycle.
type NetworkingHubReader interface {
	Resolve(ctx context.Context) (NetworkingHubResolution, error)
}

// NetworkingHub is the stable Hub selection and the already-resolved client.
type NetworkingHub struct {
	ID        string
	Namespace string
	Client    clnt.Client
}

// NetworkingHubResolution contains the resolved Hub and the status outcome owned by the
// NetworkClass reconciler.
type NetworkingHubResolution struct {
	NetworkingHub
	HubID   string
	State   privatev1.NetworkClassState
	Message string
}

// NetworkingHubResolverBuilder contains the dependencies needed to construct a canonical Hub resolver.
type NetworkingHubResolverBuilder struct {
	networkClassesClient networkClassesClient
	hubsClient           hubsListClient
	hubCache             HubCache
}

// NetworkingHubReaderBuilder contains the dependencies needed to construct a read-only canonical Hub reader.
type NetworkingHubReaderBuilder struct {
	networkClassesClient networkClassesClient
	hubCache             HubCache
}

type networkingHubResolver struct {
	networkClassesClient networkClassesClient
	hubsClient           hubsListClient
	hubCache             HubCache
	mu                   sync.Mutex
	resolveGroup         singleflight.Group
	cachedHub            *NetworkingHub
	cachedError          error
	errorExpiresAt       time.Time
	readOnly             bool
}

// NewNetworkingHubResolver creates a builder for a canonical networking Hub resolver.
func NewNetworkingHubResolver() *NetworkingHubResolverBuilder {
	return &NetworkingHubResolverBuilder{}
}

// SetNetworkClassesClient sets the private NetworkClass client.
func (b *NetworkingHubResolverBuilder) SetNetworkClassesClient(value networkClassesClient) *NetworkingHubResolverBuilder {
	b.networkClassesClient = value
	return b
}

// SetHubsClient sets the private Hub list client.
func (b *NetworkingHubResolverBuilder) SetHubsClient(value hubsListClient) *NetworkingHubResolverBuilder {
	b.hubsClient = value
	return b
}

// SetHubCache sets the cache used to resolve a Hub's Kubernetes client.
func (b *NetworkingHubResolverBuilder) SetHubCache(value HubCache) *NetworkingHubResolverBuilder {
	b.hubCache = value
	return b
}

// Build validates the dependencies and creates a canonical networking Hub resolver.
func (b *NetworkingHubResolverBuilder) Build() (NetworkClassHubResolver, error) {
	if b.networkClassesClient == nil {
		return nil, errors.New("network classes client is mandatory")
	}
	if b.hubsClient == nil {
		return nil, errors.New("hubs client is mandatory")
	}
	if b.hubCache == nil {
		return nil, errors.New("hub cache is mandatory")
	}
	return &networkingHubResolver{
		networkClassesClient: b.networkClassesClient,
		hubsClient:           b.hubsClient,
		hubCache:             b.hubCache,
	}, nil
}

// NewNetworkingHubReader creates a builder for a read-only canonical networking Hub reader.
func NewNetworkingHubReader() *NetworkingHubReaderBuilder {
	return &NetworkingHubReaderBuilder{}
}

// SetNetworkClassesClient sets the private NetworkClass client.
func (b *NetworkingHubReaderBuilder) SetNetworkClassesClient(value networkClassesClient) *NetworkingHubReaderBuilder {
	b.networkClassesClient = value
	return b
}

// SetHubCache sets the cache used to resolve a Hub's Kubernetes client.
func (b *NetworkingHubReaderBuilder) SetHubCache(value HubCache) *NetworkingHubReaderBuilder {
	b.hubCache = value
	return b
}

// Build validates the dependencies and creates a read-only canonical networking Hub reader.
func (b *NetworkingHubReaderBuilder) Build() (NetworkingHubReader, error) {
	if b.networkClassesClient == nil {
		return nil, errors.New("network classes client is mandatory")
	}
	if b.hubCache == nil {
		return nil, errors.New("hub cache is mandatory")
	}
	return &networkingHubResolver{
		networkClassesClient: b.networkClassesClient,
		hubCache:             b.hubCache,
		readOnly:             true,
	}, nil
}

func (r *networkingHubResolver) Resolve(ctx context.Context) (NetworkingHubResolution, error) {
	// The NetworkClass reconciler must re-evaluate the active Hub set on every
	// reconciliation. Hub create/delete events trigger that reconciliation, so
	// caching its discovery result would allow a stale canonical assignment to
	// survive a topology change. Read-only resource consumers can cache the
	// already-persisted canonical reference because they never discover or
	// select a Hub.
	if r.readOnly {
		if result, ok := r.cachedResolution(ctx); ok {
			return NetworkingHubResolution{
				NetworkingHub: result,
				HubID:         result.ID,
				State:         privatev1.NetworkClassState_NETWORK_CLASS_STATE_READY,
			}, nil
		}
		if err, ok := r.cachedFailure(); ok {
			return NetworkingHubResolution{}, err
		}
	}

	value, err, _ := r.resolveGroup.Do("canonical-networking-hub", func() (any, error) {
		if r.readOnly {
			if result, ok := r.cachedResolution(ctx); ok {
				return NetworkingHubResolution{
					NetworkingHub: result,
					HubID:         result.ID,
					State:         privatev1.NetworkClassState_NETWORK_CLASS_STATE_READY,
				}, nil
			}
			if err, ok := r.cachedFailure(); ok {
				return NetworkingHubResolution{}, err
			}
		}

		result, err := r.resolve(ctx)
		if r.readOnly {
			r.cacheResult(result.NetworkingHub, err)
		}
		return result, err
	})
	if err != nil {
		if result, ok := value.(NetworkingHubResolution); ok {
			return result, err
		}
		return NetworkingHubResolution{}, err
	}
	return value.(NetworkingHubResolution), nil
}

func (r *networkingHubResolver) cachedResolution(ctx context.Context) (NetworkingHub, bool) {
	r.mu.Lock()
	var cached NetworkingHub
	if r.cachedHub != nil {
		cached = *r.cachedHub
	}
	r.mu.Unlock()
	if cached.ID == "" {
		return NetworkingHub{}, false
	}

	entry, err := r.hubCache.Get(ctx, cached.ID)
	if err == nil && entry != nil {
		return NetworkingHub{ID: cached.ID, Namespace: entry.Namespace, Client: entry.Client}, true
	}

	r.mu.Lock()
	if r.cachedHub != nil && r.cachedHub.ID == cached.ID {
		r.cachedHub = nil
	}
	r.mu.Unlock()
	return NetworkingHub{}, false
}

func (r *networkingHubResolver) cachedFailure() (error, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.cachedError == nil {
		return nil, false
	}
	if time.Now().Before(r.errorExpiresAt) {
		return r.cachedError, true
	}
	r.cachedError = nil
	r.errorExpiresAt = time.Time{}
	return nil, false
}

func (r *networkingHubResolver) cacheResult(result NetworkingHub, err error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if err == nil {
		r.cachedHub = &result
		r.cachedError = nil
		r.errorExpiresAt = time.Time{}
		return
	}
	r.cachedHub = nil
	r.cachedError = err
	r.errorExpiresAt = time.Now().Add(canonicalHubNegativeCacheTTL)
}

func (r *networkingHubResolver) resolve(ctx context.Context) (NetworkingHubResolution, error) {
	networkClass, err := r.findNetworkClass(ctx)
	if err != nil {
		return NetworkingHubResolution{}, err
	}

	canonicalHubID := networkClass.GetStatus().GetHub()
	if canonicalHubID != "" {
		return r.resolveCanonicalHub(ctx, canonicalHubID)
	}
	if r.readOnly {
		return NetworkingHubResolution{
			State:   privatev1.NetworkClassState_NETWORK_CLASS_STATE_PENDING,
			Message: ErrCanonicalHubNotReady.Error(),
		}, ErrCanonicalHubNotReady
	}

	hub, err := r.findOnlyHub(ctx)
	if err != nil {
		return NetworkingHubResolution{
			State:   privatev1.NetworkClassState_NETWORK_CLASS_STATE_PENDING,
			Message: canonicalHubErrorMessage(err),
		}, err
	}
	return r.resolveCanonicalHub(ctx, hub.GetId())
}

func (r *networkingHubResolver) findNetworkClass(ctx context.Context) (*privatev1.NetworkClass, error) {
	filter := activeResourceFilter
	limit := int32(activeResourceLimit)
	response, err := r.networkClassesClient.List(ctx, privatev1.NetworkClassesListRequest_builder{
		Filter: &filter,
		Limit:  &limit,
	}.Build())
	if err != nil {
		return nil, fmt.Errorf("failed to list network classes: %w", err)
	}
	if response == nil {
		return nil, errors.New("network classes list returned an empty response")
	}

	return findOnlyActive(
		response.GetItems(),
		response.GetTotal(),
		func(networkClass *privatev1.NetworkClass) bool {
			return networkClass.GetMetadata().HasDeletionTimestamp()
		},
		ErrNoNetworkClass,
		func(count int) error { return fmt.Errorf("%w: found %d", ErrMultipleNetworkClasses, count) },
	)
}

func (r *networkingHubResolver) findOnlyHub(ctx context.Context) (*privatev1.Hub, error) {
	filter := activeResourceFilter
	limit := int32(activeResourceLimit)
	response, err := r.hubsClient.List(ctx, privatev1.HubsListRequest_builder{
		Filter: &filter,
		Limit:  &limit,
	}.Build())
	if err != nil {
		return nil, fmt.Errorf("failed to list networking hubs: %w", err)
	}
	if response == nil {
		return nil, errors.New("networking hubs list returned an empty response")
	}

	hub, err := findOnlyActive(
		response.GetItems(),
		response.GetTotal(),
		func(hub *privatev1.Hub) bool { return hub.GetMetadata().HasDeletionTimestamp() },
		ErrNoNetworkingHubs,
		func(int) error { return ErrMultipleNetworkingHubs },
	)
	if err != nil {
		return nil, err
	}
	if hub.GetId() == "" {
		return nil, fmt.Errorf("%w: candidate Hub has no identifier", ErrCanonicalHubNotFound)
	}
	return hub, nil
}

func findOnlyActive[T any](
	items []*T,
	total int32,
	isDeleting func(*T) bool,
	noItemsErr error,
	multipleItemsErr func(int) error,
) (*T, error) {
	active := make([]*T, 0, len(items))
	for _, item := range items {
		if item != nil && !isDeleting(item) {
			active = append(active, item)
		}
	}

	if total > 1 {
		return nil, multipleItemsErr(int(total))
	}

	switch len(active) {
	case 0:
		return nil, noItemsErr
	case 1:
		return active[0], nil
	default:
		return nil, multipleItemsErr(len(active))
	}
}

func (r *networkingHubResolver) resolveCanonicalHub(
	ctx context.Context,
	hubID string,
) (NetworkingHubResolution, error) {
	entry, err := r.hubCache.Get(ctx, hubID)
	if err != nil {
		state := privatev1.NetworkClassState_NETWORK_CLASS_STATE_PENDING
		kind := ErrCanonicalHubUnavailable
		if errors.Is(err, ErrHubNotFound) {
			state = privatev1.NetworkClassState_NETWORK_CLASS_STATE_FAILED
			kind = ErrCanonicalHubNotFound
		}
		return r.canonicalHubFailure(hubID, state, kind, err)
	}
	if entry == nil {
		return r.canonicalHubFailure(
			hubID,
			privatev1.NetworkClassState_NETWORK_CLASS_STATE_PENDING,
			ErrCanonicalHubUnavailable,
			errors.New("hub cache returned an empty entry"),
		)
	}

	return NetworkingHubResolution{
		NetworkingHub: NetworkingHub{ID: hubID, Namespace: entry.Namespace, Client: entry.Client},
		HubID:         hubID,
		State:         privatev1.NetworkClassState_NETWORK_CLASS_STATE_READY,
	}, nil
}

func (r *networkingHubResolver) canonicalHubFailure(
	hubID string,
	state privatev1.NetworkClassState,
	kind error,
	err error,
) (NetworkingHubResolution, error) {
	message := fmt.Sprintf("canonical networking hub %q is unavailable", hubID)
	if errors.Is(kind, ErrCanonicalHubNotFound) {
		message = fmt.Sprintf("canonical networking hub %q is not registered", hubID)
	}
	return NetworkingHubResolution{HubID: hubID, State: state, Message: message}, fmt.Errorf("%w: %q: %w", kind, hubID, err)
}

func canonicalHubErrorMessage(err error) string {
	switch {
	case errors.Is(err, ErrNoNetworkingHubs):
		return canonicalHubNoCandidatesMessage
	case errors.Is(err, ErrMultipleNetworkingHubs):
		return canonicalHubMultipleMessage
	default:
		return err.Error()
	}
}

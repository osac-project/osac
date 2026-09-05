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
	"context"
	"errors"
	"sync"
	"time"

	"golang.org/x/sync/singleflight"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
	clnt "sigs.k8s.io/controller-runtime/pkg/client"

	privatev1 "github.com/osac-project/osac/fulfillment-service/internal/api/osac/private/v1"
	"github.com/osac-project/osac/fulfillment-service/internal/auth"
	"github.com/osac-project/osac/fulfillment-service/internal/controllers"
	"github.com/osac-project/osac/fulfillment-service/internal/database/dao"
	"github.com/osac-project/osac/fulfillment-service/internal/vault"
)

//go:generate mockgen -destination=hub_lookup_mock.go -package=servers . HubLookup
//go:generate mockgen -destination=hub_client_provider_mock.go -package=servers . HubClientProvider

// hubLookupWithDAO implements HubLookup using a DAO for secret resolution.
// This avoids circular dependencies between HubClientProvider and SecretsServer.
type hubLookupWithDAO struct {
	hubServer   privatev1.HubsServer
	secretsDAO  *dao.GenericDAO[*privatev1.Secret]
	secretStore vault.SecretStore
}

// NewHubLookupWithDAO creates a HubLookup backed by the private Hubs server.
// The secret store is used to hydrate Vault-backed secret data, which isn't persisted in the
// metadata DAO. This keeps the lookup independent of the Secrets server and avoids a dependency
// cycle with the hub secret fetcher.
func NewHubLookupWithDAO(hubServer privatev1.HubsServer, secretsDAO *dao.GenericDAO[*privatev1.Secret], secretStore vault.SecretStore) HubLookup {
	return &hubLookupWithDAO{
		hubServer:   hubServer,
		secretsDAO:  secretsDAO,
		secretStore: secretStore,
	}
}

func (l *hubLookupWithDAO) GetKubeconfig(ctx context.Context, hubID string) (kubeconfig []byte, namespace string, err error) {
	hubResp, err := l.hubServer.Get(ctx, privatev1.HubsGetRequest_builder{
		Id: hubID,
	}.Build())
	if err != nil {
		return nil, "", err
	}
	hub := hubResp.GetObject()

	// Use ResolveHubKubeconfig for dual-path resolution (inline or secret-based)
	kubeconfig, err = controllers.ResolveHubKubeconfig(ctx, hub.GetSpec(), l.getSecret)
	if err != nil {
		return nil, "", err
	}

	return kubeconfig, hub.GetSpec().GetNamespace(), nil
}

func (l *hubLookupWithDAO) getSecret(ctx context.Context, id string) (*privatev1.Secret, error) {
	// The caller is already authorized to retrieve the hub-backed Secret. Hub credentials are
	// platform-scoped shared Secrets, so resolve this implementation detail using shared visibility
	// rather than the caller's tenant visibility.
	lookupCtx := auth.ContextWithSubject(ctx, &auth.Subject{
		User:    auth.SystemTenant,
		Tenants: auth.SharedTenants,
	})
	resp, err := l.secretsDAO.Get().SetId(id).Do(lookupCtx)
	if err != nil {
		return nil, err
	}
	secret := resp.GetObject()
	if l.secretStore != nil && secret.GetBackend() == privatev1.SecretBackend_SECRET_BACKEND_VAULT {
		metadata := secret.GetMetadata()
		data, err := l.secretStore.Fetch(lookupCtx, metadata.GetTenant(), metadata.GetProject(), metadata.GetName())
		if err != nil {
			return nil, vault.ToGrpcError(err)
		}
		secret.SetData(data)
	}
	return secret, nil
}

// HubClientInfo holds the cached Kubernetes client and associated hub metadata.
type HubClientInfo struct {
	Client    clnt.Client
	Config    *rest.Config
	Namespace string
}

// HubClientProvider manages cached Kubernetes clients for hub clusters.
type HubClientProvider interface {
	GetClient(ctx context.Context, hubID string) (*HubClientInfo, error)
	EvictClient(hubID string)
}

// HubClientProviderBuilder contains the data and logic needed to create a hub client provider. Don't create instances of
// this type directly, use the NewHubClientProvider function instead.
type HubClientProviderBuilder struct {
	hubLookup        HubLookup
	hubClientFactory HubClientFactory
}

type hubClientProvider struct {
	hubLookup        HubLookup
	hubClientFactory HubClientFactory
	mu               sync.RWMutex
	clients          map[string]*HubClientInfo
	group            singleflight.Group
}

// NewHubClientProvider creates a builder that can then be used to configure and create a new hub client provider.
func NewHubClientProvider() *HubClientProviderBuilder {
	return &HubClientProviderBuilder{}
}

// SetHubLookup sets the hub lookup used to resolve hub kubeconfigs. This is mandatory.
func (b *HubClientProviderBuilder) SetHubLookup(value HubLookup) *HubClientProviderBuilder {
	b.hubLookup = value
	return b
}

// SetHubClientFactory sets the factory used to create Kubernetes clients from hub kubeconfigs. This is mandatory.
func (b *HubClientProviderBuilder) SetHubClientFactory(value HubClientFactory) *HubClientProviderBuilder {
	b.hubClientFactory = value
	return b
}

// Build uses the data stored in the builder to create and configure a new hub client provider.
func (b *HubClientProviderBuilder) Build() (HubClientProvider, error) {
	if b.hubLookup == nil {
		return nil, errors.New("hub lookup is mandatory")
	}
	if b.hubClientFactory == nil {
		return nil, errors.New("hub client factory is mandatory")
	}
	return &hubClientProvider{
		hubLookup:        b.hubLookup,
		hubClientFactory: b.hubClientFactory,
		clients:          make(map[string]*HubClientInfo),
	}, nil
}

func (p *hubClientProvider) GetClient(ctx context.Context, hubID string) (*HubClientInfo, error) {
	// Always validate authorization first, even for cached clients
	kubeconfig, namespace, err := p.hubLookup.GetKubeconfig(ctx, hubID)
	if err != nil {
		return nil, classifyHubLookupError(err, hubID)
	}

	if namespace == "" {
		return nil, status.Errorf(codes.Internal, "hub %q returned an empty namespace", hubID)
	}

	p.mu.RLock()
	if info, ok := p.clients[hubID]; ok {
		p.mu.RUnlock()
		return info, nil
	}
	p.mu.RUnlock()

	ctx = context.WithoutCancel(ctx)
	// Apply timeout to prevent indefinite blocking.
	// All goroutines waiting in the singleflight group will receive the timeout error.
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()

	resultCh := p.group.DoChan(hubID, func() (any, error) {
		p.mu.RLock()
		if info, ok := p.clients[hubID]; ok {
			p.mu.RUnlock()
			return info, nil
		}
		p.mu.RUnlock()

		config, err := clientcmd.RESTConfigFromKubeConfig(kubeconfig)
		if err != nil {
			return nil, status.Errorf(codes.Internal, "failed to parse kubeconfig for hub %q", hubID)
		}

		client, err := p.hubClientFactory(config)
		if err != nil {
			return nil, status.Errorf(codes.Internal, "failed to create client for hub %q", hubID)
		}

		info := &HubClientInfo{
			Client:    client,
			Config:    config,
			Namespace: namespace,
		}

		p.mu.Lock()
		p.clients[hubID] = info
		p.mu.Unlock()

		return info, nil
	})

	select {
	case <-ctx.Done():
		return nil, status.Errorf(codes.DeadlineExceeded, "timeout waiting for hub client: %v", ctx.Err())
	case result := <-resultCh:
		if result.Err != nil {
			return nil, result.Err
		}
		return result.Val.(*HubClientInfo), nil
	}
}

func (p *hubClientProvider) EvictClient(hubID string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	delete(p.clients, hubID)
}

func classifyHubLookupError(err error, hubID string) error {
	if st, ok := status.FromError(err); ok {
		switch st.Code() {
		case codes.NotFound:
			return status.Errorf(codes.NotFound, "hub %q not found", hubID)
		case codes.Unavailable:
			return status.Errorf(codes.Unavailable, "hub %q is unavailable", hubID)
		default:
			return status.Errorf(st.Code(), "failed to look up hub %q", hubID)
		}
	}
	return status.Errorf(codes.Internal, "failed to look up hub %q", hubID)
}

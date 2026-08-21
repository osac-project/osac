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

	"golang.org/x/sync/singleflight"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
	clnt "sigs.k8s.io/controller-runtime/pkg/client"

	privatev1 "github.com/osac-project/osac/fulfillment-service/internal/api/osac/private/v1"
)

// HubClientFactory creates a Kubernetes client from a parsed REST config.
type HubClientFactory func(config *rest.Config) (clnt.Client, error)

// NewDefaultHubClientFactory creates a HubClientFactory that uses the given scheme
// to create Kubernetes clients from a parsed REST config.
func NewDefaultHubClientFactory(scheme *runtime.Scheme) HubClientFactory {
	return func(config *rest.Config) (clnt.Client, error) {
		return clnt.New(config, clnt.Options{Scheme: scheme})
	}
}

// HubLookup provides hub cluster access for console resolution.
// Implementations are pure readers that consume a tx-bound context.
type HubLookup interface {
	GetKubeconfig(ctx context.Context, hubID string) (kubeconfig []byte, namespace string, err error)
}

// privateServerHubLookup wraps the private HubsServer to implement HubLookup.
// It is a pure reader -- the caller provides a tx-bound context.
type privateServerHubLookup struct {
	hubServer privatev1.HubsServer
}

// NewPrivateServerHubLookup creates a HubLookup backed by the private Hubs server.
func NewPrivateServerHubLookup(hubServer privatev1.HubsServer) HubLookup {
	return &privateServerHubLookup{hubServer: hubServer}
}

func (l *privateServerHubLookup) GetKubeconfig(ctx context.Context, hubID string) (kubeconfig []byte, namespace string, err error) {
	hubResp, err := l.hubServer.Get(ctx, privatev1.HubsGetRequest_builder{
		Id: hubID,
	}.Build())
	if err != nil {
		return nil, "", err
	}
	hub := hubResp.GetObject()
	return hub.GetSpec().GetKubeconfig(), hub.GetSpec().GetNamespace(), nil
}

//go:generate mockgen -destination=hub_client_provider_mock.go -package=servers . HubClientProvider

// HubClientInfo holds the cached Kubernetes client and associated hub metadata.
type HubClientInfo struct {
	Client    clnt.Client
	Config    *rest.Config
	Namespace string
}

// HubClientProvider manages cached Kubernetes clients for hub clusters.
type HubClientProvider interface {
	GetClient(ctx context.Context, hubID string) (*HubClientInfo, error)
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
	p.mu.RLock()
	if info, ok := p.clients[hubID]; ok {
		p.mu.RUnlock()
		return info, nil
	}
	p.mu.RUnlock()

	ctx = context.WithoutCancel(ctx)

	result, err, _ := p.group.Do(hubID, func() (any, error) {
		p.mu.RLock()
		if info, ok := p.clients[hubID]; ok {
			p.mu.RUnlock()
			return info, nil
		}
		p.mu.RUnlock()

		kubeconfig, namespace, err := p.hubLookup.GetKubeconfig(ctx, hubID)
		if err != nil {
			return nil, classifyHubLookupError(err, hubID)
		}

		if namespace == "" {
			return nil, status.Errorf(codes.Internal, "hub %q returned an empty namespace", hubID)
		}

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
	if err != nil {
		return nil, err
	}
	return result.(*HubClientInfo), nil
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

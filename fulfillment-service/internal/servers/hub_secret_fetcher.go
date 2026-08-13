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
	"log/slog"
	"sync"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/client-go/tools/clientcmd"
	clnt "sigs.k8s.io/controller-runtime/pkg/client"
)

const (
	CoordinateHubID      = "hub_id"
	CoordinateNamespace  = "namespace"
	CoordinateSecretName = "secret_name"
	CoordinateKey        = "key"
)

//go:generate mockgen -destination=hub_secret_fetcher_mock.go -package=servers . HubSecretFetcher

// HubSecretFetcher retrieves secret data from Kubernetes Secrets on hub clusters.
type HubSecretFetcher interface {
	Fetch(ctx context.Context, coordinates map[string]string) (map[string][]byte, error)
}

// HubSecretFetcherBuilder contains the data and logic needed to create a hub secret fetcher. Don't create instances of
// this type directly, use the NewHubSecretFetcher function instead.
type HubSecretFetcherBuilder struct {
	logger           *slog.Logger
	hubLookup        HubLookup
	hubClientFactory HubClientFactory
}

type hubSecretFetcher struct {
	logger           *slog.Logger
	hubLookup        HubLookup
	hubClientFactory HubClientFactory
	clients          map[string]clnt.Client
	clientsLock      *sync.Mutex
}

// NewHubSecretFetcher creates a builder that can then be used to configure and create a new hub secret fetcher.
func NewHubSecretFetcher() *HubSecretFetcherBuilder {
	return &HubSecretFetcherBuilder{}
}

// SetLogger sets the logger. This is mandatory.
func (b *HubSecretFetcherBuilder) SetLogger(value *slog.Logger) *HubSecretFetcherBuilder {
	b.logger = value
	return b
}

// SetHubLookup sets the hub lookup used to resolve hub kubeconfigs. This is mandatory.
func (b *HubSecretFetcherBuilder) SetHubLookup(value HubLookup) *HubSecretFetcherBuilder {
	b.hubLookup = value
	return b
}

// SetHubClientFactory sets the factory used to create Kubernetes clients from hub kubeconfigs. This is mandatory.
func (b *HubSecretFetcherBuilder) SetHubClientFactory(value HubClientFactory) *HubSecretFetcherBuilder {
	b.hubClientFactory = value
	return b
}

// Build uses the data stored in the builder to create and configure a new hub secret fetcher.
func (b *HubSecretFetcherBuilder) Build() (HubSecretFetcher, error) {
	if b.logger == nil {
		return nil, errors.New("logger is mandatory")
	}
	if b.hubLookup == nil {
		return nil, errors.New("hub lookup is mandatory")
	}
	if b.hubClientFactory == nil {
		return nil, errors.New("hub client factory is mandatory")
	}
	return &hubSecretFetcher{
		logger:           b.logger,
		hubLookup:        b.hubLookup,
		hubClientFactory: b.hubClientFactory,
		clients:          make(map[string]clnt.Client),
		clientsLock:      &sync.Mutex{},
	}, nil
}

func (f *hubSecretFetcher) Fetch(ctx context.Context, coordinates map[string]string) (map[string][]byte, error) {
	hubID, err := requireCoordinate(coordinates, CoordinateHubID)
	if err != nil {
		return nil, err
	}
	namespace, err := requireCoordinate(coordinates, CoordinateNamespace)
	if err != nil {
		return nil, err
	}
	secretName, err := requireCoordinate(coordinates, CoordinateSecretName)
	if err != nil {
		return nil, err
	}
	key := coordinates[CoordinateKey]

	hubClient, err := f.getOrCreateClient(ctx, hubID)
	if err != nil {
		return nil, err
	}

	secret := &corev1.Secret{}
	err = hubClient.Get(ctx, clnt.ObjectKey{
		Namespace: namespace,
		Name:      secretName,
	}, secret)
	if err != nil {
		return nil, classifyKubeError(err, hubID, namespace, secretName)
	}

	if key != "" {
		value, ok := secret.Data[key]
		if !ok {
			return nil, status.Errorf(codes.NotFound,
				"key %q not found in secret %s/%s on hub %q", key, namespace, secretName, hubID)
		}
		return map[string][]byte{key: value}, nil
	}

	return secret.Data, nil
}

func (f *hubSecretFetcher) getOrCreateClient(ctx context.Context, hubID string) (clnt.Client, error) {
	f.clientsLock.Lock()
	defer f.clientsLock.Unlock()

	if client, ok := f.clients[hubID]; ok {
		return client, nil
	}

	kubeconfig, _, err := f.hubLookup.GetKubeconfig(ctx, hubID)
	if err != nil {
		return nil, classifyHubLookupError(err, hubID)
	}

	config, err := clientcmd.RESTConfigFromKubeConfig(kubeconfig)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "failed to parse kubeconfig for hub %q: %v", hubID, err)
	}

	client, err := f.hubClientFactory(config)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "failed to create client for hub %q: %v", hubID, err)
	}

	f.clients[hubID] = client
	return client, nil
}

func requireCoordinate(coordinates map[string]string, key string) (string, error) {
	value, ok := coordinates[key]
	if !ok || value == "" {
		return "", status.Errorf(codes.InvalidArgument, "coordinate %q is required", key)
	}
	return value, nil
}

func classifyKubeError(err error, hubID, namespace, secretName string) error {
	if apierrors.IsNotFound(err) {
		return status.Errorf(codes.NotFound,
			"secret %s/%s not found on hub %q", namespace, secretName, hubID)
	}
	if apierrors.IsTimeout(err) || apierrors.IsServerTimeout(err) || apierrors.IsServiceUnavailable(err) {
		return status.Errorf(codes.Unavailable,
			"hub %q is unavailable: %v", hubID, err)
	}
	var netErr interface{ Timeout() bool }
	if errors.As(err, &netErr) && netErr.Timeout() {
		return status.Errorf(codes.Unavailable,
			"hub %q is unavailable: %v", hubID, err)
	}
	return status.Errorf(codes.Internal,
		"failed to read secret %s/%s from hub %q: %v", namespace, secretName, hubID, err)
}

func classifyHubLookupError(err error, hubID string) error {
	if st, ok := status.FromError(err); ok {
		switch st.Code() {
		case codes.NotFound:
			return status.Errorf(codes.NotFound, "hub %q not found", hubID)
		case codes.Unavailable:
			return status.Errorf(codes.Unavailable, "hub %q is unavailable: %v", hubID, st.Message())
		default:
			return status.Errorf(st.Code(), "failed to look up hub %q: %v", hubID, st.Message())
		}
	}
	return status.Errorf(codes.Internal, "failed to look up hub %q: %v", hubID, err)
}

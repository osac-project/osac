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

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
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
	hubClientProvider HubClientProvider
}

type hubSecretFetcher struct {
	hubClientProvider HubClientProvider
}

// NewHubSecretFetcher creates a builder that can then be used to configure and create a new hub secret fetcher.
func NewHubSecretFetcher() *HubSecretFetcherBuilder {
	return &HubSecretFetcherBuilder{}
}

// SetHubClientProvider sets the hub client provider. This is mandatory.
func (b *HubSecretFetcherBuilder) SetHubClientProvider(value HubClientProvider) *HubSecretFetcherBuilder {
	b.hubClientProvider = value
	return b
}

// Build uses the data stored in the builder to create and configure a new hub secret fetcher.
func (b *HubSecretFetcherBuilder) Build() (HubSecretFetcher, error) {
	if b.hubClientProvider == nil {
		return nil, errors.New("hub client provider is mandatory")
	}
	return &hubSecretFetcher{
		hubClientProvider: b.hubClientProvider,
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

	hubInfo, err := f.hubClientProvider.GetClient(ctx, hubID)
	if err != nil {
		return nil, err
	}

	secret := &corev1.Secret{}
	err = hubInfo.Client.Get(ctx, clnt.ObjectKey{
		Namespace: namespace,
		Name:      secretName,
	}, secret)
	if err != nil {
		if errors.Is(err, context.Canceled) {
			return nil, status.Errorf(codes.Canceled,
				"request canceled while reading secret %s/%s from hub %q", namespace, secretName, hubID)
		}
		if errors.Is(err, context.DeadlineExceeded) {
			return nil, status.Errorf(codes.DeadlineExceeded,
				"request timed out while reading secret %s/%s from hub %q", namespace, secretName, hubID)
		}
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
			"hub %q is unavailable", hubID)
	}
	var netErr interface{ Timeout() bool }
	if errors.As(err, &netErr) && netErr.Timeout() {
		return status.Errorf(codes.Unavailable,
			"hub %q is unavailable", hubID)
	}
	if apierrors.IsUnauthorized(err) {
		return status.Errorf(codes.Unauthenticated,
			"authentication failed for secret %s/%s on hub %q", namespace, secretName, hubID)
	}
	if apierrors.IsForbidden(err) {
		return status.Errorf(codes.PermissionDenied,
			"access denied to secret %s/%s on hub %q", namespace, secretName, hubID)
	}
	return status.Errorf(codes.Internal,
		"failed to read secret %s/%s from hub %q", namespace, secretName, hubID)
}

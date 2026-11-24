/*
Copyright (c) 2026 Red Hat Inc.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package it

import (
	"context"
	"errors"
	"fmt"
	"time"

	"golang.org/x/sync/semaphore"
	authenticationv1 "k8s.io/api/authentication/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	corev1 "k8s.io/client-go/kubernetes/typed/core/v1"

	"github.com/osac-project/osac/fulfillment-service/internal/auth"
)

type kubernetesTokenSource struct {
	accounts    corev1.ServiceAccountInterface
	accountName string

	// The emergency REST and gRPC clients share the source. Serialize refresh
	// and invalidation while allowing callers to cancel their wait.
	gate      *semaphore.Weighted
	token     auth.Token
	refreshAt time.Time
}

func (t *Tool) makeKubernetesTokenSource(ctx context.Context, sa, namespace string) (auth.TokenSource, error) {
	source := &kubernetesTokenSource{
		accounts:    t.kubeClientSet.CoreV1().ServiceAccounts(namespace),
		accountName: sa,
		gate:        semaphore.NewWeighted(1),
	}
	// Request the first token during setup so a setup error surfaces immediately.
	if _, err := source.Token(ctx); err != nil {
		return nil, err
	}
	return source, nil
}

func (s *kubernetesTokenSource) Token(ctx context.Context) (*auth.Token, error) {
	if err := s.gate.Acquire(ctx, 1); err != nil {
		return nil, err
	}
	defer s.gate.Release(1)

	if !time.Now().Before(s.refreshAt) {
		if err := s.renew(ctx); err != nil {
			return nil, err
		}
	}
	token := s.token
	return &token, nil
}

func (s *kubernetesTokenSource) Invalidate(ctx context.Context) error {
	if err := s.gate.Acquire(ctx, 1); err != nil {
		return err
	}
	defer s.gate.Release(1)

	s.token = auth.Token{}
	s.refreshAt = time.Time{}
	return nil
}

func (s *kubernetesTokenSource) renew(ctx context.Context) error {
	response, err := s.accounts.CreateToken(ctx, s.accountName, &authenticationv1.TokenRequest{
		Spec: authenticationv1.TokenRequestSpec{ExpirationSeconds: new(int64(3600))},
	}, metav1.CreateOptions{})
	if err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		// Kubernetes request errors can include a response body. Keep it out
		// of the integration logs because this endpoint returns credentials.
		var status apierrors.APIStatus
		if errors.As(err, &status) {
			return fmt.Errorf("creating token for service account %q: Kubernetes API status %d", s.accountName, status.Status().Code)
		}
		return fmt.Errorf("creating token for service account %q failed (%T)", s.accountName, err)
	}
	now := time.Now()
	expiry := response.Status.ExpirationTimestamp.Time
	if response.Status.Token == "" || !expiry.After(now) {
		return fmt.Errorf("empty or expired token returned for Kubernetes service account %q", s.accountName)
	}

	s.token = auth.Token{Access: response.Status.Token, Expiry: expiry}
	// Use the granted lifetime. A fractional window avoids continual
	// renewal when the API server grants a lifetime shorter than a minute.
	s.refreshAt = expiry.Add(-min(time.Minute, expiry.Sub(now)/10))
	return nil
}

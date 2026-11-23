/*
Copyright (c) 2025 Red Hat Inc.

Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except in compliance with the
License. You may obtain a copy of the License at

  http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on an
"AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the specific
language governing permissions and limitations under the License.
*/

package it

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/url"
	"strings"
	"sync"
	"time"

	authenticationv1 "k8s.io/api/authentication/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"

	"github.com/osac-project/osac/fulfillment-service/internal/auth"
)

const (
	// serviceAccountTokenExpiration is the lifetime requested from the API server when minting a token
	// for one of the test service accounts.
	serviceAccountTokenExpiration = time.Hour

	// serviceAccountTokenRefreshMargin is how long before expiry a token is re-minted, so that a token
	// is never used when it is close to its deadline.
	serviceAccountTokenRefreshMargin = 10 * time.Minute

	// tokenSourceRetryAttempts is how many times a token request is retried after a transient failure.
	tokenSourceRetryAttempts = 6

	// tokenSourceRetryMinDelay and tokenSourceRetryMaxDelay bound the exponential backoff between those
	// retries. The total retry budget is about half a minute, which rides out the short connectivity
	// blips seen when the Keycloak route behind the gateway resets connections.
	tokenSourceRetryMinDelay = time.Second
	tokenSourceRetryMaxDelay = 10 * time.Second
)

// refreshableServiceAccountTokenSource returns Kubernetes service account tokens, re-minting them before they
// expire.
//
// A cached, non-refreshed token is only valid for one hour. The integration suite routinely runs longer than
// that, so specs that execute late (the emergency access ones, for example) fail with 'token expired' even
// though nothing is wrong with the code under test.
type refreshableServiceAccountTokenSource struct {
	logger         *slog.Logger
	clientSet      kubernetes.Interface
	namespace      string
	serviceAccount string

	lock  sync.Mutex
	token *auth.Token
}

// newRefreshableServiceAccountTokenSource creates a token source for the given service account.
func newRefreshableServiceAccountTokenSource(
	logger *slog.Logger, clientSet kubernetes.Interface, namespace, serviceAccount string,
) *refreshableServiceAccountTokenSource {
	return &refreshableServiceAccountTokenSource{
		logger:         logger,
		clientSet:      clientSet,
		namespace:      namespace,
		serviceAccount: serviceAccount,
	}
}

// Token returns a valid token, minting a new one when the cached token is missing or about to expire.
func (s *refreshableServiceAccountTokenSource) Token(ctx context.Context) (result *auth.Token, err error) {
	s.lock.Lock()
	defer s.lock.Unlock()
	if s.token != nil && s.token.Expiry.After(time.Now().Add(serviceAccountTokenRefreshMargin)) {
		return s.token, nil
	}
	expirationSeconds := int64(serviceAccountTokenExpiration / time.Second)
	response, err := s.clientSet.CoreV1().ServiceAccounts(s.namespace).CreateToken(
		ctx,
		s.serviceAccount,
		&authenticationv1.TokenRequest{
			Spec: authenticationv1.TokenRequestSpec{
				ExpirationSeconds: &expirationSeconds,
			},
		},
		metav1.CreateOptions{},
	)
	if err != nil {
		return nil, fmt.Errorf(
			"failed to create token for service account '%s' in namespace '%s': %w",
			s.serviceAccount, s.namespace, err,
		)
	}
	expiry := time.Now().Add(serviceAccountTokenExpiration)
	if !response.Status.ExpirationTimestamp.IsZero() {
		expiry = response.Status.ExpirationTimestamp.Time
	}
	s.token = &auth.Token{
		Access: response.Status.Token,
		Expiry: expiry,
	}
	s.logger.DebugContext(ctx, "Minted service account token",
		slog.String("namespace", s.namespace),
		slog.String("service_account", s.serviceAccount),
		slog.Time("expiry", expiry),
	)
	return s.token, nil
}

// Invalidate discards the cached token, so that the next call to Token mints a new one.
func (s *refreshableServiceAccountTokenSource) Invalidate(_ context.Context) error {
	s.lock.Lock()
	defer s.lock.Unlock()
	s.token = nil
	return nil
}

// retryingTokenSource retries token requests that fail because of a transient transport problem.
//
// The integration suite authenticates through the ingress gateway, which occasionally resets the connection to
// Keycloak for short periods (observed as 'EOF' or 'upstream connect error' with a healthy Keycloak pod). Without
// a retry a single such reset fails an otherwise unrelated spec, so the suite reports dozens of 'failed to obtain
// token' failures while most requests succeed.
type retryingTokenSource struct {
	logger *slog.Logger
	inner  auth.TokenSource
}

// newRetryingTokenSource wraps the given token source with retries for transient failures.
func newRetryingTokenSource(logger *slog.Logger, inner auth.TokenSource) *retryingTokenSource {
	return &retryingTokenSource{
		logger: logger,
		inner:  inner,
	}
}

// Token returns a token, retrying transient failures with exponential backoff.
func (s *retryingTokenSource) Token(ctx context.Context) (result *auth.Token, err error) {
	delay := tokenSourceRetryMinDelay
	for attempt := 1; ; attempt++ {
		result, err = s.inner.Token(ctx)
		if err == nil {
			return result, nil
		}
		if !isTransientTokenError(err) || attempt >= tokenSourceRetryAttempts {
			return nil, err
		}
		s.logger.WarnContext(ctx, "Transient failure obtaining token, will retry",
			slog.Int("attempt", attempt),
			slog.Duration("delay", delay),
			slog.String("error", err.Error()),
		)
		if sleepErr := sleepWithContext(ctx, delay); sleepErr != nil {
			return nil, err
		}
		delay *= 2
		if delay > tokenSourceRetryMaxDelay {
			delay = tokenSourceRetryMaxDelay
		}
	}
}

// Invalidate discards any cached token in the wrapped source.
func (s *retryingTokenSource) Invalidate(ctx context.Context) error {
	return s.inner.Invalidate(ctx)
}

// isTransientTokenError tells whether the given error is a transport level failure that is worth retrying.
// Authentication and authorisation failures (401, 403) are deliberately not retried: they mean the credentials
// or the configuration are wrong, and retrying them would only slow the test down.
func isTransientTokenError(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) {
		return true
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return true
	}
	var netErr net.Error
	if errors.As(err, &netErr) {
		return true
	}
	var urlErr *url.Error
	if errors.As(err, &urlErr) && urlErr.Timeout() {
		return true
	}
	message := strings.ToLower(err.Error())
	for _, marker := range []string{
		"connection reset",
		"connection refused",
		"broken pipe",
		"upstream connect error",
		"upstream overflow",
		"unexpected response code 502",
		"unexpected response code 503",
		"unexpected response code 504",
	} {
		if strings.Contains(message, marker) {
			return true
		}
	}
	return false
}

// sleepWithContext waits for the given duration or until the context is done, whichever happens first.
func sleepWithContext(ctx context.Context, delay time.Duration) error {
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

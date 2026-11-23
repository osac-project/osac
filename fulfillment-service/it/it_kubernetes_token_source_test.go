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
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"

	"github.com/onsi/gomega/ghttp"
	authenticationv1 "k8s.io/api/authentication/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
)

func TestEmergencyTokenSourceRenewsAndInvalidates(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		const lifetime = 5 * time.Minute
		tool, calls := tokenRequestTestTool(t, func(_ *http.Request, call int64) (int, any) {
			return http.StatusCreated, syntheticTokenRequest(call, time.Now().Add(lifetime))
		})
		ctx := t.Context()
		source, err := tool.makeKubernetesTokenSource(ctx, emergencyServiceAccount, "osac")
		if err != nil {
			t.Fatal(err)
		}
		first, err := source.Token(ctx)
		if err != nil || first.Access != "synthetic-1" || calls.Load() != 1 {
			t.Fatal("the initial token was not reused")
		}

		time.Sleep(lifetime - 30*time.Second - time.Second)
		cached, err := source.Token(ctx)
		if err != nil || cached.Access != first.Access || calls.Load() != 1 {
			t.Fatal("the token was refreshed before the renewal window")
		}

		time.Sleep(time.Second)
		renewed, err := source.Token(ctx)
		if err != nil || renewed.Access != "synthetic-2" || calls.Load() != 2 {
			t.Fatal("the token was not renewed before its server-reported expiry")
		}
		if !renewed.Expiry.Equal(time.Now().Add(lifetime)) {
			t.Fatal("the renewed token did not carry its server-reported expiry")
		}

		time.Sleep(lifetime)
		afterExpiry, err := source.Token(ctx)
		if err != nil || afterExpiry.Access != "synthetic-3" || calls.Load() != 3 {
			t.Fatal("the expired token was not replaced")
		}
		if err := source.Invalidate(ctx); err != nil {
			t.Fatal(err)
		}
		if calls.Load() != 3 {
			t.Fatal("invalidation requested a token before the next use")
		}
		replacement, err := source.Token(ctx)
		if err != nil || replacement.Access != "synthetic-4" || calls.Load() != 4 {
			t.Fatal("the invalidated token was not replaced")
		}
	})
}

func TestEmergencyTokenSourceRefreshErrors(t *testing.T) {
	for _, scenario := range []struct {
		name    string
		failure func(int64) (int, any)
		message string
	}{
		{
			name: "API denial",
			failure: func(_ int64) (int, any) {
				return http.StatusForbidden, &metav1.Status{
					TypeMeta: metav1.TypeMeta{APIVersion: "v1", Kind: "Status"},
					Status:   metav1.StatusFailure,
					Reason:   metav1.StatusReasonForbidden,
					Code:     http.StatusForbidden,
					Message:  "synthetic-server-message",
				}
			},
			message: "Kubernetes API status 403",
		},
		{
			name: "missing expiry",
			failure: func(call int64) (int, any) {
				return http.StatusCreated, syntheticTokenRequest(call, time.Time{})
			},
			message: "empty or expired token",
		},
		{
			name: "already expired",
			failure: func(call int64) (int, any) {
				return http.StatusCreated, syntheticTokenRequest(call, time.Now())
			},
			message: "empty or expired token",
		},
		{
			name: "empty token",
			failure: func(call int64) (int, any) {
				response := syntheticTokenRequest(call, time.Now().Add(time.Hour))
				response.Status.Token = ""
				return http.StatusCreated, response
			},
			message: "empty or expired token",
		},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				tool, calls := tokenRequestTestTool(t, func(_ *http.Request, call int64) (int, any) {
					if call == 1 || call == 3 {
						return scenario.failure(call)
					}
					return http.StatusCreated, syntheticTokenRequest(call, time.Now().Add(time.Hour))
				})
				ctx := t.Context()
				source, err := tool.makeKubernetesTokenSource(ctx, emergencyServiceAccount, "osac")
				if source != nil || err == nil || !strings.Contains(err.Error(), scenario.message) ||
					strings.Contains(err.Error(), "synthetic-") {
					t.Fatal("setup did not return a credential-safe TokenRequest error")
				}
				if calls.Load() != 1 {
					t.Fatal("setup made more than one TokenRequest on failure")
				}

				source, err = tool.makeKubernetesTokenSource(ctx, emergencyServiceAccount, "osac")
				if err != nil {
					t.Fatal(err)
				}
				time.Sleep(59 * time.Minute)
				token, err := source.Token(ctx)
				if token != nil || err == nil || !strings.Contains(err.Error(), scenario.message) ||
					strings.Contains(err.Error(), "synthetic-") {
					t.Fatal("failed renewal did not return a credential-safe error")
				}
				token, err = source.Token(ctx)
				if err != nil || token.Access != "synthetic-4" || calls.Load() != 4 {
					t.Fatal("a subsequent request did not recover from failed renewal")
				}
			})
		})
	}
}

func TestEmergencyTokenSourceConcurrentRefresh(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		started, release := make(chan struct{}), make(chan struct{})
		tool, calls := tokenRequestTestTool(t, func(_ *http.Request, call int64) (int, any) {
			if call == 2 {
				close(started)
				<-release
			}
			return http.StatusCreated, syntheticTokenRequest(call, time.Now().Add(time.Hour))
		})
		ctx := t.Context()
		source, err := tool.makeKubernetesTokenSource(ctx, emergencyServiceAccount, "osac")
		if err != nil {
			t.Fatal(err)
		}
		time.Sleep(59 * time.Minute)
		results := make(chan bool, 2)
		readToken := func() {
			token, err := source.Token(ctx)
			results <- err == nil && token.Access == "synthetic-2"
		}
		go readToken()
		synctest.Wait()
		select {
		case <-started:
		default:
			t.Fatal("renewal did not start")
		}

		go readToken()
		waiter, cancel := context.WithCancel(ctx)
		waiterError := make(chan error, 1)
		go func() {
			_, err := source.Token(waiter)
			waiterError <- err
		}()
		synctest.Wait()
		cancel()
		synctest.Wait()
		select {
		case err := <-waiterError:
			if !errors.Is(err, context.Canceled) {
				t.Error("a canceled waiter did not return the context error")
			}
		default:
			t.Error("a canceled waiter remained blocked during renewal")
		}

		close(release)
		first, second := <-results, <-results
		if !first || !second || calls.Load() != 2 {
			t.Fatal("the concurrent callers did not share a single renewed token")
		}
	})
}

func tokenRequestTestTool(t *testing.T, respond func(*http.Request, int64) (int, any)) (*Tool, *atomic.Int64) {
	t.Helper()
	var calls atomic.Int64
	client := &http.Client{Transport: ghttp.RoundTripperFunc(func(request *http.Request) (*http.Response, error) {
		defer request.Body.Close()
		if request.Method != http.MethodPost || request.URL.Path != "/api/v1/namespaces/osac/serviceaccounts/admin/token" {
			t.Error("unexpected TokenRequest method or target")
		}
		var tokenRequest authenticationv1.TokenRequest
		if err := json.NewDecoder(request.Body).Decode(&tokenRequest); err != nil {
			return nil, fmt.Errorf("decode synthetic TokenRequest: %w", err)
		}
		spec := tokenRequest.Spec
		if spec.ExpirationSeconds == nil || *spec.ExpirationSeconds != 3600 ||
			len(spec.Audiences) != 0 || spec.BoundObjectRef != nil {
			t.Error("TokenRequest changed the duration, default audiences, or binding")
		}
		code, body := respond(request, calls.Add(1))
		data, err := json.Marshal(body)
		if err != nil {
			return nil, err
		}
		return &http.Response{
			StatusCode: code,
			Header:     http.Header{"Content-Type": {"application/json"}},
			Body:       io.NopCloser(bytes.NewReader(data)),
		}, nil
	})}
	clientSet, err := kubernetes.NewForConfigAndClient(&rest.Config{
		Host:          "https://example.test",
		ContentConfig: rest.ContentConfig{ContentType: "application/json"},
	}, client)
	if err != nil {
		t.Fatal(err)
	}
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	return &Tool{kubeClientSet: clientSet, logger: logger}, &calls
}

func syntheticTokenRequest(call int64, expires time.Time) *authenticationv1.TokenRequest {
	return &authenticationv1.TokenRequest{
		TypeMeta: metav1.TypeMeta{APIVersion: "authentication.k8s.io/v1", Kind: "TokenRequest"},
		Status: authenticationv1.TokenRequestStatus{
			Token:               fmt.Sprintf("synthetic-%d", call),
			ExpirationTimestamp: metav1.NewTime(expires),
		},
	}
}

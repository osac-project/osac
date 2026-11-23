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
	"sync"
	"sync/atomic"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/onsi/gomega/ghttp"
	authenticationv1 "k8s.io/api/authentication/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"

	"github.com/osac-project/osac/fulfillment-service/internal/auth"
)

var _ = Describe("Emergency TokenRequest source", func() {
	It("mints eagerly, reuses, renews and invalidates tokens", func(ctx SpecContext) {
		expiry := time.Now().Add(5 * time.Minute).Truncate(time.Second)
		renewedExpiry := expiry.Add(time.Hour)
		tool, calls := tokenRequestTestTool(func(call int64) (int, any) {
			if call == 1 {
				return http.StatusCreated, syntheticTokenRequest(call, expiry)
			}
			return http.StatusCreated, syntheticTokenRequest(call, renewedExpiry)
		})

		before := time.Now()
		source := newTestEmergencyTokenSource(ctx, tool)
		after := time.Now()
		Expect(calls.Load()).To(Equal(int64(1)), "setup should mint the first token")
		// The server grants about five minutes despite the one-hour request.
		// Its early refresh boundary is one tenth of that granted lifetime
		// before expiry. Bracket the production clock read within the call.
		Expect(source.refreshAt).To(BeTemporally(">=", expiry.Add(-expiry.Sub(before)/10)))
		Expect(source.refreshAt).To(BeTemporally("<=", expiry.Add(-expiry.Sub(after)/10)))

		token, err := source.Token(ctx)
		Expect(err).NotTo(HaveOccurred())
		Expect(token.Access).To(Equal("synthetic-1"))
		Expect(token.Expiry).To(BeTemporally("==", expiry))
		Expect(calls.Load()).To(Equal(int64(1)), "reuse the cached token before its refresh window")

		// Move the cached boundary into the past while the token is still valid.
		source.refreshAt = time.Now().Add(-time.Minute)
		Expect(source.token.Expiry).To(BeTemporally(">", time.Now()))
		token, err = source.Token(ctx)
		Expect(err).NotTo(HaveOccurred())
		Expect(token.Access).To(Equal("synthetic-2"))
		Expect(token.Expiry).To(BeTemporally("==", renewedExpiry))
		Expect(source.refreshAt).To(BeTemporally("==", renewedExpiry.Add(-time.Minute)))
		Expect(calls.Load()).To(Equal(int64(2)), "refresh before the server-granted expiry")

		source.token.Expiry = time.Now().Add(-time.Minute)
		source.refreshAt = source.token.Expiry.Add(-time.Minute)
		token, err = source.Token(ctx)
		Expect(err).NotTo(HaveOccurred())
		Expect(token.Access).To(Equal("synthetic-3"))
		Expect(calls.Load()).To(Equal(int64(3)), "replace an expired token")

		Expect(source.Invalidate(ctx)).To(Succeed())
		Expect(calls.Load()).To(Equal(int64(3)), "invalidation defers minting until the next read")
		token, err = source.Token(ctx)
		Expect(err).NotTo(HaveOccurred())
		Expect(token.Access).To(Equal("synthetic-4"))
		Expect(calls.Load()).To(Equal(int64(4)))
	})

	DescribeTable("returns safe errors during setup and refresh, then recovers",
		func(ctx SpecContext, fail func(int64) (int, any), message string) {
			expiry := time.Now().Add(time.Hour).Truncate(time.Second)
			tool, calls := tokenRequestTestTool(func(call int64) (int, any) {
				if call == 1 || call == 3 {
					return fail(call)
				}
				return http.StatusCreated, syntheticTokenRequest(call, expiry)
			})

			initial, err := tool.makeKubernetesTokenSource(ctx, emergencyServiceAccount, "osac")
			Expect(initial).To(BeNil())
			Expect(err).To(MatchError(And(ContainSubstring(message), Not(ContainSubstring("synthetic-")))))
			Expect(calls.Load()).To(Equal(int64(1)))

			source := newTestEmergencyTokenSource(ctx, tool)
			source.refreshAt = time.Now().Add(-time.Minute)
			token, err := source.Token(ctx)
			Expect(token).To(BeNil())
			Expect(err).To(MatchError(And(ContainSubstring(message), Not(ContainSubstring("synthetic-")))))
			Expect(calls.Load()).To(Equal(int64(3)))

			token, err = source.Token(ctx)
			Expect(err).NotTo(HaveOccurred())
			Expect(token.Access).To(Equal("synthetic-4"))
			Expect(token.Expiry).To(BeTemporally("==", expiry))
			Expect(calls.Load()).To(Equal(int64(4)))
		},
		Entry("API denial", func(int64) (int, any) {
			return http.StatusForbidden, &metav1.Status{
				TypeMeta: metav1.TypeMeta{APIVersion: "v1", Kind: "Status"},
				Status:   metav1.StatusFailure,
				Reason:   metav1.StatusReasonForbidden,
				Code:     http.StatusForbidden,
				Message:  "synthetic-server-message",
			}
		}, "Kubernetes API status 403"),
		Entry("missing expiry", func(call int64) (int, any) {
			return http.StatusCreated, syntheticTokenRequest(call, time.Time{})
		}, "empty or expired token"),
		Entry("already expired", func(call int64) (int, any) {
			return http.StatusCreated, syntheticTokenRequest(call, time.Now().Add(-time.Minute))
		}, "empty or expired token"),
		Entry("empty token", func(call int64) (int, any) {
			response := syntheticTokenRequest(call, time.Now().Add(time.Hour))
			response.Status.Token = ""
			return http.StatusCreated, response
		}, "empty or expired token"),
	)

	It("shares one refresh across callers and lets a waiter cancel", func(ctx SpecContext) {
		const wait = 5 * time.Second
		started, release := make(chan struct{}), make(chan struct{})
		unblock := sync.OnceFunc(func() { close(release) })
		DeferCleanup(unblock)
		expiry := time.Now().Add(time.Hour).Truncate(time.Second)
		tool, calls := tokenRequestTestTool(func(call int64) (int, any) {
			if call == 2 {
				close(started)
				<-release
			}
			return http.StatusCreated, syntheticTokenRequest(call, expiry)
		})
		source := newTestEmergencyTokenSource(ctx, tool)
		source.refreshAt = time.Now().Add(-time.Minute)

		type result struct {
			token *auth.Token
			err   error
		}
		readToken := func(ctx context.Context) <-chan result {
			results := make(chan result, 1)
			go func() {
				token, err := source.Token(ctx)
				results <- result{token: token, err: err}
			}()
			return results
		}

		first := readToken(ctx)
		Eventually(started, wait).Should(BeClosed())
		// The first request must hold the gate for the entire refresh.
		acquired := source.gate.TryAcquire(1)
		if acquired {
			source.gate.Release(1)
		}
		Expect(acquired).To(BeFalse())

		// A zero-weight acquisition consumes no permit and fails when another
		// caller is queued. Wait for each reader to queue before letting it proceed.
		queueIsEmpty := func() bool { return source.gate.TryAcquire(0) }

		waiter, cancel := context.WithCancel(ctx)
		DeferCleanup(cancel)
		canceled := readToken(waiter)
		Eventually(queueIsEmpty, wait).Should(BeFalse(), "the cancelable caller should queue behind the refresh")
		cancel()
		var canceledResult result
		Eventually(canceled, wait).Should(Receive(&canceledResult))
		Expect(canceledResult.err).To(MatchError(context.Canceled))
		Expect(canceledResult.token).To(BeNil())
		Expect(calls.Load()).To(Equal(int64(2)))

		second := readToken(ctx)
		Eventually(queueIsEmpty, wait).Should(BeFalse(), "the second reader should queue behind the refresh")
		unblock()
		for _, caller := range []<-chan result{first, second} {
			var got result
			Eventually(caller, wait).Should(Receive(&got))
			Expect(got.err).NotTo(HaveOccurred())
			Expect(got.token.Access).To(Equal("synthetic-2"))
		}
		Expect(calls.Load()).To(Equal(int64(2)))
	})
})

func newTestEmergencyTokenSource(ctx context.Context, tool *Tool) *kubernetesTokenSource {
	GinkgoHelper()
	source, err := tool.makeKubernetesTokenSource(ctx, emergencyServiceAccount, "osac")
	Expect(err).NotTo(HaveOccurred())
	Expect(source).To(BeAssignableToTypeOf(&kubernetesTokenSource{}))
	return source.(*kubernetesTokenSource)
}

func tokenRequestTestTool(respond func(int64) (int, any)) (*Tool, *atomic.Int64) {
	GinkgoHelper()
	var calls atomic.Int64
	client := &http.Client{Transport: ghttp.RoundTripperFunc(func(request *http.Request) (*http.Response, error) {
		if request.Method != http.MethodPost || request.URL.Path != "/api/v1/namespaces/osac/serviceaccounts/admin/token" || request.Body == nil {
			return nil, errors.New("unexpected synthetic TokenRequest method, target, or body")
		}
		defer request.Body.Close()
		var tokenRequest authenticationv1.TokenRequest
		if err := json.NewDecoder(request.Body).Decode(&tokenRequest); err != nil {
			return nil, fmt.Errorf("decode synthetic TokenRequest: %w", err)
		}
		spec := tokenRequest.Spec
		if spec.ExpirationSeconds == nil || *spec.ExpirationSeconds != 3600 ||
			len(spec.Audiences) != 0 || spec.BoundObjectRef != nil {
			return nil, errors.New("synthetic TokenRequest changed the duration, default audiences, or binding")
		}
		code, body := respond(calls.Add(1))
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
	Expect(err).NotTo(HaveOccurred())
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

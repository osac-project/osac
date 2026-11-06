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
	"net"
	"net/url"
	"strings"
	"testing"

	"github.com/onsi/gomega"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
)

func TestComputeStorageRecorderErrorReason(t *testing.T) {
	const sensitive = "synthetic-secret"
	cases := []struct {
		name string
		err  error
		want string
	}{
		{name: "success"},
		{name: "wrapped cancellation", err: fmt.Errorf("%s: %w", sensitive, context.Canceled), want: "Canceled"},
		{name: "transport timeout", err: &url.Error{
			Op: "Get", URL: "https://private.example.invalid/api?token=" + sensitive,
			Err: context.DeadlineExceeded,
		}, want: "Timeout"},
		{name: "DNS failure", err: &net.DNSError{Err: sensitive, Name: "private.example.invalid"}, want: "ConnectionError"},
		{name: "DNS timeout", err: &net.DNSError{Err: sensitive, Name: "private.example.invalid", IsTimeout: true}, want: "Timeout"},
		{name: "unauthorized", err: apierrors.NewUnauthorized(sensitive), want: "Unauthorized"},
		{name: "wrapped forbidden", err: fmt.Errorf("%s: %w", sensitive,
			apierrors.NewForbidden(schema.GroupResource{Resource: "pods"}, sensitive, errors.New(sensitive))), want: "Forbidden"},
		{name: "not found", err: apierrors.NewNotFound(schema.GroupResource{Resource: "nodes"}, sensitive), want: "NotFound"},
		{name: "API timeout", err: apierrors.NewTimeoutError(sensitive, 1), want: "Timeout"},
		{name: "server timeout", err: apierrors.NewServerTimeout(schema.GroupResource{Resource: sensitive}, "get", 1), want: "ServerTimeout"},
		{name: "rate limit", err: apierrors.NewTooManyRequests(sensitive, 1), want: "TooManyRequests"},
		{name: "unavailable", err: apierrors.NewServiceUnavailable(sensitive), want: "ServiceUnavailable"},
		{name: "untrusted API reason", err: &apierrors.StatusError{ErrStatus: metav1.Status{
			Reason: metav1.StatusReason(sensitive), Message: sensitive,
		}}, want: "RequestFailed"},
		{name: "unknown error", err: errors.New(sensitive), want: "RequestFailed"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			reason := computeStorageRecorderErrorReason(tc.err)
			if reason != tc.want {
				t.Fatalf("reason = %q, want %q", reason, tc.want)
			}
			var failure string
			g := gomega.NewGomega(func(message string, _ ...int) { failure = message })
			g.Expect(reason).To(gomega.BeEmpty(), "get AAP recorder Pod")
			if tc.err == nil {
				if failure != "" {
					t.Fatalf("successful request produced a failure: %s", failure)
				}
				return
			}
			if !strings.Contains(failure, "get AAP recorder Pod") || !strings.Contains(failure, tc.want) {
				t.Fatalf("failure must retain the operation and safe reason: %s", failure)
			}
			for _, privateValue := range []string{sensitive, "private.example.invalid"} {
				if strings.Contains(failure, privateValue) {
					t.Fatalf("failure exposed synthetic private data: %s", failure)
				}
			}
		})
	}
}

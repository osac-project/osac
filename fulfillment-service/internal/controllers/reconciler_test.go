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

package controllers

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	. "github.com/onsi/gomega"
	privatev1 "github.com/osac-project/osac/proto/gen/osac/private/v1"
)

func TestRequeueAfterPreservesCauseAndDelay(t *testing.T) {
	RegisterTestingT(t)

	cause := errors.New("dependency is not ready")
	wrapped := fmt.Errorf("reconciliation failed: %w", RequeueAfter(cause, 250*time.Millisecond))

	Expect(errors.Is(wrapped, cause)).To(BeTrue())
	var retryable interface{ RequeueAfter() time.Duration }
	Expect(errors.As(wrapped, &retryable)).To(BeTrue())
	Expect(retryable.RequeueAfter()).To(Equal(250 * time.Millisecond))
	var backoff interface{ UseExponentialBackoff() bool }
	Expect(errors.As(wrapped, &backoff)).To(BeTrue())
	Expect(backoff.UseExponentialBackoff()).To(BeTrue())
}

func TestRequeueAfterKubernetesDeletionRequestsRetry(t *testing.T) {
	RegisterTestingT(t)

	err := RequeueAfterKubernetesDeletion("subnet")
	Expect(err).To(MatchError("kubernetes subnet deletion is still in progress"))
	var retryable interface{ RequeueAfter() time.Duration }
	Expect(errors.As(err, &retryable)).To(BeTrue())
	Expect(retryable.RequeueAfter()).To(Equal(time.Second))
	var backoff interface{ UseExponentialBackoff() bool }
	Expect(errors.As(err, &backoff)).To(BeTrue())
	Expect(backoff.UseExponentialBackoff()).To(BeFalse())
}

func TestReconcilerDoesNotAccumulateAttemptsForFixedIntervalRetries(t *testing.T) {
	RegisterTestingT(t)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	object := privatev1.VirtualNetwork_builder{Id: "vn-1"}.Build()
	reconciler := &Reconciler[*privatev1.VirtualNetwork]{
		objectChannel: make(chan *privatev1.VirtualNetwork),
		retryAttempts: map[string]uint8{"vn-1": 5},
	}
	defer reconciler.stopRetries()

	reconciler.requeue(ctx, object, RequeueAtInterval(errors.New("deletion is still in progress"), 10*time.Millisecond))

	Expect(reconciler.retryAttempts).ToNot(HaveKey("vn-1"))

	select {
	case got := <-reconciler.objectChannel:
		Expect(got).To(BeIdenticalTo(object))
	case <-time.After(100 * time.Millisecond):
		t.Fatal("fixed-interval retry did not use the requested delay")
	}
}

func TestRequeueDelayUsesExponentialBackoffWithOneMinuteCap(t *testing.T) {
	RegisterTestingT(t)

	tests := []struct {
		name    string
		initial time.Duration
		attempt uint8
		want    time.Duration
	}{
		{name: "first attempt", initial: time.Second, attempt: 0, want: time.Second},
		{name: "second attempt", initial: time.Second, attempt: 1, want: 2 * time.Second},
		{name: "capped attempt", initial: 2 * time.Second, attempt: 8, want: maxRequeueDelay},
		{name: "initial delay above cap", initial: 2 * time.Minute, attempt: 0, want: maxRequeueDelay},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			Expect(requeueDelay(test.initial, test.attempt)).To(Equal(test.want))
		})
	}
}

func TestReconcilerRequeuesRequestedErrorsOncePerObject(t *testing.T) {
	RegisterTestingT(t)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	object := privatev1.VirtualNetwork_builder{Id: "vn-1"}.Build()
	duplicate := privatev1.VirtualNetwork_builder{Id: "vn-1"}.Build()
	reconciler := &Reconciler[*privatev1.VirtualNetwork]{objectChannel: make(chan *privatev1.VirtualNetwork)}
	defer reconciler.stopRetries()

	reconciler.requeue(ctx, object, RequeueAfter(errors.New("hub is not ready"), 10*time.Millisecond))
	reconciler.requeue(ctx, duplicate, RequeueAfter(errors.New("hub is not ready"), 10*time.Millisecond))

	var got *privatev1.VirtualNetwork
	select {
	case got = <-reconciler.objectChannel:
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for the reconciler to requeue the object")
	}
	Expect(got).To(BeIdenticalTo(object))

	select {
	case <-reconciler.objectChannel:
		t.Fatal("the same object was requeued more than once")
	case <-time.After(30 * time.Millisecond):
	}

}

func TestReconcilerDoesNotRequeueErrorsWithoutARequestedDelay(t *testing.T) {
	RegisterTestingT(t)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	object := privatev1.VirtualNetwork_builder{Id: "vn-1"}.Build()
	reconciler := &Reconciler[*privatev1.VirtualNetwork]{objectChannel: make(chan *privatev1.VirtualNetwork)}

	reconciler.requeue(ctx, object, errors.New("permanent reconciliation error"))

	select {
	case <-reconciler.objectChannel:
		t.Fatal("unexpectedly requeued a non-retryable error")
	case <-time.After(30 * time.Millisecond):
	}
}

func TestReconcilerCancelsPendingRetryAfterSuccess(t *testing.T) {
	RegisterTestingT(t)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	object := privatev1.VirtualNetwork_builder{Id: "vn-1"}.Build()
	reconciler := &Reconciler[*privatev1.VirtualNetwork]{objectChannel: make(chan *privatev1.VirtualNetwork)}
	defer reconciler.stopRetries()

	reconciler.requeue(ctx, object, RequeueAfter(errors.New("hub is not ready"), 50*time.Millisecond))
	reconciler.cancelRetry(object.GetId())

	select {
	case <-reconciler.objectChannel:
		t.Fatal("successful reconciliation should cancel the pending retry")
	case <-time.After(100 * time.Millisecond):
	}
}

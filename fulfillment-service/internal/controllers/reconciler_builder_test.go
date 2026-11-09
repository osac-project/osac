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
	"log/slog"
	"testing"
	"time"

	. "github.com/onsi/gomega"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/status"

	osactesting "github.com/osac-project/osac/fulfillment-service/internal/testing"
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

func TestConflictLogLevelWarnsOnceBackoffReachesTheCap(t *testing.T) {
	RegisterTestingT(t)

	// Delays go 1, 2, 4, 8, 16, 32, then the 60s cap, so the seventh conflict is the first at the cap.
	reconciler := &Reconciler[*privatev1.VirtualNetwork]{retryAttempts: map[string]uint8{}}
	for attempt := uint8(0); attempt < 6; attempt++ {
		reconciler.retryAttempts["vn-1"] = attempt
		Expect(reconciler.conflictLogLevel("vn-1")).To(Equal(slog.LevelDebug), "attempt %d", attempt)
	}
	for _, attempt := range []uint8{6, 7, 255} {
		reconciler.retryAttempts["vn-1"] = attempt
		Expect(reconciler.conflictLogLevel("vn-1")).To(Equal(slog.LevelWarn), "attempt %d", attempt)
	}

	Expect(reconciler.conflictLogLevel("other")).To(Equal(slog.LevelDebug))
}

func TestReconcilerLogsRepeatedConflictsAtWarn(t *testing.T) {
	RegisterTestingT(t)

	logs := &osactesting.LogRecorder{}
	conflict := func(context.Context, *privatev1.Tenant) error {
		return status.Error(codes.Aborted, "object has been modified")
	}

	// After six prior conflicts, the next retry is the first at the cap.
	startTenantReconciler(t, &tenantGetServer{}, slog.New(logs), conflict, func(r *Reconciler[*privatev1.Tenant]) {
		r.retryAttempts["t1"] = 6
	})

	Eventually(func() []slog.Level {
		return logs.Levels("Reconciliation keeps conflicting with concurrent updates, will retry")
	}, 5*time.Second).Should(ConsistOf(slog.LevelWarn))
	Expect(logs.Levels("Reconciliation conflicted with a concurrent update, will retry")).To(BeEmpty())
	Expect(logs.Levels("Reconciliation failed")).To(BeEmpty())
}

func TestReconcilerClearsRetryStateWhenTheObjectIsGone(t *testing.T) {
	RegisterTestingT(t)

	unexpected := func(context.Context, *privatev1.Tenant) error {
		t.Error("the reconciler function must not run for an object that is gone")
		return nil
	}

	// Without cleanup, a later object with the same id would inherit this backoff.
	pending := &scheduledRetry{timer: time.NewTimer(time.Hour), cancel: make(chan struct{})}
	defer pending.timer.Stop()
	reconciler := startTenantReconciler(t, &tenantGetServer{gone: true}, slog.New(&osactesting.LogRecorder{}),
		unexpected, func(r *Reconciler[*privatev1.Tenant]) {
			r.retryAttempts["t1"] = 3
			r.retryTimers["t1"] = pending
		})

	Eventually(pending.cancel, 5*time.Second).Should(BeClosed())
	reconciler.retryMu.Lock()
	defer reconciler.retryMu.Unlock()
	Expect(reconciler.retryAttempts).ToNot(HaveKey("t1"))
	Expect(reconciler.retryTimers).ToNot(HaveKey("t1"))
}

// startTenantReconciler runs prepare before Start, then sends tenant "t1". It stops when the test ends.
func startTenantReconciler(t *testing.T, tenants privatev1.TenantsServer, logger *slog.Logger,
	function func(context.Context, *privatev1.Tenant) error,
	prepare func(*Reconciler[*privatev1.Tenant])) *Reconciler[*privatev1.Tenant] {
	t.Helper()
	server := osactesting.NewServer()
	t.Cleanup(server.Stop)
	privatev1.RegisterTenantsServer(server.Registrar(), tenants)
	server.Start()
	connection, err := grpc.NewClient(server.Address(), grpc.WithTransportCredentials(insecure.NewCredentials()))
	Expect(err).ToNot(HaveOccurred())
	t.Cleanup(func() {
		_ = connection.Close()
	})

	reconciler, err := NewReconciler[*privatev1.Tenant]().
		SetLogger(logger).
		SetName("test").
		SetClient(connection).
		SetSync(false).
		SetWatchInterval(time.Hour).
		SetFunction(function).
		Build()
	Expect(err).ToNot(HaveOccurred())

	// Start hasn't run, so no lock is needed.
	prepare(reconciler)

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		done <- reconciler.Start(ctx)
	}()
	t.Cleanup(func() {
		cancel()
		<-done
	})

	select {
	case reconciler.objectChannel <- privatev1.Tenant_builder{Id: "t1"}.Build():
	case <-time.After(5 * time.Second):
		t.Fatal("timed out sending the object to the reconciler")
	}
	return reconciler
}

// tenantGetServer serves the pre-reconcile read; gone makes it return NotFound.
type tenantGetServer struct {
	privatev1.UnimplementedTenantsServer
	gone bool
}

// Get returns NotFound once the tenant is gone.
func (s *tenantGetServer) Get(
	_ context.Context,
	request *privatev1.TenantsGetRequest,
) (*privatev1.TenantsGetResponse, error) {
	if s.gone {
		return nil, status.Error(codes.NotFound, "archived")
	}
	return privatev1.TenantsGetResponse_builder{
		Object: privatev1.Tenant_builder{Id: request.GetId()}.Build(),
	}.Build(), nil
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

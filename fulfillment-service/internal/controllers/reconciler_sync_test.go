/*
Copyright (c) 2026 Red Hat Inc.

Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except in compliance with the
License. You may obtain a copy of the License at

  http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on an
"AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the specific
language governing permissions and limitations under the License.
*/

package controllers_test

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sync/atomic"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"

	"github.com/osac-project/osac/fulfillment-service/internal/controllers"
	"github.com/osac-project/osac/fulfillment-service/internal/testing"
	privatev1 "github.com/osac-project/osac/proto/gen/osac/private/v1"
)

var _ = Describe("Reconciler", func() {
	var (
		builder    *controllers.ReconcilerBuilder[*privatev1.Tenant]
		service    *reconcilerTestServer
		reconciled chan string
	)

	BeforeEach(func() {
		service = &reconcilerTestServer{
			events: make(chan *privatev1.Event, 10),
		}
		server := testing.NewServer()
		DeferCleanup(server.Stop)
		privatev1.RegisterTenantsServer(server.Registrar(), service)
		privatev1.RegisterEventsServer(server.Registrar(), service)
		server.Start()
		connection, err := grpc.NewClient(server.Address(),
			grpc.WithTransportCredentials(insecure.NewCredentials()),
		)
		Expect(err).ToNot(HaveOccurred())
		DeferCleanup(connection.Close)

		reconciled = make(chan string, 10)
		builder = controllers.NewReconciler[*privatev1.Tenant]().
			SetLogger(slog.New(slog.NewTextHandler(GinkgoWriter, nil))).
			SetName("test").
			SetClient(connection).
			SetSyncInterval(10 * time.Millisecond).
			SetWatchInterval(10 * time.Millisecond).
			SetFunction(func(ctx context.Context, object *privatev1.Tenant) error {
				reconciled <- object.GetId()
				return nil
			})
	})

	start := func() {
		reconciler, err := builder.Build()
		Expect(err).ToNot(HaveOccurred())
		ctx, cancel := context.WithCancel(context.Background())
		done := make(chan error, 1)
		go func() {
			done <- reconciler.Start(ctx)
		}()
		DeferCleanup(func() {
			cancel()
			Eventually(done).Should(Receive(MatchError(context.Canceled)))
		})
		Eventually(service.watchCalls.Load).Should(BeNumerically(">=", 1))
	}

	DescribeTable("Synchronizes repeatedly when enabled", func(values []bool) {
		for _, value := range values {
			builder.SetSync(value)
		}
		start()
		// Startup and the initial watch kick can cause two calls; a third requires periodic sync.
		Eventually(service.listCalls.Load).Should(BeNumerically(">=", 3))
	},
		Entry("by default", []bool{}),
		Entry("explicitly", []bool{true}),
		Entry("after disabling it on the builder", []bool{false, true}),
	)

	It("Disables startup, periodic and watch-restart syncs while reconciling watched objects", func() {
		builder.SetSync(false)
		start()

		// A nil entry closes the watch so it restarts.
		service.events <- nil
		Eventually(service.watchCalls.Load).Should(BeNumerically(">=", 2))
		service.events <- privatev1.Event_builder{
			Tenant: privatev1.Tenant_builder{Id: "watched-tenant"}.Build(),
		}.Build()
		Eventually(reconciled).Should(Receive(Equal("watched-tenant")))
		Consistently(service.listCalls.Load, 100*time.Millisecond).Should(BeZero())
	})

	DescribeTable("Reconciles dependencies from events when sync is disabled", func(event *privatev1.Event) {
		service.items = []*privatev1.Tenant{privatev1.Tenant_builder{Id: "dependent-tenant"}.Build()}
		builder.SetSync(false)
		start()
		Consistently(service.listCalls.Load, 100*time.Millisecond).Should(BeZero())

		service.events <- event
		Eventually(reconciled).Should(Receive(Equal("dependent-tenant")))
		Expect(service.listCalls.Load()).To(Equal(int32(1)))
		Consistently(service.listCalls.Load, 100*time.Millisecond).Should(Equal(int32(1)))
	},
		Entry("VirtualNetwork readiness", privatev1.Event_builder{
			VirtualNetwork: privatev1.VirtualNetwork_builder{Id: "default-network"}.Build(),
		}.Build()),
		Entry("Hub availability", privatev1.Event_builder{
			Hub: privatev1.Hub_builder{Id: "hub"}.Build(),
		}.Build()),
		Entry("an event without an object payload", &privatev1.Event{}),
	)

	It("Closes the watch stream when a full sync fails", func() {
		service.failList.Store(true)
		builder.SetSync(false)
		start()

		// An event of a related resource starts a full sync, which fails and ends the watch.
		service.events <- privatev1.Event_builder{
			VirtualNetwork: privatev1.VirtualNetwork_builder{Id: "default-network"}.Build(),
		}.Build()
		Eventually(service.watchCalls.Load).Should(BeNumerically(">=", 2))

		// The server sees one consumer: an abandoned stream would keep its partitions and swallow events.
		Eventually(service.activeWatches.Load).Should(Equal(int32(1)))
		Consistently(service.activeWatches.Load, 100*time.Millisecond).Should(Equal(int32(1)))
	})

	It("skips stale provisioning and deletion events after NotFound and processes the next fresh object", func() {
		fresh := privatev1.Tenant_builder{
			Id:       "valid",
			Metadata: privatev1.Metadata_builder{Name: "fresh", Version: 7}.Build(),
		}.Build()
		service.get = func(req *privatev1.TenantsGetRequest) (*privatev1.TenantsGetResponse, error) {
			if req.GetId() != "valid" {
				return nil, status.Error(codes.NotFound, "archived")
			}
			return privatev1.TenantsGetResponse_builder{Object: fresh}.Build(), nil
		}
		objects := make(chan *privatev1.Tenant, 10)
		builder.SetSync(false).SetFunction(func(_ context.Context, object *privatev1.Tenant) error {
			objects <- object
			return nil
		})
		start()
		service.events <- privatev1.Event_builder{
			Tenant: privatev1.Tenant_builder{Id: "stale-provisioning"}.Build(),
		}.Build()
		service.events <- privatev1.Event_builder{
			Tenant: privatev1.Tenant_builder{Id: "stale-deletion", Metadata: privatev1.Metadata_builder{DeletionTimestamp: timestamppb.Now()}.Build()}.Build(),
		}.Build()
		service.events <- privatev1.Event_builder{
			Tenant: privatev1.Tenant_builder{Id: "valid", Metadata: privatev1.Metadata_builder{Name: "stale", Version: 1}.Build()}.Build(),
		}.Build()
		var got *privatev1.Tenant
		Eventually(objects).Should(Receive(&got))
		Expect(got.GetId()).To(Equal("valid"))
		Expect(got.GetMetadata().GetName()).To(Equal("fresh"))
		Expect(got.GetMetadata().GetVersion()).To(Equal(int32(7)))
		Expect(service.getCalls.Load()).To(Equal(int32(3)))
		Consistently(objects, 100*time.Millisecond).ShouldNot(Receive())
	})

	DescribeTable("Rejects nonpositive sync intervals when enabled", func(interval time.Duration) {
		_, err := builder.SetSyncInterval(interval).Build()
		Expect(err).To(MatchError(ContainSubstring("sync interval should be positive")))
	},
		Entry("zero", time.Duration(0)),
		Entry("negative", -time.Second),
	)

	It("Ignores the sync interval when sync is disabled", func() {
		builder.SetSync(false).SetSyncInterval(0)
		start()
		Consistently(service.listCalls.Load, 100*time.Millisecond).Should(BeZero())
	})

	// The object is cloned on entry so in-place changes by the function don't affect it.
	type received struct {
		object *privatev1.Tenant
		at     time.Time
	}

	// recordCalls records each call; result picks the return value by call number, starting at 1.
	recordCalls := func(result func(call int32, object *privatev1.Tenant) error) (chan received, *atomic.Int32) {
		objects := make(chan received, 10)
		var calls atomic.Int32
		builder.SetSync(false).SetFunction(func(_ context.Context, object *privatev1.Tenant) error {
			call := calls.Add(1)
			objects <- received{object: proto.Clone(object).(*privatev1.Tenant), at: time.Now()}
			return result(call, object)
		})
		return objects, &calls
	}

	pushTenantEvent := func(id string) {
		service.events <- privatev1.Event_builder{
			Tenant: privatev1.Tenant_builder{Id: id}.Build(),
		}.Build()
	}

	DescribeTable("Retries conflicting reconciliations with a fresh read",
		func(firstErr error) {
			// Each read returns a new version, exposing a retry that reuses the queued snapshot.
			service.get = func(req *privatev1.TenantsGetRequest) (*privatev1.TenantsGetResponse, error) {
				return privatev1.TenantsGetResponse_builder{
					Object: privatev1.Tenant_builder{
						Id:       req.GetId(),
						Metadata: privatev1.Metadata_builder{Version: service.getCalls.Load()}.Build(),
					}.Build(),
				}.Build(), nil
			}
			objects, _ := recordCalls(func(call int32, _ *privatev1.Tenant) error {
				if call == 1 {
					return firstErr
				}
				return nil
			})
			start()
			pushTenantEvent("t1")

			var first, second received
			Eventually(objects, 5*time.Second).Should(Receive(&first))
			Eventually(objects, 5*time.Second).Should(Receive(&second))
			Expect(second.object.GetMetadata().GetVersion()).To(BeNumerically(">", first.object.GetMetadata().GetVersion()))
			Expect(service.getCalls.Load()).To(BeNumerically(">=", 2))
			Expect(second.at.Sub(first.at)).To(BeNumerically(">=", 900*time.Millisecond))
		},
		Entry("bare Aborted",
			status.Error(codes.Aborted, "object has been modified")),
		Entry("wrapped Aborted",
			fmt.Errorf("update volume: %w", status.Error(codes.Aborted, "modified"))),
		Entry("Aborted joined behind another gRPC error",
			errors.Join(status.Error(codes.Unavailable, "hub down"), status.Error(codes.Aborted, "modified"))),
	)

	// Conflict-only failures are expected under contention and logged quietly; anything mixed is an error.
	const (
		conflictMessage = "Reconciliation conflicted with a concurrent update, will retry"
		failureMessage  = "Reconciliation failed"
	)
	DescribeTable("Logs a failed reconciliation at the level that matches its cause",
		func(firstErr error, wantMessage string, wantLevel slog.Level, otherMessage string) {
			logs := &testing.LogRecorder{}
			builder.SetLogger(slog.New(logs))
			objects, _ := recordCalls(func(call int32, _ *privatev1.Tenant) error {
				if call == 1 {
					return firstErr
				}
				return nil
			})
			start()
			pushTenantEvent("t1")
			Eventually(objects, 5*time.Second).Should(Receive())

			Eventually(func() []slog.Level { return logs.Levels(wantMessage) }, 5*time.Second).Should(
				ConsistOf(wantLevel))
			Expect(logs.Levels(otherMessage)).To(BeEmpty())
		},
		Entry("bare Aborted",
			status.Error(codes.Aborted, "object has been modified"), conflictMessage, slog.LevelDebug, failureMessage),
		Entry("wrapped Aborted",
			fmt.Errorf("update volume: %w", status.Error(codes.Aborted, "modified")),
			conflictMessage, slog.LevelDebug, failureMessage),
		Entry("Aborted joined behind another gRPC error",
			errors.Join(status.Error(codes.Unavailable, "hub down"), status.Error(codes.Aborted, "modified")),
			failureMessage, slog.LevelError, conflictMessage),
		Entry("Aborted that already carries a retry interval",
			controllers.RequeueAfter(status.Error(codes.Aborted, "modified"), time.Second),
			failureMessage, slog.LevelError, conflictMessage),
		Entry("an error that is not a conflict",
			status.Error(codes.Internal, "boom"), failureMessage, slog.LevelError, conflictMessage),
	)

	It("does not retry non-conflict errors", func() {
		objects, calls := recordCalls(func(int32, *privatev1.Tenant) error {
			return status.Error(codes.Internal, "boom")
		})
		start()
		pushTenantEvent("t1")
		Eventually(objects, 5*time.Second).Should(Receive())

		// Longer than the conflict delay, so a misclassified retry would show.
		Consistently(calls.Load, 2*time.Second).Should(Equal(int32(1)))
	})

	It("keeps the explicit delay of requeueable errors", func() {
		objects, calls := recordCalls(func(call int32, _ *privatev1.Tenant) error {
			if call == 1 {
				return controllers.RequeueAfter(status.Error(codes.Aborted, "x"), 3*time.Second)
			}
			return nil
		})
		start()
		pushTenantEvent("t1")
		Eventually(objects, 5*time.Second).Should(Receive())

		Consistently(calls.Load, 2*time.Second).Should(Equal(int32(1)))
		Eventually(calls.Load, 5*time.Second).Should(Equal(int32(2)))
	})

	// replaysUnmutated checks a retry replays the object as read, even after in-place changes and a failed refresh.
	replaysUnmutated := func(firstErr error) {
		const controller = "fulfillment-controller"
		const hold = "test.osac.io/hold"
		service.get = func(*privatev1.TenantsGetRequest) (*privatev1.TenantsGetResponse, error) {
			if service.getCalls.Load() == 2 {
				return nil, status.Error(codes.Unavailable, "refresh failed")
			}
			return privatev1.TenantsGetResponse_builder{
				Object: privatev1.Tenant_builder{
					Id:       "t1",
					Metadata: privatev1.Metadata_builder{Finalizers: []string{controller, hold}}.Build(),
				}.Build(),
			}.Build(), nil
		}
		objects, _ := recordCalls(func(call int32, object *privatev1.Tenant) error {
			if call == 1 {
				// Remove the finalizer in place, as the Volume reconciler does.
				object.GetMetadata().SetFinalizers([]string{hold})
				return firstErr
			}
			return nil
		})
		start()
		pushTenantEvent("t1")

		var first, second received
		Eventually(objects, 5*time.Second).Should(Receive(&first))
		Eventually(objects, 5*time.Second).Should(Receive(&second))
		Expect(first.object.GetMetadata().GetFinalizers()).To(ConsistOf(controller, hold))
		Expect(second.object.GetMetadata().GetFinalizers()).To(ConsistOf(controller, hold))
	}

	It("replays an unmutated object when the fresh read fails", func() {
		replaysUnmutated(status.Error(codes.Aborted, "modified"))
	})

	It("replays an unmutated object when the fresh read fails (requeueable)", func() {
		replaysUnmutated(controllers.RequeueAfter(errors.New("x"), time.Second))
	})
})

type reconcilerTestServer struct {
	privatev1.UnimplementedTenantsServer
	privatev1.UnimplementedEventsServer
	events        chan *privatev1.Event
	items         []*privatev1.Tenant
	failList      atomic.Bool
	listCalls     atomic.Int32
	watchCalls    atomic.Int32
	activeWatches atomic.Int32
	getCalls      atomic.Int32
	get           func(*privatev1.TenantsGetRequest) (*privatev1.TenantsGetResponse, error)
}

func (s *reconcilerTestServer) List(context.Context, *privatev1.TenantsListRequest) (*privatev1.TenantsListResponse, error) {
	s.listCalls.Add(1)
	if s.failList.Load() {
		return nil, status.Error(codes.Unavailable, "server is restarting")
	}
	return &privatev1.TenantsListResponse{Items: s.items}, nil
}

func (s *reconcilerTestServer) Get(_ context.Context, request *privatev1.TenantsGetRequest) (*privatev1.TenantsGetResponse, error) {
	s.getCalls.Add(1)
	if s.get != nil {
		return s.get(request)
	}
	return &privatev1.TenantsGetResponse{
		Object: privatev1.Tenant_builder{Id: request.GetId()}.Build(),
	}, nil
}

func (s *reconcilerTestServer) Watch(_ *privatev1.EventsWatchRequest, stream grpc.ServerStreamingServer[privatev1.EventsWatchResponse]) error {
	s.watchCalls.Add(1)
	s.activeWatches.Add(1)
	defer s.activeWatches.Add(-1)
	for {
		select {
		case <-stream.Context().Done():
			return stream.Context().Err()
		case event := <-s.events:
			if event == nil {
				return status.Error(codes.Unavailable, "restart watch")
			}
			if err := stream.Send(&privatev1.EventsWatchResponse{Event: event}); err != nil {
				return err
			}
		}
	}
}

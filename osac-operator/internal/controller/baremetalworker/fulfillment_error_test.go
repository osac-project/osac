// Copyright (c) 2026 Red Hat Inc.
// SPDX-License-Identifier: Apache-2.0

package baremetalworker

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/tools/events"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/osac-project/osac/osac-operator/api/v1alpha1"
	privatev1 "github.com/osac-project/osac/proto/gen/osac/private/v1"
)

type workerClusterErrorClient struct {
	*workerReadClient
	clusterErr error
}

func (f *workerClusterErrorClient) GetCluster(context.Context, string) (*privatev1.Cluster, error) {
	return nil, f.clusterErr
}

type fulfillmentConditionFailureClient struct {
	client.Client
	err error
}

func (c *fulfillmentConditionFailureClient) Status() client.SubResourceWriter {
	return &fulfillmentConditionFailureWriter{SubResourceWriter: c.Client.Status(), err: c.err}
}

type fulfillmentConditionFailureWriter struct {
	client.SubResourceWriter
	err error
}

func (w *fulfillmentConditionFailureWriter) Patch(context.Context, client.Object, client.Patch, ...client.SubResourcePatchOption) error {
	return w.err
}

func fulfillmentConditionRecorded(co *v1alpha1.ClusterOrder) bool {
	for _, condition := range co.Status.Conditions {
		if condition.Type == v1alpha1.ConditionFulfillmentServiceUnavailable && condition.Status == metav1.ConditionTrue {
			return true
		}
	}
	return false
}

// TestR08OrderScopedClassification drives the public reconciler: it is the single
// boundary that persists availability evidence and applies the bounded delay.
func TestR08OrderScopedClassification(t *testing.T) {
	for _, outcome := range []string{"ordinary error", "unavailable", "condition persistence error"} {
		t.Run(outcome, func(t *testing.T) {
			ctx := context.Background()
			r, fc, co := workerReadHarness(t)
			before := co.DeepCopy()
			ordinary := errors.New("ordinary provider failure")
			persistence := errors.New("unavailable condition persistence failed")
			providerErr := ordinary
			if outcome != "ordinary error" {
				providerErr = fmt.Errorf("provider unavailable: %w", ErrFulfillmentServiceUnavailable)
			}
			if outcome == "condition persistence error" {
				r.Client = &fulfillmentConditionFailureClient{Client: r.Client, err: persistence}
			}
			fc.listErr = providerErr

			res, err := r.Reconcile(ctx, ctrl.Request{NamespacedName: client.ObjectKeyFromObject(co)})
			switch outcome {
			case "ordinary error":
				if !errors.Is(err, ordinary) || !res.IsZero() {
					t.Fatalf("ordinary error not passed through: result=%v err=%v", res, err)
				}
			case "unavailable":
				if err != nil || res.RequeueAfter != unavailableBackoff {
					t.Fatalf("unavailable result=%v err=%v, want bounded backoff", res, err)
				}
			case "condition persistence error":
				if !errors.Is(err, persistence) {
					t.Fatalf("error=%v, want condition persistence error", err)
				}
			}

			if err := r.apiReader.Get(ctx, client.ObjectKeyFromObject(co), co); err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(co.Status.Workers, before.Status.Workers) {
				t.Fatal("provider error changed workers")
			}
			if got, want := fulfillmentConditionRecorded(co), outcome == "unavailable"; got != want {
				t.Fatalf("unavailable condition persisted=%v, want %v", got, want)
			}
		})
	}
}

// TestR08AuthoritativeClusterTransportIsNotOwnershipMismatch keeps fail-closed
// ownership while reporting the real transport blocker instead of an ownership
// event.
func TestR08AuthoritativeClusterTransportIsNotOwnershipMismatch(t *testing.T) {
	for _, tt := range []struct {
		name           string
		err            error
		wantEvent      bool
		wantUnavailRes bool
	}{
		{name: "transport", err: fmt.Errorf("rpc: %w", ErrFulfillmentServiceUnavailable), wantUnavailRes: true},
		{name: "semantic", err: status.Error(codes.NotFound, "cluster missing"), wantEvent: true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			ctx := context.Background()
			r, fc, co := workerReadHarness(t)
			r.fulfillment = &workerClusterErrorClient{workerReadClient: fc, clusterErr: tt.err}
			res, err := r.Reconcile(ctx, ctrl.Request{NamespacedName: client.ObjectKeyFromObject(co)})
			if tt.wantUnavailRes {
				if err != nil || res.RequeueAfter != unavailableBackoff {
					t.Fatalf("transport cluster lookup result=%v err=%v, want bounded backoff", res, err)
				}
			} else if err == nil || !strings.Contains(err.Error(), tt.err.Error()) {
				t.Fatalf("error=%v, want real blocker %v", err, tt.err)
			}
			if got := recordedEvent(r.recorder, "WorkerOwnershipMismatch"); got != tt.wantEvent {
				t.Fatalf("ownership event recorded=%v, want %v", got, tt.wantEvent)
			}
			if err := r.apiReader.Get(ctx, client.ObjectKeyFromObject(co), co); err != nil {
				t.Fatal(err)
			}
			if got, want := fulfillmentConditionRecorded(co), tt.wantUnavailRes; got != want {
				t.Fatalf("unavailable condition persisted=%v, want %v", got, want)
			}
		})
	}
}

func recordedEvent(recorder events.EventRecorder, reason string) bool {
	fake, ok := recorder.(*events.FakeRecorder)
	if !ok {
		return false
	}
	for {
		select {
		case event := <-fake.Events:
			if strings.Contains(event, reason) {
				return true
			}
		default:
			return false
		}
	}
}

// r08CallCases returns one fresh adapter per case so a classification can only
// come from that call, never from state shared with an earlier call.
func r08CallCases(err error) []struct {
	name   string
	invoke func() error
} {
	newClient := func() FulfillmentClient {
		return NewFulfillmentClient(
			&fakeBMIClient{err: err, object: &privatev1.BareMetalInstance{}},
			&fakeCVClient{err: err, object: &privatev1.ClusterVersion{}},
			&fakeClustersClient{err: err, object: &privatev1.Cluster{}},
			&fakeDiskImagesClient{err: err, object: &privatev1.DiskImage{}},
			&fakeBMICatalogItemsClient{err: err, object: &privatev1.BareMetalInstanceCatalogItem{}},
			&fakeBMITypesClient{err: err, object: &privatev1.BareMetalInstanceType{}},
		)
	}
	ctx := context.Background()
	return []struct {
		name   string
		invoke func() error
	}{
		{"Create", func() error {
			_, err := newClient().CreateBareMetalInstance(ctx, &privatev1.BareMetalInstance{})
			return err
		}},
		{"Delete", func() error { return newClient().DeleteBareMetalInstance(ctx, "id") }},
		{"Get", func() error { _, err := newClient().GetBareMetalInstance(ctx, "id"); return err }},
		{"List", func() error { _, err := newClient().ListBareMetalInstances(ctx, ""); return err }},
		{"GetClusterVersion", func() error { _, err := newClient().GetClusterVersion(ctx, "4.18.0"); return err }},
		{"GetCluster", func() error { _, err := newClient().GetCluster(ctx, "cluster-uuid"); return err }},
		{"GetDiskImage", func() error { _, err := newClient().GetDiskImage(ctx, "rhcos-4.18"); return err }},
		{"CreateBareMetalInstanceCatalogItem", func() error {
			_, err := newClient().CreateBareMetalInstanceCatalogItem(ctx, &privatev1.BareMetalInstanceCatalogItem{})
			return err
		}},
		{"ListBareMetalInstanceCatalogItems", func() error {
			_, err := newClient().ListBareMetalInstanceCatalogItems(ctx, "")
			return err
		}},
		{"GetBareMetalInstanceType", func() error {
			_, err := newClient().GetBareMetalInstanceType(ctx, "bm-standard")
			return err
		}},
	}
}

func TestR08FirstTransportFailureClassified(t *testing.T) {
	for _, code := range []codes.Code{codes.Unavailable, codes.DeadlineExceeded} {
		for _, tc := range r08CallCases(status.Error(code, "transport")) {
			t.Run(code.String()+"/"+tc.name, func(t *testing.T) {
				err := tc.invoke()
				if !errors.Is(err, ErrFulfillmentServiceUnavailable) {
					t.Fatalf("first %s failure not classified: %v", code, err)
				}
				if status.Code(err) != code {
					t.Fatalf("code=%v, want %v", status.Code(err), code)
				}
			})
		}
	}
}

func TestR08SemanticFailuresNeverUnavailable(t *testing.T) {
	for _, code := range []codes.Code{
		codes.NotFound, codes.AlreadyExists, codes.InvalidArgument,
		codes.FailedPrecondition, codes.ResourceExhausted, codes.PermissionDenied,
		codes.Unauthenticated, codes.Internal, codes.Unknown,
	} {
		for _, tc := range r08CallCases(status.Error(code, "semantic")) {
			t.Run(code.String()+"/"+tc.name, func(t *testing.T) {
				err := tc.invoke()
				if errors.Is(err, ErrFulfillmentServiceUnavailable) {
					t.Fatalf("%s misclassified as service unavailability", code)
				}
				if status.Code(err) != code {
					t.Fatalf("code=%v, want %v", status.Code(err), code)
				}
			})
		}
	}
}

func TestR08UnrelatedSuccessDoesNotResetFailureEvidence(t *testing.T) {
	bmi := &fakeBMIClient{err: status.Error(codes.Unavailable, "down"), object: &privatev1.BareMetalInstance{}}
	clusters := &fakeClustersClient{object: &privatev1.Cluster{}}
	c := NewFulfillmentClient(bmi, &fakeCVClient{object: &privatev1.ClusterVersion{}}, clusters,
		&fakeDiskImagesClient{object: &privatev1.DiskImage{}},
		&fakeBMICatalogItemsClient{object: &privatev1.BareMetalInstanceCatalogItem{}},
		&fakeBMITypesClient{object: &privatev1.BareMetalInstanceType{}})
	ctx := context.Background()
	if _, err := c.GetBareMetalInstance(ctx, "id"); !errors.Is(err, ErrFulfillmentServiceUnavailable) {
		t.Fatalf("first failure not classified: %v", err)
	}
	if _, err := c.GetCluster(ctx, "cluster"); err != nil {
		t.Fatalf("unrelated success: %v", err)
	}
	if _, err := c.GetBareMetalInstance(ctx, "id"); !errors.Is(err, ErrFulfillmentServiceUnavailable) {
		t.Fatalf("unrelated success changed failure evidence: %v", err)
	}
}

func TestR08PlainErrorPassesThrough(t *testing.T) {
	plain := errors.New("ordinary provider failure")
	for _, tc := range r08CallCases(plain) {
		t.Run(tc.name, func(t *testing.T) {
			err := tc.invoke()
			if err != plain {
				t.Fatalf("error=%v, want the original plain error", err)
			}
			if errors.Is(err, ErrFulfillmentServiceUnavailable) {
				t.Fatal("plain error misclassified as service unavailability")
			}
		})
	}
}

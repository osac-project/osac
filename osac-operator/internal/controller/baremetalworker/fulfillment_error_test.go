// Copyright (c) 2026 Red Hat Inc.
// SPDX-License-Identifier: Apache-2.0

package baremetalworker

import (
	"context"
	"errors"
	"fmt"
	"strings"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

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

// The public reconciler is the single
// boundary that persists availability evidence and applies the bounded delay.
var _ = Describe("Order-scoped fulfillment failure classification", func() {
	for _, outcome := range []string{"ordinary error", "unavailable", "condition persistence error"} {
		It(outcome, func() {
			ctx := context.Background()
			r, fc, co := workerReadHarness()
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
				Expect(errors.Is(err, ordinary)).To(BeTrue(), "ordinary error not passed through: result=%v err=%v", res, err)
				Expect(res.IsZero()).To(BeTrue(), "ordinary error not passed through: result=%v err=%v", res, err)
			case "unavailable":
				Expect(err).NotTo(HaveOccurred(), "unavailable result=%v err=%v, want bounded backoff", res, err)
				Expect(res.RequeueAfter).To(Equal(unavailableBackoff), "unavailable result=%v err=%v, want bounded backoff", res, err)
			case "condition persistence error":
				Expect(errors.Is(err, persistence)).To(BeTrue(), "error=%v, want condition persistence error", err)
			}

			Expect(r.apiReader.Get(ctx, client.ObjectKeyFromObject(co), co)).To(Succeed())
			Expect(co.Status.Workers).To(Equal(before.Status.Workers), "provider error changed workers")
			if got, want := fulfillmentConditionRecorded(co), outcome == "unavailable"; got != want {
				Fail(fmt.Sprintf("unavailable condition persisted=%v, want %v", got, want))
			}
		})
	}
})

// Cluster transport failures keep ownership fail-closed while reporting
// the real transport blocker instead of an ownership
// event.
var _ = Describe("Cluster lookup transport failures are not ownership mismatches", func() {
	for _, tt := range []struct {
		name           string
		err            error
		wantEvent      bool
		wantUnavailRes bool
	}{
		{name: "transport", err: fmt.Errorf("rpc: %w", ErrFulfillmentServiceUnavailable), wantUnavailRes: true},
		{name: "semantic", err: status.Error(codes.NotFound, "cluster missing"), wantEvent: true},
	} {
		It(tt.name, func() {
			ctx := context.Background()
			r, fc, co := workerReadHarness()
			r.fulfillment = &workerClusterErrorClient{workerReadClient: fc, clusterErr: tt.err}
			res, err := r.Reconcile(ctx, ctrl.Request{NamespacedName: client.ObjectKeyFromObject(co)})
			if tt.wantUnavailRes {
				Expect(err).NotTo(HaveOccurred(), "transport cluster lookup result=%v err=%v, want bounded backoff", res, err)
				Expect(res.RequeueAfter).To(Equal(unavailableBackoff), "transport cluster lookup result=%v err=%v, want bounded backoff", res, err)
			} else {
				Expect(err).To(HaveOccurred())
				Expect(err.Error()).To(ContainSubstring(tt.err.Error()))
			}
			if got := recordedEvent(r.recorder, "WorkerOwnershipMismatch"); got != tt.wantEvent {
				Fail(fmt.Sprintf("ownership event recorded=%v, want %v", got, tt.wantEvent))
			}
			Expect(r.apiReader.Get(ctx, client.ObjectKeyFromObject(co), co)).To(Succeed())
			if got, want := fulfillmentConditionRecorded(co), tt.wantUnavailRes; got != want {
				Fail(fmt.Sprintf("unavailable condition persisted=%v, want %v", got, want))
			}
		})
	}
})

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

var _ = Describe("Classification of the first transport failure", func() {
	for _, code := range []codes.Code{codes.Unavailable, codes.DeadlineExceeded} {
		for _, tc := range r08CallCases(status.Error(code, "transport")) {
			It(code.String()+"/"+tc.name, func() {
				err := tc.invoke()
				Expect(errors.Is(err, ErrFulfillmentServiceUnavailable)).To(BeTrue(), "first %s failure not classified: %v", code, err)
				Expect(status.Code(err)).To(Equal(code), "code=%v, want %v", status.Code(err), code)
			})
		}
	}
})

var _ = Describe("Semantic failures are never classified as service unavailability", func() {
	for _, code := range []codes.Code{
		codes.NotFound, codes.AlreadyExists, codes.InvalidArgument,
		codes.FailedPrecondition, codes.ResourceExhausted, codes.PermissionDenied,
		codes.Unauthenticated, codes.Internal, codes.Unknown,
	} {
		for _, tc := range r08CallCases(status.Error(code, "semantic")) {
			It(code.String()+"/"+tc.name, func() {
				err := tc.invoke()
				Expect(errors.Is(err, ErrFulfillmentServiceUnavailable)).To(BeFalse(), "%s misclassified as service unavailability", code)
				Expect(status.Code(err)).To(Equal(code), "code=%v, want %v", status.Code(err), code)
			})
		}
	}
})

var _ = It("preserves failure evidence across an unrelated successful call", func() {
	bmi := &fakeBMIClient{err: status.Error(codes.Unavailable, "down"), object: &privatev1.BareMetalInstance{}}
	clusters := &fakeClustersClient{object: &privatev1.Cluster{}}
	c := NewFulfillmentClient(bmi, &fakeCVClient{object: &privatev1.ClusterVersion{}}, clusters,
		&fakeDiskImagesClient{object: &privatev1.DiskImage{}},
		&fakeBMICatalogItemsClient{object: &privatev1.BareMetalInstanceCatalogItem{}},
		&fakeBMITypesClient{object: &privatev1.BareMetalInstanceType{}})
	ctx := context.Background()
	if _, err := c.GetBareMetalInstance(ctx, "id"); !errors.Is(err, ErrFulfillmentServiceUnavailable) {
		Fail(fmt.Sprintf("first failure not classified: %v", err))
	}
	if _, err := c.GetCluster(ctx, "cluster"); err != nil {
		Fail(fmt.Sprintf("unrelated success: %v", err))
	}
	if _, err := c.GetBareMetalInstance(ctx, "id"); !errors.Is(err, ErrFulfillmentServiceUnavailable) {
		Fail(fmt.Sprintf("unrelated success changed failure evidence: %v", err))
	}
})

var _ = Describe("Plain provider errors pass through unchanged", func() {
	plain := errors.New("ordinary provider failure")
	for _, tc := range r08CallCases(plain) {
		It(tc.name, func() {
			err := tc.invoke()
			Expect(err).To(BeIdenticalTo(plain), "error=%v, want the original plain error", err)
			Expect(errors.Is(err, ErrFulfillmentServiceUnavailable)).To(BeFalse(), "plain error misclassified as service unavailability")
		})
	}
})

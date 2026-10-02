// Copyright (c) 2026 Red Hat Inc.
// SPDX-License-Identifier: Apache-2.0

package baremetalworker

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"testing"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/osac-project/osac/osac-operator/api/v1alpha1"
	privatev1 "github.com/osac-project/osac/proto/gen/osac/private/v1"
)

type fulfillmentErrorClient struct {
	*workerReadClient
	err error
}

func (f *fulfillmentErrorClient) GetBareMetalInstanceType(context.Context, string) (*privatev1.BareMetalInstanceType, error) {
	return nil, f.err
}
func (f *fulfillmentErrorClient) CreateBareMetalInstance(context.Context, *privatev1.BareMetalInstance) (*privatev1.BareMetalInstance, error) {
	return nil, f.err
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

func TestFulfillmentErrorCallers(t *testing.T) {
	for _, operation := range []string{"observation", "capacity", "instance type", "create"} {
		for _, outcome := range []string{"ordinary error", "unavailable", "condition persistence error"} {
			t.Run(operation+"/"+outcome, func(t *testing.T) {
				ctx := context.Background()
				r, fc, co := workerReadHarness(t)
				before := co.DeepCopy()
				ordinary := errors.New("ordinary provider failure")
				persistence := errors.New("unavailable condition persistence failed")
				providerErr := ordinary
				wantErr := ordinary
				if outcome != "ordinary error" {
					providerErr = fmt.Errorf("provider unavailable: %w", ErrFulfillmentServiceUnavailable)
					wantErr = nil
				}
				if outcome == "condition persistence error" {
					r.Client = &fulfillmentConditionFailureClient{Client: r.Client, err: persistence}
					wantErr = persistence
				}
				fc.listErr = providerErr
				r.fulfillment = &fulfillmentErrorClient{workerReadClient: fc, err: providerErr}
				var res ctrl.Result
				var err error
				switch operation {
				case "observation":
					_, res, err = r.observeWorkerResources(ctx, co)
				case "capacity":
					res, err = r.reconcileWorkerCapacity(ctx, co, "tenant", nil, nil)
				case "instance type":
					_, res, err = r.resolveNodeSetInstanceType(ctx, co, "standard")
				case "create":
					_, res, err = r.ensureBMI(ctx, co, "tenant", v1alpha1.NodeRequest{NodeSet: "standard", BareMetal: &v1alpha1.BareMetalNodeSpec{InstanceType: "standard"}}, "reserved", nil, "{}", "filter", "data-0")
				}
				if !errors.Is(err, wantErr) {
					t.Fatalf("error=%v, want %v", err, wantErr)
				}
				if outcome == "unavailable" {
					if res.RequeueAfter != unavailableBackoff {
						t.Fatalf("result=%v, want unavailable backoff", res)
					}
				} else if !res.IsZero() {
					t.Fatalf("error should not return backoff: %v", res)
				}
				if err := r.apiReader.Get(ctx, client.ObjectKeyFromObject(co), co); err != nil {
					t.Fatal(err)
				}
				if !reflect.DeepEqual(co.Status.Workers, before.Status.Workers) {
					t.Fatal("provider error changed workers")
				}
				found := false
				for _, condition := range co.Status.Conditions {
					if condition.Type == v1alpha1.ConditionFulfillmentServiceUnavailable && condition.Status == metav1.ConditionTrue {
						found = true
					}
				}
				if found != (outcome == "unavailable") {
					t.Fatalf("unavailable condition persisted=%v, outcome=%s", found, outcome)
				}
			})
		}
	}
}

// Copyright (c) 2026 Red Hat Inc.
// SPDX-License-Identifier: Apache-2.0

package baremetalworker

import (
	"context"
	"reflect"
	"sync"
	"testing"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/osac-project/osac/osac-operator/api/v1alpha1"
	privatev1 "github.com/osac-project/osac/proto/gen/osac/private/v1"
)

// R05-U1: one logical BMI List and one Agent observation stage per invocation;
// a recorded ID that the List already returned needs no BMI Get.
func TestR05StableReadBudget(t *testing.T) {
	r, fc, co := workerReadHarness(t)
	bmi := ownedBMIFixture(co, "recorded-bmi", "recorded-id")
	fc.listed = []*privatev1.BareMetalInstance{bmi}
	fc.bmis = []*privatev1.BareMetalInstance{bmi}
	agent := agentPhaseFixture("", false)
	agent.SetNamespace(co.Namespace)
	agent.SetLabels(map[string]string{infraEnvAgentLabel: co.Name + infraEnvNameSuffix})
	if err := r.Create(context.Background(), agent); err != nil {
		t.Fatal(err)
	}

	observed, res, err := r.observeWorkerResources(context.Background(), co)
	if err != nil || !res.IsZero() {
		t.Fatalf("observation: result=%+v err=%v", res, err)
	}
	if observed.agents == nil || len(observed.agents.Items) != 1 {
		t.Fatalf("Agent observation stage missed the Agent: %+v", observed.agents)
	}
	if _, err := r.observeExistingWorkers(context.Background(), co, "tenant", observed); err != nil {
		t.Fatal(err)
	}
	if fc.lists != 1 || fc.gets != 0 {
		t.Fatalf("observation budget: lists=%d gets=%d, want 1/0", fc.lists, fc.gets)
	}
}

// R05-U2: a recorded ID omitted from the List is resolved by at most one
// fallback Get per invocation, for success, NotFound and error alike. Fresh
// destructive checks (cleanupWorker) remain explicit exceptions.
func TestR05FallbackMemoizesEveryOutcome(t *testing.T) {
	for _, tt := range []struct {
		name string
		err  error
		bmis []*privatev1.BareMetalInstance
	}{
		{name: "success", bmis: nil},
		{name: "notfound", err: status.Error(codes.NotFound, "gone")},
		{name: "error", err: status.Error(codes.Internal, "unknown")},
	} {
		t.Run(tt.name, func(t *testing.T) {
			r, fc, co := bmiStageHarness(t, workerPhaseWaitingForAgent, "recorded-id")
			if tt.name == "success" {
				tt.bmis = []*privatev1.BareMetalInstance{ownedBMIFixture(co, "recorded-bmi", "recorded-id")}
			}
			fc.bmis = tt.bmis
			fc.getErr = tt.err
			r.macResolver = nil
			observed := indexWorkerBMIs(nil)
			observed.agents = &unstructured.UnstructuredList{}
			_, _ = r.observeExistingWorkers(context.Background(), co, "tenant", observed)
			// A later stage reusing the same invocation snapshot must not pay for
			// the same fallback Get again.
			resolve := r.workerMACResolver(observed)
			for range 3 {
				_ = resolve(context.Background(), "recorded-id")
			}
			if fc.gets != 1 {
				t.Fatalf("fallback Gets=%d, want 1", fc.gets)
			}
		})
	}
}

// R05-U3: a mutation ends the invocation. A successful BMI Create is not written
// back into the observation, and no synthetic NotFound repairs a Delete.
func TestR05MutationDiscardsSnapshot(t *testing.T) {
	r, fc, co := nodeSetHarness(t, "r05-mutation", nodeRequest("standard", 1))
	if _, err := r.reserveWorkerSlots(context.Background(), co); err != nil {
		t.Fatal(err)
	}
	if err := r.apiReader.Get(context.Background(), client.ObjectKeyFromObject(co), co); err != nil {
		t.Fatal(err)
	}
	observed := indexWorkerBMIs(nil)
	res, err := r.reconcileDueWorkerCreation(context.Background(), co, "tenant", workerCreationInputs{}, observed)
	if err != nil {
		t.Fatal(err)
	}
	if res.IsZero() {
		t.Fatal("create did not return at its durable boundary")
	}
	if len(fc.names) != 1 {
		t.Fatalf("creates=%v, want one", fc.names)
	}
	if len(observed.byID) != 0 || len(observed.byName) != 0 {
		t.Fatalf("post-mutation index repair leaked into the observation: byID=%v byName=%v", observed.byID, observed.byName)
	}
}

// R05-U4: reconciled orders keep independent observation state; concurrent use
// never mutates reconciler fields, and a configured MAC resolver still wins.
func TestR05NoSharedObservationState(t *testing.T) {
	ctx := context.Background()
	r := &Reconciler{}
	order := &v1alpha1.ClusterOrder{ObjectMeta: metav1.ObjectMeta{Name: "order"}}
	first := indexWorkerBMIs([]*privatev1.BareMetalInstance{ownedBMIFixture(order, "a", "id-a")})
	second := indexWorkerBMIs([]*privatev1.BareMetalInstance{ownedBMIFixture(order, "b", "id-b")})

	var wg sync.WaitGroup
	for _, observed := range []*workerObservation{first, second} {
		wg.Add(1)
		go func(o *workerObservation) {
			defer wg.Done()
			for range 100 {
				if len(o.byName) != 1 || len(o.byID) != 1 {
					t.Errorf("observation index mutated: byID=%v byName=%v", o.byID, o.byName)
				}
				_ = r.workerMACResolver(o)
			}
		}(observed)
	}
	wg.Wait()
	if len(first.byID) != 1 || len(second.byID) != 1 {
		t.Fatalf("observations shared index state: first=%v second=%v", first.byID, second.byID)
	}
	if r.macResolver != nil {
		t.Fatal("observation mutated the reconciler resolver")
	}
	r.SetMACResolver(func(context.Context, string) []string { return []string{"override"} })
	if got := r.workerMACResolver(first)(ctx, "id-a"); !reflect.DeepEqual(got, []string{"override"}) {
		t.Fatalf("configured override ignored: %v", got)
	}
}

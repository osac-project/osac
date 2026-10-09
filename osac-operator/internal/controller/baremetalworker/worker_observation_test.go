// Copyright (c) 2026 Red Hat Inc.
// SPDX-License-Identifier: Apache-2.0

package baremetalworker

import (
	"context"
	"fmt"
	"reflect"
	"sync"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

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
var _ = It("observes listed workers with one BMI List and no fallback Get", func() {
	r, fc, co := workerReadHarness()
	bmi := ownedBMIFixture(co, "recorded-bmi", "recorded-id")
	fc.listed = []*privatev1.BareMetalInstance{bmi}
	fc.bmis = []*privatev1.BareMetalInstance{bmi}
	agent := agentPhaseFixture("", false)
	agent.SetNamespace(co.Namespace)
	agent.SetLabels(map[string]string{infraEnvAgentLabel: co.Name + infraEnvNameSuffix})
	Expect(r.Create(context.Background(), agent)).To(Succeed())

	observed, res, err := r.observeWorkerResources(context.Background(), co)
	Expect(err).NotTo(HaveOccurred(), "observation: result=%+v err=%v", res, err)
	Expect(res.IsZero()).To(BeTrue(), "observation: result=%+v err=%v", res, err)
	Expect(observed.agents).NotTo(BeNil(), "Agent observation stage missed the Agent: %+v", observed.agents)
	Expect(observed.agents.Items).To(HaveLen(1), "Agent observation stage missed the Agent: %+v", observed.agents)
	if _, err := r.observeExistingWorkers(context.Background(), co, "tenant", observed); err != nil {
		Expect(err).NotTo(HaveOccurred())
	}
	Expect(fc.lists).To(Equal(1), "observation budget: lists=%d gets=%d, want 1/0", fc.lists, fc.gets)
	Expect(fc.gets).To(Equal(0), "observation budget: lists=%d gets=%d, want 1/0", fc.lists, fc.gets)
})

// R05-U2: a recorded ID omitted from the List is resolved by at most one
// fallback Get per invocation, for success, NotFound and error alike. Fresh
// destructive checks (cleanupWorker) remain explicit exceptions.
var _ = Describe("Invocation-local fallback Get caching", func() {
	for _, tt := range []struct {
		name string
		err  error
		bmis []*privatev1.BareMetalInstance
	}{
		{name: "success", bmis: nil},
		{name: "notfound", err: status.Error(codes.NotFound, "gone")},
		{name: "error", err: status.Error(codes.Internal, "unknown")},
	} {
		It(tt.name, func() {
			r, fc, co := bmiStageHarness(workerPhaseWaitingForAgent, "recorded-id")
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
			Expect(fc.gets).To(Equal(1), "fallback Gets=%d, want 1", fc.gets)
		})
	}
})

// R05-U3: a mutation ends the invocation. A successful BMI Create is not written
// back into the observation, and no synthetic NotFound repairs a Delete.
var _ = It("discards the observation snapshot after a mutation", func() {
	r, fc, co := nodeSetHarness("r05-mutation", nodeRequest("standard", 1))
	if _, err := r.reserveWorkerSlots(context.Background(), co); err != nil {
		Expect(err).NotTo(HaveOccurred())
	}
	Expect(r.apiReader.Get(context.Background(), client.ObjectKeyFromObject(co), co)).To(Succeed())
	observed := indexWorkerBMIs(nil)
	res, err := r.reconcileDueWorkerCreation(context.Background(), co, "tenant", workerCreationInputs{}, observed)
	Expect(err).NotTo(HaveOccurred())
	Expect(res.IsZero()).To(BeFalse(), "create did not return at its durable boundary")
	Expect(fc.names).To(HaveLen(1), "creates=%v, want one", fc.names)
	Expect(observed.byID).To(BeEmpty(), "post-mutation index repair leaked into the observation: byID=%v byName=%v", observed.byID, observed.byName)
	Expect(observed.byName).To(BeEmpty(), "post-mutation index repair leaked into the observation: byID=%v byName=%v", observed.byID, observed.byName)
})

// R05-U4: reconciled orders keep independent observation state; concurrent use
// never mutates reconciler fields, and a configured MAC resolver still wins.
var _ = It("keeps concurrent observations isolated and honors the configured MAC resolver", func() {
	ctx := context.Background()
	r := &Reconciler{}
	order := &v1alpha1.ClusterOrder{ObjectMeta: metav1.ObjectMeta{Name: "order"}}
	first := indexWorkerBMIs([]*privatev1.BareMetalInstance{ownedBMIFixture(order, "a", "id-a")})
	second := indexWorkerBMIs([]*privatev1.BareMetalInstance{ownedBMIFixture(order, "b", "id-b")})

	var wg sync.WaitGroup
	for _, observed := range []*workerObservation{first, second} {
		wg.Add(1)
		go func(o *workerObservation) {
			defer GinkgoRecover()

			defer wg.Done()
			for range 100 {
				Expect(o.byName).To(HaveLen(1), "observation index mutated: byID=%v byName=%v", o.byID, o.byName)
				Expect(o.byID).To(HaveLen(1), "observation index mutated: byID=%v byName=%v", o.byID, o.byName)
				_ = r.workerMACResolver(o)
			}
		}(observed)
	}
	wg.Wait()
	Expect(first.byID).To(HaveLen(1), "observations shared index state: first=%v second=%v", first.byID, second.byID)
	Expect(second.byID).To(HaveLen(1), "observations shared index state: first=%v second=%v", first.byID, second.byID)
	Expect(r.macResolver).To(BeNil(), "observation mutated the reconciler resolver")
	r.SetMACResolver(func(context.Context, string) []string { return []string{"override"} })
	if got := r.workerMACResolver(first)(ctx, "id-a"); !reflect.DeepEqual(got, []string{"override"}) {
		Fail(fmt.Sprintf("configured override ignored: %v", got))
	}
})

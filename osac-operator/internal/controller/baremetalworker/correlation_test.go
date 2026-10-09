/*
Copyright (c) 2026 Red Hat Inc.

Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except in compliance with the
License. You may obtain a copy of the License at

  http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on an
"AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the specific
language governing permissions and limitations under the License.
*/

package baremetalworker

import (
	"context"
	"errors"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/tools/events"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	clientfake "sigs.k8s.io/controller-runtime/pkg/client/fake"

	"github.com/osac-project/osac/osac-operator/api/v1alpha1"
)

type transientAgentConflictClient struct {
	client.Client
	conflictInjected  bool
	conflictAgentName string
	staleAgent        *unstructured.Unstructured
}

const (
	assistedServiceLabel           = "infraenvs.agent-install.openshift.io"
	assistedServiceLabelValue      = "ci-cluster" + infraEnvNameSuffix
	assistedServiceAnnotation      = "agent-install.openshift.io/assisted-service-update"
	assistedServiceAnnotationValue = "registered"
	assistedServiceStatus          = "registering"
)

func (c *transientAgentConflictClient) Get(
	ctx context.Context, key client.ObjectKey, obj client.Object, opts ...client.GetOption,
) error {
	if c.conflictInjected && c.staleAgent != nil && key == client.ObjectKeyFromObject(c.staleAgent) {
		if stale, ok := obj.(*unstructured.Unstructured); ok {
			*stale = *c.staleAgent.DeepCopy()
			return nil
		}
	}
	return c.Client.Get(ctx, key, obj, opts...)
}

func (c *transientAgentConflictClient) Patch(
	ctx context.Context, obj client.Object, patch client.Patch, opts ...client.PatchOption,
) error {
	if !c.conflictInjected && obj.GetName() == c.conflictAgentName {
		c.conflictInjected = true
		current := &unstructured.Unstructured{}
		current.SetGroupVersionKind(agentGVK)
		if err := c.Client.Get(ctx, client.ObjectKeyFromObject(obj), current); err != nil {
			return err
		}
		labels := current.GetLabels()
		if labels == nil {
			labels = make(map[string]string)
		}
		labels[assistedServiceLabel] = assistedServiceLabelValue
		current.SetLabels(labels)
		annotations := current.GetAnnotations()
		if annotations == nil {
			annotations = make(map[string]string)
		}
		annotations[assistedServiceAnnotation] = assistedServiceAnnotationValue
		current.SetAnnotations(annotations)
		if err := unstructured.SetNestedField(current.Object, assistedServiceStatus, "status", "debugInfo", "state"); err != nil {
			return err
		}
		if err := c.Client.Update(ctx, current); err != nil {
			return err
		}
		return apierrors.NewConflict(
			schema.GroupResource{Group: agentGVK.Group, Resource: "agents"},
			obj.GetName(),
			errors.New("transient test conflict"),
		)
	}
	return c.Client.Patch(ctx, obj, patch, opts...)
}

var _ = It("matches compatible Agents by MAC and skips incompatible evidence", func() {
	makeAgent := func(name string, macs ...string) *unstructured.Unstructured {
		interfaces := make([]interface{}, 0, len(macs))
		for _, mac := range macs {
			interfaces = append(interfaces, map[string]interface{}{"macAddress": mac})
		}
		agent := &unstructured.Unstructured{Object: map[string]interface{}{}}
		agent.SetGroupVersionKind(agentGVK)
		agent.SetName(name)
		agent.SetNamespace("osac")
		agent.SetUID(types.UID(name + "-uid"))
		_ = unstructured.SetNestedSlice(agent.Object, interfaces, "status", "inventory", "interfaces")
		return agent
	}
	workers := []v1alpha1.WorkerStatus{
		{Name: "w-0", Kind: workerKindBMI, BareMetalInstance: v1alpha1.BareMetalInstanceReference{Name: "w-0", ID: "bmi-0"}, Phase: workerPhaseWaitingForAgent},
		{Name: "w-1", Kind: workerKindBMI, BareMetalInstance: v1alpha1.BareMetalInstanceReference{Name: "w-1", ID: "bmi-1"}, Phase: workerPhaseWaitingForAgent},
		{Name: "w-2", Kind: workerKindBMI, BareMetalInstance: v1alpha1.BareMetalInstanceReference{Name: "w-2", ID: "bmi-2"}, Phase: workerPhaseBinding},
	}
	// bmi-1 reports two NICs — correlation must match against any of them.
	hostMACs := map[string][]string{
		"bmi-0": {"aa:bb:cc:dd:ee:00"},
		"bmi-1": {"aa:bb:cc:dd:ee:11", "aa:bb:cc:dd:ee:1f"},
		"bmi-2": {"aa:bb:cc:dd:ee:22"},
	}
	resolver := func(_ context.Context, id string) []string { return hostMACs[id] }
	co := &v1alpha1.ClusterOrder{ObjectMeta: metav1.ObjectMeta{Name: "order", Namespace: "osac"}}

	for _, tt := range []struct {
		name       string
		agentMACs  []string
		wantWorker string
	}{
		{"unique match", []string{"aa:bb:cc:dd:ee:00"}, "w-0"},
		{"case-insensitive match", []string{"AA:BB:CC:DD:EE:11"}, "w-1"},
		{"no match", []string{"ff:ff:ff:ff:ff:ff"}, ""},
		{"empty agent MACs", nil, ""},
		// One Agent whose MAC matches two BMIs is ambiguous in the agent->worker
		// direction and establishes nothing.
		{"agent MAC matches several BMIs", []string{"aa:bb:cc:dd:ee:00", "aa:bb:cc:dd:ee:11"}, ""},
		{"skips workers not in WaitingForAgent phase", []string{"aa:bb:cc:dd:ee:22"}, ""},
		{"multiple interfaces, one matches", []string{"ff:ff:ff:ff:ff:ff", "aa:bb:cc:dd:ee:00"}, "w-0"},
		{"matches a BMI's secondary NIC", []string{"aa:bb:cc:dd:ee:1f"}, "w-1"},
	} {
		By(tt.name)
		func() {
			agent := makeAgent("agent", tt.agentMACs...)
			got := matchUnboundAgents(context.Background(), co, []unstructured.Unstructured{*agent}, workers, resolver)
			var established []string
			for name, association := range got {
				if association.state == agentEstablished {
					established = append(established, name)
				}
			}
			if tt.wantWorker == "" {
				Expect(established).To(BeEmpty(), "established %v, want no association", established)
				return
			}
			Expect(established).To(HaveLen(1), "established %v, want %s", established, tt.wantWorker)
			Expect(established[0]).To(Equal(tt.wantWorker), "established %v, want %s", established, tt.wantWorker)
		}()
	}
})

var _ = Describe("extractAgentMACs", func() {
	DescribeTable("extracts MAC addresses from agent inventory",
		func(obj map[string]interface{}, want []string) {
			agent := &unstructured.Unstructured{Object: obj}
			got := extractAgentMACs(agent)
			if want == nil {
				Expect(got).To(BeNil())
			} else {
				Expect(got).To(Equal(want))
			}
		},
		Entry("single interface",
			map[string]interface{}{
				"status": map[string]interface{}{
					"inventory": map[string]interface{}{
						"interfaces": []interface{}{
							map[string]interface{}{"macAddress": "aa:bb:cc:dd:ee:ff"},
						},
					},
				},
			},
			[]string{"aa:bb:cc:dd:ee:ff"},
		),
		Entry("multiple interfaces",
			map[string]interface{}{
				"status": map[string]interface{}{
					"inventory": map[string]interface{}{
						"interfaces": []interface{}{
							map[string]interface{}{"macAddress": "11:22:33:44:55:66"},
							map[string]interface{}{"macAddress": "aa:bb:cc:dd:ee:ff"},
						},
					},
				},
			},
			[]string{"11:22:33:44:55:66", "aa:bb:cc:dd:ee:ff"},
		),
		Entry("no inventory",
			map[string]interface{}{},
			nil,
		),
		Entry("empty interfaces",
			map[string]interface{}{
				"status": map[string]interface{}{
					"inventory": map[string]interface{}{
						"interfaces": []interface{}{},
					},
				},
			},
			[]string{},
		),
	)
})

var _ = Describe("Agent Installed phase projection", func() {
	makeAgent := func(workerName, conditionStatus string, debugInstalled bool) unstructured.Unstructured {
		agent := unstructured.Unstructured{Object: map[string]interface{}{}}
		agent.SetGroupVersionKind(agentGVK)
		agent.SetLabels(map[string]string{workerNameLabel: workerName})
		if conditionStatus != "" {
			_ = unstructured.SetNestedSlice(agent.Object, []interface{}{
				map[string]interface{}{"type": "Installed", "status": conditionStatus},
			}, "status", "conditions")
		}
		if debugInstalled {
			_ = unstructured.SetNestedField(agent.Object, "installed", "status", "debugInfo", "state")
		}
		return agent
	}

	DescribeTable("advances only when the Agent is installed",
		func(conditionStatus string, debugInstalled bool, wantPhase string) {
			workers := []v1alpha1.WorkerStatus{{
				Name:              "w-0",
				Kind:              workerKindBMI,
				BareMetalInstance: v1alpha1.BareMetalInstanceReference{ID: "bmi-0"},
				Phase:             workerPhaseBinding,
				CreationTimestamp: metav1.Now(),
			}}
			agents := &unstructured.UnstructuredList{Items: []unstructured.Unstructured{
				makeAgent("w-0", conditionStatus, debugInstalled),
			}}

			workers, err := projectAgentWorkerPhases(&v1alpha1.ClusterOrder{}, workers, agents)
			Expect(err).ToNot(HaveOccurred())

			Expect(workers[0].Phase).To(Equal(wantPhase))
		},
		Entry("Installed=True without debug state", "True", false, workerPhaseReady),
		Entry("Installed=False overrides stale debug state", "False", true, workerPhaseBinding),
		Entry("Installed=Unknown overrides stale debug state", "Unknown", true, workerPhaseBinding),
		Entry("legacy debug state when condition is absent", "", true, workerPhaseReady),
	)
})

var _ = Describe("requestedBareMetalWorkersByNodeSet", func() {
	It("keys NodePool capacity by the NodeSet key", func() {
		co := &v1alpha1.ClusterOrder{}
		co.Spec.NodeRequests = []v1alpha1.NodeRequest{{NodeSet: "bm-worker",
			NumberOfNodes: 2,
			BareMetal:     &v1alpha1.BareMetalNodeSpec{InstanceType: "bm-worker"},
		}}
		Expect(requestedBareMetalWorkersByNodeSet(co)).To(Equal(map[string]int64{"bm-worker": 2}))
	})
	It("counts each requested NodeSet before any Agent is correlated", func() {
		co := &v1alpha1.ClusterOrder{}
		co.Spec.NodeRequests = []v1alpha1.NodeRequest{
			{NodeSet: "bm-worker", NumberOfNodes: 2,
				BareMetal: &v1alpha1.BareMetalNodeSpec{InstanceType: "bm-worker"}},
			{NodeSet: "bm-gpu", NumberOfNodes: 3, BareMetal: &v1alpha1.BareMetalNodeSpec{InstanceType: "bm-gpu"}},
		}

		Expect(requestedBareMetalWorkersByNodeSet(co)).To(Equal(map[string]int64{
			"bm-worker": 2,
			"bm-gpu":    3,
		}))
	})
})

var _ = Describe("BareMetalWorker direct ClusterOrder validation", func() {
	It("rejects a bare-metal node request without an instance type before provisioning", func() {
		co := &v1alpha1.ClusterOrder{
			ObjectMeta: metav1.ObjectMeta{Name: "invalid-workers", Namespace: "osac"},
			Spec: v1alpha1.ClusterOrderSpec{NodeRequests: []v1alpha1.NodeRequest{{
				NodeSet: "compute", NumberOfNodes: 2, BareMetal: &v1alpha1.BareMetalNodeSpec{},
			}}},
		}
		s := runtime.NewScheme()
		Expect(v1alpha1.AddToScheme(s)).To(Succeed())
		k8sClient := clientfake.NewClientBuilder().WithScheme(s).WithObjects(co).Build()
		r := &Reconciler{Client: k8sClient, apiReader: k8sClient}

		_, err := r.Reconcile(context.Background(), ctrl.Request{NamespacedName: types.NamespacedName{Name: co.Name, Namespace: co.Namespace}})
		Expect(err).To(MatchError(ContainSubstring("bareMetal.instanceType is required")))
	})
})

var _ = Describe("reconcileNodePoolReplicas", func() {
	It("keeps replica counts isolated for NodeSets sharing an instance type", func() {
		nodePoolGVK := schema.GroupVersionKind{
			Group: "hypershift.openshift.io", Version: "v1beta1", Kind: "NodePool",
		}
		nodePoolListGVK := nodePoolGVK
		nodePoolListGVK.Kind = "NodePoolList"
		s := runtime.NewScheme()
		Expect(v1alpha1.AddToScheme(s)).To(Succeed())
		s.AddKnownTypeWithName(nodePoolGVK, &unstructured.Unstructured{})
		s.AddKnownTypeWithName(nodePoolListGVK, &unstructured.UnstructuredList{})

		co := &v1alpha1.ClusterOrder{
			ObjectMeta: metav1.ObjectMeta{Name: "co-replicas", Namespace: "osac"},
			Spec: v1alpha1.ClusterOrderSpec{
				NodeRequests: []v1alpha1.NodeRequest{
					{NodeSet: "compute", NumberOfNodes: 2,
						BareMetal: &v1alpha1.BareMetalNodeSpec{InstanceType: "bm-standard"}},
					{NodeSet: "batch", NumberOfNodes: 1,
						BareMetal: &v1alpha1.BareMetalNodeSpec{InstanceType: "bm-standard"}},
				},
			},
			Status: v1alpha1.ClusterOrderStatus{
				ClusterReference: &v1alpha1.ClusterOrderClusterReferenceType{Namespace: "workload"},
			},
		}

		makeNodePool := func(name, nodeSet string) *unstructured.Unstructured {
			np := &unstructured.Unstructured{Object: map[string]interface{}{}}
			np.SetGroupVersionKind(nodePoolGVK)
			np.SetName(name)
			np.SetNamespace("workload")
			np.SetLabels(map[string]string{"osac.openshift.io/node-set": nodeSet,
				"osac.openshift.io/clusterorder":  co.Name,
				"osac.openshift.io/instance_type": "bm-standard",
			})
			Expect(unstructured.SetNestedField(np.Object, int64(0), "spec", "replicas")).To(Succeed())
			return np
		}

		k8sClient := clientfake.NewClientBuilder().
			WithScheme(s).
			WithObjects(co, makeNodePool("standard", "compute"), makeNodePool("gpu", "batch")).
			Build()
		r := &Reconciler{Client: k8sClient}

		_, err := r.reconcileNodePoolReplicas(context.Background(), co)
		Expect(err).ToNot(HaveOccurred())

		for name, want := range map[string]int64{"standard": 2, "gpu": 1} {
			np := &unstructured.Unstructured{}
			np.SetGroupVersionKind(nodePoolGVK)
			Expect(k8sClient.Get(context.Background(), types.NamespacedName{
				Name: name, Namespace: "workload",
			}, np)).To(Succeed())
			replicas, found, err := unstructured.NestedInt64(np.Object, "spec", "replicas")
			Expect(err).ToNot(HaveOccurred())
			Expect(found).To(BeTrue())
			Expect(replicas).To(Equal(want), name)
		}
	})
})

var _ = It("unions Agent selectors and deduplicates shared objects", func() {
	s := runtime.NewScheme()
	Expect(v1alpha1.AddToScheme(s)).To(Succeed())
	s.AddKnownTypeWithName(agentGVK, &unstructured.Unstructured{})
	agentListGVK := agentGVK
	agentListGVK.Kind = "AgentList"
	s.AddKnownTypeWithName(agentListGVK, &unstructured.UnstructuredList{})

	co := &v1alpha1.ClusterOrder{ObjectMeta: metav1.ObjectMeta{Name: "order", Namespace: "osac"}}
	makeAgent := func(name, uid string, labels map[string]string) *unstructured.Unstructured {
		agent := &unstructured.Unstructured{Object: map[string]interface{}{}}
		agent.SetGroupVersionKind(agentGVK)
		agent.SetName(name)
		agent.SetNamespace(co.Namespace)
		agent.SetUID(types.UID(uid))
		agent.SetLabels(labels)
		return agent
	}
	infraOnly := makeAgent("infra-only", "uid-infra", map[string]string{
		infraEnvAgentLabel: co.Name + infraEnvNameSuffix,
	})
	clusterOnly := makeAgent("cluster-only", "uid-cluster", map[string]string{
		clusterOrderLabel: co.Name,
	})
	// One object carries both supported selectors; it must not become ambiguous.
	both := makeAgent("both", "uid-both", map[string]string{
		infraEnvAgentLabel: co.Name + infraEnvNameSuffix,
		clusterOrderLabel:  co.Name,
	})
	// A matching selector in another namespace is out of scope.
	foreignNS := makeAgent("foreign-ns", "uid-foreign", map[string]string{
		infraEnvAgentLabel: co.Name + infraEnvNameSuffix,
		clusterOrderLabel:  co.Name,
	})
	foreignNS.SetNamespace("other-namespace")
	c := clientfake.NewClientBuilder().WithScheme(s).WithObjects(infraOnly, clusterOnly, both, foreignNS).Build()
	r := &Reconciler{Client: c, apiReader: c}

	agents, err := r.listAgents(context.Background(), co)
	Expect(err).NotTo(HaveOccurred())
	// One observation stage queries both supported selectors and deduplicates by
	// UID, so a mixed population is never silently truncated to one selector.
	Expect(agents.Items).To(HaveLen(3), "selector union observed %d Agents, want 3", len(agents.Items))
	counts := map[types.UID]int{}
	for i := range agents.Items {
		counts[agents.Items[i].GetUID()]++
	}
	for uid, count := range counts {
		Expect(count).To(Equal(1), "Agent %s observed %d times, want exactly once", uid, count)
	}
})

var _ = It("rejects ambiguous Agents with duplicate worker labels", func() {
	first := agentPhaseFixture("worker", true)
	first.SetUID("uid-first")
	second := agentPhaseFixture("worker", true)
	second.SetName("agent-second")
	second.SetUID("uid-second")
	workers := []v1alpha1.WorkerStatus{
		newWorkerStatus("standard", "standard", "worker", "bmi-0", workerPhaseWaitingForAgent),
	}
	agents := &unstructured.UnstructuredList{Items: []unstructured.Unstructured{*first, *second}}

	co := &v1alpha1.ClusterOrder{ObjectMeta: metav1.ObjectMeta{Name: "order", Namespace: "osac"}}
	got, err := projectAgentWorkerPhases(co, workers, agents)
	Expect(err).ToNot(HaveOccurred(), "duplicate worker labels returned an error instead of failing closed: %v", err)
	// A shared worker-name label is not authorization: readiness needs exactly one
	// distinct compatible UID, so two claimants must fail closed.
	Expect(got[0].Phase).To(Equal(workerPhaseWaitingForAgent), "duplicate worker labels selected a phase %q, want %q", got[0].Phase, workerPhaseWaitingForAgent)
})

var _ = It("rejects incompatible MAC matches without binding an Agent", func() {
	ctx := context.Background()
	buildReconciler := func(agent *unstructured.Unstructured) (*Reconciler, client.Client) {
		c := clientfake.NewClientBuilder().WithObjects(agent).Build()
		return &Reconciler{
			Client: c, apiReader: c, recorder: events.NewFakeRecorder(10),
			macResolver: func(context.Context, string) []string { return []string{"aa"} },
		}, c
	}
	co := &v1alpha1.ClusterOrder{ObjectMeta: metav1.ObjectMeta{Name: "order", Namespace: "osac"}}
	worker := newWorkerStatus("standard", "standard", "worker", "bmi-0", workerPhaseWaitingForAgent)

	By("foreign cluster deployment")
	func() {
		agent := agentPhaseFixture("", false)
		agent.SetUID("uid-foreign")
		_ = unstructured.SetNestedSlice(agent.Object, []interface{}{
			map[string]interface{}{"macAddress": "aa"},
		}, "status", "inventory", "interfaces")
		_ = unstructured.SetNestedMap(agent.Object, map[string]interface{}{
			"name": "another-cluster", "namespace": "osac",
		}, "spec", "clusterDeploymentName")
		r, c := buildReconciler(agent)
		workers := []v1alpha1.WorkerStatus{worker}
		bound, err := r.matchAndBindAgents(ctx, co, &unstructured.UnstructuredList{Items: []unstructured.Unstructured{*agent}}, workers, r.macResolver)
		Expect(err).To(HaveOccurred(), "a conflicting cluster binding must fail closed, not correlate")
		Expect(bound).To(BeZero(), "bound %d workers despite a conflicting binding", bound)
		got := &unstructured.Unstructured{}
		got.SetGroupVersionKind(agentGVK)
		Expect(c.Get(ctx, client.ObjectKeyFromObject(agent), got)).To(Succeed())
		Expect(got.GetLabels()[workerNameLabel]).To(Equal(""), "a conflicting Agent was taken over")
	}()

	By("malformed inventory is not a match")
	func() {
		agent := agentPhaseFixture("", false)
		agent.SetUID("uid-malformed")
		_ = unstructured.SetNestedSlice(agent.Object, []interface{}{
			"not-a-map",
		}, "status", "inventory", "interfaces")
		r, _ := buildReconciler(agent)
		workers := []v1alpha1.WorkerStatus{worker}
		bound, err := r.matchAndBindAgents(ctx, co, &unstructured.UnstructuredList{Items: []unstructured.Unstructured{*agent}}, workers, r.macResolver)
		Expect(err).ToNot(HaveOccurred(), "uninterpretable inventory is unknown, not invalid: %v", err)
		// A MAC that cannot be read is unknown association, never a match.
		Expect(bound).To(Equal(0), "malformed inventory correlated: bound=%d phase=%s", bound, workers[0].Phase)
		Expect(workers[0].Phase).To(Equal(workerPhaseWaitingForAgent), "malformed inventory correlated: bound=%d phase=%s", bound, workers[0].Phase)
	}()
})

var _ = Describe("reconcileAgent with transient Agent conflicts", func() {
	It("labels correlated Agents with their instance type and preserves the binding contract", func() {
		const (
			namespace        = "osac-e2e-ci"
			clusterOrderName = "ci-cluster"
			computeMAC       = "aa:bb:cc:dd:ee:01"
			gpuMAC           = "aa:bb:cc:dd:ee:02"
		)

		makeAgent := func(name, mac string) *unstructured.Unstructured {
			agent := &unstructured.Unstructured{Object: map[string]interface{}{}}
			agent.SetGroupVersionKind(agentGVK)
			agent.SetName(name)
			agent.SetNamespace(namespace)
			agent.SetLabels(map[string]string{clusterOrderLabel: clusterOrderName})
			Expect(unstructured.SetNestedSlice(agent.Object, []interface{}{
				map[string]interface{}{"macAddress": mac},
			}, "status", "inventory", "interfaces")).To(Succeed())
			return agent
		}

		co := &v1alpha1.ClusterOrder{
			ObjectMeta: metav1.ObjectMeta{Name: clusterOrderName, Namespace: namespace},
			Status: v1alpha1.ClusterOrderStatus{Workers: []v1alpha1.WorkerStatus{
				{
					NodeSet:           "compute",
					InstanceType:      "bm-compute",
					Name:              "ci-worker-bm",
					Kind:              workerKindBMI,
					BareMetalInstance: v1alpha1.BareMetalInstanceReference{ID: "bmi-compute"},
					Phase:             workerPhaseWaitingForAgent,
				},
				{
					NodeSet:           "gpu",
					InstanceType:      "bm-gpu",
					Name:              "ci-worker-bm-gpu",
					Kind:              workerKindBMI,
					BareMetalInstance: v1alpha1.BareMetalInstanceReference{ID: "bmi-gpu"},
					Phase:             workerPhaseWaitingForAgent,
				},
			}},
		}
		computeAgent := makeAgent("agent-compute", computeMAC)
		gpuAgent := makeAgent("agent-gpu", gpuMAC)

		s := runtime.NewScheme()
		Expect(v1alpha1.AddToScheme(s)).To(Succeed())
		s.AddKnownTypeWithName(agentGVK, &unstructured.Unstructured{})
		agentListGVK := agentGVK
		agentListGVK.Kind += "List"
		s.AddKnownTypeWithName(agentListGVK, &unstructured.UnstructuredList{})
		baseClient := clientfake.NewClientBuilder().
			WithScheme(s).
			WithObjects(co, computeAgent, gpuAgent).
			Build()
		conflictClient := &transientAgentConflictClient{
			Client:            baseClient,
			conflictAgentName: computeAgent.GetName(),
			staleAgent:        computeAgent.DeepCopy(),
		}
		r := &Reconciler{
			Client:    conflictClient,
			apiReader: baseClient,
			recorder:  events.NewFakeRecorder(2),
		}
		r.SetMACResolver(func(_ context.Context, resourceID string) []string {
			switch resourceID {
			case "bmi-compute":
				return []string{computeMAC}
			case "bmi-gpu":
				return []string{gpuMAC}
			default:
				return nil
			}
		})

		agents, err := r.listAgents(context.Background(), co)
		Expect(err).ToNot(HaveOccurred())
		workers, result, err := reconcileAgentStage(r, co, co.Status.Workers, agents)
		Expect(err).To(HaveOccurred())
		Expect(workers).To(BeNil())
		Expect(result).To(BeZero())

		// The next explicit invocation observes the changed Agent and binds it;
		// the conflict did not trigger an in-call retry or take over the Agent.
		r = &Reconciler{Client: baseClient, apiReader: baseClient, recorder: events.NewFakeRecorder(2)}
		r.SetMACResolver(func(_ context.Context, resourceID string) []string {
			switch resourceID {
			case "bmi-compute":
				return []string{computeMAC}
			case "bmi-gpu":
				return []string{gpuMAC}
			default:
				return nil
			}
		})
		agentGPU := &unstructured.Unstructured{}
		agentGPU.SetGroupVersionKind(agentGVK)
		Expect(baseClient.Get(context.Background(), types.NamespacedName{Name: "agent-gpu", Namespace: namespace}, agentGPU)).To(Succeed())
		agents.Items = append(agents.Items, *agentGPU)
		workers, result, err = reconcileAgentStage(r, co, co.Status.Workers, agents)
		Expect(err).ToNot(HaveOccurred())
		Expect(workers).To(HaveLen(2))
		Expect(workers[0].Phase).To(Equal(workerPhaseBinding), "workers=%+v agents=%+v", workers, agents.Items)
		Expect(workers[1].Phase).To(Equal(workerPhaseBinding), "workers=%+v agents=%+v", workers, agents.Items)
		Expect(result).To(BeZero())

		for _, expected := range []struct {
			name, workerName, nodeSet, instanceType string
		}{
			{name: "agent-compute", workerName: "ci-worker-bm", nodeSet: "compute", instanceType: "bm-compute"},
			{name: "agent-gpu", workerName: "ci-worker-bm-gpu", nodeSet: "gpu", instanceType: "bm-gpu"},
		} {
			agent := &unstructured.Unstructured{}
			agent.SetGroupVersionKind(agentGVK)
			Expect(baseClient.Get(context.Background(), types.NamespacedName{
				Name: expected.name, Namespace: namespace,
			}, agent)).To(Succeed())

			clusterDeploymentName, found, err := unstructured.NestedString(
				agent.Object, "spec", "clusterDeploymentName", "name")
			Expect(err).ToNot(HaveOccurred())
			Expect(found).To(BeTrue())
			Expect(clusterDeploymentName).To(Equal(clusterOrderName))
			clusterDeploymentNamespace, found, err := unstructured.NestedString(
				agent.Object, "spec", "clusterDeploymentName", "namespace")
			Expect(err).ToNot(HaveOccurred())
			Expect(found).To(BeTrue())
			Expect(clusterDeploymentNamespace).To(Equal(namespace))

			approved, found, err := unstructured.NestedBool(agent.Object, "spec", "approved")
			Expect(err).ToNot(HaveOccurred())
			Expect(found).To(BeTrue())
			Expect(approved).To(BeTrue())
			Expect(agent.GetLabels()).To(HaveKeyWithValue(workerNameLabel, expected.workerName))
			Expect(agent.GetLabels()).To(HaveKeyWithValue("osac.openshift.io/instance_type", expected.instanceType))
			Expect(agent.GetLabels()).To(HaveKeyWithValue("osac.openshift.io/node-set", expected.nodeSet))
			Expect(agent.GetLabels()).NotTo(HaveKey("osac.openshift.io/resource_class"))
			Expect(agent.GetLabels()).To(HaveKeyWithValue(agentBareMetalRoleLabel, "true"))
			Expect(agent.GetLabels()).To(HaveKeyWithValue(clusterOrderLabel, clusterOrderName))
			Expect(agent.GetLabels()).To(HaveKeyWithValue("osac.openshift.io/clusterorder", clusterOrderName))
		}

		assistedAgent := &unstructured.Unstructured{}
		assistedAgent.SetGroupVersionKind(agentGVK)
		Expect(baseClient.Get(context.Background(), types.NamespacedName{
			Name: "agent-compute", Namespace: namespace,
		}, assistedAgent)).To(Succeed())
		Expect(assistedAgent.GetLabels()).To(HaveKeyWithValue(assistedServiceLabel, assistedServiceLabelValue))
		Expect(assistedAgent.GetAnnotations()).To(HaveKeyWithValue(
			assistedServiceAnnotation, assistedServiceAnnotationValue))
		assistedState, found, err := unstructured.NestedString(
			assistedAgent.Object, "status", "debugInfo", "state")
		Expect(err).ToNot(HaveOccurred())
		Expect(found).To(BeTrue())
		Expect(assistedState).To(Equal(assistedServiceStatus))
	})
})

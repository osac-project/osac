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

var _ = Describe("matchAgentToBMI", func() {
	makeAgent := func(macs ...string) *unstructured.Unstructured {
		interfaces := make([]interface{}, 0, len(macs))
		for _, mac := range macs {
			interfaces = append(interfaces, map[string]interface{}{"macAddress": mac})
		}
		agent := &unstructured.Unstructured{Object: map[string]interface{}{}}
		agent.SetGroupVersionKind(agentGVK)
		_ = unstructured.SetNestedSlice(agent.Object, interfaces, "status", "inventory", "interfaces")
		return agent
	}

	var (
		workers  []v1alpha1.WorkerStatus
		resolver MACResolver
	)

	BeforeEach(func() {
		workers = []v1alpha1.WorkerStatus{
			{Name: "w-0", ResourceID: "bmi-0", Phase: workerPhaseWaitingForAgent},
			{Name: "w-1", ResourceID: "bmi-1", Phase: workerPhaseWaitingForAgent},
			{Name: "w-2", ResourceID: "bmi-2", Phase: workerPhaseBinding},
		}
		// bmi-1 reports two NICs — correlation must match against any of them.
		hostMACs := map[string][]string{
			"bmi-0": {"aa:bb:cc:dd:ee:00"},
			"bmi-1": {"aa:bb:cc:dd:ee:11", "aa:bb:cc:dd:ee:1f"},
			"bmi-2": {"aa:bb:cc:dd:ee:22"},
		}
		resolver = func(_ context.Context, id string) []string { return hostMACs[id] }
	})

	DescribeTable("matches agents to workers by MAC",
		func(agentMACs []string, wantWorker string, wantAmbig bool) {
			agent := makeAgent(agentMACs...)
			gotWorker, gotAmbig := matchAgentToBMI(context.Background(), agent, workers, resolver)
			Expect(gotWorker).To(Equal(wantWorker))
			Expect(gotAmbig).To(Equal(wantAmbig))
		},
		Entry("unique match", []string{"aa:bb:cc:dd:ee:00"}, "w-0", false),
		Entry("case-insensitive match", []string{"AA:BB:CC:DD:EE:11"}, "w-1", false),
		Entry("no match", []string{"ff:ff:ff:ff:ff:ff"}, "", false),
		Entry("empty agent MACs", nil, "", false),
		Entry("ambiguous — agent MAC matches two BMIs", []string{"aa:bb:cc:dd:ee:00", "aa:bb:cc:dd:ee:11"}, "", true),
		Entry("skips workers not in WaitingForAgent phase", []string{"aa:bb:cc:dd:ee:22"}, "", false),
		Entry("multiple interfaces, one matches", []string{"ff:ff:ff:ff:ff:ff", "aa:bb:cc:dd:ee:00"}, "w-0", false),
		Entry("matches a BMI's secondary NIC", []string{"aa:bb:cc:dd:ee:1f"}, "w-1", false),
	)
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

var _ = Describe("advanceBindingWorkers", func() {
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
			r := &Reconciler{recorder: events.NewFakeRecorder(1)}
			workers := []v1alpha1.WorkerStatus{{
				Name:              "w-0",
				Phase:             workerPhaseBinding,
				CreationTimestamp: metav1.Now(),
			}}
			agents := &unstructured.UnstructuredList{Items: []unstructured.Unstructured{
				makeAgent("w-0", conditionStatus, debugInstalled),
			}}

			r.advanceBindingWorkers(context.Background(), &v1alpha1.ClusterOrder{}, agents, workers)

			Expect(workers[0].Phase).To(Equal(wantPhase))
		},
		Entry("Installed=True without debug state", "True", false, workerPhaseReady),
		Entry("Installed=False overrides stale debug state", "False", true, workerPhaseBinding),
		Entry("Installed=Unknown overrides stale debug state", "Unknown", true, workerPhaseBinding),
		Entry("legacy debug state when condition is absent", "", true, workerPhaseReady),
	)
})

var _ = Describe("requestedBareMetalWorkersByResourceClass", func() {
	It("counts requested bare-metal workers before any Agent is correlated", func() {
		co := &v1alpha1.ClusterOrder{}
		co.Spec.NodeRequests = []v1alpha1.NodeRequest{
			{ResourceClass: "bm-worker", NumberOfNodes: 2,
				BareMetal: &v1alpha1.BareMetalNodeSpec{InstanceType: "bm-worker"}},
			{NumberOfNodes: 3},
		}

		Expect(requestedBareMetalWorkersByResourceClass(co)).To(Equal(map[string]int64{
			"bm-worker": 2,
		}))
	})
})

var _ = Describe("reconcileNodePoolReplicas", func() {
	It("keeps replica counts isolated by resource class", func() {
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
					{ResourceClass: "bm-standard", NumberOfNodes: 2,
						BareMetal: &v1alpha1.BareMetalNodeSpec{InstanceType: "bm-standard"}},
					{ResourceClass: "bm-gpu", NumberOfNodes: 1,
						BareMetal: &v1alpha1.BareMetalNodeSpec{InstanceType: "bm-gpu"}},
				},
			},
			Status: v1alpha1.ClusterOrderStatus{
				ClusterReference: &v1alpha1.ClusterOrderClusterReferenceType{Namespace: "workload"},
			},
		}

		makeNodePool := func(name, resourceClass string) *unstructured.Unstructured {
			np := &unstructured.Unstructured{Object: map[string]interface{}{}}
			np.SetGroupVersionKind(nodePoolGVK)
			np.SetName(name)
			np.SetNamespace("workload")
			np.SetLabels(map[string]string{
				"osac.openshift.io/clusterorder": co.Name,
				nodePoolResourceClassLabel:       resourceClass,
			})
			Expect(unstructured.SetNestedField(np.Object, int64(0), "spec", "replicas")).To(Succeed())
			return np
		}

		k8sClient := clientfake.NewClientBuilder().
			WithScheme(s).
			WithObjects(co, makeNodePool("standard", "bm-standard"), makeNodePool("gpu", "bm-gpu")).
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

var _ = Describe("correlateAgents with transient Agent conflicts", func() {
	It("eventually binds both resource classes and preserves the binding contract", func() {
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
					NodeSet:    "compute",
					Name:       "ci-worker-bm",
					Kind:       workerKindBMI,
					ResourceID: "bmi-compute",
					Phase:      workerPhaseWaitingForAgent,
				},
				{
					NodeSet:    "gpu",
					Name:       "ci-worker-bm-gpu",
					Kind:       workerKindBMI,
					ResourceID: "bmi-gpu",
					Phase:      workerPhaseWaitingForAgent,
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

		workers, result, err := r.correlateAgents(context.Background(), co, co.Status.Workers)
		Expect(err).ToNot(HaveOccurred())
		Expect(workers).To(HaveLen(2))
		Expect(workers[0].Phase).To(Equal(workerPhaseBinding))
		Expect(workers[1].Phase).To(Equal(workerPhaseBinding))
		Expect(result).To(BeZero())

		for _, expected := range []struct {
			name, workerName, resourceClass string
		}{
			{name: "agent-compute", workerName: "ci-worker-bm", resourceClass: "compute"},
			{name: "agent-gpu", workerName: "ci-worker-bm-gpu", resourceClass: "gpu"},
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
			Expect(agent.GetLabels()).To(HaveKeyWithValue(nodePoolResourceClassLabel, expected.resourceClass))
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

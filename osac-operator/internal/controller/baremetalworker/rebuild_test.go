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

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"

	"github.com/osac-project/osac/osac-operator/api/v1alpha1"
)

var _ = Describe("findAgentForWorker", func() {
	makeAgent := func(name, mac string, labels map[string]string) unstructured.Unstructured {
		agent := unstructured.Unstructured{Object: map[string]interface{}{}}
		agent.SetGroupVersionKind(agentGVK)
		agent.SetName(name)
		if labels != nil {
			agent.SetLabels(labels)
		}
		interfaces := []interface{}{map[string]interface{}{"macAddress": mac}}
		_ = unstructured.SetNestedSlice(agent.Object, interfaces, "status", "inventory", "interfaces")
		return agent
	}

	It("returns the agent whose MAC matches the BMI's host MAC", func() {
		agents := &unstructured.UnstructuredList{Items: []unstructured.Unstructured{
			makeAgent("agent-0", "aa:bb:cc:00:00:00", nil),
			makeAgent("agent-1", "aa:bb:cc:11:11:11", nil),
		}}
		resolver := func(_ context.Context, id string) []string {
			if id == "bmi-1" {
				return []string{"aa:bb:cc:11:11:11"}
			}
			return nil
		}
		got := findAgentForWorker(context.Background(), agents, "bmi-1", "w-1", resolver)
		Expect(got).ToNot(BeNil())
		Expect(got.GetName()).To(Equal("agent-1"))
	})

	It("returns nil when no agent MAC matches", func() {
		agents := &unstructured.UnstructuredList{Items: []unstructured.Unstructured{
			makeAgent("agent-0", "aa:bb:cc:00:00:00", nil),
		}}
		resolver := func(context.Context, string) []string { return []string{"ff:ff:ff:ff:ff:ff"} }
		got := findAgentForWorker(context.Background(), agents, "bmi-0", "w-0", resolver)
		Expect(got).To(BeNil())
	})

	It("matches MACs case-insensitively", func() {
		agents := &unstructured.UnstructuredList{Items: []unstructured.Unstructured{
			makeAgent("agent-0", "AA:BB:CC:DD:EE:FF", nil),
		}}
		resolver := func(context.Context, string) []string { return []string{"aa:bb:cc:dd:ee:ff"} }
		got := findAgentForWorker(context.Background(), agents, "bmi-0", "w-0", resolver)
		Expect(got).ToNot(BeNil())
	})

	It("finds agent by worker-name label when MAC resolver returns empty", func() {
		agents := &unstructured.UnstructuredList{Items: []unstructured.Unstructured{
			makeAgent("agent-0", "aa:bb:cc:00:00:00", map[string]string{workerNameLabel: "w-0"}),
		}}
		resolver := func(context.Context, string) []string { return nil }
		got := findAgentForWorker(context.Background(), agents, "bmi-0", "w-0", resolver)
		Expect(got).ToNot(BeNil())
		Expect(got.GetName()).To(Equal("agent-0"))
	})
})

var _ = Describe("deriveWorkerPhase", func() {
	makeAgent := func(name string, labels map[string]string, debugInstalled bool, conditionStatus string) *unstructured.Unstructured {
		agent := &unstructured.Unstructured{Object: map[string]interface{}{}}
		agent.SetGroupVersionKind(agentGVK)
		agent.SetName(name)
		if labels != nil {
			agent.SetLabels(labels)
		}
		if debugInstalled {
			_ = unstructured.SetNestedField(agent.Object, "installed", "status", "debugInfo", "state")
		}
		if conditionStatus != "" {
			_ = unstructured.SetNestedSlice(agent.Object, []interface{}{
				map[string]interface{}{"type": "Installed", "status": conditionStatus},
			}, "status", "conditions")
		}
		return agent
	}

	It("returns WaitingForAgent when no agent is provided", func() {
		Expect(deriveWorkerPhase(nil, "w-0")).To(Equal(workerPhaseWaitingForAgent))
	})

	It("returns Ready when agent is bound and installed", func() {
		agent := makeAgent("a-0", map[string]string{workerNameLabel: "w-0"}, true, "")
		Expect(deriveWorkerPhase(agent, "w-0")).To(Equal(workerPhaseReady))
	})

	It("returns Ready when agent is bound and Installed is True", func() {
		agent := makeAgent("a-0", map[string]string{workerNameLabel: "w-0"}, false, "True")
		Expect(deriveWorkerPhase(agent, "w-0")).To(Equal(workerPhaseReady))
	})

	It("returns Binding when agent is bound but not installed", func() {
		agent := makeAgent("a-0", map[string]string{workerNameLabel: "w-0"}, false, "")
		Expect(deriveWorkerPhase(agent, "w-0")).To(Equal(workerPhaseBinding))
	})

	DescribeTable("returns Binding when Installed is not True",
		func(conditionStatus string) {
			agent := makeAgent("a-0", map[string]string{workerNameLabel: "w-0"}, true, conditionStatus)
			Expect(deriveWorkerPhase(agent, "w-0")).To(Equal(workerPhaseBinding))
		},
		Entry("False", "False"),
		Entry("Unknown", "Unknown"),
	)

	It("returns WaitingForAgent when agent exists but is not bound to this worker", func() {
		agent := makeAgent("a-0", nil, false, "")
		Expect(deriveWorkerPhase(agent, "w-0")).To(Equal(workerPhaseWaitingForAgent))
	})
})

var _ = Describe("rebuildWorkerPhases", func() {
	makeAgent := func(name, mac string, labels map[string]string, debugInstalled bool, conditionStatus string) unstructured.Unstructured {
		agent := unstructured.Unstructured{Object: map[string]interface{}{}}
		agent.SetGroupVersionKind(agentGVK)
		agent.SetName(name)
		if labels != nil {
			agent.SetLabels(labels)
		}
		interfaces := []interface{}{map[string]interface{}{"macAddress": mac}}
		_ = unstructured.SetNestedSlice(agent.Object, interfaces, "status", "inventory", "interfaces")
		if debugInstalled {
			_ = unstructured.SetNestedField(agent.Object, "installed", "status", "debugInfo", "state")
		}
		if conditionStatus != "" {
			_ = unstructured.SetNestedSlice(agent.Object, []interface{}{
				map[string]interface{}{"type": "Installed", "status": conditionStatus},
			}, "status", "conditions")
		}
		return agent
	}

	It("re-derives WaitingForAgent when BMI exists but no agent matches", func() {
		workers := []v1alpha1.WorkerStatus{
			{Name: "w-0", Kind: workerKindBMI, ResourceID: "bmi-0", Phase: workerPhaseReady},
		}
		agents := &unstructured.UnstructuredList{}
		resolver := func(context.Context, string) []string { return []string{"aa:bb:cc:00:00:00"} }
		bmiExists := func(string) bool { return true }

		result, removed := rebuildWorkerPhases(context.Background(), workers, agents, resolver, bmiExists)
		Expect(removed).To(BeEmpty())
		Expect(result).To(HaveLen(1))
		Expect(result[0].Phase).To(Equal(workerPhaseWaitingForAgent))
	})

	It("re-derives Ready when BMI exists and agent is bound+installed", func() {
		workers := []v1alpha1.WorkerStatus{
			{Name: "w-0", Kind: workerKindBMI, ResourceID: "bmi-0", Phase: workerPhaseWaitingForAgent},
		}
		agents := &unstructured.UnstructuredList{Items: []unstructured.Unstructured{
			makeAgent("agent-0", "aa:bb:cc:00:00:00", map[string]string{workerNameLabel: "w-0"}, true, ""),
		}}
		resolver := func(context.Context, string) []string { return []string{"aa:bb:cc:00:00:00"} }
		bmiExists := func(string) bool { return true }

		result, removed := rebuildWorkerPhases(context.Background(), workers, agents, resolver, bmiExists)
		Expect(removed).To(BeEmpty())
		Expect(result).To(HaveLen(1))
		Expect(result[0].Phase).To(Equal(workerPhaseReady))
	})

	It("re-derives Ready when BMI exists and bound agent has Installed=True", func() {
		workers := []v1alpha1.WorkerStatus{
			{Name: "w-0", Kind: workerKindBMI, ResourceID: "bmi-0", Phase: workerPhaseWaitingForAgent},
		}
		agents := &unstructured.UnstructuredList{Items: []unstructured.Unstructured{
			makeAgent("agent-0", "aa:bb:cc:00:00:00", map[string]string{workerNameLabel: "w-0"}, false, "True"),
		}}
		resolver := func(context.Context, string) []string { return []string{"aa:bb:cc:00:00:00"} }
		bmiExists := func(string) bool { return true }

		result, removed := rebuildWorkerPhases(context.Background(), workers, agents, resolver, bmiExists)
		Expect(removed).To(BeEmpty())
		Expect(result).To(HaveLen(1))
		Expect(result[0].Phase).To(Equal(workerPhaseReady))
	})

	It("re-derives Binding when BMI exists and agent is bound but not installed", func() {
		workers := []v1alpha1.WorkerStatus{
			{Name: "w-0", Kind: workerKindBMI, ResourceID: "bmi-0", Phase: workerPhaseWaitingForAgent},
		}
		agents := &unstructured.UnstructuredList{Items: []unstructured.Unstructured{
			makeAgent("agent-0", "aa:bb:cc:00:00:00", map[string]string{workerNameLabel: "w-0"}, false, ""),
		}}
		resolver := func(context.Context, string) []string { return []string{"aa:bb:cc:00:00:00"} }
		bmiExists := func(string) bool { return true }

		result, removed := rebuildWorkerPhases(context.Background(), workers, agents, resolver, bmiExists)
		Expect(removed).To(BeEmpty())
		Expect(result).To(HaveLen(1))
		Expect(result[0].Phase).To(Equal(workerPhaseBinding))
	})

	DescribeTable("re-derives Binding for a bound agent whose Installed condition is not True",
		func(conditionStatus string) {
			workers := []v1alpha1.WorkerStatus{
				{Name: "w-0", Kind: workerKindBMI, ResourceID: "bmi-0", Phase: workerPhaseWaitingForAgent},
			}
			agents := &unstructured.UnstructuredList{Items: []unstructured.Unstructured{
				makeAgent("agent-0", "aa:bb:cc:00:00:00", map[string]string{workerNameLabel: "w-0"}, true, conditionStatus),
			}}
			resolver := func(context.Context, string) []string { return []string{"aa:bb:cc:00:00:00"} }
			bmiExists := func(string) bool { return true }

			result, removed := rebuildWorkerPhases(context.Background(), workers, agents, resolver, bmiExists)
			Expect(removed).To(BeEmpty())
			Expect(result).To(HaveLen(1))
			Expect(result[0].Phase).To(Equal(workerPhaseBinding))
		},
		Entry("False", "False"),
		Entry("Unknown", "Unknown"),
	)

	It("removes stale entries when BMI is gone", func() {
		workers := []v1alpha1.WorkerStatus{
			{Name: "w-0", Kind: workerKindBMI, ResourceID: "bmi-0", Phase: workerPhaseReady},
			{Name: "w-1", Kind: workerKindBMI, ResourceID: "bmi-1", Phase: workerPhaseWaitingForAgent},
		}
		agents := &unstructured.UnstructuredList{}
		resolver := func(context.Context, string) []string { return nil }
		bmiExists := func(id string) bool { return id != "bmi-0" }

		result, removed := rebuildWorkerPhases(context.Background(), workers, agents, resolver, bmiExists)
		Expect(removed).To(ConsistOf("w-0"))
		Expect(result).To(HaveLen(1))
		Expect(result[0].Name).To(Equal("w-1"))
	})

	It("preserves attemptCount and failure history on re-derived workers", func() {
		failTime := metav1.Now()
		workers := []v1alpha1.WorkerStatus{
			{
				Name: "w-0", Kind: workerKindBMI, ResourceID: "bmi-0",
				Phase: workerPhaseWaitingForAgent, AttemptCount: 3,
				LastFailureReason:  "AgentRegistrationTimeout",
				LastFailureMessage: "no agent registered within 30m",
				LastFailureTime:    &failTime,
			},
		}
		agents := &unstructured.UnstructuredList{}
		resolver := func(context.Context, string) []string { return []string{"aa:bb:cc:00:00:00"} }
		bmiExists := func(string) bool { return true }

		result, removed := rebuildWorkerPhases(context.Background(), workers, agents, resolver, bmiExists)
		Expect(removed).To(BeEmpty())
		Expect(result).To(HaveLen(1))
		Expect(result[0].Phase).To(Equal(workerPhaseWaitingForAgent))
		Expect(result[0].AttemptCount).To(Equal(int32(3)))
		Expect(result[0].LastFailureReason).To(Equal("AgentRegistrationTimeout"))
		Expect(result[0].LastFailureMessage).To(Equal("no agent registered within 30m"))
		Expect(result[0].LastFailureTime).To(Equal(&failTime))
	})

	It("preserves ReadySince for workers that remain Ready", func() {
		readySince := metav1.Now()
		workers := []v1alpha1.WorkerStatus{
			{
				Name: "w-0", Kind: workerKindBMI, ResourceID: "bmi-0",
				Phase: workerPhaseReady, ReadySince: &readySince,
			},
		}
		agents := &unstructured.UnstructuredList{Items: []unstructured.Unstructured{
			makeAgent("agent-0", "aa:bb:cc:00:00:00", map[string]string{workerNameLabel: "w-0"}, true, ""),
		}}
		resolver := func(context.Context, string) []string { return []string{"aa:bb:cc:00:00:00"} }
		bmiExists := func(string) bool { return true }

		result, _ := rebuildWorkerPhases(context.Background(), workers, agents, resolver, bmiExists)
		Expect(result[0].ReadySince).To(Equal(&readySince))
	})

	It("leaves non-BMI workers untouched", func() {
		workers := []v1alpha1.WorkerStatus{
			{Name: "vm-0", Kind: "VirtualMachine", ResourceID: "vm-id", Phase: "Running"},
			{Name: "w-0", Kind: workerKindBMI, ResourceID: "bmi-0", Phase: workerPhaseReady},
		}
		agents := &unstructured.UnstructuredList{}
		resolver := func(context.Context, string) []string { return []string{"aa:bb:cc:00:00:00"} }
		bmiExists := func(string) bool { return true }

		result, _ := rebuildWorkerPhases(context.Background(), workers, agents, resolver, bmiExists)
		Expect(result).To(HaveLen(2))
		Expect(result[0].Kind).To(Equal("VirtualMachine"))
		Expect(result[0].Phase).To(Equal("Running"))
	})

	It("handles empty workers list as a no-op", func() {
		agents := &unstructured.UnstructuredList{}
		resolver := func(context.Context, string) []string { return nil }
		bmiExists := func(string) bool { return true }

		result, removed := rebuildWorkerPhases(context.Background(), nil, agents, resolver, bmiExists)
		Expect(result).To(BeNil())
		Expect(removed).To(BeEmpty())
	})

	It("handles multiple workers with mixed live states", func() {
		workers := []v1alpha1.WorkerStatus{
			{Name: "w-0", Kind: workerKindBMI, ResourceID: "bmi-0", Phase: workerPhaseProvisioning},
			{Name: "w-1", Kind: workerKindBMI, ResourceID: "bmi-1", Phase: workerPhaseProvisioning},
			{Name: "w-2", Kind: workerKindBMI, ResourceID: "bmi-2", Phase: workerPhaseReady},
		}
		agents := &unstructured.UnstructuredList{Items: []unstructured.Unstructured{
			makeAgent("agent-1", "bb:bb:bb:11:11:11", map[string]string{workerNameLabel: "w-1"}, true, ""),
		}}
		resolver := func(_ context.Context, id string) []string {
			m := map[string][]string{
				"bmi-0": {"aa:aa:aa:00:00:00"}, "bmi-1": {"bb:bb:bb:11:11:11"}, "bmi-2": {"cc:cc:cc:22:22:22"},
			}
			return m[id]
		}
		bmiExists := func(id string) bool { return id != "bmi-2" }

		result, removed := rebuildWorkerPhases(context.Background(), workers, agents, resolver, bmiExists)
		Expect(removed).To(ConsistOf("w-2"))
		Expect(result).To(HaveLen(2))
		Expect(result[0].Name).To(Equal("w-0"))
		Expect(result[0].Phase).To(Equal(workerPhaseWaitingForAgent))
		Expect(result[1].Name).To(Equal("w-1"))
		Expect(result[1].Phase).To(Equal(workerPhaseReady))
	})

	It("does not re-derive phase for workers in teardown or failed phases", func() {
		workers := []v1alpha1.WorkerStatus{
			{Name: "w-0", Kind: workerKindBMI, ResourceID: "bmi-0", Phase: workerPhaseUnbinding},
			{Name: "w-1", Kind: workerKindBMI, ResourceID: "bmi-1", Phase: workerPhaseDeleting},
			{Name: "w-2", Kind: workerKindBMI, ResourceID: "bmi-2", Phase: workerPhaseFailed, AttemptCount: 1},
		}
		agents := &unstructured.UnstructuredList{}
		resolver := func(context.Context, string) []string { return []string{"aa:bb:cc:00:00:00"} }
		bmiExists := func(string) bool { return true }

		result, removed := rebuildWorkerPhases(context.Background(), workers, agents, resolver, bmiExists)
		Expect(removed).To(BeEmpty())
		Expect(result).To(HaveLen(3))
		Expect(result[0].Phase).To(Equal(workerPhaseUnbinding))
		Expect(result[1].Phase).To(Equal(workerPhaseDeleting))
		Expect(result[2].Phase).To(Equal(workerPhaseFailed))
	})
})

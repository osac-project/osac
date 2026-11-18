// Copyright (c) 2026 Red Hat Inc.
// SPDX-License-Identifier: Apache-2.0

package baremetalworker

import (
	"context"
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/client-go/kubernetes/fake"
	"k8s.io/client-go/tools/events"
	"sigs.k8s.io/controller-runtime/pkg/client"
	clientfake "sigs.k8s.io/controller-runtime/pkg/client/fake"

	bmfov1alpha1 "github.com/osac-project/osac/bare-metal-fulfillment-operator/api/v1alpha1"
	"github.com/osac-project/osac/osac-operator/api/v1alpha1"
	privatev1 "github.com/osac-project/osac/proto/gen/osac/private/v1"
)

const (
	proofBMIID     = "bmi-proof"
	proofSubnetID  = "subnet-proof"
	proofInterface = "eno1"
	proofMAC       = "aa:bb:cc:dd:ee:ff"
	proofLease     = "192.0.2.44"
)

func workerNetworkProofFixture(now time.Time) (*Reconciler, *v1alpha1.ClusterOrder,
	v1alpha1.WorkerStatus, *workerObservation) {
	interfaceName := proofInterface
	privateBMI := privatev1.BareMetalInstance_builder{
		Id: proofBMIID,
		Spec: privatev1.BareMetalInstanceSpec_builder{
			NetworkAttachments: []*privatev1.BareMetalNetworkAttachment{
				privatev1.BareMetalNetworkAttachment_builder{
					Subnet:    privatev1.SubnetLocalReference_builder{Id: proofSubnetID}.Build(),
					Interface: &interfaceName,
				}.Build(),
			},
		}.Build(),
		Status: privatev1.BareMetalInstanceStatus_builder{
			NetworkAttachmentStatuses: []*privatev1.BareMetalNetworkAttachmentStatus{
				privatev1.BareMetalNetworkAttachmentStatus_builder{
					SubnetRef: proofSubnetID,
					Interface: proofInterface,
					IpAddress: proofLease,
				}.Build(),
			},
		}.Build(),
	}.Build()
	hubBMI := &bmfov1alpha1.BareMetalInstance{
		Status: bmfov1alpha1.BareMetalInstanceStatus{
			Phase: bmfov1alpha1.BareMetalInstancePhaseReady,
			Conditions: []metav1.Condition{{
				Type:               string(bmfov1alpha1.HostConditionNetworkHandoffComplete),
				Status:             metav1.ConditionTrue,
				LastTransitionTime: metav1.NewTime(now),
			}, {
				Type:               string(bmfov1alpha1.HostConditionIPDiscoveryComplete),
				Status:             metav1.ConditionTrue,
				LastTransitionTime: metav1.NewTime(now),
			}},
			NetworkAttachmentStatuses: []bmfov1alpha1.BareMetalNetworkAttachmentStatus{{
				SubnetRef:  proofSubnetID,
				Interface:  proofInterface,
				MACAddress: proofMAC,
				IPAddress:  proofLease,
			}},
		},
	}
	attachment := &v1alpha1.NetworkAttachment{
		Spec: v1alpha1.NetworkAttachmentSpec{
			Target: v1alpha1.NetworkAttachmentTarget{
				Kind: v1alpha1.NetworkAttachmentTargetBareMetalInstance,
				ID:   proofBMIID,
			},
			Subnet: proofSubnetID,
			Interface: v1alpha1.NetworkAttachmentInterface{
				Type:       v1alpha1.NetworkInterfacePhysical,
				Name:       proofInterface,
				MACAddress: proofMAC,
			},
			SecurityGroups: []string{"sg-proof"},
		},
		Status: v1alpha1.NetworkAttachmentStatus{Conditions: []metav1.Condition{{
			Type:   v1alpha1.NetworkConditionReady,
			Status: metav1.ConditionTrue,
		}}},
	}
	observation := indexWorkerBMIs([]*privatev1.BareMetalInstance{privateBMI})
	observation.hubBMIs[proofBMIID] = hubBMI
	observation.networkAttachments[proofBMIID] = attachment
	co := &v1alpha1.ClusterOrder{
		Spec: v1alpha1.ClusterOrderSpec{
			NetworkAttachment: &v1alpha1.ClusterNetworkAttachment{SubnetRef: proofSubnetID},
		},
	}
	worker := v1alpha1.WorkerStatus{
		Name:              "worker-0",
		Kind:              workerKindBMI,
		Phase:             workerPhaseWaitingForAgent,
		BareMetalInstance: v1alpha1.BareMetalInstanceReference{ID: proofBMIID},
	}
	return &Reconciler{NetworkingProvisioningEnabled: true}, co, worker, observation
}

func TestWorkerNetworkProofRequiresHandoffAndAcceptedLease(t *testing.T) {
	now := time.Date(2026, 11, 17, 12, 0, 0, 0, time.UTC)
	r, co, worker, observation := workerNetworkProofFixture(now)

	proof, ready, err := r.workerNetworkProof(context.Background(), co, worker, observation)
	if err != nil || !ready || !proof.bmiReady || !proof.bindingReady {
		t.Fatalf("valid handoff proof = (%+v, %t, %v), want bindable", proof, ready, err)
	}
	if proof.mac != proofMAC || proof.leaseAddress != proofLease {
		t.Fatalf("proof MAC/lease = %q/%q, want %q/%q", proof.mac, proof.leaseAddress, proofMAC, proofLease)
	}
	if !agentMatchesWorkerLease(workerProofAgent(proofMAC, proofLease), proof.mac, proof.leaseAddress) {
		t.Fatal("Agent inventory with the accepted selected-port lease did not match")
	}
	if agentMatchesWorkerLease(workerProofAgent("00:11:22:33:44:55", proofLease), proof.mac, proof.leaseAddress) {
		t.Fatal("Agent inventory on a different interface MAC matched the worker lease")
	}

	hubBMI := observation.hubBMIs[proofBMIID]
	hubBMI.Status.Conditions = nil
	if _, ready, err = r.workerNetworkProof(context.Background(), co, worker, observation); err != nil || ready {
		t.Fatalf("missing NetworkHandoffComplete proof = (%t, %v), want unavailable", ready, err)
	}

	hubBMI.Status.Conditions = []metav1.Condition{{
		Type:               string(bmfov1alpha1.HostConditionNetworkHandoffComplete),
		Status:             metav1.ConditionTrue,
		LastTransitionTime: metav1.NewTime(now),
	}, {
		Type:               string(bmfov1alpha1.HostConditionIPDiscoveryComplete),
		Status:             metav1.ConditionTrue,
		LastTransitionTime: metav1.NewTime(now),
	}}
	hubBMI.Status.Phase = bmfov1alpha1.BareMetalInstancePhaseAllocating
	proof, ready, err = r.workerNetworkProof(context.Background(), co, worker, observation)
	if err != nil || !ready || proof.bmiReady {
		t.Fatalf("non-Ready BMI proof = (%+v, %t, %v), want evidence without a bindable BMI", proof, ready, err)
	}
	hubBMI.Status.Phase = bmfov1alpha1.BareMetalInstancePhaseReady
	hubBMI.SetStatusCondition(bmfov1alpha1.HostConditionIPDiscoveryComplete,
		metav1.ConditionFalse, "LeasePending", "lease not yet accepted")
	proof, ready, err = r.workerNetworkProof(context.Background(), co, worker, observation)
	if err != nil || !ready || proof.bindingReady {
		t.Fatalf("unaccepted lease proof = (%+v, %t, %v), want not bindable", proof, ready, err)
	}
	hubBMI.SetStatusCondition(bmfov1alpha1.HostConditionIPDiscoveryComplete,
		metav1.ConditionTrue, "Discovered", "lease accepted")
	attachment := observation.networkAttachments[proofBMIID]
	attachment.Status.Conditions[0].Status = metav1.ConditionFalse
	proof, ready, err = r.workerNetworkProof(context.Background(), co, worker, observation)
	if err != nil || !ready || proof.bindingReady {
		t.Fatalf("unready NetworkAttachment proof = (%+v, %t, %v), want not bindable", proof, ready, err)
	}
	attachment.Status.Conditions[0].Status = metav1.ConditionTrue
	privateBMI := observation.byID[proofBMIID]
	privateBMI.GetStatus().GetNetworkAttachmentStatuses()[0].SetIpAddress("192.0.2.99")
	proof, ready, err = r.workerNetworkProof(context.Background(), co, worker, observation)
	if err != nil || !ready || proof.bindingReady {
		t.Fatalf("mismatched accepted lease proof = (%+v, %t, %v), want ready but not bindable", proof, ready, err)
	}
}

func TestRegistrationDeadlineUsesLatestPublicationEvidence(t *testing.T) {
	networkReady := time.Date(2026, 11, 17, 12, 0, 0, 0, time.UTC)
	managementReady := networkReady.Add(5 * time.Minute)
	apiPublished := networkReady.Add(10 * time.Minute)
	worker := v1alpha1.WorkerStatus{}
	evidence := workerNetworkProof{networkReadyAt: networkReady, provisionReady: networkReady}
	deadline := registrationDeadline(worker, evidence, managementReady, &apiPublished, true)
	want := metav1.NewTime(apiPublished.Add(agentRegistrationTimeout))
	if deadline == nil || !deadline.Equal(&want) {
		t.Fatalf("deadline = %v, want %v", deadline, want)
	}

	// The provider-networking-disabled path uses provision completion and
	// management publication; it does not require API DNS publication.
	disabledDeadline := registrationDeadline(worker, evidence, managementReady, nil, false)
	disabledWant := metav1.NewTime(managementReady.Add(agentRegistrationTimeout))
	if disabledDeadline == nil || !disabledDeadline.Equal(&disabledWant) {
		t.Fatalf("disabled-mode deadline = %v, want %v", disabledDeadline, disabledWant)
	}
}

func TestWorkerAPIRecordsPublishedRequiresSuccessEvidence(t *testing.T) {
	published := metav1.NewTime(time.Date(2026, 11, 17, 12, 0, 0, 0, time.UTC))
	co := &v1alpha1.ClusterOrder{}
	co.Status.APIDNSRecordsPublishedTime = &published
	if workerAPIRecordsPublished(co) {
		t.Fatal("API DNS timestamp without a successful publication job authorized binding")
	}
	co.Status.Jobs = []v1alpha1.NetworkJobStatus{{
		Task:    "publish_public_dns_api",
		Manager: v1alpha1.NetworkJobManagerNone,
		State:   v1alpha1.NetworkJobStateFailed,
	}}
	if workerAPIRecordsPublished(co) {
		t.Fatal("failed API DNS publication job authorized binding")
	}
	co.Status.Jobs[0].State = v1alpha1.NetworkJobStateSucceeded
	if !workerAPIRecordsPublished(co) {
		t.Fatal("successful API DNS publication with timestamp did not authorize binding")
	}
}

func TestCaaSAgentBindingPersistsIdentityBeforeApproval(t *testing.T) {
	now := time.Date(2026, 11, 17, 12, 0, 0, 0, time.UTC)
	r, co, worker, observation := workerNetworkProofFixture(now)
	co.Name = "caas-order"
	co.Namespace = "osac"
	co.UID = "caas-order-uid"
	co.Labels = map[string]string{clusterOrderIDLabel: "cluster-id"}
	co.Annotations = map[string]string{tenantAnnotationKey: "tenant-a"}
	co.Spec.SupportsAutomaticCaaSEndpoints = true
	co.Spec.ReleaseImage = "registry.example.test/ocp/release:4.22.0"
	worker.AttemptCount = 1
	worker.RegistryProbePrepared = true
	agent := workerProofAgent(proofMAC, proofLease)
	agent.SetGroupVersionKind(agentGVK)
	agent.SetName("worker-agent")
	agent.SetNamespace(co.Namespace)
	agent.SetUID("worker-agent-uid")
	agent.SetLabels(map[string]string{clusterOrderLabel: co.Name})
	kube := clientfake.NewClientBuilder().WithObjects(agent).Build()
	r.Client = kube
	r.apiReader = kube
	r.recorder = events.NewFakeRecorder(10)
	r.macResolver = func(context.Context, string) []string { return []string{proofMAC} }
	probeNames, err := workerRegistryProbeNamesFor(co, worker.Name, worker.AttemptCount)
	if err != nil {
		t.Fatal(err)
	}
	probeClient := fake.NewSimpleClientset(&corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Name: probeNames.configMap, Namespace: probeNames.namespace,
			Labels: map[string]string{clusterOrderIDLabel: co.Labels[clusterOrderIDLabel]},
			Annotations: map[string]string{
				tenantAnnotationKey: tenantOf(co), ownerReferenceAnnotation: "ClusterOrder/" + co.Name,
			},
		},
		Data: map[string]string{
			workerRegistryProbeRequestKey: workerRegistryProbeRequestedValue,
			workerRegistryProbeStatusKey:  "ready",
			workerRegistryProbeImageKey:   co.Spec.ReleaseImage,
			workerRegistryProbeReasonKey:  "ImagePulled",
		},
	})
	r.registryProbeGuestResolver = registryProbeGuestResolverFunc(func(context.Context, *v1alpha1.ClusterOrder) (registryProbeGuestAPI, error) {
		return registryProbeGuestAPI{client: probeClient, apiURL: co.Status.APIURL, caData: []byte("test-ca")}, nil
	})
	agents := &unstructured.UnstructuredList{Items: []unstructured.Unstructured{*agent}}
	observation.agents = agents
	workers := []v1alpha1.WorkerStatus{worker}

	bound, err := r.matchAndBindAgents(context.Background(), co, agents, workers, r.macResolver, observation)
	if err != nil || bound != 0 || workers[0].Agent != nil {
		t.Fatalf("binding without ManagementPublicationReady = (%d, %+v, %v)", bound, workers[0], err)
	}
	co.Status.Conditions = []metav1.Condition{{
		Type:               string(v1alpha1.ClusterOrderConditionManagementPublicationReady),
		Status:             metav1.ConditionTrue,
		LastTransitionTime: metav1.NewTime(now),
	}}
	bound, err = r.matchAndBindAgents(context.Background(), co, agents, workers, r.macResolver, observation)
	if err != nil || bound != 0 || workers[0].Agent != nil {
		t.Fatalf("binding without API DNS publication = (%d, %+v, %v)", bound, workers[0], err)
	}
	apiTime := metav1.NewTime(now)
	co.Status.APIDNSRecordsPublishedTime = &apiTime
	co.Status.Jobs = []v1alpha1.NetworkJobStatus{{
		Task:    "publish_public_dns_api",
		Manager: v1alpha1.NetworkJobManagerNone,
		State:   v1alpha1.NetworkJobStateSucceeded,
	}}
	bound, err = r.matchAndBindAgents(context.Background(), co, agents, workers, r.macResolver, observation)
	if err != nil || bound != 0 || !workerRegistryProbeConditionReady(&workers[0], co.Spec.ReleaseImage) {
		t.Fatalf("first bind pass = (%d, %+v, %v), want registry probe persistence only", bound, workers[0], err)
	}
	if workers[0].Agent != nil {
		t.Fatalf("worker Agent identity persisted before the registry result: %+v", workers[0].Agent)
	}

	bound, err = r.matchAndBindAgents(context.Background(), co, agents, workers, r.macResolver, observation)
	if err != nil || bound != 0 {
		t.Fatalf("second bind pass = (%d, %v), want identity persistence only", bound, err)
	}
	if workers[0].Agent == nil || workers[0].Agent.Name != agent.GetName() || workers[0].Agent.UID != string(agent.GetUID()) {
		t.Fatalf("persisted worker Agent identity = %+v, want %s/%s", workers[0].Agent, agent.GetName(), agent.GetUID())
	}
	stored := &unstructured.Unstructured{}
	stored.SetGroupVersionKind(agentGVK)
	if err := kube.Get(context.Background(), client.ObjectKeyFromObject(agent), stored); err != nil {
		t.Fatal(err)
	}
	if unstructuredNestedBool(t, stored, "spec", "approved") {
		t.Fatal("Agent was approved before its identity was persisted")
	}

	bound, err = r.matchAndBindAgents(context.Background(), co, agents, workers, r.macResolver, observation)
	if err != nil || bound != 1 || workers[0].Phase != workerPhaseBinding {
		t.Fatalf("second bind pass = (%d, %+v, %v), want a bound worker", bound, workers[0], err)
	}
	if err := kube.Get(context.Background(), client.ObjectKeyFromObject(agent), stored); err != nil {
		t.Fatal(err)
	}
	if !unstructuredNestedBool(t, stored, "spec", "approved") {
		t.Fatal("Agent was not approved after the persisted identity was observed")
	}
}

func unstructuredNestedBool(t *testing.T, object *unstructured.Unstructured, fields ...string) bool {
	t.Helper()
	value, found, err := unstructured.NestedBool(object.Object, fields...)
	if err != nil {
		t.Fatalf("reading %v from Agent: %v", fields, err)
	}
	return found && value
}

func workerProofAgent(mac, ip string) *unstructured.Unstructured {
	agent := &unstructured.Unstructured{Object: map[string]interface{}{}}
	_ = unstructured.SetNestedSlice(agent.Object, []interface{}{
		map[string]interface{}{"macAddress": mac, "ipV4Addresses": []interface{}{ip + "/24"}},
	}, "status", "inventory", "interfaces")
	return agent
}

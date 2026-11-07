/*
Copyright 2026.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package controller

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/url"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	hypershiftv1beta1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	hypershiftipnet "github.com/openshift/hypershift/api/util/ipnet"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	apimeta "k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	k8sfake "k8s.io/client-go/kubernetes/fake"
	k8stesting "k8s.io/client-go/testing"
	clocktesting "k8s.io/utils/clock/testing"
	"k8s.io/utils/ptr"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	"github.com/osac-project/osac/osac-operator/api/v1alpha1"
	"github.com/osac-project/osac/osac-operator/internal/networktask"
	"github.com/osac-project/osac/osac-operator/pkg/aap"
)

var _ = Describe("ClusterOrder networking lifecycle", func() {
	It("uses the configured AAP prefix for cluster endpoint jobs", func() {
		h := newNetHarness(true)
		h.engine = networktask.New(k8sClient, k8sClient, h.aap, networktask.Config{
			TemplatePrefix: "tenant-osac", Organization: "osac", ProvisioningEnabled: true,
		})
		instance := createClusterOrderForIngress(h, "endpoint-template-prefix", "unused-subnet")
		reconciler := &ClusterOrderReconciler{Client: k8sClient, Engine: h.engine}

		_, err := reconciler.runClusterEndpointJob(ctx, instance, "publish_public_dns_api",
			map[string]any{"cluster_order": map[string]any{"id": "cluster-1"}}, false)
		Expect(err).NotTo(HaveOccurred())
		Expect(h.aap.latest("publish_public_dns_api", "none").template).To(Equal("tenant-osac-cluster-endpoints"))
	})

	It("classifies guest API transport failures as not usable", func() {
		Expect(isGuestAPITransportError(&url.Error{Op: "Get", URL: "https://guest.example.test", Err: fmt.Errorf("connection refused")})).To(BeTrue())
		Expect(isGuestAPITransportError(context.DeadlineExceeded)).To(BeTrue())
		Expect(isGuestAPITransportError(fmt.Errorf("permission denied"))).To(BeFalse())
	})

	It("treats transient guest API Service lookup failures as progressing", func() {
		for _, scenario := range []struct {
			name       string
			requestErr error
			cancel     bool
			wantErr    string
		}{
			{name: "service unavailable", requestErr: apierrors.NewServiceUnavailable("temporarily unavailable")},
			{name: "request timeout", requestErr: apierrors.NewTimeoutError("request timed out", 1)},
			{name: "transport failure", requestErr: &url.Error{
				Op: "Get", URL: "https://guest.example.test", Err: fmt.Errorf("connection reset"),
			}},
			{name: "client request deadline", requestErr: context.DeadlineExceeded},
			{name: "caller cancellation", requestErr: context.Canceled, cancel: true, wantErr: context.Canceled.Error()},
			{name: "other API error", requestErr: fmt.Errorf("permission denied"), wantErr: "get guest external-ingress Service"},
		} {
			By(scenario.name)
			guestClient := k8sfake.NewSimpleClientset()
			guestClient.PrependReactor("get", "services", func(k8stesting.Action) (bool, runtime.Object, error) {
				return true, nil, scenario.requestErr
			})
			requestContext := context.Background()
			if scenario.cancel {
				var cancel context.CancelFunc
				requestContext, cancel = context.WithCancel(requestContext)
				cancel()
			}

			address, err := guestIngressServiceAddress(requestContext, guestClient)
			Expect(address).To(BeEmpty())
			if scenario.wantErr == "" {
				Expect(err).NotTo(HaveOccurred())
			} else if scenario.cancel {
				Expect(err).To(MatchError(context.Canceled))
			} else {
				Expect(err).To(MatchError(ContainSubstring(scenario.wantErr)))
			}
		}
	})

	It("reports disabled provisioning without launching endpoint jobs", func() {
		h := newNetHarness(false)
		instance := &v1alpha1.ClusterOrder{}
		reconciler := &ClusterOrderReconciler{Engine: h.engine}

		Expect(reconciler.reconcileClusterEndpointLifecycle(ctx, instance, nil, true)).To(Succeed())
		condition := apimeta.FindStatusCondition(instance.Status.Conditions, string(v1alpha1.ClusterOrderConditionEndpointsReady))
		Expect(condition).NotTo(BeNil())
		Expect(condition.Status).To(Equal(metav1.ConditionTrue))
		Expect(condition.Reason).To(Equal(endpointReasonProvisioningDisabled))
		Expect(h.aap.tasks("")).To(BeEmpty())
	})

	It("waits for the HostedCluster before checking required automatic access", func() {
		h := newNetHarness(true)
		instance := &v1alpha1.ClusterOrder{}
		reconciler := &ClusterOrderReconciler{Engine: h.engine}

		Expect(reconciler.reconcileClusterEndpointLifecycle(ctx, instance, nil, false)).To(Succeed())
		condition := apimeta.FindStatusCondition(instance.Status.Conditions, string(v1alpha1.ClusterOrderConditionEndpointsReady))
		Expect(condition).NotTo(BeNil())
		Expect(condition.Reason).To(Equal(endpointReasonProgressing))
		Expect(h.aap.tasks("")).To(BeEmpty())
	})

	It("does not treat an unresolved CaaS Fulfillment Cluster as a legacy order", func() {
		for _, scenario := range []struct {
			name   string
			client automaticClustersGetter
		}{
			{name: "client is unset"},
			{
				name: "Fulfillment returns NotFound",
				client: &mockClustersClient{
					getError: status.Error(codes.NotFound, "cluster not found"),
				},
			},
		} {
			By(scenario.name)
			h := newNetHarness(true)
			instance := &v1alpha1.ClusterOrder{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "unresolved-caas-cluster",
					Namespace: h.ns,
					Labels:    map[string]string{osacClusterOrderIDLabel: "caas-cluster-id"},
					Annotations: map[string]string{
						osacAPIEndpointAnnotation:     "192.0.2.10",
						osacIngressEndpointAnnotation: "192.0.2.11",
					},
				},
				Spec: v1alpha1.ClusterOrderSpec{TemplateID: standardCaaSTemplateID},
				Status: v1alpha1.ClusterOrderStatus{
					Phase: v1alpha1.ClusterOrderPhaseProgressing,
				},
			}
			hc := &hypershiftv1beta1.HostedCluster{
				ObjectMeta: metav1.ObjectMeta{Name: "hosted-caas", Namespace: h.ns},
				Status: hypershiftv1beta1.HostedClusterStatus{Conditions: []metav1.Condition{
					{Type: string(hypershiftv1beta1.KubeAPIServerAvailable), Status: metav1.ConditionTrue},
					{Type: string(hypershiftv1beta1.HostedClusterAvailable), Status: metav1.ConditionTrue},
					{Type: string(hypershiftv1beta1.HostedClusterDegraded), Status: metav1.ConditionFalse},
					{Type: string(hypershiftv1beta1.ClusterVersionSucceeding), Status: metav1.ConditionTrue},
				}},
			}
			reconciler := &ClusterOrderReconciler{
				Client: k8sClient, apiReader: k8sClient, Engine: h.engine,
				ClustersClient: scenario.client, StatusPollInterval: time.Second,
			}

			Expect(reconciler.handleHostedCluster(ctx, instance, hc)).To(Succeed())
			Expect(instance.Status.ApiEndpoint).To(BeEmpty(),
				"an unresolved Cluster must not select annotation-backed endpoint discovery")
			Expect(instance.Status.IngressEndpoint).To(BeEmpty())
			endpointCondition := apimeta.FindStatusCondition(instance.Status.Conditions,
				string(v1alpha1.ClusterOrderConditionEndpointsReady))
			Expect(endpointCondition).NotTo(BeNil())
			Expect(endpointCondition.Status).To(Equal(metav1.ConditionFalse))
			Expect(instance.Status.Phase).To(Equal(v1alpha1.ClusterOrderPhaseProgressing),
				"CaaS readiness remains gated until automatic endpoint access is confirmed")
		}
	})

	It("keeps the first recorded Fabric Manager for ingress reservation retries", func() {
		h := newNetHarness(true)
		vn := h.readyVirtualNetwork("caas-vn", false)
		subnet := h.readySubnet("caas-subnet", "caas-vn", false, testPlan)
		selected := &v1alpha1.NetworkManagerRef{Name: "tenant-fabric", AnsibleRole: "acme.network.fabric"}
		subnet.Status.NetworkManagers.Fabric = selected.DeepCopy()
		Expect(k8sClient.Status().Update(ctx, subnet)).To(Succeed())
		instance := createClusterOrderForIngress(h, "9f8e7d6c-5b4a-4321-8765-4c3b2a109876", subnet.Name)
		reconciler := &ClusterOrderReconciler{Client: k8sClient, Engine: h.engine}

		out, err := reconciler.runReserveClusterIngress(ctx, instance, subnet, vn)
		Expect(err).NotTo(HaveOccurred())
		Expect(out.Entry.State).To(Equal(v1alpha1.NetworkJobStateRunning))
		first := h.aap.latest(networktask.ReserveClusterIngressIP, string(v1alpha1.NetworkJobManagerFabric))
		Expect(first.vars["manager"]).To(HaveKeyWithValue("name", selected.Name))
		Expect(first.vars["manager"]).To(HaveKeyWithValue("ansible_role", selected.AnsibleRole))
		Expect(first.vars["cluster_id"]).To(Equal(instance.Labels[osacClusterOrderIDLabel]))

		// A later live Subnet manager change must not redirect a retry whose
		// original task input and manager were already persisted.
		subnet.Status.NetworkManagers.Fabric = &v1alpha1.NetworkManagerRef{
			Name: "replacement-fabric", AnsibleRole: "replacement.network.fabric",
		}
		Expect(k8sClient.Status().Update(ctx, subnet)).To(Succeed())
		instance.Status.Jobs[0].State = v1alpha1.NetworkJobStateCancelled
		Expect(k8sClient.Status().Update(ctx, instance)).To(Succeed())
		out, err = reconciler.runReserveClusterIngress(ctx, instance, subnet, vn)
		Expect(err).NotTo(HaveOccurred())
		Expect(out.Entry.State).To(Equal(v1alpha1.NetworkJobStateRunning))
		second := h.aap.latest(networktask.ReserveClusterIngressIP, string(v1alpha1.NetworkJobManagerFabric))
		Expect(second.vars["manager"]).To(HaveKeyWithValue("name", selected.Name))
		Expect(second.vars["manager"]).To(HaveKeyWithValue("ansible_role", selected.AnsibleRole))
		input, err := networktask.LoadInput(instance)
		Expect(err).NotTo(HaveOccurred())
		Expect(input["fabric_manager"]).To(Equal(map[string]any{
			"name": selected.Name, "ansible_role": selected.AnsibleRole,
		}))
		Expect(input["cluster_id"]).To(Equal(instance.Labels[osacClusterOrderIDLabel]))
	})

	It("requires the ClusterOrder, Subnet, and VirtualNetwork to share a tenant", func() {
		h := newNetHarness(true)
		vn := h.readyVirtualNetwork("tenant-vn", false)
		subnet := h.readySubnet("tenant-subnet", "tenant-vn", false, testPlan)
		instance := &v1alpha1.ClusterOrder{ObjectMeta: metav1.ObjectMeta{
			Annotations: map[string]string{networktask.TenantAnnotation: "tenant-a"},
		}}
		reconciler := &ClusterOrderReconciler{Client: k8sClient, NetworkingNamespace: h.ns}

		_, gotVN, err := reconciler.clusterEndpointSubnet(ctx, instanceWithSubnet(instance, subnet.Name))
		Expect(err).NotTo(HaveOccurred())
		Expect(gotVN.Name).To(Equal(vn.Name))

		vn.Annotations[networktask.TenantAnnotation] = "tenant-b"
		Expect(k8sClient.Update(ctx, vn)).To(Succeed())
		_, _, err = reconciler.clusterEndpointSubnet(ctx, instanceWithSubnet(instance, subnet.Name))
		Expect(err).To(HaveOccurred())
		Expect(isClusterEndpointTenantMismatch(err)).To(BeTrue())
		Expect(err.Error()).To(ContainSubstring("must belong to the same tenant"))

		mismatchedInstance := instanceWithSubnet(instance, subnet.Name)
		configured, err := reconciler.reconcileClusterGuestIngress(ctx, mismatchedInstance, nil, nil, false)
		Expect(err).NotTo(HaveOccurred())
		Expect(configured).To(BeFalse())
		condition := apimeta.FindStatusCondition(mismatchedInstance.Status.Conditions,
			string(v1alpha1.ClusterOrderConditionEndpointsReady))
		Expect(condition).NotTo(BeNil())
		Expect(condition.Status).To(Equal(metav1.ConditionFalse))
		Expect(condition.Reason).To(Equal(endpointReasonExternalAccessFailed))
		Expect(condition.Message).To(ContainSubstring("must belong to the same tenant"))
	})

	It("reports endpoint tenant mismatches as a management failure", func() {
		h := newNetHarness(true)
		vn := h.readyVirtualNetwork("tenant-check-vn", false)
		subnet := h.readySubnet("tenant-check-subnet", "tenant-check-vn", false, testPlan)
		vn.Annotations[networktask.TenantAnnotation] = "tenant-b"
		Expect(k8sClient.Update(ctx, vn)).To(Succeed())
		subnet.Annotations[networktask.TenantAnnotation] = "tenant-a"
		Expect(k8sClient.Update(ctx, subnet)).To(Succeed())

		_, podNetwork, err := net.ParseCIDR("10.128.0.0/14")
		Expect(err).NotTo(HaveOccurred())
		_, serviceNetwork, err := net.ParseCIDR("172.31.0.0/16")
		Expect(err).NotTo(HaveOccurred())
		instance := &v1alpha1.ClusterOrder{
			ObjectMeta: metav1.ObjectMeta{Annotations: map[string]string{
				networktask.TenantAnnotation: "tenant-a",
			}},
			Spec: v1alpha1.ClusterOrderSpec{
				NetworkAttachment: &v1alpha1.ClusterNetworkAttachment{SubnetRef: subnet.Name},
			},
		}
		hc := &hypershiftv1beta1.HostedCluster{Spec: hypershiftv1beta1.HostedClusterSpec{
			Networking: hypershiftv1beta1.ClusterNetworking{
				ClusterNetwork: []hypershiftv1beta1.ClusterNetworkEntry{{CIDR: hypershiftipnet.IPNet(*podNetwork)}},
				ServiceNetwork: []hypershiftv1beta1.ServiceNetworkEntry{{CIDR: hypershiftipnet.IPNet(*serviceNetwork)}},
			},
		}}
		reconciler := &ClusterOrderReconciler{Client: k8sClient, NetworkingNamespace: h.ns}

		_, check, err := reconciler.managementClusterCIDRs(ctx, instance, hc)
		Expect(err).NotTo(HaveOccurred())
		Expect(check.ready).To(BeFalse())
		Expect(check.reason).To(Equal(managementPublicationFailed))
		Expect(check.message).To(ContainSubstring("must belong to the same tenant"))
	})

	It("waits for the automatic ExternalIP and attachment before publishing an endpoint", func() {
		h := newNetHarness(true)
		clusterID := "2f5d7294-6ca8-473a-8a7f-919acdeb5712"
		instance := &v1alpha1.ClusterOrder{ObjectMeta: metav1.ObjectMeta{
			Name:      "endpoint-ready-order",
			Namespace: h.ns,
			Labels:    map[string]string{osacClusterOrderIDLabel: clusterID},
		}}
		ip := &v1alpha1.ExternalIP{ObjectMeta: metav1.ObjectMeta{
			Name: "endpoint-api-eip", Namespace: h.ns,
			Labels: map[string]string{
				autoCreatedLabel:         labelValueTrue,
				autoCreatedForLabel:      clusterID,
				autoCreatedKindLabel:     autoCreatedKindCluster,
				autoCreatedEndpointLabel: autoCreatedEndpointAPI,
			},
		}, Spec: v1alpha1.ExternalIPSpec{Pool: "pool"}}
		Expect(k8sClient.Create(ctx, ip)).To(Succeed())
		ip.Status.Address = "203.0.113.10"
		ip.Status.Conditions = []metav1.Condition{{
			Type: v1alpha1.NetworkConditionReady, Status: metav1.ConditionTrue,
			Reason: v1alpha1.ReasonSucceeded, LastTransitionTime: metav1.Now(),
		}}
		Expect(k8sClient.Status().Update(ctx, ip)).To(Succeed())
		reconciler := &ClusterOrderReconciler{Client: k8sClient, NetworkingNamespace: h.ns}

		ready, address, failure, err := reconciler.externalEndpointReady(ctx, instance, autoCreatedEndpointAPI)
		Expect(err).NotTo(HaveOccurred())
		Expect(failure).To(BeEmpty())
		Expect(ready).To(BeFalse())
		Expect(address).To(Equal("203.0.113.10"))

		attachment := &v1alpha1.ExternalIPAttachment{ObjectMeta: metav1.ObjectMeta{
			Name: "endpoint-api-eipa", Namespace: h.ns,
			Labels: map[string]string{
				autoCreatedLabel:         labelValueTrue,
				autoCreatedForLabel:      clusterID,
				autoCreatedEndpointLabel: autoCreatedEndpointAPI,
			},
		}, Spec: v1alpha1.ExternalIPAttachmentSpec{
			ExternalIP: "api-eip", Cluster: ptr.To(clusterID),
			TargetEndpoint: ptr.To(v1alpha1.ExternalIPAttachmentTargetEndpointAPI),
		}}
		Expect(k8sClient.Create(ctx, attachment)).To(Succeed())
		attachment.Status.Conditions = []metav1.Condition{{
			Type: v1alpha1.NetworkConditionReady, Status: metav1.ConditionTrue,
			Reason: v1alpha1.ReasonSucceeded, LastTransitionTime: metav1.Now(),
		}}
		Expect(k8sClient.Status().Update(ctx, attachment)).To(Succeed())

		ready, address, failure, err = reconciler.externalEndpointReady(ctx, instance, autoCreatedEndpointAPI)
		Expect(err).NotTo(HaveOccurred())
		Expect(failure).To(BeEmpty())
		Expect(ready).To(BeTrue())
		Expect(address).To(Equal("203.0.113.10"))
	})

	It("publishes the API URL with the HostedCluster API port", func() {
		h := newNetHarness(true)
		clusterID := "3f4bba29-e7ec-4d98-b810-1c4dd4a6e221"
		instance := &v1alpha1.ClusterOrder{ObjectMeta: metav1.ObjectMeta{
			Name:      "api-url-order",
			Namespace: h.ns,
			Labels:    map[string]string{osacClusterOrderIDLabel: clusterID},
		}, Spec: v1alpha1.ClusterOrderSpec{TemplateID: standardCaaSTemplateID}}
		apiIP := &v1alpha1.ExternalIP{ObjectMeta: metav1.ObjectMeta{
			Name: "api-url-eip", Namespace: h.ns,
			Labels: map[string]string{
				autoCreatedLabel:         labelValueTrue,
				autoCreatedForLabel:      clusterID,
				autoCreatedKindLabel:     autoCreatedKindCluster,
				autoCreatedEndpointLabel: autoCreatedEndpointAPI,
			},
		}, Spec: v1alpha1.ExternalIPSpec{Pool: "api-url-pool"}}
		attachment := &v1alpha1.ExternalIPAttachment{ObjectMeta: metav1.ObjectMeta{
			Name: "api-url-eipa", Namespace: h.ns,
			Labels: map[string]string{
				autoCreatedLabel:         labelValueTrue,
				autoCreatedForLabel:      clusterID,
				autoCreatedEndpointLabel: autoCreatedEndpointAPI,
			},
		}, Spec: v1alpha1.ExternalIPAttachmentSpec{
			ExternalIP: "api-url-eip", Cluster: ptr.To(clusterID),
			TargetEndpoint: ptr.To(v1alpha1.ExternalIPAttachmentTargetEndpointAPI),
		}}
		Expect(k8sClient.Create(ctx, instance)).To(Succeed())
		Expect(k8sClient.Create(ctx, apiIP)).To(Succeed())
		apiIP.Status.Address = "203.0.113.10"
		apiIP.Status.Conditions = []metav1.Condition{{
			Type: v1alpha1.NetworkConditionReady, Status: metav1.ConditionTrue,
			Reason: v1alpha1.ReasonSucceeded, LastTransitionTime: metav1.Now(),
		}}
		Expect(k8sClient.Status().Update(ctx, apiIP)).To(Succeed())
		Expect(k8sClient.Create(ctx, attachment)).To(Succeed())
		attachment.Status.Conditions = []metav1.Condition{{
			Type: v1alpha1.NetworkConditionReady, Status: metav1.ConditionTrue,
			Reason: v1alpha1.ReasonSucceeded, LastTransitionTime: metav1.Now(),
		}}
		Expect(k8sClient.Status().Update(ctx, attachment)).To(Succeed())
		hc := &hypershiftv1beta1.HostedCluster{
			ObjectMeta: metav1.ObjectMeta{Name: "api-url-cluster"},
			Spec: hypershiftv1beta1.HostedClusterSpec{
				DNS: hypershiftv1beta1.DNSSpec{BaseDomain: "example.test"},
			},
			Status: hypershiftv1beta1.HostedClusterStatus{
				ControlPlaneEndpoint: hypershiftv1beta1.APIEndpoint{Port: 6443},
			},
		}
		reconciler := &ClusterOrderReconciler{
			Client: k8sClient, Engine: h.engine, NetworkingNamespace: h.ns,
		}
		apiReady, address, failure, err := reconciler.externalEndpointReady(ctx, instance, autoCreatedEndpointAPI)
		Expect(err).NotTo(HaveOccurred())
		Expect(failure).To(BeEmpty())
		Expect(apiReady).To(BeTrue())
		Expect(address).To(Equal("203.0.113.10"))

		ready, _, _, err := reconciler.publishClusterAPIEndpointDNS(ctx, instance, hc)
		Expect(err).NotTo(HaveOccurred())
		Expect(ready).To(BeFalse())
		latestAttachment := &v1alpha1.ExternalIPAttachment{}
		Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(attachment), latestAttachment)).To(Succeed())
		Expect(latestAttachment.Finalizers).To(ContainElement(clusterEndpointDNSFinalizer),
			"the attachment must be held before the publish job starts")
		h.aap.succeed("publish_public_dns_api", "none")

		ready, _, _, err = reconciler.publishClusterAPIEndpointDNS(ctx, instance, hc)
		Expect(err).NotTo(HaveOccurred())
		Expect(ready).To(BeTrue())
		Expect(instance.Status.APIURL).To(Equal("https://api.api-url-cluster.example.test:6443"))
	})

	It("holds automatic endpoint addresses until DNS cleanup succeeds", func() {
		h := newNetHarness(true)
		clk := clocktesting.NewFakeClock(time.Now())
		h.engine.WithClock(clk)
		clusterID := "fb4d3d80-65ad-43d2-9fa0-c26c81467f9c"
		instance := &v1alpha1.ClusterOrder{ObjectMeta: metav1.ObjectMeta{
			Name: "dns-hold-order", Namespace: h.ns,
			Labels: map[string]string{osacClusterOrderIDLabel: clusterID},
		}, Spec: v1alpha1.ClusterOrderSpec{TemplateID: standardCaaSTemplateID}}
		apiIP := &v1alpha1.ExternalIP{ObjectMeta: metav1.ObjectMeta{
			Name: "dns-hold-eip", Namespace: h.ns,
			Labels: map[string]string{
				autoCreatedLabel:         labelValueTrue,
				autoCreatedForLabel:      clusterID,
				autoCreatedKindLabel:     autoCreatedKindCluster,
				autoCreatedEndpointLabel: autoCreatedEndpointAPI,
			},
		}, Spec: v1alpha1.ExternalIPSpec{Pool: "dns-hold-pool"}}
		attachment := &v1alpha1.ExternalIPAttachment{ObjectMeta: metav1.ObjectMeta{
			Name: "dns-hold-eipa", Namespace: h.ns,
			Labels: map[string]string{
				autoCreatedLabel:         labelValueTrue,
				autoCreatedForLabel:      clusterID,
				autoCreatedEndpointLabel: autoCreatedEndpointAPI,
			},
			Finalizers: []string{"test.provider/finalizer"},
		}, Spec: v1alpha1.ExternalIPAttachmentSpec{
			ExternalIP: "dns-hold-eip", Cluster: ptr.To(clusterID),
			TargetEndpoint: ptr.To(v1alpha1.ExternalIPAttachmentTargetEndpointAPI),
		}}
		Expect(k8sClient.Create(ctx, instance)).To(Succeed())
		Expect(k8sClient.Create(ctx, apiIP)).To(Succeed())
		apiIP.Status = v1alpha1.ExternalIPStatus{
			Address: "203.0.113.44",
			Conditions: []metav1.Condition{{
				Type: v1alpha1.NetworkConditionReady, Status: metav1.ConditionTrue,
				Reason: v1alpha1.ReasonSucceeded, LastTransitionTime: metav1.Now(),
			}},
		}
		Expect(k8sClient.Status().Update(ctx, apiIP)).To(Succeed())
		Expect(k8sClient.Create(ctx, attachment)).To(Succeed())
		attachment.Status.Conditions = []metav1.Condition{{
			Type: v1alpha1.NetworkConditionReady, Status: metav1.ConditionTrue,
			Reason: v1alpha1.ReasonSucceeded, LastTransitionTime: metav1.Now(),
		}}
		Expect(k8sClient.Status().Update(ctx, attachment)).To(Succeed())
		workloadNamespace := &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{
			Name:   "dns-hold-cluster",
			Labels: map[string]string{osacClusterOrderNameLabel: instance.Name},
		}}
		Expect(k8sClient.Create(ctx, workloadNamespace)).To(Succeed())
		hc := &hypershiftv1beta1.HostedCluster{ObjectMeta: metav1.ObjectMeta{
			Name: "dns-hold-cluster", Namespace: workloadNamespace.Name,
			Labels: map[string]string{osacClusterOrderNameLabel: instance.Name},
		},
			Spec: hypershiftv1beta1.HostedClusterSpec{DNS: hypershiftv1beta1.DNSSpec{BaseDomain: "example.test"}},
			Status: hypershiftv1beta1.HostedClusterStatus{
				ControlPlaneEndpoint: hypershiftv1beta1.APIEndpoint{Port: 6443},
			}}
		Expect(k8sClient.Create(ctx, hc)).To(Succeed())
		// The test HostedCluster CRD omits spec.dns, so the API server prunes it
		// on Create. Restore the field on this in-memory object for publication.
		hc.Spec.DNS.BaseDomain = "example.test"
		hc.Status.ControlPlaneEndpoint.Port = 6443
		reconciler := &ClusterOrderReconciler{
			Client: k8sClient, apiReader: k8sClient, Engine: h.engine,
			NetworkingNamespace: h.ns, StatusPollInterval: time.Second,
		}

		apiReady, address, failure, err := reconciler.externalEndpointReady(ctx, instance, autoCreatedEndpointAPI)
		Expect(err).NotTo(HaveOccurred())
		Expect(failure).To(BeEmpty())
		Expect(apiReady).To(BeTrue())
		Expect(address).To(Equal("203.0.113.44"))
		ready, _, _, err := reconciler.publishClusterAPIEndpointDNS(ctx, instance, hc)
		Expect(err).NotTo(HaveOccurred())
		Expect(ready).To(BeFalse())
		Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(attachment), attachment)).To(Succeed())
		Expect(attachment.Finalizers).To(ContainElement(clusterEndpointDNSFinalizer))
		h.aap.succeed("publish_public_dns_api", "none")
		ready, _, _, err = reconciler.publishClusterAPIEndpointDNS(ctx, instance, hc)
		Expect(err).NotTo(HaveOccurred())
		Expect(ready).To(BeTrue())
		Expect(instance.Status.APIURL).To(Equal("https://api.dns-hold-cluster.example.test:6443"))
		setClusterOrderPhase(&instance.Status, v1alpha1.ClusterOrderPhaseReady)
		instance.SetStatusCondition(v1alpha1.ConditionProgressing, metav1.ConditionFalse,
			"Cluster is ready", v1alpha1.ReasonAsExpected)
		setClusterEndpointCondition(instance, metav1.ConditionTrue, endpointReasonAsExpected,
			"API DNS records are published")
		Expect(k8sClient.Status().Update(ctx, instance)).To(Succeed())
		storedOrder := &v1alpha1.ClusterOrder{}
		Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(instance), storedOrder)).To(Succeed())
		Expect(storedOrder.Status.APIURL).To(Equal("https://api.dns-hold-cluster.example.test:6443"))
		baseDomain, err := reconciler.clusterEndpointDNSBaseDomain(ctx, storedOrder)
		Expect(err).NotTo(HaveOccurred())
		Expect(baseDomain).To(Equal("example.test"))

		Expect(k8sClient.Delete(ctx, attachment)).To(Succeed())
		result, err := reconciler.Reconcile(ctx, reconcile.Request{NamespacedName: client.ObjectKeyFromObject(instance)})
		Expect(err).NotTo(HaveOccurred())
		Expect(result.RequeueAfter).To(BeNumerically(">", 0))
		Expect(h.aap.tasks(clusterID)).To(ContainElement("remove_public_dns_api/none"))
		Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(attachment), attachment)).To(Succeed())
		Expect(attachment.Finalizers).To(ContainElement(clusterEndpointDNSFinalizer))
		Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(apiIP), &v1alpha1.ExternalIP{})).To(Succeed(),
			"the ExternalIP remains while DNS removal is pending")

		h.aap.finish("remove_public_dns_api", "none", "failed", "")
		result, err = reconciler.Reconcile(ctx, reconcile.Request{NamespacedName: client.ObjectKeyFromObject(instance)})
		Expect(err).NotTo(HaveOccurred())
		Expect(result.RequeueAfter).To(BeNumerically(">", 0))
		Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(attachment), attachment)).To(Succeed())
		Expect(attachment.Finalizers).To(ContainElement(clusterEndpointDNSFinalizer),
			"a failed cleanup attempt must retain the DNS hold")
		Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(instance), instance)).To(Succeed())
		Expect(instance.Status.APIURL).To(Equal("https://api.dns-hold-cluster.example.test:6443"),
			"published status remains while DNS cleanup is still failing")
		Expect(instance.Status.APIDNSRecordsPublishedTime).NotTo(BeNil())
		Expect(instance.Status.Phase).To(Equal(v1alpha1.ClusterOrderPhaseReady))

		clk.Step(3 * time.Minute)
		result, err = reconciler.Reconcile(ctx, reconcile.Request{NamespacedName: client.ObjectKeyFromObject(instance)})
		Expect(err).NotTo(HaveOccurred())
		Expect(result.RequeueAfter).To(BeNumerically(">", 0))
		h.aap.succeed("remove_public_dns_api", "none")
		_, err = reconciler.Reconcile(ctx, reconcile.Request{NamespacedName: client.ObjectKeyFromObject(instance)})
		Expect(err).NotTo(HaveOccurred())
		Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(attachment), attachment)).To(Succeed())
		Expect(attachment.Finalizers).NotTo(ContainElement(clusterEndpointDNSFinalizer))
		Expect(attachment.Finalizers).To(ContainElement("test.provider/finalizer"))
		Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(apiIP), &v1alpha1.ExternalIP{})).To(Succeed())

		Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(instance), instance)).To(Succeed())
		Expect(instance.Status.APIURL).To(BeEmpty())
		Expect(instance.Status.APIDNSRecordsPublishedTime).To(BeNil())
		Expect(instance.Status.Phase).To(Equal(v1alpha1.ClusterOrderPhaseProgressing))
		progressing := apimeta.FindStatusCondition(instance.Status.Conditions, v1alpha1.ConditionProgressing)
		Expect(progressing).NotTo(BeNil())
		Expect(progressing.Status).To(Equal(metav1.ConditionTrue))
		endpointReady := apimeta.FindStatusCondition(instance.Status.Conditions,
			string(v1alpha1.ClusterOrderConditionEndpointsReady))
		Expect(endpointReady).NotTo(BeNil())
		Expect(endpointReady.Status).To(Equal(metav1.ConditionFalse))
		Expect(endpointReady.Reason).To(Equal(endpointReasonProgressing))
		for _, task := range []string{"publish_public_dns_api", "remove_public_dns_api"} {
			entry := networktask.FindJob(instance.Status.Jobs, task, v1alpha1.NetworkJobManagerNone)
			Expect(entry).NotTo(BeNil())
			Expect(entry.State).To(Equal(v1alpha1.NetworkJobStateRetrying), "task %s entry: %+v", task, entry)
			Expect(entry.Message).To(Equal(networktask.MessageRerun), "task %s entry: %+v", task, entry)
		}

		// Keep the old attachment in provider cleanup without rerunning DNS
		// removal or allowing the endpoint lifecycle to recreate it.
		launchesBeforeWait := h.aap.tasks(clusterID)
		result, err = reconciler.Reconcile(ctx, reconcile.Request{NamespacedName: client.ObjectKeyFromObject(instance)})
		Expect(err).NotTo(HaveOccurred())
		Expect(result.RequeueAfter).To(BeNumerically(">", 0))
		Expect(h.aap.tasks(clusterID)).To(Equal(launchesBeforeWait))

		attachment.Finalizers = nil
		Expect(k8sClient.Update(ctx, attachment)).To(Succeed())
		err = k8sClient.Get(ctx, client.ObjectKeyFromObject(attachment), attachment)
		Expect(apierrors.IsNotFound(err)).To(BeTrue(), "the provider can now finish deleting the old attachment")
		Expect(k8sClient.Delete(ctx, apiIP)).To(Succeed())

		replacementIP := &v1alpha1.ExternalIP{ObjectMeta: metav1.ObjectMeta{
			Name: "dns-hold-replacement-eip", Namespace: h.ns,
			Labels: map[string]string{
				autoCreatedLabel:         labelValueTrue,
				autoCreatedForLabel:      clusterID,
				autoCreatedKindLabel:     autoCreatedKindCluster,
				autoCreatedEndpointLabel: autoCreatedEndpointAPI,
			},
		}, Spec: v1alpha1.ExternalIPSpec{Pool: "dns-hold-pool"}}
		Expect(k8sClient.Create(ctx, replacementIP)).To(Succeed())
		replacementIP.Status = v1alpha1.ExternalIPStatus{
			Address: "203.0.113.45",
			Conditions: []metav1.Condition{{
				Type: v1alpha1.NetworkConditionReady, Status: metav1.ConditionTrue,
				Reason: v1alpha1.ReasonSucceeded, LastTransitionTime: metav1.Now(),
			}},
		}
		Expect(k8sClient.Status().Update(ctx, replacementIP)).To(Succeed())
		replacementAttachment := &v1alpha1.ExternalIPAttachment{ObjectMeta: metav1.ObjectMeta{
			Name: "dns-hold-replacement-eipa", Namespace: h.ns,
			Labels: map[string]string{
				autoCreatedLabel:         labelValueTrue,
				autoCreatedForLabel:      clusterID,
				autoCreatedEndpointLabel: autoCreatedEndpointAPI,
			},
			Finalizers: []string{"test.provider/finalizer"},
		}, Spec: v1alpha1.ExternalIPAttachmentSpec{
			ExternalIP: replacementIP.Name, Cluster: ptr.To(clusterID),
			TargetEndpoint: ptr.To(v1alpha1.ExternalIPAttachmentTargetEndpointAPI),
		}}
		Expect(k8sClient.Create(ctx, replacementAttachment)).To(Succeed())
		replacementAttachment.Status.Conditions = []metav1.Condition{{
			Type: v1alpha1.NetworkConditionReady, Status: metav1.ConditionTrue,
			Reason: v1alpha1.ReasonSucceeded, LastTransitionTime: metav1.Now(),
		}}
		Expect(k8sClient.Status().Update(ctx, replacementAttachment)).To(Succeed())

		ready, _, _, err = reconciler.publishClusterAPIEndpointDNS(ctx, instance, hc)
		Expect(err).NotTo(HaveOccurred())
		Expect(ready).To(BeFalse())
		entry := networktask.FindJob(instance.Status.Jobs, "publish_public_dns_api", v1alpha1.NetworkJobManagerNone)
		Expect(entry.Attempt).To(Equal(int32(2)))
		Expect(entry.State).To(Equal(v1alpha1.NetworkJobStateRunning))
		publishedVars := h.aap.latest("publish_public_dns_api", "none").vars["external_ip"].(map[string]any)
		Expect(publishedVars["address"]).To(Equal("203.0.113.45"))
		Expect(h.aap.latest("publish_public_dns_api", "none").vars["hosted_cluster_base_domain"]).To(Equal(hc.Spec.DNS.BaseDomain))
		h.aap.succeed("publish_public_dns_api", "none")
		ready, _, _, err = reconciler.publishClusterAPIEndpointDNS(ctx, instance, hc)
		Expect(err).NotTo(HaveOccurred())
		Expect(ready).To(BeTrue())

		removed, _, err := reconciler.removePublishedClusterDNS(ctx, instance)
		Expect(err).NotTo(HaveOccurred())
		Expect(removed).To(BeFalse())
		Expect(h.aap.latest("remove_public_dns_api", "none").vars["hosted_cluster_base_domain"]).To(Equal(hc.Spec.DNS.BaseDomain))
		entry = networktask.FindJob(instance.Status.Jobs, "remove_public_dns_api", v1alpha1.NetworkJobManagerNone)
		Expect(entry.Attempt).To(Equal(int32(3)))
		Expect(entry.State).To(Equal(v1alpha1.NetworkJobStateRunning))
	})

	It("attempts DNS cleanup after a partial publish attempt", func() {
		h := newNetHarness(true)
		instance := &v1alpha1.ClusterOrder{ObjectMeta: metav1.ObjectMeta{
			Name: "dns-partial", Namespace: h.ns,
			Labels: map[string]string{osacClusterOrderIDLabel: "99d510e5-2666-4a53-a52a-09007a5e3812"},
		}, Spec: v1alpha1.ClusterOrderSpec{TemplateID: standardCaaSTemplateID}}
		Expect(k8sClient.Create(ctx, instance)).To(Succeed())
		instance.Status.APIURL = "https://api.dns-partial.example.test:6443"
		instance.Status.Jobs = []v1alpha1.NetworkJobStatus{{
			Task: "publish_public_dns_api", Manager: v1alpha1.NetworkJobManagerNone,
			State: v1alpha1.NetworkJobStateFailed,
		}}
		workloadNamespace := &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{
			Name:   "dns-partial-workload",
			Labels: map[string]string{osacClusterOrderNameLabel: instance.Name},
		}}
		Expect(k8sClient.Create(ctx, workloadNamespace)).To(Succeed())
		hc := &hypershiftv1beta1.HostedCluster{ObjectMeta: metav1.ObjectMeta{
			Name: "dns-partial", Namespace: workloadNamespace.Name,
			Labels: map[string]string{osacClusterOrderNameLabel: instance.Name},
		}, Spec: hypershiftv1beta1.HostedClusterSpec{
			DNS: hypershiftv1beta1.DNSSpec{BaseDomain: "example.test"},
		}}
		Expect(k8sClient.Create(ctx, hc)).To(Succeed())
		Expect(k8sClient.Status().Update(ctx, instance)).To(Succeed())
		reconciler := &ClusterOrderReconciler{Client: k8sClient, Engine: h.engine}

		done, _, err := reconciler.removePublishedClusterDNS(ctx, instance)
		Expect(err).NotTo(HaveOccurred())
		Expect(done).To(BeFalse())
		Expect(h.aap.tasks(instance.Labels[osacClusterOrderIDLabel])).To(ContainElement(
			"remove_public_dns_api/none",
		))
	})

	It("cleans live DNS only for the endpoint whose attachment is deleting", func() {
		h := newNetHarness(true)
		clusterID := "4c43a7a5-17da-4f2c-8aef-1ecf8ee7f1a2"
		instance := &v1alpha1.ClusterOrder{ObjectMeta: metav1.ObjectMeta{
			Name: "dns-endpoint-scope", Namespace: h.ns,
			Labels: map[string]string{osacClusterOrderIDLabel: clusterID},
		}, Spec: v1alpha1.ClusterOrderSpec{TemplateID: standardCaaSTemplateID}, Status: v1alpha1.ClusterOrderStatus{
			Jobs: []v1alpha1.NetworkJobStatus{
				{Task: "publish_public_dns_api", Manager: v1alpha1.NetworkJobManagerNone,
					State: v1alpha1.NetworkJobStateSucceeded, Attempt: 1, JobID: "1001"},
				{Task: "publish_public_dns_ingress", Manager: v1alpha1.NetworkJobManagerNone,
					State: v1alpha1.NetworkJobStateSucceeded, Attempt: 1, JobID: "1002"},
			},
		}}
		workloadNamespace := &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{
			Name:   instance.Name,
			Labels: map[string]string{osacClusterOrderNameLabel: instance.Name},
		}}
		Expect(k8sClient.Create(ctx, workloadNamespace)).To(Succeed())
		hc := &hypershiftv1beta1.HostedCluster{ObjectMeta: metav1.ObjectMeta{
			Name: instance.Name, Namespace: workloadNamespace.Name,
			Labels: map[string]string{osacClusterOrderNameLabel: instance.Name},
		}, Spec: hypershiftv1beta1.HostedClusterSpec{
			DNS: hypershiftv1beta1.DNSSpec{BaseDomain: "example.test"},
		}}
		Expect(k8sClient.Create(ctx, hc)).To(Succeed())
		Expect(k8sClient.Create(ctx, instance)).To(Succeed())
		instance.Status.APIURL = "https://api.dns-endpoint-scope.example.test:6443"
		instance.Status.Jobs = []v1alpha1.NetworkJobStatus{
			{Task: "publish_public_dns_api", Manager: v1alpha1.NetworkJobManagerNone,
				State: v1alpha1.NetworkJobStateSucceeded, Attempt: 1, JobID: "1001"},
			{Task: "publish_public_dns_ingress", Manager: v1alpha1.NetworkJobManagerNone,
				State: v1alpha1.NetworkJobStateSucceeded, Attempt: 1, JobID: "1002"},
		}
		Expect(k8sClient.Status().Update(ctx, instance)).To(Succeed())
		h.aap.mu.Lock()
		h.aap.jobs[1001] = &aap.Job{ID: 1001, Status: "successful"}
		h.aap.jobs[1002] = &aap.Job{ID: 1002, Status: "successful"}
		h.aap.mu.Unlock()

		attachments := map[string]*v1alpha1.ExternalIPAttachment{}
		for _, endpoint := range []struct {
			name   string
			target v1alpha1.ExternalIPAttachmentTargetEndpoint
		}{
			{name: autoCreatedEndpointAPI, target: v1alpha1.ExternalIPAttachmentTargetEndpointAPI},
			{name: autoCreatedEndpointIngress, target: v1alpha1.ExternalIPAttachmentTargetEndpointIngress},
		} {
			attachment := &v1alpha1.ExternalIPAttachment{ObjectMeta: metav1.ObjectMeta{
				Name: endpoint.name + "-dns-eipa", Namespace: h.ns,
				Labels: map[string]string{
					autoCreatedLabel:         labelValueTrue,
					autoCreatedForLabel:      clusterID,
					autoCreatedEndpointLabel: endpoint.name,
				},
				Finalizers: []string{"test.provider/finalizer", clusterEndpointDNSFinalizer},
			}, Spec: v1alpha1.ExternalIPAttachmentSpec{
				ExternalIP: endpoint.name + "-dns-eip", Cluster: ptr.To(clusterID),
				TargetEndpoint: ptr.To(endpoint.target),
			}}
			Expect(k8sClient.Create(ctx, attachment)).To(Succeed())
			attachments[endpoint.name] = attachment
		}
		var foundAttachments v1alpha1.ExternalIPAttachmentList
		Expect(k8sClient.List(ctx, &foundAttachments,
			client.InNamespace(h.ns), client.MatchingLabels{autoCreatedLabel: labelValueTrue, autoCreatedForLabel: clusterID},
		)).To(Succeed())
		Expect(foundAttachments.Items).To(HaveLen(2))

		Expect(k8sClient.Delete(ctx, attachments[autoCreatedEndpointIngress])).To(Succeed())
		deletingIngress := &v1alpha1.ExternalIPAttachment{}
		Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(attachments[autoCreatedEndpointIngress]), deletingIngress)).To(Succeed())
		Expect(deletingIngress.DeletionTimestamp.IsZero()).To(BeFalse())
		reconciler := &ClusterOrderReconciler{
			Client: k8sClient, apiReader: k8sClient, Engine: h.engine,
			NetworkingNamespace: h.ns, StatusPollInterval: time.Second,
		}
		result, err := reconciler.Reconcile(ctx, reconcile.Request{NamespacedName: client.ObjectKeyFromObject(instance)})
		Expect(err).NotTo(HaveOccurred())
		Expect(result.RequeueAfter).To(BeNumerically(">", 0))
		Expect(h.aap.tasks(clusterID)).To(Equal([]string{"remove_public_dns_ingress/none"}))

		h.aap.succeed("remove_public_dns_ingress", "none")
		result, err = reconciler.Reconcile(ctx, reconcile.Request{NamespacedName: client.ObjectKeyFromObject(instance)})
		Expect(err).NotTo(HaveOccurred())
		Expect(result.RequeueAfter).To(Equal(time.Second))
		Expect(h.aap.tasks(clusterID)).To(Equal([]string{"remove_public_dns_ingress/none"}))

		latestAPI := &v1alpha1.ExternalIPAttachment{}
		Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(attachments[autoCreatedEndpointAPI]), latestAPI)).To(Succeed())
		Expect(latestAPI.Finalizers).To(ContainElement(clusterEndpointDNSFinalizer),
			"the API address remains held while only ingress is being replaced")
		latestIngress := &v1alpha1.ExternalIPAttachment{}
		Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(attachments[autoCreatedEndpointIngress]), latestIngress)).To(Succeed())
		Expect(latestIngress.Finalizers).NotTo(ContainElement(clusterEndpointDNSFinalizer))
		Expect(latestIngress.Finalizers).To(ContainElement("test.provider/finalizer"))

		Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(instance), instance)).To(Succeed())
		apiPublish := networktask.FindJob(instance.Status.Jobs, "publish_public_dns_api", v1alpha1.NetworkJobManagerNone)
		Expect(apiPublish).NotTo(BeNil())
		Expect(apiPublish.State).To(Equal(v1alpha1.NetworkJobStateSucceeded))
		Expect(networktask.FindJob(instance.Status.Jobs, "remove_public_dns_api", v1alpha1.NetworkJobManagerNone)).To(BeNil())
		for _, task := range []string{"publish_public_dns_ingress", "remove_public_dns_ingress"} {
			entry := networktask.FindJob(instance.Status.Jobs, task, v1alpha1.NetworkJobManagerNone)
			Expect(entry).NotTo(BeNil())
			Expect(entry.State).To(Equal(v1alpha1.NetworkJobStateRetrying))
			Expect(entry.Message).To(Equal(networktask.MessageRerun))
		}
	})

	It("cancels only the DNS publish job for the deleting endpoint", func() {
		h := newNetHarness(true)
		clusterID := "a1651997-c724-42c9-a36d-828f5f2b8a08"
		instance := &v1alpha1.ClusterOrder{ObjectMeta: metav1.ObjectMeta{
			Name: "dns-cancel-scope", Namespace: h.ns,
			Labels: map[string]string{osacClusterOrderIDLabel: clusterID},
		}, Spec: v1alpha1.ClusterOrderSpec{TemplateID: standardCaaSTemplateID}, Status: v1alpha1.ClusterOrderStatus{
			Jobs: []v1alpha1.NetworkJobStatus{
				{Task: "publish_public_dns_api", Manager: v1alpha1.NetworkJobManagerNone,
					State: v1alpha1.NetworkJobStateRunning, Attempt: 1, JobID: "2001"},
				{Task: "publish_public_dns_ingress", Manager: v1alpha1.NetworkJobManagerNone,
					State: v1alpha1.NetworkJobStateRunning, Attempt: 1, JobID: "2002"},
			},
		}}
		workloadNamespace := &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{
			Name:   instance.Name,
			Labels: map[string]string{osacClusterOrderNameLabel: instance.Name},
		}}
		Expect(k8sClient.Create(ctx, workloadNamespace)).To(Succeed())
		hc := &hypershiftv1beta1.HostedCluster{ObjectMeta: metav1.ObjectMeta{
			Name: instance.Name, Namespace: workloadNamespace.Name,
			Labels: map[string]string{osacClusterOrderNameLabel: instance.Name},
		}, Spec: hypershiftv1beta1.HostedClusterSpec{
			DNS: hypershiftv1beta1.DNSSpec{BaseDomain: "example.test"},
		}}
		Expect(k8sClient.Create(ctx, hc)).To(Succeed())
		Expect(k8sClient.Create(ctx, instance)).To(Succeed())
		instance.Status.APIURL = "https://api.dns-cancel-scope.example.test:6443"
		instance.Status.Jobs = []v1alpha1.NetworkJobStatus{
			{Task: "publish_public_dns_api", Manager: v1alpha1.NetworkJobManagerNone,
				State: v1alpha1.NetworkJobStateRunning, Attempt: 1, JobID: "2001"},
			{Task: "publish_public_dns_ingress", Manager: v1alpha1.NetworkJobManagerNone,
				State: v1alpha1.NetworkJobStateRunning, Attempt: 1, JobID: "2002"},
		}
		Expect(k8sClient.Status().Update(ctx, instance)).To(Succeed())
		Expect(instance.Status.Jobs).To(HaveLen(2))
		h.aap.mu.Lock()
		h.aap.jobs[2001] = &aap.Job{ID: 2001, Status: "running"}
		h.aap.jobs[2002] = &aap.Job{ID: 2002, Status: "running"}
		h.aap.mu.Unlock()
		reconciler := &ClusterOrderReconciler{Client: k8sClient, Engine: h.engine, StatusPollInterval: time.Second}
		Expect(networktask.Started(instance, "publish_public_dns_api", v1alpha1.NetworkJobManagerNone)).To(BeTrue())
		Expect(networktask.Started(instance, "publish_public_dns_ingress", v1alpha1.NetworkJobManagerNone)).To(BeTrue())

		done, retryAfter, err := reconciler.cancelClusterEndpointJobsForDelete(ctx, instance, autoCreatedEndpointIngress)
		Expect(err).NotTo(HaveOccurred())
		Expect(done).To(BeFalse())
		Expect(retryAfter).To(BeNumerically(">", 0))
		Expect(h.aap.cancelledJobs()).To(Equal([]int{2002}))
	})
})

func createClusterOrderForIngress(h *netHarness, id, subnetName string) *v1alpha1.ClusterOrder {
	instance := &v1alpha1.ClusterOrder{
		ObjectMeta: h.meta(kindClusterOrder, id),
		Spec: v1alpha1.ClusterOrderSpec{
			TemplateID:        "test",
			NetworkAttachment: &v1alpha1.ClusterNetworkAttachment{SubnetRef: subnetName},
		},
	}
	Expect(k8sClient.Create(ctx, instance)).To(Succeed())
	Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(instance), instance)).To(Succeed())
	return instance
}

func instanceWithSubnet(instance *v1alpha1.ClusterOrder, subnetName string) *v1alpha1.ClusterOrder {
	copy := instance.DeepCopy()
	copy.Spec.NetworkAttachment = &v1alpha1.ClusterNetworkAttachment{SubnetRef: subnetName}
	return copy
}

var _ = Describe("ClusterOrder delete dependency sequence", func() {
	It("does not wait for workers when no ingress reservation was recorded", func() {
		ctx := context.Background()
		name := crdTestName("delete-without-ingress")
		instance := &v1alpha1.ClusterOrder{
			ObjectMeta: metav1.ObjectMeta{
				Name: name, Namespace: crdTestName("orders"),
				Finalizers: []string{osacFinalizer, "osac.openshift.io/baremetalworker-finalizer"},
			},
			Status: v1alpha1.ClusterOrderStatus{
				Workers: []v1alpha1.WorkerStatus{{Name: "worker-0"}},
			},
		}
		fakeClient := fake.NewClientBuilder().
			WithScheme(k8sClient.Scheme()).
			WithStatusSubresource(instance).
			WithObjects(instance).
			Build()
		aap := newNetFakeAAP()
		engine := networktask.New(fakeClient, fakeClient, aap, networktask.Config{
			TemplatePrefix: "osac", Organization: "osac", ProvisioningEnabled: true,
		})
		reconciler := &ClusterOrderReconciler{
			Client: fakeClient, Engine: engine, StatusPollInterval: time.Second,
		}

		result, err := reconciler.handleDelete(ctx, reconcile.Request{}, instance)
		Expect(err).NotTo(HaveOccurred())
		Expect(result.RequeueAfter).To(BeZero())
		Expect(aap.tasks("")).To(BeEmpty())

		latest := &v1alpha1.ClusterOrder{}
		Expect(fakeClient.Get(ctx, client.ObjectKeyFromObject(instance), latest)).To(Succeed())
		Expect(latest.Status.Workers).To(HaveLen(1), "worker cleanup remains owned by its finalizer")
		Expect(latest.Finalizers).To(Equal([]string{"osac.openshift.io/baremetalworker-finalizer"}))
	})

	It("removes DNS and waits for HostedCluster and workers before releasing ingress", func() {
		ctx := context.Background()
		name := crdTestName("delete-ingress")
		orderNamespace := crdTestName("orders")
		networkingNamespace := crdTestName("networking")
		workloadNamespace := crdTestName("cluster")
		clusterID := "7a9306c1-30a2-48b8-a58a-139193cd0279"
		fabric := &v1alpha1.NetworkManagerRef{Name: "tenant-fabric", AnsibleRole: "acme.network.fabric"}
		input, err := json.Marshal(map[string]any{
			"fabric_manager": map[string]any{"name": fabric.Name, "ansible_role": fabric.AnsibleRole},
			"cluster_id":     clusterID,
			"resource":       map[string]any{"id": clusterID, "kind": kindClusterOrder},
		})
		Expect(err).NotTo(HaveOccurred())
		instance := &v1alpha1.ClusterOrder{
			ObjectMeta: metav1.ObjectMeta{
				Name: name, Namespace: orderNamespace, UID: "delete-ingress-uid",
				Labels: map[string]string{
					osacClusterOrderIDLabel:   clusterID,
					osacClusterOrderNameLabel: name,
				},
			},
			Status: v1alpha1.ClusterOrderStatus{
				APIURL:     "https://api.cluster.example.test:6443",
				ConsoleURL: "https://console-openshift-console.apps.cluster.example.test",
				TaskInput:  &runtime.RawExtension{Raw: input},
				Jobs: []v1alpha1.NetworkJobStatus{
					{Task: networktask.ReserveClusterIngressIP, Manager: v1alpha1.NetworkJobManagerFabric,
						State: v1alpha1.NetworkJobStateSucceeded},
					{Task: "publish_public_dns_api", Manager: v1alpha1.NetworkJobManagerNone,
						State: v1alpha1.NetworkJobStateSucceeded},
					{Task: "publish_public_dns_ingress", Manager: v1alpha1.NetworkJobManagerNone,
						State: v1alpha1.NetworkJobStateSucceeded},
				},
			},
		}
		clusterNamespace := &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{
			Name: workloadNamespace, Labels: map[string]string{osacClusterOrderNameLabel: name},
		}}
		attachments := make([]*v1alpha1.ExternalIPAttachment, 0, 2)
		for _, endpoint := range []struct {
			name   string
			target v1alpha1.ExternalIPAttachmentTargetEndpoint
		}{
			{name: autoCreatedEndpointAPI, target: v1alpha1.ExternalIPAttachmentTargetEndpointAPI},
			{name: autoCreatedEndpointIngress, target: v1alpha1.ExternalIPAttachmentTargetEndpointIngress},
		} {
			attachments = append(attachments, &v1alpha1.ExternalIPAttachment{
				ObjectMeta: metav1.ObjectMeta{
					Name: endpoint.name + "-endpoint-eipa", Namespace: networkingNamespace,
					Labels: map[string]string{
						autoCreatedLabel:         labelValueTrue,
						autoCreatedForLabel:      clusterID,
						autoCreatedEndpointLabel: endpoint.name,
					},
					Finalizers: []string{clusterEndpointDNSFinalizer},
				},
				Spec: v1alpha1.ExternalIPAttachmentSpec{
					ExternalIP: endpoint.name + "-endpoint-eip", Cluster: ptr.To(clusterID),
					TargetEndpoint: ptr.To(endpoint.target),
				},
			})
		}
		hc := &hypershiftv1beta1.HostedCluster{ObjectMeta: metav1.ObjectMeta{
			Name: "cluster", Namespace: workloadNamespace,
			Labels: map[string]string{osacClusterOrderNameLabel: name},
		}, Spec: hypershiftv1beta1.HostedClusterSpec{
			DNS: hypershiftv1beta1.DNSSpec{BaseDomain: "example.test"},
		}}
		objects := make([]client.Object, 0, 3+len(attachments))
		objects = append(objects, instance, clusterNamespace, hc)
		for _, attachment := range attachments {
			objects = append(objects, attachment)
		}
		fakeClient := fake.NewClientBuilder().
			WithScheme(k8sClient.Scheme()).
			WithStatusSubresource(instance, hc).
			WithIndex(&v1alpha1.ExternalIPAttachment{}, autoExternalIPAttachmentOwnerIndexField,
				func(obj client.Object) []string {
					attachment, ok := obj.(*v1alpha1.ExternalIPAttachment)
					if !ok {
						return nil
					}
					return autoExternalIPAttachmentOwnerIndexValues(attachment)
				}).
			WithObjects(objects...).
			Build()
		Expect(fakeClient.Status().Update(ctx, instance)).To(Succeed())
		aap := newNetFakeAAP()
		engine := networktask.New(fakeClient, fakeClient, aap, networktask.Config{
			TemplatePrefix: "osac", Organization: "osac", ProvisioningEnabled: true,
		})
		reconciler := &ClusterOrderReconciler{
			Client: fakeClient, Engine: engine, NetworkingNamespace: networkingNamespace,
			StatusPollInterval: time.Second,
		}

		result, err := reconciler.handleDelete(ctx, reconcile.Request{}, instance)
		Expect(err).NotTo(HaveOccurred())
		Expect(result.RequeueAfter).To(BeNumerically(">", 0))
		Expect(aap.tasks(clusterID)).To(Equal([]string{"remove_public_dns_api/none"}))
		for _, attachment := range attachments {
			latest := &v1alpha1.ExternalIPAttachment{}
			Expect(fakeClient.Get(ctx, client.ObjectKeyFromObject(attachment), latest)).To(Succeed())
			Expect(latest.Finalizers).To(ContainElement(clusterEndpointDNSFinalizer),
				"keep endpoint attachments held until both DNS cleanup tasks succeed")
		}
		aap.succeed("remove_public_dns_api", "none")

		result, err = reconciler.handleDelete(ctx, reconcile.Request{}, instance)
		Expect(err).NotTo(HaveOccurred())
		Expect(result.RequeueAfter).To(BeNumerically(">", 0))
		Expect(aap.tasks(clusterID)).To(Equal([]string{
			"remove_public_dns_api/none", "remove_public_dns_ingress/none",
		}))
		for _, attachment := range attachments {
			latest := &v1alpha1.ExternalIPAttachment{}
			Expect(fakeClient.Get(ctx, client.ObjectKeyFromObject(attachment), latest)).To(Succeed())
			Expect(latest.Finalizers).To(ContainElement(clusterEndpointDNSFinalizer))
		}
		aap.succeed("remove_public_dns_ingress", "none")

		result, err = reconciler.handleDelete(ctx, reconcile.Request{}, instance)
		Expect(err).NotTo(HaveOccurred())
		Expect(result.RequeueAfter).To(Equal(time.Second))
		Expect(aap.tasks(clusterID)).To(HaveLen(2), "must wait for the HostedCluster before releasing ingress")
		attachmentList := &v1alpha1.ExternalIPAttachmentList{}
		Expect(fakeClient.List(ctx, attachmentList, client.InNamespace(networkingNamespace))).To(Succeed())
		Expect(attachmentList.Items).To(BeEmpty(), "delete attachments only after successful DNS cleanup")
		result, err = reconciler.handleDelete(ctx, reconcile.Request{}, instance)
		Expect(err).NotTo(HaveOccurred())
		Expect(result.RequeueAfter).To(Equal(time.Second), "must wait for the HostedCluster")

		Expect(fakeClient.Delete(ctx, hc)).To(Succeed())
		instance.Status.Workers = []v1alpha1.WorkerStatus{{Name: "worker-0"}}
		Expect(fakeClient.Status().Update(ctx, instance)).To(Succeed())
		result, err = reconciler.handleDelete(ctx, reconcile.Request{}, instance)
		Expect(err).NotTo(HaveOccurred())
		Expect(result.RequeueAfter).To(Equal(time.Second))
		Expect(aap.tasks(clusterID)).To(HaveLen(2), "must wait for workers before releasing ingress")

		instance.Status.Workers = nil
		Expect(fakeClient.Status().Update(ctx, instance)).To(Succeed())
		_, err = reconciler.handleDelete(ctx, reconcile.Request{}, instance)
		Expect(err).NotTo(HaveOccurred())
		Expect(aap.tasks(clusterID)).To(Equal([]string{
			"remove_public_dns_api/none", "remove_public_dns_ingress/none",
			"release_cluster_ingress_ip/fabric",
		}))
	})
})

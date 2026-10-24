package controller

import (
	"context"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/utils/ptr"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	bmfov1alpha1 "github.com/osac-project/osac/bare-metal-fulfillment-operator/api/v1alpha1"
	v1alpha1 "github.com/osac-project/osac/osac-operator/api/v1alpha1"
)

var _ = Describe("AgentlessNet ExternalIPAttachment context", func() {
	const (
		networkingNamespace = "attachment-networking"
		bmaasNamespace      = "attachment-bmaas"
		attachmentID        = "11111111-1111-4111-8111-111111111111"
		externalIPID        = "22222222-2222-4222-8222-222222222222"
		bmiID               = "33333333-3333-4333-8333-333333333333"
		subnetID            = "44444444-4444-4444-8444-444444444444"
		virtualNetworkID    = "55555555-5555-4555-8555-555555555555"
	)

	var (
		reader         client.Reader
		attachment     *v1alpha1.ExternalIPAttachment
		externalIP     *v1alpha1.ExternalIP
		bmi            *bmfov1alpha1.BareMetalInstance
		subnet         *v1alpha1.Subnet
		virtualNetwork *v1alpha1.VirtualNetwork
		reconciler     *ExternalIPAttachmentReconciler
	)

	BeforeEach(func() {
		scheme := runtime.NewScheme()
		Expect(corev1.AddToScheme(scheme)).To(Succeed())
		Expect(v1alpha1.AddToScheme(scheme)).To(Succeed())
		Expect(bmfov1alpha1.AddToScheme(scheme)).To(Succeed())

		attachment = &v1alpha1.ExternalIPAttachment{
			ObjectMeta: metav1.ObjectMeta{
				Name: "attachment", Namespace: networkingNamespace,
				UID:         "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa",
				Annotations: map[string]string{osacTenantKey: "tenant-a"},
			},
			Spec: v1alpha1.ExternalIPAttachmentSpec{
				ExternalIP: externalIPID, BaremetalInstance: ptr.To(bmiID),
			},
		}
		externalIP = &v1alpha1.ExternalIP{
			ObjectMeta: metav1.ObjectMeta{
				Name: "public-ip", Namespace: networkingNamespace,
				UID:         "bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbbb",
				Labels:      map[string]string{osacExternalIPIDLabel: externalIPID},
				Annotations: map[string]string{osacTenantKey: "tenant-a"},
			},
			Status: v1alpha1.ExternalIPStatus{
				Phase:   v1alpha1.ExternalIPPhaseReady,
				State:   v1alpha1.ExternalIPStateAllocated,
				Address: "198.51.100.10",
			},
		}
		bmi = &bmfov1alpha1.BareMetalInstance{
			ObjectMeta: metav1.ObjectMeta{
				Name: "machine", Namespace: bmaasNamespace,
				UID:         "cccccccc-cccc-4ccc-8ccc-cccccccccccc",
				Labels:      map[string]string{osacBareMetalInstanceIDLabel: bmiID},
				Annotations: map[string]string{osacTenantKey: "tenant-a"},
			},
			Status: bmfov1alpha1.BareMetalInstanceStatus{
				Phase: bmfov1alpha1.BareMetalInstancePhaseReady,
				NetworkAttachmentStatuses: []bmfov1alpha1.BareMetalNetworkAttachmentStatus{{
					Primary: true, IPAddress: "10.0.1.10", SubnetRef: subnetID,
				}},
			},
		}
		subnet = &v1alpha1.Subnet{
			ObjectMeta: metav1.ObjectMeta{
				Name: "subnet", Namespace: networkingNamespace,
				UID:         "dddddddd-dddd-4ddd-8ddd-dddddddddddd",
				Labels:      map[string]string{osacSubnetIDLabel: subnetID},
				Annotations: map[string]string{osacTenantKey: "tenant-a"},
			},
			Spec:   v1alpha1.SubnetSpec{VirtualNetwork: virtualNetworkID, IPv4CIDR: "10.0.1.0/24"},
			Status: v1alpha1.SubnetStatus{Phase: v1alpha1.SubnetPhaseReady},
		}
		virtualNetwork = &v1alpha1.VirtualNetwork{
			ObjectMeta: metav1.ObjectMeta{
				Name: "network", Namespace: networkingNamespace,
				UID:         "eeeeeeee-eeee-4eee-8eee-eeeeeeeeeeee",
				Labels:      map[string]string{osacVirtualNetworkIDLabel: virtualNetworkID},
				Annotations: map[string]string{osacTenantKey: "tenant-a"},
			},
			Status: v1alpha1.VirtualNetworkStatus{Phase: v1alpha1.VirtualNetworkPhaseReady},
		}
		reader = fake.NewClientBuilder().WithScheme(scheme).WithObjects(
			externalIP, bmi, subnet, virtualNetwork,
		).Build()
		reconciler = &ExternalIPAttachmentReconciler{
			Client: reader.(client.Client), APIReader: reader,
			NetworkingNamespace:        networkingNamespace,
			BaremetalInstanceNamespace: bmaasNamespace,
		}
	})

	It("builds a complete tenant-owned context from current resources", func() {
		value, virtualNetworkName, pending, err := reconciler.resolveAgentlessAttachmentContext(
			context.Background(), attachment, externalIP, bmi,
		)
		Expect(err).NotTo(HaveOccurred())
		Expect(pending).To(BeFalse())
		Expect(virtualNetworkName).To(Equal("network"))
		Expect(value.ExternalIP.KubernetesUID).To(Equal(string(externalIP.UID)))
		Expect(value.ExternalIP.Address).To(Equal("198.51.100.10"))
		Expect(value.Target.KubernetesUID).To(Equal(string(bmi.UID)))
		Expect(value.Target.IPAddress).To(Equal("10.0.1.10"))
		Expect(value.Subnet.KubernetesUID).To(Equal(string(subnet.UID)))
		Expect(value.VirtualNetwork.KubernetesUID).To(Equal(string(virtualNetwork.UID)))
	})

	It("waits while the target is not Ready", func() {
		bmi.Status.Phase = bmfov1alpha1.BareMetalInstancePhaseProgressing
		_, _, pending, err := reconciler.resolveAgentlessAttachmentContext(
			context.Background(), attachment, externalIP, bmi,
		)
		Expect(err).NotTo(HaveOccurred())
		Expect(pending).To(BeTrue())
	})

	It("rejects a tenant mismatch before dispatch", func() {
		bmi.Annotations[osacTenantKey] = "tenant-b"
		_, _, _, err := reconciler.resolveAgentlessAttachmentContext(
			context.Background(), attachment, externalIP, bmi,
		)
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring("tenant ownership"))
	})

	It("rejects a target address outside the primary Subnet", func() {
		bmi.Status.NetworkAttachmentStatuses[0].IPAddress = "10.0.2.10"
		_, _, _, err := reconciler.resolveAgentlessAttachmentContext(
			context.Background(), attachment, externalIP, bmi,
		)
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring("outside its Subnet"))
	})

	It("waits until the target Subnet exists", func() {
		Expect(reader.(client.Client).Delete(context.Background(), subnet)).To(Succeed())
		_, _, pending, err := reconciler.resolveAgentlessAttachmentContext(
			context.Background(), attachment, externalIP, bmi,
		)
		Expect(err).NotTo(HaveOccurred())
		Expect(pending).To(BeTrue())
	})
})

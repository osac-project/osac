package controller

import (
	"context"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	"github.com/osac-project/osac/osac-operator/api/v1alpha1"
	"github.com/osac-project/osac/osac-operator/pkg/provisioning"
)

var _ = Describe("fabricOutputProvider", func() {
	It("loads typed fabric outputs from the Subnet namespace ConfigMap", func() {
		ctx := context.Background()
		scheme := runtime.NewScheme()
		Expect(corev1.AddToScheme(scheme)).To(Succeed())
		configMap := &corev1.ConfigMap{
			ObjectMeta: metav1.ObjectMeta{Name: "subnet-tenant-a-fabric-output", Namespace: "tenant-a"},
			Data: map[string]string{
				"l2_vni":                "4096",
				"l3_vni":                "8192",
				"fabric_reserved_range": "192.0.2.0/26",
			},
		}
		reader := fake.NewClientBuilder().WithScheme(scheme).WithObjects(configMap).Build()
		base := &mockSubnetProvider{
			getProvisionStatusFunc: func(_ context.Context, _ client.Object, jobID string) (provisioning.ProvisionStatus, error) {
				return provisioning.ProvisionStatus{JobID: jobID, State: v1alpha1.JobStateSucceeded}, nil
			},
		}
		provider := newFabricOutputProvider(base, reader)
		subnet := &v1alpha1.Subnet{ObjectMeta: metav1.ObjectMeta{Name: "tenant-a", Namespace: "tenant-a"}}

		status, err := provider.GetProvisionStatusWithExtraVars(ctx, subnet, "fabric-job")

		Expect(err).NotTo(HaveOccurred())
		Expect(status.ExtraVars).To(Equal(map[string]any{
			"l2_vni":                int32(4096),
			"l3_vni":                int32(8192),
			"fabric_reserved_range": "192.0.2.0/26",
		}))
	})

	It("returns an error when the fabric output ConfigMap is absent", func() {
		scheme := runtime.NewScheme()
		Expect(corev1.AddToScheme(scheme)).To(Succeed())
		base := &mockSubnetProvider{
			getProvisionStatusFunc: func(_ context.Context, _ client.Object, jobID string) (provisioning.ProvisionStatus, error) {
				return provisioning.ProvisionStatus{JobID: jobID, State: v1alpha1.JobStateSucceeded}, nil
			},
		}
		provider := newFabricOutputProvider(base, fake.NewClientBuilder().WithScheme(scheme).Build())
		subnet := &v1alpha1.Subnet{ObjectMeta: metav1.ObjectMeta{Name: "tenant-a", Namespace: "tenant-a"}}

		_, err := provider.GetProvisionStatusWithExtraVars(context.Background(), subnet, "fabric-job")

		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring("tenant-a/subnet-tenant-a-fabric-output"))
	})

	It("does not read fabric output until provisioning succeeds", func() {
		scheme := runtime.NewScheme()
		Expect(corev1.AddToScheme(scheme)).To(Succeed())
		base := &mockSubnetProvider{
			getProvisionStatusFunc: func(_ context.Context, _ client.Object, jobID string) (provisioning.ProvisionStatus, error) {
				return provisioning.ProvisionStatus{JobID: jobID, State: v1alpha1.JobStateRunning}, nil
			},
		}
		provider := newFabricOutputProvider(base, fake.NewClientBuilder().WithScheme(scheme).Build())
		subnet := &v1alpha1.Subnet{ObjectMeta: metav1.ObjectMeta{Name: "tenant-a", Namespace: "tenant-a"}}

		status, err := provider.GetProvisionStatusWithExtraVars(context.Background(), subnet, "fabric-job")

		Expect(err).NotTo(HaveOccurred())
		Expect(status.State).To(Equal(v1alpha1.JobStateRunning))
		Expect(status.ExtraVars).To(BeNil())
	})

	It("returns an error when the ConfigMap omits required fabric outputs", func() {
		scheme := runtime.NewScheme()
		Expect(corev1.AddToScheme(scheme)).To(Succeed())
		configMap := &corev1.ConfigMap{
			ObjectMeta: metav1.ObjectMeta{Name: "subnet-tenant-a-fabric-output", Namespace: "tenant-a"},
			Data:       map[string]string{"l2_vni": "4096", "l3_vni": "8192"},
		}
		reader := fake.NewClientBuilder().WithScheme(scheme).WithObjects(configMap).Build()
		base := &mockSubnetProvider{
			getProvisionStatusFunc: func(_ context.Context, _ client.Object, jobID string) (provisioning.ProvisionStatus, error) {
				return provisioning.ProvisionStatus{JobID: jobID, State: v1alpha1.JobStateSucceeded}, nil
			},
		}
		provider := newFabricOutputProvider(base, reader)
		subnet := &v1alpha1.Subnet{ObjectMeta: metav1.ObjectMeta{Name: "tenant-a", Namespace: "tenant-a"}}

		_, err := provider.GetProvisionStatusWithExtraVars(context.Background(), subnet, "fabric-job")

		Expect(err).To(MatchError(ContainSubstring("fabric_reserved_range")))
	})

	It("returns a non-successful job status without looking up a Subnet output", func() {
		scheme := runtime.NewScheme()
		Expect(corev1.AddToScheme(scheme)).To(Succeed())
		base := &mockSubnetProvider{
			getProvisionStatusFunc: func(_ context.Context, _ client.Object, jobID string) (provisioning.ProvisionStatus, error) {
				return provisioning.ProvisionStatus{JobID: jobID, State: v1alpha1.JobStateFailed}, nil
			},
		}
		provider := newFabricOutputProvider(base, fake.NewClientBuilder().WithScheme(scheme).Build())

		status, err := provider.GetProvisionStatusWithExtraVars(context.Background(), &v1alpha1.Subnet{}, "fabric-job")

		Expect(err).NotTo(HaveOccurred())
		Expect(status.State).To(Equal(v1alpha1.JobStateFailed))
		Expect(status.ExtraVars).To(BeNil())
	})

	It("returns an error when outputs are requested for a non-Subnet resource", func() {
		scheme := runtime.NewScheme()
		Expect(corev1.AddToScheme(scheme)).To(Succeed())
		base := &mockSubnetProvider{
			getProvisionStatusFunc: func(_ context.Context, _ client.Object, jobID string) (provisioning.ProvisionStatus, error) {
				return provisioning.ProvisionStatus{JobID: jobID, State: v1alpha1.JobStateSucceeded}, nil
			},
		}
		provider := newFabricOutputProvider(base, fake.NewClientBuilder().WithScheme(scheme).Build())
		resource := &v1alpha1.VirtualNetwork{ObjectMeta: metav1.ObjectMeta{Name: "vn"}}

		_, err := provider.GetProvisionStatusWithExtraVars(context.Background(), resource, "fabric-job")

		Expect(err).To(MatchError(ContainSubstring("requires a Subnet resource")))
	})
})

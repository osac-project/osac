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
	"errors"
	"fmt"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	osacv1alpha1 "github.com/osac-project/osac/osac-operator/api/v1alpha1"
	"github.com/osac-project/osac/osac-operator/pkg/dispatcher"
	"github.com/osac-project/osac/osac-operator/pkg/networkmanager"
)

var _ = Describe("Subnet sequential provisioning policy", func() {
	var (
		scheme *runtime.Scheme
		plan   *dispatcher.DispatchPlan
	)

	BeforeEach(func() {
		scheme = runtime.NewScheme()
		Expect(osacv1alpha1.AddToScheme(scheme)).To(Succeed())
		plan = &dispatcher.DispatchPlan{Targets: []dispatcher.DispatchTarget{
			{Role: dispatcher.ManagerRoleFabric, Manager: networkmanager.Manager{Name: "netris"}},
			{Role: dispatcher.ManagerRoleK8s, Manager: networkmanager.Manager{Name: "cudn_evpn"}},
		}}
	})

	It("keeps the oldest Subnet on K8s even when both Subnets exist before reconcile", func() {
		older := sequentialSubnet("z-older", "vn-a", time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC))
		newer := sequentialSubnet("a-newer", "vn-a", time.Date(2026, 10, 2, 0, 0, 0, 0, time.UTC))
		unrelatedNetwork := sequentialSubnet("unrelated", "vn-b", time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC))
		k8sClient := fake.NewClientBuilder().WithScheme(scheme).WithObjects(older, newer, unrelatedNetwork).Build()
		reconciler := &SubnetReconciler{Client: k8sClient, APIReader: k8sClient}

		result, err := reconciler.applySequentialProvisioningPolicy(context.Background(), newer, plan)
		Expect(err).NotTo(HaveOccurred())
		Expect(result.K8sTarget()).To(BeNil())
		Expect(result.FabricTarget()).NotTo(BeNil())
		Expect(result.FabricTarget().Manager.Name).To(Equal("netris"))

		result, err = reconciler.applySequentialProvisioningPolicy(context.Background(), older, plan)
		Expect(err).NotTo(HaveOccurred())
		Expect(result.K8sTarget()).NotTo(BeNil())
		Expect(result.FabricTarget()).NotTo(BeNil())
	})

	It("selects the oldest persisted Subnet from the envtest API server", func() {
		// Unlike the fake-client cases above, this exercises the policy against the
		// suite's real Kubernetes API server and its persisted Subnet metadata.
		suffix := fmt.Sprintf("%x", time.Now().UnixNano())
		virtualNetworkID := "envtest-vnet-" + suffix
		tenantID := "envtest-tenant-" + suffix
		namespace := &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{
			Name: "envtest-sequential-" + suffix,
			Annotations: map[string]string{
				osacTenantKey:                       tenantID,
				"osac.openshift.io/owner-reference": virtualNetworkID,
			},
		}}
		Expect(k8sClient.Create(context.Background(), namespace)).To(Succeed())
		DeferCleanup(func() {
			Expect(client.IgnoreNotFound(k8sClient.Delete(context.Background(), namespace))).To(Succeed())
		})

		older := sequentialSubnet("z-envtest-older-"+suffix, virtualNetworkID, time.Time{})
		newer := sequentialSubnet("a-envtest-newer-"+suffix, virtualNetworkID, time.Time{})
		older.Spec.IPv4CIDR = "10.100.1.0/24"
		newer.Spec.IPv4CIDR = "10.100.2.0/24"
		for _, subnet := range []*osacv1alpha1.Subnet{older, newer} {
			subnet.Namespace = namespace.Name
			subnet.Annotations = map[string]string{
				osacTenantKey:                       tenantID,
				"osac.openshift.io/owner-reference": virtualNetworkID,
			}
		}

		Expect(k8sClient.Create(context.Background(), older)).To(Succeed())
		DeferCleanup(func() {
			Expect(client.IgnoreNotFound(k8sClient.Delete(context.Background(), older))).To(Succeed())
		})
		Expect(k8sClient.Create(context.Background(), newer)).To(Succeed())
		DeferCleanup(func() {
			Expect(client.IgnoreNotFound(k8sClient.Delete(context.Background(), newer))).To(Succeed())
		})

		// Read back server-assigned creation timestamps. If the API server records
		// both creates in the same timestamp tick, the controller's name tie-breaker
		// determines the expected oldest Subnet.
		persisted := []*osacv1alpha1.Subnet{{}, {}}
		Expect(k8sClient.Get(context.Background(), client.ObjectKeyFromObject(older), persisted[0])).To(Succeed())
		Expect(k8sClient.Get(context.Background(), client.ObjectKeyFromObject(newer), persisted[1])).To(Succeed())
		expectedOldest := persisted[0]
		if persisted[1].CreationTimestamp.Before(&persisted[0].CreationTimestamp) ||
			(persisted[1].CreationTimestamp.Equal(&persisted[0].CreationTimestamp) && persisted[1].Name < persisted[0].Name) {
			expectedOldest = persisted[1]
		}

		reconciler := &SubnetReconciler{Client: k8sClient, APIReader: k8sClient}
		for _, candidate := range persisted {
			selectedPlan, err := reconciler.applySequentialProvisioningPolicy(context.Background(), candidate, plan)
			Expect(err).NotTo(HaveOccurred())
			Expect(selectedPlan.FabricTarget()).NotTo(BeNil())
			if candidate.Name == expectedOldest.Name {
				Expect(selectedPlan.K8sTarget()).NotTo(BeNil(), candidate.Name)
			} else {
				Expect(selectedPlan.K8sTarget()).To(BeNil(), candidate.Name)
			}
		}
	})

	It("uses Subnet name as a stable tie-breaker for equal creation timestamps", func() {
		created := time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)
		nameLater := sequentialSubnet("z-name-later", "vn-a", created)
		nameEarlier := sequentialSubnet("a-name-earlier", "vn-a", created)
		k8sClient := fake.NewClientBuilder().WithScheme(scheme).WithObjects(nameLater, nameEarlier).Build()
		reconciler := &SubnetReconciler{Client: k8sClient, APIReader: k8sClient}

		result, err := reconciler.applySequentialProvisioningPolicy(context.Background(), nameLater, plan)
		Expect(err).NotTo(HaveOccurred())
		Expect(result.K8sTarget()).To(BeNil())

		result, err = reconciler.applySequentialProvisioningPolicy(context.Background(), nameEarlier, plan)
		Expect(err).NotTo(HaveOccurred())
		Expect(result.K8sTarget()).NotTo(BeNil())
	})

	It("includes the current Subnet when it is missing from the APIReader list", func() {
		current := sequentialSubnet("a-current-oldest", "vn-a", time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC))
		sibling := sequentialSubnet("z-listed-later", "vn-a", time.Date(2026, 10, 2, 0, 0, 0, 0, time.UTC))
		k8sClient := fake.NewClientBuilder().WithScheme(scheme).WithObjects(sibling).Build()
		reconciler := &SubnetReconciler{Client: k8sClient, APIReader: k8sClient}

		result, err := reconciler.applySequentialProvisioningPolicy(context.Background(), current, plan)
		Expect(err).NotTo(HaveOccurred())
		Expect(result.K8sTarget()).NotTo(BeNil())
	})

	It("honors an explicit skip annotation over previous K8s target history", func() {
		older := sequentialSubnet("older", "vn-a", time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC))
		later := sequentialSubnet("later", "vn-a", time.Date(2026, 10, 2, 0, 0, 0, 0, time.UTC))
		later.Annotations = map[string]string{
			osacSkipK8sManagerAnnotation:            labelValueTrue,
			osacK8sImplementationStrategyAnnotation: "cudn_evpn",
		}
		later.Status.ProvisioningJobs = []osacv1alpha1.JobStatus{{
			JobID:     "k8s-provision-job",
			Type:      osacv1alpha1.JobTypeProvision,
			Timestamp: metav1.NewTime(time.Date(2026, 10, 2, 0, 0, 0, 0, time.UTC)),
			State:     osacv1alpha1.JobStateSucceeded,
			Target:    string(dispatcher.ManagerRoleK8s),
		}}
		k8sClient := fake.NewClientBuilder().WithScheme(scheme).WithObjects(older, later).Build()
		reconciler := &SubnetReconciler{Client: k8sClient, APIReader: k8sClient}

		result, err := reconciler.applySequentialProvisioningPolicy(context.Background(), later, plan)
		Expect(err).NotTo(HaveOccurred())
		Expect(result.K8sTarget()).To(BeNil())
		Expect(result.FabricTarget()).NotTo(BeNil())
	})

	It("preserves a later Subnet's previously provisioned K8s target", func() {
		older := sequentialSubnet("older", "vn-a", time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC))
		later := sequentialSubnet("later", "vn-a", time.Date(2026, 10, 2, 0, 0, 0, 0, time.UTC))
		later.Annotations = map[string]string{osacK8sImplementationStrategyAnnotation: "cudn_evpn"}
		k8sClient := fake.NewClientBuilder().WithScheme(scheme).WithObjects(older, later).Build()
		reconciler := &SubnetReconciler{Client: k8sClient, APIReader: k8sClient}

		result, err := reconciler.applySequentialProvisioningPolicy(context.Background(), later, plan)
		Expect(err).NotTo(HaveOccurred())
		Expect(result.K8sTarget()).NotTo(BeNil())
	})

	It("preserves a later Subnet with K8s provisioning job history", func() {
		older := sequentialSubnet("older", "vn-a", time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC))
		later := sequentialSubnet("later", "vn-a", time.Date(2026, 10, 2, 0, 0, 0, 0, time.UTC))
		later.Status.ProvisioningJobs = []osacv1alpha1.JobStatus{{
			JobID:     "k8s-provision-job",
			Type:      osacv1alpha1.JobTypeProvision,
			Timestamp: metav1.NewTime(time.Date(2026, 10, 2, 0, 0, 0, 0, time.UTC)),
			State:     osacv1alpha1.JobStateSucceeded,
			Target:    string(dispatcher.ManagerRoleK8s),
		}}
		k8sClient := fake.NewClientBuilder().WithScheme(scheme).WithObjects(older, later).Build()
		reconciler := &SubnetReconciler{Client: k8sClient, APIReader: k8sClient}

		result, err := reconciler.applySequentialProvisioningPolicy(context.Background(), later, plan)
		Expect(err).NotTo(HaveOccurred())
		Expect(result.K8sTarget()).NotTo(BeNil())
	})

	It("returns an error when listing Subnets fails", func() {
		current := sequentialSubnet("current", "vn-a", time.Now())
		k8sClient := fake.NewClientBuilder().WithScheme(scheme).WithObjects(current).Build()
		reconciler := &SubnetReconciler{
			Client:    k8sClient,
			APIReader: sequentialListErrorReader{Reader: k8sClient, err: errors.New("APIReader unavailable")},
		}

		_, err := reconciler.applySequentialProvisioningPolicy(context.Background(), current, plan)
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring("APIReader unavailable"))
	})

	It("leaves other K8s managers unchanged", func() {
		later := sequentialSubnet("later", "vn-a", time.Now())
		later.Annotations = map[string]string{osacSkipK8sManagerAnnotation: labelValueTrue}
		otherManagerPlan := &dispatcher.DispatchPlan{Targets: []dispatcher.DispatchTarget{
			plan.Targets[0],
			{Role: dispatcher.ManagerRoleK8s, Manager: networkmanager.Manager{Name: "cudn_net"}},
		}}
		reconciler := &SubnetReconciler{}

		result, err := reconciler.applySequentialProvisioningPolicy(context.Background(), later, otherManagerPlan)
		Expect(err).NotTo(HaveOccurred())
		Expect(result.K8sTarget()).NotTo(BeNil())
	})
})

func sequentialSubnet(name, virtualNetwork string, created time.Time) *osacv1alpha1.Subnet {
	return &osacv1alpha1.Subnet{
		ObjectMeta: metav1.ObjectMeta{
			Name:              name,
			Namespace:         "osac",
			CreationTimestamp: metav1.NewTime(created),
		},
		Spec: osacv1alpha1.SubnetSpec{VirtualNetwork: virtualNetwork},
	}
}

type sequentialListErrorReader struct {
	client.Reader
	err error
}

func (r sequentialListErrorReader) List(context.Context, client.ObjectList, ...client.ListOption) error {
	return r.err
}

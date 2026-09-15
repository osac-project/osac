/*
Copyright 2025.

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

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/kubernetes/scheme"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	osacv1alpha1 "github.com/osac-project/osac/osac-operator/api/v1alpha1"
)

var _ = Describe("deriveAttachedSubnetScope", func() {
	const (
		testNS      = "test-namespace"
		testVNetUID = "test-vnet-uuid"
	)

	var (
		ctx        context.Context
		testScheme *runtime.Scheme
		reconciler *SecurityGroupReconciler
		sg         *osacv1alpha1.SecurityGroup
		subnetA    *osacv1alpha1.Subnet
		subnetB    *osacv1alpha1.Subnet
	)

	BeforeEach(func() {
		ctx = context.TODO()
		testScheme = runtime.NewScheme()
		Expect(osacv1alpha1.AddToScheme(testScheme)).To(Succeed())
		Expect(scheme.AddToScheme(testScheme)).To(Succeed())

		sg = &osacv1alpha1.SecurityGroup{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "test-sg",
				Namespace: testNS,
			},
			Spec: osacv1alpha1.SecurityGroupSpec{
				VirtualNetwork: testVNetUID,
			},
		}

		subnetA = &osacv1alpha1.Subnet{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "subnet-a",
				Namespace: testNS,
			},
			Spec: osacv1alpha1.SubnetSpec{
				VirtualNetwork: testVNetUID,
				IPv4CIDR:       "10.0.1.0/24",
			},
		}
		subnetB = &osacv1alpha1.Subnet{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "subnet-b",
				Namespace: testNS,
			},
			Spec: osacv1alpha1.SubnetSpec{
				VirtualNetwork: testVNetUID,
				IPv4CIDR:       "10.0.2.0/24",
			},
		}
	})

	setSubnetReady := func(c client.Client, subnet *osacv1alpha1.Subnet) {
		subnet.Status.Phase = osacv1alpha1.SubnetPhaseReady
		Expect(c.Status().Update(ctx, subnet)).To(Succeed())
	}

	It("returns empty scope when no workloads reference the SecurityGroup", func() {
		fakeClient := fake.NewClientBuilder().
			WithScheme(testScheme).
			WithObjects(sg, subnetA, subnetB).
			WithStatusSubresource(&osacv1alpha1.Subnet{}).
			Build()
		setSubnetReady(fakeClient, subnetA)
		setSubnetReady(fakeClient, subnetB)

		reconciler = &SecurityGroupReconciler{
			Client:                   fakeClient,
			NetworkingNamespace:      testNS,
			ComputeInstanceNamespace: testNS,
			ClusterOrderNamespace:    testNS,
		}

		scope, pending, err := reconciler.deriveAttachedSubnetScope(ctx, sg)
		Expect(err).NotTo(HaveOccurred())
		Expect(pending).To(BeFalse())
		Expect(scope.Refs).To(BeEmpty())
		Expect(scope.CIDRs).To(BeEmpty())
	})

	It("scopes to subnet-a only when ComputeInstance attaches SG on subnet-a", func() {
		ci := &osacv1alpha1.ComputeInstance{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "test-ci",
				Namespace: testNS,
			},
			Spec: osacv1alpha1.ComputeInstanceSpec{
				TemplateID: "default",
				NetworkAttachments: []osacv1alpha1.ComputeNetworkAttachment{
					{
						SubnetRef:         "subnet-a",
						SecurityGroupRefs: []string{"test-sg"},
					},
				},
			},
		}

		fakeClient := fake.NewClientBuilder().
			WithScheme(testScheme).
			WithObjects(sg, subnetA, subnetB, ci).
			WithStatusSubresource(&osacv1alpha1.Subnet{}).
			Build()
		setSubnetReady(fakeClient, subnetA)
		setSubnetReady(fakeClient, subnetB)

		reconciler = &SecurityGroupReconciler{
			Client:                   fakeClient,
			NetworkingNamespace:      testNS,
			ComputeInstanceNamespace: testNS,
			ClusterOrderNamespace:    testNS,
		}

		scope, pending, err := reconciler.deriveAttachedSubnetScope(ctx, sg)
		Expect(err).NotTo(HaveOccurred())
		Expect(pending).To(BeFalse())
		Expect(scope.Refs).To(Equal([]string{"subnet-a"}))
		Expect(scope.CIDRs).To(Equal([]string{"10.0.1.0/24"}))
	})

	It("includes subnet from ClusterOrder network attachment", func() {
		co := &osacv1alpha1.ClusterOrder{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "test-cluster",
				Namespace: testNS,
			},
			Spec: osacv1alpha1.ClusterOrderSpec{
				TemplateID: "default",
				NetworkAttachment: &osacv1alpha1.ClusterNetworkAttachment{
					SubnetRef:         "subnet-a",
					SecurityGroupRefs: []string{"test-sg"},
				},
			},
		}

		fakeClient := fake.NewClientBuilder().
			WithScheme(testScheme).
			WithObjects(sg, subnetA, co).
			WithStatusSubresource(&osacv1alpha1.Subnet{}).
			Build()
		setSubnetReady(fakeClient, subnetA)

		reconciler = &SecurityGroupReconciler{
			Client:                   fakeClient,
			NetworkingNamespace:      testNS,
			ComputeInstanceNamespace: testNS,
			ClusterOrderNamespace:    testNS,
		}

		scope, pending, err := reconciler.deriveAttachedSubnetScope(ctx, sg)
		Expect(err).NotTo(HaveOccurred())
		Expect(pending).To(BeFalse())
		Expect(scope.Refs).To(Equal([]string{"subnet-a"}))
		Expect(scope.CIDRs).To(Equal([]string{"10.0.1.0/24"}))
	})

	It("requeues when an attached subnet is not Ready", func() {
		ci := &osacv1alpha1.ComputeInstance{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "test-ci",
				Namespace: testNS,
			},
			Spec: osacv1alpha1.ComputeInstanceSpec{
				TemplateID: "default",
				NetworkAttachments: []osacv1alpha1.ComputeNetworkAttachment{
					{
						SubnetRef:         "subnet-a",
						SecurityGroupRefs: []string{"test-sg"},
					},
				},
			},
		}

		fakeClient := fake.NewClientBuilder().
			WithScheme(testScheme).
			WithObjects(sg, subnetA, ci).
			WithStatusSubresource(&osacv1alpha1.Subnet{}).
			Build()

		reconciler = &SecurityGroupReconciler{
			Client:                   fakeClient,
			NetworkingNamespace:      testNS,
			ComputeInstanceNamespace: testNS,
			ClusterOrderNamespace:    testNS,
		}

		scope, pending, err := reconciler.deriveAttachedSubnetScope(ctx, sg)
		Expect(err).NotTo(HaveOccurred())
		Expect(pending).To(BeTrue())
		Expect(scope.Refs).To(BeEmpty())
		Expect(scope.CIDRs).To(BeEmpty())
	})
})

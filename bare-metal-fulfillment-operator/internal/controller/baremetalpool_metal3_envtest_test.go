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
	metal3api "github.com/metal3-io/baremetal-operator/apis/metal3.io/v1alpha1"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"

	"github.com/osac-project/osac/bare-metal-fulfillment-operator/api/v1alpha1"
	"github.com/osac-project/osac/bare-metal-fulfillment-operator/internal/inventory"
)

var _ = Describe("BareMetalPool Metal3 Integration", func() {
	const poolName = "metal3-pool"
	const bmhName = "metal3-pool-bmh"

	BeforeEach(func() {
		ensureTestNamespace(metal3TestNS)
	})

	AfterEach(func() {
		instances := &v1alpha1.BareMetalInstanceList{}
		Expect(k8sClient.List(ctx, instances, client.InNamespace(metal3TestNS))).To(Succeed())
		for i := range instances.Items {
			cleanupBMI(instances.Items[i].Name)
		}

		pool := &v1alpha1.BareMetalPool{}
		err := k8sClient.Get(ctx, types.NamespacedName{Name: poolName, Namespace: metal3TestNS}, pool)
		if err == nil {
			controllerutil.RemoveFinalizer(pool, BareMetalPoolFinalizer)
			Expect(k8sClient.Update(ctx, pool)).To(Succeed())
			Expect(client.IgnoreNotFound(k8sClient.Delete(ctx, pool))).To(Succeed())
		} else {
			Expect(apierrors.IsNotFound(err)).To(BeTrue())
		}
		cleanupBMH(bmhName)
	})

	It("aggregates Ready after a pool instance is assigned a Metal3 host", func() {
		createMetal3BMH(bmhName, map[string]string{
			inventory.Metal3HostTypeLabel: "pool-node",
		}, metal3api.OperationalStatusOK, metal3api.StateAvailable)

		pool := &v1alpha1.BareMetalPool{
			ObjectMeta: metav1.ObjectMeta{Name: poolName, Namespace: metal3TestNS},
			Spec: v1alpha1.BareMetalPoolSpec{
				HostSets: []v1alpha1.BareMetalHostSet{{HostType: "pool-node", Replicas: 1}},
			},
		}
		Expect(k8sClient.Create(ctx, pool)).To(Succeed())

		poolReconciler := NewBareMetalPoolReconciler(
			k8sClient, k8sClient.Scheme(), nil, 0, 0, 0, 0,
		)
		poolRequest := ctrl.Request{NamespacedName: types.NamespacedName{
			Name: poolName, Namespace: metal3TestNS,
		}}
		result, err := poolReconciler.Reconcile(ctx, poolRequest)
		Expect(err).NotTo(HaveOccurred())
		Expect(result.RequeueAfter).To(Equal(DefaultHostReadyPollIntervalDuration))

		instances := &v1alpha1.BareMetalInstanceList{}
		Expect(k8sClient.List(ctx, instances,
			client.InNamespace(metal3TestNS),
			client.MatchingLabels{BareMetalPoolLabelKey: string(pool.UID)},
		)).To(Succeed())
		Expect(instances.Items).To(HaveLen(1))
		instanceName := instances.Items[0].Name

		instanceReconciler := newMetal3Reconciler()
		reconcileN(instanceReconciler, instanceName, 5)
		instance := getBMI(instanceName)
		Expect(instance.Spec.ExternalHostID).To(Equal(metal3TestNS + "/" + bmhName))
		Expect(instance.Status.Phase).To(Equal(v1alpha1.BareMetalInstancePhaseReady))

		result, err = poolReconciler.Reconcile(ctx, poolRequest)
		Expect(err).NotTo(HaveOccurred())
		Expect(result.IsZero()).To(BeTrue())

		Expect(k8sClient.Get(ctx, poolRequest.NamespacedName, pool)).To(Succeed())
		Expect(pool.Status.Phase).To(Equal(v1alpha1.BareMetalPoolPhaseReady))
		condition := pool.GetStatusCondition(v1alpha1.BareMetalPoolConditionTypeReady)
		Expect(condition).NotTo(BeNil())
		Expect(condition.Status).To(Equal(metav1.ConditionTrue))
		Expect(pool.Status.HostSets).To(ConsistOf(
			v1alpha1.BareMetalHostSet{HostType: "pool-node", Replicas: 1},
		))
	})
})

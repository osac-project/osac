/*
Copyright (c) 2026 Red Hat Inc.

Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except in compliance with the
License. You may obtain a copy of the License at

  http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on an
"AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the specific
language governing permissions and limitations under the License.
*/

package controller

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	osacv1alpha1 "github.com/osac-project/osac/osac-operator/api/v1alpha1"
)

// reserveExistingWorker models a controller crash after reserving the BMI name
// but before recording the ID from a successful create.
func reserveExistingWorker(order *osacv1alpha1.ClusterOrder, name string) {
	GinkgoHelper()
	latest := &osacv1alpha1.ClusterOrder{}
	Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(order), latest)).To(Succeed())
	instanceType := latest.Spec.NodeRequests[0].BareMetal.InstanceType
	now := metav1.Now()
	latest.Status.Workers = []osacv1alpha1.WorkerStatus{{
		Name: name, Kind: "BareMetalInstance", NodeSet: latest.Spec.NodeRequests[0].NodeSet,
		InstanceType: instanceType, Phase: "Provisioning", CreationTimestamp: now,
		BareMetalInstance: osacv1alpha1.BareMetalInstanceReference{Name: name},
		AttemptStartedAt:  &now,
	}}
	Expect(k8sClient.Status().Update(ctx, latest)).To(Succeed())
}

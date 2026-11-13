/*
Copyright (c) 2026 Red Hat Inc.

Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except in compliance with the
License. You may obtain a copy of the License at

  http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on an
"AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the specific
language governing permissions and limitations under the License.
*/

package it

import (
	"context"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	appsv1 "k8s.io/api/apps/v1"
	crclient "sigs.k8s.io/controller-runtime/pkg/client"

	privatev1 "github.com/osac-project/osac/proto/gen/osac/private/v1"
	publicv1 "github.com/osac-project/osac/proto/gen/osac/public/v1"
)

// waitForFulfillmentRollout waits for the current Deployment generation and both ingress routes. Deployment
// availability alone does not guarantee the proxy has replaced its old upstream connections. Fixtures must use this
// after both patching and restoring the shared grpc-server so subsequent specs can use either API immediately.
func waitForFulfillmentRollout(ctx context.Context, key crclient.ObjectKey) {
	GinkgoHelper()
	Eventually(ctx, func(g Gomega) {
		callCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
		defer cancel()
		current := &appsv1.Deployment{}
		g.Expect(tool.KubeClient().Get(callCtx, key, current)).To(Succeed())
		g.Expect(current.Status.ObservedGeneration).To(BeNumerically(">=", current.Generation))
		g.Expect(current.Status.UpdatedReplicas).To(Equal(*current.Spec.Replicas))
		g.Expect(current.Status.Replicas).To(Equal(*current.Spec.Replicas))
		g.Expect(current.Status.AvailableReplicas).To(Equal(*current.Spec.Replicas))
	}, 3*time.Minute, time.Second).Should(Succeed())
	// Count-only catalog reads establish API readiness without writes or billing-provider calls. Bound the calls
	// inside each attempt as well as the poll, including when this gate runs during cleanup.
	Eventually(ctx, func(g Gomega) {
		callCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
		defer cancel()
		_, err := privatev1.NewInstanceTypesClient(tool.InternalView().AdminConn()).List(callCtx,
			privatev1.InstanceTypesListRequest_builder{Limit: new(int32(0))}.Build())
		g.Expect(err).ToNot(HaveOccurred())
		_, err = publicv1.NewInstanceTypesClient(tool.ExternalView().AdminConn()).List(callCtx,
			publicv1.InstanceTypesListRequest_builder{Limit: new(int32(0))}.Build())
		g.Expect(err).ToNot(HaveOccurred())
	}, time.Minute, time.Second).Should(Succeed())
}

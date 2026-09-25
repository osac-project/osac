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
	"k8s.io/client-go/tools/events"

	"github.com/osac-project/osac/osac-operator/api/v1alpha1"
	privatev1 "github.com/osac-project/osac/proto/gen/osac/private/v1"
)

// Exercise removeFailedExcess directly: the ordinary reconcile calls handleFailedWorkers
// first, so an end-to-end failed scale-down case alone cannot reach this deletion branch.
type foreignExcessClient struct {
	FulfillmentClient
	bmi     *privatev1.BareMetalInstance
	deletes []string
}

func (f *foreignExcessClient) GetCluster(_ context.Context, id string) (*privatev1.Cluster, error) {
	return privatev1.Cluster_builder{Id: id, Metadata: privatev1.Metadata_builder{Tenant: "tenant1"}.Build()}.Build(), nil
}
func (f *foreignExcessClient) GetBareMetalInstance(_ context.Context, _ string) (*privatev1.BareMetalInstance, error) {
	return f.bmi, nil
}
func (f *foreignExcessClient) DeleteBareMetalInstance(_ context.Context, id string) error {
	f.deletes = append(f.deletes, id)
	return nil
}

var _ = Describe("BareMetalWorker failed excess tenant safety", func() {
	It("retains a foreign BMI status ID instead of deleting it during failed scale-down", func() {
		co := &v1alpha1.ClusterOrder{ObjectMeta: metav1.ObjectMeta{
			Name: "order-a", Annotations: map[string]string{"osac.openshift.io/tenant": "tenant1"},
			Labels: map[string]string{"osac.openshift.io/clusterorder-uuid": "cluster-a"},
		}}
		w := v1alpha1.WorkerStatus{Name: "order-a-worker-1", Kind: workerKindBMI, Phase: workerPhaseFailed, ResourceID: "foreign-id"}
		foreign := &foreignExcessClient{bmi: privatev1.BareMetalInstance_builder{
			Id: "foreign-id", Metadata: privatev1.Metadata_builder{
				Tenant: "tenant2", Name: w.Name,
				Labels:      map[string]string{"osac.openshift.io/cluster-order": co.Name},
				Annotations: map[string]string{"osac.openshift.io/owner-reference": "ClusterOrder/" + co.Name},
			}.Build(),
		}.Build()}
		r := &Reconciler{fulfillment: foreign, recorder: events.NewFakeRecorder(10)}
		kept := r.removeFailedExcess(context.Background(), co, nil, w)
		Expect(foreign.deletes).To(BeEmpty())
		Expect(kept).To(ConsistOf(w), "foreign status must be retained for safe recovery")
	})
})

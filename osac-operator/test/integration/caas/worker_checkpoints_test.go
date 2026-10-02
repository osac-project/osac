// Copyright (c) 2026 Red Hat Inc.
// SPDX-License-Identifier: Apache-2.0

package caas

import (
	"context"
	"errors"
	"fmt"
	"time"

	. "github.com/onsi/ginkgo/v2" //nolint:revive,staticcheck
	. "github.com/onsi/gomega"    //nolint:revive,staticcheck
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/tools/events"
	ctrl "sigs.k8s.io/controller-runtime"
	crclient "sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/osac-project/osac/osac-operator/api/v1alpha1"
	"github.com/osac-project/osac/osac-operator/internal/controller/baremetalworker"
	privatev1 "github.com/osac-project/osac/proto/gen/osac/private/v1"
)

// Delegate Create to the REAL service, then lose one successful acknowledgement.
// There is no fault injection in the production adapter or service.
type connectedWorkerResponseClient struct {
	baremetalworker.FulfillmentClient
	loseNext      bool
	lossError     error
	lost          *privatev1.BareMetalInstance
	omitLists     int
	creates       int
	alreadyExists int
	successful    []*privatev1.BareMetalInstance
}

func (f *connectedWorkerResponseClient) CreateBareMetalInstance(
	ctx context.Context, request *privatev1.BareMetalInstance,
) (*privatev1.BareMetalInstance, error) {
	f.creates++
	bmi, err := f.FulfillmentClient.CreateBareMetalInstance(ctx, request)
	if err != nil {
		if status.Code(err) == codes.AlreadyExists {
			f.alreadyExists++
		}
		return nil, err
	}
	f.successful = append(f.successful, bmi)
	if f.loseNext {
		f.loseNext = false
		f.lost = bmi
		f.lossError = status.Error(codes.DeadlineExceeded, "lost real successful Create acknowledgement")
		return nil, f.lossError
	}
	return bmi, nil
}

func (f *connectedWorkerResponseClient) ListBareMetalInstances(
	ctx context.Context, filter string,
) ([]*privatev1.BareMetalInstance, error) {
	if f.omitLists > 0 {
		f.omitLists--
		return nil, nil
	}
	return f.FulfillmentClient.ListBareMetalInstances(ctx, filter)
}

func connectedWorkerReconciler(
	client crclient.Client, scheme *runtime.Scheme, provider baremetalworker.FulfillmentClient,
) *baremetalworker.Reconciler {
	return baremetalworker.NewReconciler(client, client, scheme, provider,
		baremetalworker.NewIgnitionFetcher(nil), events.NewFakeRecorder(100), connectedConfig.namespace)
}

// Dependencies are explicitly ready before this finite trace. Errors and real
// dependency/backoff results are not hidden by polling or sleeping.
func driveConnectedWorkerCheckpoints(ctx context.Context, client crclient.Client, scheme *runtime.Scheme,
	key crclient.ObjectKey, provider *connectedWorkerResponseClient, count int,
) {
	GinkgoHelper()
	r := connectedWorkerReconciler(client, scheme, provider)
	lossHandled := false
	for step := 0; step < 16+8*count; step++ {
		before := &v1alpha1.ClusterOrder{}
		Expect(client.Get(ctx, key, before)).To(Succeed())
		createsBefore := provider.creates
		res, err := r.Reconcile(ctx, ctrl.Request{NamespacedName: key})
		latest := &v1alpha1.ClusterOrder{}
		Expect(client.Get(ctx, key, latest)).To(Succeed())
		Expect(provider.creates-createsBefore).To(BeNumerically("<=", 1), "one Create attempt per public invocation")
		if provider.lossError != nil && errors.Is(err, provider.lossError) && !lossHandled {
			assertConnectedLostResponse(ctx, before, latest, provider)
			lossHandled = true
			provider.omitLists = 1 // Force real same-scope AlreadyExists and a real re-list.
			r = connectedWorkerReconciler(client, scheme, provider)
			continue
		}
		Expect(err).NotTo(HaveOccurred(), "checkpoint %d: result=%+v", step, res)
		assertConnectedStableSlots(before, latest)
		if provider.creates == createsBefore && res.RequeueAfter != time.Second &&
			connectedWorkerCapacityComplete(latest, count) {
			return
		}
		Expect(res.RequeueAfter).To(Equal(time.Second), "unexpected dependency/backoff at checkpoint %d", step)
		Expect(latest.Status.Workers).NotTo(Equal(before.Status.Workers), "checkpoint must persist allocation progress")
	}
	Fail(fmt.Sprintf("worker allocation exceeded fixture bound 16+8*%d", count))
}

func assertConnectedLostResponse(
	ctx context.Context, before, latest *v1alpha1.ClusterOrder, provider *connectedWorkerResponseClient,
) {
	GinkgoHelper()
	Expect(latest.Status.Workers).To(Equal(before.Status.Workers), "lost acknowledgement must preserve reservations")
	Expect(provider.successful).To(HaveLen(1))
	filter := fmt.Sprintf(`this.metadata.labels["osac.openshift.io/cluster-order"] == %q`, latest.Name)
	stored, err := provider.FulfillmentClient.ListBareMetalInstances(ctx, filter)
	Expect(err).NotTo(HaveOccurred())
	Expect(stored).To(HaveLen(1), "one real committed incarnation before recovery")
	Expect(stored[0].GetId()).To(Equal(provider.lost.GetId()))
	Expect(stored[0].GetMetadata().GetTenant()).To(Equal(latest.Annotations["osac.openshift.io/tenant"]))
	Expect(stored[0].GetMetadata().GetAnnotations()).To(HaveKeyWithValue(
		"osac.openshift.io/owner-reference", "ClusterOrder/"+latest.Name))
}

func assertConnectedStableSlots(before, latest *v1alpha1.ClusterOrder) {
	GinkgoHelper()
	if len(before.Status.Workers) == 0 {
		return // Initial reservation checkpoint.
	}
	Expect(latest.Status.Workers).To(HaveLen(len(before.Status.Workers)))
	for i, worker := range latest.Status.Workers {
		Expect(worker.Name).To(Equal(before.Status.Workers[i].Name))
		Expect(worker.NodeSet).To(Equal(before.Status.Workers[i].NodeSet))
		Expect(worker.BareMetalInstance.Name).To(Equal(before.Status.Workers[i].BareMetalInstance.Name))
	}
}

func connectedWorkerCapacityComplete(order *v1alpha1.ClusterOrder, count int) bool {
	if len(order.Status.Workers) != count || order.Status.CurrentWorkers == nil ||
		*order.Status.CurrentWorkers != int32(count) {
		return false
	}
	for _, worker := range order.Status.Workers {
		if worker.BareMetalInstance.ID == "" {
			return false
		}
	}
	return true
}

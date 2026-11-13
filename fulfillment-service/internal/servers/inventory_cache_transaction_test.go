/*
Copyright (c) 2026 Red Hat Inc.

Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except in compliance with the
License. You may obtain a copy of the License at

  http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on an
"AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the specific
language governing permissions and limitations under the License.
*/

package servers

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"time"

	"github.com/google/uuid"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/prometheus/client_golang/prometheus"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"

	"github.com/osac-project/osac/fulfillment-service/internal/billing"
	"github.com/osac-project/osac/fulfillment-service/internal/database"
	"github.com/osac-project/osac/fulfillment-service/internal/database/dao"
	"github.com/osac-project/osac/fulfillment-service/internal/testing/billingfake"
	privatev1 "github.com/osac-project/osac/proto/gen/osac/private/v1"
	publicv1 "github.com/osac-project/osac/proto/gen/osac/public/v1"
)

// blockingTxManager wraps the suite's transaction manager and deterministically parks the first Begin
// until the test releases it, so an inventory fill can be observed parked inside its own sweep
// transaction. Every Begin is counted, so a spec can prove how many sweep transactions were started.
type blockingTxManager struct {
	database.TxManager
	once    sync.Once
	started chan struct{}
	release chan struct{}
	begins  atomic.Int32
}

// newBlockingTxManager wraps the given delegate manager.
func newBlockingTxManager(delegate database.TxManager) *blockingTxManager {
	return &blockingTxManager{
		TxManager: delegate,
		started:   make(chan struct{}),
		release:   make(chan struct{}),
	}
}

// Begin parks the first call until releaseFill, then delegates to the wrapped manager.
func (m *blockingTxManager) Begin(ctx context.Context) (database.Tx, error) {
	m.begins.Add(1)
	m.once.Do(func() {
		close(m.started)
		<-m.release
	})
	return m.TxManager.Begin(ctx)
}

// releaseFill closes the release channel exactly once, so a failing assertion cannot leave the parked
// fill blocked forever.
func (m *blockingTxManager) releaseFill() {
	select {
	case <-m.release:
		// Already released.
	default:
		close(m.release)
	}
}

// beginParkedFillRequest starts a real request transaction with the suite manager — forced real with a
// trivial statement, because the manager's transactions are lazy — and wraps it, together with the
// blocking manager, in a cancelable request context carrying both.
//
// The transaction's lifetime belongs to the spec cleanup, not the spec body: a spec that fails before
// ending it would leave it holding a pooled connection, and the suite's pool close would then block on it.
// The returned poison ends the transaction once and reports the same error the explicit end did; the
// cleanup shares that guard, so poisoning early is safe and the cleanup becomes a no-op.
func beginParkedFillRequest(blocking *blockingTxManager) (requestCtx context.Context,
	cancel context.CancelFunc, poison func() error) {
	requestTx, err := tm.Begin(ctx)
	Expect(err).ToNot(HaveOccurred())
	// The cleanup is registered before the next fallible step: the manager does not end a transaction by
	// itself, so a failure in that step would otherwise leave this one holding a pooled connection.
	poisoned := false
	poison = func() error {
		if poisoned {
			return nil
		}
		poisoned = true
		return requestTx.End(ctx)
	}
	DeferCleanup(func() {
		Expect(poison()).To(Succeed())
	})
	_, err = requestTx.Exec(ctx, "SELECT 1")
	Expect(err).ToNot(HaveOccurred())
	requestCtx, cancel = context.WithCancel(database.TxIntoContext(
		database.TxManagerIntoContext(ctx, blocking), requestTx))
	return
}

// waitFillStarted waits until the inventory fill has entered its sweep transaction, keeping the wait
// well inside the fill budget: the fill is parked, not slow.
func waitFillStarted(blocking *blockingTxManager) {
	select {
	case <-blocking.started:
	case <-time.After(10 * time.Second):
		Fail("the inventory fill never started its sweep transaction")
	}
}

// canceledRequest reports whether a List call returned because its request context was canceled. Handlers
// may surface the cancellation directly or, as gRPC handlers should, as the transport's Canceled status.
func canceledRequest(err error) bool {
	return errors.Is(err, context.Canceled) || status.Code(err) == codes.Canceled
}

var _ = Describe("Inventory cache transaction ownership", func() {
	It("keeps the catalog pricing inputs fill on its own transaction when the leading request is canceled", func() {
		// One committed compute item: the fill's sweep transaction must see it, and the second List must
		// surface its aggregated row.
		itemID := uuid.NewString()
		seedCommittedCatalogFixtures(func(seedCtx context.Context) {
			computeItemsDao, err := dao.NewGenericDAO[*privatev1.ComputeInstanceCatalogItem]().
				SetLogger(logger).SetTenancyLogic(tenancy).Build()
			Expect(err).ToNot(HaveOccurred())
			_, err = computeItemsDao.Create().SetObject(privatev1.ComputeInstanceCatalogItem_builder{
				Id:        itemID,
				Metadata:  privatev1.Metadata_builder{Name: "vmaas-transaction", Tenant: testTenant}.Build(),
				Title:     "VMaaS transaction item",
				Published: true,
				Fields: privatev1.ComputeInstanceCatalogItemFields_builder{
					InstanceType: privatev1.InstanceTypeReferenceFieldPolicy_builder{
						Locked: privatev1.InstanceTypeReference_builder{Id: "it1", Name: "m1.small"}.Build(),
					}.Build(),
				}.Build(),
			}.Build()).Do(seedCtx)
			Expect(err).ToNot(HaveOccurred())
		})

		private, err := NewPrivateCatalogPricingInputsServer().SetLogger(logger).SetTenancyLogic(tenancy).Build()
		Expect(err).ToNot(HaveOccurred())
		server, err := NewCatalogPricingInputsServer().SetLogger(logger).SetDelegate(private).Build()
		Expect(err).ToNot(HaveOccurred())

		blocking := newBlockingTxManager(tm)
		defer blocking.releaseFill()
		requestCtx, cancel, poison := beginParkedFillRequest(blocking)
		defer cancel()

		type listResult struct {
			response *publicv1.CatalogPricingInputsListResponse
			err      error
		}
		results := make(chan listResult, 1)
		go func() {
			response, err := server.List(requestCtx, &publicv1.CatalogPricingInputsListRequest{})
			results <- listResult{response: response, err: err}
		}()

		waitFillStarted(blocking)

		// Cancel the leading request and finish — poisoning — its transaction while the fill is parked
		// inside its own Begin.
		cancel()
		Expect(poison()).To(Succeed())

		select {
		case first := <-results:
			Expect(first.err).To(HaveOccurred())
			Expect(canceledRequest(first.err)).To(BeTrue())
		case <-time.After(10 * time.Second):
			Fail("the canceled leading List never returned")
		}

		// Let the parked fill finish: its sweep must run in the transaction it began itself, not in the
		// poisoned request transaction of the caller that started it.
		blocking.releaseFill()

		response, err := server.List(database.TxManagerIntoContext(ctx, blocking),
			&publicv1.CatalogPricingInputsListRequest{})
		Expect(err).ToNot(HaveOccurred())
		Expect(response.GetItems()).To(HaveLen(1))
		row := response.GetItems()[0]
		Expect(row.GetService()).To(Equal(serviceComputeInstance))
		Expect(row.GetDimension()).To(Equal(dimensionInstanceType))
		Expect(row.GetCatalogItems()).To(HaveLen(1))
		Expect(row.GetCatalogItems()[0].GetId()).To(Equal(itemID))
		Expect(row.GetCatalogItems()[0].GetName()).To(Equal("vmaas-transaction"))

		// The second List joined the single in-flight fill instead of starting a second sweep, and no
		// closed-transaction error surfaced: the fill never used the request's transaction.
		Expect(blocking.begins.Load()).To(Equal(int32(1)))
	})

	It("keeps the provider rate card items fill on its own transaction when the leading request is canceled", func() {
		// Mirror the cross-reference scenario: one committed cluster item whose locked baremetal instance
		// type matches the fake provider's CaaS rate row, so the second List has a real match.
		seedCommittedCatalogFixtures(func(seedCtx context.Context) {
			clusterItemsDao, err := dao.NewGenericDAO[*privatev1.ClusterCatalogItem]().
				SetLogger(logger).SetTenancyLogic(tenancy).Build()
			Expect(err).ToNot(HaveOccurred())
			_, err = clusterItemsDao.Create().SetObject(privatev1.ClusterCatalogItem_builder{
				Id:       uuid.NewString(),
				Metadata: privatev1.Metadata_builder{Name: "caas-transaction", Tenant: testTenant}.Build(),
				Fields: privatev1.ClusterCatalogItemFields_builder{
					NodeSets: privatev1.ClusterNodeSetMapPolicy_builder{
						Locked: privatev1.ClusterNodeSetMap_builder{
							Items: map[string]*privatev1.ClusterCatalogNodeSet{
								"workers": privatev1.ClusterCatalogNodeSet_builder{
									Size: 3,
									BaremetalInstanceType: privatev1.BareMetalInstanceTypeReference_builder{
										Id: "bt1", Name: "large",
									}.Build(),
								}.Build(),
							},
						}.Build(),
					}.Build(),
				}.Build(),
			}.Build()).Do(seedCtx)
			Expect(err).ToNot(HaveOccurred())
		})

		scenario := billingfake.Scenario{
			Services: []billingfake.ServiceSpec{{
				ID:         "svc-cluster",
				Name:       "cluster",
				Attributes: []billing.PriceableAttribute{{ID: "dim-1", Name: "baremetal_instance_type"}},
				Rates: map[string]billingfake.RateCardSpec{"USD": {Rows: []billing.RateRow{
					{DimensionID: "dim-1", PriceMethod: "QUANTITY", UnitRate: "4",
						AttributeValues: []billing.AttributeValue{{Name: "baremetal_instance_type", Value: "large"}}},
				}}},
			}},
		}
		private, err := NewPrivateProviderRateCardItemsServer().
			SetLogger(logger).SetProvider(billingfake.New(scenario).Provider()).SetTenancyLogic(tenancy).
			SetMetricsRegisterer(prometheus.NewRegistry()).Build()
		Expect(err).ToNot(HaveOccurred())
		server, err := NewProviderRateCardItemsServer().SetLogger(logger).SetDelegate(private).Build()
		Expect(err).ToNot(HaveOccurred())

		blocking := newBlockingTxManager(tm)
		defer blocking.releaseFill()
		requestCtx, cancel, poison := beginParkedFillRequest(blocking)
		defer cancel()

		type listResult struct {
			response *publicv1.ProviderRateCardItemsListResponse
			err      error
		}
		results := make(chan listResult, 1)
		go func() {
			response, err := server.List(requestCtx, publicv1.ProviderRateCardItemsListRequest_builder{
				Service: proto.String("cluster"), CurrencyCode: proto.String("USD"),
			}.Build())
			results <- listResult{response: response, err: err}
		}()

		waitFillStarted(blocking)

		// Cancel the leading request and finish — poisoning — its transaction while the fill is parked
		// inside its own Begin.
		cancel()
		Expect(poison()).To(Succeed())

		select {
		case first := <-results:
			Expect(first.err).To(HaveOccurred())
			Expect(canceledRequest(first.err)).To(BeTrue())
		case <-time.After(10 * time.Second):
			Fail("the canceled leading List never returned")
		}

		// Let the parked fill finish: its sweep must run in the transaction it began itself, not in the
		// poisoned request transaction of the caller that started it.
		blocking.releaseFill()

		response, err := server.List(database.TxManagerIntoContext(ctx, blocking),
			publicv1.ProviderRateCardItemsListRequest_builder{
				Service: proto.String("cluster"), CurrencyCode: proto.String("USD"),
			}.Build())
		Expect(err).ToNot(HaveOccurred())
		Expect(response.GetItems()).To(HaveLen(1))
		Expect(response.GetItems()[0].GetCatalogItemMatches()).To(ConsistOf("caas-transaction"))

		// The second List joined the single in-flight fill instead of starting a second sweep, and no
		// closed-transaction error surfaced: the fill never used the request's transaction.
		Expect(blocking.begins.Load()).To(Equal(int32(1)))
	})
})

package reconciliation_test

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"time"

	cloudevents "github.com/cloudevents/sdk-go/v2"
	"github.com/go-logr/logr"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"google.golang.org/grpc"

	privatev1 "github.com/osac-project/osac-metering/internal/api/osac/private/v1"
	"github.com/osac-project/osac-metering/internal/projection"
	"github.com/osac-project/osac-metering/internal/reconciliation"
)

type mockComputeClient struct {
	items []*privatev1.ComputeInstance
	err   error
}

func (m *mockComputeClient) List(_ context.Context, req *privatev1.ComputeInstancesListRequest, _ ...grpc.CallOption) (*privatev1.ComputeInstancesListResponse, error) {
	if m.err != nil {
		return nil, m.err
	}
	offset := int(req.GetOffset())
	limit := int(req.GetLimit())
	if offset >= len(m.items) {
		return &privatev1.ComputeInstancesListResponse{}, nil
	}
	end := offset + limit
	if end > len(m.items) {
		end = len(m.items)
	}
	return &privatev1.ComputeInstancesListResponse{
		Items: m.items[offset:end],
	}, nil
}

type mockStore struct {
	mu        sync.Mutex
	states    map[string]projection.ResourceState
	upsertErr map[string]error
}

func newMockStore() *mockStore {
	return &mockStore{states: map[string]projection.ResourceState{}}
}

func (s *mockStore) Get(_ context.Context, id string) (*projection.ResourceState, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	st, ok := s.states[id]
	if !ok {
		return nil, nil
	}
	return &st, nil
}
func (s *mockStore) Upsert(_ context.Context, st projection.ResourceState) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.upsertErr != nil {
		if err, ok := s.upsertErr[st.ResourceID]; ok {
			return err
		}
	}
	s.states[st.ResourceID] = st
	return nil
}
func (s *mockStore) Delete(_ context.Context, id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.states, id)
	return nil
}
func (s *mockStore) ListBillable(_ context.Context) ([]projection.ResourceState, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var result []projection.ResourceState
	for _, st := range s.states {
		if st.IsBillable {
			result = append(result, st)
		}
	}
	return result, nil
}
func (s *mockStore) ListAll(_ context.Context) ([]projection.ResourceState, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var result []projection.ResourceState
	for _, st := range s.states {
		result = append(result, st)
	}
	return result, nil
}
func (s *mockStore) UpdateLastHeartbeat(_ context.Context, _ []string, _ time.Time) error { return nil }

type mockPublisher struct {
	mu        sync.Mutex
	published []cloudevents.Event
	err       error
}

func (p *mockPublisher) Publish(_ context.Context, event cloudevents.Event) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.err != nil {
		return p.err
	}
	p.published = append(p.published, event)
	return nil
}

func makeCI(id, tenant, state string, version int32) *privatev1.ComputeInstance {
	ciState := privatev1.ComputeInstanceState_COMPUTE_INSTANCE_STATE_UNSPECIFIED
	instanceType := "m5.large"
	switch state {
	case "RUNNING":
		ciState = privatev1.ComputeInstanceState_COMPUTE_INSTANCE_STATE_RUNNING
	case "STOPPED":
		ciState = privatev1.ComputeInstanceState_COMPUTE_INSTANCE_STATE_STOPPED
	}
	return &privatev1.ComputeInstance{
		Id: id,
		Metadata: &privatev1.Metadata{
			Tenant:  tenant,
			Version: version,
		},
		Spec: &privatev1.ComputeInstanceSpec{
			InstanceType: &privatev1.InstanceTypeReference{Name: instanceType},
			Image:        &privatev1.ComputeInstanceImage{SourceRef: "rhel-9"},
			BootDisk:     &privatev1.ComputeInstanceDisk{SizeGib: 50},
		},
		Status: &privatev1.ComputeInstanceStatus{State: ciState},
	}
}

var _ = Describe("Reconciler", func() {
	var (
		ctx    context.Context
		cancel context.CancelFunc
	)

	BeforeEach(func() {
		ctx, cancel = context.WithTimeout(context.Background(), 5*time.Second)
	})
	AfterEach(func() { cancel() })

	Describe("Reconcile", func() {
		It("detects missed_creation when resource in fulfillment but not in projection", func() {
			client := &mockComputeClient{
				items: []*privatev1.ComputeInstance{
					makeCI("vm-new", "tenant-1", "RUNNING", 1),
				},
			}
			store := newMockStore()
			pub := &mockPublisher{}
			recon := reconciliation.NewReconciler(client, store, pub, logr.Discard(), 60*time.Second)

			Expect(recon.Reconcile(ctx)).To(Succeed())

			pub.mu.Lock()
			defer pub.mu.Unlock()
			var correctionFound bool
			for _, e := range pub.published {
				if e.Type() == "osac.resource.correction.v1" {
					var data map[string]any
					Expect(json.Unmarshal(e.Data(), &data)).To(Succeed())
					Expect(data["reason"]).To(Equal("missed_creation"))
					Expect(data["resource_id"]).To(Equal("vm-new"))
					correctionFound = true
				}
			}
			Expect(correctionFound).To(BeTrue(), "expected missed_creation correction event")

			store.mu.Lock()
			defer store.mu.Unlock()
			Expect(store.states).To(HaveKey("vm-new"))
		})

		It("detects missed_deletion when resource in projection but not in fulfillment", func() {
			client := &mockComputeClient{items: []*privatev1.ComputeInstance{}}
			store := newMockStore()
			store.states["vm-gone"] = projection.ResourceState{
				ResourceID:   "vm-gone",
				ResourceType: "compute_instance",
				TenantID:     "tenant-1",
				CurrentState: "RUNNING",
			}
			pub := &mockPublisher{}
			recon := reconciliation.NewReconciler(client, store, pub, logr.Discard(), 60*time.Second)

			Expect(recon.Reconcile(ctx)).To(Succeed())

			pub.mu.Lock()
			defer pub.mu.Unlock()
			Expect(pub.published).To(HaveLen(1))
			var data map[string]any
			Expect(json.Unmarshal(pub.published[0].Data(), &data)).To(Succeed())
			Expect(data["reason"]).To(Equal("missed_deletion"))

			store.mu.Lock()
			defer store.mu.Unlock()
			Expect(store.states).ToNot(HaveKey("vm-gone"))
		})

		It("detects state_drift when states differ", func() {
			client := &mockComputeClient{
				items: []*privatev1.ComputeInstance{
					makeCI("vm-drift", "tenant-1", "STOPPED", 5),
				},
			}
			store := newMockStore()
			store.states["vm-drift"] = projection.ResourceState{
				ResourceID:         "vm-drift",
				ResourceType:       "compute_instance",
				TenantID:           "tenant-1",
				CurrentState:       "RUNNING",
				FulfillmentVersion: 3,
			}
			pub := &mockPublisher{}
			recon := reconciliation.NewReconciler(client, store, pub, logr.Discard(), 60*time.Second)

			Expect(recon.Reconcile(ctx)).To(Succeed())

			pub.mu.Lock()
			defer pub.mu.Unlock()
			Expect(pub.published).To(HaveLen(1))
			var data map[string]any
			Expect(json.Unmarshal(pub.published[0].Data(), &data)).To(Succeed())
			Expect(data["reason"]).To(Equal("state_drift"))
			Expect(data["previous_state_in_projection"]).To(Equal("RUNNING"))
			Expect(data["actual_state_from_source"]).To(Equal("STOPPED"))
		})

		It("sets BillableSince when state drifts from STOPPED to RUNNING", func() {
			client := &mockComputeClient{
				items: []*privatev1.ComputeInstance{
					makeCI("res-drift", "tenant-1", "RUNNING", 2),
				},
			}
			store := newMockStore()
			store.states["res-drift"] = projection.ResourceState{
				ResourceID:         "res-drift",
				ResourceType:       "compute_instance",
				TenantID:           "tenant-1",
				CurrentState:       "STOPPED",
				IsBillable:         false,
				FulfillmentVersion: 1,
			}
			pub := &mockPublisher{}
			recon := reconciliation.NewReconciler(client, store, pub, logr.Discard(), 60*time.Second)

			Expect(recon.Reconcile(ctx)).To(Succeed())

			store.mu.Lock()
			defer store.mu.Unlock()
			updated := store.states["res-drift"]
			Expect(updated.IsBillable).To(BeTrue())
			Expect(updated.BillableSince).NotTo(BeNil())
			Expect(updated.CurrentState).To(Equal("RUNNING"))
		})

		It("emits synthetic heartbeat for stale billable resources", func() {
			staleTime := time.Now().Add(-5 * time.Minute)
			client := &mockComputeClient{
				items: []*privatev1.ComputeInstance{
					makeCI("res-stale", "tenant-1", "RUNNING", 1),
				},
			}
			store := newMockStore()
			store.states["res-stale"] = projection.ResourceState{
				ResourceID:         "res-stale",
				ResourceType:       "compute_instance",
				TenantID:           "tenant-1",
				CurrentState:       "RUNNING",
				IsBillable:         true,
				FulfillmentVersion: 1,
				LastHeartbeatAt:    &staleTime,
			}
			pub := &mockPublisher{}
			recon := reconciliation.NewReconciler(client, store, pub, logr.Discard(), 60*time.Second)

			Expect(recon.Reconcile(ctx)).To(Succeed())

			pub.mu.Lock()
			defer pub.mu.Unlock()
			found := false
			for _, e := range pub.published {
				if e.Type() == "osac.resource.heartbeat.v1" {
					found = true
					break
				}
			}
			Expect(found).To(BeTrue(), "expected synthetic heartbeat event")
		})

		It("extracts billing dimensions for missed_creation", func() {
			client := &mockComputeClient{
				items: []*privatev1.ComputeInstance{
					makeCI("vm-dims", "tenant-1", "RUNNING", 1),
				},
			}
			store := newMockStore()
			pub := &mockPublisher{}
			recon := reconciliation.NewReconciler(client, store, pub, logr.Discard(), 60*time.Second)

			Expect(recon.Reconcile(ctx)).To(Succeed())

			store.mu.Lock()
			defer store.mu.Unlock()
			Expect(store.states).To(HaveKey("vm-dims"))
			dims := store.states["vm-dims"].BillingDimensions
			Expect(dims["instance_type"]).To(Equal("m5.large"))
			Expect(dims["image_ref"]).To(Equal("rhel-9"))
			Expect(dims["boot_disk_size_gib"]).To(BeNumerically("==", 50))
		})

		It("emits no corrections when states match", func() {
			client := &mockComputeClient{
				items: []*privatev1.ComputeInstance{
					makeCI("vm-ok", "tenant-1", "RUNNING", 1),
				},
			}
			store := newMockStore()
			store.states["vm-ok"] = projection.ResourceState{
				ResourceID:   "vm-ok",
				ResourceType: "compute_instance",
				TenantID:     "tenant-1",
				CurrentState: "RUNNING",
				BillingDimensions: map[string]any{
					"instance_type":      "m5.large",
					"image_ref":          "rhel-9",
					"boot_disk_size_gib": int32(50),
				},
			}
			pub := &mockPublisher{}
			recon := reconciliation.NewReconciler(client, store, pub, logr.Discard(), 60*time.Second)

			Expect(recon.Reconcile(ctx)).To(Succeed())

			pub.mu.Lock()
			defer pub.mu.Unlock()
			Expect(pub.published).To(BeEmpty())
		})

		It("fails when List API returns error", func() {
			client := &mockComputeClient{err: fmt.Errorf("connection refused")}
			store := newMockStore()
			pub := &mockPublisher{}
			recon := reconciliation.NewReconciler(client, store, pub, logr.Discard(), 60*time.Second)

			err := recon.Reconcile(ctx)
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("connection refused"))
		})

		It("fails when correction publish fails (publish-first)", func() {
			client := &mockComputeClient{
				items: []*privatev1.ComputeInstance{
					makeCI("vm-new", "tenant-1", "RUNNING", 1),
				},
			}
			store := newMockStore()
			pub := &mockPublisher{err: fmt.Errorf("kafka down")}
			recon := reconciliation.NewReconciler(client, store, pub, logr.Discard(), 60*time.Second)

			err := recon.Reconcile(ctx)
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("kafka down"))

			store.mu.Lock()
			defer store.mu.Unlock()
			Expect(store.states).ToNot(HaveKey("vm-new"))
		})

		It("detects billing_dimensions_drift when state matches but dimensions differ", func() {
			instanceType := "m5.xlarge"
			client := &mockComputeClient{
				items: []*privatev1.ComputeInstance{
					{
						Id: "vm-dims-drift",
						Metadata: &privatev1.Metadata{
							Tenant:  "tenant-1",
							Version: 2,
						},
						Spec: &privatev1.ComputeInstanceSpec{
							InstanceType: &privatev1.InstanceTypeReference{Name: instanceType},
						},
						Status: &privatev1.ComputeInstanceStatus{
							State: privatev1.ComputeInstanceState_COMPUTE_INSTANCE_STATE_RUNNING,
						},
					},
				},
			}
			store := newMockStore()
			store.states["vm-dims-drift"] = projection.ResourceState{
				ResourceID:         "vm-dims-drift",
				ResourceType:       "compute_instance",
				TenantID:           "tenant-1",
				CurrentState:       "RUNNING",
				IsBillable:         true,
				FulfillmentVersion: 1,
				BillingDimensions:  map[string]any{"instance_type": "m5.large"},
			}
			pub := &mockPublisher{}
			recon := reconciliation.NewReconciler(client, store, pub, logr.Discard(), 60*time.Second)

			Expect(recon.Reconcile(ctx)).To(Succeed())

			pub.mu.Lock()
			defer pub.mu.Unlock()
			var found bool
			for _, e := range pub.published {
				if e.Type() == "osac.resource.correction.v1" {
					var data map[string]any
					Expect(json.Unmarshal(e.Data(), &data)).To(Succeed())
					if data["reason"] == "billing_dimensions_drift" {
						found = true
					}
				}
			}
			Expect(found).To(BeTrue(), "expected billing_dimensions_drift correction")

			store.mu.Lock()
			defer store.mu.Unlock()
			Expect(store.states["vm-dims-drift"].BillingDimensions["instance_type"]).To(Equal("m5.xlarge"))
		})

		It("preserves BillableSince on billing_dimensions_drift for billable resource", func() {
			instanceType := "m5.xlarge"
			originalStart := time.Now().Add(-2 * time.Hour).UTC().Truncate(time.Microsecond)
			client := &mockComputeClient{
				items: []*privatev1.ComputeInstance{
					{
						Id: "vm-keep-billable",
						Metadata: &privatev1.Metadata{
							Tenant:  "tenant-1",
							Version: 2,
						},
						Spec: &privatev1.ComputeInstanceSpec{
							InstanceType: &privatev1.InstanceTypeReference{Name: instanceType},
						},
						Status: &privatev1.ComputeInstanceStatus{
							State: privatev1.ComputeInstanceState_COMPUTE_INSTANCE_STATE_RUNNING,
						},
					},
				},
			}
			store := newMockStore()
			store.states["vm-keep-billable"] = projection.ResourceState{
				ResourceID:         "vm-keep-billable",
				ResourceType:       "compute_instance",
				TenantID:           "tenant-1",
				CurrentState:       "RUNNING",
				IsBillable:         true,
				BillableSince:      &originalStart,
				FulfillmentVersion: 1,
				BillingDimensions:  map[string]any{"instance_type": "m5.large"},
			}
			pub := &mockPublisher{}
			recon := reconciliation.NewReconciler(client, store, pub, logr.Discard(), 60*time.Second)

			Expect(recon.Reconcile(ctx)).To(Succeed())

			store.mu.Lock()
			defer store.mu.Unlock()
			updated := store.states["vm-keep-billable"]
			Expect(updated.BillingDimensions["instance_type"]).To(Equal("m5.xlarge"))
			Expect(updated.BillableSince).ToNot(BeNil())
			Expect(*updated.BillableSince).To(Equal(originalStart))
		})

		It("advances fulfillment version when state and dimensions match but version is newer", func() {
			client := &mockComputeClient{
				items: []*privatev1.ComputeInstance{
					makeCI("vm-version-advance", "tenant-1", "RUNNING", 10),
				},
			}
			store := newMockStore()
			store.states["vm-version-advance"] = projection.ResourceState{
				ResourceID:         "vm-version-advance",
				ResourceType:       "compute_instance",
				TenantID:           "tenant-1",
				CurrentState:       "RUNNING",
				FulfillmentVersion: 5,
				BillingDimensions: map[string]any{
					"instance_type":      "m5.large",
					"image_ref":          "rhel-9",
					"boot_disk_size_gib": int32(50),
				},
			}
			pub := &mockPublisher{}
			recon := reconciliation.NewReconciler(client, store, pub, logr.Discard(), 60*time.Second)

			Expect(recon.Reconcile(ctx)).To(Succeed())

			pub.mu.Lock()
			defer pub.mu.Unlock()
			Expect(pub.published).To(BeEmpty())

			store.mu.Lock()
			defer store.mu.Unlock()
			Expect(store.states["vm-version-advance"].FulfillmentVersion).To(Equal(int32(10)))
		})

		It("emits no correction when state and dimensions both match", func() {
			instanceType := "m5.large"
			client := &mockComputeClient{
				items: []*privatev1.ComputeInstance{
					{
						Id: "vm-match",
						Metadata: &privatev1.Metadata{
							Tenant:  "tenant-1",
							Version: 1,
						},
						Spec: &privatev1.ComputeInstanceSpec{
							InstanceType: &privatev1.InstanceTypeReference{Name: instanceType},
							Image:        &privatev1.ComputeInstanceImage{SourceRef: "rhel-9"},
							BootDisk:     &privatev1.ComputeInstanceDisk{SizeGib: 50},
						},
						Status: &privatev1.ComputeInstanceStatus{
							State: privatev1.ComputeInstanceState_COMPUTE_INSTANCE_STATE_RUNNING,
						},
					},
				},
			}
			store := newMockStore()
			store.states["vm-match"] = projection.ResourceState{
				ResourceID:   "vm-match",
				ResourceType: "compute_instance",
				TenantID:     "tenant-1",
				CurrentState: "RUNNING",
				BillingDimensions: map[string]any{
					"instance_type":      "m5.large",
					"image_ref":          "rhel-9",
					"boot_disk_size_gib": int32(50),
				},
			}
			pub := &mockPublisher{}
			recon := reconciliation.NewReconciler(client, store, pub, logr.Discard(), 60*time.Second)

			Expect(recon.Reconcile(ctx)).To(Succeed())

			pub.mu.Lock()
			defer pub.mu.Unlock()
			Expect(pub.published).To(BeEmpty())
		})

		It("continues reconciliation when upsert returns ErrStaleVersion", func() {
			client := &mockComputeClient{
				items: []*privatev1.ComputeInstance{
					makeCI("vm-stale", "tenant-1", "STOPPED", 5),
					makeCI("vm-new", "tenant-1", "RUNNING", 1),
				},
			}
			store := newMockStore()
			store.states["vm-stale"] = projection.ResourceState{
				ResourceID:         "vm-stale",
				ResourceType:       "compute_instance",
				TenantID:           "tenant-1",
				CurrentState:       "RUNNING",
				FulfillmentVersion: 3,
				BillingDimensions: map[string]any{
					"instance_type":      "m5.large",
					"image_ref":          "rhel-9",
					"boot_disk_size_gib": int32(50),
				},
			}
			store.upsertErr = map[string]error{
				"vm-stale": projection.ErrStaleVersion,
			}
			pub := &mockPublisher{}
			recon := reconciliation.NewReconciler(client, store, pub, logr.Discard(), 60*time.Second)

			Expect(recon.Reconcile(ctx)).To(Succeed())

			pub.mu.Lock()
			defer pub.mu.Unlock()
			Expect(len(pub.published)).To(BeNumerically(">=", 1))
		})

		It("correction event has osac.resource.correction.v1 type", func() {
			client := &mockComputeClient{
				items: []*privatev1.ComputeInstance{
					makeCI("vm-new", "tenant-1", "RUNNING", 1),
				},
			}
			store := newMockStore()
			pub := &mockPublisher{}
			recon := reconciliation.NewReconciler(client, store, pub, logr.Discard(), 60*time.Second)

			Expect(recon.Reconcile(ctx)).To(Succeed())

			pub.mu.Lock()
			defer pub.mu.Unlock()
			var found bool
			for _, e := range pub.published {
				if e.Type() == "osac.resource.correction.v1" {
					Expect(e.Extensions()["osacresourceid"]).To(Equal("vm-new"))
					found = true
					break
				}
			}
			Expect(found).To(BeTrue(), "expected correction event")
		})
	})

	Describe("RunPeriodic", func() {
		It("stops on context cancellation", func() {
			client := &mockComputeClient{}
			store := newMockStore()
			pub := &mockPublisher{}
			recon := reconciliation.NewReconciler(client, store, pub, logr.Discard(), 60*time.Second)

			periodicCtx, periodicCancel := context.WithCancel(ctx)
			done := make(chan struct{})
			go func() {
				recon.RunPeriodic(periodicCtx, time.Hour)
				close(done)
			}()

			time.Sleep(10 * time.Millisecond)
			periodicCancel()
			Eventually(done, time.Second).Should(BeClosed())
		})
	})
})

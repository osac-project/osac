package reconciliation

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
	"google.golang.org/protobuf/proto"

	"github.com/osac-project/osac-metering/internal/events"
	"github.com/osac-project/osac-metering/internal/heartbeat"
	"github.com/osac-project/osac-metering/internal/projection"
	privatev1 "github.com/osac-project/osac/proto/gen/osac/private/v1"
	"google.golang.org/protobuf/types/known/timestamppb"
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

type mockClusterClient struct {
	items []*privatev1.Cluster
	err   error
}

type mockBareMetalInstancesClient struct {
	items    []*privatev1.BareMetalInstance
	err      error
	requests []*privatev1.BareMetalInstancesListRequest
}

type fakeBMaaSReplaySource struct {
	records []BMaaSReplayRecord
	err     error
	calls   []struct {
		resourceID string
		from       int32
		to         int32
	}
}

func (r *fakeBMaaSReplaySource) Replay(_ context.Context, resourceID string, fromVersion, toVersion int32) ([]BMaaSReplayRecord, error) {
	r.calls = append(r.calls, struct {
		resourceID string
		from       int32
		to         int32
	}{resourceID, fromVersion, toVersion})
	if r.err != nil {
		return nil, r.err
	}
	filtered := make([]BMaaSReplayRecord, 0, len(r.records))
	for _, record := range r.records {
		inRange := record.Version > fromVersion && record.Version <= toVersion
		if record.EventType == privatev1.EventType_EVENT_TYPE_OBJECT_DELETED {
			inRange = record.Version >= fromVersion && record.Version <= toVersion
		}
		if inRange {
			filtered = append(filtered, record)
		}
	}
	return filtered, nil
}

func replayRecord(resourceID, tenantID, projectID, state string, version int32, eventID string, transitionTime time.Time, billingDims map[string]any) BMaaSReplayRecord {
	return BMaaSReplayRecord{
		ResourceID:     resourceID,
		TenantID:       tenantID,
		ProjectID:      projectID,
		State:          state,
		Version:        version,
		EventType:      privatev1.EventType_EVENT_TYPE_OBJECT_UPDATED,
		EventID:        eventID,
		BillingDims:    billingDims,
		TransitionTime: transitionTime,
	}
}

func deletedReplayRecord(resourceID, tenantID, projectID string, version int32, eventID string, deletionTime time.Time, billingDims map[string]any) BMaaSReplayRecord {
	return BMaaSReplayRecord{
		ResourceID:     resourceID,
		TenantID:       tenantID,
		ProjectID:      projectID,
		Version:        version,
		EventType:      privatev1.EventType_EVENT_TYPE_OBJECT_DELETED,
		EventID:        eventID,
		BillingDims:    billingDims,
		TransitionTime: deletionTime,
	}
}

func (m *mockBareMetalInstancesClient) List(_ context.Context, req *privatev1.BareMetalInstancesListRequest, _ ...grpc.CallOption) (*privatev1.BareMetalInstancesListResponse, error) {
	m.requests = append(m.requests, proto.Clone(req).(*privatev1.BareMetalInstancesListRequest))
	if m.err != nil {
		return nil, m.err
	}
	offset := int(req.GetOffset())
	limit := int(req.GetLimit())
	if offset >= len(m.items) {
		return &privatev1.BareMetalInstancesListResponse{}, nil
	}
	end := offset + limit
	if end > len(m.items) {
		end = len(m.items)
	}
	return &privatev1.BareMetalInstancesListResponse{Items: m.items[offset:end]}, nil
}

func (m *mockClusterClient) List(_ context.Context, req *privatev1.ClustersListRequest, _ ...grpc.CallOption) (*privatev1.ClustersListResponse, error) {
	if m.err != nil {
		return nil, m.err
	}
	offset := int(req.GetOffset())
	limit := int(req.GetLimit())
	if offset >= len(m.items) {
		return &privatev1.ClustersListResponse{}, nil
	}
	end := offset + limit
	if end > len(m.items) {
		end = len(m.items)
	}
	return &privatev1.ClustersListResponse{
		Items: m.items[offset:end],
	}, nil
}

type mockStore struct {
	mu                     sync.Mutex
	states                 map[string]projection.ResourceState
	upsertErr              map[string]error
	lastHeartbeatUpdateIDs [][]string
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
func (s *mockStore) UpdateLastHeartbeat(_ context.Context, resourceIDs []string, _ time.Time) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.lastHeartbeatUpdateIDs = append(s.lastHeartbeatUpdateIDs, append([]string(nil), resourceIDs...))
	return nil
}

type mockPublisher struct {
	mu        sync.Mutex
	published []cloudevents.Event
	err       error
	failAfter int
}

func (p *mockPublisher) Publish(_ context.Context, event cloudevents.Event) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.err != nil {
		return p.err
	}
	if p.failAfter > 0 && len(p.published) >= p.failAfter {
		return fmt.Errorf("publisher failure after %d events", p.failAfter)
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
	case "STARTING":
		ciState = privatev1.ComputeInstanceState_COMPUTE_INSTANCE_STATE_STARTING
	case "STOPPING":
		ciState = privatev1.ComputeInstanceState_COMPUTE_INSTANCE_STATE_STOPPING
	}
	return &privatev1.ComputeInstance{
		Id: id,
		Metadata: &privatev1.Metadata{
			Tenant:  tenant,
			Version: version,
		},
		Spec: &privatev1.ComputeInstanceSpec{
			InstanceType: &privatev1.InstanceTypeReference{Name: instanceType},
			DiskImage:    &privatev1.DiskImageReference{Name: "rhel-9"},
			BootDisk:     &privatev1.ComputeInstanceDisk{SizeGib: proto.Int32(50)},
		},
		Status: &privatev1.ComputeInstanceStatus{State: ciState},
	}
}

func makeBMI(id, tenant string, state privatev1.BareMetalInstanceState, version int32, transitionTime *timestamppb.Timestamp) *privatev1.BareMetalInstance {
	return &privatev1.BareMetalInstance{
		Id: id,
		Metadata: &privatev1.Metadata{
			Tenant:  tenant,
			Project: "project-1",
			Version: version,
		},
		Spec: &privatev1.BareMetalInstanceSpec{
			CatalogItem:  &privatev1.BareMetalInstanceCatalogItemReference{Name: "catalog-1"},
			InstanceType: &privatev1.BareMetalInstanceTypeLocalReference{Id: "bm.large"},
		},
		Status: &privatev1.BareMetalInstanceStatus{
			State:               state,
			StateTransitionTime: transitionTime,
		},
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
			recon := NewReconciler(client, nil, nil, nil, store, pub, logr.Discard(), 60*time.Second)

			Expect(recon.Reconcile(ctx)).To(Succeed())

			pub.mu.Lock()
			defer pub.mu.Unlock()
			var correctionFound bool
			for _, e := range pub.published {
				if e.Type() == events.EventCorrection {
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
				ResourceType: events.ResourceTypeComputeInstance,
				TenantID:     "tenant-1",
				CurrentState: "RUNNING",
			}
			pub := &mockPublisher{}
			recon := NewReconciler(client, nil, nil, nil, store, pub, logr.Discard(), 60*time.Second)

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

		It("does not delete BMaaS projections without a BMI List client", func() {
			store := newMockStore()
			now := time.Now().UTC().Truncate(time.Microsecond)
			store.states["bmi-preserved"] = projection.ResourceState{
				ResourceID:        "bmi-preserved",
				ResourceType:      events.ResourceTypeBareMetalInstance,
				TenantID:          "tenant-1",
				CurrentState:      "RUNNING",
				IsBillable:        true,
				BillableSince:     &now,
				LastHeartbeatAt:   &now,
				BillingDimensions: map[string]any{"bm_instance_type": "bmi-type-gpu-large"},
			}
			pub := &mockPublisher{}
			recon := NewReconciler(nil, nil, nil, nil, store, pub, logr.Discard(), 60*time.Second)

			Expect(recon.Reconcile(ctx)).To(Succeed())

			pub.mu.Lock()
			defer pub.mu.Unlock()
			Expect(pub.published).To(BeEmpty())
			store.mu.Lock()
			defer store.mu.Unlock()
			Expect(store.states).To(HaveKey("bmi-preserved"))
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
				ResourceType:       events.ResourceTypeComputeInstance,
				TenantID:           "tenant-1",
				CurrentState:       "RUNNING",
				FulfillmentVersion: 3,
			}
			pub := &mockPublisher{}
			recon := NewReconciler(client, nil, nil, nil, store, pub, logr.Discard(), 60*time.Second)

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
				ResourceType:       events.ResourceTypeComputeInstance,
				TenantID:           "tenant-1",
				CurrentState:       "STOPPED",
				IsBillable:         false,
				FulfillmentVersion: 1,
			}
			pub := &mockPublisher{}
			recon := NewReconciler(client, nil, nil, nil, store, pub, logr.Discard(), 60*time.Second)

			Expect(recon.Reconcile(ctx)).To(Succeed())

			store.mu.Lock()
			defer store.mu.Unlock()
			updated := store.states["res-drift"]
			Expect(updated.IsBillable).To(BeTrue())
			Expect(updated.BillableSince).NotTo(BeNil())
			Expect(updated.CurrentState).To(Equal("RUNNING"))
		})

		It("sets EverBillable when a never-billable resource drifts into a billable state", func() {
			client := &mockComputeClient{
				items: []*privatev1.ComputeInstance{
					makeCI("res-drift-first", "tenant-1", "RUNNING", 2),
				},
			}
			store := newMockStore()
			store.states["res-drift-first"] = projection.ResourceState{
				ResourceID:         "res-drift-first",
				ResourceType:       events.ResourceTypeComputeInstance,
				TenantID:           "tenant-1",
				CurrentState:       "STOPPED",
				IsBillable:         false,
				EverBillable:       false, // never actually billed before this drift
				FulfillmentVersion: 1,
			}
			pub := &mockPublisher{}
			recon := NewReconciler(client, nil, nil, nil, store, pub, logr.Discard(), 60*time.Second)

			Expect(recon.Reconcile(ctx)).To(Succeed())

			store.mu.Lock()
			defer store.mu.Unlock()
			updated := store.states["res-drift-first"]
			Expect(updated.IsBillable).To(BeTrue())
			Expect(updated.EverBillable).To(BeTrue(),
				"a resource that just drifted into a billable state must be marked EverBillable, "+
					"or its next real resume will wrongly emit started.v1 again")
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
				ResourceType:       events.ResourceTypeComputeInstance,
				TenantID:           "tenant-1",
				CurrentState:       "RUNNING",
				IsBillable:         true,
				FulfillmentVersion: 1,
				LastHeartbeatAt:    &staleTime,
			}
			pub := &mockPublisher{}
			recon := NewReconciler(client, nil, nil, nil, store, pub, logr.Discard(), 60*time.Second)

			Expect(recon.Reconcile(ctx)).To(Succeed())

			pub.mu.Lock()
			defer pub.mu.Unlock()
			found := false
			for _, e := range pub.published {
				if e.Type() == events.EventHeartbeat {
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
			recon := NewReconciler(client, nil, nil, nil, store, pub, logr.Discard(), 60*time.Second)

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
				ResourceType: events.ResourceTypeComputeInstance,
				TenantID:     "tenant-1",
				CurrentState: "RUNNING",
				BillingDimensions: map[string]any{
					"instance_type":      "m5.large",
					"image_ref":          "rhel-9",
					"boot_disk_size_gib": int32(50),
				},
			}
			pub := &mockPublisher{}
			recon := NewReconciler(client, nil, nil, nil, store, pub, logr.Discard(), 60*time.Second)

			Expect(recon.Reconcile(ctx)).To(Succeed())

			pub.mu.Lock()
			defer pub.mu.Unlock()
			Expect(pub.published).To(BeEmpty())
		})

		It("fails when List API returns error", func() {
			client := &mockComputeClient{err: fmt.Errorf("connection refused")}
			store := newMockStore()
			pub := &mockPublisher{}
			recon := NewReconciler(client, nil, nil, nil, store, pub, logr.Discard(), 60*time.Second)

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
			recon := NewReconciler(client, nil, nil, nil, store, pub, logr.Discard(), 60*time.Second)

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
				ResourceType:       events.ResourceTypeComputeInstance,
				TenantID:           "tenant-1",
				CurrentState:       "RUNNING",
				IsBillable:         true,
				FulfillmentVersion: 1,
				BillingDimensions:  map[string]any{"instance_type": "m5.large"},
			}
			pub := &mockPublisher{}
			recon := NewReconciler(client, nil, nil, nil, store, pub, logr.Discard(), 60*time.Second)

			Expect(recon.Reconcile(ctx)).To(Succeed())

			pub.mu.Lock()
			defer pub.mu.Unlock()
			var found bool
			for _, e := range pub.published {
				if e.Type() == events.EventCorrection {
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
				ResourceType:       events.ResourceTypeComputeInstance,
				TenantID:           "tenant-1",
				CurrentState:       "RUNNING",
				IsBillable:         true,
				BillableSince:      &originalStart,
				FulfillmentVersion: 1,
				BillingDimensions:  map[string]any{"instance_type": "m5.large"},
			}
			pub := &mockPublisher{}
			recon := NewReconciler(client, nil, nil, nil, store, pub, logr.Discard(), 60*time.Second)

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
				ResourceType:       events.ResourceTypeComputeInstance,
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
			recon := NewReconciler(client, nil, nil, nil, store, pub, logr.Discard(), 60*time.Second)

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
							DiskImage:    &privatev1.DiskImageReference{Name: "rhel-9"},
							BootDisk:     &privatev1.ComputeInstanceDisk{SizeGib: proto.Int32(50)},
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
				ResourceType: events.ResourceTypeComputeInstance,
				TenantID:     "tenant-1",
				CurrentState: "RUNNING",
				BillingDimensions: map[string]any{
					"instance_type":      "m5.large",
					"image_ref":          "rhel-9",
					"boot_disk_size_gib": int32(50),
				},
			}
			pub := &mockPublisher{}
			recon := NewReconciler(client, nil, nil, nil, store, pub, logr.Discard(), 60*time.Second)

			Expect(recon.Reconcile(ctx)).To(Succeed())

			pub.mu.Lock()
			defer pub.mu.Unlock()
			Expect(pub.published).To(BeEmpty())
		})

		It("skips transient state STARTING when no projection exists", func() {
			client := &mockComputeClient{
				items: []*privatev1.ComputeInstance{
					makeCI("vm-new-transient", "tenant-1", "STARTING", 1),
				},
			}
			store := newMockStore()
			pub := &mockPublisher{}
			recon := NewReconciler(client, nil, nil, nil, store, pub, logr.Discard(), 60*time.Second)

			Expect(recon.Reconcile(ctx)).To(Succeed())

			pub.mu.Lock()
			defer pub.mu.Unlock()
			Expect(pub.published).To(BeEmpty(), "no correction events for transient state with no projection")

			store.mu.Lock()
			defer store.mu.Unlock()
			Expect(store.states).ToNot(HaveKey("vm-new-transient"),
				"must not create projection for transient state")
		})

		It("skips transient state STOPPING when no projection exists", func() {
			client := &mockComputeClient{
				items: []*privatev1.ComputeInstance{
					makeCI("vm-new-stopping", "tenant-1", "STOPPING", 1),
				},
			}
			store := newMockStore()
			pub := &mockPublisher{}
			recon := NewReconciler(client, nil, nil, nil, store, pub, logr.Discard(), 60*time.Second)

			Expect(recon.Reconcile(ctx)).To(Succeed())

			pub.mu.Lock()
			defer pub.mu.Unlock()
			Expect(pub.published).To(BeEmpty(), "no correction events for transient state with no projection")

			store.mu.Lock()
			defer store.mu.Unlock()
			Expect(store.states).ToNot(HaveKey("vm-new-stopping"),
				"must not create projection for transient state")
		})

		It("skips transient state STARTING and preserves projection CurrentState", func() {
			client := &mockComputeClient{
				items: []*privatev1.ComputeInstance{
					makeCI("vm-transient", "tenant-1", "STARTING", 5),
				},
			}
			origTransition := time.Now().Add(-10 * time.Minute).UTC().Truncate(time.Microsecond)
			store := newMockStore()
			store.states["vm-transient"] = projection.ResourceState{
				ResourceID:         "vm-transient",
				ResourceType:       events.ResourceTypeComputeInstance,
				TenantID:           "tenant-1",
				CurrentState:       "STOPPED",
				IsBillable:         false,
				FulfillmentVersion: 3,
				TransitionTime:     origTransition,
			}
			pub := &mockPublisher{}
			recon := NewReconciler(client, nil, nil, nil, store, pub, logr.Discard(), 60*time.Second)

			Expect(recon.Reconcile(ctx)).To(Succeed())

			pub.mu.Lock()
			defer pub.mu.Unlock()
			Expect(pub.published).To(BeEmpty(), "no correction events for transient state")

			store.mu.Lock()
			defer store.mu.Unlock()
			updated := store.states["vm-transient"]
			Expect(updated.CurrentState).To(Equal("STOPPED"), "CurrentState must not change to transient STARTING")
			Expect(updated.PreviousState).To(BeEmpty(), "PreviousState must not be set")
			Expect(updated.IsBillable).To(BeFalse(), "billability must not change")
			Expect(updated.FulfillmentVersion).To(Equal(int32(5)), "FulfillmentVersion must advance")
			Expect(updated.TransitionTime).To(Equal(origTransition), "TransitionTime must not change — no state transition occurred")
		})

		It("skips transient state STOPPING and preserves projection CurrentState", func() {
			client := &mockComputeClient{
				items: []*privatev1.ComputeInstance{
					makeCI("vm-stopping", "tenant-1", "STOPPING", 4),
				},
			}
			billStart := time.Now().Add(-1 * time.Hour).UTC().Truncate(time.Microsecond)
			origTransition := time.Now().Add(-10 * time.Minute).UTC().Truncate(time.Microsecond)
			store := newMockStore()
			store.states["vm-stopping"] = projection.ResourceState{
				ResourceID:         "vm-stopping",
				ResourceType:       events.ResourceTypeComputeInstance,
				TenantID:           "tenant-1",
				CurrentState:       "RUNNING",
				IsBillable:         true,
				BillableSince:      &billStart,
				FulfillmentVersion: 2,
				TransitionTime:     origTransition,
			}
			pub := &mockPublisher{}
			recon := NewReconciler(client, nil, nil, nil, store, pub, logr.Discard(), 60*time.Second)

			Expect(recon.Reconcile(ctx)).To(Succeed())

			pub.mu.Lock()
			defer pub.mu.Unlock()
			for _, e := range pub.published {
				Expect(e.Type()).ToNot(Equal(events.EventCorrection),
					"no StateDrift correction for transient STOPPING")
			}

			store.mu.Lock()
			defer store.mu.Unlock()
			updated := store.states["vm-stopping"]
			Expect(updated.CurrentState).To(Equal("RUNNING"), "CurrentState must stay RUNNING")
			Expect(updated.IsBillable).To(BeTrue(), "billability must not change")
			Expect(updated.BillableSince).ToNot(BeNil(), "BillableSince must be preserved")
			Expect(*updated.BillableSince).To(Equal(billStart), "BillableSince timestamp must not change")
			Expect(updated.FulfillmentVersion).To(Equal(int32(4)), "FulfillmentVersion must advance")
			Expect(updated.TransitionTime).To(Equal(origTransition), "TransitionTime must not change — no state transition occurred")
		})

		It("skips write for transient state when fulfillment version equals projection version", func() {
			origTransition := time.Now().Add(-10 * time.Minute).UTC().Truncate(time.Microsecond)
			client := &mockComputeClient{
				items: []*privatev1.ComputeInstance{
					makeCI("vm-same-ver", "tenant-1", "STARTING", 3),
				},
			}
			store := newMockStore()
			store.states["vm-same-ver"] = projection.ResourceState{
				ResourceID:         "vm-same-ver",
				ResourceType:       events.ResourceTypeComputeInstance,
				TenantID:           "tenant-1",
				CurrentState:       "STOPPED",
				IsBillable:         false,
				FulfillmentVersion: 3,
				TransitionTime:     origTransition,
			}
			pub := &mockPublisher{}
			recon := NewReconciler(client, nil, nil, nil, store, pub, logr.Discard(), 60*time.Second)

			Expect(recon.Reconcile(ctx)).To(Succeed())

			pub.mu.Lock()
			defer pub.mu.Unlock()
			Expect(pub.published).To(BeEmpty(), "no correction events for same-version transient state")

			store.mu.Lock()
			defer store.mu.Unlock()
			updated := store.states["vm-same-ver"]
			Expect(updated.CurrentState).To(Equal("STOPPED"), "CurrentState must not change to transient STARTING")
			Expect(updated.FulfillmentVersion).To(Equal(int32(3)), "FulfillmentVersion must not change")
			Expect(updated.TransitionTime).To(Equal(origTransition), "TransitionTime must not change — no write should occur")
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
				ResourceType:       events.ResourceTypeComputeInstance,
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
			recon := NewReconciler(client, nil, nil, nil, store, pub, logr.Discard(), 60*time.Second)

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
			recon := NewReconciler(client, nil, nil, nil, store, pub, logr.Discard(), 60*time.Second)

			Expect(recon.Reconcile(ctx)).To(Succeed())

			pub.mu.Lock()
			defer pub.mu.Unlock()
			var found bool
			for _, e := range pub.published {
				if e.Type() == events.EventCorrection {
					Expect(e.Extensions()["osacresourceid"]).To(Equal("vm-new"))
					found = true
					break
				}
			}
			Expect(found).To(BeTrue(), "expected correction event")
		})
	})

	Describe("BMaaS fulfillment loading", func() {
		It("replays every lifecycle hop when recovering a missed creation", func() {
			t1 := time.Date(2026, 9, 14, 10, 0, 0, 0, time.UTC)
			t2 := t1.Add(time.Minute)
			t3 := t2.Add(time.Minute)
			t4 := t3.Add(time.Minute)
			dims := map[string]any{"bm_instance_type": "bm.large"}
			resolver := &fakeBMaaSReplaySource{records: []BMaaSReplayRecord{
				replayRecord("bmi-missed-creation", "tenant-1", "project-1", "STOPPING", 1, "event-1", t1, dims),
				replayRecord("bmi-missed-creation", "tenant-1", "project-1", "STOPPED", 2, "event-2", t2, dims),
				replayRecord("bmi-missed-creation", "tenant-1", "project-1", "STARTING", 3, "event-3", t3, dims),
				replayRecord("bmi-missed-creation", "tenant-1", "project-1", "RUNNING", 4, "event-4", t4, dims),
			}}
			store := newMockStore()
			pub := &mockPublisher{}
			recon := NewReconciler(nil, nil, nil, resolver, store, pub, logr.Discard(), time.Minute)
			fulfillment := map[string]fulfillmentResource{"bmi-missed-creation": {
				resourceType:      events.ResourceTypeBareMetalInstance,
				state:             "RUNNING",
				version:           4,
				tenantID:          "tenant-1",
				projectID:         "project-1",
				billingDimensions: dims,
				transitionTime:    &t4,
			}}

			corrections, err := recon.reconcileFulfillmentResources(ctx, fulfillment, map[string]projection.ResourceState{}, t4)
			Expect(err).NotTo(HaveOccurred())
			Expect(corrections).To(Equal(2))
			Expect(pub.published).To(HaveLen(2))
			Expect(pub.published[0].ID()).To(HaveSuffix("/allocation"))
			Expect(pub.published[0].Time()).To(Equal(t1))
			Expect(pub.published[1].ID()).To(HaveSuffix("/consumption"))
			Expect(pub.published[1].Time()).To(Equal(t4))
			Expect(store.states["bmi-missed-creation"].CurrentState).To(Equal("RUNNING"))
			Expect(store.states["bmi-missed-creation"].FulfillmentVersion).To(Equal(int32(4)))
		})

		It("holds a missed creation when replay starts after version one", func() {
			t1 := time.Date(2026, 9, 14, 10, 0, 0, 0, time.UTC)
			t2 := t1.Add(time.Minute)
			dims := map[string]any{"bm_instance_type": "bm.large"}
			resolver := &fakeBMaaSReplaySource{records: []BMaaSReplayRecord{
				replayRecord("bmi-missed-creation-gap", "tenant-1", "project-1", "STOPPED", 2, "event-2", t1, dims),
				replayRecord("bmi-missed-creation-gap", "tenant-1", "project-1", "RUNNING", 3, "event-3", t2, dims),
			}}
			store := newMockStore()
			pub := &mockPublisher{}
			recon := NewReconciler(nil, nil, nil, resolver, store, pub, logr.Discard(), time.Minute)
			fulfillment := map[string]fulfillmentResource{"bmi-missed-creation-gap": {
				resourceType:      events.ResourceTypeBareMetalInstance,
				state:             "RUNNING",
				version:           3,
				tenantID:          "tenant-1",
				billingDimensions: dims,
				transitionTime:    &t2,
			}}

			corrections, err := recon.reconcileFulfillmentResources(ctx, fulfillment, map[string]projection.ResourceState{}, t2)
			Expect(err).NotTo(HaveOccurred())
			Expect(corrections).To(Equal(0))
			Expect(pub.published).To(BeEmpty())
			Expect(store.states).ToNot(HaveKey("bmi-missed-creation-gap"))
		})

		It("holds a missed creation when replay dimensions do not match the snapshot", func() {
			t1 := time.Date(2026, 9, 14, 10, 0, 0, 0, time.UTC)
			t2 := t1.Add(time.Minute)
			replayDims := map[string]any{"bm_instance_type": "bm.large"}
			snapshotDims := map[string]any{"bm_instance_type": "bm.xlarge"}
			resolver := &fakeBMaaSReplaySource{records: []BMaaSReplayRecord{
				replayRecord("bmi-missed-creation-drift", "tenant-1", "project-1", "STOPPED", 1, "event-1", t1, replayDims),
				replayRecord("bmi-missed-creation-drift", "tenant-1", "project-1", "RUNNING", 2, "event-2", t2, replayDims),
			}}
			store := newMockStore()
			pub := &mockPublisher{}
			recon := NewReconciler(nil, nil, nil, resolver, store, pub, logr.Discard(), time.Minute)
			fulfillment := map[string]fulfillmentResource{"bmi-missed-creation-drift": {
				resourceType:      events.ResourceTypeBareMetalInstance,
				state:             "RUNNING",
				version:           2,
				tenantID:          "tenant-1",
				billingDimensions: snapshotDims,
				transitionTime:    &t2,
			}}

			corrections, err := recon.reconcileFulfillmentResources(ctx, fulfillment, map[string]projection.ResourceState{}, t2)
			Expect(err).NotTo(HaveOccurred())
			Expect(corrections).To(Equal(0))
			Expect(pub.published).To(BeEmpty())
			Expect(store.states).ToNot(HaveKey("bmi-missed-creation-drift"))
		})

		It("initializes a missing RUNNING projection from resolver-established meter boundaries", func() {
			allocationSince := time.Date(2026, 9, 14, 10, 0, 0, 0, time.UTC)
			consumptionSince := time.Date(2026, 9, 14, 11, 0, 0, 0, time.UTC)
			transitionTime := time.Date(2026, 9, 14, 12, 0, 0, 0, time.UTC)
			dims := map[string]any{"bm_instance_type": "bm.large"}
			resolver := &fakeBMaaSReplaySource{records: []BMaaSReplayRecord{
				replayRecord("bmi-new", "tenant-1", "project-1", "STOPPED", 1, "event-1", allocationSince, dims),
				replayRecord("bmi-new", "tenant-1", "project-1", "STOPPED", 2, "event-2", allocationSince, dims),
				replayRecord("bmi-new", "tenant-1", "project-1", "STOPPED", 3, "event-3", allocationSince, dims),
				replayRecord("bmi-new", "tenant-1", "project-1", "STOPPED", 4, "event-4", allocationSince, dims),
				replayRecord("bmi-new", "tenant-1", "project-1", "STOPPED", 5, "event-5", allocationSince, dims),
				replayRecord("bmi-new", "tenant-1", "project-1", "RUNNING", 6, "event-6", consumptionSince, dims),
				replayRecord("bmi-new", "tenant-1", "project-1", "RUNNING", 7, "event-7", transitionTime, dims),
			}}
			store := newMockStore()
			pub := &mockPublisher{}
			recon := NewReconciler(nil, nil, nil, resolver, store, pub, logr.Discard(), time.Minute)
			fulfillment := map[string]fulfillmentResource{"bmi-new": {
				resourceType:      events.ResourceTypeBareMetalInstance,
				state:             "RUNNING",
				version:           7,
				tenantID:          "tenant-1",
				projectID:         "project-1",
				billingDimensions: dims,
				transitionTime:    &transitionTime,
			}}

			corrections, err := recon.reconcileFulfillmentResources(ctx, fulfillment, map[string]projection.ResourceState{}, transitionTime)
			Expect(err).NotTo(HaveOccurred())
			Expect(corrections).To(Equal(2))
			Expect(resolver.calls).To(Equal([]struct {
				resourceID string
				from       int32
				to         int32
			}{{"bmi-new", 0, 7}}))

			Expect(pub.published).To(HaveLen(2))
			Expect(pub.published[0].ID()).To(HaveSuffix("/allocation"))
			Expect(pub.published[1].ID()).To(HaveSuffix("/consumption"))
			stored := store.states["bmi-new"]
			Expect(stored.BillableSince).NotTo(BeNil())
			Expect(*stored.BillableSince).To(Equal(allocationSince))
			Expect(stored.ComponentBillableSince).To(HaveKeyWithValue(events.BMaaSMeterConsumption, consumptionSince))
			Expect(stored.TransitionTime).To(Equal(transitionTime))
		})

		It("holds a missing RUNNING projection when resolver history lacks a consumption boundary", func() {
			transitionTime := time.Date(2026, 9, 14, 12, 0, 0, 0, time.UTC)
			resolver := &fakeBMaaSReplaySource{}
			store := newMockStore()
			pub := &mockPublisher{}
			recon := NewReconciler(nil, nil, nil, resolver, store, pub, logr.Discard(), time.Minute)
			fulfillment := map[string]fulfillmentResource{"bmi-incomplete": {
				resourceType:      events.ResourceTypeBareMetalInstance,
				state:             "RUNNING",
				version:           8,
				tenantID:          "tenant-1",
				billingDimensions: map[string]any{"bm_instance_type": "bm.large"},
				transitionTime:    &transitionTime,
			}}

			corrections, err := recon.reconcileFulfillmentResources(ctx, fulfillment, map[string]projection.ResourceState{}, transitionTime)
			Expect(err).NotTo(HaveOccurred())
			Expect(corrections).To(Equal(0))
			Expect(pub.published).To(BeEmpty())
			Expect(store.states).ToNot(HaveKey("bmi-incomplete"))
		})

		It("holds a same-state version gap when replay is unavailable", func() {
			allocationSince := time.Date(2026, 9, 14, 10, 0, 0, 0, time.UTC)
			consumptionSince := time.Date(2026, 9, 14, 11, 0, 0, 0, time.UTC)
			transitionTime := time.Date(2026, 9, 14, 12, 0, 0, 0, time.UTC)
			store := newMockStore()
			store.states["bmi-version-gap"] = projection.ResourceState{
				ResourceID:         "bmi-version-gap",
				ResourceType:       events.ResourceTypeBareMetalInstance,
				CurrentState:       "RUNNING",
				IsBillable:         true,
				BillableSince:      &allocationSince,
				FulfillmentVersion: 1,
				ComponentBillableSince: map[string]time.Time{
					events.BMaaSMeterConsumption: consumptionSince,
				},
				BillingDimensions: map[string]any{"bm_instance_type": "bm.large"},
			}
			pub := &mockPublisher{}
			recon := NewReconciler(nil, nil, &mockBareMetalInstancesClient{}, nil, store, pub, logr.Discard(), time.Minute)
			fulfillment := map[string]fulfillmentResource{"bmi-version-gap": {
				resourceType:      events.ResourceTypeBareMetalInstance,
				state:             "RUNNING",
				version:           4,
				tenantID:          "tenant-1",
				billingDimensions: map[string]any{"bm_instance_type": "bm.large"},
				transitionTime:    &transitionTime,
			}}

			corrections, err := recon.reconcileFulfillmentResources(ctx, fulfillment, store.states, transitionTime)
			Expect(err).NotTo(HaveOccurred())
			Expect(corrections).To(Equal(0))
			Expect(pub.published).To(BeEmpty())
			Expect(store.states["bmi-version-gap"].FulfillmentVersion).To(Equal(int32(1)))
			Expect(*store.states["bmi-version-gap"].BillableSince).To(Equal(allocationSince))
			Expect(store.states["bmi-version-gap"].ComponentBillableSince).To(HaveKeyWithValue(events.BMaaSMeterConsumption, consumptionSince))
		})

		It("returns a publisher error from a later replay hop", func() {
			allocationSince := time.Date(2026, 9, 14, 10, 0, 0, 0, time.UTC)
			consumptionSince := time.Date(2026, 9, 14, 11, 0, 0, 0, time.UTC)
			t1 := time.Date(2026, 9, 14, 12, 0, 0, 0, time.UTC)
			t2 := t1.Add(time.Minute)
			dims := map[string]any{"bm_instance_type": "bm.large"}
			store := newMockStore()
			store.states["bmi-publish-error"] = projection.ResourceState{
				ResourceID:         "bmi-publish-error",
				ResourceType:       events.ResourceTypeBareMetalInstance,
				CurrentState:       "RUNNING",
				IsBillable:         true,
				BillableSince:      &allocationSince,
				FulfillmentVersion: 1,
				ComponentBillableSince: map[string]time.Time{
					events.BMaaSMeterConsumption: consumptionSince,
				},
				BillingDimensions: dims,
			}
			resolver := &fakeBMaaSReplaySource{records: []BMaaSReplayRecord{
				replayRecord("bmi-publish-error", "tenant-1", "project-1", "STOPPING", 2, "event-2", t1, dims),
				replayRecord("bmi-publish-error", "tenant-1", "project-1", "RUNNING", 3, "event-3", t2, dims),
			}}
			pub := &mockPublisher{failAfter: 1}
			recon := NewReconciler(nil, nil, nil, resolver, store, pub, logr.Discard(), time.Minute)
			fulfillment := map[string]fulfillmentResource{"bmi-publish-error": {
				resourceType:      events.ResourceTypeBareMetalInstance,
				state:             "RUNNING",
				version:           3,
				tenantID:          "tenant-1",
				billingDimensions: dims,
				transitionTime:    &t2,
			}}

			_, err := recon.reconcileFulfillmentResources(ctx, fulfillment, store.states, t2)
			Expect(err).To(MatchError(ContainSubstring("publisher failure after 1 events")))
		})

		It("returns a non-stale upsert error from replay", func() {
			allocationSince := time.Date(2026, 9, 14, 10, 0, 0, 0, time.UTC)
			consumptionSince := time.Date(2026, 9, 14, 11, 0, 0, 0, time.UTC)
			t1 := time.Date(2026, 9, 14, 12, 0, 0, 0, time.UTC)
			t2 := t1.Add(time.Minute)
			dims := map[string]any{"bm_instance_type": "bm.large"}
			store := newMockStore()
			store.states["bmi-upsert-error"] = projection.ResourceState{
				ResourceID:         "bmi-upsert-error",
				ResourceType:       events.ResourceTypeBareMetalInstance,
				CurrentState:       "RUNNING",
				IsBillable:         true,
				BillableSince:      &allocationSince,
				FulfillmentVersion: 1,
				ComponentBillableSince: map[string]time.Time{
					events.BMaaSMeterConsumption: consumptionSince,
				},
				BillingDimensions: dims,
			}
			store.upsertErr = map[string]error{"bmi-upsert-error": fmt.Errorf("database unavailable")}
			resolver := &fakeBMaaSReplaySource{records: []BMaaSReplayRecord{
				replayRecord("bmi-upsert-error", "tenant-1", "project-1", "STOPPING", 2, "event-2", t1, dims),
				replayRecord("bmi-upsert-error", "tenant-1", "project-1", "STOPPED", 3, "event-3", t2, dims),
			}}
			pub := &mockPublisher{}
			recon := NewReconciler(nil, nil, nil, resolver, store, pub, logr.Discard(), time.Minute)
			fulfillment := map[string]fulfillmentResource{"bmi-upsert-error": {
				resourceType:      events.ResourceTypeBareMetalInstance,
				state:             "STOPPED",
				version:           3,
				tenantID:          "tenant-1",
				billingDimensions: dims,
				transitionTime:    &t2,
			}}

			_, err := recon.reconcileFulfillmentResources(ctx, fulfillment, store.states, t2)
			Expect(err).To(MatchError(ContainSubstring("database unavailable")))
		})

		It("holds RUNNING to STOPPED when replay does not provide the STOPPING boundary", func() {
			allocationSince := time.Date(2026, 9, 14, 10, 0, 0, 0, time.UTC)
			consumptionSince := time.Date(2026, 9, 14, 11, 0, 0, 0, time.UTC)
			stoppedAt := time.Date(2026, 9, 14, 12, 0, 0, 0, time.UTC)
			store := newMockStore()
			store.states["bmi-stop-boundary"] = projection.ResourceState{
				ResourceID:         "bmi-stop-boundary",
				ResourceType:       events.ResourceTypeBareMetalInstance,
				CurrentState:       "RUNNING",
				IsBillable:         true,
				BillableSince:      &allocationSince,
				FulfillmentVersion: 1,
				ComponentBillableSince: map[string]time.Time{
					events.BMaaSMeterConsumption: consumptionSince,
				},
				BillingDimensions: map[string]any{"bm_instance_type": "bm.large"},
			}
			resolver := &fakeBMaaSReplaySource{records: []BMaaSReplayRecord{
				replayRecord("bmi-stop-boundary", "tenant-1", "project-1", "STOPPED", 2, "event-2", stoppedAt, map[string]any{"bm_instance_type": "bm.large"}),
			}}
			pub := &mockPublisher{}
			recon := NewReconciler(nil, nil, nil, resolver, store, pub, logr.Discard(), time.Minute)
			fulfillment := map[string]fulfillmentResource{"bmi-stop-boundary": {
				resourceType:      events.ResourceTypeBareMetalInstance,
				state:             "STOPPED",
				version:           2,
				tenantID:          "tenant-1",
				billingDimensions: map[string]any{"bm_instance_type": "bm.large"},
				transitionTime:    &stoppedAt,
			}}

			corrections, err := recon.reconcileFulfillmentResources(ctx, fulfillment, store.states, stoppedAt)
			Expect(err).NotTo(HaveOccurred())
			Expect(corrections).To(Equal(0))
			Expect(pub.published).To(BeEmpty())
			Expect(store.states["bmi-stop-boundary"].CurrentState).To(Equal("RUNNING"))
		})

		It("closes RUNNING to STOPPED at the replayed STOPPING timestamp", func() {
			allocationSince := time.Date(2026, 9, 14, 10, 0, 0, 0, time.UTC)
			consumptionSince := time.Date(2026, 9, 14, 11, 0, 0, 0, time.UTC)
			stoppingAt := time.Date(2026, 9, 14, 12, 0, 0, 0, time.UTC)
			stoppedAt := stoppingAt.Add(time.Minute)
			dims := map[string]any{"bm_instance_type": "bm.large"}
			store := newMockStore()
			store.states["bmi-stop-replay"] = projection.ResourceState{
				ResourceID:         "bmi-stop-replay",
				ResourceType:       events.ResourceTypeBareMetalInstance,
				CurrentState:       "RUNNING",
				IsBillable:         true,
				BillableSince:      &allocationSince,
				FulfillmentVersion: 1,
				ComponentBillableSince: map[string]time.Time{
					events.BMaaSMeterConsumption: consumptionSince,
				},
				BillingDimensions: dims,
			}
			resolver := &fakeBMaaSReplaySource{records: []BMaaSReplayRecord{
				replayRecord("bmi-stop-replay", "tenant-1", "project-1", "STOPPING", 2, "event-stopping", stoppingAt, dims),
				replayRecord("bmi-stop-replay", "tenant-1", "project-1", "STOPPED", 3, "event-stopped", stoppedAt, dims),
			}}
			pub := &mockPublisher{}
			recon := NewReconciler(nil, nil, nil, resolver, store, pub, logr.Discard(), time.Minute)
			fulfillment := map[string]fulfillmentResource{"bmi-stop-replay": {
				resourceType:      events.ResourceTypeBareMetalInstance,
				state:             "STOPPED",
				version:           3,
				tenantID:          "tenant-1",
				billingDimensions: dims,
				transitionTime:    &stoppedAt,
			}}

			corrections, err := recon.reconcileFulfillmentResources(ctx, fulfillment, store.states, stoppedAt)
			Expect(err).NotTo(HaveOccurred())
			Expect(corrections).To(Equal(1))
			Expect(pub.published).To(HaveLen(1))
			var data map[string]any
			Expect(json.Unmarshal(pub.published[0].Data(), &data)).To(Succeed())
			interval := data["affected_interval"].(map[string]any)
			Expect(interval["to"]).To(Equal(stoppingAt.Format(time.RFC3339Nano)))
			stored := store.states["bmi-stop-replay"]
			Expect(stored.CurrentState).To(Equal("STOPPED"))
			Expect(stored.FulfillmentVersion).To(Equal(int32(3)))
			Expect(stored.BillableSince).NotTo(BeNil())
			Expect(stored.ComponentBillableSince).ToNot(HaveKey(events.BMaaSMeterConsumption))
		})

		It("closes RUNNING to STARTING at the snapshot timestamp without replay", func() {
			allocationSince := time.Date(2026, 9, 14, 10, 0, 0, 0, time.UTC)
			consumptionSince := time.Date(2026, 9, 14, 11, 0, 0, 0, time.UTC)
			transitionTime := time.Date(2026, 9, 14, 12, 0, 0, 0, time.UTC)
			dims := map[string]any{"bm_instance_type": "bm.large"}
			store := newMockStore()
			store.states["bmi-starting"] = projection.ResourceState{
				ResourceID:         "bmi-starting",
				ResourceType:       events.ResourceTypeBareMetalInstance,
				CurrentState:       "RUNNING",
				IsBillable:         true,
				BillableSince:      &allocationSince,
				FulfillmentVersion: 1,
				ComponentBillableSince: map[string]time.Time{
					events.BMaaSMeterConsumption: consumptionSince,
				},
				BillingDimensions: dims,
			}
			pub := &mockPublisher{}
			recon := NewReconciler(nil, nil, nil, nil, store, pub, logr.Discard(), time.Minute)
			fulfillment := map[string]fulfillmentResource{"bmi-starting": {
				resourceType:      events.ResourceTypeBareMetalInstance,
				state:             "STARTING",
				version:           2,
				tenantID:          "tenant-1",
				billingDimensions: dims,
				transitionTime:    &transitionTime,
			}}

			corrections, err := recon.reconcileFulfillmentResources(ctx, fulfillment, store.states, transitionTime)
			Expect(err).NotTo(HaveOccurred())
			Expect(corrections).To(Equal(1))
			Expect(pub.published).To(HaveLen(1))
			Expect(pub.published[0].ID()).To(HaveSuffix("/consumption"))
			Expect(pub.published[0].Time()).To(Equal(transitionTime))
		})

		It("replays ordered lifecycle hops and is idempotent on a second reconcile", func() {
			allocationSince := time.Date(2026, 9, 14, 10, 0, 0, 0, time.UTC)
			consumptionSince := time.Date(2026, 9, 14, 11, 0, 0, 0, time.UTC)
			t1 := time.Date(2026, 9, 14, 12, 0, 0, 0, time.UTC)
			t2 := t1.Add(time.Minute)
			t3 := t2.Add(time.Minute)
			t4 := t3.Add(time.Minute)
			dims := map[string]any{"bm_instance_type": "bm.large"}
			store := newMockStore()
			store.states["bmi-replay"] = projection.ResourceState{
				ResourceID:         "bmi-replay",
				ResourceType:       events.ResourceTypeBareMetalInstance,
				CurrentState:       "RUNNING",
				IsBillable:         true,
				EverBillable:       true,
				BillableSince:      &allocationSince,
				FulfillmentVersion: 1,
				ComponentBillableSince: map[string]time.Time{
					events.BMaaSMeterConsumption: consumptionSince,
				},
				ComponentEverStarted: map[string]bool{
					events.BMaaSMeterAllocation:  true,
					events.BMaaSMeterConsumption: true,
				},
				BillingDimensions: dims,
			}
			resolver := &fakeBMaaSReplaySource{records: []BMaaSReplayRecord{
				replayRecord("bmi-replay", "tenant-1", "project-1", "STOPPING", 2, "event-2", t1, dims),
				replayRecord("bmi-replay", "tenant-1", "project-1", "STOPPED", 3, "event-3", t2, dims),
				replayRecord("bmi-replay", "tenant-1", "project-1", "STARTING", 4, "event-4", t3, dims),
				replayRecord("bmi-replay", "tenant-1", "project-1", "RUNNING", 5, "event-5", t4, dims),
			}}
			pub := &mockPublisher{}
			recon := NewReconciler(nil, nil, nil, resolver, store, pub, logr.Discard(), time.Minute)
			fulfillment := map[string]fulfillmentResource{"bmi-replay": {
				resourceType:      events.ResourceTypeBareMetalInstance,
				state:             "RUNNING",
				version:           5,
				tenantID:          "tenant-1",
				projectID:         "project-1",
				billingDimensions: dims,
				transitionTime:    &t4,
			}}

			corrections, err := recon.reconcileFulfillmentResources(ctx, fulfillment, store.states, t4)
			Expect(err).NotTo(HaveOccurred())
			Expect(corrections).To(Equal(2))
			Expect(pub.published).To(HaveLen(2))
			Expect(pub.published[0].Type()).To(Equal(events.EventCorrection))
			Expect(pub.published[1].Type()).To(Equal(events.EventCorrection))
			stored := store.states["bmi-replay"]
			Expect(stored.CurrentState).To(Equal("RUNNING"))
			Expect(stored.FulfillmentVersion).To(Equal(int32(5)))
			Expect(stored.BillableSince).NotTo(BeNil())
			Expect(*stored.BillableSince).To(Equal(allocationSince))
			Expect(stored.ComponentBillableSince).To(HaveKeyWithValue(events.BMaaSMeterConsumption, t4))

			corrections, err = recon.reconcileFulfillmentResources(ctx, fulfillment, store.states, t4)
			Expect(err).NotTo(HaveOccurred())
			Expect(corrections).To(Equal(0))
			Expect(pub.published).To(HaveLen(2))
		})

		DescribeTable("reconciles BMaaS state drift at independent meter boundaries",
			func(targetState string, allocationEvents, consumptionEvents int, allocationRemains bool) {
				allocationSince := time.Date(2026, 9, 14, 10, 0, 0, 0, time.UTC)
				consumptionSince := time.Date(2026, 9, 14, 11, 0, 0, 0, time.UTC)
				transitionTime := time.Date(2026, 9, 14, 12, 0, 0, 0, time.UTC)
				store := newMockStore()
				store.states["bmi-drift"] = projection.ResourceState{
					ResourceID:         "bmi-drift",
					ResourceType:       events.ResourceTypeBareMetalInstance,
					CurrentState:       "RUNNING",
					IsBillable:         true,
					EverBillable:       true,
					BillableSince:      &allocationSince,
					FulfillmentVersion: 1,
					TransitionTime:     allocationSince,
					ComponentBillableSince: map[string]time.Time{
						events.BMaaSMeterConsumption: consumptionSince,
					},
					ComponentEverStarted: map[string]bool{
						events.BMaaSMeterAllocation:  true,
						events.BMaaSMeterConsumption: true,
					},
					BillingDimensions: map[string]any{"bm_instance_type": "bm.large"},
				}
				pub := &mockPublisher{}
				resolver := &fakeBMaaSReplaySource{records: []BMaaSReplayRecord{
					replayRecord("bmi-recovered-allocation", "tenant-1", "project-1", "RUNNING", 2, "event-2", transitionTime, map[string]any{"bm_instance_type": "bm.large"}),
				}}
				recon := NewReconciler(nil, nil, nil, resolver, store, pub, logr.Discard(), time.Minute)
				fulfillment := map[string]fulfillmentResource{"bmi-drift": {
					resourceType:      events.ResourceTypeBareMetalInstance,
					state:             targetState,
					version:           2,
					tenantID:          "tenant-1",
					billingDimensions: map[string]any{"bm_instance_type": "bm.large"},
					transitionTime:    &transitionTime,
				}}

				corrections, err := recon.reconcileFulfillmentResources(ctx, fulfillment, store.states, transitionTime)
				Expect(err).NotTo(HaveOccurred())
				Expect(corrections).To(Equal(1))
				Expect(pub.published).To(HaveLen(allocationEvents + consumptionEvents))
				for _, event := range pub.published {
					if allocationEvents == 0 {
						Expect(event.ID()).NotTo(HaveSuffix("/allocation"))
					}
				}
				stored := store.states["bmi-drift"]
				Expect(stored.TransitionTime).To(Equal(transitionTime))
				if allocationRemains {
					Expect(stored.BillableSince).NotTo(BeNil())
					Expect(*stored.BillableSince).To(Equal(allocationSince))
					Expect(stored.IsBillable).To(BeTrue())
				} else {
					Expect(stored.BillableSince).To(BeNil())
					Expect(stored.IsBillable).To(BeFalse())
				}
				Expect(stored.ComponentBillableSince).ToNot(HaveKey(events.BMaaSMeterConsumption))
			},
			Entry("RUNNING to STARTING", "STARTING", 0, 1, true),
			Entry("RUNNING to STOPPING", "STOPPING", 0, 1, true),
			Entry("RUNNING to FAILED", "FAILED", 1, 1, false),
			Entry("RUNNING to DELETING", "DELETING", 0, 1, true),
		)

		It("holds state drift when resolver history cannot recover a missing consumption interval", func() {
			allocationSince := time.Date(2026, 9, 14, 10, 0, 0, 0, time.UTC)
			transitionTime := time.Date(2026, 9, 14, 12, 0, 0, 0, time.UTC)
			store := newMockStore()
			store.states["bmi-incomplete-drift"] = projection.ResourceState{
				ResourceID:        "bmi-incomplete-drift",
				ResourceType:      events.ResourceTypeBareMetalInstance,
				CurrentState:      "RUNNING",
				IsBillable:        true,
				BillableSince:     &allocationSince,
				TransitionTime:    allocationSince,
				BillingDimensions: map[string]any{"bm_instance_type": "bm.large"},
			}
			resolver := &fakeBMaaSReplaySource{records: []BMaaSReplayRecord{
				replayRecord("bmi-incomplete-drift", "tenant-1", "", "STOPPED", 2, "event-2", transitionTime, map[string]any{"bm_instance_type": "bm.large"}),
			}}
			pub := &mockPublisher{}
			recon := NewReconciler(nil, nil, nil, resolver, store, pub, logr.Discard(), time.Minute)
			fulfillment := map[string]fulfillmentResource{"bmi-incomplete-drift": {
				resourceType:      events.ResourceTypeBareMetalInstance,
				state:             "STOPPED",
				version:           2,
				tenantID:          "tenant-1",
				billingDimensions: map[string]any{"bm_instance_type": "bm.large"},
				transitionTime:    &transitionTime,
			}}

			corrections, err := recon.reconcileFulfillmentResources(ctx, fulfillment, store.states, transitionTime)
			Expect(err).NotTo(HaveOccurred())
			Expect(corrections).To(Equal(0))
			Expect(resolver.calls).To(HaveLen(1))
			Expect(pub.published).To(BeEmpty())
			stored := store.states["bmi-incomplete-drift"]
			Expect(stored.CurrentState).To(Equal("RUNNING"))
			Expect(stored.ComponentBillableSince).ToNot(HaveKey(events.BMaaSMeterConsumption))
		})

		It("holds an existing billable projection when allocation history is missing", func() {
			transitionTime := time.Date(2026, 9, 14, 12, 0, 0, 0, time.UTC)
			store := newMockStore()
			store.states["bmi-missing-allocation"] = projection.ResourceState{
				ResourceID:         "bmi-missing-allocation",
				ResourceType:       events.ResourceTypeBareMetalInstance,
				CurrentState:       "STOPPED",
				IsBillable:         true,
				EverBillable:       true,
				FulfillmentVersion: 1,
				TransitionTime:     transitionTime,
				BillingDimensions: map[string]any{
					"bm_instance_type": "bm.large",
				},
			}
			pub := &mockPublisher{}
			recon := NewReconciler(nil, nil, nil, nil, store, pub, logr.Discard(), time.Minute)
			fulfillment := map[string]fulfillmentResource{"bmi-missing-allocation": {
				resourceType:      events.ResourceTypeBareMetalInstance,
				state:             "RUNNING",
				version:           2,
				tenantID:          "tenant-1",
				billingDimensions: map[string]any{"bm_instance_type": "bm.large"},
				transitionTime:    &transitionTime,
			}}

			corrections, err := recon.reconcileFulfillmentResources(ctx, fulfillment, store.states, transitionTime)
			Expect(err).NotTo(HaveOccurred())
			Expect(corrections).To(Equal(0))
			Expect(pub.published).To(BeEmpty())
			stored := store.states["bmi-missing-allocation"]
			Expect(stored.CurrentState).To(Equal("STOPPED"))
			Expect(stored.BillableSince).To(BeNil())
		})

		It("holds BMaaS instance-type dimension drift", func() {
			allocationSince := time.Date(2026, 9, 14, 10, 0, 0, 0, time.UTC)
			transitionTime := time.Date(2026, 9, 14, 12, 0, 0, 0, time.UTC)
			store := newMockStore()
			store.states["bmi-recovered-allocation"] = projection.ResourceState{
				ResourceID:         "bmi-recovered-allocation",
				ResourceType:       events.ResourceTypeBareMetalInstance,
				CurrentState:       "STOPPED",
				IsBillable:         true,
				EverBillable:       true,
				FulfillmentVersion: 1,
				TransitionTime:     transitionTime,
				BillingDimensions: map[string]any{
					"bm_instance_type": "bm.large",
				},
			}
			resolver := &fakeBMaaSReplaySource{records: []BMaaSReplayRecord{
				replayRecord("bmi-recovered-allocation", "tenant-1", "project-1", "RUNNING", 2, "event-2", allocationSince, map[string]any{"bm_instance_type": "bm.large"}),
			}}
			pub := &mockPublisher{}
			recon := NewReconciler(nil, nil, nil, resolver, store, pub, logr.Discard(), time.Minute)
			fulfillment := map[string]fulfillmentResource{"bmi-recovered-allocation": {
				resourceType: events.ResourceTypeBareMetalInstance,
				state:        "STOPPED",
				version:      2,
				tenantID:     "tenant-1",
				projectID:    "project-1",
				billingDimensions: map[string]any{
					"bm_instance_type": "bm.xlarge",
				},
				transitionTime: &transitionTime,
			}}

			corrections, err := recon.reconcileFulfillmentResources(ctx, fulfillment, store.states, transitionTime)
			Expect(err).NotTo(HaveOccurred())
			Expect(corrections).To(Equal(0))
			Expect(resolver.calls).To(BeEmpty())
			Expect(pub.published).To(BeEmpty())
			stored := store.states["bmi-recovered-allocation"]
			Expect(stored.BillableSince).To(BeNil())
			Expect(stored.BillingDimensions).To(HaveKeyWithValue("bm_instance_type", "bm.large"))
		})

		It("updates catalog-only dimension drift without creating a lifecycle interval", func() {
			allocationSince := time.Date(2026, 9, 14, 10, 0, 0, 0, time.UTC)
			transitionTime := time.Date(2026, 9, 14, 12, 0, 0, 0, time.UTC)
			store := newMockStore()
			store.states["bmi-catalog-drift"] = projection.ResourceState{
				ResourceID:         "bmi-catalog-drift",
				ResourceType:       events.ResourceTypeBareMetalInstance,
				CurrentState:       "STOPPED",
				IsBillable:         true,
				EverBillable:       true,
				BillableSince:      &allocationSince,
				TransitionTime:     allocationSince,
				FulfillmentVersion: 1,
				BillingDimensions: map[string]any{
					"bm_instance_type": "bm.large",
					"catalog_item":     "old-catalog",
				},
			}
			pub := &mockPublisher{}
			recon := NewReconciler(nil, nil, nil, nil, store, pub, logr.Discard(), time.Minute)
			fulfillment := map[string]fulfillmentResource{"bmi-catalog-drift": {
				resourceType: events.ResourceTypeBareMetalInstance,
				state:        "STOPPED",
				version:      2,
				tenantID:     "tenant-1",
				projectID:    "project-1",
				billingDimensions: map[string]any{
					"bm_instance_type": "bm.large",
					"catalog_item":     "new-catalog",
				},
				transitionTime: &transitionTime,
			}}

			corrections, err := recon.reconcileFulfillmentResources(ctx, fulfillment, store.states, transitionTime)
			Expect(err).NotTo(HaveOccurred())
			Expect(corrections).To(Equal(0))
			Expect(pub.published).To(BeEmpty())
			stored := store.states["bmi-catalog-drift"]
			Expect(stored.FulfillmentVersion).To(Equal(int32(2)))
			Expect(stored.BillingDimensions).To(HaveKeyWithValue("catalog_item", "new-catalog"))
			Expect(stored.BillableSince).NotTo(BeNil())
			Expect(*stored.BillableSince).To(Equal(allocationSince))
		})

		It("uses the snapshot timestamp for a contiguous RUNNING to FAILED transition", func() {
			allocationSince := time.Date(2026, 9, 14, 10, 0, 0, 0, time.UTC)
			consumptionSince := time.Date(2026, 9, 14, 11, 0, 0, 0, time.UTC)
			transitionTime := time.Date(2026, 9, 14, 12, 0, 0, 0, time.UTC)
			store := newMockStore()
			store.states["bmi-failed"] = projection.ResourceState{
				ResourceID:         "bmi-failed",
				ResourceType:       events.ResourceTypeBareMetalInstance,
				CurrentState:       "RUNNING",
				IsBillable:         true,
				BillableSince:      &allocationSince,
				FulfillmentVersion: 1,
				ComponentBillableSince: map[string]time.Time{
					events.BMaaSMeterConsumption: consumptionSince,
				},
				BillingDimensions: map[string]any{"bm_instance_type": "bm.large"},
			}
			pub := &mockPublisher{}
			recon := NewReconciler(nil, nil, nil, nil, store, pub, logr.Discard(), time.Minute)
			fulfillment := map[string]fulfillmentResource{"bmi-failed": {
				resourceType:      events.ResourceTypeBareMetalInstance,
				state:             "FAILED",
				version:           2,
				tenantID:          "tenant-1",
				billingDimensions: map[string]any{"bm_instance_type": "bm.large"},
				transitionTime:    &transitionTime,
			}}

			corrections, err := recon.reconcileFulfillmentResources(ctx, fulfillment, store.states, transitionTime)
			Expect(err).NotTo(HaveOccurred())
			Expect(corrections).To(Equal(1))
			Expect(pub.published).To(HaveLen(2))
			for _, event := range pub.published {
				var data map[string]any
				Expect(json.Unmarshal(event.Data(), &data)).To(Succeed())
				Expect(data["affected_interval"]).NotTo(BeNil())
			}
		})

		It("reopens consumption without restarting allocation", func() {
			allocationSince := time.Date(2026, 9, 14, 10, 0, 0, 0, time.UTC)
			transitionTime := time.Date(2026, 9, 14, 12, 0, 0, 0, time.UTC)
			store := newMockStore()
			store.states["bmi-resume"] = projection.ResourceState{
				ResourceID:         "bmi-resume",
				ResourceType:       events.ResourceTypeBareMetalInstance,
				CurrentState:       "STOPPED",
				IsBillable:         true,
				EverBillable:       true,
				BillableSince:      &allocationSince,
				FulfillmentVersion: 1,
				TransitionTime:     allocationSince,
				ComponentEverStarted: map[string]bool{
					events.BMaaSMeterAllocation:  true,
					events.BMaaSMeterConsumption: true,
				},
				BillingDimensions: map[string]any{"bm_instance_type": "bm.large"},
			}
			pub := &mockPublisher{}
			recon := NewReconciler(nil, nil, nil, nil, store, pub, logr.Discard(), time.Minute)
			fulfillment := map[string]fulfillmentResource{"bmi-resume": {
				resourceType:      events.ResourceTypeBareMetalInstance,
				state:             "RUNNING",
				version:           2,
				tenantID:          "tenant-1",
				billingDimensions: map[string]any{"bm_instance_type": "bm.large"},
				transitionTime:    &transitionTime,
			}}

			corrections, err := recon.reconcileFulfillmentResources(ctx, fulfillment, store.states, transitionTime)
			Expect(err).NotTo(HaveOccurred())
			Expect(corrections).To(Equal(1))
			Expect(pub.published).To(HaveLen(1))
			Expect(pub.published[0].ID()).To(HaveSuffix("/consumption"))
			stored := store.states["bmi-resume"]
			Expect(stored.BillableSince).NotTo(BeNil())
			Expect(*stored.BillableSince).To(Equal(allocationSince))
			Expect(stored.ComponentBillableSince).To(HaveKeyWithValue(events.BMaaSMeterConsumption, transitionTime))
		})

		It("emits meter-specific missed-deletion corrections before removing the projection", func() {
			allocationSince := time.Date(2026, 9, 14, 10, 0, 0, 0, time.UTC)
			consumptionSince := time.Date(2026, 9, 14, 11, 0, 0, 0, time.UTC)
			deletionTime := time.Date(2026, 9, 14, 12, 0, 0, 0, time.UTC)
			store := newMockStore()
			state := projection.ResourceState{
				ResourceID:     "bmi-deleted",
				ResourceType:   events.ResourceTypeBareMetalInstance,
				TenantID:       "tenant-1",
				ProjectID:      "project-1",
				CurrentState:   "RUNNING",
				IsBillable:     true,
				BillableSince:  &allocationSince,
				TransitionTime: allocationSince,
				ComponentBillableSince: map[string]time.Time{
					events.BMaaSMeterConsumption: consumptionSince,
				},
				ComponentEverStarted: map[string]bool{
					events.BMaaSMeterAllocation:  true,
					events.BMaaSMeterConsumption: true,
				},
				FulfillmentVersion: 7,
				BillingDimensions:  map[string]any{"bm_instance_type": "bm.large"},
			}
			store.states[state.ResourceID] = state
			pub := &mockPublisher{}
			readTime := time.Date(2026, 9, 14, 15, 0, 0, 0, time.UTC)
			resolver := &fakeBMaaSReplaySource{records: []BMaaSReplayRecord{
				replayRecord("bmi-deleted", "tenant-1", "project-1", "RUNNING", 7, "event-update", deletionTime.Add(-time.Minute), map[string]any{"bm_instance_type": "bm.large"}),
				deletedReplayRecord("bmi-deleted", "tenant-1", "project-1", 7, "event-delete", deletionTime, map[string]any{"bm_instance_type": "bm.large"}),
			}}
			recon := NewReconciler(nil, nil, &mockBareMetalInstancesClient{}, resolver, store, pub, logr.Discard(), time.Minute)

			corrections, err := recon.reconcileMissedDeletions(ctx, map[string]fulfillmentResource{}, store.states, readTime)
			Expect(err).NotTo(HaveOccurred())
			Expect(corrections).To(Equal(1))
			Expect(pub.published).To(HaveLen(2))
			Expect(pub.published[0].ID()).To(HaveSuffix("/allocation"))
			Expect(pub.published[1].ID()).To(HaveSuffix("/consumption"))
			Expect(pub.published[0].Time()).To(Equal(deletionTime))
			Expect(pub.published[1].Time()).To(Equal(deletionTime))
			Expect(store.states).ToNot(HaveKey("bmi-deleted"))
		})

		It("holds missed deletion when replay has no deletion event", func() {
			allocationSince := time.Date(2026, 9, 14, 10, 0, 0, 0, time.UTC)
			consumptionSince := time.Date(2026, 9, 14, 11, 0, 0, 0, time.UTC)
			updateTime := time.Date(2026, 9, 14, 12, 0, 0, 0, time.UTC)
			store := newMockStore()
			store.states["bmi-update-only"] = projection.ResourceState{
				ResourceID:         "bmi-update-only",
				ResourceType:       events.ResourceTypeBareMetalInstance,
				CurrentState:       "RUNNING",
				IsBillable:         true,
				BillableSince:      &allocationSince,
				FulfillmentVersion: 7,
				TransitionTime:     allocationSince,
				ComponentBillableSince: map[string]time.Time{
					events.BMaaSMeterConsumption: consumptionSince,
				},
				BillingDimensions: map[string]any{"bm_instance_type": "bm.large"},
			}
			pub := &mockPublisher{}
			resolver := &fakeBMaaSReplaySource{records: []BMaaSReplayRecord{
				replayRecord("bmi-update-only", "tenant-1", "project-1", "RUNNING", 7, "event-update", updateTime, map[string]any{"bm_instance_type": "bm.large"}),
			}}
			recon := NewReconciler(nil, nil, &mockBareMetalInstancesClient{}, resolver, store, pub, logr.Discard(), time.Minute)

			corrections, err := recon.reconcileMissedDeletions(ctx, map[string]fulfillmentResource{}, store.states, updateTime.Add(time.Hour))
			Expect(err).NotTo(HaveOccurred())
			Expect(corrections).To(Equal(0))
			Expect(pub.published).To(BeEmpty())
			Expect(store.states).To(HaveKey("bmi-update-only"))
		})

		It("holds missed deletion without a resolver-provided boundary", func() {
			allocationSince := time.Date(2026, 9, 14, 10, 0, 0, 0, time.UTC)
			consumptionSince := time.Date(2026, 9, 14, 11, 0, 0, 0, time.UTC)
			store := newMockStore()
			store.states["bmi-held-deletion"] = projection.ResourceState{
				ResourceID:     "bmi-held-deletion",
				ResourceType:   events.ResourceTypeBareMetalInstance,
				CurrentState:   "RUNNING",
				IsBillable:     true,
				BillableSince:  &allocationSince,
				TransitionTime: allocationSince,
				ComponentBillableSince: map[string]time.Time{
					events.BMaaSMeterConsumption: consumptionSince,
				},
				BillingDimensions: map[string]any{"bm_instance_type": "bm.large"},
			}
			pub := &mockPublisher{}
			recon := NewReconciler(nil, nil, &mockBareMetalInstancesClient{}, nil, store, pub, logr.Discard(), time.Minute)

			corrections, err := recon.reconcileMissedDeletions(ctx, map[string]fulfillmentResource{}, store.states, time.Date(2026, 9, 14, 15, 0, 0, 0, time.UTC))
			Expect(err).NotTo(HaveOccurred())
			Expect(corrections).To(Equal(0))
			Expect(pub.published).To(BeEmpty())
			Expect(store.states).To(HaveKey("bmi-held-deletion"))
		})

		It("synthesizes allocation and consumption heartbeats for a stale BMaaS projection present in fulfillment", func() {
			allocationSince := time.Date(2026, 9, 14, 10, 0, 0, 0, time.UTC)
			consumptionSince := time.Date(2026, 9, 14, 11, 0, 0, 0, time.UTC)
			lastHeartbeat := time.Date(2026, 9, 14, 11, 15, 0, 0, time.UTC)
			now := time.Date(2026, 9, 14, 12, 0, 0, 0, time.UTC)
			store := newMockStore()
			store.states["bmi-heartbeat"] = projection.ResourceState{
				ResourceID:        "bmi-heartbeat",
				ResourceType:      events.ResourceTypeBareMetalInstance,
				CurrentState:      "RUNNING",
				IsBillable:        true,
				BillableSince:     &allocationSince,
				LastHeartbeatAt:   &lastHeartbeat,
				BillingDimensions: map[string]any{"bm_instance_type": "bm.large"},
				ComponentBillableSince: map[string]time.Time{
					events.BMaaSMeterConsumption: consumptionSince,
				},
			}
			pub := &mockPublisher{}
			recon := NewReconciler(nil, nil, &mockBareMetalInstancesClient{}, nil, store, pub, logr.Discard(), time.Minute)

			corrections, err := recon.reconcileStaleHeartbeats(ctx, map[string]fulfillmentResource{"bmi-heartbeat": {}}, now)
			Expect(err).NotTo(HaveOccurred())
			Expect(corrections).To(Equal(1))
			Expect(pub.published).To(HaveLen(2))
			Expect(pub.published[0].ID()).To(HaveSuffix("/allocation"))
			Expect(pub.published[1].ID()).To(HaveSuffix("/consumption"))
			Expect(store.lastHeartbeatUpdateIDs).To(Equal([][]string{{"bmi-heartbeat"}}))
		})

		It("skips BMaaS deletion and heartbeat checks when the client is not configured", func() {
			lastHeartbeat := time.Date(2026, 9, 14, 10, 0, 0, 0, time.UTC)
			allocationSince := lastHeartbeat.Add(-time.Hour)
			store := newMockStore()
			store.states["bmi-disabled"] = projection.ResourceState{
				ResourceID:      "bmi-disabled",
				ResourceType:    events.ResourceTypeBareMetalInstance,
				CurrentState:    "RUNNING",
				IsBillable:      true,
				BillableSince:   &allocationSince,
				LastHeartbeatAt: &lastHeartbeat,
				BillingDimensions: map[string]any{
					"bm_instance_type": "bm.large",
				},
			}
			pub := &mockPublisher{}
			recon := NewReconciler(nil, nil, nil, NewUnavailableBMaaSReplaySource(), store, pub, logr.Discard(), time.Minute)
			now := lastHeartbeat.Add(3 * time.Minute)

			corrections, err := recon.reconcileMissedDeletions(ctx, map[string]fulfillmentResource{}, store.states, now)
			Expect(err).NotTo(HaveOccurred())
			Expect(corrections).To(Equal(0))
			corrections, err = recon.reconcileStaleHeartbeats(ctx, map[string]fulfillmentResource{}, now)
			Expect(err).NotTo(HaveOccurred())
			Expect(corrections).To(Equal(0))
			Expect(pub.published).To(BeEmpty())
			Expect(store.states).To(HaveKey("bmi-disabled"))
		})

		It("does not heartbeat a held BMaaS drift but still heartbeats a matching resource", func() {
			allocationSince := time.Date(2026, 9, 14, 10, 0, 0, 0, time.UTC)
			consumptionSince := time.Date(2026, 9, 14, 11, 0, 0, 0, time.UTC)
			lastHeartbeat := time.Date(2026, 9, 14, 11, 15, 0, 0, time.UTC)
			transitionTime := time.Date(2026, 9, 14, 11, 30, 0, 0, time.UTC)
			store := newMockStore()
			for _, id := range []string{"bmi-held-drift", "bmi-matching"} {
				store.states[id] = projection.ResourceState{
					ResourceID:      id,
					ResourceType:    events.ResourceTypeBareMetalInstance,
					TenantID:        "tenant-1",
					ProjectID:       "project-1",
					CurrentState:    "RUNNING",
					IsBillable:      true,
					EverBillable:    true,
					BillableSince:   &allocationSince,
					LastHeartbeatAt: &lastHeartbeat,
					FulfillmentVersion: func() int32 {
						if id == "bmi-held-drift" {
							return 1
						}
						return 2
					}(),
					ComponentBillableSince: map[string]time.Time{
						events.BMaaSMeterConsumption: consumptionSince,
					},
					BillingDimensions: map[string]any{"bm_instance_type": "bm.large"},
				}
			}
			client := &mockBareMetalInstancesClient{items: []*privatev1.BareMetalInstance{
				makeBMI("bmi-held-drift", "tenant-1", privatev1.BareMetalInstanceState_BARE_METAL_INSTANCE_STATE_STOPPED, 2, timestamppb.New(transitionTime)),
				makeBMI("bmi-matching", "tenant-1", privatev1.BareMetalInstanceState_BARE_METAL_INSTANCE_STATE_RUNNING, 2, timestamppb.New(transitionTime)),
			}}
			pub := &mockPublisher{}
			recon := NewReconciler(nil, nil, client, nil, store, pub, logr.Discard(), time.Minute)

			Expect(recon.Reconcile(ctx)).To(Succeed())
			Expect(store.lastHeartbeatUpdateIDs).To(Equal([][]string{{"bmi-matching"}}))
			for _, event := range pub.published {
				Expect(event.Extensions()["osacresourceid"]).To(Equal("bmi-matching"))
			}
		})

		It("continues the pass when BMaaS skips an intermediate state", func() {
			transitionTime := time.Date(2026, 9, 14, 12, 0, 0, 0, time.UTC)
			store := newMockStore()
			store.states["bmi-skipped-state"] = projection.ResourceState{
				ResourceID:         "bmi-skipped-state",
				ResourceType:       events.ResourceTypeBareMetalInstance,
				CurrentState:       "STOPPED",
				IsBillable:         true,
				BillableSince:      func() *time.Time { value := transitionTime.Add(-time.Hour); return &value }(),
				FulfillmentVersion: 1,
				BillingDimensions:  map[string]any{"bm_instance_type": "bm.large"},
			}
			client := &mockBareMetalInstancesClient{items: []*privatev1.BareMetalInstance{
				makeBMI("bmi-skipped-state", "tenant-1", privatev1.BareMetalInstanceState_BARE_METAL_INSTANCE_STATE_STOPPING, 2, timestamppb.New(transitionTime)),
			}}
			computeClient := &mockComputeClient{items: []*privatev1.ComputeInstance{
				makeCI("vm-after-bmaas-hold", "tenant-1", "RUNNING", 1),
			}}
			pub := &mockPublisher{}
			recon := NewReconciler(computeClient, nil, client, nil, store, pub, logr.Discard(), time.Minute)

			Expect(recon.Reconcile(ctx)).To(Succeed())
			Expect(store.states).To(HaveKey("vm-after-bmaas-hold"))
			var sawVMCorrection bool
			for _, event := range pub.published {
				if event.Extensions()["osacresourceid"] == "vm-after-bmaas-hold" && event.Type() == events.EventCorrection {
					sawVMCorrection = true
				}
			}
			Expect(sawVMCorrection).To(BeTrue())
		})

		It("holds a newly observed BMaaS snapshot until meter-specific reconciliation is available", func() {
			client := &mockBareMetalInstancesClient{items: []*privatev1.BareMetalInstance{
				makeBMI("bmi-new", "tenant-1", privatev1.BareMetalInstanceState_BARE_METAL_INSTANCE_STATE_RUNNING, 1, timestamppb.Now()),
			}}
			store := newMockStore()
			pub := &mockPublisher{}
			recon := NewReconciler(nil, nil, client, nil, store, pub, logr.Discard(), 60*time.Second)

			Expect(recon.Reconcile(ctx)).To(Succeed())

			pub.mu.Lock()
			defer pub.mu.Unlock()
			Expect(pub.published).To(BeEmpty(), "BMaaS must not emit generic corrections or synthetic heartbeats without durable meter boundaries")
			store.mu.Lock()
			defer store.mu.Unlock()
			Expect(store.states).ToNot(HaveKey("bmi-new"), "BMaaS must not persist guessed billable intervals")
		})
		It("holds a projected BMaaS instance skipped for invalid dimensions", func() {
			deletionTime := time.Date(2026, 9, 14, 12, 0, 0, 0, time.UTC)
			invalid := makeBMI("bmi-invalid-dimensions", "tenant-1", privatev1.BareMetalInstanceState_BARE_METAL_INSTANCE_STATE_RUNNING, 7, timestamppb.New(deletionTime))
			invalid.Spec.InstanceType = nil
			client := &mockBareMetalInstancesClient{items: []*privatev1.BareMetalInstance{invalid}}
			store := newMockStore()
			allocationSince := deletionTime.Add(-2 * time.Hour)
			consumptionSince := deletionTime.Add(-time.Hour)
			store.states[invalid.GetId()] = projection.ResourceState{
				ResourceID:         invalid.GetId(),
				ResourceType:       events.ResourceTypeBareMetalInstance,
				CurrentState:       "RUNNING",
				IsBillable:         true,
				BillableSince:      &allocationSince,
				FulfillmentVersion: 7,
				ComponentBillableSince: map[string]time.Time{
					events.BMaaSMeterConsumption: consumptionSince,
				},
				BillingDimensions: map[string]any{"bm_instance_type": "bm.large"},
			}
			resolver := &fakeBMaaSReplaySource{records: []BMaaSReplayRecord{
				deletedReplayRecord(invalid.GetId(), "tenant-1", "project-1", 7, "event-delete", deletionTime, map[string]any{"bm_instance_type": "bm.large"}),
			}}
			pub := &mockPublisher{}
			recon := NewReconciler(nil, nil, client, resolver, store, pub, logr.Discard(), time.Minute)
			presence := heartbeat.NewBMaaSPresence()
			recon.SetBMaaSPresence(presence)

			Expect(recon.Reconcile(ctx)).To(Succeed())
			Expect(pub.published).To(BeEmpty())
			Expect(store.states).To(HaveKey(invalid.GetId()))
			Expect(presence.Contains(invalid.GetId())).To(BeFalse())
		})

		It("holds a projected BMaaS instance skipped for invalid dimensions", func() {
			deletionTime := time.Date(2026, 9, 14, 12, 0, 0, 0, time.UTC)
			invalid := makeBMI("bmi-invalid-dimensions", "tenant-1", privatev1.BareMetalInstanceState_BARE_METAL_INSTANCE_STATE_RUNNING, 7, timestamppb.New(deletionTime))
			invalid.Spec.InstanceType = nil
			client := &mockBareMetalInstancesClient{items: []*privatev1.BareMetalInstance{invalid}}
			store := newMockStore()
			allocationSince := deletionTime.Add(-2 * time.Hour)
			consumptionSince := deletionTime.Add(-time.Hour)
			store.states[invalid.GetId()] = projection.ResourceState{
				ResourceID:         invalid.GetId(),
				ResourceType:       events.ResourceTypeBareMetalInstance,
				CurrentState:       "RUNNING",
				IsBillable:         true,
				BillableSince:      &allocationSince,
				FulfillmentVersion: 7,
				ComponentBillableSince: map[string]time.Time{
					events.BMaaSMeterConsumption: consumptionSince,
				},
				BillingDimensions: map[string]any{"bm_instance_type": "bm.large"},
			}
			resolver := &fakeBMaaSReplaySource{records: []BMaaSReplayRecord{
				deletedReplayRecord(invalid.GetId(), "tenant-1", "project-1", 7, "event-delete", deletionTime, map[string]any{"bm_instance_type": "bm.large"}),
			}}
			pub := &mockPublisher{}
			recon := NewReconciler(nil, nil, client, resolver, store, pub, logr.Discard(), time.Minute)

			Expect(recon.Reconcile(ctx)).To(Succeed())
			Expect(pub.published).To(BeEmpty())
			Expect(store.states).To(HaveKey(invalid.GetId()))
		})

		It("closes active BMaaS meters and removes a projection missing from fulfillment", func() {
			client := &mockBareMetalInstancesClient{}
			store := newMockStore()
			allocationSince := time.Date(2026, 9, 14, 10, 0, 0, 0, time.UTC)
			consumptionSince := time.Date(2026, 9, 14, 11, 0, 0, 0, time.UTC)
			lastHeartbeat := time.Date(2026, 9, 14, 12, 0, 0, 0, time.UTC)
			store.states["bmi-missing"] = projection.ResourceState{
				ResourceID:    "bmi-missing",
				ResourceType:  events.ResourceTypeBareMetalInstance,
				TenantID:      "tenant-1",
				CurrentState:  "RUNNING",
				IsBillable:    true,
				BillableSince: &allocationSince,
				ComponentBillableSince: map[string]time.Time{
					events.BMaaSMeterConsumption: consumptionSince,
				},
				LastHeartbeatAt:   &lastHeartbeat,
				BillingDimensions: map[string]any{"bm_instance_type": "bm.large"},
			}
			pub := &mockPublisher{}
			resolver := &fakeBMaaSReplaySource{records: []BMaaSReplayRecord{
				deletedReplayRecord("bmi-missing", "tenant-1", "project-1", 0, "event-delete", lastHeartbeat, map[string]any{"bm_instance_type": "bm.large"}),
			}}
			recon := NewReconciler(nil, nil, client, resolver, store, pub, logr.Discard(), 60*time.Second)

			Expect(recon.Reconcile(ctx)).To(Succeed())

			pub.mu.Lock()
			defer pub.mu.Unlock()
			Expect(pub.published).To(HaveLen(2))
			Expect(pub.published[0].ID()).To(HaveSuffix("/allocation"))
			Expect(pub.published[1].ID()).To(HaveSuffix("/consumption"))
			store.mu.Lock()
			defer store.mu.Unlock()
			Expect(store.states).ToNot(HaveKey("bmi-missing"))
		})

		It("does not synthesize or checkpoint heartbeats after missed-deletion closure", func() {
			client := &mockBareMetalInstancesClient{}
			store := newMockStore()
			allocationSince := time.Date(2026, 9, 14, 10, 0, 0, 0, time.UTC)
			consumptionSince := time.Date(2026, 9, 14, 11, 0, 0, 0, time.UTC)
			lastHeartbeat := time.Date(2026, 9, 14, 11, 30, 0, 0, time.UTC)
			store.states["bmi-stale-missing"] = projection.ResourceState{
				ResourceID:    "bmi-stale-missing",
				ResourceType:  events.ResourceTypeBareMetalInstance,
				TenantID:      "tenant-1",
				CurrentState:  "RUNNING",
				IsBillable:    true,
				BillableSince: &allocationSince,
				ComponentBillableSince: map[string]time.Time{
					events.BMaaSMeterConsumption: consumptionSince,
				},
				LastHeartbeatAt:   &lastHeartbeat,
				BillingDimensions: map[string]any{"bm_instance_type": "bm.large"},
			}
			pub := &mockPublisher{}
			resolver := &fakeBMaaSReplaySource{records: []BMaaSReplayRecord{
				deletedReplayRecord("bmi-stale-missing", "tenant-1", "project-1", 0, "event-delete", lastHeartbeat, map[string]any{"bm_instance_type": "bm.large"}),
			}}
			recon := NewReconciler(nil, nil, client, resolver, store, pub, logr.Discard(), time.Minute)

			Expect(recon.Reconcile(ctx)).To(Succeed())

			pub.mu.Lock()
			defer pub.mu.Unlock()
			Expect(pub.published).To(HaveLen(2), "the deletion correction closes both active meters before stale heartbeat handling")
			store.mu.Lock()
			defer store.mu.Unlock()
			Expect(store.states).ToNot(HaveKey("bmi-stale-missing"))
			Expect(store.lastHeartbeatUpdateIDs).To(BeEmpty(), "a deleted resource must not checkpoint a synthetic heartbeat")
		})

		It("loads paginated snapshots with normalized states, dimensions, metadata, and transition times", func() {
			transitionTime := time.Date(2026, 9, 14, 10, 30, 0, 0, time.UTC)
			items := []*privatev1.BareMetalInstance{
				makeBMI("bmi-running", "tenant-running", privatev1.BareMetalInstanceState_BARE_METAL_INSTANCE_STATE_RUNNING, 7, timestamppb.New(transitionTime)),
				makeBMI("bmi-stopped", "tenant-stopped", privatev1.BareMetalInstanceState_BARE_METAL_INSTANCE_STATE_STOPPED, 8, timestamppb.New(transitionTime)),
				makeBMI("bmi-failed", "tenant-failed", privatev1.BareMetalInstanceState_BARE_METAL_INSTANCE_STATE_FAILED, 9, timestamppb.New(transitionTime)),
				{Id: "bmi-no-status", Metadata: &privatev1.Metadata{Tenant: "tenant-none", Project: "project-none", Version: 10}, Spec: &privatev1.BareMetalInstanceSpec{InstanceType: &privatev1.BareMetalInstanceTypeLocalReference{Id: "bm.small"}}},
			}
			for i := 0; i < 496; i++ {
				items = append(items, makeBMI(fmt.Sprintf("bmi-padding-%d", i), "tenant-padding", privatev1.BareMetalInstanceState_BARE_METAL_INSTANCE_STATE_RUNNING, 1, timestamppb.New(transitionTime)))
			}
			items = append(items, makeBMI("bmi-second-page", "tenant-second", privatev1.BareMetalInstanceState_BARE_METAL_INSTANCE_STATE_RUNNING, 11, timestamppb.New(transitionTime)))

			client := &mockBareMetalInstancesClient{items: items}
			store := newMockStore()
			pub := &mockPublisher{}
			recon := NewReconciler(nil, nil, client, nil, store, pub, logr.Discard(), 60*time.Second)

			loaded, err := recon.loadFulfillmentState(ctx)
			Expect(err).NotTo(HaveOccurred())
			Expect(loaded).To(HaveLen(501))
			Expect(client.requests).To(HaveLen(2))
			Expect(client.requests[0].GetOffset()).To(Equal(int32(0)))
			Expect(client.requests[0].GetLimit()).To(Equal(int32(500)))
			Expect(client.requests[1].GetOffset()).To(Equal(int32(500)))

			running := loaded["bmi-running"]
			Expect(running.resourceType).To(Equal(events.ResourceTypeBareMetalInstance))
			Expect(running.state).To(Equal("RUNNING"))
			Expect(running.tenantID).To(Equal("tenant-running"))
			Expect(running.projectID).To(Equal("project-1"))
			Expect(running.version).To(Equal(int32(7)))
			Expect(running.billingDimensions).To(Equal(map[string]any{"bm_instance_type": "bm.large", "catalog_item": "catalog-1"}))
			Expect(running.transitionTime).NotTo(BeNil())
			Expect(*running.transitionTime).To(Equal(transitionTime))
			Expect(loaded["bmi-stopped"].state).To(Equal("STOPPED"))
			Expect(loaded["bmi-failed"].state).To(Equal("FAILED"))
			Expect(loaded["bmi-no-status"].state).To(Equal("UNSPECIFIED"))
			Expect(loaded["bmi-no-status"].transitionTime).To(BeNil())

			stoppedBillable, err := isBillableForType(events.ResourceTypeBareMetalInstance, loaded["bmi-stopped"].state)
			Expect(err).NotTo(HaveOccurred())
			Expect(stoppedBillable).To(BeTrue(), "STOPPED BMaaS remains allocation-billable")
			failedBillable, err := isBillableForType(events.ResourceTypeBareMetalInstance, loaded["bmi-failed"].state)
			Expect(err).NotTo(HaveOccurred())
			Expect(failedBillable).To(BeFalse())
			unspecifiedBillable, err := isBillableForType(events.ResourceTypeBareMetalInstance, loaded["bmi-no-status"].state)
			Expect(err).NotTo(HaveOccurred())
			Expect(unspecifiedBillable).To(BeFalse())
		})

		It("skips snapshots without a bare metal instance type ID", func() {
			client := &mockBareMetalInstancesClient{items: []*privatev1.BareMetalInstance{
				{
					Id:       "bmi-invalid-dimensions",
					Metadata: &privatev1.Metadata{Tenant: "tenant-1", Version: 1},
					Spec:     &privatev1.BareMetalInstanceSpec{InstanceType: &privatev1.BareMetalInstanceTypeLocalReference{}},
				},
				makeBMI("bmi-valid", "tenant-1", privatev1.BareMetalInstanceState_BARE_METAL_INSTANCE_STATE_RUNNING, 2, timestamppb.Now()),
			}}
			computeClient := &mockComputeClient{items: []*privatev1.ComputeInstance{makeCI("vm-valid", "tenant-1", "RUNNING", 1)}}
			store := newMockStore()
			recon := NewReconciler(computeClient, nil, client, nil, store, &mockPublisher{}, logr.Discard(), 60*time.Second)

			loaded, err := recon.loadFulfillmentState(ctx)
			Expect(err).NotTo(HaveOccurred())
			Expect(loaded).To(HaveLen(2))
			Expect(loaded).To(HaveKey("bmi-valid"))
			Expect(loaded).NotTo(HaveKey("bmi-invalid-dimensions"))

			Expect(recon.Reconcile(ctx)).To(Succeed())
			Expect(store.states).To(HaveKey("vm-valid"))
		})

		It("propagates bare metal instance list errors", func() {
			client := &mockBareMetalInstancesClient{err: fmt.Errorf("fulfillment unavailable")}
			recon := NewReconciler(nil, nil, client, nil, newMockStore(), &mockPublisher{}, logr.Discard(), 60*time.Second)

			_, err := recon.loadFulfillmentState(ctx)
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("listing bare metal instances (offset=0): fulfillment unavailable"))
		})
	})

	Describe("RunPeriodic", func() {
		It("stops on context cancellation", func() {
			client := &mockComputeClient{}
			store := newMockStore()
			pub := &mockPublisher{}
			recon := NewReconciler(client, nil, nil, nil, store, pub, logr.Discard(), 60*time.Second)

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

	Describe("CaaS cluster reconciliation", func() {
		makeClusterProto := func(id, tenant string, state privatev1.ClusterState, version int32) *privatev1.Cluster {
			return &privatev1.Cluster{
				Id: id,
				Metadata: &privatev1.Metadata{
					Tenant:  tenant,
					Version: version,
				},
				Spec: &privatev1.ClusterSpec{
					Template: &privatev1.ClusterTemplateReference{Name: "ocp-ci-small"},
					Version:  &privatev1.ClusterVersionReference{Id: "4.17.0", Name: "4.17.0"},
					NodeSets: map[string]*privatev1.ClusterNodeSet{
						"gpu-workers": {HostType: &privatev1.HostTypeReference{Name: "gpu-h100"}, Size: proto.Int32(2)},
					},
				},
				Status: &privatev1.ClusterStatus{State: state},
			}
		}

		It("detects missed_creation for cluster and emits N+1 correction events", func() {
			computeClient := &mockComputeClient{}
			clusterClient := &mockClusterClient{
				items: []*privatev1.Cluster{
					makeClusterProto("cl-missed", "tenant-1", privatev1.ClusterState_CLUSTER_STATE_READY, 1),
				},
			}
			store := newMockStore()
			pub := &mockPublisher{}
			recon := NewReconciler(computeClient, clusterClient, nil, nil, store, pub, logr.Discard(), 60*time.Second)

			Expect(recon.Reconcile(ctx)).To(Succeed())

			pub.mu.Lock()
			defer pub.mu.Unlock()
			correctionCount := 0
			for _, e := range pub.published {
				if e.Type() == events.EventCorrection {
					correctionCount++
					var data map[string]any
					Expect(json.Unmarshal(e.Data(), &data)).To(Succeed())
					Expect(data["reason"]).To(Equal("missed_creation"))
					Expect(data["resource_type"]).To(Equal(events.ResourceTypeClusterOrder))
					bd := data["billing_dimensions"].(map[string]any)
					Expect(bd).To(HaveKey("component"))
					Expect(bd).To(HaveKey("host_type"))
					Expect(bd).NotTo(HaveKey("components"))
				}
			}
			// 1 control_plane + 1 gpu-h100 worker = 2 correction events
			Expect(correctionCount).To(Equal(2))

			store.mu.Lock()
			defer store.mu.Unlock()
			Expect(store.states).To(HaveKey("cl-missed"))
			Expect(store.states["cl-missed"].ResourceType).To(Equal(events.ResourceTypeClusterOrder))
			Expect(store.states["cl-missed"].IsBillable).To(BeTrue())
		})

		It("detects state_drift for cluster and emits N+1 correction events", func() {
			computeClient := &mockComputeClient{}
			clusterClient := &mockClusterClient{
				items: []*privatev1.Cluster{
					makeClusterProto("cl-drift", "tenant-1", privatev1.ClusterState_CLUSTER_STATE_FAILED, 2),
				},
			}
			store := newMockStore()
			now := time.Now().UTC().Truncate(time.Microsecond)
			store.states["cl-drift"] = projection.ResourceState{
				ResourceID:         "cl-drift",
				ResourceType:       events.ResourceTypeClusterOrder,
				TenantID:           "tenant-1",
				CurrentState:       "READY",
				IsBillable:         true,
				BillableSince:      &now,
				FulfillmentVersion: 1,
				BillingDimensions: map[string]any{
					"cluster_template": "ocp-ci-small",
					"release_image":    "4.17.0",
					"components": []any{
						map[string]any{"node_set": "_control_plane", "component": "control_plane", "host_type": "_control_plane", "node_count": float64(1)},
						map[string]any{"node_set": "gpu-workers", "component": "worker", "host_type": "gpu-h100", "node_count": float64(2)},
					},
				},
			}

			pub := &mockPublisher{}
			recon := NewReconciler(computeClient, clusterClient, nil, nil, store, pub, logr.Discard(), 60*time.Second)

			Expect(recon.Reconcile(ctx)).To(Succeed())

			pub.mu.Lock()
			defer pub.mu.Unlock()
			driftCount := 0
			for _, e := range pub.published {
				if e.Type() == events.EventCorrection {
					var data map[string]any
					Expect(json.Unmarshal(e.Data(), &data)).To(Succeed())
					if data["reason"] == "state_drift" {
						driftCount++
					}
				}
			}
			Expect(driftCount).To(Equal(2))

			store.mu.Lock()
			defer store.mu.Unlock()
			Expect(store.states["cl-drift"].CurrentState).To(Equal("FAILED"))
			Expect(store.states["cl-drift"].IsBillable).To(BeFalse())
		})

		It("detects missed_deletion for cluster and emits N+1 correction events", func() {
			computeClient := &mockComputeClient{}
			clusterClient := &mockClusterClient{}
			store := newMockStore()
			now := time.Now().UTC().Truncate(time.Microsecond)
			store.states["cl-gone"] = projection.ResourceState{
				ResourceID:    "cl-gone",
				ResourceType:  events.ResourceTypeClusterOrder,
				TenantID:      "tenant-1",
				CurrentState:  "READY",
				IsBillable:    true,
				BillableSince: &now,
				BillingDimensions: map[string]any{
					"cluster_template": "ocp-ci-small",
					"components": []any{
						map[string]any{"node_set": "_control_plane", "component": "control_plane", "host_type": "_control_plane", "node_count": float64(1)},
						map[string]any{"node_set": "gpu-workers", "component": "worker", "host_type": "gpu-h100", "node_count": float64(2)},
					},
				},
			}

			pub := &mockPublisher{}
			recon := NewReconciler(computeClient, clusterClient, nil, nil, store, pub, logr.Discard(), 60*time.Second)

			Expect(recon.Reconcile(ctx)).To(Succeed())

			pub.mu.Lock()
			defer pub.mu.Unlock()
			deletionCount := 0
			for _, e := range pub.published {
				if e.Type() == events.EventCorrection {
					var data map[string]any
					Expect(json.Unmarshal(e.Data(), &data)).To(Succeed())
					if data["reason"] == "missed_deletion" {
						deletionCount++
						bd := data["billing_dimensions"].(map[string]any)
						Expect(bd).To(HaveKey("component"))
						Expect(bd).NotTo(HaveKey("components"))
					}
				}
			}
			Expect(deletionCount).To(Equal(2))

			store.mu.Lock()
			defer store.mu.Unlock()
			Expect(store.states).ToNot(HaveKey("cl-gone"))
		})

		It("skips cluster missed_deletion when clusterClient is nil", func() {
			computeClient := &mockComputeClient{}
			store := newMockStore()
			now := time.Now().UTC().Truncate(time.Microsecond)
			store.states["cl-safe"] = projection.ResourceState{
				ResourceID:        "cl-safe",
				ResourceType:      events.ResourceTypeClusterOrder,
				TenantID:          "tenant-1",
				CurrentState:      "READY",
				IsBillable:        true,
				BillableSince:     &now,
				LastHeartbeatAt:   &now,
				BillingDimensions: map[string]any{},
			}

			pub := &mockPublisher{}
			recon := NewReconciler(computeClient, nil, nil, nil, store, pub, logr.Discard(), 60*time.Second)

			Expect(recon.Reconcile(ctx)).To(Succeed())

			pub.mu.Lock()
			defer pub.mu.Unlock()
			for _, e := range pub.published {
				Expect(e.Type()).ToNot(Equal(events.EventCorrection))
			}

			store.mu.Lock()
			defer store.mu.Unlock()
			Expect(store.states).To(HaveKey("cl-safe"))
		})

		It("paginates ListClusters correctly", func() {
			clusters := make([]*privatev1.Cluster, 0, 600)
			for i := range 600 {
				clusters = append(clusters, makeClusterProto(
					fmt.Sprintf("cl-%d", i), "tenant-1",
					privatev1.ClusterState_CLUSTER_STATE_READY, 1))
			}
			computeClient := &mockComputeClient{}
			clusterClient := &mockClusterClient{items: clusters}
			store := newMockStore()
			pub := &mockPublisher{}
			recon := NewReconciler(computeClient, clusterClient, nil, nil, store, pub, logr.Discard(), 60*time.Second)

			Expect(recon.Reconcile(ctx)).To(Succeed())

			store.mu.Lock()
			defer store.mu.Unlock()
			Expect(store.states).To(HaveLen(600))
		})

		It("produces deterministic synthetic heartbeat IDs for clusters", func() {
			computeClient := &mockComputeClient{}
			clusterClient := &mockClusterClient{
				items: []*privatev1.Cluster{
					makeClusterProto("cl-hb", "tenant-1", privatev1.ClusterState_CLUSTER_STATE_READY, 1),
				},
			}
			store := newMockStore()
			now := time.Now().Add(-5 * time.Minute).UTC().Truncate(time.Microsecond)
			store.states["cl-hb"] = projection.ResourceState{
				ResourceID:         "cl-hb",
				ResourceType:       events.ResourceTypeClusterOrder,
				TenantID:           "tenant-1",
				CurrentState:       "READY",
				IsBillable:         true,
				BillableSince:      &now,
				FulfillmentVersion: 1,
				BillingDimensions: map[string]any{
					"cluster_template": "ocp-ci-small",
					"release_image":    "4.17.0",
					"components": []any{
						map[string]any{"node_set": "_control_plane", "component": "control_plane", "host_type": "_control_plane", "node_count": float64(1)},
						map[string]any{"node_set": "gpu-workers", "component": "worker", "host_type": "gpu-h100", "node_count": float64(2)},
					},
				},
			}

			pub := &mockPublisher{}
			recon := NewReconciler(computeClient, clusterClient, nil, nil, store, pub, logr.Discard(), 60*time.Second)

			Expect(recon.Reconcile(ctx)).To(Succeed())

			pub.mu.Lock()
			defer pub.mu.Unlock()

			var hbEvents []cloudevents.Event
			for _, e := range pub.published {
				if e.Type() == events.EventHeartbeat {
					hbEvents = append(hbEvents, e)
				}
			}
			Expect(hbEvents).To(HaveLen(2))

			ids := map[string]bool{}
			for _, e := range hbEvents {
				Expect(e.ID()).To(ContainSubstring("synthetic-hb/cl-hb/"))
				Expect(e.ID()).NotTo(ContainSubstring("synthetic-hb/cl-hb//"))
				ids[e.ID()] = true
			}
			Expect(ids).To(HaveLen(2))
		})
	})
})

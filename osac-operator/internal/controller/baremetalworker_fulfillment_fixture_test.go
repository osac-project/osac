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
	"context"
	"sort"
	"sync"

	"github.com/google/uuid"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"

	"github.com/osac-project/osac/osac-operator/internal/controller/baremetalworker"
	privatev1 "github.com/osac-project/osac/proto/gen/osac/private/v1"
)

// workerFulfillmentStub is an in-memory, call-recording implementation of
// baremetalworker.FulfillmentClient for tests. All methods are safe for concurrent use.
type workerFulfillmentStub struct {
	mu              sync.Mutex
	bmis            map[string]*privatev1.BareMetalInstance
	hostMACs        map[string]string
	clusterVersions map[string]*privatev1.ClusterVersion
	clusters        map[string]*privatev1.Cluster
	diskImages      map[string]*privatev1.DiskImage
	catalogItems    map[string]*privatev1.BareMetalInstanceCatalogItem
	instanceTypes   map[string]*privatev1.BareMetalInstanceType
	createErr       error
	deleteErr       error
	pendingDeletion bool
	listEmptyCalls  int
	listErr         error
	getErrors       map[string]error

	createCalls            []*privatev1.BareMetalInstance
	deleteCalls            []string
	getCalls               []string
	listCalls              []string
	getDiskImageCalls      []string
	createCatalogItemCalls []*privatev1.BareMetalInstanceCatalogItem
	listCatalogItemCalls   []string
	getInstanceTypeCalls   []string
}

var _ baremetalworker.FulfillmentClient = (*workerFulfillmentStub)(nil)

// newWorkerFulfillmentStub returns isolated responses for one test. This double
// exists only in the controller test binary; it does not establish the real
// service's validation, database uniqueness or provider lifecycle contract.
func newWorkerFulfillmentStub() *workerFulfillmentStub {
	return &workerFulfillmentStub{
		bmis:            map[string]*privatev1.BareMetalInstance{},
		getErrors:       map[string]error{},
		hostMACs:        map[string]string{},
		clusterVersions: map[string]*privatev1.ClusterVersion{},
		clusters:        map[string]*privatev1.Cluster{},
		diskImages:      map[string]*privatev1.DiskImage{},
		catalogItems:    map[string]*privatev1.BareMetalInstanceCatalogItem{},
		instanceTypes:   map[string]*privatev1.BareMetalInstanceType{},
	}
}

func cloneBMI(b *privatev1.BareMetalInstance) *privatev1.BareMetalInstance {
	return proto.Clone(b).(*privatev1.BareMetalInstance)
}

func cloneCV(c *privatev1.ClusterVersion) *privatev1.ClusterVersion {
	return proto.Clone(c).(*privatev1.ClusterVersion)
}

func cloneCluster(c *privatev1.Cluster) *privatev1.Cluster {
	return proto.Clone(c).(*privatev1.Cluster)
}

func cloneDiskImage(d *privatev1.DiskImage) *privatev1.DiskImage {
	return proto.Clone(d).(*privatev1.DiskImage)
}

// CreateBareMetalInstance records the call, assigns a distinct incarnation UUID and stores
// the object. It returns the injected create error when one is set (still recording the call).
func (f *workerFulfillmentStub) CreateBareMetalInstance(
	_ context.Context, obj *privatev1.BareMetalInstance,
) (*privatev1.BareMetalInstance, error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	f.createCalls = append(f.createCalls, cloneBMI(obj))
	if f.createErr != nil {
		return nil, f.createErr
	}
	name := obj.GetMetadata().GetName()
	if name == "" {
		return nil, status.Error(codes.InvalidArgument, "baremetalinstance metadata.name is required")
	}
	// Mirror scoped name uniqueness independently of the external ID. In
	// particular, a repeated Create must not allocate a second incarnation.
	metadata := obj.GetMetadata()
	for _, existing := range f.bmis {
		other := existing.GetMetadata()
		if other.GetName() == name && other.GetTenant() == metadata.GetTenant() && other.GetProject() == metadata.GetProject() {
			return nil, status.Errorf(codes.AlreadyExists, "baremetalinstance %q already exists in scope", name)
		}
	}
	stored := cloneBMI(obj)
	id := stored.GetId()
	if id == "" {
		id = uuid.NewString()
		stored.SetId(id)
	}
	if _, exists := f.bmis[id]; exists {
		return nil, status.Errorf(codes.AlreadyExists, "baremetalinstance %q already exists", id)
	}
	f.bmis[id] = stored
	return cloneBMI(stored), nil
}

// DeleteBareMetalInstance records the request. By default it removes the object;
// pending-deletion fixtures retain it with deletion metadata until completion.
// Injected errors precede mutation (lost acknowledgements use a test wrapper).
func (f *workerFulfillmentStub) DeleteBareMetalInstance(_ context.Context, id string) error {
	f.mu.Lock()
	defer f.mu.Unlock()

	f.deleteCalls = append(f.deleteCalls, id)
	if f.deleteErr != nil {
		return f.deleteErr
	}
	if f.pendingDeletion {
		if bmi := f.bmis[id]; bmi != nil && bmi.GetMetadata().GetDeletionTimestamp() == nil {
			bmi.GetMetadata().SetDeletionTimestamp(timestamppb.Now())
		}
		return nil
	}
	delete(f.bmis, id)
	delete(f.hostMACs, id)
	return nil
}

// GetBareMetalInstance records the call and returns a copy of the stored object, or an error if
// no object with that id exists.
func (f *workerFulfillmentStub) GetBareMetalInstance(
	_ context.Context, id string,
) (*privatev1.BareMetalInstance, error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	f.getCalls = append(f.getCalls, id)
	if err := f.getErrors[id]; err != nil {
		return nil, err
	}
	b, ok := f.bmis[id]
	if !ok {
		return nil, status.Errorf(codes.NotFound, "baremetalinstance %q not found", id)
	}
	out := cloneBMI(b)
	f.injectHostMAC(out, id)
	return out, nil
}

// injectHostMAC surfaces a MAC recorded via SetHostMAC through the returned BMI's
// status.hardware.nics — the same field the real inventory backend populates at allocation
// time (OSAC-4203) and that the production MAC resolver reads. If the BMI already carries NICs
// with that MAC, this is a no-op. Caller must hold f.mu.
func (f *workerFulfillmentStub) injectHostMAC(bmi *privatev1.BareMetalInstance, id string) {
	mac := f.hostMACs[id]
	if mac == "" {
		return
	}
	for _, nic := range bmi.GetStatus().GetHardware().GetNics() {
		if nic.GetMac() == mac {
			return
		}
	}
	st := bmi.GetStatus()
	if st == nil {
		st = privatev1.BareMetalInstanceStatus_builder{}.Build()
		bmi.SetStatus(st)
	}
	hw := st.GetHardware()
	if hw == nil {
		hw = privatev1.BareMetalHardware_builder{}.Build()
		st.SetHardware(hw)
	}
	hw.SetNics(append(hw.GetNics(), privatev1.BareMetalNICStatus_builder{Mac: mac}.Build()))
}

// ListBareMetalInstances records the filter and returns copies of all stored objects, ordered by
// id for deterministic tests.
func (f *workerFulfillmentStub) ListBareMetalInstances(
	_ context.Context, filter string,
) ([]*privatev1.BareMetalInstance, error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	f.listCalls = append(f.listCalls, filter)
	if f.listErr != nil {
		return nil, f.listErr
	}
	if f.listEmptyCalls > 0 {
		f.listEmptyCalls--
		return nil, nil
	}
	ids := make([]string, 0, len(f.bmis))
	for id := range f.bmis {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	out := make([]*privatev1.BareMetalInstance, 0, len(ids))
	for _, id := range ids {
		bmi := cloneBMI(f.bmis[id])
		f.injectHostMAC(bmi, id)
		out = append(out, bmi)
	}
	return out, nil
}

// GetClusterVersion records the call and returns a preloaded ClusterVersion (see
// AddClusterVersion), or an error if none is registered for that id.
func (f *workerFulfillmentStub) GetClusterVersion(
	_ context.Context, id string,
) (*privatev1.ClusterVersion, error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	cv, ok := f.clusterVersions[id]
	if !ok {
		return nil, status.Errorf(codes.NotFound, "clusterversion %q not found", id)
	}
	return cloneCV(cv), nil
}

// GetCluster records the call and returns a preloaded Cluster (see AddCluster), or an error
// if none is registered for that id.
func (f *workerFulfillmentStub) GetCluster(
	_ context.Context, id string,
) (*privatev1.Cluster, error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	cl, ok := f.clusters[id]
	if !ok {
		return nil, status.Errorf(codes.NotFound, "cluster %q not found", id)
	}
	return cloneCluster(cl), nil
}

// GetDiskImage records the call and returns a preloaded DiskImage (see AddDiskImage), or an
// error if none is registered for that id.
func (f *workerFulfillmentStub) GetDiskImage(
	_ context.Context, id string,
) (*privatev1.DiskImage, error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	f.getDiskImageCalls = append(f.getDiskImageCalls, id)
	di, ok := f.diskImages[id]
	if !ok {
		return nil, status.Errorf(codes.NotFound, "diskimage %q not found", id)
	}
	return cloneDiskImage(di), nil
}

func cloneCatalogItem(c *privatev1.BareMetalInstanceCatalogItem) *privatev1.BareMetalInstanceCatalogItem {
	return proto.Clone(c).(*privatev1.BareMetalInstanceCatalogItem)
}

// CreateBareMetalInstanceCatalogItem records the call, assigns an id (defaulting to metadata.name)
// and stores the catalog item. Returns AlreadyExists on duplicate names.
func (f *workerFulfillmentStub) CreateBareMetalInstanceCatalogItem(
	_ context.Context, obj *privatev1.BareMetalInstanceCatalogItem,
) (*privatev1.BareMetalInstanceCatalogItem, error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	f.createCatalogItemCalls = append(f.createCatalogItemCalls, cloneCatalogItem(obj))
	name := obj.GetMetadata().GetName()
	if name == "" {
		return nil, status.Error(codes.InvalidArgument, "catalogitem metadata.name is required")
	}
	stored := cloneCatalogItem(obj)
	id := stored.GetId()
	if id == "" {
		id = name
		stored.SetId(id)
	}
	if _, exists := f.catalogItems[id]; exists {
		return nil, status.Errorf(codes.AlreadyExists, "catalogitem %q already exists", id)
	}
	f.catalogItems[id] = stored
	return cloneCatalogItem(stored), nil
}

// ListBareMetalInstanceCatalogItems records the filter and returns copies of all stored catalog
// items, ordered by id for deterministic tests.
func (f *workerFulfillmentStub) ListBareMetalInstanceCatalogItems(
	_ context.Context, filter string,
) ([]*privatev1.BareMetalInstanceCatalogItem, error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	f.listCatalogItemCalls = append(f.listCatalogItemCalls, filter)
	ids := make([]string, 0, len(f.catalogItems))
	for id := range f.catalogItems {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	out := make([]*privatev1.BareMetalInstanceCatalogItem, 0, len(ids))
	for _, id := range ids {
		out = append(out, cloneCatalogItem(f.catalogItems[id]))
	}
	return out, nil
}

func cloneInstanceType(t *privatev1.BareMetalInstanceType) *privatev1.BareMetalInstanceType {
	return proto.Clone(t).(*privatev1.BareMetalInstanceType)
}

// GetBareMetalInstanceType records the call and returns a preloaded BareMetalInstanceType
// (see AddBareMetalInstanceType), or an error if none is registered for that id.
func (f *workerFulfillmentStub) GetBareMetalInstanceType(
	_ context.Context, id string,
) (*privatev1.BareMetalInstanceType, error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	f.getInstanceTypeCalls = append(f.getInstanceTypeCalls, id)
	it, ok := f.instanceTypes[id]
	if !ok {
		return nil, status.Errorf(codes.NotFound, "baremetalinstancetype %q not found", id)
	}
	return cloneInstanceType(it), nil
}

// --- Builder / control surface (test-only) ---

// AddBareMetalInstanceType preloads a BareMetalInstanceType so GetBareMetalInstanceType
// can return it (keyed by name from metadata).
func (f *workerFulfillmentStub) AddBareMetalInstanceType(it *privatev1.BareMetalInstanceType) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.instanceTypes[it.GetMetadata().GetName()] = cloneInstanceType(it)
}

// AddCluster preloads a Cluster so GetCluster can return it (keyed by id).
func (f *workerFulfillmentStub) AddCluster(cl *privatev1.Cluster) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.clusters[cl.GetId()] = cloneCluster(cl)
}

// AddClusterVersion preloads a ClusterVersion so GetClusterVersion can return it (keyed by id).
func (f *workerFulfillmentStub) AddClusterVersion(cv *privatev1.ClusterVersion) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.clusterVersions[cv.GetId()] = cloneCV(cv)
}

// AddDiskImage preloads a DiskImage so GetDiskImage can return it (keyed by id).
func (f *workerFulfillmentStub) AddDiskImage(di *privatev1.DiskImage) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.diskImages[di.GetId()] = cloneDiskImage(di)
}

// SetHostMAC records the allocated host MAC for a BMI so correlation tests can drive Agent
// matching. GetBareMetalInstance surfaces it through status.hardware.nics (OSAC-4203) — the
// same field the real inventory backend populates and the production MAC resolver reads — so
// tests exercise the default resolver rather than a side channel.
func (f *workerFulfillmentStub) SetHostMAC(bmiID, mac string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.hostMACs[bmiID] = mac
}

// SetListEmptyOnce simulates a BMI appearing between list-before-create and AlreadyExists re-list.
func (f *workerFulfillmentStub) SetListEmptyOnce() {
	f.SetListEmptyCalls(1)
}

// SetListEmptyCalls omits BMIs for count consecutive lists, to exercise fallback
// Gets and create races after the shared worker observation.
func (f *workerFulfillmentStub) SetListEmptyCalls(count int) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.listEmptyCalls = count
}

// SetListError makes subsequent BMI Lists fail without turning an outage into
// a successful empty observation. Nil clears the injection.
func (f *workerFulfillmentStub) SetListError(err error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.listErr = err
}

// SetGetError injects per-ID unknown or NotFound evidence for fallback Gets.
func (f *workerFulfillmentStub) SetGetError(id string, err error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if err == nil {
		delete(f.getErrors, id)
	} else {
		f.getErrors[id] = err
	}
}

// SetCreateError makes subsequent CreateBareMetalInstance calls return err (nil clears it).
func (f *workerFulfillmentStub) SetCreateError(err error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.createErr = err
}

// SetDeleteError makes subsequent DeleteBareMetalInstance calls return err (nil clears it).
func (f *workerFulfillmentStub) SetDeleteError(err error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.deleteErr = err
}

// SetPendingDeletion retains deleting objects until explicit fixture completion.
// This models API persistence, not provider or hardware release.
func (f *workerFulfillmentStub) SetPendingDeletion(pending bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.pendingDeletion = pending
}

// CompleteDeletion explicitly archives a test-owned pending object.
func (f *workerFulfillmentStub) CompleteDeletion(id string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	delete(f.bmis, id)
	delete(f.hostMACs, id)
}

// --- Recorded-call accessors (return copies under lock) ---

// CreateCalls returns copies of the BareMetalInstance objects passed to CreateBareMetalInstance.
func (f *workerFulfillmentStub) CreateCalls() []*privatev1.BareMetalInstance {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]*privatev1.BareMetalInstance, len(f.createCalls))
	for i, b := range f.createCalls {
		out[i] = cloneBMI(b)
	}
	return out
}

// DeleteCalls returns the ids passed to DeleteBareMetalInstance, in order.
func (f *workerFulfillmentStub) DeleteCalls() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.deleteCalls...)
}

// GetCalls returns the ids passed to GetBareMetalInstance, in order.
func (f *workerFulfillmentStub) GetCalls() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.getCalls...)
}

// ListCalls returns the filters passed to ListBareMetalInstances, in order.
func (f *workerFulfillmentStub) ListCalls() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.listCalls...)
}

// GetDiskImageCalls returns the ids passed to GetDiskImage, in order.
func (f *workerFulfillmentStub) GetDiskImageCalls() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.getDiskImageCalls...)
}

// CreateCatalogItemCalls returns copies of the catalog items passed to CreateBareMetalInstanceCatalogItem.
func (f *workerFulfillmentStub) CreateCatalogItemCalls() []*privatev1.BareMetalInstanceCatalogItem {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]*privatev1.BareMetalInstanceCatalogItem, len(f.createCatalogItemCalls))
	for i, c := range f.createCatalogItemCalls {
		out[i] = cloneCatalogItem(c)
	}
	return out
}

// ListCatalogItemCalls returns the filters passed to ListBareMetalInstanceCatalogItems, in order.
func (f *workerFulfillmentStub) ListCatalogItemCalls() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.listCatalogItemCalls...)
}

// GetInstanceTypeCalls returns the ids passed to GetBareMetalInstanceType, in order.
func (f *workerFulfillmentStub) GetInstanceTypeCalls() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.getInstanceTypeCalls...)
}

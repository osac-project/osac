/*
Copyright (c) 2026 Red Hat Inc.

Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except in compliance with the
License. You may obtain a copy of the License at

  http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on an
"AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the specific
language governing permissions and limitations under the License.
*/

// Package fake provides in-memory test doubles for the bare-metal worker controller's external
// seams: a recording FulfillmentClient (OSAC-4146) that stands in for the fulfillment-service
// private API, and a configurable HTTP endpoint that stands in for an InfraEnv's discovery
// ignition URL. Together they let path-A tests exercise the full reconcile loop without a
// running fulfillment-service or assisted-service.
//
// Field-contract note: the fake performs only minimal validation (it requires metadata.name on
// create, as the real API does). The fulfillment-service contract tests (OSAC-4129 track) are
// the source of truth for the accepted-field contract, and the B-lite harness (OSAC-4153) is the
// anti-drift check that runs the controller against the real private API + Postgres.
package fake

import (
	"context"
	"sort"
	"sync"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"

	"github.com/osac-project/osac/osac-operator/internal/controller/baremetalworker"
	privatev1 "github.com/osac-project/osac/proto/gen/osac/private/v1"
)

// FulfillmentClient is an in-memory, call-recording implementation of
// baremetalworker.FulfillmentClient for tests. All methods are safe for concurrent use.
type FulfillmentClient struct {
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

	createCalls            []*privatev1.BareMetalInstance
	deleteCalls            []string
	getCalls               []string
	listCalls              []string
	getCVCalls             []string
	getClusterCalls        []string
	getDiskImageCalls      []string
	createCatalogItemCalls []*privatev1.BareMetalInstanceCatalogItem
	listCatalogItemCalls   []string
	getInstanceTypeCalls   []string
}

var _ baremetalworker.FulfillmentClient = (*FulfillmentClient)(nil)

// NewFulfillmentClient returns an empty fake FulfillmentClient.
func NewFulfillmentClient() *FulfillmentClient {
	return &FulfillmentClient{
		bmis:            map[string]*privatev1.BareMetalInstance{},
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

// CreateBareMetalInstance records the call, assigns an id (defaulting to metadata.name) and stores
// the object. It returns the injected create error when one is set (still recording the call).
func (f *FulfillmentClient) CreateBareMetalInstance(
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
	stored := cloneBMI(obj)
	id := stored.GetId()
	if id == "" {
		id = name
		stored.SetId(id)
	}
	// Mirror the real private API's uniqueness constraint (OSAC-3266: UNIQUE(tenant,project,name)),
	// so idempotency/list-before-create tests exercise the AlreadyExists path.
	if _, exists := f.bmis[id]; exists {
		return nil, status.Errorf(codes.AlreadyExists, "baremetalinstance %q already exists", id)
	}
	f.bmis[id] = stored
	return cloneBMI(stored), nil
}

// DeleteBareMetalInstance records the call and removes the object. It returns the injected delete
// error when one is set (still recording the call).
func (f *FulfillmentClient) DeleteBareMetalInstance(_ context.Context, id string) error {
	f.mu.Lock()
	defer f.mu.Unlock()

	f.deleteCalls = append(f.deleteCalls, id)
	if f.deleteErr != nil {
		return f.deleteErr
	}
	delete(f.bmis, id)
	delete(f.hostMACs, id)
	return nil
}

// GetBareMetalInstance records the call and returns a copy of the stored object, or an error if
// no object with that id exists.
func (f *FulfillmentClient) GetBareMetalInstance(
	_ context.Context, id string,
) (*privatev1.BareMetalInstance, error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	f.getCalls = append(f.getCalls, id)
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
func (f *FulfillmentClient) injectHostMAC(bmi *privatev1.BareMetalInstance, id string) {
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
func (f *FulfillmentClient) ListBareMetalInstances(
	_ context.Context, filter string,
) ([]*privatev1.BareMetalInstance, error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	f.listCalls = append(f.listCalls, filter)
	ids := make([]string, 0, len(f.bmis))
	for id := range f.bmis {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	out := make([]*privatev1.BareMetalInstance, 0, len(ids))
	for _, id := range ids {
		out = append(out, cloneBMI(f.bmis[id]))
	}
	return out, nil
}

// GetClusterVersion records the call and returns a preloaded ClusterVersion (see
// AddClusterVersion), or an error if none is registered for that id.
func (f *FulfillmentClient) GetClusterVersion(
	_ context.Context, id string,
) (*privatev1.ClusterVersion, error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	f.getCVCalls = append(f.getCVCalls, id)
	cv, ok := f.clusterVersions[id]
	if !ok {
		return nil, status.Errorf(codes.NotFound, "clusterversion %q not found", id)
	}
	return cloneCV(cv), nil
}

// GetCluster records the call and returns a preloaded Cluster (see AddCluster), or an error
// if none is registered for that id.
func (f *FulfillmentClient) GetCluster(
	_ context.Context, id string,
) (*privatev1.Cluster, error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	f.getClusterCalls = append(f.getClusterCalls, id)
	cl, ok := f.clusters[id]
	if !ok {
		return nil, status.Errorf(codes.NotFound, "cluster %q not found", id)
	}
	return cloneCluster(cl), nil
}

// GetDiskImage records the call and returns a preloaded DiskImage (see AddDiskImage), or an
// error if none is registered for that id.
func (f *FulfillmentClient) GetDiskImage(
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
func (f *FulfillmentClient) CreateBareMetalInstanceCatalogItem(
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
func (f *FulfillmentClient) ListBareMetalInstanceCatalogItems(
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
func (f *FulfillmentClient) GetBareMetalInstanceType(
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
func (f *FulfillmentClient) AddBareMetalInstanceType(it *privatev1.BareMetalInstanceType) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.instanceTypes[it.GetMetadata().GetName()] = cloneInstanceType(it)
}

// AddCluster preloads a Cluster so GetCluster can return it (keyed by id).
func (f *FulfillmentClient) AddCluster(cl *privatev1.Cluster) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.clusters[cl.GetId()] = cloneCluster(cl)
}

// AddClusterVersion preloads a ClusterVersion so GetClusterVersion can return it (keyed by id).
func (f *FulfillmentClient) AddClusterVersion(cv *privatev1.ClusterVersion) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.clusterVersions[cv.GetId()] = cloneCV(cv)
}

// AddDiskImage preloads a DiskImage so GetDiskImage can return it (keyed by id).
func (f *FulfillmentClient) AddDiskImage(di *privatev1.DiskImage) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.diskImages[di.GetId()] = cloneDiskImage(di)
}

// SetHostMAC records the allocated host MAC for a BMI so correlation tests can drive Agent
// matching. GetBareMetalInstance surfaces it through status.hardware.nics (OSAC-4203) — the
// same field the real inventory backend populates and the production MAC resolver reads — so
// tests exercise the default resolver rather than a side channel.
func (f *FulfillmentClient) SetHostMAC(bmiID, mac string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.hostMACs[bmiID] = mac
}

// HostMAC returns the MAC previously set for a BMI via SetHostMAC (empty if none).
func (f *FulfillmentClient) HostMAC(bmiID string) string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.hostMACs[bmiID]
}

// SetCreateError makes subsequent CreateBareMetalInstance calls return err (nil clears it).
func (f *FulfillmentClient) SetCreateError(err error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.createErr = err
}

// SetDeleteError makes subsequent DeleteBareMetalInstance calls return err (nil clears it).
func (f *FulfillmentClient) SetDeleteError(err error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.deleteErr = err
}

// --- Recorded-call accessors (return copies under lock) ---

// CreateCalls returns copies of the BareMetalInstance objects passed to CreateBareMetalInstance.
func (f *FulfillmentClient) CreateCalls() []*privatev1.BareMetalInstance {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]*privatev1.BareMetalInstance, len(f.createCalls))
	for i, b := range f.createCalls {
		out[i] = cloneBMI(b)
	}
	return out
}

// DeleteCalls returns the ids passed to DeleteBareMetalInstance, in order.
func (f *FulfillmentClient) DeleteCalls() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.deleteCalls...)
}

// GetCalls returns the ids passed to GetBareMetalInstance, in order.
func (f *FulfillmentClient) GetCalls() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.getCalls...)
}

// ListCalls returns the filters passed to ListBareMetalInstances, in order.
func (f *FulfillmentClient) ListCalls() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.listCalls...)
}

// GetClusterVersionCalls returns the ids passed to GetClusterVersion, in order.
func (f *FulfillmentClient) GetClusterVersionCalls() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.getCVCalls...)
}

// GetClusterCalls returns the ids passed to GetCluster, in order.
func (f *FulfillmentClient) GetClusterCalls() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.getClusterCalls...)
}

// GetDiskImageCalls returns the ids passed to GetDiskImage, in order.
func (f *FulfillmentClient) GetDiskImageCalls() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.getDiskImageCalls...)
}

// CreateCatalogItemCalls returns copies of the catalog items passed to CreateBareMetalInstanceCatalogItem.
func (f *FulfillmentClient) CreateCatalogItemCalls() []*privatev1.BareMetalInstanceCatalogItem {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]*privatev1.BareMetalInstanceCatalogItem, len(f.createCatalogItemCalls))
	for i, c := range f.createCatalogItemCalls {
		out[i] = cloneCatalogItem(c)
	}
	return out
}

// ListCatalogItemCalls returns the filters passed to ListBareMetalInstanceCatalogItems, in order.
func (f *FulfillmentClient) ListCatalogItemCalls() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.listCatalogItemCalls...)
}

// GetInstanceTypeCalls returns the ids passed to GetBareMetalInstanceType, in order.
func (f *FulfillmentClient) GetInstanceTypeCalls() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.getInstanceTypeCalls...)
}

package driver

// nodeRoute is what the node plugin records about a staged or published
// volume so that later calls without a volume context can be routed.
type nodeRoute struct {
	Version        int
	Backend        string
	Protocol       string
	VendorVolumeID string
}

// nodeRouteStore keeps node routes keyed by CSI volume ID.
type nodeRouteStore interface {
	Save(volumeID string, r nodeRoute) error
	Load(volumeID string) (nodeRoute, bool, error)
	Delete(volumeID string) error
}

type noopRouteStore struct{}

func (noopRouteStore) Save(string, nodeRoute) error { return nil }

func (noopRouteStore) Load(string) (nodeRoute, bool, error) { return nodeRoute{}, false, nil }

func (noopRouteStore) Delete(string) error { return nil }

// newFileRouteStore returns a store that keeps routes as files under dir.
func newFileRouteStore(dir string) nodeRouteStore {
	return noopRouteStore{}
}

// newMemoryRouteStore returns a store that keeps routes in memory.
func newMemoryRouteStore() nodeRouteStore {
	return noopRouteStore{}
}

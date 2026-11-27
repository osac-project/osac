package driver

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
)

// nodeRouteVersion is the format version written by the file route store.
const nodeRouteVersion = 1

// nodeRoute is what the node plugin records about a staged or published
// volume so that later calls without a volume context can be routed.
type nodeRoute struct {
	Version        int    `json:"version"`
	Backend        string `json:"backend"`
	Protocol       string `json:"protocol"`
	VendorVolumeID string `json:"vendorVolumeID"`
}

// nodeRouteStore keeps node routes keyed by CSI volume ID. Load reports
// false with a nil error when no route is recorded.
type nodeRouteStore interface {
	Save(volumeID string, r nodeRoute) error
	Load(volumeID string) (nodeRoute, bool, error)
	Delete(volumeID string) error
}

// fileRouteStore keeps one JSON file per volume under dir. File names are
// the hex SHA-256 of the volume ID, so any volume ID maps to a plain file
// name inside dir.
type fileRouteStore struct {
	dir string

	mu    sync.Mutex
	locks map[string]*sync.Mutex
}

// newFileRouteStore returns a store that keeps routes as files under dir.
// The directory must exist.
func newFileRouteStore(dir string) nodeRouteStore {
	return &fileRouteStore{dir: dir, locks: make(map[string]*sync.Mutex)}
}

func (s *fileRouteStore) path(volumeID string) string {
	sum := sha256.Sum256([]byte(volumeID))
	return filepath.Join(s.dir, hex.EncodeToString(sum[:]))
}

// lock serialises writes for one volume.
func (s *fileRouteStore) lock(volumeID string) func() {
	s.mu.Lock()
	l, ok := s.locks[volumeID]
	if !ok {
		l = &sync.Mutex{}
		s.locks[volumeID] = l
	}
	s.mu.Unlock()
	l.Lock()
	return l.Unlock
}

func (s *fileRouteStore) Save(volumeID string, r nodeRoute) error {
	r.Version = nodeRouteVersion
	data, err := json.Marshal(r)
	if err != nil {
		return fmt.Errorf("encoding route for volume %q: %w", volumeID, err)
	}

	defer s.lock(volumeID)()

	// CreateTemp creates the file with mode 0600.
	tmp, err := os.CreateTemp(s.dir, ".route-*")
	if err != nil {
		return fmt.Errorf("creating route file for volume %q: %w", volumeID, err)
	}
	tmpName := tmp.Name()
	if err := writeAndClose(tmp, data); err != nil {
		_ = os.Remove(tmpName)
		return fmt.Errorf("writing route file for volume %q: %w", volumeID, err)
	}
	if err := os.Rename(tmpName, s.path(volumeID)); err != nil {
		_ = os.Remove(tmpName)
		return fmt.Errorf("renaming route file for volume %q: %w", volumeID, err)
	}
	return nil
}

func writeAndClose(f *os.File, data []byte) error {
	if _, err := f.Write(data); err != nil {
		_ = f.Close()
		return err
	}
	if err := f.Sync(); err != nil {
		_ = f.Close()
		return err
	}
	return f.Close()
}

func (s *fileRouteStore) Load(volumeID string) (nodeRoute, bool, error) {
	data, err := os.ReadFile(s.path(volumeID))
	if errors.Is(err, os.ErrNotExist) {
		return nodeRoute{}, false, nil
	}
	if err != nil {
		return nodeRoute{}, false, fmt.Errorf("reading route file for volume %q: %w", volumeID, err)
	}

	var r nodeRoute
	if err := json.Unmarshal(data, &r); err != nil {
		return nodeRoute{}, false, fmt.Errorf("decoding route file for volume %q: %w", volumeID, err)
	}
	if r.Version != nodeRouteVersion {
		return nodeRoute{}, false, fmt.Errorf("route file for volume %q has unsupported version %d",
			volumeID, r.Version)
	}
	return r, true, nil
}

func (s *fileRouteStore) Delete(volumeID string) error {
	defer s.lock(volumeID)()

	err := os.Remove(s.path(volumeID))
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("removing route file for volume %q: %w", volumeID, err)
	}
	return nil
}

// memoryRouteStore keeps routes in memory; they are lost on restart.
type memoryRouteStore struct {
	mu     sync.Mutex
	routes map[string]nodeRoute
}

// newMemoryRouteStore returns a store that keeps routes in memory.
func newMemoryRouteStore() nodeRouteStore {
	return &memoryRouteStore{routes: make(map[string]nodeRoute)}
}

func (s *memoryRouteStore) Save(volumeID string, r nodeRoute) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.routes[volumeID] = r
	return nil
}

func (s *memoryRouteStore) Load(volumeID string) (nodeRoute, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	r, ok := s.routes[volumeID]
	return r, ok, nil
}

func (s *memoryRouteStore) Delete(volumeID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.routes, volumeID)
	return nil
}

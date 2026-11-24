package gitops

import (
	"sync"

	"github.com/go-git/go-git/v5/config"
	"github.com/go-git/go-git/v5/plumbing"
	"github.com/go-git/go-git/v5/plumbing/format/index"
	"github.com/go-git/go-git/v5/plumbing/storer"
	"github.com/go-git/go-git/v5/storage"
	"github.com/go-git/go-git/v5/storage/memory"
)

// lockedStorage is go-git's in-memory storage with every call under one
// lock, so that snapshots read objects while a fetch or a commit writes new
// ones. memory.Storage keeps plain maps, and go-git writes to them in the
// middle of a fetch, as each object arrives from the network: a lock around
// the whole fetch would make every read wait for the remote. With the lock
// on each call instead, a read waits at most for one map access.
//
// The lock makes each call safe, not a sequence of calls. That is enough for
// snapshots, which read immutable objects by hash: an object is complete
// before SetEncodedObject publishes it, and nothing changes it afterwards.
// The references, the index and the config, which go-git reads, changes and
// writes back, are used only by the operation that holds RepoWriter.op.
//
// It implements storage.Storer and nothing else, on purpose: the optional
// interfaces memory.Storage also has (Transactioner, LooseObjectStorer,
// PackedObjectStorer) reach its maps without this lock, and the fetch, push
// and commit paths RepoWriter uses work without them.
type lockedStorage struct {
	mu sync.RWMutex
	s  *memory.Storage
}

var _ storage.Storer = (*lockedStorage)(nil)

func newLockedStorage() *lockedStorage {
	return &lockedStorage{s: memory.NewStorage()}
}

func (l *lockedStorage) NewEncodedObject() plumbing.EncodedObject {
	return l.s.NewEncodedObject()
}

func (l *lockedStorage) SetEncodedObject(obj plumbing.EncodedObject) (plumbing.Hash, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.s.SetEncodedObject(obj)
}

func (l *lockedStorage) EncodedObject(t plumbing.ObjectType, h plumbing.Hash) (plumbing.EncodedObject, error) {
	l.mu.RLock()
	defer l.mu.RUnlock()
	return l.s.EncodedObject(t, h)
}

// IterEncodedObjects iterates over a copy: memory.Storage collects the
// objects into a slice before it returns.
func (l *lockedStorage) IterEncodedObjects(t plumbing.ObjectType) (storer.EncodedObjectIter, error) {
	l.mu.RLock()
	defer l.mu.RUnlock()
	return l.s.IterEncodedObjects(t)
}

func (l *lockedStorage) HasEncodedObject(h plumbing.Hash) error {
	l.mu.RLock()
	defer l.mu.RUnlock()
	return l.s.HasEncodedObject(h)
}

func (l *lockedStorage) EncodedObjectSize(h plumbing.Hash) (int64, error) {
	l.mu.RLock()
	defer l.mu.RUnlock()
	return l.s.EncodedObjectSize(h)
}

func (l *lockedStorage) AddAlternate(remote string) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.s.AddAlternate(remote)
}

func (l *lockedStorage) SetReference(ref *plumbing.Reference) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.s.SetReference(ref)
}

func (l *lockedStorage) CheckAndSetReference(ref, old *plumbing.Reference) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.s.CheckAndSetReference(ref, old)
}

func (l *lockedStorage) Reference(n plumbing.ReferenceName) (*plumbing.Reference, error) {
	l.mu.RLock()
	defer l.mu.RUnlock()
	return l.s.Reference(n)
}

// IterReferences iterates over a copy, as IterEncodedObjects does.
func (l *lockedStorage) IterReferences() (storer.ReferenceIter, error) {
	l.mu.RLock()
	defer l.mu.RUnlock()
	return l.s.IterReferences()
}

func (l *lockedStorage) RemoveReference(n plumbing.ReferenceName) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.s.RemoveReference(n)
}

func (l *lockedStorage) CountLooseRefs() (int, error) {
	l.mu.RLock()
	defer l.mu.RUnlock()
	return l.s.CountLooseRefs()
}

func (l *lockedStorage) PackRefs() error {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.s.PackRefs()
}

func (l *lockedStorage) SetShallow(commits []plumbing.Hash) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.s.SetShallow(commits)
}

func (l *lockedStorage) Shallow() ([]plumbing.Hash, error) {
	l.mu.RLock()
	defer l.mu.RUnlock()
	return l.s.Shallow()
}

func (l *lockedStorage) SetIndex(idx *index.Index) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.s.SetIndex(idx)
}

// Index takes the write lock: memory.Storage creates the index on first use.
func (l *lockedStorage) Index() (*index.Index, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.s.Index()
}

// Config takes the write lock: memory.Storage creates the config on first use.
func (l *lockedStorage) Config() (*config.Config, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.s.Config()
}

func (l *lockedStorage) SetConfig(cfg *config.Config) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.s.SetConfig(cfg)
}

func (l *lockedStorage) Module(name string) (storage.Storer, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.s.Module(name)
}

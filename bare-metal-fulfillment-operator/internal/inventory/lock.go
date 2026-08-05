/*
Copyright 2026.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package inventory

import (
	"sync"
)

var (
	// hostLocks is a process-global map that grows monotonically and retains mutexes
	// for every observed hostID. The map is never pruned in the current implementation.
	// sync.Map is used for its write-once, read-many optimization pattern.
	hostLocks sync.Map
)

// TryLock attempts to acquire a lock for the given host ID.
// Returns true if lock was acquired, false otherwise.
// Locks are per-hostID, so different host IDs do not contend.
// Callers must ensure they call Unlock for every successful TryLock to avoid leaking locks.
func TryLock(hostID string) bool {
	mu, _ := hostLocks.LoadOrStore(hostID, &sync.Mutex{})
	return mu.(*sync.Mutex).TryLock()
}

// Unlock releases the lock for the given host ID.
// PRECONDITION: Unlock must only be called if TryLock previously returned true for the same hostID.
// Calling Unlock without a corresponding successful TryLock will panic with "sync: unlock of unlocked mutex".
func Unlock(hostID string) {
	if mu, ok := hostLocks.Load(hostID); ok {
		mu.(*sync.Mutex).Unlock()
	}
}

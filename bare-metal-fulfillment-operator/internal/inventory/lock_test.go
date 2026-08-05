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
	"testing"
)

func TestTryLock(t *testing.T) {
	t.Run("TryLock returns false when lock is already held", func(t *testing.T) {
		hostID := "test-host-1"

		// First TryLock should succeed
		if !TryLock(hostID) {
			t.Fatal("First TryLock should succeed")
		}
		defer Unlock(hostID)

		// Second TryLock should fail (lock is held)
		if TryLock(hostID) {
			t.Fatal("Second TryLock should fail when lock is held")
			Unlock(hostID) // Clean up if test fails
		}
	})

	t.Run("Unlock allows subsequent TryLock to succeed", func(t *testing.T) {
		hostID := "test-host-2"

		// Acquire lock
		if !TryLock(hostID) {
			t.Fatal("First TryLock should succeed")
		}

		// Release lock
		Unlock(hostID)

		// Should be able to acquire again
		if !TryLock(hostID) {
			t.Fatal("TryLock should succeed after Unlock")
		}
		defer Unlock(hostID)
	})

	t.Run("Locks are per-hostID and do not contend", func(t *testing.T) {
		hostID1 := "test-host-3"
		hostID2 := "test-host-4"

		// Acquire lock for hostID1
		if !TryLock(hostID1) {
			t.Fatal("TryLock for hostID1 should succeed")
		}
		defer Unlock(hostID1)

		// Should be able to acquire lock for hostID2 independently
		if !TryLock(hostID2) {
			t.Fatal("TryLock for hostID2 should succeed (different hostID)")
		}
		defer Unlock(hostID2)
	})

	t.Run("Multiple calls to TryLock and Unlock maintain invariants", func(t *testing.T) {
		hostID := "test-host-5"

		// Acquire and release multiple times
		for i := 0; i < 3; i++ {
			if !TryLock(hostID) {
				t.Fatalf("TryLock iteration %d should succeed", i)
			}
			Unlock(hostID)
		}
	})
}

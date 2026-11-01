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

package networktask

import (
	"context"
	"sync"

	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/osac-project/osac/osac-operator/api/v1alpha1"
)

// vniAcceptanceLock is the vnLocks key of the operator-wide VNI acceptance
// lock; it cannot collide with a VirtualNetwork API ID, which is a UUID.
const vniAcceptanceLock = "osac-vni-acceptance"

// AcceptSegment records the network segment returned by the Fabric Manager.
// On first acceptance it checks the L2 VNI against every other VirtualNetwork
// and Subnet (CheckVNIUnique, read through reader), so call it under
// Request.SerializeVNI; an accepted segment never changes afterwards.
func AcceptSegment(ctx context.Context, reader client.Reader, subnet *v1alpha1.Subnet, networkType string, result Result) error {
	segment, err := ParseNetworkSegment(result, networkType)
	if err != nil || segment == nil {
		return err
	}
	if current := subnet.Status.NetworkSegment; current != nil {
		if *segment != *current {
			return Reject("network segment changed from the accepted value")
		}
		return nil
	}
	if err := CheckVNIUnique(ctx, reader, subnet.Namespace, string(subnet.UID), segment.L2VNI, VNIKindL2); err != nil {
		return err
	}
	subnet.Status.NetworkSegment = segment
	return nil
}

// keyedMutex retains a key only while it has a holder or waiters.
type keyedMutex struct {
	mu    sync.Mutex
	locks map[string]*keyedMutexEntry
}

type keyedMutexEntry struct {
	mu   sync.Mutex
	refs int
}

func (k *keyedMutex) acquire(key string) func() {
	k.mu.Lock()
	if k.locks == nil {
		k.locks = map[string]*keyedMutexEntry{}
	}
	m, ok := k.locks[key]
	if !ok {
		m = &keyedMutexEntry{}
		k.locks[key] = m
	}
	// Count the reference before waiting, so unlock cannot remove a key that
	// another goroutine is about to acquire.
	m.refs++
	k.mu.Unlock()
	m.mu.Lock()
	return func() {
		m.mu.Unlock()
		k.mu.Lock()
		defer k.mu.Unlock()
		m.refs--
		if m.refs == 0 {
			delete(k.locks, key)
		}
	}
}

// SubnetSlot serializes Subnet tasks per VirtualNetwork (contract §4.7
// Concurrency). Set it on a Subnet task's Request: before the launch the
// engine takes the VirtualNetwork's lock, lists the Subnets from the API
// server with its namespace and VirtualNetwork label. The Running status
// reserves the slot after its conditional write releases the lock, before AAP
// calls. The same key serializes acceptance through its resulting status write.
type SubnetSlot struct {
	// VirtualNetworkID is the API ID of the Subnet's VirtualNetwork.
	VirtualNetworkID string
}

// slotHeld reports whether another Subnet of the VirtualNetwork holds the
// slot through a Running task.
func slotHeld(subnet client.Object, others []v1alpha1.Subnet, vnID string) bool {
	for i := range others {
		other := &others[i]
		if other.UID == subnet.GetUID() || other.Spec.VirtualNetwork != vnID {
			continue
		}
		for _, job := range other.Status.Jobs {
			if job.State != v1alpha1.NetworkJobStateRunning {
				continue
			}
			return true
		}
	}
	return false
}

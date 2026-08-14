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

package controller

import (
	"context"
	"fmt"
	"sync/atomic"
)

// MockVendorProvisioner is a test-only VendorProvisioner that succeeds
// immediately with deterministic IDs. It tracks call counts for test
// assertions. Set CreateErr or DeleteErr to simulate vendor failures.
type MockVendorProvisioner struct {
	createCount atomic.Int64
	deleteCount atomic.Int64

	// CreateErr, when non-nil, is returned by CreateVolume instead of
	// succeeding. Allows tests to simulate vendor failures.
	CreateErr error

	// DeleteErr, when non-nil, is returned by DeleteVolume instead of
	// succeeding.
	DeleteErr error
}

// NewMockVendorProvisioner creates a mock provisioner that succeeds by default.
func NewMockVendorProvisioner() *MockVendorProvisioner {
	return &MockVendorProvisioner{}
}

// CreateVolume returns a deterministic vendor volume ID composed of
// "mock-" plus a monotonic counter. Backend and protocol are fixed
// strings suitable for test assertions.
func (m *MockVendorProvisioner) CreateVolume(_ context.Context, req VendorCreateVolumeRequest) (VendorCreateVolumeResponse, error) {
	n := m.createCount.Add(1)
	if m.CreateErr != nil {
		return VendorCreateVolumeResponse{}, m.CreateErr
	}
	return VendorCreateVolumeResponse{
		VendorVolumeID: fmt.Sprintf("mock-%d", n),
		Backend:        "mock-backend",
		Protocol:       "Block",
	}, nil
}

// DeleteVolume records the call and returns DeleteErr (nil by default).
func (m *MockVendorProvisioner) DeleteVolume(_ context.Context, _ VendorDeleteVolumeRequest) error {
	m.deleteCount.Add(1)
	return m.DeleteErr
}

// CreateCallCount returns the number of times CreateVolume was called.
func (m *MockVendorProvisioner) CreateCallCount() int64 {
	return m.createCount.Load()
}

// DeleteCallCount returns the number of times DeleteVolume was called.
func (m *MockVendorProvisioner) DeleteCallCount() int64 {
	return m.deleteCount.Load()
}

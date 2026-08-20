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

// Package inventory provides implementations of inventory clients
package inventory

import (
	"context"

	"github.com/osac-project/osac/bare-metal-fulfillment-operator/internal/baremetalhost"
)

// Config is a struct that holds info needed to create a new client implementation
type Config struct {
	Name       string                 `json:"name"`
	Type       string                 `json:"type"`
	Options    map[string]any         `json:"options"`
	HostClass  string                 `json:"hostClass"`
	BMHManager *baremetalhost.Manager `json:"-"`
}

// Host is the common return type all clients must use
type Host struct {
	BareMetalPoolID     string
	BareMetalInstanceID string
	InventoryHostID     string
	Name                string
	HostType            string
	HostClass           string
	ProvisionState      string
	ManagedBy           string
}

// HostNIC represents a physical network interface on an inventory host.
type HostNIC struct {
	// MAC is the hardware MAC address, lowercased (e.g. "aa:bb:cc:dd:ee:ff").
	MAC string
}

// Client interface for inventory implementations
type Client interface {
	// FindFreeHost returns a host with matching fields that is not already assigned
	FindFreeHost(ctx context.Context, matchExpressions map[string]string) (*Host, error)

	// AssignHost attempts to mark a host as assigned
	// bareMetalInstanceID must be non-empty; implementations should reject empty values.
	// Returns the assigned host if successful, or nil if the host is unavailable
	// Returns an error only for backend failures
	// This operation must be strongly consistent: it only returns when the inventory state reflects the assignment
	AssignHost(ctx context.Context, inventoryHostID string, bareMetalInstanceID string, labels map[string]string) (*Host, error)

	// UnassignHost updates the host by undoing the assign operation
	UnassignHost(ctx context.Context, inventoryHostID string, labels []string) error

	// GetHostNICs returns the physical network interfaces for an allocated host.
	// Returns an empty slice (not an error) when the backend has no NIC data for the host.
	GetHostNICs(ctx context.Context, inventoryHostID string) ([]HostNIC, error)
}

// NewClientFunc is a function that creates a new inventory client from config
type NewClientFunc func(ctx context.Context, cfg *Config) (Client, error)

// newClientFuncs is a registry of available inventory client implementations
var newClientFuncs = make(map[string]NewClientFunc)

// NewClient creates a new inventory client based on the config type
func NewClient(ctx context.Context, cfg *Config) (Client, error) {
	newClientFunc, ok := newClientFuncs[cfg.Type]
	if !ok {
		return nil, nil
	}

	return newClientFunc(ctx, cfg)
}

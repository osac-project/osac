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

// Package credentials defines the BMC credential contracts shared by inventory
// backends and credential stores.
//
// A Reader resolves credentials for a host from a source. A Writer persists
// credentials for a host in a destination. A Reference carries the domain
// identifiers they exchange; it never contains a destination-specific storage
// key.
package credentials

import (
	"context"
	"errors"
)

var (
	// ErrResolutionFailed identifies credentials that are missing or invalid
	// and will not succeed if the caller retries without a configuration change.
	ErrResolutionFailed = errors.New("BMC credential resolution failed")
	// ErrCredentialsNotFound identifies a source with no matching credentials.
	ErrCredentialsNotFound = errors.New("BMC credentials not found")
	// ErrAmbiguousCredentials identifies a source with multiple matching credentials.
	ErrAmbiguousCredentials = errors.New("multiple BMC credentials found")
)

// BMC contains the username and password used to connect to a BMC.
type BMC struct {
	Username []byte
	Password []byte
}

// Reader retrieves BMC credentials for a host from its configured source.
type Reader interface {
	// GetCredentials returns the credentials for the host identified by its
	// inventory host ID.
	GetCredentials(ctx context.Context, hostID string) (*BMC, error)

	// Close releases resources held by the reader.
	Close() error
}

// Reference identifies the host a set of BMC credentials belongs to and the
// BareMetalInstance that owns them.
//
// It is the contract between the layer that produces credentials (an inventory
// backend) and a Writer that stores them. It deliberately carries domain
// identifiers only: never a destination-specific storage key. Resolving that
// key is the Writer implementation's job.
type Reference struct {
	// HostID is the host's identity in the inventory layer. Writers map it to
	// their own storage key; callers must never pass a storage key here.
	HostID string

	// BMIID is the UID of the BareMetalInstance that owns the credentials.
	// Writers use it to reject changes and deletions by any other owner.
	BMIID string
}

// Writer stores and deletes the BMC credentials for a host in its configured
// destination. Callers identify the host and its owner; they never choose the
// destination's storage key.
type Writer interface {
	// Create creates or updates the credentials for ref.HostID and records
	// ref.BMIID as their owner.
	Create(ctx context.Context, ref Reference, bmc BMC) error

	// Delete removes the credentials for ref.HostID, but only if ref.BMIID is
	// still their owner.
	Delete(ctx context.Context, ref Reference) error
}

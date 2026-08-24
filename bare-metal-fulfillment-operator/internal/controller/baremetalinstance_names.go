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
	"fmt"
	"time"

	"k8s.io/apimachinery/pkg/runtime/schema"

	"github.com/osac-project/osac/osac-operator/pkg/provisioning"

	"github.com/osac-project/osac/bare-metal-fulfillment-operator/internal/shared"
)

const (
	// BareMetalPoolIDLabel is the BareMetalPool ID label to put in the inventory backend
	BareMetalPoolIDLabel = "bareMetalPoolId"

	// NoFreeHostsPollIntervalDuration is the default polling interval when no free hosts are available
	DefaultNoFreeHostsPollIntervalDuration = 30 * time.Second

	// TryLockFailPollIntervalDuration is the default polling interval when lock acquisition fails
	DefaultTryLockFailPollIntervalDuration = 1 * time.Second

	// DefaultManagementRecheckIntervalDuration is the default interval to recheck management operations (power state, etc.)
	DefaultManagementRecheckIntervalDuration = 10 * time.Second

	// DefaultProvisionPollIntervalDuration is the default interval to poll provisioning job status
	DefaultProvisionPollIntervalDuration = provisioning.DefaultStatusPollInterval

	autoCreatedLabel         = shared.OsacPrefix + "/auto-created"
	autoCreatedForLabel      = shared.OsacPrefix + "/auto-created-for"
	bareMetalInstanceIDLabel = shared.OsacPrefix + "/baremetalinstance-uuid"
)

var (
	// BareMetalInstanceInventoryFinalizer is the finalizer added to BareMetalInstance resources for inventory cleanup
	BareMetalInstanceInventoryFinalizer string = fmt.Sprintf("%s/inventory", shared.OsacPrefix)

	// BareMetalInstanceManagementFinalizer is the finalizer for management operations
	BareMetalInstanceManagementFinalizer string = fmt.Sprintf("%s/baremetalinstance", shared.OsacPrefix)

	// BareMetalInstanceNetworkingFinalizer is the finalizer for network attachment cleanup
	BareMetalInstanceNetworkingFinalizer string = fmt.Sprintf("%s/baremetalinstance-networking", shared.OsacPrefix)

	// BareMetalInstanceCleanupFinalizer is the finalizer for auto-provisioned ExternalIP cleanup
	BareMetalInstanceCleanupFinalizer string = fmt.Sprintf("%s/baremetalinstance-cleanup", shared.OsacPrefix)

	externalIPAttachmentGVK = schema.GroupVersionKind{
		Group: "osac.openshift.io", Version: "v1alpha1", Kind: "ExternalIPAttachment",
	}
	externalIPGVK = schema.GroupVersionKind{
		Group: "osac.openshift.io", Version: "v1alpha1", Kind: "ExternalIP",
	}
)

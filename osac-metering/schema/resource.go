/*
Copyright (c) 2026 Red Hat, Inc.

Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except
in compliance with the License. You may obtain a copy of the License at

  http://www.apache.org/licenses/LICENSE-2.0
*/

package schema

import "slices"

// Resource type constants identify OSAC resource types in CloudEvent payloads.
const (
	ResourceTypeComputeInstance   = "compute_instance"
	ResourceTypeClusterOrder      = "cluster_order"
	ResourceTypeExternalIP        = "external_ip"
	ResourceTypeNATGateway        = "nat_gateway"
	ResourceTypeBareMetalInstance = "bare_metal_instance"
	ResourceTypeVolume            = "volume"
)

// resourceTypes lists the resource_type values events carry, sorted.
var resourceTypes = []string{
	ResourceTypeBareMetalInstance,
	ResourceTypeClusterOrder,
	ResourceTypeComputeInstance,
	ResourceTypeExternalIP,
	ResourceTypeNATGateway,
	ResourceTypeVolume,
}

// ResourceTypes returns the resource_type values events carry, sorted.
func ResourceTypes() []string {
	return slices.Clone(resourceTypes)
}

// KnownResourceType reports whether value is a resource_type events carry.
func KnownResourceType(value string) bool {
	return slices.Contains(resourceTypes, value)
}

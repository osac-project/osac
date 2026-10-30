/*
Copyright (c) 2026 Red Hat, Inc.

Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except in
compliance with the License. You may obtain a copy of the License at

  http://www.apache.org/licenses/LICENSE-2.0
*/

package rules

// dimensionFields maps a dimension name to the history field that carries its value, per
// resource type. The names are the billing contract with the provider; the paths are the
// fields the fold records. A revision can only use names listed here.
var dimensionFields = map[string]map[string]string{
	"ComputeInstance": {
		"instance_type":      "status.instance_type",
		"image":              "spec.disk_image",
		"boot_disk_size_gib": "spec.boot_disk.size_gib",
	},
	"Cluster": {
		"template": "spec.template",
		"version":  "spec.version",
	},
	"BareMetalInstance": {
		"instance_type": "spec.instance_type",
		"cluster":       "status.cluster",
		"node_set":      "status.node_set",
	},
	"Volume": {
		"storage_tier":         "spec.storage_tier",
		"protocol":             "status.protocol",
		"vendor_volume_id":     "status.vendor_volume_id",
		"provisioned_size_gib": "status.provisioned_size_gib",
	},
	"ExternalIP": {
		"pool":      "spec.pool",
		"ip_family": "status.address",
		"attached":  "status.attached",
	},
	"NATGateway": {
		"virtual_network": "spec.virtual_network",
		"external_ip":     "spec.external_ip",
	},
}

// computedDimensions names the dimensions whose value the calculator derives from another
// tracked field instead of reading it from history. Conditions evaluate raw stored fields,
// so a condition on one of them compares the underlying value and never matches. Keep this
// list in sync with the derivation in internal/usage: ip_family parses the tracked address
// (ipFamilyOf in engine.go).
var computedDimensions = map[string]bool{
	"ip_family": true,
}

// DimensionPath resolves a dimension name to its history field for a resource.
func DimensionPath(resource, name string) (string, bool) {
	paths, known := dimensionFields[resource]
	if !known {
		return "", false
	}
	path, known := paths[name]
	return path, known
}

// IdentityFields are the identity facts the fold records for every resource. Every meter sends
// them as dimensions.
var IdentityFields = []string{
	"metadata.project",
	"spec.catalog_item",
	"spec.template",
}

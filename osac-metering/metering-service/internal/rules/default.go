/*
Copyright (c) 2026 Red Hat, Inc.

Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except in
compliance with the License. You may obtain a copy of the License at

  http://www.apache.org/licenses/LICENSE-2.0
*/

package rules

import "time"

// DefaultNumber is the revision number of the default meters.
const DefaultNumber = "0001"

// DefaultEffectiveAt covers all time: the default meters bill from the first event.
var DefaultEffectiveAt = time.Unix(0, 0).UTC()

// DefaultMetersYAML is the complete set of default meters, shipped as revision 0001. FAILED is
// never billable. Allocation meters bill through DELETING until physical deletion. Volume
// capacity multiplies by the provisioned GiB. A cluster bills its installation time
// retroactively when it reaches READY, and nothing if it never does.
const DefaultMetersYAML = `- resource: ComputeInstance
  meter: compute
  unit: second
  billable_when:
    states: [RUNNING]
  dimensions: [instance_type, image, boot_disk_size_gib]

- resource: Cluster
  meter: control_plane
  unit: second
  billable_when:
    states: [PROGRESSING, READY]
    turns_billable_once: [READY]
  dimensions: [template, version]

- resource: BareMetalInstance
  meter: allocation
  unit: second
  billable_when:
    states: [STARTING, RUNNING, STOPPING, STOPPED, DELETING]
    turns_billable_once: [STARTING, RUNNING]
  dimensions: [instance_type, cluster, node_set]

- resource: BareMetalInstance
  meter: consumption
  unit: second
  billable_when:
    states: [RUNNING]
  dimensions: [instance_type, cluster, node_set]

- resource: Volume
  meter: capacity
  unit: gibibyte_second
  billable_when:
    states: [AVAILABLE, DELETING]
    turns_billable_once: [AVAILABLE]
    fields:
      protocol: BLOCK
      vendor_volume_id: set
  multiplier: provisioned_size_gib
  dimensions: [storage_tier]

- resource: ExternalIP
  meter: allocation
  unit: second
  billable_when:
    states: [ALLOCATED, DELETING]
    turns_billable_once: [ALLOCATED]
  dimensions: [pool, ip_family, attribution, attached]

- resource: NATGateway
  meter: allocation
  unit: second
  billable_when:
    states: [READY, DELETING]
    turns_billable_once: [READY]
  dimensions: [virtual_network, external_ip]
`

// DefaultRevision returns the default meters as revision 0001.
func DefaultRevision() (Revision, error) {
	return Parse(
		NamePrefix+DefaultNumber,
		DefaultEffectiveAt.Format(time.RFC3339),
		DefaultMetersYAML,
	)
}

/*
Copyright (c) 2026 Red Hat, Inc.

Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except
in compliance with the License. You may obtain a copy of the License at

  http://www.apache.org/licenses/LICENSE-2.0
*/

package events

import cloudevents "github.com/cloudevents/sdk-go/v2"

// SetOSACExtensions sets the standard OSAC CloudEvent extension attributes
// on the given event: osacresourceid, osacresourcetype, osactenant, and
// optionally osacproject.
func SetOSACExtensions(ce *cloudevents.Event, resourceID, resourceType, tenantID, projectID string) {
	ce.SetExtension("osacresourceid", resourceID)
	ce.SetExtension("osacresourcetype", resourceType)
	ce.SetExtension("osactenant", tenantID)
	if projectID != "" {
		ce.SetExtension("osacproject", projectID)
	}
}

/*
Copyright (c) 2026 Red Hat, Inc.

Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except
in compliance with the License. You may obtain a copy of the License at

  http://www.apache.org/licenses/LICENSE-2.0
*/

package watch

import (
	"testing"

	cloudevents "github.com/cloudevents/sdk-go/v2"
)

func TestBuildComponentEventHandlesNilBaseData(t *testing.T) {
	c := &Consumer{}
	baseCE := cloudevents.NewEvent()
	baseCE.SetID("evt-1")
	baseCE.SetSource("osac-metering")
	baseCE.SetType("osac.resource.started.v1")
	// No SetData call: the base event carries zero-length data, so DataAs
	// leaves the target map nil without returning an error.

	ce, err := c.buildComponentEvent(&baseCE, "evt-1/node-a", map[string]any{"component": "worker"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	var data map[string]any
	if err := ce.DataAs(&data); err != nil {
		t.Fatalf("unexpected error reading component event data: %v", err)
	}
	if data["billing_dimensions"] == nil {
		t.Errorf("expected billing_dimensions to be set on the component event even when the base event carried no data")
	}
}

/*
Copyright (c) 2026 Red Hat, Inc.

Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except
in compliance with the License. You may obtain a copy of the License at

  http://www.apache.org/licenses/LICENSE-2.0
*/

package main

import (
	"bytes"
	"time"

	cloudevents "github.com/cloudevents/sdk-go/v2"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/osac-project/osac-metering/schema"
)

// heartbeat builds a usage event of the shape metering publishes. The
// worked example in the design has tenant acme and VM vm-7 of instance type
// small in project web.
func heartbeat(id, quantity string, dimensions map[string]string) cloudevents.Event {
	GinkgoHelper()

	event := cloudevents.NewEvent()
	event.SetID(id)
	event.SetType(schema.HeartbeatType)
	event.SetSource("osac-metering")
	event.SetTime(time.Date(2026, 10, 5, 10, 1, 0, 0, time.UTC))
	Expect(event.SetData(cloudevents.ApplicationJSON, schema.HeartbeatData{
		TenantID:          "acme",
		ResourceType:      schema.ResourceTypeComputeInstance,
		ResourceID:        "vm-7",
		Meter:             "compute",
		Component:         "compute",
		BillingDimensions: dimensions,
		Usage: schema.Usage{
			Semantics: schema.UsageSemanticsInterval,
			From:      "2026-10-05T10:00:00Z",
			To:        "2026-10-05T10:01:00Z",
			Quantity:  quantity,
			Unit:      schema.UsagePrecisionSecond,
			Precision: schema.UsagePrecisionSecond,
		},
		HeartbeatTime: "2026-10-05T10:01:00Z",
		RulesRevision: "0001",
	})).To(Succeed())
	return event
}

var smallDimensions = map[string]string{
	"boot_disk_size_gib": "30",
	"deployment":         "example/deployment",
	"image":              "rhel-9",
	"instance_type":      "small",
	"project":            "web",
	"tenant":             "acme",
}

var _ = Describe("Totals", func() {
	It("sums the worked example to 110 seconds", func() {
		totals := NewTotals()
		for _, event := range []cloudevents.Event{
			heartbeat("2026-10-05T10:01-acme-vm-7", "20.000000", smallDimensions),
			heartbeat("2026-10-05T10:02-acme-vm-7", "60.000000", smallDimensions),
			heartbeat("2026-10-05T10:03-acme-vm-7", "60.000000", smallDimensions),
			// The stop at 10:02:30 corrects the span billed past it.
			heartbeat("2026-10-05T10:04-acme-vm-7", "-30.000000", smallDimensions),
		} {
			added, err := totals.Add(event)
			Expect(err).NotTo(HaveOccurred())
			Expect(added).To(BeTrue())
		}

		rows := totals.Rows()
		Expect(rows).To(HaveLen(1))
		Expect(rows[0].Quantity).To(Equal("110.000000"))
		Expect(rows[0].Unit).To(Equal("second"))
		Expect(rows[0].Key).To(Equal(UsageKey{
			ResourceType: schema.ResourceTypeComputeInstance,
			ResourceID:   "vm-7",
			Meter:        "compute",
			Component:    "compute",
			Dimensions:   `{"boot_disk_size_gib":"30","deployment":"example/deployment","image":"rhel-9","instance_type":"small","project":"web","tenant":"acme"}`,
		}))
		Expect(rows[0].Dimensions).To(Equal("boot_disk_size_gib=30,deployment=example/deployment,image=rhel-9,instance_type=small,project=web,tenant=acme"))
	})

	It("drops a repeat with the same CloudEvent id", func() {
		totals := NewTotals()
		event := heartbeat("2026-10-05T10:04-acme-vm-7", "-30.000000", smallDimensions)

		added, err := totals.Add(event)
		Expect(err).NotTo(HaveOccurred())
		Expect(added).To(BeTrue())

		added, err = totals.Add(event)
		Expect(err).NotTo(HaveOccurred())
		Expect(added).To(BeFalse())

		accepted, repeats := totals.Counts()
		Expect(accepted).To(Equal(1))
		Expect(repeats).To(Equal(1))
		Expect(totals.Rows()[0].Quantity).To(Equal("-30.000000"))
	})

	It("keeps a total per resource, meter, component, and dimension set", func() {
		totals := NewTotals()
		for _, event := range []cloudevents.Event{
			heartbeat("event-1", "60.000000", smallDimensions),
			heartbeat("event-2", "60.000000", map[string]string{
				"boot_disk_size_gib": "30",
				"deployment":         "example/deployment",
				"image":              "rhel-9",
				"instance_type":      "large",
				"project":            "web",
				"tenant":             "acme",
			}),
		} {
			added, err := totals.Add(event)
			Expect(err).NotTo(HaveOccurred())
			Expect(added).To(BeTrue())
		}

		rows := totals.Rows()
		Expect(rows).To(HaveLen(2))
		Expect(rows[0].Dimensions).To(ContainSubstring("instance_type=large"))
		Expect(rows[1].Dimensions).To(ContainSubstring("instance_type=small"))
	})

	It("keeps dimension maps with colliding display text apart", func() {
		totals := NewTotals()
		for _, event := range []cloudevents.Event{
			heartbeat("event-1", "60.000000", map[string]string{"a": "b,c=d"}),
			heartbeat("event-2", "60.000000", map[string]string{"a": "b", "c": "d"}),
		} {
			added, err := totals.Add(event)
			Expect(err).NotTo(HaveOccurred())
			Expect(added).To(BeTrue())
		}

		// Both maps render as "a=b,c=d"; only the encoded key tells them apart.
		rows := totals.Rows()
		Expect(rows).To(HaveLen(2))
		Expect(rows[0].Dimensions).To(Equal("a=b,c=d"))
		Expect(rows[1].Dimensions).To(Equal("a=b,c=d"))
		Expect(rows[0].Key).NotTo(Equal(rows[1].Key))
	})

	It("rejects an event it cannot read", func() {
		totals := NewTotals()

		unknownType := heartbeat("event-1", "60.000000", smallDimensions)
		unknownType.SetType("osac.resource.heartbeat.v2")
		_, err := totals.Add(unknownType)
		Expect(err).To(MatchError(ContainSubstring("not \"osac.resource.heartbeat.v1\"")))

		notANumber := heartbeat("event-2", "soon", smallDimensions)
		_, err = totals.Add(notANumber)
		Expect(err).To(MatchError(ContainSubstring(`quantity "soon"`)))

		noID := heartbeat("", "60.000000", smallDimensions)
		_, err = totals.Add(noID)
		Expect(err).To(MatchError(ContainSubstring("no CloudEvent id")))
	})

	It("prints one row and the billable total", func() {
		totals := NewTotals()
		for _, event := range []cloudevents.Event{
			heartbeat("event-1", "20.000000", smallDimensions),
			heartbeat("event-2", "-5.000000", smallDimensions),
		} {
			_, err := totals.Add(event)
			Expect(err).NotTo(HaveOccurred())
		}

		var output bytes.Buffer
		Expect(totals.Print(&output)).To(Succeed())
		Expect(output.String()).To(ContainSubstring("vm-7"))
		Expect(output.String()).To(ContainSubstring("15.000000"))
		Expect(output.String()).To(ContainSubstring("BILLABLE TOTAL: 15.000000 second"))
	})
})

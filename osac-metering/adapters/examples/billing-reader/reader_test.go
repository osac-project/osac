/*
Copyright (c) 2026 Red Hat, Inc.

Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except
in compliance with the License. You may obtain a copy of the License at

  http://www.apache.org/licenses/LICENSE-2.0
*/

package main

import (
	"bytes"
	"fmt"
	"sync"
	"time"

	"github.com/IBM/sarama"
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
	return heartbeatForTenant(id, "acme", quantity, dimensions)
}

// heartbeatForTenant builds the same event for another tenant.
func heartbeatForTenant(id, tenant, quantity string, dimensions map[string]string) cloudevents.Event {
	GinkgoHelper()

	event := cloudevents.NewEvent()
	event.SetID(id)
	event.SetType(schema.HeartbeatType)
	event.SetSource("osac-metering")
	event.SetTime(time.Date(2026, 10, 5, 10, 1, 0, 0, time.UTC))
	Expect(event.SetData(cloudevents.ApplicationJSON, schema.HeartbeatData{
		TenantID:          tenant,
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
			TenantID:     "acme",
			ResourceType: schema.ResourceTypeComputeInstance,
			ResourceID:   "vm-7",
			Meter:        "compute",
			Component:    "compute",
			Dimensions:   `{"boot_disk_size_gib":"30","deployment":"example/deployment","image":"rhel-9","instance_type":"small","project":"web","tenant":"acme"}`,
		}))
		Expect(rows[0].Dimensions).To(Equal("boot_disk_size_gib=30,deployment=example/deployment,image=rhel-9,instance_type=small,project=web,tenant=acme"))
	})

	It("keeps every tenant's usage separate", func() {
		totals := NewTotals()
		for _, event := range []cloudevents.Event{
			heartbeatForTenant("acme-event", "acme", "110.000000", smallDimensions),
			heartbeatForTenant("globex-event", "globex", "60.000000", smallDimensions),
		} {
			added, err := totals.Add(event)
			Expect(err).NotTo(HaveOccurred())
			Expect(added).To(BeTrue())
		}

		rows := totals.Rows()
		Expect(rows).To(HaveLen(2))
		Expect(rows[0].Key.TenantID).To(Equal("acme"))
		Expect(rows[0].Quantity).To(Equal("110.000000"))
		Expect(rows[1].Key.TenantID).To(Equal("globex"))
		Expect(rows[1].Quantity).To(Equal("60.000000"))
	})

	It("sums concurrent adds without losing or double-counting", func() {
		const workers = 8
		const perWorker = 100

		// Build every event before the goroutines start, so a Ginkgo helper
		// never runs off the test goroutine.
		type work struct {
			events []cloudevents.Event
			repeat cloudevents.Event
		}
		batches := make([]work, workers)
		for worker := 0; worker < workers; worker++ {
			tenant := fmt.Sprintf("tenant-%d", worker)
			batch := work{repeat: heartbeatForTenant(fmt.Sprintf("worker-%d-repeat", worker), tenant, "5.000000", smallDimensions)}
			for i := 0; i < perWorker; i++ {
				batch.events = append(batch.events, heartbeatForTenant(fmt.Sprintf("worker-%d-%d", worker, i), tenant, "1.000000", smallDimensions))
			}
			batches[worker] = batch
		}

		totals := NewTotals()
		errs := make(chan error, workers)
		var wg sync.WaitGroup
		for worker := 0; worker < workers; worker++ {
			wg.Add(1)
			go func(batch work) {
				defer wg.Done()
				for _, event := range batch.events {
					if _, err := totals.Add(event); err != nil {
						errs <- err
						return
					}
				}
				// Every worker adds the same repeat three times: one accepted
				// event and two drops.
				for i := 0; i < 3; i++ {
					if _, err := totals.Add(batch.repeat); err != nil {
						errs <- err
						return
					}
				}
			}(batches[worker])
		}
		wg.Wait()
		close(errs)
		for err := range errs {
			Expect(err).NotTo(HaveOccurred())
		}

		accepted, repeats := totals.Counts()
		Expect(accepted).To(Equal(workers * (perWorker + 1)))
		Expect(repeats).To(Equal(workers * 2))

		rows := totals.Rows()
		Expect(rows).To(HaveLen(workers))
		for _, row := range rows {
			Expect(row.Quantity).To(Equal("105.000000"))
		}
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
		Expect(output.String()).To(ContainSubstring("BILLABLE TOTAL acme: 15.000000 second"))
	})
})

var _ = Describe("initialOffset", func() {
	It("defaults to the oldest record", func() {
		offset, err := initialOffset("oldest")
		Expect(err).NotTo(HaveOccurred())
		Expect(offset).To(Equal(int64(sarama.OffsetOldest)))
	})

	It("accepts the newest offset regardless of case", func() {
		offset, err := initialOffset("Newest")
		Expect(err).NotTo(HaveOccurred())
		Expect(offset).To(Equal(int64(sarama.OffsetNewest)))
	})

	It("rejects anything else", func() {
		_, err := initialOffset("beginning")
		Expect(err).To(MatchError(ContainSubstring(`invalid READER_START_OFFSET "beginning"`)))
	})
})

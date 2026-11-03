/*
Copyright (c) 2026 Red Hat, Inc.

Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except
in compliance with the License. You may obtain a copy of the License at

  http://www.apache.org/licenses/LICENSE-2.0
*/

package schema_test

import (
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/osac-project/osac-metering/schema"
)

var _ = Describe("Published schema versions", func() {
	It("publishes the version the events name", func() {
		versions, err := schema.PublishedVersions()
		Expect(err).NotTo(HaveOccurred())
		Expect(versions).NotTo(BeEmpty())

		current, err := schema.VersionForType(versions, schema.HeartbeatType)
		Expect(err).NotTo(HaveOccurred())
		Expect(current.CloudEventType).To(Equal(schema.HeartbeatType))
		Expect(current.Version).To(Equal(schema.SchemaVersion))
		Expect(current.SourcePath).To(Equal("versions/" + schema.SchemaVersion + ".json"))
		Expect(current.SourceSHA256).To(MatchRegexp("^[0-9a-f]{64}$"))
	})

	It("carries a version and an effective date on every file", func() {
		versions, err := schema.PublishedVersions()
		Expect(err).NotTo(HaveOccurred())
		Expect(versions).To(HaveLen(1))

		Expect(versions[0].Version).To(Equal("v1"))
		Expect(versions[0].EffectiveAt).
			To(Equal(time.Date(1970, 1, 1, 0, 0, 0, 0, time.UTC)))
	})

	It("lists the payload fields the events carry", func() {
		versions, err := schema.PublishedVersions()
		Expect(err).NotTo(HaveOccurred())
		version, err := schema.VersionForType(versions, schema.HeartbeatType)
		Expect(err).NotTo(HaveOccurred())

		names := map[string]schema.Field{}
		for _, field := range version.Data {
			names[field.Name] = field
		}
		Expect(names).To(HaveKey("tenant_id"))
		Expect(names).To(HaveKey("resource_type"))
		Expect(names).To(HaveKey("resource_id"))
		Expect(names).To(HaveKey("meter"))
		Expect(names).To(HaveKey("component"))
		Expect(names).To(HaveKey("billing_dimensions"))
		Expect(names).To(HaveKey("usage"))
		Expect(names).To(HaveKey("heartbeat_time"))
		Expect(names).To(HaveKey("last_applied_offset"))
		Expect(names).To(HaveKey("rules_revision"))
		Expect(names).To(HaveKey("external_billing_account_id"))
		Expect(names["external_billing_account_id"].Required).To(BeFalse())

		usage := names["usage"]
		Expect(usage.Type).To(Equal(schema.FieldTypeObject))
		Expect(usage.Required).To(BeTrue())
		quantities := map[string]schema.Field{}
		for _, field := range usage.Fields {
			quantities[field.Name] = field
		}
		Expect(quantities).To(HaveKey("from"))
		Expect(quantities).To(HaveKey("to"))
		Expect(quantities).To(HaveKey("quantity"))
		Expect(quantities["quantity"].Pattern).NotTo(BeEmpty())

		Expect(names["last_applied_offset"].Type).To(Equal(schema.FieldTypeInteger))
	})

	It("selects the version in effect when a record was written", func() {
		version, err := schema.VersionInEffect(time.Date(2026, 11, 2, 10, 0, 0, 0, time.UTC))
		Expect(err).NotTo(HaveOccurred())
		Expect(version.Version).To(Equal("v1"))

		version, err = schema.VersionInEffect(time.Unix(0, 0))
		Expect(err).NotTo(HaveOccurred())
		Expect(version.Version).To(Equal("v1"))

	})

	It("refuses a record time before the first version and the zero time", func() {
		before, err := time.Parse(time.RFC3339, "1969-12-31T23:59:59Z")
		Expect(err).NotTo(HaveOccurred())
		_, err = schema.VersionInEffect(before)
		Expect(err).To(MatchError(ContainSubstring("no schema version is in effect")))

		_, err = schema.VersionInEffect(time.Time{})
		Expect(err).To(MatchError(ContainSubstring("zero time")))
	})

	It("reports an event type it has not published", func() {
		versions, err := schema.PublishedVersions()
		Expect(err).NotTo(HaveOccurred())
		_, err = schema.VersionForType(versions, "osac.resource.heartbeat.v99")
		Expect(err).To(MatchError(ContainSubstring("treat it as a version you have not adopted")))
	})
})

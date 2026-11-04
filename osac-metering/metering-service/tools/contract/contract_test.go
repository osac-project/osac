/*
Copyright (c) 2026 Red Hat, Inc.

Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except
in compliance with the License. You may obtain a copy of the License at

  http://www.apache.org/licenses/LICENSE-2.0
*/

package contract_test

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/santhosh-tekuri/jsonschema/v6"

	"github.com/osac-project/osac/proto/metering"

	"github.com/osac-project/osac-metering/internal/database"
	"github.com/osac-project/osac-metering/internal/heartbeat"
	"github.com/osac-project/osac-metering/schema"
	"github.com/osac-project/osac-metering/tools/contract"
)

func TestContract(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "Contract Suite")
}

// repositoryRoot walks up from the package directory to the checkout root.
func repositoryRoot() string {
	directory, err := os.Getwd()
	Expect(err).NotTo(HaveOccurred())
	for {
		if _, err := os.Stat(filepath.Join(directory, "go.work")); err == nil {
			return directory
		}
		parent := filepath.Dir(directory)
		Expect(parent).NotTo(Equal(directory), "no checkout root above the test directory")
		directory = parent
	}
}

// generated returns the two generated files by name.
func generated(sources contract.Sources) (reference, checkable string) {
	files, err := contract.Generate(sources)
	Expect(err).NotTo(HaveOccurred())
	reference = string(files[contract.DocsDirectory+"/"+contract.ReferenceName])
	checkable = string(files[contract.DocsDirectory+"/"+contract.SchemaName])
	return reference, checkable
}

var _ = Describe("The generated contract", func() {
	var (
		root    string
		sources contract.Sources
	)

	BeforeEach(func() {
		root = repositoryRoot()
		var err error
		sources, err = contract.LoadSources(root)
		Expect(err).NotTo(HaveOccurred())
	})

	It("matches the committed files, so CI fails on a stale reference", func() {
		Expect(contract.Check(root)).To(Succeed())
	})

	It("renders the same bytes whatever order the inputs arrive in", func() {
		first, err := contract.Generate(sources)
		Expect(err).NotTo(HaveOccurred())

		shuffled := contract.Sources{
			Resources:         make([]metering.Resource, len(sources.Resources)),
			Revision:          sources.Revision,
			Versions:          slices.Clone(sources.Versions),
			HeartbeatInterval: sources.HeartbeatInterval,
			TopicRetention:    sources.TopicRetention,
		}
		for i, resource := range sources.Resources {
			fields := slices.Clone(resource.Fields)
			slices.Reverse(fields)
			shuffled.Resources[i] = metering.Resource{Message: resource.Message, Fields: fields}
		}
		slices.Reverse(shuffled.Resources)
		shuffled.Revision.Meters = slices.Clone(sources.Revision.Meters)
		slices.Reverse(shuffled.Revision.Meters)

		second, err := contract.Generate(shuffled)
		Expect(err).NotTo(HaveOccurred())
		Expect(second).To(Equal(first))
	})

	It("lists every resource type and meter with its unit and billable states", func() {
		reference, _ := generated(sources)

		Expect(reference).To(ContainSubstring("6 resource types and 7 meters"))
		for _, heading := range []string{
			"### Compute instance (`compute_instance`)",
			"### Cluster order (`cluster_order`)",
			"### Bare metal instance (`bare_metal_instance`)",
			"### Volume (`volume`)",
			"### External IP (`external_ip`)",
			"### NAT gateway (`nat_gateway`)",
		} {
			Expect(reference).To(ContainSubstring(heading))
		}

		Expect(reference).To(
			ContainSubstring("| `compute` | `compute` | `second` | RUNNING |"),
		)
		Expect(reference).To(ContainSubstring(
			"| `control_plane` | `control_plane` | `second` | PROGRESSING, READY |",
		))
		Expect(reference).To(ContainSubstring(
			"| `consumption` | `consumption` | `second` | RUNNING |",
		))
		Expect(reference).To(ContainSubstring(
			"| `capacity` | `capacity` | `gibibyte_second` | AVAILABLE, DELETING |",
		))
		Expect(strings.Count(reference, "| `allocation` | `allocation` | `second` |")).
			To(Equal(3), "three resource types bill an allocation meter")
		Expect(reference).To(ContainSubstring("`vendor_volume_id` is `set`"))
	})

	It("states the reading rule, the lifecycle, and the transport facts", func() {
		reference, _ := generated(sources)

		for _, statement := range []string{
			"Add every usage event for one resource, meter, component, and set of billing dimensions",
			"A correction is a usage event with a negative quantity for the same resource, meter, component, and billing dimensions",
			"## Terms",
			"Replay decodes every retained record with the current payload shape",
			"One usage topic per tenant, named `osac.metering.heartbeat.<tenant>`",
			"Each usage topic has one partition",
			"At least once",
			"`topics.retentionMs` (milliseconds); the default `-1` keeps every record",
			"Raw event log | Internal",
			"The reader owns its retries and its failed records",
			"The CloudEvent `id` is the deduplication key",
			"It leaves out the quantity",
			"ships the next immutable revision with a later effective date",
			"A 100 GiB volume's quantity is already multiplied by 100",
		} {
			Expect(reference).To(ContainSubstring(statement))
		}
	})

	It("uses the resource type values events carry", func() {
		reference, _ := generated(sources)
		Expect(reference).To(ContainSubstring(
			"The values below are the ones events carry in `resource_type`:",
		))
		Expect(reference).To(ContainSubstring("`cluster_order`"))
		for _, resourceType := range []string{
			schema.ResourceTypeComputeInstance,
			schema.ResourceTypeClusterOrder,
			schema.ResourceTypeBareMetalInstance,
			schema.ResourceTypeVolume,
			schema.ResourceTypeExternalIP,
			schema.ResourceTypeNATGateway,
		} {
			Expect(reference).To(ContainSubstring("(`" + resourceType + "`)"))
		}
	})

	It("publishes a checkable schema for every version", func() {
		_, checkable := generated(sources)

		var doc struct {
			Schema string              `json:"$schema"`
			OneOf  []map[string]string `json:"oneOf"`
			Defs   map[string]struct {
				Version      string `json:"x-osac-version"`
				EffectiveAt  string `json:"x-osac-effective-at"`
				SourceFile   string `json:"x-osac-source-file"`
				SourceSHA256 string `json:"x-osac-source-sha256"`
				Properties   map[string]struct {
					Const      string                     `json:"const"`
					Properties map[string]json.RawMessage `json:"properties"`
					Required   []string                   `json:"required"`
				} `json:"properties"`
			} `json:"$defs"`
		}
		Expect(json.Unmarshal([]byte(checkable), &doc)).To(Succeed())
		Expect(doc.Schema).To(Equal("https://json-schema.org/draft/2020-12/schema"))
		Expect(doc.OneOf).To(HaveLen(len(sources.Versions)))

		for _, version := range sources.Versions {
			definition, found := doc.Defs[version.Version]
			Expect(found).To(BeTrue())
			Expect(definition.Version).To(Equal(version.Version))
			Expect(definition.EffectiveAt).
				To(Equal(version.EffectiveAt.Format("2006-01-02T15:04:05Z")))
			Expect(definition.SourceFile).To(Equal(
				"osac-metering/schema/versions/" + version.Version + ".json",
			))
			Expect(definition.SourceSHA256).To(Equal(version.SourceSHA256))
			Expect(definition.Properties["type"].Const).To(Equal(version.CloudEventType))
		}
	})

	It("enumerates the resource types in the checkable schema", func() {
		_, checkable := generated(sources)

		type fieldSchema struct {
			Enum []string `json:"enum"`
		}
		var published struct {
			Definitions map[string]struct {
				Properties map[string]json.RawMessage `json:"properties"`
			} `json:"$defs"`
		}
		Expect(json.Unmarshal([]byte(checkable), &published)).To(Succeed())
		definition := published.Definitions[schema.SchemaVersion]

		var envelope fieldSchema
		Expect(json.Unmarshal(definition.Properties["osacresourcetype"], &envelope)).
			To(Succeed())
		Expect(envelope.Enum).To(ConsistOf(schema.ResourceTypes()))

		var payload struct {
			Properties map[string]fieldSchema `json:"properties"`
		}
		Expect(json.Unmarshal(definition.Properties["data"], &payload)).To(Succeed())
		Expect(payload.Properties["resource_type"].Enum).To(ConsistOf(schema.ResourceTypes()))
	})

	It("accepts a real heartbeat and refuses a version it has not published", func() {
		_, checkable := generated(sources)
		schemaDoc, err := jsonschema.UnmarshalJSON(strings.NewReader(checkable))
		Expect(err).NotTo(HaveOccurred())
		compiler := jsonschema.NewCompiler()
		Expect(compiler.AddResource("usage-event.schema.json", schemaDoc)).To(Succeed())
		compiled, err := compiler.Compile("usage-event.schema.json")
		Expect(err).NotTo(HaveOccurred())

		written := time.Date(2026, 11, 2, 10, 0, 0, 0, time.UTC)
		key := database.LedgerKey{
			ResourceType: "Cluster", ResourceID: "cluster-1", Meter: "control_plane",
			Component: "control_plane", Dimensions: `{"tenant":"acme","deployment":"test"}`,
		}
		event, err := heartbeat.New(
			"acme", key, written.Add(-time.Minute), written, 60_000_000, "second",
			written, 7, sources.Revision.Number, "",
		)
		Expect(err).NotTo(HaveOccurred())
		payload, err := event.MarshalJSON()
		Expect(err).NotTo(HaveOccurred())
		instance, err := jsonschema.UnmarshalJSON(bytes.NewReader(payload))
		Expect(err).NotTo(HaveOccurred())
		Expect(compiled.Validate(instance)).To(Succeed())

		// The event's envelope and the published envelope must name the same attributes. A
		// new extension on the producer side that the schema file does not carry would
		// otherwise make every real event fail the checkable schema.
		var published struct {
			Definitions map[string]struct {
				Properties map[string]json.RawMessage `json:"properties"`
			} `json:"$defs"`
		}
		Expect(json.Unmarshal([]byte(checkable), &published)).To(Succeed())
		definition := published.Definitions[schema.SchemaVersion]
		Expect(definition.Properties).NotTo(BeEmpty())
		schemaAttributes := make([]string, 0, len(definition.Properties))
		for name := range definition.Properties {
			if name != "data" {
				schemaAttributes = append(schemaAttributes, name)
			}
		}
		var envelope map[string]json.RawMessage
		Expect(json.Unmarshal(payload, &envelope)).To(Succeed())
		eventAttributes := make([]string, 0, len(envelope))
		for name := range envelope {
			if name != "data" {
				eventAttributes = append(eventAttributes, name)
			}
		}
		Expect(eventAttributes).To(ConsistOf(schemaAttributes))

		event.SetType("osac.resource.heartbeat.v99")
		payload, err = event.MarshalJSON()
		Expect(err).NotTo(HaveOccurred())
		instance, err = jsonschema.UnmarshalJSON(bytes.NewReader(payload))
		Expect(err).NotTo(HaveOccurred())
		Expect(compiled.Validate(instance)).NotTo(Succeed())
	})

	It("records the hash of the published file a reader holds", func() {
		_, checkable := generated(sources)

		content, err := os.ReadFile(filepath.Join(
			root, "osac-metering/schema/versions", schema.SchemaVersion+".json",
		))
		Expect(err).NotTo(HaveOccurred())
		digest := sha256.Sum256(content)
		Expect(checkable).To(ContainSubstring(hex.EncodeToString(digest[:])))
	})

	It("publishes the billing account id from v1 on", func() {
		reference, checkable := generated(sources)
		Expect(reference).To(ContainSubstring(
			"Schema version `v1` and later carry `external_billing_account_id`",
		))
		Expect(checkable).To(ContainSubstring(`"external_billing_account_id"`))

		var published struct {
			Definitions map[string]struct {
				Properties map[string]json.RawMessage `json:"properties"`
			} `json:"$defs"`
		}
		Expect(json.Unmarshal([]byte(checkable), &published)).To(Succeed())
		var data struct {
			Properties map[string]json.RawMessage `json:"properties"`
		}
		Expect(json.Unmarshal(published.Definitions["v1"].Properties["data"], &data)).
			To(Succeed())
		Expect(data.Properties).To(HaveKey("external_billing_account_id"))
		Expect(published.Definitions).To(HaveLen(1))
	})

	It("refuses a meter the field registry does not list", func() {
		broken := contract.Sources{
			Resources:         sources.Resources,
			Revision:          sources.Revision,
			Versions:          sources.Versions,
			HeartbeatInterval: sources.HeartbeatInterval,
			TopicRetention:    sources.TopicRetention,
		}
		broken.Resources = slices.Clone(sources.Resources)
		broken.Resources = broken.Resources[1:]
		_, err := contract.Generate(broken)
		Expect(err).To(MatchError(ContainSubstring(
			"name a resource the field registry lists",
		)))
	})
})

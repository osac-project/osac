/*
Copyright (c) 2026 Red Hat Inc.

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

package servers

import (
	"fmt"
	"strconv"
	"testing"

	privatev1 "github.com/osac-project/osac/proto/gen/osac/private/v1"
)

func TestAutoCreatedExternalIPFilterEscapesOwnerID(t *testing.T) {
	ownerID := "ci-1' || this.metadata.labels['osac.openshift.io/auto-created'] == 'true"
	want := fmt.Sprintf(
		"(this.metadata.labels['%s'] == 'true' && this.metadata.labels['%s'] == %s) || "+
			"(this.metadata.labels['%s'] == 'true' && this.metadata.labels['%s'] == %s)",
		autoCreatedLabel,
		autoCreatedForLabel,
		strconv.Quote(ownerID),
		autoProvisionedLabel,
		autoProvisionedForLabel,
		strconv.Quote(ownerID),
	)

	if got := autoCreatedExternalIPFilter(ownerID); got != want {
		t.Fatalf("autoCreatedExternalIPFilter(%q) = %q, want %q", ownerID, got, want)
	}
}

func TestAutoCreatedExternalIPAttachmentFilterEscapesOwnerID(t *testing.T) {
	ownerID := "ci-1' || this.metadata.labels['osac.openshift.io/auto-created'] == 'true"
	want := fmt.Sprintf(
		"(this.metadata.labels['%s'] == 'true' && this.metadata.labels['%s'] == %s) || "+
			"(this.metadata.labels['%s'] == 'true' && this.metadata.labels['%s'] == %s)",
		autoCreatedLabel,
		autoCreatedForLabel,
		strconv.Quote(ownerID),
		autoProvisionedLabel,
		autoProvisionedForLabel,
		strconv.Quote(ownerID),
	)

	if got := autoCreatedExternalIPAttachmentFilter(ownerID); got != want {
		t.Fatalf("autoCreatedExternalIPAttachmentFilter(%q) = %q, want %q", ownerID, got, want)
	}
}

func TestExternalIPConsumerFilterIncludesLegacyAndMalformedCandidates(t *testing.T) {
	id := `eip-1`
	name := `public-ip-a`
	want := fmt.Sprintf(
		"(this.spec.external_ip.id == %s || this.spec.external_ip.name == %s || "+
			"this.spec.external_ip.id == %s || this.spec.external_ip.name == %s || "+
			`((!has(this.spec.external_ip.id) || this.spec.external_ip.id == "") && (!has(this.spec.external_ip.name) || this.spec.external_ip.name == "")))`,
		strconv.Quote(id), strconv.Quote(id), strconv.Quote(name), strconv.Quote(name),
	)
	owner := privatev1.Metadata_builder{Tenant: "tenant"}.Build()
	want = fmt.Sprintf("(this.spec.external_ip.id == %s || (this.metadata.tenant == %s && this.metadata.project == %s && (%s)))",
		strconv.Quote(id), strconv.Quote(owner.GetTenant()), strconv.Quote(owner.GetProject()), want[1:len(want)-1])
	got, err := externalIPConsumerFilter(id, name, owner)
	if err != nil {
		t.Fatal(err)
	}
	if got != want {
		t.Fatalf("externalIPConsumerFilter(%q, %q) = %q, want %q", id, name, got, want)
	}
}

func TestExternalIPLookupRejectsUnsupportedLiterals(t *testing.T) {
	owner := privatev1.Metadata_builder{Tenant: "tenant"}.Build()
	if _, err := externalIPConsumerFilter("", "default-nat", owner); err == nil {
		t.Fatal("missing parent identity was accepted")
	}
	if _, err := externalIPConsumerFilter("eip", "default-nat", nil); err == nil {
		t.Fatal("missing parent owner was accepted")
	}
	for _, value := range []string{"path\\name", "quote'name", "quote\"name", "line\nname", "control\x00", "non-ascii-π"} {
		t.Run(strconv.Quote(value), func(t *testing.T) {
			if err := externalIPLookupValue(value); err == nil {
				t.Fatal("unsupported literal was accepted")
			}
			if _, err := externalIPConsumerFilter(value, "default-nat", owner); err == nil {
				t.Fatal("unsupported candidate filter was accepted")
			}
			for _, invalidOwner := range []*privatev1.Metadata{
				privatev1.Metadata_builder{Tenant: value}.Build(),
				privatev1.Metadata_builder{Tenant: "tenant", Project: value}.Build(),
			} {
				if _, err := externalIPConsumerFilter("eip", "default-nat", invalidOwner); err == nil {
					t.Fatal("unsupported owner filter was accepted")
				}
			}
		})
	}
	for _, value := range []string{"", "default-nat", "01a17a4e-c253-7807-bb57-0b24c238fc7a", "shared", "project.child"} {
		if err := externalIPLookupValue(value); err != nil {
			t.Fatal(err)
		}
	}
}

func TestExternalIPConsumerSnapshotDetectsIdentityAndOwnerChanges(t *testing.T) {
	reference := privatev1.ExternalIPLocalReference_builder{Id: "eip", Name: "alias"}.Build()
	owner := privatev1.Metadata_builder{Tenant: "tenant"}.Build()
	if !sameExternalIPConsumer(reference, owner, reference, owner) {
		t.Fatal("stable snapshot rejected")
	}
	for _, changed := range []struct {
		reference *privatev1.ExternalIPLocalReference
		owner     *privatev1.Metadata
	}{
		{nil, owner},
		{privatev1.ExternalIPLocalReference_builder{Id: "different", Name: "alias"}.Build(), owner},
		{privatev1.ExternalIPLocalReference_builder{Id: "eip", Name: "different"}.Build(), owner},
		{reference, privatev1.Metadata_builder{Tenant: "different"}.Build()},
		{reference, privatev1.Metadata_builder{Tenant: "tenant", Project: "different"}.Build()},
	} {
		if sameExternalIPConsumer(reference, owner, changed.reference, changed.owner) {
			t.Fatal("changed snapshot accepted")
		}
	}
}

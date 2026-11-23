/*
Copyright (c) 2026 Red Hat Inc.

Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except in compliance with the
License. You may obtain a copy of the License at

  http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on an
"AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the specific
language governing permissions and limitations under the License.
*/

package cevent

import (
	"encoding/json"
	"fmt"
	"regexp"
	"strings"

	cloudevents "github.com/cloudevents/sdk-go/v2"

	"github.com/osac-project/osac/osac-notifications/internal/grant"
)

const (
	extTenant             = "osactenant"
	extKind               = "kind"
	extSeverity           = "severity"
	extMatchSubscriptions = "osacmatchsubscriptions"

	TopicPrefix = "osac.notifications."
)

var (
	typePattern = regexp.MustCompile(`^osac\.notification\.([a-z]+)\.([a-z0-9_]+)$`)
	// TopicPattern matches ingest topics and excludes DLQ topics
	// (e.g. osac.notifications.dlq.<tenant>).
	TopicPattern = regexp.MustCompile(`^osac\.notifications\.[^.]+$`)
)

// Recipient is an inbox target from CE data.recipients.
type Recipient struct {
	Type string `json:"type"`
	ID   string `json:"id"`
}

// Resource is an optional typed API object reference from CE data.resource.
type Resource struct {
	Cluster         *LocalRef `json:"cluster,omitempty"`
	ComputeInstance *LocalRef `json:"compute_instance,omitempty"`
	VirtualNetwork  *LocalRef `json:"virtual_network,omitempty"`
	Subnet          *LocalRef `json:"subnet,omitempty"`
	ExternalIP      *LocalRef `json:"external_ip,omitempty"`
	BareMetal       *LocalRef `json:"bare_metal_instance,omitempty"`
}

// LocalRef is a local object id reference.
type LocalRef struct {
	ID string `json:"id"`
}

// Data is the CloudEvent data payload for notification ingest.
type Data struct {
	Title       string         `json:"title"`
	Description string         `json:"description,omitempty"`
	Recipients  []Recipient    `json:"recipients,omitempty"`
	Resource    *Resource      `json:"resource,omitempty"`
	Details     map[string]any `json:"details,omitempty"`
}

// Accepted is a validated CloudEvent ready for intent storage and fan-out.
type Accepted struct {
	Event     cloudevents.Event
	Tenant    string
	Source    string
	ID        string
	Kind      string
	Type      string
	Severity  string
	Topic     string
	MatchSubs bool
	Data      Data
	Payload   json.RawMessage
}

// TopicMatches reports whether topic is an ingest topic (not DLQ).
func TopicMatches(topic string) bool {
	return TopicPattern.MatchString(topic)
}

// TenantFromTopic extracts the tenant suffix from osac.notifications.<tenant>.
func TenantFromTopic(topic string) (string, error) {
	if !strings.HasPrefix(topic, TopicPrefix) {
		return "", fmt.Errorf("topic %q does not use prefix %q", topic, TopicPrefix)
	}
	tenant := strings.TrimPrefix(topic, TopicPrefix)
	if tenant == "" || strings.Contains(tenant, ".") {
		return "", fmt.Errorf("topic %q is not a single-tenant ingest topic", topic)
	}
	return tenant, nil
}

// Parse unmarshals a CloudEvents JSON payload.
func Parse(body []byte) (cloudevents.Event, error) {
	event := cloudevents.NewEvent()
	if err := json.Unmarshal(body, &event); err != nil {
		return cloudevents.Event{}, fmt.Errorf("unmarshal cloud event: %w", err)
	}
	return event, nil
}

// Validate checks CE fields against the design Validation (Kafka ingest) rules.
func Validate(topic string, body []byte, grants grant.Map) (*Accepted, error) {
	if !TopicMatches(topic) {
		return nil, fmt.Errorf("topic %q is not an ingest topic", topic)
	}
	topicTenant, err := TenantFromTopic(topic)
	if err != nil {
		return nil, err
	}

	event, err := Parse(body)
	if err != nil {
		return nil, err
	}
	if event.SpecVersion() != "1.0" {
		return nil, fmt.Errorf("specversion must be 1.0")
	}
	if event.ID() == "" {
		return nil, fmt.Errorf("id is required")
	}
	if event.Source() == "" {
		return nil, fmt.Errorf("source is required")
	}
	if event.Type() == "" {
		return nil, fmt.Errorf("type is required")
	}
	if event.Time().IsZero() {
		return nil, fmt.Errorf("time is required")
	}

	tenant, _ := extensionString(event, extTenant)
	if tenant == "" {
		return nil, fmt.Errorf("%s extension is required", extTenant)
	}
	if tenant != topicTenant {
		return nil, fmt.Errorf("%s %q does not match topic tenant %q", extTenant, tenant, topicTenant)
	}

	kind, _ := extensionString(event, extKind)
	if kind == "" {
		return nil, fmt.Errorf("%s extension is required", extKind)
	}
	if kind == "announcement" {
		return nil, fmt.Errorf("kind announcement is not allowed on Kafka ingest")
	}

	severity, _ := extensionString(event, extSeverity)
	switch severity {
	case "info", "warning", "critical":
	default:
		return nil, fmt.Errorf("severity must be info, warning, or critical")
	}

	matches := typePattern.FindStringSubmatch(event.Type())
	if matches == nil {
		return nil, fmt.Errorf("type must match osac.notification.<kind>.<name>")
	}
	if matches[1] != kind {
		return nil, fmt.Errorf("type kind segment %q must equal kind %q", matches[1], kind)
	}

	if err := grants.Check(event.Source(), kind); err != nil {
		return nil, err
	}

	var data Data
	if len(event.Data()) > 0 {
		if err := json.Unmarshal(event.Data(), &data); err != nil {
			return nil, fmt.Errorf("unmarshal data: %w", err)
		}
	}
	if data.Title == "" {
		return nil, fmt.Errorf("data.title is required")
	}
	if len(UserRecipientIDs(data.Recipients)) == 0 {
		return nil, fmt.Errorf("data.recipients must include at least one recipient")
	}

	matchSubs := true
	if raw, ok := event.Extensions()[extMatchSubscriptions]; ok {
		switch v := raw.(type) {
		case bool:
			matchSubs = v
		case string:
			matchSubs = v == "true" || v == "1"
		}
	}

	return &Accepted{
		Event:     event,
		Tenant:    tenant,
		Source:    event.Source(),
		ID:        event.ID(),
		Kind:      kind,
		Type:      event.Type(),
		Severity:  severity,
		Topic:     topic,
		MatchSubs: matchSubs,
		Data:      data,
		Payload:   append(json.RawMessage(nil), body...),
	}, nil
}

// UserRecipientIDs returns ids for recipients with type user (case-insensitive).
func UserRecipientIDs(recipients []Recipient) []string {
	ids := make([]string, 0, len(recipients))
	seen := map[string]struct{}{}
	for _, r := range recipients {
		if !strings.EqualFold(r.Type, "user") {
			continue
		}
		if r.ID == "" {
			continue
		}
		if _, ok := seen[r.ID]; ok {
			continue
		}
		seen[r.ID] = struct{}{}
		ids = append(ids, r.ID)
	}
	return ids
}

func extensionString(event cloudevents.Event, name string) (string, bool) {
	raw, ok := event.Extensions()[name]
	if !ok || raw == nil {
		return "", false
	}
	switch v := raw.(type) {
	case string:
		return v, true
	default:
		return fmt.Sprint(v), true
	}
}

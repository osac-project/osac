// Package remotewrite merges the keys OSAC owns into the management cluster's
// cluster-monitoring-config document. It preserves every other key, including
// a pre-existing enableUserWorkload, and never writes enableUserWorkload or the
// userWorkloadMonitoring block itself.
package remotewrite

import (
	"bytes"
	"errors"
	"fmt"
	"strings"

	"gopkg.in/yaml.v3"
)

// ErrConflict reports a differently named remoteWrite entry that already
// targets the OSAC Thanos Receive URL. Merge returns the input unchanged.
var ErrConflict = errors.New("remoteWrite entry already targets the OSAC Thanos Receive URL")

const caFile = "/etc/prometheus/configmaps/service-ca-bundle/service-ca.crt"

// Options carries the values the management cluster writes with.
type Options struct {
	Enabled            bool
	ScrapeInterval     string
	ReceiveURL         string
	PlatformNamespaces []string
}

// Merge returns the cluster-monitoring-config document with OSAC's owned keys
// upserted, or with only those keys removed when Enable is false. An empty
// current document is a no-op when disabled and the full payload when enabled.
func Merge(current []byte, opts Options) ([]byte, error) {
	doc, err := decode(current)
	if err != nil {
		return nil, err
	}
	if doc == nil {
		if !opts.Enabled {
			return current, nil
		}
		doc = documentOf(mappingOf())
	}

	root := doc.Content[0]
	if opts.Enabled {
		if err := upsertEnabled(root, opts); err != nil {
			return current, err
		}
	} else if !removeOwned(root) {
		return current, nil
	}

	out, err := yaml.Marshal(doc)
	if err != nil {
		return nil, fmt.Errorf("marshal cluster-monitoring-config: %w", err)
	}
	return out, nil
}

func decode(current []byte) (*yaml.Node, error) {
	if len(bytes.TrimSpace(current)) == 0 {
		return nil, nil
	}
	var doc yaml.Node
	if err := yaml.Unmarshal(current, &doc); err != nil {
		return nil, fmt.Errorf("parse cluster-monitoring-config: %w", err)
	}
	if doc.Kind != yaml.DocumentNode || len(doc.Content) == 0 {
		return nil, errors.New("cluster-monitoring-config is not a YAML document")
	}
	root := doc.Content[0]
	if root.Kind == yaml.ScalarNode && root.Tag == "!!null" {
		return nil, nil
	}
	if root.Kind != yaml.MappingNode {
		return nil, errors.New("cluster-monitoring-config is not a mapping")
	}
	return &doc, nil
}

func upsertEnabled(root *yaml.Node, opts Options) error {
	prom := ensureMapping(root, "prometheusK8s")
	setScalar(prom, "scrapeInterval", opts.ScrapeInterval)

	labels := ensureMapping(prom, "externalLabels")
	setScalar(labels, "cluster", "management")
	setScalar(labels, "managed_by", "osac")

	entries := ensureSequence(prom, "remoteWrite")
	osacIndex := -1
	for i, entry := range entries.Content {
		name := scalarValue(entry, "name")
		if name == "osac" {
			if osacIndex < 0 {
				osacIndex = i
			}
			continue
		}
		if scalarValue(entry, "url") == opts.ReceiveURL {
			return fmt.Errorf("%w: remoteWrite entry %q already targets the OSAC Thanos Receive URL", ErrConflict, name)
		}
	}

	replacement := remoteWriteEntry(opts)
	if osacIndex >= 0 {
		entries.Content[osacIndex] = replacement
		entries.Content = dropDuplicateOSAC(entries.Content, osacIndex)
	} else {
		entries.Content = append(entries.Content, replacement)
	}
	return nil
}

func dropDuplicateOSAC(entries []*yaml.Node, keep int) []*yaml.Node {
	kept := entries[:0]
	for i, entry := range entries {
		if i != keep && scalarValue(entry, "name") == "osac" {
			continue
		}
		kept = append(kept, entry)
	}
	return kept
}

func removeOwned(root *yaml.Node) bool {
	prom := mappingValue(root, "prometheusK8s")
	if prom == nil || prom.Kind != yaml.MappingNode {
		return false
	}
	changed := false
	if labels := mappingValue(prom, "externalLabels"); labels != nil && labels.Kind == yaml.MappingNode {
		changed = deleteKeyWithValue(labels, "cluster", "management") || changed
		changed = deleteKeyWithValue(labels, "managed_by", "osac") || changed
		if len(labels.Content) == 0 {
			changed = deleteKey(prom, "externalLabels") || changed
		}
	}
	if entries := mappingValue(prom, "remoteWrite"); entries != nil && entries.Kind == yaml.SequenceNode {
		kept := entries.Content[:0]
		for _, entry := range entries.Content {
			if scalarValue(entry, "name") == "osac" {
				changed = true
				continue
			}
			kept = append(kept, entry)
		}
		entries.Content = kept
		if len(entries.Content) == 0 {
			_ = deleteKey(prom, "remoteWrite")
		}
	}
	return changed
}

func remoteWriteEntry(opts Options) *yaml.Node {
	return mappingOf(
		key("name"), scalar("osac"),
		key("url"), scalar(opts.ReceiveURL),
		key("tlsConfig"), mappingOf(
			key("caFile"), scalar(caFile),
		),
		key("writeRelabelConfigs"), relabelConfigs(opts.PlatformNamespaces),
	)
}

// relabelConfigs is IC-1's stamp-and-drop allowlist. The platform namespace
// list comes from values so a deployment that moves OSAC's namespaces changes
// the list rather than the rule.
func relabelConfigs(namespaces []string) *yaml.Node {
	platform := fmt.Sprintf("(%s)", strings.Join(namespaces, "|"))
	return sequenceOf(
		mappingOf(
			key("action"), scalar("labeldrop"),
			key("regex"), scalar("prometheus_replica"),
		),
		mappingOf(
			key("action"), scalar("replace"),
			key("sourceLabels"), sequenceOf(scalar("__name__")),
			key("targetLabel"), scalar("__tmp_keep"),
			key("regex"), scalar("kube_(node|persistentvolume)_.*|kube_persistentvolumeclaim_.*|kube_pod_spec_volumes_persistentvolumeclaims_info"),
			key("replacement"), scalar("keep"),
		),
		mappingOf(
			key("action"), scalar("replace"),
			key("sourceLabels"), sequenceOf(scalar("__name__"), scalar("namespace")),
			key("separator"), scalar(";"),
			key("targetLabel"), scalar("__tmp_keep"),
			key("regex"), scalar("kube_pod_.*;"+platform),
			key("replacement"), scalar("keep"),
		),
		dropNothingKept("kube_.*;"),
		labeldropKeep(),
		mappingOf(
			key("action"), scalar("replace"),
			key("sourceLabels"), sequenceOf(scalar("__name__"), scalar("namespace")),
			key("separator"), scalar(";"),
			key("targetLabel"), scalar("__tmp_keep"),
			key("regex"), scalar("container_.*;"+platform),
			key("replacement"), scalar("keep"),
		),
		dropNothingKept("container_.*;"),
		labeldropKeep(),
		mappingOf(
			key("action"), scalar("drop"),
			key("sourceLabels"), sequenceOf(scalar("__name__")),
			key("regex"), scalar("kserve_.*|vllm_.*|dcgm_.*"),
		),
	)
}

func dropNothingKept(regex string) *yaml.Node {
	return mappingOf(
		key("action"), scalar("drop"),
		key("sourceLabels"), sequenceOf(scalar("__name__"), scalar("__tmp_keep")),
		key("separator"), scalar(";"),
		key("regex"), scalar(regex),
	)
}

func labeldropKeep() *yaml.Node {
	return mappingOf(
		key("action"), scalar("labeldrop"),
		key("regex"), scalar("__tmp_keep"),
	)
}

func documentOf(root *yaml.Node) *yaml.Node {
	return &yaml.Node{Kind: yaml.DocumentNode, Content: []*yaml.Node{root}}
}

func key(value string) *yaml.Node {
	return &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: value}
}

func scalar(value string) *yaml.Node {
	return &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: value}
}

func mappingOf(pairs ...*yaml.Node) *yaml.Node {
	return &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map", Content: pairs}
}

func sequenceOf(items ...*yaml.Node) *yaml.Node {
	return &yaml.Node{Kind: yaml.SequenceNode, Tag: "!!seq", Content: items}
}

func mappingValue(mapping *yaml.Node, name string) *yaml.Node {
	if mapping == nil || mapping.Kind != yaml.MappingNode {
		return nil
	}
	for i := 0; i+1 < len(mapping.Content); i += 2 {
		if mapping.Content[i].Value == name {
			return mapping.Content[i+1]
		}
	}
	return nil
}

func scalarValue(mapping *yaml.Node, name string) string {
	value := mappingValue(mapping, name)
	if value == nil || value.Kind != yaml.ScalarNode {
		return ""
	}
	return value.Value
}

// ensureMapping returns the mapping under name, replacing a null or missing
// value and leaving any other scalar to be reported by the caller's reads.
func ensureMapping(mapping *yaml.Node, name string) *yaml.Node {
	if value := mappingValue(mapping, name); value != nil && value.Kind == yaml.MappingNode {
		return value
	}
	replacement := mappingOf()
	setNode(mapping, name, replacement)
	return replacement
}

func ensureSequence(mapping *yaml.Node, name string) *yaml.Node {
	if value := mappingValue(mapping, name); value != nil && value.Kind == yaml.SequenceNode {
		return value
	}
	replacement := sequenceOf()
	setNode(mapping, name, replacement)
	return replacement
}

func setScalar(mapping *yaml.Node, name, value string) {
	setNode(mapping, name, scalar(value))
}

func setNode(mapping *yaml.Node, name string, value *yaml.Node) {
	if mappingValue(mapping, name) != nil {
		for i := 0; i+1 < len(mapping.Content); i += 2 {
			if mapping.Content[i].Value == name {
				mapping.Content[i+1] = value
				return
			}
		}
	}
	mapping.Content = append(mapping.Content, key(name), value)
}

func deleteKey(mapping *yaml.Node, name string) bool {
	for i := 0; i+1 < len(mapping.Content); i += 2 {
		if mapping.Content[i].Value == name {
			mapping.Content = append(mapping.Content[:i], mapping.Content[i+2:]...)
			return true
		}
	}
	return false
}

// deleteKeyWithValue removes a key only when it still carries the value OSAC
// wrote. The disable path must not strip a label another operator set to a
// different value under the same key.
func deleteKeyWithValue(mapping *yaml.Node, name, value string) bool {
	if scalarValue(mapping, name) != value {
		return false
	}
	return deleteKey(mapping, name)
}

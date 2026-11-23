/*
Copyright 2026.

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

package metricswrite

import (
	"errors"
	"fmt"
	"regexp"
	"strings"

	"gopkg.in/yaml.v3"
)

const (
	// MonitoringNamespace holds the tenant cluster's cluster monitoring config.
	MonitoringNamespace = "openshift-monitoring"
	// MonitoringConfigName and MonitoringConfigKey locate the document the
	// cluster monitoring operator reads.
	MonitoringConfigName = "cluster-monitoring-config"
	MonitoringConfigKey  = "config.yaml"

	// RemoteWriteName is the entry name OSAC owns. Every merge replaces it and
	// every removal deletes only it.
	RemoteWriteName = "osac"

	// CABundleSecretName holds the write CA bundle the tenant cluster verifies
	// the proxy with. ClientCertSecretName and ClientKeySecretName hold the
	// issued client identity.
	CABundleSecretName   = "osac-metrics-write-ca"
	ClientCertSecretName = "osac-metrics-write-cert"
	ClientKeySecretName  = "osac-metrics-write-key"

	// CAKey, CertKey and KeyKey are the Secret data keys cert-manager writes
	// into an issued Secret and the tenant monitoring configuration references.
	CAKey   = "ca.crt"
	CertKey = "tls.crt"
	KeyKey  = "tls.key"

	tmpKeepLabel     = "__tmp_keep"
	kubeFamilyPrefix = "kube_"
)

// ErrRemoteWriteConflict reports a differently named entry that already targets
// the OSAC write URL. Overwriting it would silently redirect another writer's
// samples, so the merge refuses instead.
var ErrRemoteWriteConflict = errors.New("remote-write entry already targets the OSAC write URL")

// MonitoringOptions carries the values the tenant cluster's remote-write entry
// is built from. Every field is required except DropPrefixes.
type MonitoringOptions struct {
	// ClusterName is the hosted cluster name and the value of the cluster
	// external-label hint. The write proxy overrides it from the verified
	// certificate subject, so the hint only affects the cluster's local view.
	ClusterName string
	// WriteURL is the external Route the proxy serves.
	WriteURL string
	// ScrapeInterval is written to prometheusK8s.scrapeInterval.
	ScrapeInterval string
	// KeepKubePrefix is the only kube_ family the cluster forwards.
	KeepKubePrefix string
	// DropPrefixes are the metric-name prefixes the cluster drops before
	// sending. The proxy applies the same rule again; this copy only saves
	// bandwidth.
	DropPrefixes []string
}

func (o MonitoringOptions) validate() error {
	switch {
	case o.ClusterName == "":
		return errors.New("cluster name is required")
	case o.WriteURL == "":
		return errors.New("write URL is required")
	case o.ScrapeInterval == "":
		return errors.New("scrape interval is required")
	case o.KeepKubePrefix == "":
		return errors.New("keep kube prefix is required")
	}
	return nil
}

// MergeMonitoringConfig returns the cluster-monitoring-config document with
// OSAC's keys upserted. It preserves every key OSAC does not own, including a
// pre-existing enableUserWorkload and other remoteWrite entries.
func MergeMonitoringConfig(current []byte, opts MonitoringOptions) ([]byte, error) {
	if err := opts.validate(); err != nil {
		return nil, err
	}
	doc, err := decode(current)
	if err != nil {
		return nil, err
	}
	if doc == nil {
		doc = documentOf(mappingOf())
	}
	if err := upsertOwned(doc.Content[0], opts); err != nil {
		return nil, err
	}
	out, err := yaml.Marshal(doc)
	if err != nil {
		return nil, fmt.Errorf("marshal cluster-monitoring-config: %w", err)
	}
	return out, nil
}

// RemoveMonitoringEntry removes only the osac remoteWrite entry. Other entries
// and every other key stay untouched.
func RemoveMonitoringEntry(current []byte) ([]byte, error) {
	doc, err := decode(current)
	if err != nil {
		return nil, err
	}
	if doc == nil {
		return current, nil
	}
	prometheus := mappingValue(doc.Content[0], "prometheusK8s")
	if prometheus == nil || prometheus.Kind != yaml.MappingNode {
		return current, nil
	}
	entries := mappingValue(prometheus, "remoteWrite")
	if entries == nil || entries.Kind != yaml.SequenceNode {
		return current, nil
	}
	kept := entries.Content[:0]
	changed := false
	for _, entry := range entries.Content {
		if scalarValue(entry, "name") == RemoteWriteName {
			changed = true
			continue
		}
		kept = append(kept, entry)
	}
	if !changed {
		return current, nil
	}
	entries.Content = kept
	if len(entries.Content) == 0 {
		_ = deleteKey(prometheus, "remoteWrite")
	}
	out, err := yaml.Marshal(doc)
	if err != nil {
		return nil, fmt.Errorf("marshal cluster-monitoring-config: %w", err)
	}
	return out, nil
}

func upsertOwned(root *yaml.Node, opts MonitoringOptions) error {
	prometheus := ensureMapping(root, "prometheusK8s")
	setScalar(prometheus, "scrapeInterval", opts.ScrapeInterval)

	labels := ensureMapping(prometheus, "externalLabels")
	setScalar(labels, "cluster", opts.ClusterName)
	setScalar(labels, "managed_by", RemoteWriteName)

	entries := ensureSequence(prometheus, "remoteWrite")
	osacIndex := -1
	for i, entry := range entries.Content {
		name := scalarValue(entry, "name")
		if name == RemoteWriteName {
			if osacIndex < 0 {
				osacIndex = i
			}
			continue
		}
		if scalarValue(entry, "url") == opts.WriteURL {
			return fmt.Errorf("%w: entry %q", ErrRemoteWriteConflict, name)
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
		if i != keep && scalarValue(entry, "name") == RemoteWriteName {
			continue
		}
		kept = append(kept, entry)
	}
	return kept
}

func remoteWriteEntry(opts MonitoringOptions) *yaml.Node {
	return mappingOf(
		key("name"), scalar(RemoteWriteName),
		key("url"), scalar(opts.WriteURL),
		key("tlsConfig"), mappingOf(
			key("ca"), secretSelector(CABundleSecretName, CAKey),
			key("cert"), secretSelector(ClientCertSecretName, CertKey),
			// keySecret is a SecretKeySelector, not a SecretOrConfigMap.
			key("keySecret"), mappingOf(
				key("name"), scalar(ClientKeySecretName),
				key("key"), scalar(KeyKey),
			),
		),
		key("writeRelabelConfigs"), dropRuleConfigs(opts),
	)
}

func secretSelector(name, dataKey string) *yaml.Node {
	return mappingOf(
		key("secret"), mappingOf(
			key("name"), scalar(name),
			key("key"), scalar(dataKey),
		),
	)
}

// dropRuleConfigs renders the cluster-side copy of the write proxy's drop rule.
// Of the kube_ families only the keep prefix survives, and the denied prefixes
// never leave the cluster. A temporary label records a kept kube_ name because
// RE2 has no negative lookahead; the last rule then drops the denied families.
func dropRuleConfigs(opts MonitoringOptions) *yaml.Node {
	rules := []*yaml.Node{
		mappingOf(
			key("action"), scalar("replace"),
			key("sourceLabels"), sequenceOf(scalar("__name__")),
			key("targetLabel"), scalar(tmpKeepLabel),
			key("regex"), scalar("^"+regexp.QuoteMeta(opts.KeepKubePrefix)+".*"),
			key("replacement"), scalar("keep"),
		),
		mappingOf(
			key("action"), scalar("drop"),
			key("sourceLabels"), sequenceOf(scalar("__name__"), scalar(tmpKeepLabel)),
			key("separator"), scalar(";"),
			key("regex"), scalar("^"+regexp.QuoteMeta(kubeFamilyPrefix)+".*;$"),
		),
		mappingOf(
			key("action"), scalar("labeldrop"),
			key("regex"), scalar(tmpKeepLabel),
		),
	}

	prefixes := make([]string, 0, len(opts.DropPrefixes))
	for _, prefix := range opts.DropPrefixes {
		if prefix == "" {
			continue
		}
		prefixes = append(prefixes, regexp.QuoteMeta(prefix))
	}
	if len(prefixes) > 0 {
		rules = append(rules, mappingOf(
			key("action"), scalar("drop"),
			key("sourceLabels"), sequenceOf(scalar("__name__")),
			key("regex"), scalar("^("+strings.Join(prefixes, "|")+").*$"),
		))
	}
	return sequenceOf(rules...)
}

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
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

const testWriteURL = "https://thanos-receive-managed-osac-observability.apps.example.com/api/v1/receive"

func testMonitoringOptions() MonitoringOptions {
	return MonitoringOptions{
		ClusterName:    "prod-east",
		WriteURL:       testWriteURL,
		ScrapeInterval: "30s",
		KeepKubePrefix: "kube_node_",
		DropPrefixes:   []string{"container_", "claim", "mount", "kserve_", "vllm_", "dcgm_"},
	}
}

func decodeDocument(t *testing.T, raw []byte) map[string]any {
	t.Helper()
	out := map[string]any{}
	if err := yaml.Unmarshal(raw, &out); err != nil {
		t.Fatalf("decode merged document: %v\n%s", err, raw)
	}
	return out
}

func mappingAt(t *testing.T, root map[string]any, path ...string) map[string]any {
	t.Helper()
	current := root
	for i, key := range path {
		next, ok := current[key]
		if !ok {
			t.Fatalf("missing key %q in path %v", key, path[:i+1])
		}
		mapped, ok := next.(map[string]any)
		if !ok {
			t.Fatalf("key %q in path %v is %T, want a mapping", key, path[:i+1], next)
		}
		current = mapped
	}
	return current
}

func sequenceAt(t *testing.T, root map[string]any, path ...string) []any {
	t.Helper()
	parent := mappingAt(t, root, path[:len(path)-1]...)
	value, ok := parent[path[len(path)-1]]
	if !ok {
		t.Fatalf("missing key %q", path[len(path)-1])
	}
	sequence, ok := value.([]any)
	if !ok {
		t.Fatalf("key %q is %T, want a sequence", path[len(path)-1], value)
	}
	return sequence
}

func TestMergeMonitoringConfigBuildsRemoteWriteEntry(t *testing.T) {
	out, err := MergeMonitoringConfig(nil, testMonitoringOptions())
	if err != nil {
		t.Fatalf("merge: %v", err)
	}
	root := decodeDocument(t, out)

	prometheus := mappingAt(t, root, "prometheusK8s")
	if got := prometheus["scrapeInterval"]; got != "30s" {
		t.Errorf("scrapeInterval = %v, want 30s", got)
	}
	labels := mappingAt(t, root, "prometheusK8s", "externalLabels")
	if got := labels["cluster"]; got != "prod-east" {
		t.Errorf("externalLabels.cluster = %v, want prod-east", got)
	}
	if got := labels["managed_by"]; got != "osac" {
		t.Errorf("externalLabels.managed_by = %v, want osac", got)
	}

	entries := sequenceAt(t, root, "prometheusK8s", "remoteWrite")
	if len(entries) != 1 {
		t.Fatalf("remoteWrite has %d entries, want 1", len(entries))
	}
	entry, ok := entries[0].(map[string]any)
	if !ok {
		t.Fatalf("remoteWrite[0] is %T, want a mapping", entries[0])
	}
	if got := entry["name"]; got != RemoteWriteName {
		t.Errorf("remoteWrite[0].name = %v, want %v", got, RemoteWriteName)
	}
	if got := entry["url"]; got != testWriteURL {
		t.Errorf("remoteWrite[0].url = %v, want %v", got, testWriteURL)
	}

	tls := mappingAt(t, entry, "tlsConfig")
	ca := mappingAt(t, tls, "ca", "secret")
	if got := ca["name"]; got != CABundleSecretName {
		t.Errorf("tlsConfig.ca.secret.name = %v, want %v", got, CABundleSecretName)
	}
	if got := ca["key"]; got != CAKey {
		t.Errorf("tlsConfig.ca.secret.key = %v, want %v", got, CAKey)
	}
	cert := mappingAt(t, tls, "cert", "secret")
	if got := cert["name"]; got != ClientCertSecretName {
		t.Errorf("tlsConfig.cert.secret.name = %v, want %v", got, ClientCertSecretName)
	}
	if got := cert["key"]; got != CertKey {
		t.Errorf("tlsConfig.cert.secret.key = %v, want %v", got, CertKey)
	}
	// keySecret is a SecretKeySelector, not a SecretOrConfigMap: a nested
	// "secret" key here would leave Prometheus without a client key and the
	// remote-write pipeline would never start.
	key := mappingAt(t, tls, "keySecret")
	if got := key["name"]; got != ClientKeySecretName {
		t.Errorf("tlsConfig.keySecret.name = %v, want %v", got, ClientKeySecretName)
	}
	if got := key["key"]; got != KeyKey {
		t.Errorf("tlsConfig.keySecret.key = %v, want %v", got, KeyKey)
	}
	if _, nested := key["secret"]; nested {
		t.Error("tlsConfig.keySecret must not nest a secret selector")
	}
}

func TestMergeMonitoringConfigAppliesClusterSideDropRule(t *testing.T) {
	out, err := MergeMonitoringConfig(nil, testMonitoringOptions())
	if err != nil {
		t.Fatalf("merge: %v", err)
	}
	root := decodeDocument(t, out)
	entry := sequenceAt(t, root, "prometheusK8s", "remoteWrite")[0].(map[string]any)
	rules := sequenceAt(t, entry, "writeRelabelConfigs")
	if len(rules) != 4 {
		t.Fatalf("writeRelabelConfigs has %d rules, want 4", len(rules))
	}

	keep := rules[0].(map[string]any)
	if got := keep["action"]; got != "replace" {
		t.Errorf("keep rule action = %v, want replace", got)
	}
	if got := keep["targetLabel"]; got != tmpKeepLabel {
		t.Errorf("keep rule targetLabel = %v, want %v", got, tmpKeepLabel)
	}
	if got := keep["regex"]; got != "^kube_node_.*" {
		t.Errorf("keep rule regex = %v, want ^kube_node_.*", got)
	}

	kubeDrop := rules[1].(map[string]any)
	if got := kubeDrop["action"]; got != "drop" {
		t.Errorf("kube family rule action = %v, want drop", got)
	}
	if got := kubeDrop["regex"]; got != `^kube_.*;$` {
		t.Errorf("kube family rule regex = %v", got)
	}

	labelDrop := rules[2].(map[string]any)
	if got := labelDrop["action"]; got != "labeldrop" {
		t.Errorf("temporary label rule action = %v, want labeldrop", got)
	}

	familyDrop := rules[3].(map[string]any)
	if got := familyDrop["action"]; got != "drop" {
		t.Errorf("family drop rule action = %v, want drop", got)
	}
	want := `^(container_|claim|mount|kserve_|vllm_|dcgm_).*$`
	if got := familyDrop["regex"]; got != want {
		t.Errorf("family drop rule regex = %v, want %v", got, want)
	}
}

func TestMergeMonitoringConfigPreservesForeignKeys(t *testing.T) {
	current := []byte(`
enableUserWorkload: true
prometheusK8s:
  retention: 24h
  remoteWrite:
    - name: tenant-feed
      url: https://other.example.com/api/v1/receive
`)
	out, err := MergeMonitoringConfig(current, testMonitoringOptions())
	if err != nil {
		t.Fatalf("merge: %v", err)
	}
	root := decodeDocument(t, out)
	if got := root["enableUserWorkload"]; got != true {
		t.Errorf("enableUserWorkload = %v, want true", got)
	}
	prometheus := mappingAt(t, root, "prometheusK8s")
	if got := prometheus["retention"]; got != "24h" {
		t.Errorf("prometheusK8s.retention = %v, want 24h", got)
	}
	entries := sequenceAt(t, root, "prometheusK8s", "remoteWrite")
	if len(entries) != 2 {
		t.Fatalf("remoteWrite has %d entries, want 2", len(entries))
	}
	if got := entries[0].(map[string]any)["name"]; got != "tenant-feed" {
		t.Errorf("foreign entry was not preserved first: %v", got)
	}
	if got := entries[1].(map[string]any)["name"]; got != RemoteWriteName {
		t.Errorf("osac entry missing after foreign entry: %v", got)
	}
}

func TestMergeMonitoringConfigReplacesExistingEntryIdempotently(t *testing.T) {
	first, err := MergeMonitoringConfig(nil, testMonitoringOptions())
	if err != nil {
		t.Fatalf("first merge: %v", err)
	}
	second, err := MergeMonitoringConfig(first, testMonitoringOptions())
	if err != nil {
		t.Fatalf("second merge: %v", err)
	}
	if string(first) != string(second) {
		t.Errorf("merge is not idempotent:\nfirst:\n%s\nsecond:\n%s", first, second)
	}
	root := decodeDocument(t, second)
	entries := sequenceAt(t, root, "prometheusK8s", "remoteWrite")
	osac := 0
	for _, raw := range entries {
		if raw.(map[string]any)["name"] == RemoteWriteName {
			osac++
		}
	}
	if osac != 1 {
		t.Errorf("remoteWrite has %d osac entries, want 1", osac)
	}
}

func TestMergeMonitoringConfigRejectsForeignEntryOnSameURL(t *testing.T) {
	current := []byte(`
prometheusK8s:
  remoteWrite:
    - name: another-writer
      url: ` + testWriteURL + `
`)
	_, err := MergeMonitoringConfig(current, testMonitoringOptions())
	if err == nil {
		t.Fatal("merge succeeded, want a conflict error")
	}
	if !strings.Contains(err.Error(), "another-writer") {
		t.Errorf("error %q does not name the conflicting entry", err)
	}
}

func TestMergeMonitoringConfigRejectsMissingOptions(t *testing.T) {
	tests := map[string]func(*MonitoringOptions){
		"cluster name":    func(o *MonitoringOptions) { o.ClusterName = "" },
		"write URL":       func(o *MonitoringOptions) { o.WriteURL = "" },
		"scrape interval": func(o *MonitoringOptions) { o.ScrapeInterval = "" },
		"keep prefix":     func(o *MonitoringOptions) { o.KeepKubePrefix = "" },
	}
	for name, mutate := range tests {
		t.Run(name, func(t *testing.T) {
			opts := testMonitoringOptions()
			mutate(&opts)
			if _, err := MergeMonitoringConfig(nil, opts); err == nil {
				t.Fatal("merge succeeded, want an error")
			}
		})
	}
}

func TestMergeMonitoringConfigRejectsMalformedDocument(t *testing.T) {
	if _, err := MergeMonitoringConfig([]byte("just a string"), testMonitoringOptions()); err == nil {
		t.Fatal("merge succeeded, want a parse error")
	}
}

func TestRemoveMonitoringEntryKeepsForeignContent(t *testing.T) {
	current := []byte(`
enableUserWorkload: true
prometheusK8s:
  retention: 24h
  remoteWrite:
    - name: tenant-feed
      url: https://other.example.com/api/v1/receive
    - name: osac
      url: ` + testWriteURL + `
`)
	out, err := RemoveMonitoringEntry(current, "prod-east")
	if err != nil {
		t.Fatalf("remove: %v", err)
	}
	root := decodeDocument(t, out)
	if got := root["enableUserWorkload"]; got != true {
		t.Errorf("enableUserWorkload = %v, want true", got)
	}
	prometheus := mappingAt(t, root, "prometheusK8s")
	if got := prometheus["retention"]; got != "24h" {
		t.Errorf("prometheusK8s.retention = %v, want 24h", got)
	}
	entries := sequenceAt(t, root, "prometheusK8s", "remoteWrite")
	if len(entries) != 1 {
		t.Fatalf("remoteWrite has %d entries, want 1", len(entries))
	}
	if got := entries[0].(map[string]any)["name"]; got != "tenant-feed" {
		t.Errorf("remove kept %v, want tenant-feed", got)
	}
}

func TestRemoveMonitoringEntryDropsOnlyTheLabelsOSACSet(t *testing.T) {
	current := []byte(`
prometheusK8s:
  externalLabels:
    cluster: prod-east
    managed_by: osac
    site: east
  remoteWrite:
    - name: osac
      url: ` + testWriteURL + `
`)
	out, err := RemoveMonitoringEntry(current, "prod-east")
	if err != nil {
		t.Fatalf("remove: %v", err)
	}
	root := decodeDocument(t, out)
	labels := mappingAt(t, root, "prometheusK8s", "externalLabels")
	for _, name := range []string{"cluster", "managed_by"} {
		if value, exists := labels[name]; exists {
			t.Errorf("externalLabels.%s survived removal: %v", name, value)
		}
	}
	if got := labels["site"]; got != "east" {
		t.Errorf("externalLabels.site = %v, want east", got)
	}
}

func TestRemoveMonitoringEntryKeepsLabelsAnotherWriterSet(t *testing.T) {
	current := []byte(`
prometheusK8s:
  externalLabels:
    cluster: shared
    managed_by: another-writer
`)
	out, err := RemoveMonitoringEntry(current, "prod-east")
	if err != nil {
		t.Fatalf("remove: %v", err)
	}
	root := decodeDocument(t, out)
	labels := mappingAt(t, root, "prometheusK8s", "externalLabels")
	if got := labels["cluster"]; got != "shared" {
		t.Errorf("externalLabels.cluster = %v, want shared", got)
	}
	if got := labels["managed_by"]; got != "another-writer" {
		t.Errorf("externalLabels.managed_by = %v, want another-writer", got)
	}
}

func TestRemoveMonitoringEntryDeletesEmptyRemoteWriteList(t *testing.T) {
	current := []byte(`
prometheusK8s:
  remoteWrite:
    - name: osac
      url: ` + testWriteURL + `
`)
	out, err := RemoveMonitoringEntry(current, "prod-east")
	if err != nil {
		t.Fatalf("remove: %v", err)
	}
	root := decodeDocument(t, out)
	prometheus := mappingAt(t, root, "prometheusK8s")
	if _, exists := prometheus["remoteWrite"]; exists {
		t.Errorf("remoteWrite key survived removal: %v", prometheus["remoteWrite"])
	}
}

func TestRemoveMonitoringEntryLeavesEmptyDocumentAlone(t *testing.T) {
	out, err := RemoveMonitoringEntry(nil, "prod-east")
	if err != nil {
		t.Fatalf("remove: %v", err)
	}
	if string(out) != "" {
		t.Errorf("remove changed an empty document: %q", out)
	}
}

func TestRemoveMonitoringEntryRejectsMalformedDocument(t *testing.T) {
	if _, err := RemoveMonitoringEntry([]byte("just a string"), "prod-east"); err == nil {
		t.Fatal("remove succeeded, want a parse error")
	}
}

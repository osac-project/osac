package remotewrite

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

const (
	testReceiveURL = "https://thanos-receive.osac-observability.svc.cluster.local:19291/api/v1/receive"
	testNamespace  = "(osac|osac-csi|osac-observability)"
)

func testOptions(enabled bool) Options {
	return Options{
		Enabled:            enabled,
		ScrapeInterval:     "30s",
		ReceiveURL:         testReceiveURL,
		PlatformNamespaces: []string{"osac", "osac-csi", "osac-observability"},
	}
}

func readFixture(t *testing.T, name string) []byte {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("testdata", name))
	if err != nil {
		t.Fatalf("read fixture %s: %v", name, err)
	}
	return data
}

// parseDocument decodes YAML into the generic tree the assertions compare, so
// map key order never matters but list order always does.
func parseDocument(t *testing.T, data []byte) map[string]any {
	t.Helper()
	var doc map[string]any
	if err := yaml.Unmarshal(data, &doc); err != nil {
		t.Fatalf("parse document: %v\n%s", err, data)
	}
	return doc
}

func prometheusK8s(t *testing.T, data []byte) map[string]any {
	t.Helper()
	doc := parseDocument(t, data)
	prom, ok := doc["prometheusK8s"].(map[string]any)
	if !ok {
		t.Fatalf("no prometheusK8s block in:\n%s", data)
	}
	return prom
}

// expectedPayload is IC-1 verbatim, with the platform namespace list rendered
// from the options.
func expectedPayload() map[string]any {
	raw := fmt.Sprintf(`prometheusK8s:
  scrapeInterval: 30s
  externalLabels:
    cluster: management
    managed_by: osac
  remoteWrite:
    - name: osac
      url: %s
      tlsConfig:
        caFile: /etc/prometheus/configmaps/service-ca-bundle/service-ca.crt
      writeRelabelConfigs:
        - action: labeldrop
          regex: prometheus_replica
        - action: replace
          sourceLabels: [__name__]
          targetLabel: __tmp_keep
          regex: 'kube_(node|persistentvolume)_.*|kube_persistentvolumeclaim_.*|kube_pod_spec_volumes_persistentvolumeclaims_info'
          replacement: keep
        - action: replace
          sourceLabels: [__name__, namespace]
          separator: ';'
          targetLabel: __tmp_keep
          regex: 'kube_pod_.*;%s'
          replacement: keep
        - action: drop
          sourceLabels: [__name__, __tmp_keep]
          separator: ';'
          regex: 'kube_.*;'
        - action: labeldrop
          regex: __tmp_keep
        - action: replace
          sourceLabels: [__name__, namespace]
          separator: ';'
          targetLabel: __tmp_keep
          regex: 'container_.*;%s'
          replacement: keep
        - action: drop
          sourceLabels: [__name__, __tmp_keep]
          separator: ';'
          regex: 'container_.*;'
        - action: labeldrop
          regex: __tmp_keep
        - action: drop
          sourceLabels: [__name__]
          regex: kserve_.*|vllm_.*|dcgm_.*
`, testReceiveURL, testNamespace, testNamespace)
	var doc map[string]any
	if err := yaml.Unmarshal([]byte(raw), &doc); err != nil {
		panic(err)
	}
	return doc
}

func TestMergeEmptyEnabledWritesTheFullPayload(t *testing.T) {
	out, err := Merge(nil, testOptions(true))
	if err != nil {
		t.Fatalf("Merge: %v", err)
	}
	got := parseDocument(t, out)
	want := expectedPayload()
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("payload mismatch\n got: %#v\nwant: %#v", got, want)
	}
}

func TestMergePreservesUnrelatedKeys(t *testing.T) {
	out, err := Merge(readFixture(t, "unrelated.yaml"), testOptions(true))
	if err != nil {
		t.Fatalf("Merge: %v", err)
	}
	doc := parseDocument(t, out)
	if _, ok := doc["alertmanagerMain"]; !ok {
		t.Fatalf("alertmanagerMain was dropped:\n%s", out)
	}
	prom := prometheusK8s(t, out)
	if prom["retention"] != "15d" {
		t.Fatalf("retention changed to %v", prom["retention"])
	}
	if prom["scrapeInterval"] != "30s" {
		t.Fatalf("scrapeInterval = %v", prom["scrapeInterval"])
	}
	labels := prom["externalLabels"].(map[string]any)
	if labels["cluster"] != "management" || labels["managed_by"] != "osac" {
		t.Fatalf("external labels = %v", labels)
	}
}

func TestMergeNeverWritesUserWorkloadSettings(t *testing.T) {
	out, err := Merge(readFixture(t, "user-workload.yaml"), testOptions(true))
	if err != nil {
		t.Fatalf("Merge: %v", err)
	}
	prom := prometheusK8s(t, out)
	if prom["enableUserWorkload"] != true {
		t.Fatalf("enableUserWorkload was not preserved:\n%s", out)
	}
	if _, ok := prom["userWorkloadMonitoring"]; ok {
		t.Fatalf("userWorkloadMonitoring was written:\n%s", out)
	}
}

func TestMergeRejectsADifferentNameForTheReceiveURL(t *testing.T) {
	current := readFixture(t, "conflict.yaml")
	out, err := Merge(current, testOptions(true))
	if !errors.Is(err, ErrConflict) {
		t.Fatalf("error = %v, want ErrConflict", err)
	}
	if !strings.Contains(err.Error(), `remoteWrite entry "guest" already targets the OSAC Thanos Receive URL`) {
		t.Fatalf("error text %q lacks the fixed message", err.Error())
	}
	if string(out) != string(current) {
		t.Fatalf("conflict changed the document:\n%s", out)
	}
}

func TestMergeReplacesAStaleOSACEntry(t *testing.T) {
	out, err := Merge(readFixture(t, "stale-osac.yaml"), testOptions(true))
	if err != nil {
		t.Fatalf("Merge: %v", err)
	}
	prom := prometheusK8s(t, out)
	entries := prom["remoteWrite"].([]any)
	if len(entries) != 2 {
		t.Fatalf("remoteWrite has %d entries, want 2:\n%s", len(entries), out)
	}
	first := entries[0].(map[string]any)
	if first["name"] != "osac" || first["url"] != testReceiveURL {
		t.Fatalf("osac entry = %v", first)
	}
	second := entries[1].(map[string]any)
	if second["name"] != "guest" || second["url"] != "https://guest.example.com/api/v1/receive" {
		t.Fatalf("guest entry = %v", second)
	}
}

func TestMergeIsIdempotent(t *testing.T) {
	first, err := Merge(readFixture(t, "unrelated.yaml"), testOptions(true))
	if err != nil {
		t.Fatalf("first Merge: %v", err)
	}
	second, err := Merge(first, testOptions(true))
	if err != nil {
		t.Fatalf("second Merge: %v", err)
	}
	if string(first) != string(second) {
		t.Fatalf("second merge changed the document:\nfirst:\n%s\nsecond:\n%s", first, second)
	}
}

func TestMergeDisabledRemovesOnlyOwnedKeys(t *testing.T) {
	out, err := Merge(readFixture(t, "disabled.yaml"), testOptions(false))
	if err != nil {
		t.Fatalf("Merge: %v", err)
	}
	want := parseDocument(t, []byte(`prometheusK8s:
  retention: 15d
  scrapeInterval: 1m
  enableUserWorkload: true
  externalLabels:
    region: us-east-1
  remoteWrite:
    - name: guest
      url: https://guest.example.com/api/v1/receive
alertmanagerMain: {}
`))
	got := parseDocument(t, out)
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("disabled document mismatch\n got: %#v\nwant: %#v", got, want)
	}
}

func TestMergeDisabledWithoutAConfigMapIsANoOp(t *testing.T) {
	out, err := Merge(nil, testOptions(false))
	if err != nil {
		t.Fatalf("Merge: %v", err)
	}
	if len(out) != 0 {
		t.Fatalf("expected empty output, got:\n%s", out)
	}
}

func TestMergeDisabledWithoutOwnedKeysLeavesTheDocumentAlone(t *testing.T) {
	current := readFixture(t, "unrelated.yaml")
	out, err := Merge(current, testOptions(false))
	if err != nil {
		t.Fatalf("Merge: %v", err)
	}
	if string(out) != string(current) {
		t.Fatalf("document changed:\n%s", out)
	}
}

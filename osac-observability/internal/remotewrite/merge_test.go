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
  externalLabels:
    osac_cluster: management
    managed_by: osac
  remoteWrite:
    - name: osac
      url: %s
      tlsConfig:
        ca:
          configMap:
            name: serving-certs-ca-bundle
            key: service-ca.crt
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

// TestMergeEnabledAvoidsReservedExternalLabels pins that the enable path stays
// clear of the external-label keys the OpenShift monitoring ConfigMap webhook
// reserves. A document that sets any of them is refused outright -- the webhook
// denies the whole ConfigMap update -- so the management remote write never
// installs. The cluster label OSAC wants instead uses its own prefixed key.
func TestMergeEnabledAvoidsReservedExternalLabels(t *testing.T) {
	out, err := Merge(nil, testOptions(true))
	if err != nil {
		t.Fatalf("Merge: %v", err)
	}
	labels := prometheusK8s(t, out)["externalLabels"].(map[string]any)
	for _, reserved := range []string{"prometheus", "prometheus_replica", "cluster"} {
		if _, ok := labels[reserved]; ok {
			t.Fatalf("externalLabels sets reserved key %q: %v", reserved, labels)
		}
	}
	if labels["osac_cluster"] != "management" {
		t.Fatalf("externalLabels = %v, want osac_cluster: management", labels)
	}
}

// TestMergeEnabledTrustsTheServiceCAThroughAConfigMapReference pins the TLS
// shape the OpenShift monitoring webhook accepts. It rejects unknown fields,
// so the upstream Prometheus `caFile` spelling fails the whole ConfigMap
// update instead of taking effect. The CA must be named by SecretOrConfigMap
// reference; Prometheus already mounts serving-certs-ca-bundle from the
// service CA, so the operator reuses that mount for Thanos Receive.
func TestMergeEnabledTrustsTheServiceCAThroughAConfigMapReference(t *testing.T) {
	out, err := Merge(nil, testOptions(true))
	if err != nil {
		t.Fatalf("Merge: %v", err)
	}
	entry := prometheusK8s(t, out)["remoteWrite"].([]any)[0].(map[string]any)
	tls, ok := entry["tlsConfig"].(map[string]any)
	if !ok {
		t.Fatalf("remoteWrite entry has no tlsConfig: %v", entry)
	}
	if _, ok := tls["caFile"]; ok {
		t.Fatalf("tlsConfig uses the rejected caFile field: %v", tls)
	}
	ca, ok := tls["ca"].(map[string]any)
	if !ok {
		t.Fatalf("tlsConfig has no ca reference: %v", tls)
	}
	cm, ok := ca["configMap"].(map[string]any)
	if !ok {
		t.Fatalf("tlsConfig.ca is not a configMap reference: %v", ca)
	}
	if cm["name"] != "serving-certs-ca-bundle" || cm["key"] != "service-ca.crt" {
		t.Fatalf("tlsConfig.ca configMap = %v, want serving-certs-ca-bundle/service-ca.crt", cm)
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
	if value, ok := prom["scrapeInterval"]; ok {
		t.Fatalf("scrapeInterval was written: %v", value)
	}
	labels := prom["externalLabels"].(map[string]any)
	if labels["osac_cluster"] != "management" || labels["managed_by"] != "osac" {
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

// TestMergeDisabledKeepsLabelsOSACDoesNotOwn pins that the disable path only
// removes the label values OSAC wrote. A cluster whose external labels already
// name another cluster or owner must keep them even though the same key names
// appear.
func TestMergeDisabledKeepsLabelsOSACDoesNotOwn(t *testing.T) {
	out, err := Merge(readFixture(t, "foreign-labels.yaml"), testOptions(false))
	if err != nil {
		t.Fatalf("Merge: %v", err)
	}
	want := parseDocument(t, []byte(`prometheusK8s:
  retention: 15d
  scrapeInterval: 1m
  externalLabels:
    osac_cluster: prod-east
    managed_by: platform-team
    region: us-east-1
  remoteWrite:
    - name: guest
      url: https://guest.example.com/api/v1/receive
`))
	got := parseDocument(t, out)
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("disabled document mismatch\n got: %#v\nwant: %#v", got, want)
	}
}

// TestMergeDisabledLeavesForeignLabelsWhenNothingElseIsOwned proves the label
// removal is value gated on its own: there is no osac entry whose removal could
// mask a changed document.
func TestMergeDisabledLeavesForeignLabelsWhenNothingElseIsOwned(t *testing.T) {
	current := []byte(`prometheusK8s:
  externalLabels:
    osac_cluster: prod-east
    managed_by: platform-team
`)
	out, err := Merge(current, testOptions(false))
	if err != nil {
		t.Fatalf("Merge: %v", err)
	}
	if string(out) != string(current) {
		t.Fatalf("document changed:\n%s", out)
	}
}

// TestMergeLeavesTheScrapeIntervalAlone pins that neither direction touches
// prometheusK8s.scrapeInterval. The OpenShift monitoring webhook rejects that
// field as unknown, so writing it fails the whole ConfigMap update, and the
// platform Prometheus already scrapes every 30s of its own accord.
func TestMergeLeavesTheScrapeIntervalAlone(t *testing.T) {
	current := []byte(`prometheusK8s:
  scrapeInterval: 15s
`)
	enabled, err := Merge(current, testOptions(true))
	if err != nil {
		t.Fatalf("Merge enabled: %v", err)
	}
	if got := prometheusK8s(t, enabled)["scrapeInterval"]; got != "15s" {
		t.Fatalf("enable changed scrapeInterval to %v", got)
	}
	disabled, err := Merge(enabled, testOptions(false))
	if err != nil {
		t.Fatalf("Merge disabled: %v", err)
	}
	want := parseDocument(t, current)
	if got := parseDocument(t, disabled); !reflect.DeepEqual(got, want) {
		t.Fatalf("disable left a different document\n got: %#v\nwant: %#v", got, want)
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

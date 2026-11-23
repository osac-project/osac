package main

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/osac-project/osac/osac-observability/internal/remotewrite"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/kubernetes/fake"
	k8stesting "k8s.io/client-go/testing"
)

func testConfig(enabled bool) config {
	return config{
		Enabled:            enabled,
		ScrapeInterval:     "30s",
		ReceiveURL:         defaultReceiveURL,
		PlatformNamespaces: []string{"osac", "osac-csi", "osac-observability"},
		Namespace:          defaultNamespace,
		Name:               defaultName,
	}
}

func existingConfigMap(configYAML string) *corev1.ConfigMap {
	return &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{Name: defaultName, Namespace: defaultNamespace},
		Data:       map[string]string{dataKey: configYAML},
	}
}

func TestRunLeavesAMissingConfigMapAloneWhenDisabled(t *testing.T) {
	client := fake.NewSimpleClientset()
	if err := run(context.Background(), client, testConfig(false)); err != nil {
		t.Fatalf("run: %v", err)
	}
	_, err := client.CoreV1().ConfigMaps(defaultNamespace).Get(context.Background(), defaultName, metav1.GetOptions{})
	if !apierrors.IsNotFound(err) {
		t.Fatalf("expected no ConfigMap, got error %v", err)
	}
}

// TestRunFailsWhenTheConfigMapIsMissing pins that cluster monitoring owns the
// ConfigMap. Without it there is nowhere to enable remote write, and inventing
// one would make the install look successful while the cluster scrapes nothing.
func TestRunFailsWhenTheConfigMapIsMissing(t *testing.T) {
	client := fake.NewSimpleClientset()
	err := run(context.Background(), client, testConfig(true))
	if err == nil {
		t.Fatal("expected an error for a missing cluster-monitoring-config")
	}
	if !strings.Contains(err.Error(), "cluster-monitoring-config") {
		t.Fatalf("error does not name the ConfigMap: %v", err)
	}
	for _, action := range client.Actions() {
		if action.GetVerb() == "create" {
			t.Fatalf("run created %s %s", action.GetResource().Resource, action.GetSubresource())
		}
	}
}

func TestRunPreservesOtherDataKeysAndUserWorkload(t *testing.T) {
	client := fake.NewSimpleClientset(&corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{Name: defaultName, Namespace: defaultNamespace},
		Data: map[string]string{
			dataKey:    "prometheusK8s:\n  enableUserWorkload: true\n",
			"other.md": "untouched",
		},
	})
	if err := run(context.Background(), client, testConfig(true)); err != nil {
		t.Fatalf("run: %v", err)
	}
	cm, err := client.CoreV1().ConfigMaps(defaultNamespace).Get(context.Background(), defaultName, metav1.GetOptions{})
	if err != nil {
		t.Fatalf("get ConfigMap: %v", err)
	}
	if cm.Data["other.md"] != "untouched" {
		t.Fatalf("other data key changed: %v", cm.Data)
	}
	if !strings.Contains(cm.Data[dataKey], "enableUserWorkload: true") {
		t.Fatalf("enableUserWorkload was not preserved:\n%s", cm.Data[dataKey])
	}
	if strings.Contains(cm.Data[dataKey], "userWorkloadMonitoring") {
		t.Fatalf("userWorkloadMonitoring was written:\n%s", cm.Data[dataKey])
	}
}

func TestRunStopsOnAConflictWithoutUpdating(t *testing.T) {
	current := "prometheusK8s:\n  remoteWrite:\n    - name: guest\n      url: " + defaultReceiveURL + "\n"
	client := fake.NewSimpleClientset(existingConfigMap(current))
	err := run(context.Background(), client, testConfig(true))
	if !errors.Is(err, remotewrite.ErrConflict) {
		t.Fatalf("error = %v, want ErrConflict", err)
	}
	for _, action := range client.Actions() {
		if action.GetVerb() == "update" {
			t.Fatalf("a conflict still issued an update: %v", action)
		}
	}
	cm, getErr := client.CoreV1().ConfigMaps(defaultNamespace).Get(context.Background(), defaultName, metav1.GetOptions{})
	if getErr != nil {
		t.Fatalf("get ConfigMap: %v", getErr)
	}
	if cm.Data[dataKey] != current {
		t.Fatalf("ConfigMap changed on conflict:\n%s", cm.Data[dataKey])
	}
}

func TestRunRemovesOnlyOwnedKeysWhenDisabled(t *testing.T) {
	current := "prometheusK8s:\n  enableUserWorkload: true\n  externalLabels:\n    cluster: management\n    managed_by: osac\n  remoteWrite:\n    - name: osac\n      url: https://old.example.com/api/v1/receive\n    - name: guest\n      url: https://guest.example.com/api/v1/receive\n"
	client := fake.NewSimpleClientset(existingConfigMap(current))
	if err := run(context.Background(), client, testConfig(false)); err != nil {
		t.Fatalf("run: %v", err)
	}
	cm, err := client.CoreV1().ConfigMaps(defaultNamespace).Get(context.Background(), defaultName, metav1.GetOptions{})
	if err != nil {
		t.Fatalf("get ConfigMap: %v", err)
	}
	got := cm.Data[dataKey]
	for _, unwanted := range []string{"cluster: management", "managed_by: osac", "name: osac"} {
		if strings.Contains(got, unwanted) {
			t.Fatalf("%q survived the disable path:\n%s", unwanted, got)
		}
	}
	for _, wanted := range []string{"enableUserWorkload: true", "name: guest"} {
		if !strings.Contains(got, wanted) {
			t.Fatalf("%q did not survive the disable path:\n%s", wanted, got)
		}
	}
}

func TestRunDoesNotWriteWhenNothingChanged(t *testing.T) {
	current := "prometheusK8s:\n  retention: 15d\n"
	client := fake.NewSimpleClientset(existingConfigMap(current))
	if err := run(context.Background(), client, testConfig(false)); err != nil {
		t.Fatalf("run: %v", err)
	}
	for _, action := range client.Actions() {
		if action.GetVerb() == "update" || action.GetVerb() == "patch" {
			t.Fatalf("no-op disable path wrote to the ConfigMap: %v", action)
		}
	}
}

const previousIntervalKey = "osac.openshift.io/prometheus-scrape-interval"

func TestRunRemembersThePreviousScrapeInterval(t *testing.T) {
	client := fake.NewSimpleClientset(existingConfigMap(
		"prometheusK8s:\n  scrapeInterval: 15s\n"))
	if err := run(context.Background(), client, testConfig(true)); err != nil {
		t.Fatalf("run: %v", err)
	}
	cm, err := client.CoreV1().ConfigMaps(defaultNamespace).Get(context.Background(), defaultName, metav1.GetOptions{})
	if err != nil {
		t.Fatalf("get ConfigMap: %v", err)
	}
	if got := cm.Annotations[previousIntervalKey]; got != "15s" {
		t.Fatalf("remembered interval = %q, want 15s", got)
	}
	if !strings.Contains(cm.Data[dataKey], "scrapeInterval: 30s") {
		t.Fatalf("scrapeInterval was not overwritten:\n%s", cm.Data[dataKey])
	}
}

// TestRunKeepsTheFirstRememberedInterval pins that a second enable is not
// mistaken for a fresh one: the recorded value must stay the pre-OSAC value,
// never OSAC's own interval.
func TestRunKeepsTheFirstRememberedInterval(t *testing.T) {
	cm := existingConfigMap("prometheusK8s:\n  scrapeInterval: 30s\n")
	cm.Annotations = map[string]string{previousIntervalKey: "15s"}
	client := fake.NewSimpleClientset(cm)
	if err := run(context.Background(), client, testConfig(true)); err != nil {
		t.Fatalf("run: %v", err)
	}
	got, err := client.CoreV1().ConfigMaps(defaultNamespace).Get(context.Background(), defaultName, metav1.GetOptions{})
	if err != nil {
		t.Fatalf("get ConfigMap: %v", err)
	}
	if got.Annotations[previousIntervalKey] != "15s" {
		t.Fatalf("remembered interval = %q, want 15s", got.Annotations[previousIntervalKey])
	}
}

// TestRunRetriesAConflictAndKeepsTheRememberedInterval reproduces a race with
// another writer: the first update conflicts, the fresh read already shows a
// different interval, and the retry still records the value the first read saw.
func TestRunRetriesAConflictAndKeepsTheRememberedInterval(t *testing.T) {
	client := fake.NewSimpleClientset(existingConfigMap(
		"prometheusK8s:\n  scrapeInterval: 15s\n"))
	updates := 0
	client.PrependReactor("update", "configmaps", func(action k8stesting.Action) (bool, runtime.Object, error) {
		updates++
		if updates > 1 {
			return false, nil, nil
		}
		// Simulate the other writer: the stored object now carries a different
		// interval and no remembered key.
		cm := action.(k8stesting.UpdateAction).GetObject().(*corev1.ConfigMap).DeepCopy()
		cm.Data[dataKey] = "prometheusK8s:\n  scrapeInterval: 20s\n"
		cm.Annotations = map[string]string{}
		if err := client.Tracker().Update(
			schema.GroupVersionResource{Version: "v1", Resource: "configmaps"},
			cm, defaultNamespace); err != nil {
			t.Fatalf("seed the conflicting ConfigMap: %v", err)
		}
		return true, nil, apierrors.NewConflict(
			schema.GroupResource{Resource: "configmaps"}, defaultName,
			errors.New("the object has been modified"))
	})
	if err := run(context.Background(), client, testConfig(true)); err != nil {
		t.Fatalf("run: %v", err)
	}
	if updates != 2 {
		t.Fatalf("update attempts = %d, want 2", updates)
	}
	cm, err := client.CoreV1().ConfigMaps(defaultNamespace).Get(context.Background(), defaultName, metav1.GetOptions{})
	if err != nil {
		t.Fatalf("get ConfigMap: %v", err)
	}
	if got := cm.Annotations[previousIntervalKey]; got != "15s" {
		t.Fatalf("remembered interval = %q, want the first read's 15s", got)
	}
	if !strings.Contains(cm.Data[dataKey], "scrapeInterval: 30s") {
		t.Fatalf("scrapeInterval was not written:\n%s", cm.Data[dataKey])
	}
}

func TestRunRestoresTheRememberedScrapeIntervalOnDisable(t *testing.T) {
	cm := existingConfigMap("prometheusK8s:\n  scrapeInterval: 30s\n  remoteWrite:\n    - name: osac\n      url: https://old.example.com/api/v1/receive\n")
	cm.Annotations = map[string]string{previousIntervalKey: "15s"}
	client := fake.NewSimpleClientset(cm)
	if err := run(context.Background(), client, testConfig(false)); err != nil {
		t.Fatalf("run: %v", err)
	}
	got, err := client.CoreV1().ConfigMaps(defaultNamespace).Get(context.Background(), defaultName, metav1.GetOptions{})
	if err != nil {
		t.Fatalf("get ConfigMap: %v", err)
	}
	if !strings.Contains(got.Data[dataKey], "scrapeInterval: 15s") {
		t.Fatalf("previous interval was not restored:\n%s", got.Data[dataKey])
	}
	if _, ok := got.Annotations[previousIntervalKey]; ok {
		t.Fatalf("remembered key survived the disable: %v", got.Annotations)
	}
}

func TestRunDeletesTheScrapeIntervalItAddedOnDisable(t *testing.T) {
	client := fake.NewSimpleClientset(existingConfigMap(
		"prometheusK8s:\n  scrapeInterval: 30s\n  remoteWrite:\n    - name: osac\n      url: https://old.example.com/api/v1/receive\n"))
	if err := run(context.Background(), client, testConfig(false)); err != nil {
		t.Fatalf("run: %v", err)
	}
	got, err := client.CoreV1().ConfigMaps(defaultNamespace).Get(context.Background(), defaultName, metav1.GetOptions{})
	if err != nil {
		t.Fatalf("get ConfigMap: %v", err)
	}
	if strings.Contains(got.Data[dataKey], "scrapeInterval") {
		t.Fatalf("scrapeInterval survived the disable:\n%s", got.Data[dataKey])
	}
}

func TestLoadConfigDefaults(t *testing.T) {
	cfg, err := loadConfig(func(string) string { return "" })
	if err != nil {
		t.Fatalf("loadConfig: %v", err)
	}
	if cfg.Enabled || cfg.Namespace != defaultNamespace || cfg.Name != defaultName {
		t.Fatalf("unexpected defaults: %+v", cfg)
	}
	if cfg.ReceiveURL != defaultReceiveURL || cfg.ScrapeInterval != defaultScrapeInterval {
		t.Fatalf("unexpected payload defaults: %+v", cfg)
	}
	if len(cfg.PlatformNamespaces) != 3 {
		t.Fatalf("platform namespaces = %v", cfg.PlatformNamespaces)
	}
}

func TestLoadConfigRejectsMalformedNamespaces(t *testing.T) {
	_, err := loadConfig(func(name string) string {
		if name == "OBS_PLATFORM_NAMESPACES" {
			return "osac"
		}
		return ""
	})
	if err == nil {
		t.Fatal("expected an error for a non-JSON namespace list")
	}
}

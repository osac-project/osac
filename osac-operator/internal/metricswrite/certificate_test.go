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

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
)

func readyIssuer(status map[string]any) *unstructured.Unstructured {
	issuer := &unstructured.Unstructured{Object: map[string]any{
		"apiVersion": "cert-manager.io/v1",
		"kind":       "Issuer",
		"metadata":   map[string]any{"name": "osac-metrics-write-ca", "namespace": "osac-observability"},
	}}
	if status != nil {
		issuer.Object["status"] = status
	}
	return issuer
}

func TestIssuerReady(t *testing.T) {
	tests := map[string]struct {
		issuer *unstructured.Unstructured
		want   bool
	}{
		"ready": {
			issuer: readyIssuer(map[string]any{"conditions": []any{
				map[string]any{"type": "Ready", "status": "True"},
			}}),
			want: true,
		},
		"not ready": {
			issuer: readyIssuer(map[string]any{"conditions": []any{
				map[string]any{"type": "Ready", "status": "False"},
			}}),
			want: false,
		},
		"ready unknown": {
			issuer: readyIssuer(map[string]any{"conditions": []any{
				map[string]any{"type": "Ready", "status": "Unknown"},
			}}),
			want: false,
		},
		"no conditions": {issuer: readyIssuer(nil), want: false},
		"other condition only": {
			issuer: readyIssuer(map[string]any{"conditions": []any{
				map[string]any{"type": "Ready", "status": "True"},
				map[string]any{"type": "Verified", "status": "False"},
			}}),
			want: true,
		},
		"malformed conditions": {
			issuer: readyIssuer(map[string]any{"conditions": "not a list"}),
			want:   false,
		},
		"nil issuer": {issuer: nil, want: false},
	}
	for name, testCase := range tests {
		t.Run(name, func(t *testing.T) {
			if got := IssuerReady(testCase.issuer); got != testCase.want {
				t.Errorf("IssuerReady() = %v, want %v", got, testCase.want)
			}
		})
	}
}

func testCertificateOptions() CertificateOptions {
	return CertificateOptions{
		Namespace:      "osac-observability",
		OrderName:      "prod-east",
		OrderNamespace: "osac-orders",
		OrderUID:       "6f1c2a4e",
		CommonName:     "prod-east-abc12",
		Issuer:         "osac-metrics-write-ca",
		Duration:       "90d",
		RenewBefore:    "30d",
	}
}

func TestClientCertificate(t *testing.T) {
	cert, err := ClientCertificate(testCertificateOptions())
	if err != nil {
		t.Fatalf("build certificate: %v", err)
	}
	if got := cert.GetAPIVersion(); got != "cert-manager.io/v1" {
		t.Errorf("apiVersion = %q", got)
	}
	if got := cert.GetKind(); got != "Certificate" {
		t.Errorf("kind = %q", got)
	}
	if got := cert.GetName(); got != "osac-metrics-write-prod-east" {
		t.Errorf("name = %q", got)
	}
	if got := cert.GetNamespace(); got != "osac-observability" {
		t.Errorf("namespace = %q", got)
	}
	if got := cert.GetLabels()[ClusterOrderLabel]; got != "prod-east" {
		t.Errorf("label %q = %q", ClusterOrderLabel, got)
	}
	if got := cert.GetAnnotations()[ClusterOrderUIDAnnotation]; got != "6f1c2a4e" {
		t.Errorf("annotation %q = %q", ClusterOrderUIDAnnotation, got)
	}
	if got := cert.GetAnnotations()[ClusterOrderNamespaceAnnotation]; got != "osac-orders" {
		t.Errorf("annotation %q = %q", ClusterOrderNamespaceAnnotation, got)
	}

	wantStrings := map[string]string{
		"spec.commonName":           "prod-east-abc12",
		"spec.secretName":           "osac-metrics-write-prod-east",
		"spec.duration":             "90d",
		"spec.renewBefore":          "30d",
		"spec.issuerRef.name":       "osac-metrics-write-ca",
		"spec.issuerRef.kind":       "Issuer",
		"spec.issuerRef.group":      "cert-manager.io",
		"spec.privateKey.algorithm": "ECDSA",
	}
	for path, want := range wantStrings {
		got, found, err := unstructured.NestedString(cert.Object, splitPath(path)...)
		if err != nil || !found {
			t.Fatalf("%s missing: %v", path, err)
		}
		if got != want {
			t.Errorf("%s = %q, want %q", path, got, want)
		}
	}
	usages, found, err := unstructured.NestedStringSlice(cert.Object, "spec", "usages")
	if err != nil || !found {
		t.Fatalf("spec.usages missing: %v", err)
	}
	if len(usages) != 1 || usages[0] != "client auth" {
		t.Errorf("spec.usages = %v, want [client auth]", usages)
	}
	size, found, err := unstructured.NestedInt64(cert.Object, "spec", "privateKey", "size")
	if err != nil || !found || size != 256 {
		t.Errorf("spec.privateKey.size = %v, want 256 (%v)", size, err)
	}
}

func TestClientCertificateOmitsEmptyLifetime(t *testing.T) {
	opts := testCertificateOptions()
	opts.Duration = ""
	opts.RenewBefore = ""
	cert, err := ClientCertificate(opts)
	if err != nil {
		t.Fatalf("build certificate: %v", err)
	}
	if _, found, _ := unstructured.NestedString(cert.Object, "spec", "duration"); found {
		t.Error("spec.duration rendered while empty")
	}
	if _, found, _ := unstructured.NestedString(cert.Object, "spec", "renewBefore"); found {
		t.Error("spec.renewBefore rendered while empty")
	}
}

func TestClientCertificateRejectsMissingInput(t *testing.T) {
	tests := map[string]func(*CertificateOptions){
		"namespace":   func(o *CertificateOptions) { o.Namespace = "" },
		"order name":  func(o *CertificateOptions) { o.OrderName = "" },
		"common name": func(o *CertificateOptions) { o.CommonName = "" },
		"issuer":      func(o *CertificateOptions) { o.Issuer = "" },
	}
	for name, mutate := range tests {
		t.Run(name, func(t *testing.T) {
			opts := testCertificateOptions()
			mutate(&opts)
			if _, err := ClientCertificate(opts); err == nil {
				t.Fatal("build succeeded, want an error")
			}
		})
	}
}

func TestCertificateName(t *testing.T) {
	if got := CertificateName("prod-east"); got != "osac-metrics-write-prod-east" {
		t.Errorf("CertificateName = %q", got)
	}
}

func TestNewClientSecrets(t *testing.T) {
	secrets := NewClientSecrets("prod-east", []byte("ca-pem"), []byte("cert-pem"), []byte("key-pem"))
	all := secrets.All()
	if len(all) != 3 {
		t.Fatalf("All() has %d secrets, want 3", len(all))
	}
	tests := []struct {
		secret *corev1.Secret
		name   string
		key    string
		value  string
	}{
		{secret: secrets.CA, name: CABundleSecretName, key: CAKey, value: "ca-pem"},
		{secret: secrets.Cert, name: ClientCertSecretName, key: CertKey, value: "cert-pem"},
		{secret: secrets.Key, name: ClientKeySecretName, key: KeyKey, value: "key-pem"},
	}
	for _, testCase := range tests {
		if testCase.secret.Name != testCase.name {
			t.Errorf("secret name = %q, want %q", testCase.secret.Name, testCase.name)
		}
		if testCase.secret.Namespace != MonitoringNamespace {
			t.Errorf("%s namespace = %q, want %q", testCase.name, testCase.secret.Namespace, MonitoringNamespace)
		}
		if got := testCase.secret.Labels[ClusterOrderLabel]; got != "prod-east" {
			t.Errorf("%s label %q = %q", testCase.name, ClusterOrderLabel, got)
		}
		if got := string(testCase.secret.Data[testCase.key]); got != testCase.value {
			t.Errorf("%s data[%q] = %q, want %q", testCase.name, testCase.key, got, testCase.value)
		}
		if len(testCase.secret.Data) != 1 {
			t.Errorf("%s carries %d keys, want exactly one", testCase.name, len(testCase.secret.Data))
		}
	}
}

func splitPath(path string) []string {
	return strings.Split(path, ".")
}

// Copyright (c) 2026 Red Hat Inc.
//
// Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except
// in compliance with the License. You may obtain a copy of the License at
//
//   http://www.apache.org/licenses/LICENSE-2.0

package admincreds

import (
	"bytes"
	"context"
	"encoding/base64"
	"testing"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/kubernetes/fake"
)

const (
	sourceNamespace = "keycloak"
	sourceSecret    = "keycloak-client-secrets"
	sourceKey       = "thanos-admin"
	destNamespace   = "osac-observability"
	clientSecret    = "thanos-admin-client-secret"
	clientSecretKey = "client-secret"
	cookieSecretKey = "cookie-secret"
	cookieOne       = "thanos-admin-oauth2-proxy"
	cookieTwo       = "thanos-admin-openshift-oauth"
)

func testConfig(random *bytes.Reader) Config {
	return Config{
		KeycloakNamespace:  sourceNamespace,
		KeycloakSecretName: sourceSecret,
		KeycloakKey:        sourceKey,
		Namespace:          destNamespace,
		ClientSecretName:   clientSecret,
		ClientSecretKey:    clientSecretKey,
		// oauth2-proxy decodes base64url from its environment variable, which
		// cannot carry binary bytes; ose-oauth-proxy decodes standard base64
		// from its --cookie-secret-file contents.
		CookieSecrets: []CookieSecret{
			{Name: cookieOne},
			{Name: cookieTwo, Base64: true},
		},
		CookieSecretKey: cookieSecretKey,
		Random:          random,
	}
}

func testConfigWithCABundle(random *bytes.Reader) Config {
	cfg := testConfig(random)
	cfg.CABundle = CABundle{
		SourceNamespace: sourceNamespace,
		SourceName:      "ca-bundle",
		SourceKey:       "bundle.pem",
		Name:            "keycloak-ca-bundle",
		Key:             "bundle.pem",
	}
	return cfg
}

func sourceCABundle(value string) *corev1.ConfigMap {
	return &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{Name: "ca-bundle", Namespace: sourceNamespace},
		Data:       map[string]string{"bundle.pem": value},
	}
}

func randomReader() *bytes.Reader {
	var data []byte
	for i := byte(1); i <= 4; i++ {
		data = append(data, bytes.Repeat([]byte{i}, 32)...)
	}
	return bytes.NewReader(data)
}

func sourceKeycloakSecret(t *testing.T, value string) *corev1.Secret {
	t.Helper()
	return &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: sourceSecret, Namespace: sourceNamespace},
		Data:       map[string][]byte{sourceKey: []byte(value)},
	}
}

func getSecret(t *testing.T, client kubernetes.Interface, namespace, name string) *corev1.Secret {
	t.Helper()
	secret, err := client.CoreV1().Secrets(namespace).Get(context.Background(), name, metav1.GetOptions{})
	if err != nil {
		t.Fatalf("get secret %s/%s: %v", namespace, name, err)
	}
	return secret
}

func TestReconcileCopiesTheClientSecret(t *testing.T) {
	client := fake.NewSimpleClientset(sourceKeycloakSecret(t, "s3cr3t"))

	if err := Reconcile(context.Background(), client, testConfig(randomReader())); err != nil {
		t.Fatalf("Reconcile: %v", err)
	}

	secret := getSecret(t, client, destNamespace, clientSecret)
	if got := string(secret.Data[clientSecretKey]); got != "s3cr3t" {
		t.Errorf("client secret = %q, want the keycloak value", got)
	}
}

func TestReconcileGeneratesACookieSecretInTheFormatEachProxyReads(t *testing.T) {
	client := fake.NewSimpleClientset(sourceKeycloakSecret(t, "s3cr3t"))

	if err := Reconcile(context.Background(), client, testConfig(randomReader())); err != nil {
		t.Fatalf("Reconcile: %v", err)
	}

	// randomReader yields four 32-byte blocks: 1s, 2s, 3s and 4s. The
	// environment Secret takes the first block as base64url, which oauth2-proxy
	// decodes; the mounted Secret takes the second as standard base64, which
	// ose-oauth-proxy decodes. Both stored values are printable text, because an
	// environment variable cannot carry binary bytes.
	plain := getSecret(t, client, destNamespace, cookieOne).Data[cookieSecretKey]
	if want := base64.RawURLEncoding.EncodeToString(bytes.Repeat([]byte{1}, 32)); string(plain) != want {
		t.Errorf("%s cookie secret = %q, want %q", cookieOne, plain, want)
	}
	encoded := getSecret(t, client, destNamespace, cookieTwo).Data[cookieSecretKey]
	decoded, err := base64.StdEncoding.DecodeString(string(encoded))
	if err != nil {
		t.Fatalf("%s cookie secret is not base64: %v", cookieTwo, err)
	}
	if want := bytes.Repeat([]byte{2}, 32); !bytes.Equal(decoded, want) {
		t.Errorf("%s cookie secret decodes to %q, want the second 32 bytes", cookieTwo, decoded)
	}
}

func TestReconcileReplacesABinaryCookieSecret(t *testing.T) {
	// Binary bytes cannot survive the environment variable path: the runtime
	// replaces invalid UTF-8, so oauth2-proxy reads a longer, different value
	// than the Secret holds. Such a value is regenerated.
	existing := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: cookieOne, Namespace: destNamespace},
		Data:       map[string][]byte{cookieSecretKey: bytes.Repeat([]byte{0xfe}, 32)},
	}
	client := fake.NewSimpleClientset(sourceKeycloakSecret(t, "s3cr3t"), existing)

	if err := Reconcile(context.Background(), client, testConfig(randomReader())); err != nil {
		t.Fatalf("Reconcile: %v", err)
	}

	want := base64.RawURLEncoding.EncodeToString(bytes.Repeat([]byte{1}, 32))
	if got := string(getSecret(t, client, destNamespace, cookieOne).Data[cookieSecretKey]); got != want {
		t.Errorf("cookie secret = %q, want a fresh printable value %q", got, want)
	}
}

func TestReconcileMigratesALegacyStandardBase64CookieSecret(t *testing.T) {
	// Earlier releases stored standard base64 in the environment Secret, which
	// oauth2-proxy does not decode (it trims the padding and decodes base64url,
	// so the value is read whole and rejected as the wrong length). The key
	// material is unchanged, so the upgrade re-encodes it instead of rotating it.
	rawBytes := bytes.Repeat([]byte{7}, 32)
	existing := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: cookieOne, Namespace: destNamespace},
		Data:       map[string][]byte{cookieSecretKey: []byte(base64.StdEncoding.EncodeToString(rawBytes))},
	}
	client := fake.NewSimpleClientset(sourceKeycloakSecret(t, "s3cr3t"), existing)

	if err := Reconcile(context.Background(), client, testConfig(randomReader())); err != nil {
		t.Fatalf("Reconcile: %v", err)
	}

	want := base64.RawURLEncoding.EncodeToString(rawBytes)
	if got := string(getSecret(t, client, destNamespace, cookieOne).Data[cookieSecretKey]); got != want {
		t.Errorf("cookie secret = %q, want the same bytes encoded as %q", got, want)
	}
}

func TestReconcileKeepsAnUnusableCookieSecretValue(t *testing.T) {
	// A value that is neither a usable raw secret nor base64 of one is
	// someone's deliberate pin; rewriting it would be a rotation.
	existing := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: cookieOne, Namespace: destNamespace},
		Data:       map[string][]byte{cookieSecretKey: []byte("short")},
	}
	client := fake.NewSimpleClientset(sourceKeycloakSecret(t, "s3cr3t"), existing)

	if err := Reconcile(context.Background(), client, testConfig(randomReader())); err != nil {
		t.Fatalf("Reconcile: %v", err)
	}

	if got := string(getSecret(t, client, destNamespace, cookieOne).Data[cookieSecretKey]); got != "short" {
		t.Errorf("cookie secret = %q, want it preserved", got)
	}
}

func TestReconcileCopiesTheCABundle(t *testing.T) {
	// The proxies verify the Keycloak Route's certificate against the cluster
	// bundle, which lives in the Keycloak namespace; a volume cannot cross
	// namespaces, so the job copies it beside them.
	const bundle = "-----BEGIN CERTIFICATE-----\nMIIB\n-----END CERTIFICATE-----\n"
	client := fake.NewSimpleClientset(
		sourceKeycloakSecret(t, "s3cr3t"), sourceCABundle(bundle))

	if err := Reconcile(context.Background(), client, testConfigWithCABundle(randomReader())); err != nil {
		t.Fatalf("Reconcile: %v", err)
	}

	copied, err := client.CoreV1().ConfigMaps(destNamespace).Get(context.Background(), "keycloak-ca-bundle", metav1.GetOptions{})
	if err != nil {
		t.Fatalf("get the copied CA bundle: %v", err)
	}
	if got := copied.Data["bundle.pem"]; got != bundle {
		t.Errorf("CA bundle = %q, want the source contents", got)
	}
}

func TestReconcileUpdatesAChangedCABundle(t *testing.T) {
	stale := &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{Name: "keycloak-ca-bundle", Namespace: destNamespace},
		Data:       map[string]string{"bundle.pem": "old"},
	}
	client := fake.NewSimpleClientset(
		sourceKeycloakSecret(t, "s3cr3t"), sourceCABundle("new"), stale)

	if err := Reconcile(context.Background(), client, testConfigWithCABundle(randomReader())); err != nil {
		t.Fatalf("Reconcile: %v", err)
	}

	copied, err := client.CoreV1().ConfigMaps(destNamespace).Get(context.Background(), "keycloak-ca-bundle", metav1.GetOptions{})
	if err != nil {
		t.Fatalf("get the copied CA bundle: %v", err)
	}
	if got := copied.Data["bundle.pem"]; got != "new" {
		t.Errorf("CA bundle = %q, want the rotated source contents", got)
	}
}

func TestReconcileFailsWithoutTheSourceCABundle(t *testing.T) {
	client := fake.NewSimpleClientset(sourceKeycloakSecret(t, "s3cr3t"))

	if err := Reconcile(context.Background(), client, testConfigWithCABundle(randomReader())); err == nil {
		t.Fatal("Reconcile accepted a missing CA bundle source")
	}
}

func TestReconcileRejectsAPartialCABundleConfig(t *testing.T) {
	cfg := testConfigWithCABundle(randomReader())
	cfg.CABundle.SourceKey = ""

	if err := Reconcile(context.Background(), fake.NewSimpleClientset(sourceKeycloakSecret(t, "s3cr3t")), cfg); err == nil {
		t.Fatal("Reconcile accepted a partially configured CA bundle")
	}
}

func TestReconcileIsIdempotent(t *testing.T) {
	client := fake.NewSimpleClientset(sourceKeycloakSecret(t, "s3cr3t"))
	cfg := testConfig(randomReader())

	if err := Reconcile(context.Background(), client, cfg); err != nil {
		t.Fatalf("first Reconcile: %v", err)
	}
	first := getSecret(t, client, destNamespace, clientSecret)
	cookie := getSecret(t, client, destNamespace, cookieOne)

	// A second run must read only: no create, update or patch.
	client.ClearActions()
	if err := Reconcile(context.Background(), client, cfg); err != nil {
		t.Fatalf("second Reconcile: %v", err)
	}
	for _, action := range client.Actions() {
		if action.GetVerb() != "get" {
			t.Errorf("second run performed a %s on %s", action.GetVerb(), action.GetResource().Resource)
		}
	}
	if got := string(getSecret(t, client, destNamespace, clientSecret).Data[clientSecretKey]); got != "s3cr3t" {
		t.Errorf("client secret changed on the second run: %q", got)
	}
	if got := getSecret(t, client, destNamespace, cookieOne).Data[cookieSecretKey]; !bytes.Equal(got, cookie.Data[cookieSecretKey]) {
		t.Errorf("cookie secret was rotated on the second run")
	}
	if first.Data[clientSecretKey] == nil {
		t.Error("first run did not write the client secret")
	}
}

func TestReconcileKeepsAnExistingCookieSecret(t *testing.T) {
	// An operator may have pinned the cookie secret; upgrades must not log
	// every admin out by replacing it.
	existing := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: cookieOne, Namespace: destNamespace},
		Data:       map[string][]byte{cookieSecretKey: []byte("pinned-cookie-value")},
	}
	client := fake.NewSimpleClientset(sourceKeycloakSecret(t, "s3cr3t"), existing)

	if err := Reconcile(context.Background(), client, testConfig(randomReader())); err != nil {
		t.Fatalf("Reconcile: %v", err)
	}

	if got := string(getSecret(t, client, destNamespace, cookieOne).Data[cookieSecretKey]); got != "pinned-cookie-value" {
		t.Errorf("cookie secret = %q, want it preserved", got)
	}
	if _, err := client.CoreV1().Secrets(destNamespace).Get(context.Background(), cookieTwo, metav1.GetOptions{}); err != nil {
		t.Errorf("the missing cookie secret was not created: %v", err)
	}
}

func TestReconcileFillsAnEmptyCookieSecret(t *testing.T) {
	empty := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: cookieOne, Namespace: destNamespace},
		Data:       map[string][]byte{cookieSecretKey: {}},
	}
	client := fake.NewSimpleClientset(sourceKeycloakSecret(t, "s3cr3t"), empty)

	if err := Reconcile(context.Background(), client, testConfig(randomReader())); err != nil {
		t.Fatalf("Reconcile: %v", err)
	}

	if got := getSecret(t, client, destNamespace, cookieOne).Data[cookieSecretKey]; len(got) == 0 {
		t.Error("the empty cookie secret was left empty")
	}
}

func TestReconcileUpdatesAChangedClientSecret(t *testing.T) {
	old := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: clientSecret, Namespace: destNamespace},
		Data:       map[string][]byte{clientSecretKey: []byte("old-value")},
	}
	client := fake.NewSimpleClientset(sourceKeycloakSecret(t, "new-value"), old)

	if err := Reconcile(context.Background(), client, testConfig(randomReader())); err != nil {
		t.Fatalf("Reconcile: %v", err)
	}

	if got := string(getSecret(t, client, destNamespace, clientSecret).Data[clientSecretKey]); got != "new-value" {
		t.Errorf("client secret = %q, want the rotated keycloak value", got)
	}
}

func TestReconcileFailsWithoutTheSourceSecret(t *testing.T) {
	client := fake.NewSimpleClientset()

	err := Reconcile(context.Background(), client, testConfig(randomReader()))
	if err == nil {
		t.Fatal("Reconcile should fail when the keycloak secret is absent")
	}
	_, getErr := client.CoreV1().Secrets(destNamespace).Get(context.Background(), clientSecret, metav1.GetOptions{})
	if !apierrors.IsNotFound(getErr) {
		t.Error("a destination secret was written without a source secret")
	}
}

func TestReconcileFailsOnAMissingOrEmptySourceKey(t *testing.T) {
	for _, value := range []string{"", "   "} {
		secret := &corev1.Secret{
			ObjectMeta: metav1.ObjectMeta{Name: sourceSecret, Namespace: sourceNamespace},
			Data:       map[string][]byte{sourceKey: []byte(value)},
		}
		client := fake.NewSimpleClientset(secret)
		if err := Reconcile(context.Background(), client, testConfig(randomReader())); err == nil {
			t.Errorf("Reconcile accepted the keycloak key value %q", value)
		}
	}

	missing := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: sourceSecret, Namespace: sourceNamespace},
		Data:       map[string][]byte{"other": []byte("value")},
	}
	if err := Reconcile(context.Background(), fake.NewSimpleClientset(missing), testConfig(randomReader())); err == nil {
		t.Error("Reconcile accepted a keycloak secret without the thanos-admin key")
	}
}

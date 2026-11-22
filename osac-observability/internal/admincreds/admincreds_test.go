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
		CookieSecretNames:  []string{cookieOne, cookieTwo},
		CookieSecretKey:    cookieSecretKey,
		Random:             random,
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

func TestReconcileGeneratesDistinctCookieSecrets(t *testing.T) {
	client := fake.NewSimpleClientset(sourceKeycloakSecret(t, "s3cr3t"))

	if err := Reconcile(context.Background(), client, testConfig(randomReader())); err != nil {
		t.Fatalf("Reconcile: %v", err)
	}

	seen := map[string]bool{}
	for _, name := range []string{cookieOne, cookieTwo} {
		value := string(getSecret(t, client, destNamespace, name).Data[cookieSecretKey])
		decoded, err := base64.StdEncoding.DecodeString(value)
		if err != nil {
			t.Fatalf("%s cookie secret is not base64: %v", name, err)
		}
		if len(decoded) != 32 {
			t.Errorf("%s cookie secret decodes to %d bytes, want 32", name, len(decoded))
		}
		if seen[value] {
			t.Errorf("the two cookie secrets are identical")
		}
		seen[value] = true
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

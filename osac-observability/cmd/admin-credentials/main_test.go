// Copyright (c) 2026 Red Hat Inc.
//
// Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except
// in compliance with the License. You may obtain a copy of the License at
//
//   http://www.apache.org/licenses/LICENSE-2.0

package main

import (
	"reflect"
	"testing"

	"github.com/osac-project/osac/osac-observability/internal/admincreds"
)

func env(values map[string]string) func(string) string {
	return func(name string) string { return values[name] }
}

func TestLoadConfigDefaultsToTheDeployedObjectNames(t *testing.T) {
	cfg, err := loadConfig(env(nil))
	if err != nil {
		t.Fatalf("loadConfig: %v", err)
	}

	want := admincreds.Config{
		KeycloakNamespace:  "keycloak",
		KeycloakSecretName: "keycloak-client-secrets",
		KeycloakKey:        "thanos-admin",
		Namespace:          "osac-observability",
		ClientSecretName:   "thanos-admin-client-secret",
		ClientSecretKey:    "client-secret",
		CookieSecretNames:  []string{"thanos-admin-oauth2-proxy", "thanos-admin-openshift-oauth"},
		CookieSecretKey:    "cookie-secret",
	}
	if !reflect.DeepEqual(cfg, want) {
		t.Errorf("config = %+v, want %+v", cfg, want)
	}
}

func TestLoadConfigHonorsOverrides(t *testing.T) {
	cfg, err := loadConfig(env(map[string]string{
		"KEYCLOAK_NAMESPACE":        "idp",
		"KEYCLOAK_SECRET_NAME":      "clients",
		"KEYCLOAK_SECRET_KEY":       "admin",
		"OBS_NAMESPACE":             "obs",
		"ADMIN_CLIENT_SECRET_NAME":  "admin-secret",
		"ADMIN_CLIENT_SECRET_KEY":   "value",
		"ADMIN_COOKIE_SECRET_KEY":   "cookie",
		"ADMIN_COOKIE_SECRET_NAMES": `["cookie-a"]`,
	}))
	if err != nil {
		t.Fatalf("loadConfig: %v", err)
	}

	if cfg.KeycloakNamespace != "idp" || cfg.KeycloakSecretName != "clients" ||
		cfg.KeycloakKey != "admin" || cfg.Namespace != "obs" ||
		cfg.ClientSecretName != "admin-secret" || cfg.ClientSecretKey != "value" ||
		cfg.CookieSecretKey != "cookie" {
		t.Errorf("overrides were not applied: %+v", cfg)
	}
	if want := []string{"cookie-a"}; !reflect.DeepEqual(cfg.CookieSecretNames, want) {
		t.Errorf("cookie secret names = %v, want %v", cfg.CookieSecretNames, want)
	}
}

func TestLoadConfigRejectsMalformedCookieSecretNames(t *testing.T) {
	_, err := loadConfig(env(map[string]string{"ADMIN_COOKIE_SECRET_NAMES": "not-json"}))
	if err == nil {
		t.Fatal("loadConfig accepted malformed ADMIN_COOKIE_SECRET_NAMES")
	}
}

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
		Namespace: "osac-observability",
		// The gate verifies the Keycloak Route through a copy of the cluster
		// trust bundle; the chart mounts it at a fixed path, so the names are
		// fixed too.
		CABundle: admincreds.CABundle{
			SourceNamespace: "keycloak",
			SourceName:      "ca-bundle",
			SourceKey:       "bundle.pem",
			Name:            "keycloak-ca-bundle",
			Key:             "bundle.pem",
		},
	}
	if !reflect.DeepEqual(cfg, want) {
		t.Errorf("config = %+v, want %+v", cfg, want)
	}
}

func TestLoadConfigHonorsTheNamespaceOverride(t *testing.T) {
	cfg, err := loadConfig(env(map[string]string{"OBS_NAMESPACE": "obs"}))
	if err != nil {
		t.Fatalf("loadConfig: %v", err)
	}
	if cfg.Namespace != "obs" {
		t.Errorf("Namespace = %q, want obs", cfg.Namespace)
	}
}

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
)

func noEnv(string) string { return "" }

func TestLoadWriteProxyConfigDefaults(t *testing.T) {
	cfg, err := loadWriteProxyConfig(nil, noEnv)
	if err != nil {
		t.Fatalf("loadWriteProxyConfig: %v", err)
	}
	if cfg.Listen != ":8443" {
		t.Errorf("listen = %q", cfg.Listen)
	}
	if cfg.ProbeListen != ":8080" {
		t.Errorf("probe listen = %q", cfg.ProbeListen)
	}
	if cfg.Upstream.String() != defaultUpstream {
		t.Errorf("upstream = %q", cfg.Upstream)
	}
	if cfg.RevocationName != "osac-metrics-write-revocation" {
		t.Errorf("revocation name = %q", cfg.RevocationName)
	}
	if cfg.Rule.KeepKubePrefix != "kube_node_" {
		t.Errorf("keep prefix = %q", cfg.Rule.KeepKubePrefix)
	}
	want := []string{"container_", "claim", "mount", "kserve_", "vllm_", "dcgm_"}
	if !reflect.DeepEqual(cfg.Rule.DropPrefixes, want) {
		t.Errorf("drop prefixes = %v, want %v", cfg.Rule.DropPrefixes, want)
	}
}

func TestLoadWriteProxyConfigReadsEnvironment(t *testing.T) {
	env := map[string]string{
		"OSAC_WRITE_PROXY_LISTEN":               ":9443",
		"OSAC_WRITE_PROXY_UPSTREAM":             "https://receive.example:19291/api/v1/receive",
		"OSAC_WRITE_PROXY_REVOCATION_NAMESPACE": "custom-ns",
		"OSAC_WRITE_PROXY_DROP_PREFIXES":        "foo_, bar_",
	}
	cfg, err := loadWriteProxyConfig(nil, func(name string) string { return env[name] })
	if err != nil {
		t.Fatalf("loadWriteProxyConfig: %v", err)
	}
	if cfg.Listen != ":9443" {
		t.Errorf("listen = %q", cfg.Listen)
	}
	if cfg.Upstream.String() != "https://receive.example:19291/api/v1/receive" {
		t.Errorf("upstream = %q", cfg.Upstream)
	}
	if cfg.RevocationNamespace != "custom-ns" {
		t.Errorf("namespace = %q", cfg.RevocationNamespace)
	}
	if !reflect.DeepEqual(cfg.Rule.DropPrefixes, []string{"foo_", "bar_"}) {
		t.Errorf("drop prefixes = %v", cfg.Rule.DropPrefixes)
	}
}

func TestLoadWriteProxyConfigFlagsOverrideEnvironment(t *testing.T) {
	cfg, err := loadWriteProxyConfig([]string{"--listen", ":10443"}, func(name string) string {
		if name == "OSAC_WRITE_PROXY_LISTEN" {
			return ":9443"
		}
		return ""
	})
	if err != nil {
		t.Fatalf("loadWriteProxyConfig: %v", err)
	}
	if cfg.Listen != ":10443" {
		t.Errorf("listen = %q", cfg.Listen)
	}
}

func TestLoadWriteProxyConfigRejectsInvalidUpstream(t *testing.T) {
	if _, err := loadWriteProxyConfig([]string{"--upstream", "ftp://example"}, noEnv); err == nil {
		t.Fatal("expected an error for an unsupported scheme")
	}
}

func TestRunRejectsUnknownSubcommand(t *testing.T) {
	if err := run([]string{"authorize"}, noEnv); err == nil {
		t.Fatal("expected an error")
	}
}

func TestRunRequiresASubcommand(t *testing.T) {
	if err := run(nil, noEnv); err == nil {
		t.Fatal("expected an error")
	}
}

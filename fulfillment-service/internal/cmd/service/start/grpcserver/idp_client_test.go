/*
Copyright (c) 2026 Red Hat Inc.

Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except in compliance with the
License. You may obtain a copy of the License at

	http://www.apache.org/licenses/LICENSE-2.0
*/
package grpcserver

import (
	"context"
	"log/slog"
	"os"
	"path/filepath"
	"testing"
)

func TestIDPConfiguration(t *testing.T) {
	valid := func() *runnerContext {
		r := &runnerContext{logger: slog.Default()}
		r.args.idpURL = "https://keycloak.example.test"
		r.args.authIssuerURL = "https://keycloak.example.test/realms/osac"
		r.args.idpClientID = "osac-controller"
		r.args.idpClientSecret = "test-only"
		return r
	}
	for _, tc := range []struct {
		name   string
		change func(*runnerContext)
	}{
		{"missing URL", func(r *runnerContext) { r.args.idpURL = "" }},
		{"missing issuer", func(r *runnerContext) { r.args.authIssuerURL = "" }},
		{"missing ID", func(r *runnerContext) { r.args.idpClientID = "" }},
		{"missing secret", func(r *runnerContext) { r.args.idpClientSecret = "" }},
		{"ambiguous ID", func(r *runnerContext) { r.args.idpClientIDFile = "irrelevant" }},
		{"ambiguous secret", func(r *runnerContext) { r.args.idpClientSecretFile = "irrelevant" }},
		{"missing file", func(r *runnerContext) { r.args.idpClientSecret = ""; r.args.idpClientSecretFile = "nonexistent" }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := valid()
			tc.change(r)
			if _, err := r.createIDPClient(context.Background(), nil); err == nil {
				t.Fatal("incomplete or ambiguous IDP configuration accepted")
			}
		})
	}
	t.Run("inline credentials", func(t *testing.T) {
		if _, err := valid().createIDPClient(context.Background(), nil); err != nil {
			t.Fatal(err)
		}
	})
	t.Run("projected credentials", func(t *testing.T) {
		r := valid()
		dir := t.TempDir()
		r.args.idpClientID = ""
		r.args.idpClientSecret = ""
		r.args.idpClientIDFile = filepath.Join(dir, "client-id")
		r.args.idpClientSecretFile = filepath.Join(dir, "client-secret")
		for path, value := range map[string]string{r.args.idpClientIDFile: "osac-controller\n", r.args.idpClientSecretFile: "test-only\n"} {
			if err := os.WriteFile(path, []byte(value), 0600); err != nil {
				t.Fatal(err)
			}
		}
		if _, err := r.createIDPClient(context.Background(), nil); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(r.args.idpClientSecretFile, []byte(" \n"), 0600); err != nil {
			t.Fatal(err)
		}
		if _, err := r.createIDPClient(context.Background(), nil); err == nil {
			t.Fatal("empty projected secret accepted")
		}
	})
}
func TestIDPCredentialFlags(t *testing.T) {
	for _, name := range []string{"idp-url", "auth-issuer-url", "idp-client-id", "idp-client-id-file", "idp-client-secret", "idp-client-secret-file"} {
		if Cmd().Flags().Lookup(name) == nil {
			t.Errorf("missing flag %s", name)
		}
	}
}

/*
Copyright (c) 2026 Red Hat, Inc.

Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except
in compliance with the License. You may obtain a copy of the License at

  http://www.apache.org/licenses/LICENSE-2.0
*/

package contract

import (
	"strings"
	"testing"
)

func TestCompareFilesAcceptsIdenticalFiles(t *testing.T) {
	files := map[string][]byte{"a.md": []byte("same\n")}
	if err := compareFiles(files, files); err != nil {
		t.Fatalf("identical files must pass, got %v", err)
	}
}

func TestCompareFilesReportsAStaleFile(t *testing.T) {
	committed := map[string][]byte{"a.md": []byte("old\n")}
	generated := map[string][]byte{"a.md": []byte("new\n")}
	err := compareFiles(committed, generated)
	if err == nil || !strings.Contains(err.Error(), "a.md is stale") {
		t.Fatalf("want a stale-file error, got %v", err)
	}
	if !strings.Contains(err.Error(), "make -C osac-metering contract") {
		t.Fatalf("the error must say how to regenerate, got %v", err)
	}
}

func TestCompareFilesReportsAMissingFile(t *testing.T) {
	generated := map[string][]byte{"a.md": []byte("new\n")}
	err := compareFiles(map[string][]byte{}, generated)
	if err == nil || !strings.Contains(err.Error(), "a.md is missing") {
		t.Fatalf("want a missing-file error, got %v", err)
	}
}

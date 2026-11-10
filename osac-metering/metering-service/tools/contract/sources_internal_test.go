/*
Copyright (c) 2026 Red Hat, Inc.

Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except
in compliance with the License. You may obtain a copy of the License at

  http://www.apache.org/licenses/LICENSE-2.0
*/

package contract

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// repositoryRoot walks up from the package directory to the checkout root.
func repositoryRoot(t *testing.T) string {
	t.Helper()
	directory, err := os.Getwd()
	if err != nil {
		t.Fatalf("reading the working directory: %v", err)
	}
	for {
		if _, err := os.Stat(filepath.Join(directory, "go.work")); err == nil {
			return directory
		}
		parent := filepath.Dir(directory)
		if parent == directory {
			t.Fatal("no checkout root above the test directory")
		}
		directory = parent
	}
}

func writeRevision(t *testing.T, directory, name string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(directory, name), []byte("- resource: Cluster\n"), 0o644); err != nil {
		t.Fatalf("writing %s: %v", name, err)
	}
}

func TestLatestRevisionPicksTheHighestShippedNumber(t *testing.T) {
	directory := t.TempDir()
	writeRevision(t, directory, "0001-meters.yaml")
	writeRevision(t, directory, "0010-meters.yaml")
	writeRevision(t, directory, "0002-meters.yaml")
	writeRevision(t, directory, "README.md")

	number, metersPath, err := latestRevision(directory)
	if err != nil {
		t.Fatalf("finding the newest revision: %v", err)
	}
	if number != "0010" {
		t.Fatalf("newest revision is %s, want 0010", number)
	}
	if filepath.Base(metersPath) != "0010-meters.yaml" {
		t.Fatalf("meters path is %s, want 0010-meters.yaml", metersPath)
	}
}

func TestLatestRevisionRefusesADirectoryWithoutRevisions(t *testing.T) {
	_, _, err := latestRevision(t.TempDir())
	if err == nil || !strings.Contains(err.Error(), "no <NNNN>-meters.yaml") {
		t.Fatalf("want a missing-revision error, got %v", err)
	}
}

func TestStrayFilesListsWhatTheGeneratorDoesNotWrite(t *testing.T) {
	generated := map[string][]byte{
		DocsDirectory + "/" + ReferenceName: nil,
		DocsDirectory + "/" + SchemaName:    nil,
	}
	names := []string{ReferenceName, SchemaName, "notes.md"}
	stray := strayFiles(generated, names)
	if len(stray) != 1 || stray[0] != "notes.md" {
		t.Fatalf("stray files are %v, want [notes.md]", stray)
	}
}

func TestReferenceDocumentsTheNewestShippedRevision(t *testing.T) {
	root := repositoryRoot(t)
	sources, err := LoadSources(root)
	if err != nil {
		t.Fatalf("loading the contract sources: %v", err)
	}
	number, _, err := latestRevision(filepath.Join(root, rulesDirectory))
	if err != nil {
		t.Fatalf("finding the newest revision: %v", err)
	}
	if sources.Revision.Number != number {
		t.Fatalf(
			"the contract uses revision %s, but the newest shipped file is %s",
			sources.Revision.Number, number,
		)
	}
	files, err := Generate(sources)
	if err != nil {
		t.Fatalf("generating the contract: %v", err)
	}
	reference := string(files[DocsDirectory+"/"+ReferenceName])
	if !strings.Contains(reference, "revision `"+number+"`") {
		t.Fatalf("the reference does not name revision %s", number)
	}
	if !strings.Contains(reference, "`"+sources.HeartbeatInterval+"`") {
		t.Fatalf("the reference does not quote the chart interval %s", sources.HeartbeatInterval)
	}
	if !strings.Contains(reference, "`"+sources.TopicRetention+"`") {
		t.Fatalf("the reference does not quote the chart retention %s", sources.TopicRetention)
	}
}

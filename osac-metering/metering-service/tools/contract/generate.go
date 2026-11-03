/*
Copyright (c) 2026 Red Hat, Inc.

Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except
in compliance with the License. You may obtain a copy of the License at

  http://www.apache.org/licenses/LICENSE-2.0
*/

package contract

import (
	"bytes"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// Generate renders both generated files. The returned map is keyed by the repository path of
// each file.
func Generate(sources Sources) (map[string][]byte, error) {
	reference, err := renderReference(sources)
	if err != nil {
		return nil, err
	}
	generatedSchema, err := renderSchema(sources)
	if err != nil {
		return nil, err
	}
	return map[string][]byte{
		DocsDirectory + "/" + ReferenceName: reference,
		DocsDirectory + "/" + SchemaName:    generatedSchema,
	}, nil
}

// Write regenerates the committed files in a repository checkout.
func Write(repoRoot string) error {
	sources, err := LoadSources(repoRoot)
	if err != nil {
		return err
	}
	files, err := Generate(sources)
	if err != nil {
		return err
	}
	for _, path := range sortedPaths(files) {
		full := filepath.Join(repoRoot, path)
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			return fmt.Errorf("creating %s: %w", filepath.Dir(path), err)
		}
		if err := os.WriteFile(full, files[path], 0o644); err != nil {
			return fmt.Errorf("writing %s: %w", path, err)
		}
	}
	return nil
}

// Check fails when a published schema file differs from its SHA-256 manifest, or when a
// committed contract file differs from the regenerated one, so a change to a resource, a
// meter, or a schema file cannot merge without its regenerated reference.
func Check(repoRoot string) error {
	if err := checkPublishedManifest(repoRoot); err != nil {
		return err
	}
	sources, err := LoadSources(repoRoot)
	if err != nil {
		return err
	}
	files, err := Generate(sources)
	if err != nil {
		return err
	}
	committed := make(map[string][]byte, len(files))
	for path := range files {
		content, err := os.ReadFile(filepath.Join(repoRoot, path))
		if errors.Is(err, fs.ErrNotExist) {
			continue
		}
		if err != nil {
			return fmt.Errorf("reading %s: %w", path, err)
		}
		committed[path] = content
	}
	if err := compareFiles(committed, files); err != nil {
		return err
	}
	entries, err := os.ReadDir(filepath.Join(repoRoot, DocsDirectory))
	if err != nil {
		return fmt.Errorf("reading %s: %w", DocsDirectory, err)
	}
	names := make([]string, 0, len(entries))
	for _, entry := range entries {
		names = append(names, entry.Name())
	}
	if stray := strayFiles(files, names); len(stray) > 0 {
		return fmt.Errorf(
			"%s holds files the generator does not write: %s; remove them or generate "+
				"them from a source input",
			DocsDirectory, strings.Join(stray, ", "),
		)
	}
	return nil
}

// strayFiles returns the entries in the generated directory that the generator does not
// write, so a hand-added file under docs/contract cannot sit outside the diff.
func strayFiles(generated map[string][]byte, names []string) []string {
	stray := make([]string, 0, len(names))
	for _, name := range names {
		if _, written := generated[DocsDirectory+"/"+name]; !written {
			stray = append(stray, name)
		}
	}
	sort.Strings(stray)
	return stray
}

// compareFiles reports every generated file that is missing or differs from the committed
// one.
func compareFiles(committed, generated map[string][]byte) error {
	stale := make([]string, 0, len(generated))
	for _, path := range sortedPaths(generated) {
		content, found := committed[path]
		switch {
		case !found:
			stale = append(stale, path+" is missing")
		case !bytes.Equal(content, generated[path]):
			stale = append(stale, path+" is stale")
		}
	}
	if len(stale) > 0 {
		return fmt.Errorf(
			"the generated metering contract is out of date: %s; run "+
				"`make -C osac-metering contract` and commit the result",
			strings.Join(stale, ", "),
		)
	}
	return nil
}

// sortedPaths returns the file paths in a stable order, so an error names the same file first
// every run.
func sortedPaths(files map[string][]byte) []string {
	paths := make([]string, 0, len(files))
	for path := range files {
		paths = append(paths, path)
	}
	sort.Strings(paths)
	return paths
}

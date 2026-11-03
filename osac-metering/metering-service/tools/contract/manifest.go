/*
Copyright (c) 2026 Red Hat, Inc.

Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except
in compliance with the License. You may obtain a copy of the License at

  http://www.apache.org/licenses/LICENSE-2.0
*/

package contract

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

const (
	// manifestName is the committed SHA-256 manifest of the published version files. It sits
	// beside the versions directory, not inside it: the release-tag gate rejects every change
	// under the tagged directory, so a manifest there would block every new version.
	manifestName = "published.sha256"

	// versionsDirectory holds the published schema files, one JSON file per version.
	versionsDirectory = schemaDirectory + "/versions"
)

var (
	sha256Pattern          = regexp.MustCompile(`^[0-9a-f]{64}$`)
	versionFileNamePattern = regexp.MustCompile(`^v[1-9][0-9]*\.json$`)
)

// checkPublishedManifest verifies the committed SHA-256 manifest against the published schema
// files on disk. It fails when a file's bytes differ from its entry, when a file has no entry,
// when an entry names no file, when an entry repeats, or when a line is malformed. The
// manifest is committed by hand: a new version needs an explicit entry.
func checkPublishedManifest(repoRoot string) error {
	manifestPath := filepath.Join(repoRoot, schemaDirectory, manifestName)
	content, err := os.ReadFile(manifestPath)
	if err != nil {
		return fmt.Errorf("reading %s: %w", manifestPath, err)
	}
	entries := map[string]string{}
	for index, line := range strings.Split(string(content), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) != 2 {
			return fmt.Errorf(
				"%s line %d is not '<sha256>  <version>.json'", manifestName, index+1,
			)
		}
		digest := fields[0]
		name := strings.TrimPrefix(fields[1], "*")
		if !sha256Pattern.MatchString(digest) {
			return fmt.Errorf("%s line %d has an invalid SHA-256 %q", manifestName, index+1, digest)
		}
		if !versionFileNamePattern.MatchString(name) {
			return fmt.Errorf("%s line %d names %q, want v<number>.json", manifestName, index+1, name)
		}
		if _, repeated := entries[name]; repeated {
			return fmt.Errorf("%s lists %s more than once", manifestName, name)
		}
		entries[name] = digest
	}

	paths, err := filepath.Glob(filepath.Join(repoRoot, versionsDirectory, "*.json"))
	if err != nil {
		return fmt.Errorf("listing %s: %w", versionsDirectory, err)
	}
	seen := map[string]bool{}
	for _, path := range paths {
		name := filepath.Base(path)
		want, found := entries[name]
		if !found {
			return fmt.Errorf(
				"published schema %s has no entry in %s; add its SHA-256 explicitly",
				name, manifestName,
			)
		}
		fileContent, err := os.ReadFile(path)
		if err != nil {
			return fmt.Errorf("reading %s: %w", path, err)
		}
		digest := sha256.Sum256(fileContent)
		actual := hex.EncodeToString(digest[:])
		if actual != want {
			return fmt.Errorf(
				"published schema %s changed: %s records %s, the file is %s; a published "+
					"version is immutable, so ship the next version with its own entry",
				name, manifestName, want, actual,
			)
		}
		seen[name] = true
	}

	extra := make([]string, 0, len(entries))
	for name := range entries {
		if !seen[name] {
			extra = append(extra, name)
		}
	}
	sort.Strings(extra)
	if len(extra) > 0 {
		return fmt.Errorf(
			"%s lists %s, which no published schema file provides", manifestName,
			strings.Join(extra, ", "),
		)
	}
	return nil
}

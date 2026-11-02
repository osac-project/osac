/*
Copyright (c) 2026 Red Hat, Inc.

Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except
in compliance with the License. You may obtain a copy of the License at

  http://www.apache.org/licenses/LICENSE-2.0
*/

// Package contract generates the published metering contract: a reference a person reads and
// a JSON Schema a tool checks. Both come from three inputs that already exist as data: the
// metering field registry, the default rule revision the chart ships, and the published schema
// files. Nothing is hand-written, so the reference cannot drift from the code.
package contract

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/osac-project/osac/proto/metering"

	"github.com/osac-project/osac-metering/internal/rules"
	"github.com/osac-project/osac-metering/schema"
)

const (
	// DocsDirectory is where the generated files are committed, relative to the repository
	// root.
	DocsDirectory = "osac-metering/docs/contract"

	// ReferenceName is the readable reference a person reads.
	ReferenceName = "reference.md"

	// SchemaName is the JSON Schema a tool checks an event against.
	SchemaName = "schema.json"

	rulesDirectory  = "osac-metering/charts/osac-metering/rules"
	rulesTemplate   = "osac-metering/charts/osac-metering/templates/rules.yaml"
	schemaDirectory = "osac-metering/schema"
)

// rulesFilePattern matches a shipped rule revision file, for example 0002-meters.yaml.
var rulesFilePattern = regexp.MustCompile(`^([0-9]{4})-meters\.yaml$`)

// Sources are the three inputs the contract is generated from.
type Sources struct {
	// Resources is the field registry: every billable resource and the fields metering
	// tracks on it.
	Resources []metering.Resource

	// Revision is the default rule revision: every meter, its unit, billable states, and
	// dimensions.
	Revision rules.Revision

	// Versions are the published schema files, sorted by version.
	Versions []schema.Version
}

// LoadSources reads the three inputs from a repository checkout.
func LoadSources(repoRoot string) (Sources, error) {
	resources, err := metering.Resources()
	if err != nil {
		return Sources{}, fmt.Errorf("reading the metering field registry: %w", err)
	}

	number, metersPath, err := latestRevision(filepath.Join(repoRoot, rulesDirectory))
	if err != nil {
		return Sources{}, err
	}
	metersYAML, err := os.ReadFile(metersPath)
	if err != nil {
		return Sources{}, fmt.Errorf("reading the default rule revision: %w", err)
	}
	effectiveAt, err := revisionEffectiveAt(repoRoot, number)
	if err != nil {
		return Sources{}, err
	}
	revision, err := rules.Parse(rules.NamePrefix+number, effectiveAt, string(metersYAML))
	if err != nil {
		return Sources{}, fmt.Errorf("reading the default rule revision: %w", err)
	}

	versions, err := schema.PublishedVersions()
	if err != nil {
		return Sources{}, fmt.Errorf("reading the published schema files: %w", err)
	}
	return Sources{Resources: resources, Revision: revision, Versions: versions}, nil
}

// latestRevision returns the highest-numbered rule revision the chart ships and the path of
// its meters file. A change ships as the next revision file, so the newest number is the
// default the reference documents.
func latestRevision(directory string) (number, metersPath string, err error) {
	entries, err := os.ReadDir(directory)
	if err != nil {
		return "", "", fmt.Errorf("reading the shipped rule revisions in %s: %w", rulesDirectory, err)
	}
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		match := rulesFilePattern.FindStringSubmatch(entry.Name())
		if match == nil {
			continue
		}
		// The names are four-digit zero-padded, so string order is numeric order.
		if match[1] > number {
			number = match[1]
		}
	}
	if number == "" {
		return "", "", fmt.Errorf("no <NNNN>-meters.yaml rule revision in %s", rulesDirectory)
	}
	return number, filepath.Join(directory, number+"-meters.yaml"), nil
}

// revisionEffectiveAt reads a rule revision's effective time from the chart template that
// ships it. The meters document holds the meters alone; the time lives with the ConfigMap.
func revisionEffectiveAt(repoRoot, number string) (string, error) {
	path := filepath.Join(repoRoot, rulesTemplate)
	content, err := os.ReadFile(path)
	if err != nil {
		return "", fmt.Errorf("reading %s: %w", rulesTemplate, err)
	}
	name := "name: " + rules.NamePrefix + number
	inRevision := false
	for _, line := range strings.Split(string(content), "\n") {
		trimmed := strings.TrimSpace(line)
		switch {
		case trimmed == "---":
			inRevision = false
		case strings.HasPrefix(trimmed, "name: "+rules.NamePrefix):
			inRevision = trimmed == name
		case inRevision && strings.HasPrefix(trimmed, "effective_at:"):
			value := strings.TrimSpace(strings.TrimPrefix(trimmed, "effective_at:"))
			value = strings.Trim(value, `"'`)
			if value == "" {
				return "", fmt.Errorf("%s: %s has an empty effective_at", rulesTemplate, name)
			}
			return value, nil
		}
	}
	return "", fmt.Errorf(
		"%s: no effective_at for %s%s", rulesTemplate, rules.NamePrefix, number,
	)
}

// schemaFilePath is the repository path of the schema module, for the source-file note in the
// generated schema.
func schemaFilePath(version schema.Version) string {
	return schemaDirectory + "/" + version.SourcePath
}

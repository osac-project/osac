/*
Copyright (c) 2026 Red Hat, Inc.

Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except
in compliance with the License. You may obtain a copy of the License at

  http://www.apache.org/licenses/LICENSE-2.0
*/

// Command metering-contract regenerates osac-metering/docs/contract/ from the metering field
// registry, the default rule revision, and the published schema files. Run it from the
// metering-service module: `go run ./cmd/metering-contract`.
package main

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"

	"github.com/osac-project/osac-metering/tools/contract"
)

func main() {
	repoRoot := flag.String(
		"repo-root", "", "repository root; defaults to the nearest parent holding go.work",
	)
	check := flag.Bool(
		"check", false, "fail when the committed files are stale instead of writing them",
	)
	flag.Parse()

	root, err := resolveRoot(*repoRoot)
	if err != nil {
		fail(err)
	}
	if *check {
		err = contract.Check(root)
	} else {
		err = contract.Write(root)
	}
	if err != nil {
		fail(err)
	}
}

// resolveRoot finds the repository root by walking up from the working directory until a
// checkout root is reached. The binary runs from the metering-service module, so the default
// keeps the command's arguments empty.
func resolveRoot(configured string) (string, error) {
	if configured != "" {
		return configured, nil
	}
	directory, err := os.Getwd()
	if err != nil {
		return "", fmt.Errorf("reading the working directory: %w", err)
	}
	for {
		if _, err := os.Stat(filepath.Join(directory, "go.work")); err == nil {
			return directory, nil
		}
		parent := filepath.Dir(directory)
		if parent == directory {
			return "", fmt.Errorf(
				"no repository root found above the working directory; pass -repo-root",
			)
		}
		directory = parent
	}
}

func fail(err error) {
	fmt.Fprintln(os.Stderr, "metering-contract:", err)
	os.Exit(1)
}

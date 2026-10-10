/*
Copyright (c) 2026 Red Hat, Inc.

Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except
in compliance with the License. You may obtain a copy of the License at

  http://www.apache.org/licenses/LICENSE-2.0
*/

// Package envutil provides common environment variable and file-reading
// helpers for adapter main functions. All functions that encounter an
// error call log.Fatalf, making them suitable for use during process
// startup only.
package envutil

import (
	"log"
	"os"
	"strings"
)

// RequireEnv returns the value of the named environment variable or
// terminates the process if it is empty or unset.
func RequireEnv(key string) string {
	v := os.Getenv(key)
	if v == "" {
		log.Fatalf("%s is required", key)
	}
	return v
}

// EnvOrDefault returns the value of the named environment variable, or
// fallback if the variable is empty or unset.
func EnvOrDefault(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

// ReadFileOrFatal reads the file at path, trims whitespace, and returns
// the result. It terminates the process if the file cannot be read or
// is empty after trimming.
func ReadFileOrFatal(path string) string {
	data, err := os.ReadFile(path)
	if err != nil {
		log.Fatalf("reading %s: %v", path, err)
	}
	trimmed := strings.TrimSpace(string(data))
	if trimmed == "" {
		log.Fatalf("%s is empty", path)
	}
	return trimmed
}

// SplitAndTrim splits s by sep, trims whitespace from each part, and
// returns only the non-empty parts.
func SplitAndTrim(s, sep string) []string {
	parts := strings.Split(s, sep)
	var result []string
	for _, p := range parts {
		if trimmed := strings.TrimSpace(p); trimmed != "" {
			result = append(result, trimmed)
		}
	}
	return result
}

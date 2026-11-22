// Copyright (c) 2026 Red Hat Inc.
//
// Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except
// in compliance with the License. You may obtain a copy of the License at
//
//   http://www.apache.org/licenses/LICENSE-2.0

// Package allowlist decides which Thanos Query endpoints the admin Routes may
// reach. The list is data, not code: the chart renders it into a ConfigMap and
// the proxy reads it at start-up, so an endpoint can only be opened by changing
// the deployed configuration.
package allowlist

import (
	"errors"
	"fmt"
	"io/fs"
	"net/http"
	"os"
	"regexp"
	"strings"
)

// namePlaceholder stands for one path segment in a specifier, as in
// /api/v1/label/<name>/values. It is the only placeholder accepted: every other
// character is matched literally, so a typo narrows the list instead of
// turning it into a wildcard.
const namePlaceholder = "<name>"

// DefaultSpecifiers is the endpoint list the chart renders into the allowlist
// ConfigMap, and the fallback when that file is not mounted. Keeping one
// constant means the deployed list and the compiled-in default cannot drift.
const DefaultSpecifiers = "/api/v1/query\n" +
	"/api/v1/query_range\n" +
	"/api/v1/series\n" +
	"/api/v1/labels\n" +
	"/api/v1/label/<name>/values\n"

// Matcher holds the compiled endpoint list.
type Matcher struct {
	specifiers []string
	patterns   []*regexp.Regexp
}

// Default returns a matcher over DefaultSpecifiers.
func Default() *Matcher {
	matcher, err := parse(DefaultSpecifiers)
	if err != nil {
		// DefaultSpecifiers is a package constant covered by the tests; a
		// failure here is a programming error, not an input error.
		panic(fmt.Sprintf("allowlist: default specifiers are invalid: %v", err))
	}
	return matcher
}

// Load reads a newline-delimited list of endpoint paths. Blank lines and lines
// starting with '#' are ignored. A missing file yields Default so a proxy with
// no mounted ConfigMap still serves only the documented endpoints; an existing
// but empty file is an error rather than a silent "allow all".
func Load(path string) (*Matcher, error) {
	content, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return Default(), nil
	}
	if err != nil {
		return nil, fmt.Errorf("read allowlist %s: %w", path, err)
	}
	matcher, err := parse(string(content))
	if err != nil {
		return nil, fmt.Errorf("allowlist %s: %w", path, err)
	}
	return matcher, nil
}

func parse(content string) (*Matcher, error) {
	matcher := &Matcher{}
	for line := range strings.SplitSeq(content, "\n") {
		spec := strings.TrimSpace(line)
		if spec == "" || strings.HasPrefix(spec, "#") {
			continue
		}
		pattern, err := compile(spec)
		if err != nil {
			return nil, err
		}
		matcher.specifiers = append(matcher.specifiers, spec)
		matcher.patterns = append(matcher.patterns, pattern)
	}
	if len(matcher.patterns) == 0 {
		return nil, errors.New("no endpoint is listed")
	}
	return matcher, nil
}

// compile turns one specifier into a pattern anchored to the whole path.
func compile(spec string) (*regexp.Regexp, error) {
	if !strings.HasPrefix(spec, "/") {
		return nil, fmt.Errorf("endpoint %q must be an absolute path with no method", spec)
	}
	if strings.ContainsAny(spec, " \t") {
		return nil, fmt.Errorf("endpoint %q must not contain whitespace", spec)
	}
	var pattern strings.Builder
	pattern.WriteString("^")
	rest := spec
	for {
		index := strings.IndexAny(rest, "<>")
		if index < 0 {
			pattern.WriteString(regexp.QuoteMeta(rest))
			break
		}
		if !strings.HasPrefix(rest[index:], namePlaceholder) {
			return nil, fmt.Errorf("endpoint %q uses an unknown placeholder", spec)
		}
		pattern.WriteString(regexp.QuoteMeta(rest[:index]))
		pattern.WriteString("[^/]+")
		rest = rest[index+len(namePlaceholder):]
	}
	pattern.WriteString("$")
	return regexp.Compile(pattern.String())
}

// Allowed reports whether method may reach path. Only GET and POST are proxied;
// every other method is denied even on a listed path.
func (matcher *Matcher) Allowed(method, path string) bool {
	if method != http.MethodGet && method != http.MethodPost {
		return false
	}
	for _, pattern := range matcher.patterns {
		if pattern.MatchString(path) {
			return true
		}
	}
	return false
}

// Specifiers returns the configured endpoint list in order.
func (matcher *Matcher) Specifiers() []string {
	return append([]string(nil), matcher.specifiers...)
}

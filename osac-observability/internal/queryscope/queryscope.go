// Copyright (c) 2026 Red Hat Inc.
//
// Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except
// in compliance with the License. You may obtain a copy of the License at
//
//   http://www.apache.org/licenses/LICENSE-2.0

// Package queryscope restricts a Prometheus query API request to one metric
// visibility class. It is the enforcement half of the provider/platform split:
// the management cluster stamps a `visibility` label on every series at ingest,
// and this package makes the label unavoidable for the persona that must not
// see the other class.
//
// Every supported endpoint is rewritten, not just queries: /series, /labels and
// /label/<name>/values leak metric and label names without a selector, so they
// receive one too. A request this package cannot parse is rejected rather than
// forwarded unscoped.
package queryscope

import (
	"fmt"
	"net/url"
	"strings"

	"github.com/prometheus/prometheus/model/labels"
	"github.com/prometheus/prometheus/promql/parser"
)

// Rewriter injects one equality matcher into every selector a request carries.
// The zero value is not usable; Label and Value must both be set.
type Rewriter struct {
	Label string
	Value string
}

// Enabled reports whether the rewriter was configured. An empty scope means the
// persona may see every visibility class.
func (r Rewriter) Enabled() bool {
	return r.Label != "" && r.Value != ""
}

func (r Rewriter) matcher() *labels.Matcher {
	return &labels.Matcher{Type: labels.MatchEqual, Name: r.Label, Value: r.Value}
}

// Query rewrites one PromQL expression so every vector selector carries the
// scope matcher. Function calls, joins, subqueries and the @/offset modifiers
// are preserved because the expression is re-rendered from its AST.
func (r Rewriter) Query(expression string) (string, error) {
	if strings.TrimSpace(expression) == "" {
		return "", fmt.Errorf("query parameter is empty")
	}
	parsed, err := parser.NewParser(parser.Options{}).ParseExpr(expression)
	if err != nil {
		return "", fmt.Errorf("parse query: %w", err)
	}
	parser.Inspect(parsed, func(node parser.Node, _ []parser.Node) error {
		if selector, ok := node.(*parser.VectorSelector); ok {
			selector.LabelMatchers = append(selector.LabelMatchers, r.matcher())
		}
		return nil
	})
	return parsed.String(), nil
}

// Selector rewrites one `match[]` selector.
func (r Rewriter) Selector(selector string) (string, error) {
	matchers, err := parser.NewParser(parser.Options{}).ParseMetricSelector(selector)
	if err != nil {
		return "", fmt.Errorf("parse match[]: %w", err)
	}
	matchers = append(matchers, r.matcher())
	rendered := make([]string, len(matchers))
	for i, matcher := range matchers {
		rendered[i] = matcher.String()
	}
	return "{" + strings.Join(rendered, ",") + "}", nil
}

// Request rewrites the parameters of one allowlisted request path in place. The
// path is matched against the five endpoints the allowlist serves; anything
// else is an error, because a path this package does not understand must never
// reach the store unscoped.
func (r Rewriter) Request(path string, params url.Values) error {
	if !r.Enabled() {
		return nil
	}
	switch {
	case path == "/api/v1/query" || path == "/api/v1/query_range":
		scoped, err := r.Query(params.Get("query"))
		if err != nil {
			return err
		}
		params.Set("query", scoped)
		return nil
	case path == "/api/v1/series" || path == "/api/v1/labels" ||
		(strings.HasPrefix(path, "/api/v1/label/") && strings.HasSuffix(path, "/values")):
		return r.rewriteMatchers(params)
	default:
		return fmt.Errorf("path %q is not scoped", path)
	}
}

// rewriteMatchers scopes a listing endpoint. Prometheus treats an absent
// match[] as "every series", which would enumerate the other visibility class,
// so an empty list is replaced with the scope selector alone.
func (r Rewriter) rewriteMatchers(params url.Values) error {
	selectors := params["match[]"]
	if len(selectors) == 0 {
		params.Set("match[]", "{"+r.matcher().String()+"}")
		return nil
	}
	scoped := make([]string, len(selectors))
	for i, selector := range selectors {
		rewritten, err := r.Selector(selector)
		if err != nil {
			return err
		}
		scoped[i] = rewritten
	}
	params["match[]"] = scoped
	return nil
}

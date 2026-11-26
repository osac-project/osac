// Copyright (c) 2026 Red Hat Inc.
//
// Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except
// in compliance with the License. You may obtain a copy of the License at
//
//   http://www.apache.org/licenses/LICENSE-2.0

package queryscope

import (
	"net/url"
	"strings"
	"testing"

	"github.com/prometheus/prometheus/model/labels"
	"github.com/prometheus/prometheus/promql/parser"
)

func testRewriter() Rewriter {
	return Rewriter{Label: "visibility", Value: "platform"}
}

// assertEverySelectorScoped parses an expression and fails when any vector
// selector escapes the scope matcher. Parsing the result rather than comparing
// strings keeps the test independent of how the AST re-renders.
func assertEverySelectorScoped(t *testing.T, expression string) {
	t.Helper()
	parsed, err := parser.NewParser(expression).ParseExpr()
	if err != nil {
		t.Fatalf("rewritten query does not parse: %v\n%s", err, expression)
	}
	selectors := 0
	parser.Inspect(parsed, func(node parser.Node, _ []parser.Node) error {
		selector, ok := node.(*parser.VectorSelector)
		if !ok {
			return nil
		}
		selectors++
		for _, matcher := range selector.LabelMatchers {
			if matcher.Name == "visibility" && matcher.Type == labels.MatchEqual && matcher.Value == "platform" {
				return nil
			}
		}
		t.Errorf("selector in %q has no visibility=platform matcher: %v", expression, selector.LabelMatchers)
		return nil
	})
	if selectors == 0 {
		t.Fatalf("rewritten query %q has no vector selector", expression)
	}
}

func TestQueryScopesEveryShapeOfSelector(t *testing.T) {
	for _, expression := range []string{
		"up",
		`up{job="api"}`,
		`{__name__=~".+"}`,
		`sum by (namespace) (rate(container_cpu_usage_seconds_total[5m]))`,
		`up and on(instance) node_load1`,
		`up / ignoring(job) node_load1`,
		`absent(up{job="missing"})`,
		`max_over_time(rate(up[5m:1m])[1h:5m])`,
		`up offset 1h`,
		`up @ 1609459200`,
		`label_replace(up, "dst", "$1", "src", "(.*)")`,
	} {
		scoped, err := testRewriter().Query(expression)
		if err != nil {
			t.Fatalf("Query(%q): %v", expression, err)
		}
		if !strings.Contains(scoped, `visibility="platform"`) {
			t.Fatalf("Query(%q) = %q, scope matcher missing", expression, scoped)
		}
		assertEverySelectorScoped(t, scoped)
	}
}

func TestQueryRejectsWhatItCannotParse(t *testing.T) {
	for _, expression := range []string{"", "   ", "up{", "rate(up[5m]", "sum(up"} {
		if _, err := testRewriter().Query(expression); err == nil {
			t.Fatalf("Query(%q) accepted an unparsable expression", expression)
		}
	}
}

func TestSelectorAddsTheScopeMatcher(t *testing.T) {
	scoped, err := testRewriter().Selector(`up{job="api"}`)
	if err != nil {
		t.Fatalf("Selector: %v", err)
	}
	matchers, err := parser.ParseMetricSelector(scoped)
	if err != nil {
		t.Fatalf("rewritten selector does not parse: %v", err)
	}
	found := false
	for _, matcher := range matchers {
		if matcher.Name == "visibility" && matcher.Value == "platform" {
			found = true
		}
	}
	if !found {
		t.Fatalf("rewritten selector %q has no scope matcher", scoped)
	}
}

func TestRequestScopesQueryAndRange(t *testing.T) {
	for _, path := range []string{"/api/v1/query", "/api/v1/query_range"} {
		params := url.Values{"query": {`up{job="api"}`}}
		if err := testRewriter().Request(path, params); err != nil {
			t.Fatalf("%s: %v", path, err)
		}
		assertEverySelectorScoped(t, params.Get("query"))
	}
}

func TestRequestRejectsAQueryOnlyRequestWithoutQuery(t *testing.T) {
	if err := testRewriter().Request("/api/v1/query", url.Values{}); err == nil {
		t.Fatal("query endpoint accepted a request with no query")
	}
}

// A listing endpoint with no match[] lists every series, so the scope selector
// must be added rather than left to mean "everything".
func TestRequestScopesListingEndpointsWithoutMatchers(t *testing.T) {
	for _, path := range []string{"/api/v1/series", "/api/v1/labels", "/api/v1/label/__name__/values"} {
		params := url.Values{}
		if err := testRewriter().Request(path, params); err != nil {
			t.Fatalf("%s: %v", path, err)
		}
		matchers := params["match[]"]
		if len(matchers) != 1 {
			t.Fatalf("%s: match[] = %v, want one scope selector", path, matchers)
		}
		parsed, err := parser.ParseMetricSelector(matchers[0])
		if err != nil {
			t.Fatalf("%s: scope selector does not parse: %v", path, err)
		}
		if len(parsed) != 1 || parsed[0].Name != "visibility" || parsed[0].Value != "platform" {
			t.Fatalf("%s: scope selector = %v", path, parsed)
		}
	}
}

func TestRequestScopesEveryExistingMatcher(t *testing.T) {
	params := url.Values{"match[]": {`up{job="api"}`, `{__name__=~"node_.*"}`}}
	if err := testRewriter().Request("/api/v1/series", params); err != nil {
		t.Fatalf("Request: %v", err)
	}
	for _, selector := range params["match[]"] {
		if !strings.Contains(selector, `visibility="platform"`) {
			t.Fatalf("selector %q was not scoped", selector)
		}
	}
}

func TestRequestRejectsAPathItDoesNotUnderstand(t *testing.T) {
	if err := testRewriter().Request("/api/v1/admin/tsdb/delete_series", url.Values{}); err == nil {
		t.Fatal("an unscopable path was forwarded")
	}
}

func TestDisabledRewriterLeavesTheRequestAlone(t *testing.T) {
	params := url.Values{"query": {"up"}}
	if err := (Rewriter{}).Request("/api/v1/query", params); err != nil {
		t.Fatalf("disabled Request: %v", err)
	}
	if got := params.Get("query"); got != "up" {
		t.Fatalf("disabled rewriter changed the query to %q", got)
	}
}

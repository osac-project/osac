/*
Copyright (c) 2026 Red Hat Inc.

Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except in compliance with the
License. You may obtain a copy of the License at

  http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on an
"AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the specific
language governing permissions and limitations under the License.
*/

package grpcserver

import (
	"go/ast"
	"go/parser"
	"go/token"
	"strings"
	"testing"
)

// registerServerExemptMarker must appear in a comment on the line immediately above any publicv1/privatev1
// Register*Server(grpcServer, ...) call that is intentionally kept in start_grpc_server_cmd.go instead of being
// wired up through RegisterResourceServers in register_servers.go. Filterable resources (anything with a List RPC
// and a CEL filter field) belong in RegisterResourceServers so they're covered by its single, testable code path —
// see that function's doc comment. New resources landing directly in start_grpc_server_cmd.go instead have
// repeatedly caused painful rebase conflicts between this file and register_servers.go, since both evolve the same
// registration logic independently. This test exists to catch that at review time instead.
const registerServerExemptMarker = "filterable-resource-exempt:"

// TestNoInlineResourceServerRegistration parses start_grpc_server_cmd.go's source and fails if it finds a
// publicv1/privatev1 Register*Server call registering directly on grpcServer without an explicit
// registerServerExemptMarker comment on the preceding line explaining why it isn't part of RegisterResourceServers.
func TestNoInlineResourceServerRegistration(t *testing.T) {
	const fileName = "start_grpc_server_cmd.go"

	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, fileName, nil, parser.ParseComments)
	if err != nil {
		t.Fatalf("failed to parse %s: %v", fileName, err)
	}

	// Map the line immediately after each comment group to that group's full text, so a marker anywhere in a
	// multi-line comment block is found regardless of which line it's on.
	textByLineAfter := map[int]string{}
	for _, group := range file.Comments {
		lineAfter := fset.Position(group.End()).Line + 1
		textByLineAfter[lineAfter] = group.Text()
	}

	ast.Inspect(file, func(n ast.Node) bool {
		call, ok := n.(*ast.CallExpr)
		if !ok {
			return true
		}
		sel, ok := call.Fun.(*ast.SelectorExpr)
		if !ok {
			return true
		}
		pkgIdent, ok := sel.X.(*ast.Ident)
		if !ok {
			return true
		}
		if pkgIdent.Name != "publicv1" && pkgIdent.Name != "privatev1" {
			return true
		}
		if !strings.HasPrefix(sel.Sel.Name, "Register") || !strings.HasSuffix(sel.Sel.Name, "Server") {
			return true
		}
		if len(call.Args) == 0 {
			return true
		}
		firstArg, ok := call.Args[0].(*ast.Ident)
		if !ok || firstArg.Name != "grpcServer" {
			return true
		}

		line := fset.Position(call.Pos()).Line
		if comment, ok := textByLineAfter[line]; ok && strings.Contains(comment, registerServerExemptMarker) {
			return true
		}

		t.Errorf(
			"%s:%d: %s.%s is registered directly on grpcServer without a preceding %q comment.\n"+
				"Filterable resources (anything with a List RPC and a CEL filter field) must be registered "+
				"through RegisterResourceServers in register_servers.go instead, so they get covered by its "+
				"single, testable registration path.\n"+
				"If this server is genuinely not a filterable resource, add a comment directly above this "+
				"line explaining why, containing %q.",
			fileName, line, pkgIdent.Name, sel.Sel.Name, registerServerExemptMarker, registerServerExemptMarker,
		)
		return true
	})
}

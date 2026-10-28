/*
Copyright (c) 2026 Red Hat Inc.

Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except in compliance with the
License. You may obtain a copy of the License at

  http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on an
"AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
specific language governing permissions and limitations under the License.
*/

package dao

import (
	"github.com/google/cel-go/common/ast"
	"github.com/google/cel-go/common/operators"

	metadatautil "github.com/osac-project/osac/fulfillment-service/internal/metadata"
)

// ContainsReservedMetadataReference reports whether expr explicitly indexes or tests membership of a reserved key in
// metadata.labels or metadata.annotations. It walks the complete expression tree so a nested or mixed predicate cannot
// hide a reserved reference.
func ContainsReservedMetadataReference(expr ast.Expr) bool {
	if expr == nil {
		return false
	}
	switch expr.Kind() {
	case ast.CallKind:
		return callContainsReservedMetadataReference(expr.AsCall())
	case ast.SelectKind:
		return ContainsReservedMetadataReference(expr.AsSelect().Operand())
	case ast.ListKind:
		return containsReservedMetadataReferenceInExprs(expr.AsList().Elements())
	case ast.MapKind:
		return mapContainsReservedMetadataReference(expr.AsMap())
	case ast.StructKind:
		return structContainsReservedMetadataReference(expr.AsStruct())
	case ast.ComprehensionKind:
		comprehension := expr.AsComprehension()
		return containsReservedMetadataReferenceInExprs([]ast.Expr{
			comprehension.IterRange(),
			comprehension.AccuInit(),
			comprehension.LoopCondition(),
			comprehension.LoopStep(),
			comprehension.Result(),
		})
	}
	return false
}

func callContainsReservedMetadataReference(call ast.CallExpr) bool {
	args := call.Args()
	if call.FunctionName() == operators.Index && len(args) == 2 &&
		isMetadataMap(args[0]) && hasReservedMetadataLiteral(args[1]) {
		return true
	}
	if call.FunctionName() == operators.In && len(args) == 2 &&
		isMetadataMap(args[1]) && hasReservedMetadataLiteral(args[0]) {
		return true
	}
	return ContainsReservedMetadataReference(call.Target()) || containsReservedMetadataReferenceInExprs(args)
}

func containsReservedMetadataReferenceInExprs(exprs []ast.Expr) bool {
	for _, expr := range exprs {
		if ContainsReservedMetadataReference(expr) {
			return true
		}
	}
	return false
}

func mapContainsReservedMetadataReference(expression ast.MapExpr) bool {
	for _, entry := range expression.Entries() {
		if entry.Kind() != ast.MapEntryKind {
			continue
		}
		mapEntry := entry.AsMapEntry()
		if ContainsReservedMetadataReference(mapEntry.Key()) || ContainsReservedMetadataReference(mapEntry.Value()) {
			return true
		}
	}
	return false
}

func structContainsReservedMetadataReference(expression ast.StructExpr) bool {
	for _, entry := range expression.Fields() {
		if entry.Kind() == ast.StructFieldKind && ContainsReservedMetadataReference(entry.AsStructField().Value()) {
			return true
		}
	}
	return false
}

func hasReservedMetadataLiteral(expr ast.Expr) bool {
	if expr.Kind() != ast.LiteralKind {
		return false
	}
	key, ok := expr.AsLiteral().Value().(string)
	if !ok {
		return false
	}
	return metadatautil.IsReservedMetadataKey(key)
}

func isMetadataMap(expr ast.Expr) bool {
	fields := make([]string, 0, 3)
	for expr != nil && expr.Kind() == ast.SelectKind {
		selectExpr := expr.AsSelect()
		fields = append(fields, selectExpr.FieldName())
		expr = selectExpr.Operand()
	}
	if expr == nil || expr.Kind() != ast.IdentKind || (expr.AsIdent() != "this" && expr.AsIdent() != "event") {
		return false
	}
	return len(fields) >= 2 && fields[1] == "metadata" && (fields[0] == "labels" || fields[0] == "annotations")
}

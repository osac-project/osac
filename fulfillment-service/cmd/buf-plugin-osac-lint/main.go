/*
Copyright (c) 2026 Red Hat Inc.

Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except in compliance with the
License. You may obtain a copy of the License at

  http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on an
"AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the specific
language governing permissions and limitations under the License.
*/

// This is a custom `buf` lint plugin. It implements the `OSAC_OBJECT_SHAPE` rule, which checks that
// the base message of every resource (the message returned by the `Get` RPC and accepted by the
// `Create` RPC of a service) follows the standard shape documented in `docs/API.md`:
//
//	message Thing {
//	  string id = 1;
//	  Metadata metadata = 2;
//	  ThingSpec spec = 3;
//	  ThingStatus status = 4;
//	}
package main

import (
	"context"

	"buf.build/go/bufplugin/check"
	"google.golang.org/protobuf/reflect/protoreflect"
)

const objectShapeRuleID = "OSAC_OBJECT_SHAPE"

func main() {
	check.Main(
		&check.Spec{
			Rules: []*check.RuleSpec{
				{
					ID:      objectShapeRuleID,
					Default: true,
					Purpose: "Checks that the object type returned by Get and accepted by Create has the standard " +
						"id/metadata/spec/status shape.",
					Type:    check.RuleTypeLint,
					Handler: check.RuleHandlerFunc(checkObjectShape),
				},
			},
		},
	)
}

func checkObjectShape(_ context.Context, responseWriter check.ResponseWriter, request check.Request) error {
	for _, fileDescriptor := range request.FileDescriptors() {
		if fileDescriptor.IsImport() {
			continue
		}
		services := fileDescriptor.ProtoreflectFileDescriptor().Services()
		for i := range services.Len() {
			checkService(responseWriter, services.Get(i))
		}
	}
	return nil
}

// checkService looks for a `Get` and a `Create` method whose messages both reference the same
// object type via an `object` field, and validates the shape of that object type.
func checkService(responseWriter check.ResponseWriter, service protoreflect.ServiceDescriptor) {
	getObject := resolveObjectType(service, "Get", "output")
	createObject := resolveObjectType(service, "Create", "input")
	if getObject == nil || createObject == nil {
		return
	}
	if getObject.FullName() != createObject.FullName() {
		return
	}
	checkShape(responseWriter, getObject)
}

// resolveObjectType finds the method with the given name on the service, then returns the message
// type of the `object` field (number 1) of its input or output message, as selected by `side`.
func resolveObjectType(
	service protoreflect.ServiceDescriptor,
	methodName string,
	side string,
) protoreflect.MessageDescriptor {
	methods := service.Methods()
	method := methods.ByName(protoreflect.Name(methodName))
	if method == nil {
		return nil
	}
	var envelope protoreflect.MessageDescriptor
	switch side {
	case "input":
		envelope = method.Input()
	case "output":
		envelope = method.Output()
	default:
		return nil
	}
	field := envelope.Fields().ByName("object")
	if field == nil || field.Number() != 1 || field.Kind() != protoreflect.MessageKind {
		return nil
	}
	return field.Message()
}

// checkShape validates that the given message has exactly the four fields expected of a base object
// type: `id`, `metadata`, `spec` and `status`. Rather than reporting every individual mismatch, it
// reports a single annotation that states the message doesn't adhere to the required shape and shows
// what that shape is, since the fix is always the same: rewrite the message to match it (or add a
// `buf:lint:ignore` comment for the documented flat-message exception).
func checkShape(responseWriter check.ResponseWriter, message protoreflect.MessageDescriptor) {
	if shapeMatches(message) {
		return
	}
	name := message.Name()
	responseWriter.AddAnnotation(
		check.WithMessagef(
			"Message %q is returned by Get and accepted by Create, so it must be a base object type with "+
				"exactly this shape:\n\n"+
				"  message %s {\n"+
				"    string id = 1;\n"+
				"    Metadata metadata = 2;\n"+
				"    %sSpec spec = 3;\n"+
				"    %sStatus status = 4;\n"+
				"  }\n",
			name, name, name, name,
		),
		check.WithDescriptor(message),
	)
}

// shapeMatches reports whether the message has exactly the four fields expected of a base object
// type: `id`, `metadata`, `spec` and `status`.
func shapeMatches(message protoreflect.MessageDescriptor) bool {
	fields := message.Fields()
	if fields.Len() != 4 {
		return false
	}
	name := string(message.Name())
	return fieldMatches(fields.ByNumber(1), "id", protoreflect.StringKind, "") &&
		fieldMatches(fields.ByNumber(2), "metadata", protoreflect.MessageKind, "Metadata") &&
		fieldMatches(fields.ByNumber(3), "spec", protoreflect.MessageKind, name+"Spec") &&
		fieldMatches(fields.ByNumber(4), "status", protoreflect.MessageKind, name+"Status")
}

// fieldMatches reports whether the given field (which may be nil, if absent) matches the expected
// name, kind and, for message fields, message type name.
func fieldMatches(
	field protoreflect.FieldDescriptor,
	expectedName string,
	expectedKind protoreflect.Kind,
	expectedMessageName string,
) bool {
	if field == nil {
		return false
	}
	if string(field.Name()) != expectedName || field.Kind() != expectedKind {
		return false
	}
	if expectedMessageName != "" && string(field.Message().Name()) != expectedMessageName {
		return false
	}
	return true
}

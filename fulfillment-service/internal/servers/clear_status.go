/*
Copyright (c) 2026 Red Hat Inc.

Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except in compliance with the
License. You may obtain a copy of the License at

  http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on an
"AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the specific
language governing permissions and limitations under the License.
*/

package servers

import (
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protoreflect"
)

// clearStatusField clears the status field from a proto message if it exists.
// Used only on Create requests where no prior status exists.
// Status fields are marked with OUTPUT_ONLY and should never be accepted from user input.
func clearStatusField(message proto.Message) {
	if message == nil {
		return
	}

	reflection := message.ProtoReflect()
	descriptor := reflection.Descriptor()
	fields := descriptor.Fields()

	// Look for a field named "status"
	statusField := fields.ByName("status")
	if statusField == nil {
		return
	}

	// Clear the status field
	reflection.Clear(statusField)
}

// restoreStatusField copies the status field from current to candidate.
// Used on Update requests to preserve controller-managed status.
// Status fields are OUTPUT_ONLY and should never be modified by user input.
func restoreStatusField(current, candidate proto.Message) {
	if current == nil || candidate == nil {
		return
	}

	currentReflect := current.ProtoReflect()
	candidateReflect := candidate.ProtoReflect()

	// Look for status field in both messages
	statusField := currentReflect.Descriptor().Fields().ByName("status")
	if statusField == nil {
		return
	}

	// If current has status, copy it to candidate
	if currentReflect.Has(statusField) {
		currentStatus := currentReflect.Get(statusField)

		// Clone singular message-valued status fields to prevent candidate callbacks from
		// mutating the current object's status. Leave scalar, list, and map fields unchanged.
		if statusField.Kind() == protoreflect.MessageKind && !statusField.IsList() && !statusField.IsMap() {
			clonedStatus := proto.Clone(currentStatus.Message().Interface())
			candidateReflect.Set(statusField, protoreflect.ValueOfMessage(clonedStatus.ProtoReflect()))
		} else {
			candidateReflect.Set(statusField, currentStatus)
		}
	} else {
		// If current has no status, clear it in candidate
		candidateReflect.Clear(statusField)
	}
}

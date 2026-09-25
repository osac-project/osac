/*
Copyright (c) 2025 Red Hat Inc.

Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except in compliance with the
License. You may obtain a copy of the License at

  http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on an
"AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the specific
language governing permissions and limitations under the License.
*/

package servers

import (
	grpccodes "google.golang.org/grpc/codes"
	grpcstatus "google.golang.org/grpc/status"
)

// resourceRef is the common interface for typed resource reference messages.
type resourceRef interface {
	GetId() string
	GetName() string
}

// refKey extracts a display/lookup key from a typed resource reference.
// Returns the id if set, otherwise the name.
func refKey(ref resourceRef) string {
	if id := ref.GetId(); id != "" {
		return id
	}
	return ref.GetName()
}

func validateImmutableSecurityGroups[T resourceRef](existing, updated []T, fieldPath string) error {
	if len(existing) != len(updated) {
		return grpcstatus.Errorf(grpccodes.InvalidArgument,
			"cannot change %s: security groups are immutable", fieldPath)
	}
	for i := range existing {
		if refKey(existing[i]) != refKey(updated[i]) {
			return grpcstatus.Errorf(grpccodes.InvalidArgument,
				"cannot change %s[%d]: security groups are immutable", fieldPath, i)
		}
	}
	return nil
}

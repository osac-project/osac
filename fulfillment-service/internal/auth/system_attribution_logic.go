/*
Copyright (c) 2025 Red Hat Inc.

Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except in compliance with the
License. You may obtain a copy of the License at

  http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on an
"AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the specific
language governing permissions and limitations under the License.
*/

package auth

import (
	"context"
	"log/slog"
)

// SystemAttributionLogicBuilder contains the data and logic needed to create system attribution logic.
type SystemAttributionLogicBuilder struct {
	logger *slog.Logger
}

// SystemAttributionLogic is an attribution logic implementation intended exclusively for the private API, where all
// objects are attributed to the "system" creator. This implementation should NOT be used for the public API.
type SystemAttributionLogic struct {
	logger *slog.Logger
}

// NewSystemAttributionLogic creates a new builder for system attribution logic.
func NewSystemAttributionLogic() *SystemAttributionLogicBuilder {
	return &SystemAttributionLogicBuilder{}
}

// SetLogger sets the logger that will be used by the attribution logic.
func (b *SystemAttributionLogicBuilder) SetLogger(value *slog.Logger) *SystemAttributionLogicBuilder {
	b.logger = value
	return b
}

// Build creates the system attribution logic.
func (b *SystemAttributionLogicBuilder) Build() (result *SystemAttributionLogic, err error) {
	// Create the attribution logic:
	result = &SystemAttributionLogic{
		logger: b.logger,
	}
	return
}

// DetermineAssignedCreator returns "system" as the creator for objects created through the private API.
func (l *SystemAttributionLogic) DetermineAssignedCreator(_ context.Context) (result string, err error) {
	result = "system"
	return
}

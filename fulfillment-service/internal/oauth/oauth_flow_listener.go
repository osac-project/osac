/*
Copyright (c) 2025 Red Hat Inc.

Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except in compliance with the
License. You may obtain a copy of the License at

  http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on an
"AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the specific
language governing permissions and limitations under the License.
*/

package oauth

import (
	"context"
	"time"
)

// FlowListener provides a high-level interface for OAuth-related user interactions. It allows the same OAuth code to
// work with different user interfaces: console-based interactions, mobile apps, web UIs, etc.
//
//go:generate mockgen -source=oauth_flow_listener.go -destination=oauth_flow_listener_mock.go -package=oauth FlowListener
type FlowListener interface {
	// Start is called when the flow is started. It should present the relevant information to the user, and return
	// as soon as possible.
	Start(ctx context.Context, event FlowStartEvent) error

	// End is called when the flow is ended. It should inform the user about the final result of the flow (success
	// or failure) and return as soon as possible.
	End(ctx context.Context, event FlowEndEvent) error
}

// FlowStartEvent contains all the information related to the start of the flow.
type FlowStartEvent struct {
	// Flow is the type of flow that is being started.
	Flow Flow

	// AuthorizationUri is the URI that the user should open to complete the code flow.
	AuthorizationUri string

	// UserCode is the code that the user needs to enter to complete the device flow.
	UserCode string

	// ExpiresIn is the lifetime of the code of the device flow, in seconds.
	ExpiresIn time.Duration

	// VerificationUri is the URI that the user should open to complete the device flow.
	VerificationUri string

	// VerificationUriComplete is the URI that the user should open to complete the device flow, but with the user
	// code embedded in it, which makes it easier to use for humans. This is optional and not all authorization
	// servers will return it.
	VerificationUriComplete string
}

// FlowEndEvent contains all the information related to the end of the flow.
type FlowEndEvent struct {
	// Outcome the the outcome of the flow. It will be true if the flow succeeded, false otherwise.
	Outcome bool
}

/*
Copyright (c) 2026 Red Hat Inc.

Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except in compliance with the
License. You may obtain a copy of the License at

  http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on an
"AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the specific
language governing permissions and limitations under the License.
*/

package events

import (
	"context"

	"google.golang.org/protobuf/proto"
)

// Notifier is the interface for components that know how to send event notifications using protocol buffers
// messages as payload.
//
//go:generate mockgen -destination=events_notifier_mock.go -package=events . Notifier
type Notifier interface {
	Notify(ctx context.Context, payload proto.Message) error
}

/*
Copyright (c) 2026 Red Hat Inc.

Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except in compliance with the
License. You may obtain a copy of the License at

  http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on an
"AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the specific
language governing permissions and limitations under the License.
*/

package fanout

import (
	"context"
	"fmt"

	"github.com/osac-project/osac/osac-notifications/internal/cevent"
)

// SubscriptionMatcher creates deliveries for subscriptions that match an event.
type SubscriptionMatcher interface {
	Match(ctx context.Context, accepted *cevent.Accepted) error
}

// Dispatcher runs inbox fan-out then optional subscription matching.
type Dispatcher struct {
	inbox   *Inbox
	matcher SubscriptionMatcher
}

// NewDispatcher builds a fan-out dispatcher. matcher may be nil.
func NewDispatcher(inbox *Inbox, matcher SubscriptionMatcher) *Dispatcher {
	return &Dispatcher{inbox: inbox, matcher: matcher}
}

// FanOut creates inbox notifications, then matches webhook subscriptions.
func (d *Dispatcher) FanOut(ctx context.Context, accepted *cevent.Accepted) error {
	if d == nil || d.inbox == nil {
		return fmt.Errorf("fan-out dispatcher is not configured")
	}
	if err := d.inbox.FanOut(ctx, accepted); err != nil {
		return err
	}
	if d.matcher != nil {
		if err := d.matcher.Match(ctx, accepted); err != nil {
			return fmt.Errorf("subscription match: %w", err)
		}
	}
	return nil
}

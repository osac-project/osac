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

	"google.golang.org/grpc/credentials"
)

// ticketCredentials implements the gRPC PerRPCCredentials interface for single-use console session tickets. Unlike
// tokenCredentials, which wraps a long-lived TokenSource, this type carries an immutable ticket string that is used
// exactly once as a per-call credential.
type ticketCredentials struct {
	ticket string
}

// NewTicketCredentials creates per-call credentials that carry a single-use console session ticket in the Authorization
// header. The ticket is an opaque JWT issued by ConsoleSessions.Create and validated by ConsoleProxy.Connect.
func NewTicketCredentials(ticket string) credentials.PerRPCCredentials {
	return &ticketCredentials{ticket: ticket}
}

// GetRequestMetadata returns the Authorization header carrying the ticket as a Bearer token.
func (c *ticketCredentials) GetRequestMetadata(ctx context.Context, uri ...string) (map[string]string, error) {
	return map[string]string{
		"Authorization": "Bearer " + c.ticket,
	}, nil
}

// RequireTransportSecurity returns true because tickets are sensitive credentials that must not be sent over plaintext
// connections. Console commands already require Keycloak auth over TLS, so plaintext mode is unreachable before any
// ticket is issued.
func (c *ticketCredentials) RequireTransportSecurity() bool {
	return true
}

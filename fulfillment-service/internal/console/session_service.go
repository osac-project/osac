/*
Copyright (c) 2025 Red Hat Inc.

Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except in compliance with the
License. You may obtain a copy of the License at

  http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on an
"AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the specific
language governing permissions and limitations under the License.
*/

package console

import (
	"context"
	"errors"
	"log/slog"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"k8s.io/client-go/rest"
)

// DefaultTicketTTL is the TTL for console session tickets.
const DefaultTicketTTL = 30 * time.Second

// TargetResolver resolves a compute instance to the hub connection data needed
// for backend target construction. Implemented by the servers package resolver.
type TargetResolver interface {
	ResolveComputeInstance(ctx context.Context, resourceID string) (*ResolveResult, error)
}

// ResolveResult contains the hub cluster data needed to build a KubeVirt backend target.
type ResolveResult struct {
	HubConfig *rest.Config
	Namespace string
	CRName    string
}

// CreateSessionRequest is the domain request for creating a console session.
type CreateSessionRequest struct {
	User        string
	ResourceID  string
	ConsoleType string
	ClientID    string
}

// CreateSessionResult is the domain result of creating a console session.
type CreateSessionResult struct {
	Ticket    string
	ExpiresAt time.Time
}

// SessionServiceBuilder contains the data and logic needed to create a session service. Don't create instances of this
// type directly, use the NewSessionService function instead.
type SessionServiceBuilder struct {
	logger   *slog.Logger
	resolver TargetResolver
	sealer   *TicketSealer
}

// SessionService orchestrates console session creation: resolving the compute
// instance, building the KubeVirt backend target, and sealing the encrypted ticket.
type SessionService struct {
	logger   *slog.Logger
	resolver TargetResolver
	sealer   *TicketSealer
}

// NewSessionService creates a builder that can then be used to configure and create a new session service.
func NewSessionService() *SessionServiceBuilder {
	return &SessionServiceBuilder{}
}

// SetLogger sets the logger. This is mandatory.
func (b *SessionServiceBuilder) SetLogger(value *slog.Logger) *SessionServiceBuilder {
	b.logger = value
	return b
}

// SetResolver sets the target resolver. This is mandatory.
func (b *SessionServiceBuilder) SetResolver(value TargetResolver) *SessionServiceBuilder {
	b.resolver = value
	return b
}

// SetSealer sets the ticket sealer. This is mandatory.
func (b *SessionServiceBuilder) SetSealer(value *TicketSealer) *SessionServiceBuilder {
	b.sealer = value
	return b
}

// Build uses the data stored in the builder to create and configure a new session service.
func (b *SessionServiceBuilder) Build() (*SessionService, error) {
	// Check parameters:
	if b.logger == nil {
		return nil, errors.New("logger is mandatory")
	}
	if b.resolver == nil {
		return nil, errors.New("resolver is mandatory")
	}
	if b.sealer == nil {
		return nil, errors.New("sealer is mandatory")
	}

	// Create and populate the object:
	return &SessionService{
		logger:   b.logger,
		resolver: b.resolver,
		sealer:   b.sealer,
	}, nil
}

// CreateSession resolves a compute instance, builds the backend target, and
// seals the encrypted ticket for console access.
func (s *SessionService) CreateSession(ctx context.Context, req *CreateSessionRequest) (*CreateSessionResult, error) {
	// Resolve the compute instance: DB lookups + K8s CR lookup on the hub.
	resolved, err := s.resolver.ResolveComputeInstance(ctx, req.ResourceID)
	if err != nil {
		return nil, err
	}

	// Build the KubeVirt backend target (WebSocket URL + bearer token).
	target, err := BuildKubeVirtTarget(resolved.HubConfig, resolved.Namespace, resolved.CRName, req.ConsoleType)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "failed to build backend target: %v", err)
	}

	// Seal the ticket with pre-computed backend URI and token.
	ticket := &Ticket{
		User:        req.User,
		ClientID:    req.ClientID,
		ConsoleType: req.ConsoleType,
		TargetURI:   target.BackendURI,
		TargetToken: target.BackendToken,
	}
	tokenString, expiresAt, err := s.sealer.Seal(ctx, ticket, DefaultTicketTTL)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "failed to seal ticket: %v", err)
	}

	s.logger.InfoContext(ctx, "Console session ticket created",
		slog.String("resource_id", req.ResourceID),
		slog.String("user", req.User),
		slog.String("console_type", req.ConsoleType),
	)

	return &CreateSessionResult{
		Ticket:    tokenString,
		ExpiresAt: expiresAt,
	}, nil
}

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
	"context"
	"errors"
	"log/slog"

	"github.com/google/uuid"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/timestamppb"

	publicv1 "github.com/osac-project/fulfillment-service/internal/api/osac/public/v1"
	"github.com/osac-project/fulfillment-service/internal/auth"
	"github.com/osac-project/fulfillment-service/internal/console"
)

// ConsoleServerBuilder builds a ConsoleSessionsServer.
type ConsoleServerBuilder struct {
	logger         *slog.Logger
	sessionService *console.SessionService
}

// consoleSessionsServer implements the ConsoleSessions gRPC service. It is a thin
// adapter: validates and maps protobuf requests, delegates to the SessionService
// for domain orchestration, and maps results back to protobuf responses.
type consoleSessionsServer struct {
	publicv1.UnimplementedConsoleSessionsServer
	logger         *slog.Logger
	sessionService *console.SessionService
}

// NewConsoleServer creates a new builder for the console sessions server.
func NewConsoleServer() *ConsoleServerBuilder {
	return &ConsoleServerBuilder{}
}

func (b *ConsoleServerBuilder) SetLogger(value *slog.Logger) *ConsoleServerBuilder {
	b.logger = value
	return b
}

func (b *ConsoleServerBuilder) SetSessionService(value *console.SessionService) *ConsoleServerBuilder {
	b.sessionService = value
	return b
}

func (b *ConsoleServerBuilder) Build() (publicv1.ConsoleSessionsServer, error) {
	if b.logger == nil {
		return nil, errors.New("logger is mandatory")
	}
	if b.sessionService == nil {
		return nil, errors.New("session service is mandatory")
	}

	return &consoleSessionsServer{
		logger:         b.logger,
		sessionService: b.sessionService,
	}, nil
}

// Create validates the protobuf request, delegates to the SessionService for
// domain orchestration, and maps the result back to a protobuf response.
func (s *consoleSessionsServer) Create(ctx context.Context,
	req *publicv1.ConsoleSessionsCreateRequest) (response *publicv1.ConsoleSessionsCreateResponse, err error) {
	obj := req.GetObject()
	resourceType := obj.GetResourceType()
	resourceID := obj.GetResourceId()

	if resourceID == "" {
		err = status.Errorf(codes.InvalidArgument, "field 'resource_id' is mandatory")
		return
	}

	if resourceType != publicv1.ConsoleResourceType_CONSOLE_RESOURCE_TYPE_COMPUTE_INSTANCE {
		err = status.Errorf(codes.Unimplemented, "unsupported resource type: %s", resourceType.String())
		return
	}

	consoleType := mapConsoleType(obj.GetType())
	if consoleType == "" {
		err = status.Errorf(codes.InvalidArgument, "unsupported console type: %s", obj.GetType().String())
		return
	}

	clientID := obj.GetClientId()
	if clientID != "" {
		if _, err = uuid.Parse(clientID); err != nil {
			err = status.Errorf(codes.InvalidArgument, "client_id must be a valid UUID or empty")
			return
		}
	}

	subject := auth.SubjectFromContext(ctx)

	result, err := s.sessionService.CreateSession(ctx, &console.CreateSessionRequest{
		User:        subject.User,
		ResourceID:  resourceID,
		ConsoleType: consoleType,
		ClientID:    clientID,
	})
	if err != nil {
		return
	}

	response = publicv1.ConsoleSessionsCreateResponse_builder{
		Object: publicv1.ConsoleSession_builder{
			ResourceType: resourceType,
			ResourceId:   resourceID,
			Type:         obj.GetType(),
			ClientId:     clientID,
			Ticket:       result.Ticket,
			ExpiresAt:    timestamppb.New(result.ExpiresAt),
		}.Build(),
	}.Build()
	return
}

// mapConsoleType maps proto ConsoleType to internal console type string.
func mapConsoleType(ct publicv1.ConsoleType) string {
	switch ct {
	case publicv1.ConsoleType_CONSOLE_TYPE_SERIAL:
		return console.ConsoleTypeSerial
	case publicv1.ConsoleType_CONSOLE_TYPE_VNC:
		return console.ConsoleTypeVNC
	default:
		return ""
	}
}

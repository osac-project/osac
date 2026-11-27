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
	"errors"
	"fmt"
	"log/slog"
	"regexp"
	"strings"

	"google.golang.org/grpc"
	grpccodes "google.golang.org/grpc/codes"
	grpcstatus "google.golang.org/grpc/status"

	"github.com/osac-project/osac/osac-notifications/internal/cevent"
	privatev1 "github.com/osac-project/osac/proto/gen/osac/private/v1"
)

// ErrNonRetryable marks fan-out failures that should not stay pending (failed + DLQ).
var ErrNonRetryable = errors.New("non-retryable")

var dns1123Label = regexp.MustCompile(`^[a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?$`)

// NotificationsCreateClient creates inbox notifications via the private API.
type NotificationsCreateClient interface {
	Create(ctx context.Context, request *privatev1.NotificationsCreateRequest,
		opts ...grpc.CallOption) (*privatev1.NotificationsCreateResponse, error)
}

// Inbox creates one Notification per USER recipient.
type Inbox struct {
	logger slog.Logger
	client NotificationsCreateClient
}

// NewInbox builds an inbox fan-out helper.
func NewInbox(logger *slog.Logger, client NotificationsCreateClient) *Inbox {
	if logger == nil {
		logger = slog.Default()
	}
	return &Inbox{logger: *logger, client: client}
}

// FanOut creates inbox rows for USER recipients. AlreadyExists is treated as success.
func (i *Inbox) FanOut(ctx context.Context, accepted *cevent.Accepted) error {
	if i == nil || i.client == nil {
		return fmt.Errorf("inbox fan-out is not configured")
	}
	kind, err := kindFromSegment(accepted.Kind)
	if err != nil {
		return err
	}
	severity, err := severityFromSegment(accepted.Severity)
	if err != nil {
		return err
	}

	userIDs := cevent.UserRecipientIDs(accepted.Data.Recipients)
	if len(userIDs) == 0 {
		// Defensive: empty USER recipients are rejected at validation.
		return fmt.Errorf("%w: no USER recipients", ErrNonRetryable)
	}

	resource := mapResource(accepted.Tenant, accepted.Data.Resource)
	for _, userID := range userIDs {
		name, nameErr := inboxName(accepted.ID, userID)
		if nameErr != nil {
			return nameErr
		}
		object := privatev1.Notification_builder{
			Metadata: privatev1.Metadata_builder{
				Name:   name,
				Tenant: accepted.Tenant,
			}.Build(),
			Spec: privatev1.NotificationSpec_builder{
				Kind:        kind,
				Severity:    severity,
				Type:        accepted.Type,
				Title:       accepted.Data.Title,
				Source:      accepted.Source,
				RecipientId: userID,
			}.Build(),
		}.Build()
		if accepted.Data.Description != "" {
			object.GetSpec().SetDescription(accepted.Data.Description)
		}
		if resource != nil {
			object.GetSpec().SetObject(resource)
		}
		if len(accepted.Data.Details) > 0 {
			details := make(map[string]string, len(accepted.Data.Details))
			for k, v := range accepted.Data.Details {
				details[k] = fmt.Sprint(v)
			}
			object.GetSpec().SetDetails(details)
		}

		_, err := i.client.Create(ctx, privatev1.NotificationsCreateRequest_builder{
			Object: object,
		}.Build())
		if err != nil {
			if grpcstatus.Code(err) == grpccodes.AlreadyExists {
				continue
			}
			wrapped := fmt.Errorf("create inbox notification for recipient %q: %w", userID, err)
			if permanentGRPC(err) {
				return fmt.Errorf("%w: %w", ErrNonRetryable, wrapped)
			}
			return wrapped
		}
	}
	return nil
}

// inboxName builds a deterministic RFC 1123 DNS label.
// Invalid or over-long names return ErrNonRetryable.
func inboxName(eventID, userID string) (string, error) {
	name := strings.ToLower("inbox-" + userID + "-" + eventID)
	name = strings.ReplaceAll(name, "/", "-")
	name = strings.ReplaceAll(name, "_", "-")
	if !dns1123Label.MatchString(name) {
		return "", fmt.Errorf("%w: metadata.name %q is not a valid DNS label", ErrNonRetryable, name)
	}
	return name, nil
}

func permanentGRPC(err error) bool {
	switch grpcstatus.Code(err) {
	case grpccodes.InvalidArgument, grpccodes.NotFound, grpccodes.PermissionDenied,
		grpccodes.Unauthenticated, grpccodes.FailedPrecondition, grpccodes.Unimplemented:
		return true
	default:
		return false
	}
}

// Permanent reports whether fan-out should stop retrying (failed status + DLQ).
func Permanent(err error) bool {
	return err != nil && errors.Is(err, ErrNonRetryable)
}

func kindFromSegment(segment string) (privatev1.NotificationKind, error) {
	switch segment {
	case "event":
		return privatev1.NotificationKind_NOTIFICATION_KIND_EVENT, nil
	default:
		return 0, fmt.Errorf("unsupported kind %q", segment)
	}
}

func severityFromSegment(segment string) (privatev1.NotificationSeverity, error) {
	switch segment {
	case "info":
		return privatev1.NotificationSeverity_NOTIFICATION_SEVERITY_INFO, nil
	case "warning":
		return privatev1.NotificationSeverity_NOTIFICATION_SEVERITY_WARNING, nil
	case "critical":
		return privatev1.NotificationSeverity_NOTIFICATION_SEVERITY_CRITICAL, nil
	default:
		return 0, fmt.Errorf("unsupported severity %q", segment)
	}
}

// mapResource maps CE data.resource into a non-local NotificationObjectReference.
// CE wire only carries object ids; tenant comes from the accepted event.
func mapResource(tenant string, resource *cevent.Resource) *privatev1.NotificationObjectReference {
	if resource == nil {
		return nil
	}
	out := &privatev1.NotificationObjectReference{}
	switch {
	case resource.Cluster != nil && resource.Cluster.ID != "":
		out.SetCluster(privatev1.ClusterReference_builder{
			Id: resource.Cluster.ID, Tenant: tenant,
		}.Build())
	case resource.ComputeInstance != nil && resource.ComputeInstance.ID != "":
		out.SetComputeInstance(privatev1.ComputeInstanceReference_builder{
			Id: resource.ComputeInstance.ID, Tenant: tenant,
		}.Build())
	case resource.VirtualNetwork != nil && resource.VirtualNetwork.ID != "":
		out.SetVirtualNetwork(privatev1.VirtualNetworkReference_builder{
			Id: resource.VirtualNetwork.ID, Tenant: tenant,
		}.Build())
	case resource.Subnet != nil && resource.Subnet.ID != "":
		out.SetSubnet(privatev1.SubnetReference_builder{
			Id: resource.Subnet.ID, Tenant: tenant,
		}.Build())
	case resource.ExternalIP != nil && resource.ExternalIP.ID != "":
		out.SetExternalIp(privatev1.ExternalIPReference_builder{
			Id: resource.ExternalIP.ID, Tenant: tenant,
		}.Build())
	case resource.BareMetal != nil && resource.BareMetal.ID != "":
		out.SetBareMetalInstance(privatev1.BareMetalInstanceReference_builder{
			Id: resource.BareMetal.ID, Tenant: tenant,
		}.Build())
	default:
		return nil
	}
	return out
}

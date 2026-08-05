/*
Copyright (c) 2025 Red Hat Inc.

Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except in compliance with the
License. You may obtain a copy of the License at

  http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on an
"AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the specific
language governing permissions and limitations under the License.
*/

package health

import (
	"context"

	healthv1 "google.golang.org/grpc/health/grpc_health_v1"
)

// Reporter is an interface for components to report their health status.
//
//go:generate mockgen -destination=health_reporter_mock.go -package=health . Reporter
type Reporter interface {
	// Report reports the health status of a component. The name parameter identifies the component, and the status
	// parameter indicates the serving status. Only 'SERVING' and 'NOT_SERVING' are expected.
	Report(ctx context.Context, name string, status healthv1.HealthCheckResponse_ServingStatus)
}

/*
Copyright (c) 2026 Red Hat Inc.

Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except in compliance with the
License. You may obtain a copy of the License at

  http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on an
"AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
specific language governing permissions and limitations under the License.
*/

package networking

import (
	"context"
	"errors"
	"testing"

	"github.com/jackc/pgx/v5"
	privatev1 "github.com/osac-project/osac/proto/gen/osac/private/v1"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

type settingStore struct {
	value bool
	err   error
}

func (s settingStore) QueryRow(_ context.Context, sql string, args ...any) pgx.Row {
	if sql != readProvisioningSettingSQL || len(args) != 0 {
		return settingRow{err: errors.New("unexpected query")}
	}
	return settingRow(s)
}

type settingRow struct {
	value bool
	err   error
}

func (r settingRow) Scan(dest ...any) error {
	if r.err != nil {
		return r.err
	}
	*dest[0].(*bool) = r.value
	return nil
}

func TestReadInstalledProvisioning(t *testing.T) {
	for _, value := range []bool{true, false} {
		got, err := ReadInstalledProvisioning(context.Background(), settingStore{value: value})
		if err != nil || got != value {
			t.Fatalf("read %t: %t, %v", value, got, err)
		}
	}
	const sensitiveHost = "db.private.example.test"
	_, err := ReadInstalledProvisioning(
		context.Background(), settingStore{err: errors.New("connection to " + sensitiveHost + " failed")},
	)
	if err == nil || err.Error() != "read installation networking setting failed" {
		t.Fatalf("unexpected read result: %v", err)
	}
}

type remoteSettingReader struct {
	value bool
	err   error
}

func (r remoteSettingReader) Get(
	context.Context, *privatev1.InstallationSettingsGetRequest, ...grpc.CallOption,
) (*privatev1.InstallationSettingsGetResponse, error) {
	if r.err != nil {
		return nil, r.err
	}
	return privatev1.InstallationSettingsGetResponse_builder{NetworkingProvisioningEnabled: r.value}.Build(), nil
}

type sequencedRemoteSettingReader struct {
	value           bool
	errors          []error
	calls           int
	cancelAfterCall context.CancelFunc
}

func (r *sequencedRemoteSettingReader) Get(
	context.Context, *privatev1.InstallationSettingsGetRequest, ...grpc.CallOption,
) (*privatev1.InstallationSettingsGetResponse, error) {
	r.calls++
	if len(r.errors) > 0 {
		err := r.errors[0]
		r.errors = r.errors[1:]
		if r.cancelAfterCall != nil {
			r.cancelAfterCall()
		}
		return nil, err
	}
	return privatev1.InstallationSettingsGetResponse_builder{NetworkingProvisioningEnabled: r.value}.Build(), nil
}

func TestRemoteInstallationProvisioning(t *testing.T) {
	for _, value := range []bool{true, false} {
		if err := CheckRemoteInstallationProvisioning(
			context.Background(), remoteSettingReader{value: value}, value,
		); err != nil {
			t.Fatal(err)
		}
		err := CheckRemoteInstallationProvisioning(context.Background(), remoteSettingReader{value: value}, !value)
		if err == nil || err.Error() != ProvisioningSettingChangedMessage {
			t.Fatalf("mismatch: %v", err)
		}
	}
	if err := CheckRemoteInstallationProvisioning(
		context.Background(), remoteSettingReader{err: errors.New("unavailable")}, true,
	); err == nil {
		t.Fatal("unavailable API was accepted")
	}
}

func TestRemoteInstallationProvisioningRetriesTransientErrors(t *testing.T) {
	reader := &sequencedRemoteSettingReader{
		value: true,
		errors: []error{
			status.Error(codes.Unavailable, "fulfillment is starting"),
			status.Error(codes.DeadlineExceeded, "request timed out"),
		},
	}
	if err := CheckRemoteInstallationProvisioning(context.Background(), reader, true); err != nil {
		t.Fatalf("transient errors followed by success: %v", err)
	}
	if reader.calls != 3 {
		t.Fatalf("Get called %d times, want 3", reader.calls)
	}
}

func TestRemoteInstallationProvisioningDoesNotRetryPermanentErrors(t *testing.T) {
	reader := &sequencedRemoteSettingReader{
		errors: []error{status.Error(codes.PermissionDenied, "token at private.example.test")},
	}
	err := CheckRemoteInstallationProvisioning(context.Background(), reader, true)
	if err == nil || err.Error() != "read installation networking setting: gRPC status PermissionDenied" {
		t.Fatalf("expected sanitized permission error, got %v", err)
	}
	if reader.calls != 1 {
		t.Fatalf("Get called %d times, want 1", reader.calls)
	}
}

func TestRemoteInstallationProvisioningStopsRetryWhenContextCancelled(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	reader := &sequencedRemoteSettingReader{
		errors:          []error{status.Error(codes.Unavailable, "fulfillment is starting")},
		cancelAfterCall: cancel,
	}
	err := CheckRemoteInstallationProvisioning(ctx, reader, true)
	if err == nil || err.Error() != "read installation networking setting: gRPC status Canceled" {
		t.Fatalf("expected sanitized cancellation error, got %v", err)
	}
	if reader.calls != 1 {
		t.Fatalf("Get called %d times, want 1", reader.calls)
	}
}

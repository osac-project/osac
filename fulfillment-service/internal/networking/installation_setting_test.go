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
	"sync"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	privatev1 "github.com/osac-project/osac/proto/gen/osac/private/v1"
	"google.golang.org/grpc"
)

type settingStore struct {
	mu      sync.Mutex
	value   *bool
	execErr error
	readErr error
}

func (s *settingStore) Exec(_ context.Context, _ string, args ...any) (pgconn.CommandTag, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.execErr != nil {
		return pgconn.CommandTag{}, s.execErr
	}
	if s.value == nil {
		value := args[1].(bool)
		s.value = &value
	}
	return pgconn.CommandTag{}, nil
}
func (s *settingStore) QueryRow(context.Context, string, ...any) pgx.Row {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.readErr != nil {
		return settingRow{err: s.readErr}
	}
	if s.value == nil {
		return settingRow{err: pgx.ErrNoRows}
	}
	return settingRow{value: *s.value}
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

func TestInstallationProvisioning(t *testing.T) {
	for _, first := range []bool{true, false} {
		t.Run(map[bool]string{true: "enabled", false: "disabled"}[first], func(t *testing.T) {
			store := &settingStore{}
			for i := 0; i < 2; i++ {
				if err := CheckInstallationProvisioning(context.Background(), store, first); err != nil {
					t.Fatal(err)
				}
			}
			err := CheckInstallationProvisioning(context.Background(), store, !first)
			if err == nil || err.Error() != ProvisioningSettingChangedMessage {
				t.Fatalf("unexpected mismatch result: %v", err)
			}
			got, err := ReadInstalledProvisioning(context.Background(), store)
			if err != nil || got != first {
				t.Fatalf("saved value changed: %t, %v", got, err)
			}
		})
	}
	const sensitiveHost = "db.private.example.test"
	store := &settingStore{execErr: errors.New("connection to " + sensitiveHost + " failed")}
	err := CheckInstallationProvisioning(context.Background(), store, true)
	if err == nil || err.Error() != "store installation networking setting failed" {
		t.Fatalf("unexpected write result: %v", err)
	}
	store = &settingStore{readErr: errors.New("connection to " + sensitiveHost + " failed")}
	err = CheckInstallationProvisioning(context.Background(), store, true)
	if err == nil || err.Error() != "read installation networking setting failed" {
		t.Fatalf("unexpected read result: %v", err)
	}
}

func TestConcurrentInstallationProvisioning(t *testing.T) {
	store := &settingStore{}
	var wg sync.WaitGroup
	results := make([]error, 40)
	for i := range results {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			results[i] = CheckInstallationProvisioning(context.Background(), store, i%2 == 0)
		}(i)
	}
	wg.Wait()
	value, err := ReadInstalledProvisioning(context.Background(), store)
	if err != nil {
		t.Fatal(err)
	}
	for i, result := range results {
		if (i%2 == 0) == value && result != nil {
			t.Fatalf("matching start %d failed: %v", i, result)
		}
		if (i%2 == 0) != value && (result == nil || result.Error() != ProvisioningSettingChangedMessage) {
			t.Fatalf("mismatching start %d: %v", i, result)
		}
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

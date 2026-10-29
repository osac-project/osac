/*
Copyright (c) 2026 Red Hat Inc.

Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except in compliance with the
License. You may obtain a copy of the License at

  http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on an
"AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
specific language governing permissions and limitations under the License.
*/

package grpcserver

import (
	"context"
	"errors"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	privatev1 "github.com/osac-project/osac/proto/gen/osac/private/v1"
)

type installedSettingStore struct {
	value bool
	err   error
}

func (s installedSettingStore) Exec(context.Context, string, ...any) (pgconn.CommandTag, error) {
	panic("read-only API must not write")
}
func (s installedSettingStore) QueryRow(context.Context, string, ...any) pgx.Row {
	return installedSettingRow(s)
}

type installedSettingRow struct {
	value bool
	err   error
}

func (r installedSettingRow) Scan(dest ...any) error {
	if r.err != nil {
		return r.err
	}
	*dest[0].(*bool) = r.value
	return nil
}

func TestInstallationSettingsGet(t *testing.T) {
	for _, value := range []bool{true, false} {
		server := &installationSettingsServer{store: installedSettingStore{value: value}}
		result, err := server.Get(context.Background(), &privatev1.InstallationSettingsGetRequest{})
		if err != nil || result.GetNetworkingProvisioningEnabled() != value {
			t.Fatalf("get %t: %v, %v", value, result, err)
		}
	}
	server := &installationSettingsServer{store: installedSettingStore{err: errors.New("database unavailable")}}
	if _, err := server.Get(context.Background(), &privatev1.InstallationSettingsGetRequest{}); err == nil {
		t.Fatal("database failure was ignored")
	}
}

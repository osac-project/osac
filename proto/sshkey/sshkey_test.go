/*
Copyright (c) 2026 Red Hat Inc.

Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except in compliance with the
License. You may obtain a copy of the License at

  http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on an
"AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
specific language governing permissions and limitations under the License.
*/

package sshkey

import (
	"context"
	"errors"
	"strings"
	"testing"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/timestamppb"

	privatev1 "github.com/osac-project/osac/proto/gen/osac/private/v1"
)

const testKey = "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIG8K1ZuSC7tmzxD5LJJXwkCfStVEjzXWYCFhJaLBxWAn test@example.com"

func sshKeySecret(secretType privatev1.SecretType, data map[string][]byte) *privatev1.Secret {
	return privatev1.Secret_builder{
		Id:       "key-id",
		Metadata: privatev1.Metadata_builder{Name: "key-name", Tenant: "tenant1"}.Build(),
		Type:     secretType,
		Data:     data,
	}.Build()
}

func usableKeySecret() *privatev1.Secret {
	return sshKeySecret(privatev1.SecretType_SECRET_TYPE_SSH_PUBLIC_KEY,
		map[string][]byte{PublicKeyDataKey: []byte(testKey)})
}

// stubGetter records the requested ID and answers with a fixed object or error.
type stubGetter struct {
	object  *privatev1.Secret
	err     error
	lastArg *privatev1.SecretsGetRequest
}

func (s *stubGetter) Get(_ context.Context, in *privatev1.SecretsGetRequest, _ ...grpc.CallOption) (*privatev1.SecretsGetResponse, error) {
	s.lastArg = in
	if s.err != nil {
		return nil, s.err
	}
	return privatev1.SecretsGetResponse_builder{Object: s.object}.Build(), nil
}

func TestValidateSecretData(t *testing.T) {
	t.Run("accepts a well-formed SSH-key Secret", func(t *testing.T) {
		secret := usableKeySecret()
		if err := ValidateSecretData(secret, secret.GetData(), "key-id"); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
	})

	t.Run("rejects a wrong-type Secret", func(t *testing.T) {
		secret := sshKeySecret(privatev1.SecretType_SECRET_TYPE_USER_DATA,
			map[string][]byte{"userdata": []byte("#cloud-config")})
		err := ValidateSecretData(secret, secret.GetData(), "key-id")
		if err == nil || !strings.Contains(err.Error(), "expected SECRET_TYPE_SSH_PUBLIC_KEY") {
			t.Fatalf("expected a type error, got: %v", err)
		}
	})

	t.Run("rejects an empty public_key entry", func(t *testing.T) {
		secret := sshKeySecret(privatev1.SecretType_SECRET_TYPE_SSH_PUBLIC_KEY, nil)
		err := ValidateSecretData(secret, secret.GetData(), "key-id")
		if err == nil || !strings.Contains(err.Error(), "public_key") {
			t.Fatalf("expected an empty-entry error, got: %v", err)
		}
	})

	t.Run("rejects malformed key material without echoing it", func(t *testing.T) {
		secret := sshKeySecret(privatev1.SecretType_SECRET_TYPE_SSH_PUBLIC_KEY,
			map[string][]byte{PublicKeyDataKey: []byte("not-a-key")})
		err := ValidateSecretData(secret, secret.GetData(), "key-id")
		if err == nil || !strings.Contains(err.Error(), "invalid OpenSSH public key") {
			t.Fatalf("expected a format error, got: %v", err)
		}
		if strings.Contains(err.Error(), testKey) {
			t.Fatalf("error leaks key material: %v", err)
		}
	})
}

// fetchClassification asserts the Permanent/Reason mapping of a Fetch failure.
func fetchClassification(t *testing.T, err error, permanent bool, reason string) {
	t.Helper()
	var fetchErr *Error
	if !errors.As(err, &fetchErr) {
		t.Fatalf("expected a typed error, got: %v", err)
	}
	if fetchErr.Permanent != permanent || fetchErr.Reason != reason {
		t.Fatalf("expected classification (permanent=%v, reason=%q), got (%v, %q)",
			permanent, reason, fetchErr.Permanent, fetchErr.Reason)
	}
}

func TestFetch(t *testing.T) {
	t.Run("returns the public key material for a valid secret", func(t *testing.T) {
		client := &stubGetter{object: usableKeySecret()}
		key, err := Fetch(context.Background(), client, "key-id", "tenant1")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if key != testKey {
			t.Fatalf("unexpected key material: %q", key)
		}
		if client.lastArg.GetId() != "key-id" {
			t.Fatalf("expected fetch by id, got %q", client.lastArg.GetId())
		}
	})

	t.Run("classifies an empty id as a permanent invalid reference", func(t *testing.T) {
		_, err := Fetch(context.Background(), &stubGetter{}, "", "tenant1")
		fetchClassification(t, err, true, "SecretInvalid")
	})

	t.Run("classifies a NotFound client error as permanently missing", func(t *testing.T) {
		client := &stubGetter{err: status.Error(codes.NotFound, "no such secret")}
		_, err := Fetch(context.Background(), client, "key-id", "tenant1")
		fetchClassification(t, err, true, "SecretNotFound")
	})

	t.Run("keeps other client errors transient with the status intact", func(t *testing.T) {
		client := &stubGetter{err: status.Error(codes.Unavailable, "transport is down")}
		_, err := Fetch(context.Background(), client, "key-id", "tenant1")
		fetchClassification(t, err, false, "")
		var fetchErr *Error
		if !errors.As(err, &fetchErr) {
			t.Fatalf("expected a typed error, got: %v", err)
		}
		if status.Code(fetchErr.Err) != codes.Unavailable {
			t.Fatalf("expected Unavailable, got %s", status.Code(fetchErr.Err))
		}
	})

	t.Run("rejects a nil object", func(t *testing.T) {
		client := &stubGetter{}
		if _, err := Fetch(context.Background(), client, "key-id", "tenant1"); err == nil {
			t.Fatal("expected an error for a nil object")
		}
	})

	t.Run("classifies a deleted secret as permanently missing", func(t *testing.T) {
		secret := usableKeySecret()
		secret.GetMetadata().SetDeletionTimestamp(timestamppb.Now())
		client := &stubGetter{object: secret}
		_, err := Fetch(context.Background(), client, "key-id", "tenant1")
		fetchClassification(t, err, true, "SecretNotFound")
	})

	t.Run("classifies a cross-tenant secret as permanently missing", func(t *testing.T) {
		client := &stubGetter{object: usableKeySecret()}
		_, err := Fetch(context.Background(), client, "key-id", "tenant2")
		fetchClassification(t, err, true, "SecretNotFound")
		if !strings.Contains(err.Error(), "does not belong to tenant 'tenant2'") {
			t.Fatalf("expected a tenant mismatch message, got: %v", err)
		}
	})

	t.Run("classifies an unusable secret as permanently invalid", func(t *testing.T) {
		secret := sshKeySecret(privatev1.SecretType_SECRET_TYPE_USER_DATA,
			map[string][]byte{"userdata": []byte("#cloud-config")})
		client := &stubGetter{object: secret}
		_, err := Fetch(context.Background(), client, "key-id", "tenant1")
		fetchClassification(t, err, true, "SecretInvalid")
	})
}

/*
Copyright 2026.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package trustadmission

import (
	"context"
	"crypto/sha256"
	"crypto/x509"
	"encoding/hex"
	"errors"
	"maps"
	"sync"
	"time"

	corev1 "k8s.io/api/core/v1"
)

var (
	errInvalidRecord          = errors.New("invalid expected bundle record")
	errUnauthorizedConfigMap  = errors.New("configmap does not match an active expected bundle")
	errUnauthorizedDeployment = errors.New("deployment does not match one active expected bundle")
)

type Store interface {
	Publish(context.Context, ExpectedBundle) error
	Revoke(context.Context, RecordKey) error
	AuthorizeConfigMap(context.Context, *corev1.ConfigMap, time.Time) error
	AuthorizeDeployment(context.Context, string, string, time.Time) error
}

type MemoryStore struct {
	mu      sync.RWMutex
	records map[RecordKey]ExpectedBundle
}

func NewStore() *MemoryStore {
	return &MemoryStore{records: make(map[RecordKey]ExpectedBundle)}
}

func (s *MemoryStore) Publish(_ context.Context, record ExpectedBundle) error {
	if err := validateRecord(record); err != nil {
		return err
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	record.BundlePEM = append([]byte(nil), record.BundlePEM...)
	s.records[record.Key] = record
	return nil
}

func (s *MemoryStore) Revoke(_ context.Context, key RecordKey) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.records, key)
	return nil
}

func (s *MemoryStore) AuthorizeConfigMap(_ context.Context, candidate *corev1.ConfigMap, now time.Time) error {
	if candidate == nil || candidate.Name != ConfigMapName || len(candidate.Data) != 1 || len(candidate.BinaryData) != 0 || candidate.Data[BundleDataKey] == "" || candidate.Immutable != nil || len(candidate.OwnerReferences) != 0 || len(candidate.Finalizers) != 0 || candidate.GenerateName != "" {
		return errUnauthorizedConfigMap
	}

	bundle := []byte(candidate.Data[BundleDataKey])
	key := RecordKey{
		ClusterOrderUID: candidate.Annotations[OwnerReferenceAnnotation],
		TenantNamespace: candidate.Namespace,
		ConfigMapName:   candidate.Name,
		BundleSHA256:    bundleHash(bundle),
	}

	s.mu.RLock()
	defer s.mu.RUnlock()
	record, found := s.records[key]
	if !found || !record.ExpiresAt.After(now) || !matchesConfigMap(record, candidate, bundle) {
		return errUnauthorizedConfigMap
	}
	return nil
}

func (s *MemoryStore) AuthorizeDeployment(_ context.Context, namespace, bundleSHA256 string, now time.Time) error {
	s.mu.RLock()
	defer s.mu.RUnlock()

	matches := 0
	for _, record := range s.records {
		if record.Key.TenantNamespace == namespace && record.Key.ConfigMapName == ConfigMapName && record.Key.BundleSHA256 == bundleSHA256 && record.ExpiresAt.After(now) {
			matches++
		}
	}
	if matches != 1 {
		return errUnauthorizedDeployment
	}
	return nil
}

func validateRecord(record ExpectedBundle) error {
	if record.Key.ClusterOrderUID == "" || record.Key.TenantNamespace == "" || record.Key.ConfigMapName != ConfigMapName || record.Key.BundleSHA256 == "" || record.Tenant == "" || record.OwnerReference != record.Key.ClusterOrderUID || len(record.BundlePEM) == 0 || record.ExpiresAt.IsZero() {
		return errInvalidRecord
	}
	if !x509.NewCertPool().AppendCertsFromPEM(record.BundlePEM) || bundleHash(record.BundlePEM) != record.Key.BundleSHA256 {
		return errInvalidRecord
	}
	return nil
}

func matchesConfigMap(record ExpectedBundle, candidate *corev1.ConfigMap, bundle []byte) bool {
	return candidate.Namespace == record.Key.TenantNamespace &&
		candidate.Name == record.Key.ConfigMapName &&
		string(bundle) == string(record.BundlePEM) &&
		maps.Equal(candidate.Annotations, map[string]string{
			TenantAnnotation:         record.Tenant,
			OwnerReferenceAnnotation: record.OwnerReference,
			BundleHashAnnotation:     record.Key.BundleSHA256,
		})
}

func bundleHash(bundle []byte) string {
	digest := sha256.Sum256(bundle)
	return hex.EncodeToString(digest[:])
}

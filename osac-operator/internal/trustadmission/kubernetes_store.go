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
	"encoding/json"
	"errors"
	"fmt"
	"time"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	corev1client "k8s.io/client-go/kubernetes/typed/core/v1"
	"k8s.io/client-go/util/retry"
)

const (
	ExpectedBundleStoreName = "osac-fulfillment-trust-expected-bundles"
	expectedBundleStoreKey  = "records.json"
	storeRequestTimeout     = 5 * time.Second
)

var errExpectedBundleStoreNotFound = errors.New("expected bundle store was not found")

type KubernetesStore struct {
	secrets   corev1client.SecretsGetter
	namespace string
}

type storedExpectedBundle struct {
	Key            RecordKey `json:"key"`
	Tenant         string    `json:"tenant"`
	OwnerReference string    `json:"ownerReference"`
	BundlePEM      []byte    `json:"bundlePEM"`
	ExpiresAt      time.Time `json:"expiresAt"`
}

func NewKubernetesStore(secrets corev1client.SecretsGetter, namespace string) (*KubernetesStore, error) {
	if secrets == nil || namespace == "" {
		return nil, errInvalidHandlerConfig
	}

	return &KubernetesStore{secrets: secrets, namespace: namespace}, nil
}

// Ready confirms that the protected store exists and can be decoded.
func (s *KubernetesStore) Ready(ctx context.Context) error {
	ctx, cancel := context.WithTimeout(ctx, storeRequestTimeout)
	defer cancel()
	secret, err := s.secrets.Secrets(s.namespace).Get(ctx, ExpectedBundleStoreName, metav1.GetOptions{})
	if err != nil {
		return fmt.Errorf("get expected bundle store: %w", err)
	}
	if len(secret.Data[expectedBundleStoreKey]) == 0 {
		return fmt.Errorf("expected bundle store is uninitialized")
	}
	_, err = decodeRecords(secret.Data[expectedBundleStoreKey])
	return err
}

func (s *KubernetesStore) Publish(ctx context.Context, record ExpectedBundle) error {
	if err := validateRecord(record); err != nil {
		return err
	}

	return s.update(ctx, func(records []ExpectedBundle) ([]ExpectedBundle, error) {
		updated := make([]ExpectedBundle, 0, len(records)+1)
		for _, existing := range records {
			if existing.Key != record.Key {
				updated = append(updated, existing)
			}
		}
		return append(updated, record), nil
	})
}

func (s *KubernetesStore) Revoke(ctx context.Context, key RecordKey) error {
	return s.update(ctx, func(records []ExpectedBundle) ([]ExpectedBundle, error) {
		updated := make([]ExpectedBundle, 0, len(records))
		for _, record := range records {
			if record.Key != key {
				updated = append(updated, record)
			}
		}
		return updated, nil
	})
}

func (s *KubernetesStore) AuthorizeConfigMap(ctx context.Context, candidate *corev1.ConfigMap, now time.Time) error {
	records, err := s.records(ctx)
	if err != nil {
		return errUnauthorizedConfigMap
	}

	memoryStore := NewStore()
	for _, record := range records {
		if validateRecord(record) != nil {
			continue
		}
		if err := memoryStore.Publish(ctx, record); err != nil {
			return errUnauthorizedConfigMap
		}
	}
	return memoryStore.AuthorizeConfigMap(ctx, candidate, now)
}

func (s *KubernetesStore) AuthorizeDeployment(ctx context.Context, namespace, bundleSHA256 string, now time.Time) error {
	records, err := s.records(ctx)
	if err != nil {
		return errUnauthorizedDeployment
	}

	memoryStore := NewStore()
	for _, record := range records {
		if validateRecord(record) != nil {
			continue
		}
		if err := memoryStore.Publish(ctx, record); err != nil {
			return errUnauthorizedDeployment
		}
	}
	return memoryStore.AuthorizeDeployment(ctx, namespace, bundleSHA256, now)
}

func (s *KubernetesStore) records(ctx context.Context) ([]ExpectedBundle, error) {
	ctx, cancel := context.WithTimeout(ctx, storeRequestTimeout)
	defer cancel()
	secret, err := s.secrets.Secrets(s.namespace).Get(ctx, ExpectedBundleStoreName, metav1.GetOptions{})
	if apierrors.IsNotFound(err) {
		return nil, errExpectedBundleStoreNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("get expected bundle store: %w", err)
	}

	return decodeRecords(secret.Data[expectedBundleStoreKey])
}

func (s *KubernetesStore) update(ctx context.Context, mutate func([]ExpectedBundle) ([]ExpectedBundle, error)) error {
	ctx, cancel := context.WithTimeout(ctx, storeRequestTimeout)
	defer cancel()
	return retry.RetryOnConflict(retry.DefaultRetry, func() error {
		secret, err := s.secrets.Secrets(s.namespace).Get(ctx, ExpectedBundleStoreName, metav1.GetOptions{})
		if apierrors.IsNotFound(err) {
			return errExpectedBundleStoreNotFound
		}
		if err != nil {
			return fmt.Errorf("get expected bundle store: %w", err)
		}

		records, err := decodeRecords(secret.Data[expectedBundleStoreKey])
		if err != nil {
			return err
		}
		now := time.Now()
		active := make([]ExpectedBundle, 0, len(records))
		for _, record := range records {
			if record.ExpiresAt.After(now) && validateRecord(record) == nil {
				active = append(active, record)
			}
		}
		records, err = mutate(active)
		if err != nil {
			return err
		}

		encoded, err := encodeRecords(records)
		if err != nil {
			return err
		}
		if secret.Data == nil {
			secret.Data = make(map[string][]byte)
		}
		secret.Data[expectedBundleStoreKey] = encoded
		_, err = s.secrets.Secrets(s.namespace).Update(ctx, secret, metav1.UpdateOptions{})
		return err
	})
}

func decodeRecords(data []byte) ([]ExpectedBundle, error) {
	if len(data) == 0 {
		return nil, nil
	}

	var stored []storedExpectedBundle
	if err := json.Unmarshal(data, &stored); err != nil {
		return nil, fmt.Errorf("decode expected bundle store: %w", err)
	}

	records := make([]ExpectedBundle, 0, len(stored))
	for _, record := range stored {
		records = append(records, ExpectedBundle{
			Key:            record.Key,
			Tenant:         record.Tenant,
			OwnerReference: record.OwnerReference,
			BundlePEM:      append([]byte(nil), record.BundlePEM...),
			ExpiresAt:      record.ExpiresAt,
		})
	}
	return records, nil
}

func encodeRecords(records []ExpectedBundle) ([]byte, error) {
	stored := make([]storedExpectedBundle, 0, len(records))
	for _, record := range records {
		stored = append(stored, storedExpectedBundle{
			Key:            record.Key,
			Tenant:         record.Tenant,
			OwnerReference: record.OwnerReference,
			BundlePEM:      append([]byte(nil), record.BundlePEM...),
			ExpiresAt:      record.ExpiresAt,
		})
	}
	return json.Marshal(stored)
}

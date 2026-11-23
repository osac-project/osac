// Copyright (c) 2026 Red Hat Inc.
//
// Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except
// in compliance with the License. You may obtain a copy of the License at
//
//   http://www.apache.org/licenses/LICENSE-2.0

package writeproxy

import (
	"context"
	"errors"
	"fmt"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/watch"
	"k8s.io/client-go/kubernetes"
)

// RevocationSyncer keeps a RevocationStore current from the shared ConfigMap. Until the first
// successful read the store stays unsynced and every write is refused.
type RevocationSyncer struct {
	Client    kubernetes.Interface
	Namespace string
	Name      string
	Store     *RevocationStore
	// Retry is the delay after a failed read or watch.
	Retry time.Duration
}

// Run watches the revocation ConfigMap until the context is cancelled. It never returns a
// non-nil error: the store's own state is what protects the endpoint, so a transient API
// failure closes the gate and the loop re-lists.
func (s *RevocationSyncer) Run(ctx context.Context) error {
	retry := s.Retry
	if retry <= 0 {
		retry = time.Second
	}
	for {
		if ctx.Err() != nil {
			return nil
		}
		configMap, err := s.Client.CoreV1().ConfigMaps(s.Namespace).Get(ctx, s.Name, metav1.GetOptions{})
		if err != nil {
			s.Store.Fail()
			if !sleep(ctx, retry) {
				return nil
			}
			continue
		}
		s.Store.Apply(configMap.Data)
		watcher, err := s.Client.CoreV1().ConfigMaps(s.Namespace).Watch(ctx, metav1.ListOptions{
			FieldSelector:   "metadata.name=" + s.Name,
			ResourceVersion: configMap.ResourceVersion,
		})
		if err != nil {
			s.Store.Fail()
			if !sleep(ctx, retry) {
				return nil
			}
			continue
		}
		err = consumeConfigMapEvents(ctx, watcher, s.Store)
		watcher.Stop()
		if err == nil {
			return nil
		}
		if !sleep(ctx, retry) {
			return nil
		}
	}
}

// consumeConfigMapEvents applies watch events to the store. On any outcome other than context
// cancellation it closes the gate before returning, so a gap in the denylist rejects writes
// instead of admitting potentially revoked certificates.
func consumeConfigMapEvents(ctx context.Context, watcher watch.Interface, store *RevocationStore) error {
	for {
		select {
		case <-ctx.Done():
			return nil
		case event, ok := <-watcher.ResultChan():
			if !ok {
				store.Fail()
				return errors.New("revocation ConfigMap watch closed")
			}
			switch event.Type {
			case watch.Added, watch.Modified:
				configMap, ok := event.Object.(*corev1.ConfigMap)
				if !ok {
					store.Fail()
					return fmt.Errorf("unexpected object %T in revocation watch", event.Object)
				}
				store.Apply(configMap.Data)
			case watch.Deleted:
				store.Fail()
				return errors.New("revocation ConfigMap was deleted")
			case watch.Error:
				store.Fail()
				return fmt.Errorf("revocation ConfigMap watch error: %v", event.Object)
			}
		}
	}
}

func sleep(ctx context.Context, delay time.Duration) bool {
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-timer.C:
		return true
	}
}

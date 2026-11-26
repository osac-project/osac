// Copyright (c) 2026 Red Hat Inc.
//
// Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except
// in compliance with the License. You may obtain a copy of the License at
//
//   http://www.apache.org/licenses/LICENSE-2.0

package writeproxy

import (
	"context"
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/watch"
	"k8s.io/client-go/kubernetes/fake"
	k8stesting "k8s.io/client-go/testing"
)

func revision(name, data string) *corev1.ConfigMap {
	return &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: "osac-observability"},
		Data:       map[string]string{RevocationKey: data},
	}
}

func TestConsumeConfigMapEventsAppliesUpdates(t *testing.T) {
	store := NewRevocationStore()
	watcher := watch.NewFake()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- consumeConfigMapEvents(ctx, watcher, store) }()

	watcher.Add(revision(RevocationConfigMapName, "4d2"))
	waitFor(t, func() bool { return store.Synced() && store.Revoked("4d2") })
	if !store.Synced() {
		t.Fatal("an added ConfigMap must mark the store synced")
	}

	watcher.Modify(revision(RevocationConfigMapName, ""))
	waitFor(t, func() bool { return !store.Revoked("4d2") })

	cancel()
	if err := <-done; err != nil {
		t.Fatalf("consumeConfigMapEvents: %v", err)
	}
}

func TestConsumeConfigMapEventsFailsOnWatchError(t *testing.T) {
	store := NewRevocationStore()
	store.Apply(map[string]string{RevocationKey: ""})
	watcher := watch.NewFake()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- consumeConfigMapEvents(ctx, watcher, store) }()

	watcher.Error(&metav1.Status{Status: metav1.StatusFailure, Message: "watch is unreadable"})
	if err := <-done; err == nil {
		t.Fatal("expected a watch error")
	}
	if store.Synced() {
		t.Fatal("an unreadable watch must close the gate")
	}
}

func TestConsumeConfigMapEventsFailsOnClosedWatch(t *testing.T) {
	store := NewRevocationStore()
	store.Apply(map[string]string{RevocationKey: ""})
	watcher := watch.NewFake()
	done := make(chan error, 1)
	go func() { done <- consumeConfigMapEvents(context.Background(), watcher, store) }()

	watcher.Stop()
	if err := <-done; err == nil {
		t.Fatal("expected an error when the watch channel closes")
	}
	if store.Synced() {
		t.Fatal("a closed watch must close the gate until it is re-established")
	}
}

func TestConsumeConfigMapEventsFailsWhenTheConfigMapIsDeleted(t *testing.T) {
	store := NewRevocationStore()
	store.Apply(map[string]string{RevocationKey: ""})
	watcher := watch.NewFake()
	done := make(chan error, 1)
	go func() { done <- consumeConfigMapEvents(context.Background(), watcher, store) }()

	watcher.Delete(revision(RevocationConfigMapName, ""))
	if err := <-done; err == nil {
		t.Fatal("expected an error when the denylist disappears")
	}
	if store.Synced() {
		t.Fatal("a missing denylist must close the gate")
	}
}

func TestRevocationSyncerLoadsTheConfigMapBeforeWatching(t *testing.T) {
	client := fake.NewSimpleClientset(revision(RevocationConfigMapName, "4d2"))
	watcher := watch.NewFake()
	client.PrependWatchReactor("configmaps", func(k8stesting.Action) (bool, watch.Interface, error) {
		return true, watcher, nil
	})
	store := NewRevocationStore()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	syncer := &RevocationSyncer{
		Client:    client,
		Namespace: "osac-observability",
		Name:      RevocationConfigMapName,
		Store:     store,
		Retry:     time.Millisecond,
	}
	go func() { _ = syncer.Run(ctx) }()
	waitFor(t, func() bool { return store.Synced() && store.Revoked("4d2") })
}

func TestRevocationSyncerStaysClosedWithoutTheConfigMap(t *testing.T) {
	client := fake.NewSimpleClientset()
	store := NewRevocationStore()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	syncer := &RevocationSyncer{
		Client:    client,
		Namespace: "osac-observability",
		Name:      RevocationConfigMapName,
		Store:     store,
		Retry:     time.Millisecond,
	}
	go func() { _ = syncer.Run(ctx) }()
	time.Sleep(20 * time.Millisecond)
	if store.Synced() {
		t.Fatal("a missing ConfigMap must leave the gate closed")
	}
}

func waitFor(t *testing.T, condition func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if condition() {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatal("condition was not met before the deadline")
}

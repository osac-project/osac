// Copyright (c) 2026 Red Hat Inc.
//
// Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except
// in compliance with the License. You may obtain a copy of the License at
//
//   http://www.apache.org/licenses/LICENSE-2.0

package writeproxy

import "testing"

func TestRevocationStoreStartsUnsynced(t *testing.T) {
	store := NewRevocationStore()
	if store.Synced() {
		t.Fatal("a store that has not observed the ConfigMap must not be synced")
	}
	if !store.Revoked("any") {
		t.Fatal("an unsynced store must refuse every identity")
	}
}

func TestRevocationStoreApplyMarksSynced(t *testing.T) {
	store := NewRevocationStore()
	store.Apply(map[string]string{RevocationKey: "4d2\n"})
	if !store.Synced() {
		t.Fatal("a store that read the ConfigMap must be synced")
	}
	if !store.Revoked("4d2") {
		t.Error("4d2 must be revoked")
	}
	if store.Revoked("4d3") {
		t.Error("4d3 must not be revoked")
	}
}

func TestRevocationStoreApplyHandlesBlankLinesAndDuplicates(t *testing.T) {
	store := NewRevocationStore()
	store.Apply(map[string]string{RevocationKey: "\n4d2\n\n4d2\n 4d3 \n"})
	if !store.Revoked("4d2") || !store.Revoked("4d3") {
		t.Errorf("serials = %v", store.revokedSerials())
	}
	if got := len(store.revokedSerials()); got != 2 {
		t.Errorf("serials = %d, want 2", got)
	}
}

func TestRevocationStoreAcceptssEmptyConfigMap(t *testing.T) {
	store := NewRevocationStore()
	store.Apply(map[string]string{RevocationKey: ""})
	if !store.Synced() {
		t.Error("an empty but present ConfigMap is a valid synced state")
	}
	if store.Revoked("4d2") {
		t.Error("nothing is revoked yet")
	}
}

func TestRevocationStoreApplyReplacesContents(t *testing.T) {
	store := NewRevocationStore()
	store.Apply(map[string]string{RevocationKey: "4d2"})
	store.Apply(map[string]string{RevocationKey: ""})
	if store.Revoked("4d2") {
		t.Error("an applied ConfigMap replaces the previous list; 4d2 must be back")
	}
	if !store.Synced() {
		t.Error("the store stays synced")
	}
}

func TestRevocationStoreFailClosesTheGate(t *testing.T) {
	store := NewRevocationStore()
	store.Apply(map[string]string{RevocationKey: ""})
	store.Fail()
	if store.Synced() {
		t.Fatal("an unreadable watch must close the gate")
	}
	if !store.Revoked("anything") {
		t.Fatal("a closed gate refuses every identity")
	}
}

func TestRevocationStoreNormalizesSerialCase(t *testing.T) {
	store := NewRevocationStore()
	store.Apply(map[string]string{RevocationKey: "4D2"})
	if !store.Revoked("4d2") {
		t.Error("serial comparison must ignore case")
	}
}

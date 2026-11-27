package driver

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"testing"
)

func routeFileName(volumeID string) string {
	sum := sha256.Sum256([]byte(volumeID))
	return hex.EncodeToString(sum[:])
}

func TestFileRouteStoreRoundTrip(t *testing.T) {
	dir := t.TempDir()
	store := newFileRouteStore(dir)
	want := nodeRoute{Backend: "vast", Protocol: "nfs", VendorVolumeID: "pvc-n"}

	if err := store.Save("vol-1", want); err != nil {
		t.Fatalf("save: %v", err)
	}
	got, ok, err := store.Load("vol-1")
	if err != nil || !ok {
		t.Fatalf("load: ok=%v err=%v", ok, err)
	}
	want.Version = 1
	if got != want {
		t.Errorf("load = %+v, want %+v", got, want)
	}

	if err := store.Delete("vol-1"); err != nil {
		t.Fatalf("delete: %v", err)
	}
	if _, ok, err := store.Load("vol-1"); err != nil || ok {
		t.Errorf("load after delete: ok=%v err=%v, want not found", ok, err)
	}
	if err := store.Delete("vol-1"); err != nil {
		t.Errorf("deleting a missing route: %v", err)
	}
}

func TestFileRouteStoreHashesVolumeID(t *testing.T) {
	parent := t.TempDir()
	dir := filepath.Join(parent, "routes")
	if err := os.Mkdir(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	store := newFileRouteStore(dir)
	volumeID := "../escape/../../vol"

	if err := store.Save(volumeID, nodeRoute{Backend: "vast", Protocol: "nfs", VendorVolumeID: "x"}); err != nil {
		t.Fatalf("save: %v", err)
	}

	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 || entries[0].Name() != routeFileName(volumeID) {
		names := make([]string, 0, len(entries))
		for _, e := range entries {
			names = append(names, e.Name())
		}
		t.Fatalf("route files = %v, want only %s", names, routeFileName(volumeID))
	}
	parentEntries, err := os.ReadDir(parent)
	if err != nil {
		t.Fatal(err)
	}
	if len(parentEntries) != 1 {
		t.Errorf("files outside the store directory: %v", parentEntries)
	}
}

func TestFileRouteStoreFileMode(t *testing.T) {
	dir := t.TempDir()
	store := newFileRouteStore(dir)
	if err := store.Save("vol-1", nodeRoute{Backend: "vast", Protocol: "nfs", VendorVolumeID: "x"}); err != nil {
		t.Fatalf("save: %v", err)
	}
	info, err := os.Stat(filepath.Join(dir, routeFileName("vol-1")))
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	if mode := info.Mode().Perm(); mode != 0o600 {
		t.Errorf("route file mode = %o, want 600", mode)
	}
}

func TestFileRouteStoreRejectsBadFiles(t *testing.T) {
	for name, content := range map[string]string{
		"invalid JSON":    "{not json",
		"unknown version": `{"version":2,"backend":"vast","protocol":"nfs","vendorVolumeID":"x"}`,
	} {
		t.Run(name, func(t *testing.T) {
			dir := t.TempDir()
			path := filepath.Join(dir, routeFileName("vol-1"))
			if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
				t.Fatal(err)
			}
			if _, _, err := newFileRouteStore(dir).Load("vol-1"); err == nil {
				t.Error("expected an error loading a bad route file")
			}
		})
	}
}

func TestMemoryRouteStoreRoundTrip(t *testing.T) {
	store := newMemoryRouteStore()
	want := nodeRoute{Version: 1, Backend: "netapp", Protocol: "block", VendorVolumeID: "v"}
	if err := store.Save("vol-1", want); err != nil {
		t.Fatalf("save: %v", err)
	}
	got, ok, err := store.Load("vol-1")
	if err != nil || !ok || got != want {
		t.Fatalf("load = %+v ok=%v err=%v, want %+v", got, ok, err, want)
	}
	if err := store.Delete("vol-1"); err != nil {
		t.Fatalf("delete: %v", err)
	}
	if _, ok, _ := store.Load("vol-1"); ok {
		t.Error("route still present after delete")
	}
}

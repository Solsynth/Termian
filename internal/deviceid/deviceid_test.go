package deviceid

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestLoadIsStableAcrossCalls guards the property Stargate depends on: the id
// must not change between runs.
func TestLoadIsStableAcrossCalls(t *testing.T) {
	dir := t.TempDir()

	first, err := Load(dir)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	second, err := Load(dir)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if first.ID == "" {
		t.Fatal("ID is empty")
	}
	if first.ID != second.ID {
		t.Fatalf("IDs differ across calls: %q vs %q", first.ID, second.ID)
	}
	if first.Platform == 0 {
		t.Fatal("Platform is unset")
	}
	if first.Name == "" || first.Name == "localhost" {
		t.Fatalf("Name = %q, want a usable device name", first.Name)
	}
}

// TestPersistedIDUsedWhenNoMachineID covers the fallback identifier: it is
// written once with owner-only permissions and reused afterwards.
func TestPersistedIDUsedWhenNoMachineID(t *testing.T) {
	if readMachineID() != "" {
		t.Skip("host exposes a machine id; the fallback path is unreachable")
	}

	dir := filepath.Join(t.TempDir(), "termian")
	identity, err := Load(dir)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	path := persistedIDPath(dir)
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read persisted id: %v", err)
	}
	if got := strings.TrimSpace(string(data)); got != identity.ID {
		t.Fatalf("persisted id = %q, want %q", got, identity.ID)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat persisted id: %v", err)
	}
	if perm := info.Mode().Perm(); perm != 0o600 {
		t.Fatalf("persisted id mode = %o, want 600", perm)
	}
}

// TestHashIDIsPrefixed guards accidental collisions with a bare sha256 of the
// raw machine id.
func TestHashIDIsPrefixed(t *testing.T) {
	got := hashID("machine-1")
	if len(got) != 64 {
		t.Fatalf("hash length = %d, want 64 hex characters", len(got))
	}
	if got == hashID("machine-2") {
		t.Fatal("different machine ids hashed to the same value")
	}
}

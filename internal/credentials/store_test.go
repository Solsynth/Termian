package credentials

import (
	"errors"
	"os"
	"strings"
	"testing"

	"github.com/zalando/go-keyring"
)

// sampleRecord builds a credentials record for server.
func sampleRecord(server string) *Credentials {
	return &Credentials{
		Server:       server,
		Method:       MethodDevice,
		ClientID:     "solian",
		DeviceID:     "device-1",
		RefreshToken: "rt-secret-value",
	}
}

// TestOpenAutoFallsBackToFileWithRestrictedPerms covers the documented
// downgrade: without a usable keyring the refresh token lands in a 0600 file
// inside a 0700 directory.
func TestOpenAutoFallsBackToFileWithRestrictedPerms(t *testing.T) {
	keyring.MockInitWithError(errors.New("no secret service"))

	dir := t.TempDir() + "/termian"
	store, err := Open(ModeAuto, dir)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	if store.Backend != BackendFile {
		t.Fatalf("Backend = %q, want %q", store.Backend, BackendFile)
	}
	if notice := store.Notice("https://api.solian.app/stargate"); !strings.Contains(notice, "0600 file") {
		t.Fatalf("Notice = %q, want a 0600 file warning", notice)
	}

	creds := sampleRecord("https://api.solian.app/stargate")
	if err := store.Save(creds); err != nil {
		t.Fatalf("Save: %v", err)
	}

	fileInfo, err := os.Stat(store.Describe(creds.Server))
	if err != nil {
		t.Fatalf("stat credentials file: %v", err)
	}
	if perm := fileInfo.Mode().Perm(); perm != 0o600 {
		t.Fatalf("credentials file mode = %o, want 600", perm)
	}
	dirInfo, err := os.Stat(store.Path)
	if err != nil {
		t.Fatalf("stat state dir: %v", err)
	}
	if perm := dirInfo.Mode().Perm(); perm != 0o700 {
		t.Fatalf("state dir mode = %o, want 700", perm)
	}

	loaded, err := store.Load(creds.Server)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if loaded == nil || loaded.RefreshToken != creds.RefreshToken || loaded.Method != MethodDevice {
		t.Fatalf("Load = %+v, want the saved record", loaded)
	}

	if err := store.Delete(creds.Server); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	gone, err := store.Load(creds.Server)
	if err != nil {
		t.Fatalf("Load after Delete: %v", err)
	}
	if gone != nil {
		t.Fatalf("Load after Delete = %+v, want nil", gone)
	}
}

// TestKeyringRoundTrip covers the healthy path, including the hashed item name
// (keyring user names may not contain URL punctuation).
func TestKeyringRoundTrip(t *testing.T) {
	keyring.MockInit()

	store, err := Open(ModeKeyring, "")
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	if store.Backend != BackendKeyring {
		t.Fatalf("Backend = %q, want %q", store.Backend, BackendKeyring)
	}

	creds := sampleRecord("https://api.solian.app/stargate")
	if err := store.Save(creds); err != nil {
		t.Fatalf("Save: %v", err)
	}
	loaded, err := store.Load(creds.Server)
	if err != nil || loaded == nil {
		t.Fatalf("Load = %+v, %v", loaded, err)
	}
	if loaded.RefreshToken != creds.RefreshToken {
		t.Fatalf("RefreshToken = %q, want %q", loaded.RefreshToken, creds.RefreshToken)
	}
	if hashed := key(creds.Server); strings.ContainsAny(hashed, ":/") {
		t.Fatalf("keyring key %q contains URL punctuation", hashed)
	}

	if err := store.Delete(creds.Server); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if loaded, err := store.Load(creds.Server); err != nil || loaded != nil {
		t.Fatalf("Load after Delete = %+v, %v; want nil, nil", loaded, err)
	}
}

// TestFileStoreSeparatesServers guards the credential key: two servers must not
// overwrite each other.
func TestFileStoreSeparatesServers(t *testing.T) {
	dir := t.TempDir()
	store, err := Open(ModeFile, dir)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}

	first := sampleRecord("https://api.solian.app/stargate")
	second := sampleRecord("https://staging.solian.app/stargate")
	second.RefreshToken = "rt-other"
	for _, creds := range []*Credentials{first, second} {
		if err := store.Save(creds); err != nil {
			t.Fatalf("Save %s: %v", creds.Server, err)
		}
	}

	for _, want := range []*Credentials{first, second} {
		got, err := store.Load(want.Server)
		if err != nil {
			t.Fatalf("Load %s: %v", want.Server, err)
		}
		if got == nil || got.RefreshToken != want.RefreshToken {
			t.Fatalf("Load %s = %+v, want %q", want.Server, got, want.RefreshToken)
		}
	}
}

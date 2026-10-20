// Package credentials persists the rotating refresh token for one Solian
// server, preferring the system keyring and falling back to a 0600 file.
//
// Access tokens never reach disk: they live in the API token source for the
// lifetime of a process.
package credentials

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/adrg/xdg"
	"github.com/zalando/go-keyring"
)

// Method identifies which login flow produced the credentials.
const (
	MethodDevice    = "device"
	MethodChallenge = "challenge"
)

// Backend names the storage medium backing a Store.
type Backend string

const (
	BackendKeyring Backend = "keyring"
	BackendFile    Backend = "file"
)

// Store modes accepted by Open.
const (
	ModeAuto    = "auto"
	ModeKeyring = "keyring"
	ModeFile    = "file"
)

// keyringService groups Termian secrets in the OS keyring.
const keyringService = "termian"

// Credentials is the persisted login record for one server.
type Credentials struct {
	// Server is the normalized API base, e.g. https://api.solian.app/stargate.
	Server string `json:"server"`
	// Method is "device" or "challenge".
	Method string `json:"method"`
	// ClientID is the OAuth client used by the device flow; empty otherwise.
	ClientID string `json:"client_id,omitempty"`
	DeviceID string `json:"device_id"`
	// AccountID, AccountName and Nick are a cached account snapshot so the
	// TUI can greet the user before the first API round trip completes.
	AccountID   string `json:"account_id"`
	AccountName string `json:"account_name"`
	Nick        string `json:"nick"`

	RefreshToken string `json:"refresh_token"`
	// RefreshExpiresAt is zero for the device flow: the OIDC token response
	// carries no refresh lifetime.
	RefreshExpiresAt time.Time `json:"refresh_expires_at"`
	ObtainedAt       time.Time `json:"obtained_at"`
}

// Store reads and writes credential records.
type Store struct {
	// Backend is the medium actually in use.
	Backend Backend
	// Path is the credential file path in file mode, or the keyring service
	// name in keyring mode.
	Path string
	// noticeErr records why the keyring was skipped in auto mode; Notice turns
	// it into a user-facing line.
	noticeErr error
	stateDir  string
}

// Open resolves a store for the requested mode.
//
// "auto" prefers the keyring and silently downgrades to the file backend when
// the keyring is unavailable (no Secret Service, locked session, ...), in which
// case Notice explains where the refresh token ends up.
func Open(mode, stateDir string) (*Store, error) {
	switch mode {
	case "", ModeAuto:
		return openAuto(stateDir)
	case ModeKeyring:
		if err := keyringProbe(); err != nil {
			return nil, fmt.Errorf("system keyring unavailable and --store keyring forbids the file fallback: %w", err)
		}
		return &Store{Backend: BackendKeyring, Path: keyringService}, nil
	case ModeFile:
		return openFile(stateDir)
	default:
		return nil, fmt.Errorf("credentials: unknown store mode %q (want auto, keyring or file)", mode)
	}
}

// openAuto probes the keyring and falls back to the file store.
func openAuto(stateDir string) (*Store, error) {
	err := keyringProbe()
	if err == nil {
		return &Store{Backend: BackendKeyring, Path: keyringService}, nil
	}
	store, ferr := openFile(stateDir)
	if ferr != nil {
		return nil, ferr
	}
	store.noticeErr = err
	return store, nil
}

// Notice explains a storage downgrade for humans, naming the fallback file.
// It is empty when the keyring is in use.
func (s *Store) Notice(server string) string {
	if s.noticeErr == nil || s.Backend != BackendFile {
		return ""
	}
	return fmt.Sprintf("system keyring unavailable (%v) — refresh token stored in a 0600 file at %s",
		s.noticeErr, s.filePath(server))
}

// openFile builds a file-backed store rooted in stateDir.
func openFile(stateDir string) (*Store, error) {
	if stateDir == "" {
		var err error
		stateDir, err = defaultStateDir()
		if err != nil {
			return nil, err
		}
	}
	if err := os.MkdirAll(stateDir, 0o700); err != nil {
		return nil, fmt.Errorf("create state directory: %w", err)
	}
	return &Store{Backend: BackendFile, Path: stateDir, stateDir: stateDir}, nil
}

// defaultStateDir returns $XDG_STATE_HOME/termian (or the portable fallback).
func defaultStateDir() (string, error) {
	path, err := xdg.StateFile("termian")
	if err != nil {
		return "", fmt.Errorf("resolve state directory: %w", err)
	}
	return path, nil
}

// key returns the keyring item name for a server: URL punctuation is not allowed
// in keyring user names, so the base URL is hashed.
func key(server string) string {
	sum := sha256.Sum256([]byte(server))
	return hex.EncodeToString(sum[:])
}

// filePath returns the credential file for a server.
func (s *Store) filePath(server string) string {
	return filepath.Join(s.stateDir, "credentials-"+key(server)[:16]+".json")
}

// keyringProbe verifies that the keyring is usable without touching secrets.
func keyringProbe() error {
	_, err := keyring.Get(keyringService, "probe")
	if err == nil || errors.Is(err, keyring.ErrNotFound) {
		return nil
	}
	return err
}

// Load returns the stored credentials for server, or nil when none exist.
func (s *Store) Load(server string) (*Credentials, error) {
	var blob []byte
	var err error
	switch s.Backend {
	case BackendKeyring:
		var secret string
		secret, err = keyring.Get(keyringService, key(server))
		if errors.Is(err, keyring.ErrNotFound) {
			return nil, nil
		}
		if err != nil {
			return nil, fmt.Errorf("read keyring: %w", err)
		}
		blob = []byte(secret)
	case BackendFile:
		blob, err = os.ReadFile(s.filePath(server))
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		if err != nil {
			return nil, fmt.Errorf("read credentials file: %w", err)
		}
	default:
		return nil, fmt.Errorf("credentials: store has no backend")
	}

	var creds Credentials
	if err := json.Unmarshal(blob, &creds); err != nil {
		return nil, fmt.Errorf("decode stored credentials: %w", err)
	}
	if creds.Server == "" {
		creds.Server = server
	}
	return &creds, nil
}

// Save stores the credentials, replacing any previous record for the server.
func (s *Store) Save(c *Credentials) error {
	if c == nil || c.Server == "" {
		return errors.New("credentials: cannot save credentials without a server")
	}
	blob, err := json.Marshal(c)
	if err != nil {
		return fmt.Errorf("encode credentials: %w", err)
	}
	switch s.Backend {
	case BackendKeyring:
		if err := keyring.Set(keyringService, key(c.Server), string(blob)); err != nil {
			return fmt.Errorf("write keyring: %w", err)
		}
		return nil
	case BackendFile:
		return s.writeFile(s.filePath(c.Server), blob)
	default:
		return fmt.Errorf("credentials: store has no backend")
	}
}

// Delete removes the record for server; a missing record is not an error.
func (s *Store) Delete(server string) error {
	switch s.Backend {
	case BackendKeyring:
		err := keyring.Delete(keyringService, key(server))
		if err == nil || errors.Is(err, keyring.ErrNotFound) {
			return nil
		}
		return fmt.Errorf("delete keyring item: %w", err)
	case BackendFile:
		err := os.Remove(s.filePath(server))
		if err == nil || errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return fmt.Errorf("delete credentials file: %w", err)
	default:
		return fmt.Errorf("credentials: store has no backend")
	}
}

// WriteFile atomically replaces path with data using owner-only permissions.
func (s *Store) writeFile(path string, data []byte) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("create credential directory: %w", err)
	}
	if err := os.Chmod(dir, 0o700); err != nil {
		return fmt.Errorf("restrict credential directory: %w", err)
	}

	tmp, err := os.CreateTemp(dir, ".credentials-*.tmp")
	if err != nil {
		return fmt.Errorf("create temp credentials file: %w", err)
	}
	tmpName := tmp.Name()
	cleanup := func() { _ = os.Remove(tmpName) }

	if err := tmp.Chmod(0o600); err != nil {
		_ = tmp.Close()
		cleanup()
		return fmt.Errorf("restrict temp credentials file: %w", err)
	}
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		cleanup()
		return fmt.Errorf("write temp credentials file: %w", err)
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		cleanup()
		return fmt.Errorf("flush temp credentials file: %w", err)
	}
	if err := tmp.Close(); err != nil {
		cleanup()
		return fmt.Errorf("close temp credentials file: %w", err)
	}
	if err := os.Rename(tmpName, path); err != nil {
		cleanup()
		return fmt.Errorf("commit credentials file: %w", err)
	}
	return nil
}

// Describe renders the storage location for humans, never the secrets.
func (s *Store) Describe(server string) string {
	if s.Backend == BackendFile {
		return s.filePath(server)
	}
	return "system keyring (" + keyringService + ")"
}

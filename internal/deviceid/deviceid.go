// Package deviceid derives a stable device identity for Stargate.
//
// Stargate keys challenge reuse and risky-IP accounting on
// (account, ip, user-agent, device_id), so the identifier must survive
// restarts: a machine id is hashed when the OS exposes one, otherwise a random
// identifier is generated once and persisted under the state directory.
package deviceid

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
)

// Platform codes understood by Stargate (0 = unknown).
const (
	PlatformWeb     = 1
	PlatformIOS     = 2
	PlatformAndroid = 3
	PlatformMacOS   = 4
	PlatformWindows = 5
	PlatformLinux   = 6
)

// Identity is the device description sent with device-flow and challenge
// requests.
type Identity struct {
	ID       string
	Name     string
	Platform int
}

// machineIDFiles are the well-known machine id locations, most specific first.
var machineIDFiles = []string{"/etc/machine-id", "/var/lib/dbus/machine-id"}

// Load resolves the device identity, persisting a generated identifier in
// stateDir when the OS exposes no machine id.
func Load(stateDir string) (Identity, error) {
	id := Identity{
		Name:     hostname(),
		Platform: platform(),
	}

	if raw := readMachineID(); raw != "" {
		id.ID = hashID(raw)
		return id, nil
	}

	persisted, err := loadPersistedID(stateDir)
	if err != nil {
		return Identity{}, err
	}
	if persisted == "" {
		persisted, err = newRandomID()
		if err != nil {
			return Identity{}, err
		}
		if err := savePersistedID(stateDir, persisted); err != nil {
			return Identity{}, err
		}
	}
	id.ID = persisted
	return id, nil
}

// platform maps the host OS onto a Stargate platform code.
func platform() int {
	switch runtime.GOOS {
	case "linux":
		return PlatformLinux
	case "darwin":
		return PlatformMacOS
	case "windows":
		return PlatformWindows
	default:
		return PlatformWeb
	}
}

// hostname returns a non-empty, human-meaningful device name.
func hostname() string {
	name, err := os.Hostname()
	if err != nil {
		return "termian"
	}
	name = strings.TrimSpace(name)
	if name == "" || strings.EqualFold(name, "localhost") {
		return "termian"
	}
	return name
}

// readMachineID returns the raw machine identifier, or "" when unavailable.
// Failures are not fatal: callers fall back to a persisted random identifier.
func readMachineID() string {
	for _, path := range machineIDFiles {
		data, err := os.ReadFile(path)
		if err != nil {
			continue
		}
		if raw := strings.TrimSpace(string(data)); raw != "" {
			return raw
		}
	}
	return ""
}

// hashID derives the opaque, stable device id from a raw machine identifier.
func hashID(raw string) string {
	sum := sha256.Sum256([]byte("termian:" + raw))
	return hex.EncodeToString(sum[:])
}

// newRandomID generates a random device id.
func newRandomID() (string, error) {
	buf := make([]byte, 16)
	if _, err := rand.Read(buf); err != nil {
		return "", fmt.Errorf("generate device id: %w", err)
	}
	return hex.EncodeToString(buf), nil
}

// persistedIDPath is the fallback identifier location.
func persistedIDPath(stateDir string) string {
	return filepath.Join(stateDir, "device-id")
}

// loadPersistedID reads the stored fallback identifier, "" when absent.
func loadPersistedID(stateDir string) (string, error) {
	if stateDir == "" {
		return "", nil
	}
	data, err := os.ReadFile(persistedIDPath(stateDir))
	if errors.Is(err, os.ErrNotExist) {
		return "", nil
	}
	if err != nil {
		return "", fmt.Errorf("read persisted device id: %w", err)
	}
	return strings.TrimSpace(string(data)), nil
}

// savePersistedID writes the fallback identifier with owner-only permissions.
func savePersistedID(stateDir, id string) error {
	if stateDir == "" {
		return errors.New("deviceid: no state directory for persisted device id")
	}
	if err := os.MkdirAll(stateDir, 0o700); err != nil {
		return fmt.Errorf("create state directory: %w", err)
	}
	path := persistedIDPath(stateDir)
	if err := os.WriteFile(path, []byte(id+"\n"), 0o600); err != nil {
		return fmt.Errorf("write persisted device id: %w", err)
	}
	return nil
}

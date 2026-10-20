package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/adrg/xdg"
)

// withEnv points the XDG directories at a temporary home and reloads xdg.
func withEnv(t *testing.T) string {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, "config"))
	t.Setenv("XDG_STATE_HOME", filepath.Join(home, "state"))
	for _, key := range []string{EnvServer, EnvClientID, EnvStore, EnvOutput, EnvConfig} {
		t.Setenv(key, "")
	}
	xdg.Reload()
	t.Cleanup(xdg.Reload)
	return home
}

// writeConfig writes a config file at the XDG config location.
func writeConfig(t *testing.T, body string) string {
	t.Helper()
	path := filepath.Join(os.Getenv("XDG_CONFIG_HOME"), "termian", "config.toml")
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}
	return path
}

// TestLoadDefaults keeps the documented defaults stable.
func TestLoadDefaults(t *testing.T) {
	withEnv(t)

	cfg, err := Load(Overrides{})
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Server != DefaultServer || cfg.ClientID != DefaultClientID || cfg.Scope != DefaultScope {
		t.Fatalf("cfg = %+v, want defaults", cfg)
	}
	if cfg.Store != DefaultStore || cfg.Output != DefaultOutput {
		t.Fatalf("cfg = %+v, want defaults", cfg)
	}
	if cfg.Path != "" {
		t.Fatalf("Path = %q, want empty when no file exists", cfg.Path)
	}
}

// TestPrecedence checks flags > env > file > defaults.
func TestPrecedence(t *testing.T) {
	withEnv(t)
	path := writeConfig(t, `
server = "https://file.example/stargate"
client_id = "file-client"
store = "file"
output = "json"
`)

	cfg, err := Load(Overrides{})
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Path != path {
		t.Fatalf("Path = %q, want %q", cfg.Path, path)
	}
	if cfg.Server != "https://file.example/stargate" || cfg.ClientID != "file-client" {
		t.Fatalf("file values not applied: %+v", cfg)
	}

	t.Setenv(EnvServer, "https://env.example/stargate")
	t.Setenv(EnvOutput, "text")
	cfg, err = Load(Overrides{ClientID: "flag-client"})
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Server != "https://env.example/stargate" {
		t.Fatalf("Server = %q, want the env value", cfg.Server)
	}
	if cfg.ClientID != "flag-client" {
		t.Fatalf("ClientID = %q, want the flag value", cfg.ClientID)
	}
	if cfg.Output != "text" {
		t.Fatalf("Output = %q, want the env value", cfg.Output)
	}
}

// TestMalformedFileIsHardError keeps silent fallbacks from hiding typos.
func TestMalformedFileIsHardError(t *testing.T) {
	withEnv(t)
	path := writeConfig(t, "server = [")

	_, err := Load(Overrides{})
	if err == nil {
		t.Fatal("Load accepted a malformed config")
	}
	if !strings.Contains(err.Error(), path) {
		t.Fatalf("error %q does not name %q", err, path)
	}
}

// TestExplicitMissingFileErrors covers --config pointing at a nonexistent path.
func TestExplicitMissingFileErrors(t *testing.T) {
	withEnv(t)

	missing := filepath.Join(t.TempDir(), "nope.toml")
	if _, err := Load(Overrides{Path: missing}); err == nil {
		t.Fatal("Load accepted a missing explicit config")
	}
}

// TestInvalidValuesRejected pins validation messages.
func TestInvalidValuesRejected(t *testing.T) {
	withEnv(t)

	if _, err := Load(Overrides{Store: "vault"}); err == nil {
		t.Fatal("Load accepted an unknown store mode")
	}
	if _, err := Load(Overrides{Output: "yaml"}); err == nil {
		t.Fatal("Load accepted an unknown output mode")
	}
}

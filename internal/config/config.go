// Package config resolves Termian settings from the configuration file,
// environment variables, flags and built-in defaults, in that order of
// precedence (flags win).
package config

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"

	"github.com/adrg/xdg"
	"github.com/pelletier/go-toml/v2"
)

// Defaults applied when nothing else supplies a value.
const (
	DefaultServer   = "https://api.solian.app"
	DefaultClientID = "solian"
	DefaultScope    = "*"
	DefaultStore    = "auto"
	DefaultOutput   = "text"
	// DefaultAttachments decides what the TUI does with post attachments:
	// hand them to the browser, render an inline preview, or show only names.
	DefaultAttachments = "browser"
	// DefaultChatHistory is how many messages a chat pane keeps per room.
	DefaultChatHistory = 200
)

// Env names read by Load.
const (
	EnvServer      = "TERMIAN_SERVER"
	EnvClientID    = "TERMIAN_CLIENT_ID"
	EnvStore       = "TERMIAN_STORE"
	EnvOutput      = "TERMIAN_OUTPUT"
	EnvAttachments = "TERMIAN_ATTACHMENTS"
	EnvChatHistory = "TERMIAN_CHAT_HISTORY"
	EnvConfig      = "TERMIAN_CONFIG"
)

// Config is the fully resolved runtime configuration.
type Config struct {
	Server   string
	ClientID string
	Scope    string
	Store    string
	Output   string
	// Attachments is browser, preview or off.
	Attachments string
	// ChatHistory is the number of messages kept per chat room.
	ChatHistory int
	// Path is the configuration file actually consulted; empty when no file
	// was read and defaults were used.
	Path string
	// StateDir holds mutable state: the credentials file and the fallback
	// device id.
	StateDir string
}

// Overrides carries flag values; empty strings mean "not set".
type Overrides struct {
	Server      string
	ClientID    string
	Store       string
	Output      string
	Attachments string
	ChatHistory int
	// Path overrides the configuration file location (--config).
	Path string
}

// file mirrors the on-disk TOML schema; every key is optional.
type file struct {
	Server      *string `toml:"server"`
	ClientID    *string `toml:"client_id"`
	Scope       *string `toml:"scope"`
	Store       *string `toml:"store"`
	Output      *string `toml:"output"`
	Attachments *string `toml:"attachments"`
	ChatHistory *int    `toml:"chat_history"`
}

// Load resolves the configuration.
//
// A missing configuration file is not an error; a malformed one is, and the
// error names the offending path.
func Load(over Overrides) (Config, error) {
	cfg := Config{
		Server:      DefaultServer,
		ClientID:    DefaultClientID,
		Scope:       DefaultScope,
		Store:       DefaultStore,
		Output:      DefaultOutput,
		Attachments: DefaultAttachments,
		ChatHistory: DefaultChatHistory,
	}

	path, explicit, err := resolvePath(over.Path)
	if err != nil {
		return Config{}, err
	}
	loaded, err := applyFile(path, explicit, &cfg)
	if err != nil {
		return Config{}, err
	}
	if loaded {
		cfg.Path = path
	}

	// Environment beats the file.
	applyEnv(&cfg)
	// Flags beat the environment.
	applyOverrides(&cfg, over)

	if err := cfg.validate(); err != nil {
		return Config{}, err
	}

	stateDir, err := xdg.StateFile("termian")
	if err != nil {
		return Config{}, fmt.Errorf("resolve state directory: %w", err)
	}
	cfg.StateDir = stateDir
	return cfg, nil
}

// resolvePath picks the configuration file location and reports whether the
// user pinned it explicitly (which makes a missing file an error).
func resolvePath(flagPath string) (string, bool, error) {
	if flagPath != "" {
		return flagPath, true, nil
	}
	if envPath := os.Getenv(EnvConfig); envPath != "" {
		return envPath, true, nil
	}
	path, err := xdg.ConfigFile(filepath.Join("termian", "config.toml"))
	if err != nil {
		return "", false, fmt.Errorf("resolve config path: %w", err)
	}
	return path, false, nil
}

// applyFile reads path and applies set keys onto cfg. The second result reports
// whether a file was read.
func applyFile(path string, explicit bool, cfg *Config) (bool, error) {
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		if explicit {
			return false, fmt.Errorf("config file %s does not exist", path)
		}
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("read config file %s: %w", path, err)
	}

	var f file
	if err := toml.Unmarshal(data, &f); err != nil {
		return false, fmt.Errorf("parse config file %s: %w", path, err)
	}
	setIfPresent(&cfg.Server, f.Server)
	setIfPresent(&cfg.ClientID, f.ClientID)
	setIfPresent(&cfg.Scope, f.Scope)
	setIfPresent(&cfg.Store, f.Store)
	setIfPresent(&cfg.Output, f.Output)
	setIfPresent(&cfg.Attachments, f.Attachments)
	if f.ChatHistory != nil {
		cfg.ChatHistory = *f.ChatHistory
	}
	return true, nil
}

// applyEnv overlays environment variables.
func applyEnv(cfg *Config) {
	setIfPresent(&cfg.Server, nonEmpty(os.Getenv(EnvServer)))
	setIfPresent(&cfg.ClientID, nonEmpty(os.Getenv(EnvClientID)))
	setIfPresent(&cfg.Store, nonEmpty(os.Getenv(EnvStore)))
	setIfPresent(&cfg.Output, nonEmpty(os.Getenv(EnvOutput)))
	setIfPresent(&cfg.Attachments, nonEmpty(os.Getenv(EnvAttachments)))
	if raw := os.Getenv(EnvChatHistory); raw != "" {
		if n, err := strconv.Atoi(raw); err == nil {
			cfg.ChatHistory = n
		}
	}
}

// applyOverrides overlays flag values.
func applyOverrides(cfg *Config, over Overrides) {
	setIfPresent(&cfg.Server, nonEmpty(over.Server))
	setIfPresent(&cfg.ClientID, nonEmpty(over.ClientID))
	setIfPresent(&cfg.Store, nonEmpty(over.Store))
	setIfPresent(&cfg.Output, nonEmpty(over.Output))
	setIfPresent(&cfg.Attachments, nonEmpty(over.Attachments))
	if over.ChatHistory > 0 {
		cfg.ChatHistory = over.ChatHistory
	}
}

// validate rejects unusable values early, with actionable messages.
func (c *Config) validate() error {
	switch c.Store {
	case "auto", "keyring", "file":
	default:
		return fmt.Errorf("invalid store mode %q (want auto, keyring or file)", c.Store)
	}
	switch c.Output {
	case "text", "json":
	default:
		return fmt.Errorf("invalid output mode %q (want text or json)", c.Output)
	}
	switch c.Attachments {
	case "browser", "preview", "off":
	default:
		return fmt.Errorf("invalid attachments mode %q (want browser, preview or off)", c.Attachments)
	}
	if c.ChatHistory <= 0 {
		return fmt.Errorf("chat_history must be positive, got %d", c.ChatHistory)
	}
	if c.Server == "" {
		return errors.New("server must not be empty")
	}
	if c.Scope == "" {
		c.Scope = DefaultScope
	}
	if c.ClientID == "" {
		c.ClientID = DefaultClientID
	}
	return nil
}

// setIfPresent overwrites dst when src is set.
func setIfPresent(dst *string, src *string) {
	if src != nil && *src != "" {
		*dst = *src
	}
}

// nonEmpty turns an empty string into a nil pointer.
func nonEmpty(v string) *string {
	if v == "" {
		return nil
	}
	return &v
}

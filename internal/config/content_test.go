package config

import "testing"

// TestAttachmentSettings covers the two new keys: file, env and flag.
func TestAttachmentSettings(t *testing.T) {
	withEnv(t)

	writeConfig(t, `
attachments  = "preview"
chat_history = 50
`)
	cfg, err := Load(Overrides{})
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Attachments != "preview" || cfg.ChatHistory != 50 {
		t.Fatalf("cfg = %+v", cfg)
	}

	t.Setenv(EnvAttachments, "off")
	t.Setenv(EnvChatHistory, "300")
	cfg, err = Load(Overrides{})
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Attachments != "off" || cfg.ChatHistory != 300 {
		t.Fatalf("env did not win: %+v", cfg)
	}

	cfg, err = Load(Overrides{Attachments: "browser", ChatHistory: 120})
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Attachments != "browser" || cfg.ChatHistory != 120 {
		t.Fatalf("flag did not win: %+v", cfg)
	}
}

// TestAttachmentDefaults keeps the documented defaults stable.
func TestAttachmentDefaults(t *testing.T) {
	withEnv(t)

	cfg, err := Load(Overrides{})
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Attachments != DefaultAttachments {
		t.Fatalf("attachments = %q", cfg.Attachments)
	}
	if cfg.ChatHistory != DefaultChatHistory {
		t.Fatalf("chat history = %d", cfg.ChatHistory)
	}
}

// TestAttachmentValidation rejects unknown policies and empty histories.
func TestAttachmentValidation(t *testing.T) {
	withEnv(t)

	if _, err := Load(Overrides{Attachments: "kitty"}); err == nil {
		t.Fatal("an unknown attachment mode was accepted")
	}
	// A negative flag is "not set" by convention; a negative file value is
	// rejected.
	writeConfig(t, "chat_history = -1\n")
	if _, err := Load(Overrides{}); err == nil {
		t.Fatal("a negative history limit was accepted")
	}
}

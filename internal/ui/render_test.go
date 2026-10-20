package ui

import (
	"bytes"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"testing"

	"src.solsynth.dev/sosys/termian/internal/api"
)

// TestEmitWritesOneCompactLine guards the NDJSON contract scripts rely on.
func TestEmitWritesOneCompactLine(t *testing.T) {
	out := &bytes.Buffer{}
	r := New(out, &bytes.Buffer{}, "json")

	payload := map[string]any{
		"type":      RecordAuthorization,
		"user_code": "LHJC-ZBPP",
		"note":      "<script>",
	}
	if err := r.Emit(payload); err != nil {
		t.Fatalf("Emit: %v", err)
	}

	line := out.String()
	if strings.Count(line, "\n") != 1 || !strings.HasSuffix(line, "\n") {
		t.Fatalf("output is not exactly one line: %q", line)
	}
	if !strings.Contains(line, "<script>") || strings.Contains(line, `\u003c`) {
		t.Fatalf("HTML escaping is on, want raw text: %q", line)
	}
	var decoded map[string]any
	if err := json.Unmarshal([]byte(line), &decoded); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if decoded["user_code"] != "LHJC-ZBPP" {
		t.Fatalf("decoded = %v", decoded)
	}
}

// TestTextfIsSilentInJSONMode keeps stdout readable when scripts consume it.
func TestTextfIsSilentInJSONMode(t *testing.T) {
	stderr := &bytes.Buffer{}
	r := New(&bytes.Buffer{}, stderr, "json")
	r.Textf("waiting…")
	if stderr.Len() != 0 {
		t.Fatalf("Textf wrote %q in JSON mode", stderr.String())
	}

	text := New(&bytes.Buffer{}, stderr, "text")
	text.Textf("waiting…")
	if got := stderr.String(); got != "waiting…\n" {
		t.Fatalf("Textf text mode = %q", got)
	}
}

// TestFailExitCodes pins the documented mapping.
func TestFailExitCodes(t *testing.T) {
	cases := []struct {
		name string
		err  error
		want int
		code string
	}{
		{"account missing", &api.APIError{Code: "AUTH_ACCOUNT_NOT_FOUND", Message: "Account was not found.", Status: 404}, ExitFailure, "AUTH_ACCOUNT_NOT_FOUND"},
		{"bad password", &api.APIError{Code: "AUTH_INVALID_PASSWORD", Status: 400}, ExitFailure, "AUTH_INVALID_PASSWORD"},
		{"rate limited", &api.APIError{Code: "RATE_LIMITED", Status: 429}, ExitRateLimited, "RATE_LIMITED"},
		{"server error", &api.APIError{Code: "SERVER_ERROR", Status: 500}, ExitServer, "SERVER_ERROR"},
		{"denied", &api.DeviceError{Code: api.DeviceAccessDenied, Status: 400}, ExitDenied, "access_denied"},
		{"expired", &api.DeviceError{Code: api.DeviceExpiredToken, Status: 400}, ExitDenied, "expired_token"},
		{"invalid grant", &api.DeviceError{Code: api.DeviceInvalidGrant, Status: 400}, ExitDenied, "invalid_grant"},
		{"transport", &api.TransportError{Err: errors.New("dial tcp: refused")}, ExitTransport, "TRANSPORT_ERROR"},
		{"expired session", api.ErrSessionExpired, ExitAuthMissing, "SESSION_EXPIRED"},
		{"no credentials", api.ErrNoCredentials, ExitAuthMissing, "NO_CREDENTIALS"},
		{"malformed", &api.MalformedResponseError{Path: "/auth/me", Err: errors.New("unexpected end of JSON")}, ExitServer, "MALFORMED_RESPONSE"},
		{"redirect page", &api.APIError{Code: "HTTP_302", Status: http.StatusFound}, ExitFailure, "HTTP_302"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			stderr := &bytes.Buffer{}
			r := New(&bytes.Buffer{}, stderr, "json")

			if got := Fail(r, tc.err); got != tc.want {
				t.Fatalf("Fail exit = %d, want %d", got, tc.want)
			}
			var rec ErrorRecord
			if err := json.Unmarshal(stderr.Bytes(), &rec); err != nil {
				t.Fatalf("decode error record %q: %v", stderr.String(), err)
			}
			if rec.Type != RecordError {
				t.Fatalf("record type = %q, want %q", rec.Type, RecordError)
			}
			if rec.Code != tc.code {
				t.Fatalf("code = %q, want %q", rec.Code, tc.code)
			}
			if rec.Message == "" {
				t.Fatal("error record has no message")
			}
		})
	}
}

// TestFailHints covers the actionable next steps promised in the docs.
func TestFailHints(t *testing.T) {
	cases := map[string]string{
		"AUTH_ACCOUNT_NOT_FOUND": "check the username or email",
		"NO_AUTH_FACTORS":        "this account has no usable login factor — use device login",
		"RATE_LIMITED":           "wait and retry",
	}
	for code, want := range cases {
		if got := hint(code, &api.APIError{Code: code}); got != want {
			t.Errorf("hint(%s) = %q, want %q", code, got, want)
		}
	}

	detail := "Your account is suspended until Friday."
	got := hint("ACCOUNT_LOCKED", &api.APIError{Code: "ACCOUNT_LOCKED", Detail: &detail})
	if got != detail {
		t.Errorf("locked hint = %q, want the server detail", got)
	}
}

// TestTableTextAndJSON covers both renderings of the same data.
func TestTableTextAndJSON(t *testing.T) {
	out := &bytes.Buffer{}
	text := New(out, &bytes.Buffer{}, "text")
	if err := text.Table("account", []string{"field", "value"}, [][]string{{"name", "alice"}}); err != nil {
		t.Fatalf("Table: %v", err)
	}
	if !strings.Contains(out.String(), "field") || !strings.Contains(out.String(), "alice") {
		t.Fatalf("text table = %q", out.String())
	}

	jsonOut := &bytes.Buffer{}
	asJSON := New(jsonOut, &bytes.Buffer{}, "json")
	if err := asJSON.Table("account", []string{"field", "value"}, [][]string{{"name", "alice"}}); err != nil {
		t.Fatalf("Table: %v", err)
	}
	var rec map[string]any
	if err := json.Unmarshal(jsonOut.Bytes(), &rec); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if rec["type"] != "account" {
		t.Fatalf("record = %v", rec)
	}
}

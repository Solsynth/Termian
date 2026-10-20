package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/adrg/xdg"
)

// fakeServer is a scripted Stargate stand-in for the headless flows.
type fakeServer struct {
	t   *testing.T
	srv *httptest.Server

	polls        int
	refreshCalls int
	revoked      bool

	// twoFactor serves a challenge that needs a password and then a TOTP code.
	twoFactor bool
	// steps records every submitted factor ID, in order.
	steps []string
}

// newFakeServer starts the stand-in and points XDG at a temp home.
func newFakeServer(t *testing.T) *fakeServer {
	t.Helper()

	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, "config"))
	t.Setenv("XDG_STATE_HOME", filepath.Join(home, "state"))
	for _, key := range []string{"TERMIAN_SERVER", "TERMIAN_CLIENT_ID", "TERMIAN_OUTPUT", "TERMIAN_STORE", "TERMIAN_CONFIG"} {
		t.Setenv(key, "")
	}
	xdg.Reload()
	t.Cleanup(xdg.Reload)

	f := &fakeServer{t: t}
	f.srv = httptest.NewServer(http.HandlerFunc(f.handle))
	t.Cleanup(f.srv.Close)
	return f
}

// handle routes the endpoints the CLI exercises.
func (f *fakeServer) handle(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	path := strings.TrimPrefix(r.URL.Path, "/stargate")

	switch {
	case path == "/auth/open/device/code":
		io.WriteString(w, `{"device_code":"dc-1","user_code":"LHJC-ZBPP",
			"verification_uri":"https://id.solian.app/auth/device",
			"verification_uri_complete":"https://id.solian.app/auth/device?code=LHJC-ZBPP",
			"expires_in":600,"interval":1}`)

	case path == "/auth/open/token":
		_ = r.ParseForm()
		switch r.PostForm.Get("grant_type") {
		case "urn:ietf:params:oauth:grant-type:device_code":
			f.polls++
			if f.polls == 1 {
				w.WriteHeader(http.StatusBadRequest)
				io.WriteString(w, `{"error":"authorization_pending","error_description":"Authorization pending."}`)
				return
			}
			io.WriteString(w, `{"access_token":"at-device","refresh_token":"rt-device","token_type":"Bearer","expires_in":300}`)
		case "refresh_token":
			f.refreshCalls++
			io.WriteString(w, `{"access_token":"at-refreshed","refresh_token":"rt-refreshed","token_type":"Bearer","expires_in":300}`)
		default:
			w.WriteHeader(http.StatusBadRequest)
			io.WriteString(w, `{"error":"unsupported_grant_type"}`)
		}

	case path == "/auth/challenge" && r.Method == http.MethodPost && f.twoFactor:
		io.WriteString(w, `{"id":"ch-2","step_total":2,"step_remain":2,"blacklist_factors":[],"account_id":"acc-1"}`)

	case path == "/auth/challenge/ch-2/factors":
		if len(f.steps) == 0 {
			io.WriteString(w, `[{"id":"f-pw","type":0,"trustworthy":2},{"id":"f-totp","type":3,"trustworthy":2}]`)
			return
		}
		io.WriteString(w, `[{"id":"f-pw","type":0,"trustworthy":2},{"id":"f-totp","type":3,"trustworthy":2}]`)

	case path == "/auth/challenge/ch-2" && r.Method == http.MethodPatch:
		var body map[string]string
		_ = json.NewDecoder(r.Body).Decode(&body)
		f.steps = append(f.steps, body["factor_id"])
		remain := 2 - len(f.steps)
		blacklist, _ := json.Marshal(f.steps)
		fmt.Fprintf(w, `{"id":"ch-2","step_total":2,"step_remain":%d,"blacklist_factors":%s,"account_id":"acc-1"}`,
			remain, blacklist)

	case path == "/auth/challenge" && r.Method == http.MethodPost:
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		if body["account"] != "alice" {
			w.WriteHeader(http.StatusNotFound)
			io.WriteString(w, `{"code":"AUTH_ACCOUNT_NOT_FOUND","message":"Account was not found.","status":404}`)
			return
		}
		io.WriteString(w, `{"id":"ch-1","step_total":1,"step_remain":1,"blacklist_factors":[],"account_id":"acc-1"}`)

	case path == "/auth/challenge/ch-1/factors":
		io.WriteString(w, `[{"id":"f-pw","type":0,"trustworthy":2}]`)

	case path == "/auth/challenge/ch-1" && r.Method == http.MethodPatch:
		var body map[string]string
		_ = json.NewDecoder(r.Body).Decode(&body)
		if body["password"] != "correct-horse" {
			w.WriteHeader(http.StatusBadRequest)
			io.WriteString(w, `{"code":"AUTH_INVALID_PASSWORD","message":"Invalid password.","status":400}`)
			return
		}
		io.WriteString(w, `{"id":"ch-1","step_total":1,"step_remain":0,"blacklist_factors":["f-pw"],"account_id":"acc-1"}`)

	case path == "/auth/token":
		io.WriteString(w, `{"token":"at-challenge","refresh_token":"rt-challenge","expires_in":300,"refresh_expires_in":2592000}`)

	case path == "/auth/me":
		if r.Header.Get("Authorization") == "" {
			w.WriteHeader(http.StatusUnauthorized)
			io.WriteString(w, `{"code":"AUTH_UNAUTHORIZED","message":"Authentication is required.","status":401}`)
			return
		}
		io.WriteString(w, `{"id":"acc-1","name":"alice","nick":"Alice","language":"en","region":"us",
			"is_superuser":false,"session_id":"sess-1","token_type":"Bearer"}`)

	case path == "/accounts/me":
		io.WriteString(w, `{"id":"acc-1","name":"alice","nick":"Alice","perk_level":2,
			"profile":{"level":7,"experience":1234,"created_at":"2024-01-02T03:04:05Z"}}`)

	case path == "/sessions/current" && r.Method == http.MethodDelete:
		f.revoked = true
		w.WriteHeader(http.StatusNoContent)

	default:
		f.t.Errorf("unexpected request %s %s", r.Method, r.URL.Path)
		w.WriteHeader(http.StatusNotFound)
		io.WriteString(w, `{"code":"NOT_FOUND","message":"no route","status":404}`)
	}
}

// invocation captures one command tree run against the fake server.
type invocation struct {
	stdout string
	stderr string
	code   int
}

// run executes one command tree invocation against the fake server.
func (f *fakeServer) run(t *testing.T, stdin string, args ...string) invocation {
	t.Helper()

	stdout := &bytes.Buffer{}
	stderr := &bytes.Buffer{}
	app := &app{
		stdin:  strings.NewReader(stdin),
		stdout: stdout,
		stderr: stderr,
	}
	args = append(args, "--no-tui", "--store", "file", "--server", f.srv.URL)
	code := app.run(context.Background(), args)
	return invocation{stdout: stdout.String(), stderr: stderr.String(), code: code}
}

// TestDeviceLoginWhoamiRefreshLogout walks the headless lifecycle end to end.
func TestDeviceLoginWhoamiRefreshLogout(t *testing.T) {
	f := newFakeServer(t)

	login := f.run(t, "", "login", "--output", "json")
	if login.code != 0 {
		t.Fatalf("login exit = %d (stderr %s)", login.code, login.stderr)
	}
	records := ndjson(t, login.stdout)
	if len(records) == 0 || records[0]["type"] != "authorization" {
		t.Fatalf("first record = %v, want authorization", records)
	}
	if code, _ := records[0]["user_code"].(string); code != "LHJC-ZBPP" {
		t.Fatalf("user_code = %v", records[0]["user_code"])
	}
	if uri, _ := records[0]["verification_uri"].(string); uri != "https://id.solian.app/auth/device" {
		t.Fatalf("verification_uri = %v", records[0]["verification_uri"])
	}
	last := records[len(records)-1]
	if last["type"] != "authenticated" {
		t.Fatalf("last record = %v, want authenticated", last)
	}
	account, ok := last["account"].(map[string]any)
	if !ok || account["name"] != "alice" {
		t.Fatalf("account = %v", last["account"])
	}
	if strings.Contains(login.stdout, "rt-device") || strings.Contains(login.stdout, "at-device") {
		t.Fatalf("stdout leaked a token: %s", login.stdout)
	}

	whoami := f.run(t, "", "whoami", "--output", "json")
	if whoami.code != 0 {
		t.Fatalf("whoami exit = %d (stderr %s)", whoami.code, whoami.stderr)
	}
	who := ndjson(t, whoami.stdout)
	if len(who) != 1 || who[0]["type"] != "account" {
		t.Fatalf("whoami records = %v", who)
	}
	session, ok := who[0]["session"].(map[string]any)
	if !ok || session["id"] != "sess-1" {
		t.Fatalf("session = %v", who[0]["session"])
	}

	// Two rotations in a row must both succeed (server grace period).
	before := f.refreshCalls
	for i := range 2 {
		refresh := f.run(t, "", "refresh", "--output", "json")
		if refresh.code != 0 {
			t.Fatalf("refresh %d exit = %d (stderr %s)", i+1, refresh.code, refresh.stderr)
		}
		refreshed := ndjson(t, refresh.stdout)
		if len(refreshed) != 1 || refreshed[0]["type"] != "refreshed" {
			t.Fatalf("refresh %d records = %v", i+1, refreshed)
		}
	}
	if rotations := f.refreshCalls - before; rotations != 2 {
		t.Fatalf("refresh calls = %d, want 2", rotations)
	}

	outv := f.run(t, "", "logout", "--output", "json")
	if outv.code != 0 {
		t.Fatalf("logout exit = %d (stderr %s)", outv.code, outv.stderr)
	}
	out := ndjson(t, outv.stdout)
	if len(out) != 1 || out[0]["type"] != "logged_out" {
		t.Fatalf("logout records = %v", out)
	}
	if !f.revoked {
		t.Fatal("logout did not revoke the server session")
	}

	after := f.run(t, "", "whoami", "--output", "json")
	if after.code != 3 {
		t.Fatalf("whoami after logout exit = %d, want 3", after.code)
	}
	errRecord := ndjson(t, after.stderr)
	if len(errRecord) != 1 || errRecord[0]["type"] != "error" {
		t.Fatalf("error records = %v", errRecord)
	}
}

// TestPasswordLoginTwoFactors covers the multi-step challenge loop: the first
// factor is answered, its ID is blacklisted server-side, and only the remaining
// factor is offered next.
func TestPasswordLoginTwoFactors(t *testing.T) {
	f := newFakeServer(t)
	f.twoFactor = true

	// account, factor choice (1 = password), password, TOTP code.
	res := f.run(t, "alice\n1\nhunter2\n123456\n", "login", "--password", "--output", "json")
	if res.code != 0 {
		t.Fatalf("password login exit = %d (stderr %s)", res.code, res.stderr)
	}
	if len(f.steps) != 2 || f.steps[0] != "f-pw" || f.steps[1] != "f-totp" {
		t.Fatalf("submitted factors = %v, want [f-pw f-totp]", f.steps)
	}

	records := ndjson(t, res.stdout)
	if last := records[len(records)-1]; last["type"] != "authenticated" {
		t.Fatalf("records = %v", records)
	}
}

// TestPasswordLoginUnknownAccount covers the documented failure path.
func TestPasswordLoginUnknownAccount(t *testing.T) {
	f := newFakeServer(t)

	res := f.run(t, "definitely-not-a-user-zzz\n", "login", "--password", "--output", "json")
	if res.code != 1 {
		t.Fatalf("login exit = %d, want 1 (stderr %s)", res.code, res.stderr)
	}
	records := ndjson(t, res.stderr)
	if len(records) != 1 {
		t.Fatalf("error records = %v", records)
	}
	if code, _ := records[0]["code"].(string); code != "AUTH_ACCOUNT_NOT_FOUND" {
		t.Fatalf("code = %v, want AUTH_ACCOUNT_NOT_FOUND", records[0]["code"])
	}
}

// TestPasswordLoginChallengeFlow covers account + factor + secret + exchange.
func TestPasswordLoginChallengeFlow(t *testing.T) {
	f := newFakeServer(t)

	res := f.run(t, "alice\ncorrect-horse\n", "login", "--password", "--output", "json")
	if res.code != 0 {
		t.Fatalf("password login exit = %d (stderr %s)", res.code, res.stderr)
	}
	records := ndjson(t, res.stdout)
	last := records[len(records)-1]
	if last["type"] != "authenticated" {
		t.Fatalf("records = %v", records)
	}
	if method, _ := last["method"].(string); method != "challenge" {
		t.Fatalf("method = %v, want challenge", last["method"])
	}

	// The stored session is usable right away.
	text := f.run(t, "", "whoami", "--output", "text")
	if text.code != 0 {
		t.Fatalf("whoami exit = %d (stderr %s)", text.code, text.stderr)
	}
	if !strings.Contains(text.stdout, "alice") || !strings.Contains(text.stdout, "sess-1") {
		t.Fatalf("text output = %q", text.stdout)
	}
}

// ndjson decodes every record in the buffer.
func ndjson(t *testing.T, raw string) []map[string]any {
	t.Helper()
	var out []map[string]any
	for _, line := range strings.Split(strings.TrimSpace(raw), "\n") {
		if strings.TrimSpace(line) == "" {
			continue
		}
		var rec map[string]any
		if err := json.Unmarshal([]byte(line), &rec); err != nil {
			t.Fatalf("bad NDJSON line %q: %v", line, err)
		}
		out = append(out, rec)
	}
	return out
}

// TestReadSecretFallsBackToLine keeps scripted logins working: without a TTY the
// secret is read from stdin as a plain line.
func TestReadSecretFallsBackToLine(t *testing.T) {
	a := &app{stdin: strings.NewReader("hunter2\n"), stdout: io.Discard, stderr: io.Discard}
	secret, err := a.readSecret("")
	if err != nil {
		t.Fatalf("readSecret: %v", err)
	}
	if secret != "hunter2" {
		t.Fatalf("secret = %q", secret)
	}

	a = &app{stdin: strings.NewReader(""), stdout: io.Discard, stderr: io.Discard}
	if _, err := a.readSecret(""); err == nil {
		t.Fatal("readSecret accepted an empty secret")
	}
}

// TestUsageErrorsExitTwo pins exit 2 for invalid invocations.
func TestUsageErrorsExitTwo(t *testing.T) {
	f := newFakeServer(t)

	cases := map[string][]string{
		"unknown flag":        {"whoami", "--bogus"},
		"unknown command":     {"nosuchcmd"},
		"conflicting flows":   {"login", "--device", "--password"},
		"unexpected argument": {"version", "extra"},
	}
	for name, args := range cases {
		t.Run(name, func(t *testing.T) {
			res := f.run(t, "", args...)
			if res.code != 2 {
				t.Fatalf("exit = %d, want 2 (stderr %s)", res.code, res.stderr)
			}
		})
	}
}

// TestNoTUIWithoutTerminal pins the headless selection rule for JSON output.
func TestNoTUIWithoutTerminal(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, "config"))
	t.Setenv("XDG_STATE_HOME", filepath.Join(home, "state"))
	xdg.Reload()
	t.Cleanup(xdg.Reload)

	a := &app{stdin: strings.NewReader(""), stdout: os.Stdout, stderr: os.Stderr}
	if err := a.prepare(); err != nil {
		t.Fatalf("prepare: %v", err)
	}
	if a.tuiAllowed() {
		t.Fatal("tuiAllowed() = true under go test (stdout is not a terminal)")
	}
}

package tui

import (
	"bytes"
	"context"
	"errors"
	"io"
	"strings"
	"sync"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/charmbracelet/x/exp/teatest/v2"

	"src.solsynth.dev/sosys/termian/internal/api"
	"src.solsynth.dev/sosys/termian/internal/credentials"
	"src.solsynth.dev/sosys/termian/internal/deviceid"
)

// fakeAPI is a scripted API implementation for the TUI tests.
type fakeAPI struct {
	mu sync.Mutex

	deviceCode api.DeviceCode
	// pollResults are returned in order; the last one repeats.
	pollResults []pollResult
	polls       int

	challenge api.Challenge
	factors   []api.Factor
	// secrets maps an accepted secret per factor ID; a missing entry accepts
	// anything.
	secrets map[string]string

	identity api.Identity
	account  api.Account
}

// pollResult is one device token poll answer.
type pollResult struct {
	tok api.OAuthToken
	err error
}

// RequestDeviceCode returns the scripted device authorization.
func (f *fakeAPI) RequestDeviceCode(context.Context) (api.DeviceCode, error) {
	return f.deviceCode, nil
}

// PollDeviceToken returns the next scripted poll answer.
func (f *fakeAPI) PollDeviceToken(context.Context, string) (api.OAuthToken, error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	pending := pollResult{err: &api.DeviceError{Code: api.DeviceAuthorizationPending}}
	if len(f.pollResults) == 0 {
		return api.OAuthToken{}, pending.err
	}
	idx := min(f.polls, len(f.pollResults)-1)
	f.polls++
	return f.pollResults[idx].tok, f.pollResults[idx].err
}

// CreateChallenge returns the scripted challenge.
func (f *fakeAPI) CreateChallenge(context.Context, string) (api.Challenge, error) {
	return f.challenge, nil
}

// Factors returns the scripted factor list.
func (f *fakeAPI) Factors(context.Context, string) ([]api.Factor, error) {
	return f.factors, nil
}

// RequestFactorCode is a no-op in the fake.
func (f *fakeAPI) RequestFactorCode(context.Context, string, string) error { return nil }

// SubmitFactor verifies the secret and advances the challenge.
func (f *fakeAPI) SubmitFactor(_ context.Context, _, factorID, secret string) (api.Challenge, error) {
	if want, ok := f.secrets[factorID]; ok && secret != want {
		return api.Challenge{}, &api.APIError{
			Code:    "AUTH_INVALID_PASSWORD",
			Message: "Invalid password.",
			Status:  400,
		}
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	f.challenge.StepRemain--
	f.challenge.BlacklistFactors = append(f.challenge.BlacklistFactors, factorID)
	return f.challenge, nil
}

// ExchangeChallenge issues the scripted token pair.
func (f *fakeAPI) ExchangeChallenge(context.Context, string) (api.TokenPair, error) {
	return api.TokenPair{Token: "at-challenge", RefreshToken: "rt-challenge", ExpiresIn: 300}, nil
}

// Challenge reports the current scripted challenge.
func (f *fakeAPI) Challenge(context.Context, string) (api.Challenge, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.challenge, nil
}

// Identity returns the scripted identity.
func (f *fakeAPI) Identity(context.Context) (api.Identity, error) { return f.identity, nil }

// Account returns the scripted account.
func (f *fakeAPI) Account(context.Context) (api.Account, error) { return f.account, nil }

// newTestDeps builds Deps wired to the fake API and a temporary file store.
func newTestDeps(t *testing.T, fake *fakeAPI) Deps {
	t.Helper()

	store, err := credentials.Open(credentials.ModeFile, t.TempDir())
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	client, err := api.New("https://api.invalid", "solian", "*",
		deviceid.Identity{ID: "dev-1", Name: "test-host", Platform: deviceid.PlatformLinux}, "test")
	if err != nil {
		t.Fatalf("api.New: %v", err)
	}

	return Deps{
		Ctx:          context.Background(),
		API:          fake,
		Tokens:       api.NewTokenSource(client, store, nil),
		Store:        store,
		Server:       "https://api.solian.app/stargate",
		ClientID:     "solian",
		DeviceID:     "dev-1",
		Version:      "test",
		PollInterval: 5 * time.Millisecond,
	}
}

// recorder accumulates every frame teatest renders.
//
// The program only repaints changed lines and teatest hands out a read-once
// stream, so each assertion inspects the accumulated history instead of one
// slice of new bytes.
type recorder struct {
	t     *testing.T
	tm    *teatest.TestModel
	model *Model
	buf   bytes.Buffer
}

// newRecorder starts a test program at the given terminal size.
func newRecorder(t *testing.T, model *Model, width, height int) *recorder {
	t.Helper()
	tm := teatest.NewTestModel(t, model, teatest.WithInitialTermSize(width, height))
	return &recorder{t: t, tm: tm, model: model}
}

// waitState blocks until the model snapshot satisfies cond.
func (r *recorder) waitState(what string, cond func(Snapshot) bool) {
	r.t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if cond(r.model.Snapshot()) {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	r.drain()
	r.t.Fatalf("timed out waiting for %s; snapshot = %+v\n%s", what, r.model.Snapshot(), tail(r.buf.String(), 800))
}

// drain copies whatever the program has rendered into the history buffer.
func (r *recorder) drain() {
	r.t.Helper()
	chunk, err := io.ReadAll(r.tm.Output())
	if err != nil {
		r.t.Fatalf("read output: %v", err)
	}
	r.buf.Write(chunk)
}

// wait blocks until cond matches the accumulated frames.
func (r *recorder) wait(what string, cond func(flat string) bool) {
	r.t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		r.drain()
		if cond(flatten(r.buf.String())) {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	r.drain()
	r.t.Fatalf("timed out waiting for %s; last frames:\n%s", what, tail(r.buf.String(), 1200))
}

// send delivers a key press.
func (r *recorder) send(key tea.KeyPressMsg) { r.tm.Send(key) }

// typeText types plain characters.
func (r *recorder) typeText(text string) { r.tm.Type(text) }

// finish quits the program and returns the final model.
func (r *recorder) finish() *Model {
	r.t.Helper()
	r.send(tea.KeyPressMsg{Code: 'c', Mod: tea.ModCtrl})
	r.tm.WaitFinished(r.t, teatest.WithFinalTimeout(5*time.Second))

	final, ok := r.tm.FinalModel(r.t, teatest.WithFinalTimeout(5*time.Second)).(*Model)
	if !ok {
		r.t.Fatal("final model is not *tui.Model")
	}
	return final
}

// flatten removes styling escapes and the spaces the UI adds for letter spacing
// and card padding, so assertions see plain text.
//
// The renderer interleaves cursor-position sequences between characters of one
// line, so escapes must go before any substring match.
func flatten(s string) string {
	return ansi.Strip(strings.ReplaceAll(s, " ", ""))
}

// tail returns the last n bytes of s for failure messages.
func tail(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return "…" + s[len(s)-n:]
}

// TestDeviceLoginScenario drives the login pane end to end: device code, one
// pending poll, then success, landing on the home screen.
func TestDeviceLoginScenario(t *testing.T) {
	fake := &fakeAPI{
		deviceCode: api.DeviceCode{
			DeviceCode:      "dc-1",
			UserCode:        "LHJC-ZBPP",
			VerificationURI: "https://id.solian.app/auth/device",
			ExpiresIn:       600,
			Interval:        5,
		},
		pollResults: []pollResult{
			{err: &api.DeviceError{Code: api.DeviceAuthorizationPending, Description: "Authorization pending."}},
			{tok: api.OAuthToken{AccessToken: "at-device", RefreshToken: "rt-device", ExpiresIn: 300}},
		},
		identity: api.Identity{ID: "acc-1", Name: "alice", Nick: "Alice", SessionID: "sess-1", TokenType: "Bearer"},
		account: api.Account{
			ID: "acc-1", Name: "alice", Nick: "Alice", PerkLevel: 2,
			Profile: &api.Profile{Level: 7, Experience: 1234},
		},
	}

	deps := newTestDeps(t, fake)
	// A visible gap between polls keeps the device card on screen long enough
	// for the recorder to capture it.
	deps.PollInterval = 300 * time.Millisecond
	model := NewModel(deps)
	rec := newRecorder(t, model, 100, 30)

	rec.wait("the device card", func(flat string) bool {
		return strings.Contains(flat, "Termian") &&
			strings.Contains(flat, "SignintoSolian") &&
			strings.Contains(flat, "LHJC-ZBPP") &&
			strings.Contains(flat, "https://id.solian.app/auth/device")
	})
	rec.waitState("the session shell", func(s Snapshot) bool {
		return s.Screen == ScreenHome && s.HomeLoaded && s.AccountName == "alice"
	})
	rec.wait("the account card", func(flat string) bool {
		return strings.Contains(flat, "level7·1234XP") && strings.Contains(flat, "SignedintoSolian")
	})

	final := rec.finish()
	if final.Screen != ScreenHome {
		t.Fatalf("final screen = %s, want home", final.Screen)
	}
	if got := final.Home.identity.Nick; got != "Alice" {
		t.Fatalf("home nick = %q", got)
	}
	if final.Home.account.Profile == nil || final.Home.account.Profile.Level != 7 {
		t.Fatalf("home profile = %+v", final.Home.account.Profile)
	}

	creds, err := model.Deps.Store.Load("https://api.solian.app/stargate")
	if err != nil || creds == nil {
		t.Fatalf("stored credentials = %+v, %v", creds, err)
	}
	if creds.RefreshToken != "rt-device" || creds.AccountName != "alice" {
		t.Fatalf("stored credentials = %+v", creds)
	}
}

// TestScreenNavigation covers the documented keys: p enters the password pane,
// esc returns to the device pane, ctrl+c quits.
func TestScreenNavigation(t *testing.T) {
	fake := &fakeAPI{
		deviceCode: api.DeviceCode{
			DeviceCode:      "dc-1",
			UserCode:        "LHJC-ZBPP",
			VerificationURI: "https://id.solian.app/auth/device",
			ExpiresIn:       600,
			Interval:        5,
		},
		pollResults: []pollResult{{err: &api.DeviceError{Code: api.DeviceAuthorizationPending}}},
	}

	rec := newRecorder(t, NewModel(newTestDeps(t, fake)), 100, 30)
	rec.wait("the device card", func(flat string) bool {
		return strings.Contains(flat, "LHJC-ZBPP")
	})

	rec.typeText("p")
	rec.waitState("the password pane", func(s Snapshot) bool { return s.Screen == ScreenPassword })
	rec.wait("the account prompt", func(flat string) bool {
		return strings.Contains(flat, "Passwordlogin") && strings.Contains(flat, "usernameoremail")
	})

	rec.send(tea.KeyPressMsg{Code: tea.KeyEsc})
	rec.waitState("the login pane", func(s Snapshot) bool { return s.Screen == ScreenLogin })

	final := rec.finish()
	if final.Screen != ScreenLogin {
		t.Fatalf("final screen = %s, want login", final.Screen)
	}
}

// TestViewUsesAltScreen pins the full-screen contract the TUI promises.
func TestViewUsesAltScreen(t *testing.T) {
	model := NewModel(newTestDeps(t, &fakeAPI{
		deviceCode:  api.DeviceCode{DeviceCode: "dc-1", UserCode: "LHJC-ZBPP", ExpiresIn: 600, Interval: 5},
		pollResults: []pollResult{{err: &api.DeviceError{Code: api.DeviceAuthorizationPending}}},
	}))

	view := model.View()
	if !view.AltScreen {
		t.Fatal("View.AltScreen = false")
	}
	if view.WindowTitle != "Termian — Solian" {
		t.Fatalf("WindowTitle = %q", view.WindowTitle)
	}
	if view.Cursor != nil {
		t.Fatal("View.Cursor is set; Bubbles' virtual cursor must be kept")
	}
}

// TestSmallTerminalRendersNotice keeps the layout honest on tiny terminals.
func TestSmallTerminalRendersNotice(t *testing.T) {
	fake := &fakeAPI{
		deviceCode:  api.DeviceCode{DeviceCode: "dc-1", UserCode: "LHJC-ZBPP", ExpiresIn: 600, Interval: 5},
		pollResults: []pollResult{{err: &api.DeviceError{Code: api.DeviceAuthorizationPending}}},
	}

	model := NewModel(newTestDeps(t, fake))
	model.Width, model.Height, model.sized = 30, 8, true

	if view := model.render(); !strings.Contains(view, "terminal too small") {
		t.Fatalf("render = %q, want the size notice", view)
	}
	// Keys other than ctrl+c must be ignored while too small.
	if _, cmd := model.Update(tea.KeyPressMsg{Code: 'p', Text: "p"}); cmd != nil {
		t.Fatal("a key was handled in a too-small terminal")
	}
	if model.Screen != ScreenLogin {
		t.Fatalf("screen = %s, want login", model.Screen)
	}

	updated, cmd := model.Update(tea.KeyPressMsg{Code: 'c', Mod: tea.ModCtrl})
	if cmd == nil || updated.(*Model).Screen != ScreenLogin {
		t.Fatal("ctrl+c did not produce a quit command")
	}
}

// TestPasswordLoginScenario walks the challenge pane: account, factor, wrong
// secret, right secret, then the home screen.
func TestPasswordLoginScenario(t *testing.T) {
	fake := &fakeAPI{
		challenge: api.Challenge{ID: "ch-1", StepTotal: 1, StepRemain: 1, AccountID: "acc-1"},
		factors:   []api.Factor{{ID: "f-pw", Type: api.FactorPassword, Trustworthy: 2}},
		secrets:   map[string]string{"f-pw": "correct-horse"},
		identity:  api.Identity{ID: "acc-1", Name: "alice", Nick: "Alice", SessionID: "sess-1"},
		account:   api.Account{ID: "acc-1", Name: "alice", Nick: "Alice"},
	}

	model := NewModelWithScreen(newTestDeps(t, fake), ScreenPassword)
	rec := newRecorder(t, model, 100, 30)

	rec.wait("the account prompt", func(flat string) bool {
		return strings.Contains(flat, "Passwordlogin")
	})

	rec.typeText("alice")
	rec.send(tea.KeyPressMsg{Code: tea.KeyEnter})
	rec.waitState("the factor list", func(s Snapshot) bool { return s.PasswordStage == stageFactor })
	rec.wait("the factor row", func(flat string) bool {
		return strings.Contains(flat, "chooseafactor") && strings.Contains(flat, "▸password")
	})

	rec.send(tea.KeyPressMsg{Code: tea.KeyEnter})
	rec.waitState("the secret prompt", func(s Snapshot) bool { return s.PasswordStage == stageSecret })

	rec.typeText("wrong")
	rec.send(tea.KeyPressMsg{Code: tea.KeyEnter})
	rec.waitState("the rejection banner", func(s Snapshot) bool {
		return s.PasswordStage == stageSecret && strings.Contains(s.Banner, "wrong password/code")
	})

	// Replace the rejected secret before retrying.
	for range len("wrong") {
		rec.send(tea.KeyPressMsg{Code: tea.KeyBackspace})
	}
	rec.typeText("correct-horse")
	rec.send(tea.KeyPressMsg{Code: tea.KeyEnter})
	rec.waitState("the session shell", func(s Snapshot) bool {
		return s.Screen == ScreenHome && s.AccountName == "alice"
	})

	final := rec.finish()
	if final.Screen != ScreenHome {
		t.Fatalf("final screen = %s, want home", final.Screen)
	}
	if creds := model.Deps.Tokens.Credentials(); creds == nil || creds.Method != credentials.MethodChallenge {
		t.Fatalf("token source credentials = %+v", creds)
	}
}

// TestDeviceErrorStopsPolling covers a terminal device-flow failure.
func TestDeviceErrorStopsPolling(t *testing.T) {
	fake := &fakeAPI{
		deviceCode:  api.DeviceCode{DeviceCode: "dc-1", UserCode: "LHJC-ZBPP", ExpiresIn: 600, Interval: 5},
		pollResults: []pollResult{{err: &api.DeviceError{Code: api.DeviceAccessDenied, Status: 400}}},
	}

	rec := newRecorder(t, NewModel(newTestDeps(t, fake)), 100, 30)
	rec.waitState("the denial banner", func(s Snapshot) bool {
		return s.LoginFinished && strings.Contains(s.Banner, "sign-in was denied")
	})
	rec.wait("the denial text", func(flat string) bool {
		return strings.Contains(flat, "sign-inwasdenied")
	})

	final := rec.finish()
	if !final.Login.finished {
		t.Fatal("poll loop still running after access_denied")
	}
	if final.Screen != ScreenLogin {
		t.Fatalf("screen = %s, want login", final.Screen)
	}
}

// TestSessionExpiredShowsBanner keeps the recovery path visible.
func TestSessionExpiredShowsBanner(t *testing.T) {
	model := NewModelWithScreen(newTestDeps(t, &fakeAPI{}), ScreenHome)
	model.Width, model.Height, model.sized = 100, 30, true

	updated, _ := model.Update(authedMsg{err: api.ErrSessionExpired})
	final := updated.(*Model)
	if !final.Home.refreshDisabled {
		t.Fatal("refresh was not disabled after a rejected session")
	}
	if !strings.Contains(final.render(), "session expired") {
		t.Fatalf("banner missing: %q", final.Banner)
	}
}

// TestLogoutReturnsToLogin covers the l key: credentials are deleted and the
// device flow restarts.
func TestLogoutReturnsToLogin(t *testing.T) {
	fake := &fakeAPI{
		deviceCode:  api.DeviceCode{DeviceCode: "dc-2", UserCode: "ABCD-EFGH", ExpiresIn: 600, Interval: 5},
		pollResults: []pollResult{{err: &api.DeviceError{Code: api.DeviceAuthorizationPending}}},
		identity:    api.Identity{ID: "acc-1", Name: "alice", Nick: "Alice"},
		account:     api.Account{ID: "acc-1", Name: "alice", Nick: "Alice"},
	}
	deps := newTestDeps(t, fake)
	deps.Tokens.Seed(&credentials.Credentials{
		Server:       deps.Server,
		Method:       credentials.MethodDevice,
		RefreshToken: "rt-device",
	}, "at-device", 300)

	model := NewModelWithScreen(deps, ScreenHome)
	model.Width, model.Height, model.sized = 100, 30, true

	updated, cmd := model.Update(loggedOutMsg{})
	final := updated.(*Model)
	if final.Screen != ScreenLogin {
		t.Fatalf("screen = %s, want login", final.Screen)
	}
	if cmd == nil {
		t.Fatal("logout did not restart the device flow")
	}
	if !strings.Contains(final.render(), "signed out") {
		t.Fatalf("notice = %q", final.Banner)
	}
}

// TestLogoutErrorSurfaces keeps a failed revoke visible without losing the shell.
func TestLogoutErrorSurfaces(t *testing.T) {
	model := NewModelWithScreen(newTestDeps(t, &fakeAPI{}), ScreenHome)
	model.Width, model.Height, model.sized = 100, 30, true

	updated, _ := model.Update(loggedOutMsg{err: errors.New("network unreachable")})
	final := updated.(*Model)
	if final.Screen != ScreenHome {
		t.Fatalf("screen = %s, want home", final.Screen)
	}
	if !strings.Contains(final.Banner, "network unreachable") {
		t.Fatalf("banner = %q", final.Banner)
	}
}

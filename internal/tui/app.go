// Package tui is the interactive Termian front end: a Bubble Tea program with a
// device-flow login pane, a password/TOTP challenge pane and a session shell.
package tui

import (
	"context"
	"fmt"
	"os/exec"
	"runtime"
	"strings"
	"sync"
	"time"

	"charm.land/bubbles/v2/spinner"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"src.solsynth.dev/sosys/termian/internal/api"
	"src.solsynth.dev/sosys/termian/internal/credentials"
)

// Screen identifies the visible pane.
type Screen int

// Screens in navigation order.
const (
	ScreenLogin Screen = iota
	ScreenPassword
	ScreenHome
)

// String renders the screen name (used in tests and diagnostics).
func (s Screen) String() string {
	switch s {
	case ScreenLogin:
		return "login"
	case ScreenPassword:
		return "password"
	case ScreenHome:
		return "home"
	default:
		return "unknown"
	}
}

// API is the subset of the Stargate client the TUI depends on; tests fake it.
type API interface {
	RequestDeviceCode(context.Context) (api.DeviceCode, error)
	PollDeviceToken(context.Context, string) (api.OAuthToken, error)
	CreateChallenge(context.Context, string) (api.Challenge, error)
	Factors(context.Context, string) ([]api.Factor, error)
	RequestFactorCode(context.Context, string, string) error
	SubmitFactor(context.Context, string, string, string) (api.Challenge, error)
	ExchangeChallenge(context.Context, string) (api.TokenPair, error)
	Challenge(context.Context, string) (api.Challenge, error)
	Identity(context.Context) (api.Identity, error)
	Account(context.Context) (api.Account, error)
}

// commandTimeout bounds every asynchronous API call made from the UI.
const commandTimeout = 15 * time.Second

// Deps is the static configuration the model needs.
type Deps struct {
	// Ctx is cancelled when the program exits.
	Ctx context.Context
	// API performs the auth calls.
	API API
	// Tokens owns the access token; nil before the first successful login.
	Tokens *api.TokenSource
	// Store persists the refresh token.
	Store *credentials.Store
	// Server is the normalized API base used as the credential key.
	Server string
	// ClientID is the OAuth client used by the device flow.
	ClientID string
	// DeviceID is this machine's stable identity.
	DeviceID string
	// Version is the Termian version shown in the UI.
	Version string
	// PollInterval overrides the server device poll interval (tests).
	PollInterval time.Duration
}

// Model is the root Bubble Tea model.
type Model struct {
	Screen   Screen
	Width    int
	Height   int
	Dark     bool
	Styles   Styles
	Banner   string
	BannerOK bool
	Spinner  spinner.Model
	Login    loginModel
	Password passwordModel
	Home     homeModel
	Deps     Deps

	sized bool

	// mu guards the mutable UI state so observers (tests, diagnostics) can take
	// a consistent snapshot while the program goroutine updates it.
	mu sync.Mutex
}

// Snapshot is a consistent view of the model's state.
type Snapshot struct {
	Screen         Screen
	PasswordStage  passwordStage
	LoginFinished  bool
	HomeLoaded     bool
	Banner         string
	BannerOK       bool
	AccountName    string
	SessionCredsOK bool
	RefreshBlocked bool
}

// Snapshot returns the current UI state under the model lock.
func (m *Model) Snapshot() Snapshot {
	m.mu.Lock()
	defer m.mu.Unlock()

	snap := Snapshot{
		Screen:         m.Screen,
		PasswordStage:  m.Password.Stage,
		LoginFinished:  m.Login.finished,
		HomeLoaded:     m.Home.loaded,
		Banner:         m.Banner,
		BannerOK:       m.BannerOK,
		AccountName:    m.Home.account.Name,
		RefreshBlocked: m.Home.refreshDisabled,
	}
	if m.Deps.Tokens != nil {
		snap.SessionCredsOK = m.Deps.Tokens.Credentials() != nil
	}
	return snap
}

// NewModel builds the initial model on the device login pane.
func NewModel(deps Deps) *Model {
	return NewModelWithScreen(deps, ScreenLogin)
}

// NewModelWithScreen builds a model starting on a specific screen.
func NewModelWithScreen(deps Deps, screen Screen) *Model {
	sp := spinner.New()
	sp.Spinner = spinner.Dot

	m := &Model{
		Screen:  screen,
		Dark:    true,
		Spinner: sp,
		Deps:    deps,
		Login:   newLoginModel(),
		Home:    newHomeModel(),
	}
	m.Styles = newStyles(m.Dark)
	m.Password = newPasswordModel(m.Styles)
	if screen == ScreenPassword {
		m.Password.reset()
	}
	return m
}

// Init implements tea.Model.
func (m *Model) Init() tea.Cmd {
	cmds := []tea.Cmd{m.Spinner.Tick, tea.RequestBackgroundColor}
	if m.Screen == ScreenHome && m.Deps.Tokens != nil {
		cmds = append(cmds, tea.Tick(time.Second, func(t time.Time) tea.Msg { return homeTickMsg{at: t} }))
		cmds = append(cmds, m.fetchAccountCmd())
		return tea.Batch(cmds...)
	}
	cmds = append(cmds, m.startDeviceFlowCmd())
	return tea.Batch(cmds...)
}

// View implements tea.Model.
func (m *Model) View() tea.View {
	m.mu.Lock()
	defer m.mu.Unlock()

	v := tea.NewView(m.render())
	v.AltScreen = true
	v.WindowTitle = "Termian — Solian"
	return v
}

// render draws the active screen, centered.
func (m *Model) render() string {
	if m.tooSmall() {
		return lipgloss.Place(max(m.Width, 1), max(m.Height, 1), lipgloss.Center, lipgloss.Center,
			m.Styles.Muted.Render("terminal too small"))
	}

	var body string
	switch m.Screen {
	case ScreenLogin:
		body = m.loginView()
	case ScreenPassword:
		body = m.passwordView()
	case ScreenHome:
		body = m.homeView()
	}
	if m.Banner != "" {
		body = lipgloss.JoinVertical(lipgloss.Center, body, "", m.bannerView())
	}
	if !m.sized {
		return body
	}
	return lipgloss.Place(m.Width, m.Height, lipgloss.Center, lipgloss.Center, body)
}

// bannerView renders the status line.
func (m *Model) bannerView() string {
	if m.BannerOK {
		return m.Styles.Notice.Render(m.Banner)
	}
	return m.Styles.Error.Render(m.Banner)
}

// setBanner shows a failure message below the active card.
func (m *Model) setBanner(msg string) {
	if msg == "" {
		m.clearBanner()
		return
	}
	m.Banner = msg
	m.BannerOK = false
}

// setNotice shows a success message below the active card.
func (m *Model) setNotice(msg string) {
	m.Banner = msg
	m.BannerOK = true
}

// clearBanner removes the status line.
func (m *Model) clearBanner() {
	m.Banner = ""
	m.BannerOK = false
}

// tooSmall reports whether the layout does not fit the terminal.
func (m *Model) tooSmall() bool {
	return m.sized && (m.Width < MinWidth || m.Height < MinHeight)
}

// Update implements tea.Model.
func (m *Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	m.mu.Lock()
	defer m.mu.Unlock()

	// Global keys win over every screen handler.
	if key, ok := msg.(tea.KeyPressMsg); ok {
		if key.String() == "ctrl+c" {
			return m, tea.Quit
		}
		if m.tooSmall() {
			return m, nil
		}
	}

	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.Width, m.Height = msg.Width, msg.Height
		m.sized = true
		m.Login.setWidth(cardWidth(DeviceCardWidth, msg.Width))
		m.Password.setWidth(cardWidth(PasswordCardWidth, msg.Width))
		m.Home.setWidth(cardWidth(HomeCardWidth, msg.Width))
		return m, nil

	case tea.BackgroundColorMsg:
		dark := msg.IsDark()
		if dark != m.Dark {
			m.Dark = dark
			m.Styles = newStyles(dark)
			m.Password.applyStyles(m.Styles)
		}
		return m, nil

	case spinner.TickMsg:
		var cmd tea.Cmd
		m.Spinner, cmd = m.Spinner.Update(msg)
		return m, cmd

	case tea.KeyPressMsg:
		return m.handleKey(msg)
	}

	return m.handleScreenMsg(msg)
}

// handleKey routes a key press to the active screen.
func (m *Model) handleKey(key tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	switch m.Screen {
	case ScreenLogin:
		return m.updateLoginKey(key)
	case ScreenPassword:
		return m.updatePasswordKey(key)
	case ScreenHome:
		return m.updateHomeKey(key)
	}
	return m, nil
}

// handleScreenMsg routes asynchronous results to the pane that owns them.
func (m *Model) handleScreenMsg(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg.(type) {
	case deviceCodeMsg, pollDueMsg, deviceTokenMsg, browserDoneMsg:
		return m.updateLoginMsg(msg)
	case challengeMsg, factorsMsg, factorCodeMsg, stepResultMsg, exchangedMsg, challengeTickMsg:
		return m.updatePasswordMsg(msg)
	case authedMsg, refreshedMsg, loggedOutMsg, homeTickMsg:
		return m.updateHomeMsg(msg)
	}
	return m, nil
}

// showScreen switches panes and clears transient status.
func (m *Model) showScreen(screen Screen) {
	m.Screen = screen
	m.clearBanner()
}

// withTimeout runs fn against a per-call timeout derived from the program
// context and returns its message.
func (m *Model) withTimeout(fn func(ctx context.Context) tea.Msg) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(m.Deps.Ctx, commandTimeout)
		defer cancel()
		return fn(ctx)
	}
}

// startDeviceFlowCmd requests a device authorization.
func (m *Model) startDeviceFlowCmd() tea.Cmd {
	return m.withTimeout(func(ctx context.Context) tea.Msg {
		code, err := m.Deps.API.RequestDeviceCode(ctx)
		return deviceCodeMsg{code: code, err: err}
	})
}

// pollDeviceTokenCmd performs one token poll.
func (m *Model) pollDeviceTokenCmd(deviceCode string) tea.Cmd {
	return m.withTimeout(func(ctx context.Context) tea.Msg {
		tok, err := m.Deps.API.PollDeviceToken(ctx, deviceCode)
		return deviceTokenMsg{tok: tok, err: err}
	})
}

// schedulePoll arms the next device token poll.
func (m *Model) schedulePoll(interval time.Duration) tea.Cmd {
	return tea.Tick(interval, func(t time.Time) tea.Msg { return pollDueMsg{at: t} })
}

// scheduleChallengePoll arms the 2 s cross-device challenge poll.
func (m *Model) scheduleChallengePoll() tea.Cmd {
	return tea.Tick(2*time.Second, func(time.Time) tea.Msg { return challengeTickMsg{due: true} })
}

// fetchChallengeCmd re-reads a challenge after a poll tick.
func (m *Model) fetchChallengeCmd(challengeID string) tea.Cmd {
	return m.withTimeout(func(ctx context.Context) tea.Msg {
		ch, err := m.Deps.API.Challenge(ctx, challengeID)
		return challengeTickMsg{ch: ch, err: err}
	})
}

// fetchAccountCmd loads the identity and account using the current token.
func (m *Model) fetchAccountCmd() tea.Cmd {
	return m.withTimeout(m.fetchAccount)
}

// fetchAccount performs the identity and account calls with a supplied context.
func (m *Model) fetchAccount(ctx context.Context) tea.Msg {
	if m.Deps.Tokens != nil {
		if _, err := m.Deps.Tokens.Token(ctx); err != nil {
			return authedMsg{err: err}
		}
	}
	id, err := m.Deps.API.Identity(ctx)
	if err != nil {
		return authedMsg{err: err}
	}
	acc, err := m.Deps.API.Account(ctx)
	if err != nil {
		return authedMsg{id: id, err: err}
	}
	return authedMsg{id: id, acc: acc}
}

// finishLoginCmd persists freshly issued credentials, seeds the token source and
// loads the account, which moves the UI to the home screen.
func (m *Model) finishLoginCmd(creds credentials.Credentials, access string, expiresIn int64) tea.Cmd {
	return m.withTimeout(func(ctx context.Context) tea.Msg {
		if creds.ObtainedAt.IsZero() {
			creds.ObtainedAt = time.Now()
		}
		if m.Deps.Store == nil {
			return authedMsg{err: fmt.Errorf("no credential store configured")}
		}
		if m.Deps.Tokens == nil {
			return authedMsg{err: fmt.Errorf("no token source configured")}
		}
		if err := m.Deps.Store.Save(&creds); err != nil {
			return authedMsg{err: fmt.Errorf("store credentials: %w", err)}
		}
		m.Deps.Tokens.Seed(&creds, access, expiresIn)

		msg, ok := m.fetchAccount(ctx).(authedMsg)
		if !ok || msg.err != nil {
			return msg
		}
		// Cache the account snapshot so a cold start can greet the user.
		updated := creds
		updated.AccountID = msg.id.ID
		updated.AccountName = msg.id.Name
		updated.Nick = msg.id.Nick
		if err := m.Deps.Store.Save(&updated); err != nil {
			return authedMsg{id: msg.id, acc: msg.acc, err: fmt.Errorf("store credentials: %w", err)}
		}
		// Re-seed so the token source holds the record carrying the snapshot.
		m.Deps.Tokens.Seed(&updated, m.Deps.Tokens.Access(), expiresIn)
		return msg
	})
}

// openerCommand returns the platform command that hands a URL to the browser.
func openerCommand(url string) *exec.Cmd {
	switch runtime.GOOS {
	case "darwin":
		return exec.Command("open", url) //nolint:gosec // fixed binary, user-supplied verification URL
	case "windows":
		return exec.Command("rundll32", "url.dll,FileProtocolHandler", url) //nolint:gosec
	default:
		return exec.Command("xdg-open", url) //nolint:gosec
	}
}

// letterSpace spreads a code so it reads as a code, not a word.
func letterSpace(s string) string {
	return strings.Join(strings.Split(s, ""), " ")
}

// countdown formats a remaining duration as mm:ss.
func countdown(d time.Duration) string {
	if d < 0 {
		d = 0
	}
	total := int(d.Seconds())
	return fmt.Sprintf("%02d:%02d", total/60, total%60)
}

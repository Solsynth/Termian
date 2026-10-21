package tui

import (
	"context"
	"errors"
	"fmt"
	"time"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"src.solsynth.dev/sosys/termian/internal/api"
	"src.solsynth.dev/sosys/termian/internal/credentials"
)

// homeModel is the post-login session shell.
type homeModel struct {
	width int

	identity api.Identity
	account  api.Account
	loaded   bool

	// refreshDisabled is set after a rejected rotation until the user signs in
	// again.
	refreshDisabled bool
	// refreshing suppresses duplicate rotation commands from the tick.
	refreshing bool
}

// newHomeModel builds the idle session shell.
func newHomeModel() homeModel {
	return homeModel{width: HomeCardWidth}
}

// setWidth records the card width.
func (h *homeModel) setWidth(w int) { h.width = w }

// displayName prefers the nickname, then the account name.
func (h *homeModel) displayName() string {
	switch {
	case h.account.Nick != "":
		return h.account.Nick
	case h.identity.Nick != "":
		return h.identity.Nick
	case h.account.Name != "":
		return h.account.Name
	default:
		return h.identity.Name
	}
}

// handleName is the "@name" handle.
func (h *homeModel) handleName() string {
	if h.account.Name != "" {
		return h.account.Name
	}
	return h.identity.Name
}

// accountID is the canonical account identifier.
func (h *homeModel) accountID() string {
	if h.account.ID != "" {
		return h.account.ID
	}
	return h.identity.ID
}

// homeView renders the account and session cards.
func (m *Model) homeView() string {
	h := &m.Home
	var lines []string

	lines = append(lines, m.Styles.Title.Render("Termian"))
	lines = append(lines, m.Styles.Subtitle.Render("Signed in to Solian"))
	lines = append(lines, "")

	if !h.loaded {
		lines = append(lines, m.Spinner.View()+" "+m.Styles.Muted.Render("loading account…"))
	} else {
		lines = append(lines, m.Styles.Bold.Render(h.displayName()))
		lines = append(lines, m.Styles.Muted.Render("@"+h.handleName()))
		lines = append(lines, m.Styles.Muted.Render(h.accountID()))
		lines = append(lines, m.Styles.Foreground.Render(h.profileLine()))
		lines = append(lines, "")
		lines = append(lines, m.Styles.Label.Render("browse"))
		lines = append(lines, m.Styles.Foreground.Render("f  timeline"))
		lines = append(lines, m.Styles.Foreground.Render("b  bookmarks"))
		lines = append(lines, m.Styles.Foreground.Render("c  chats"))
		lines = append(lines, m.Styles.Foreground.Render("n  new post"))
		lines = append(lines, "")
		lines = append(lines, m.sessionLines()...)
	}

	lines = append(lines, "")
	lines = append(lines, m.Styles.Hint.Render("f feed · b bookmarks · c chats · n post · r refresh · l logout · q quit"))

	return m.Styles.Card.Width(h.width).Render(lipgloss.JoinVertical(lipgloss.Left, lines...))
}

// profileLine renders level, experience and join date.
func (h *homeModel) profileLine() string {
	level, xp := 0, 0
	joined := ""
	if h.account.Profile != nil {
		level = h.account.Profile.Level
		xp = h.account.Profile.Experience
		if h.account.Profile.CreatedAt != nil {
			joined = h.account.Profile.CreatedAt.Format("2006-01-02")
		}
	}
	line := fmt.Sprintf("level %d · %d XP", level, xp)
	if joined != "" {
		line += " · joined " + joined
	}
	return line
}

// sessionLines renders the session card body.
func (m *Model) sessionLines() []string {
	h := &m.Home
	out := []string{m.Styles.Label.Render("session")}

	out = append(out, m.row("server", m.Deps.Server))

	method := credentials.MethodDevice
	if creds := m.tokenCredentials(); creds != nil && creds.Method != "" {
		method = creds.Method
	}
	out = append(out, m.row("method", method))

	storage := string(credentials.BackendKeyring)
	if m.Deps.Store != nil {
		storage = string(m.Deps.Store.Backend)
	}
	out = append(out, m.row("storage", storage))

	remaining := "unknown"
	if m.Deps.Tokens != nil {
		if until := m.Deps.Tokens.AccessExpiry(); !until.IsZero() {
			remaining = countdown(time.Until(until))
		}
	}
	if h.refreshDisabled {
		remaining = "session expired"
	}
	out = append(out, m.row("access", "expires in "+remaining))

	refreshUntil := "until rejected"
	if creds := m.tokenCredentials(); creds != nil && !creds.RefreshExpiresAt.IsZero() {
		refreshUntil = creds.RefreshExpiresAt.Format("2006-01-02 15:04")
	}
	out = append(out, m.row("refresh", refreshUntil))
	return out
}

// tokenCredentials returns the credential record backing the session.
func (m *Model) tokenCredentials() *credentials.Credentials {
	if m.Deps.Tokens == nil {
		return nil
	}
	return m.Deps.Tokens.Credentials()
}

// row renders one "label   value" line.
func (m *Model) row(label, value string) string {
	return m.Styles.Label.Width(9).Render(label) + m.Styles.Foreground.Render(value)
}

// updateHomeKey handles keys on the session shell.
func (m *Model) updateHomeKey(key tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	h := &m.Home
	switch key.String() {
	case "esc":
		// Documented as a no-op: leaving the session shell needs q or l.
		return m, nil
	case "q":
		return m, tea.Quit
	case "r":
		if h.refreshDisabled || m.Deps.Tokens == nil {
			m.setBanner("session expired — press l to sign out")
			return m, nil
		}
		if h.refreshing {
			return m, nil
		}
		h.refreshing = true
		m.clearBanner()
		return m, m.forceRefreshCmd()
	case "l":
		m.clearBanner()
		return m, m.logoutCmd()
	case "f":
		m.Feed.reset(feedTimeline)
		m.showScreen(ScreenFeed)
		return m, m.loadFeedCmd(feedTimeline, 0)
	case "b":
		if err := m.sessionGuard("bookmarks need a session"); err != nil {
			return m, nil
		}
		m.Feed.reset(feedBookmarks)
		m.showScreen(ScreenFeed)
		return m, m.loadFeedCmd(feedBookmarks, 0)
	case "c":
		if err := m.sessionGuard("chats need a session"); err != nil {
			return m, nil
		}
		m.Chats = newChatsModel()
		m.showScreen(ScreenChats)
		return m, m.loadChatsCmd()
	case "n":
		if err := m.sessionGuard("sign in to post"); err != nil {
			return m, nil
		}
		return m, m.openCompose(nil)
	}
	return m, nil
}

// updateHomeMsg handles asynchronous results on the session shell.
func (m *Model) updateHomeMsg(msg tea.Msg) (tea.Model, tea.Cmd) {
	h := &m.Home
	switch msg := msg.(type) {
	case authedMsg:
		return m.handleAuthed(msg)

	case refreshedMsg:
		h.refreshing = false
		if msg.err != nil {
			if apiIsSessionExpired(msg.err) {
				h.refreshDisabled = true
				m.setBanner("session expired — press l to sign out")
				return m, nil
			}
			m.setBanner("refresh failed: " + msg.err.Error())
			return m, nil
		}
		m.setNotice("access token refreshed")
		return m, nil

	case loggedOutMsg:
		if msg.err != nil {
			m.setBanner("logout: " + msg.err.Error())
			return m, nil
		}
		h.loaded = false
		h.refreshDisabled = false
		h.refreshing = false
		h.identity = api.Identity{}
		h.account = api.Account{}
		m.guest = false
		m.resetContentPanes()
		m.Login = newLoginModel()
		m.Login.setWidth(cardWidth(DeviceCardWidth, m.Width))
		m.Password.reset()
		m.Screen = ScreenLogin
		m.setNotice("signed out")
		return m, m.startDeviceFlowCmd()

	case homeTickMsg:
		return m.handleHomeTick()
	}
	return m, nil
}

// handleAuthed applies a loaded account snapshot and enters the home screen.
func (m *Model) handleAuthed(msg authedMsg) (tea.Model, tea.Cmd) {
	h := &m.Home
	if msg.err != nil {
		if apiIsSessionExpired(msg.err) {
			h.refreshDisabled = true
			h.loaded = false
			m.setBanner("session expired — press l to sign out")
			return m, nil
		}
		m.setBanner(msg.err.Error())
		return m, nil
	}

	h.identity = msg.id
	h.account = msg.acc
	h.loaded = true
	h.refreshDisabled = false
	m.guest = false

	m.Screen = ScreenHome
	m.clearBanner()
	return m, tea.Tick(time.Second, func(t time.Time) tea.Msg { return homeTickMsg{at: t} })
}

// handleHomeTick re-arms the countdown and renews the token near expiry.
func (m *Model) handleHomeTick() (tea.Model, tea.Cmd) {
	h := &m.Home
	cmds := []tea.Cmd{tea.Tick(time.Second, func(t time.Time) tea.Msg { return homeTickMsg{at: t} })}

	if h.refreshDisabled || h.refreshing || m.Deps.Tokens == nil {
		return m, tea.Batch(cmds...)
	}
	if until := m.Deps.Tokens.AccessExpiry(); !until.IsZero() && time.Until(until) < accessRefreshSkew {
		h.refreshing = true
		cmds = append(cmds, m.renewTokenCmd())
	}
	return m, tea.Batch(cmds...)
}

// accessRefreshSkew is how early the session shell renews the access token.
const accessRefreshSkew = 60 * time.Second

// renewTokenCmd obtains a token, rotating only when the current one is close to
// expiry (the tick's own guard lives in handleHomeTick).
func (m *Model) renewTokenCmd() tea.Cmd {
	return m.withTimeout(func(ctx context.Context) tea.Msg {
		if m.Deps.Tokens == nil {
			return refreshedMsg{err: api.ErrSessionExpired}
		}
		_, err := m.Deps.Tokens.Token(ctx)
		return refreshedMsg{at: time.Now(), err: err}
	})
}

// forceRefreshCmd rotates the access token.
func (m *Model) forceRefreshCmd() tea.Cmd {
	return m.withTimeout(func(ctx context.Context) tea.Msg {
		if m.Deps.Tokens == nil {
			return refreshedMsg{err: api.ErrSessionExpired}
		}
		return refreshedMsg{at: time.Now(), err: m.Deps.Tokens.ForceRefresh(ctx)}
	})
}

// logoutCmd revokes the session and deletes stored credentials.
func (m *Model) logoutCmd() tea.Cmd {
	return m.withTimeout(func(ctx context.Context) tea.Msg {
		if m.Deps.Tokens == nil {
			return loggedOutMsg{}
		}
		return loggedOutMsg{err: m.Deps.Tokens.Logout(ctx)}
	})
}

// apiIsSessionExpired reports whether err means "sign in again".
func apiIsSessionExpired(err error) bool {
	return errors.Is(err, api.ErrSessionExpired) ||
		api.IsCode(err, "TOKEN_EXPIRED") ||
		api.IsCode(err, "AUTH_REFRESH_FAILED")
}

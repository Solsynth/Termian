package tui

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"

	"src.solsynth.dev/sosys/termian/internal/api"
	"src.solsynth.dev/sosys/termian/internal/credentials"
)

// homeModel is the post-login session shell and profile/settings page.
type homeModel struct {
	identity api.Identity
	account  api.Account
	loaded   bool

	publishers       []api.Publisher
	publishersLoaded bool
	preferences      []api.NotificationPreference
	prefsLoaded      bool
	// setting indexes the interactive profile settings rows.
	setting int

	// refreshDisabled is set after a rejected rotation until the user signs in
	// again.
	refreshDisabled bool
	// refreshing suppresses duplicate rotation commands from the tick.
	refreshing bool
}

// newHomeModel builds the idle session shell.
func newHomeModel() homeModel {
	return homeModel{}
}

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

// homeView renders the signed-in profile, its publishers and the settings the
// terminal can change.
func (m *Model) homeView() string {
	h := &m.Home
	right := ""
	var body []string
	if !h.loaded {
		body = append(body, m.Spinner.View()+" "+m.Styles.Muted.Render("loading account…"))
		return m.pane("Profile", right, body)
	}

	right = "@" + h.handleName()
	body = append(body, m.Styles.Bold.Render(h.displayName()))
	body = append(body, m.Styles.Foreground.Render(h.profileLine()))
	body = append(body, "")
	body = append(body, m.accountLines()...)
	body = append(body, "")
	body = append(body, m.publisherLines()...)
	body = append(body, "")
	body = append(body, m.preferenceLines()...)
	body = append(body, "")
	body = append(body, m.sessionLines()...)
	body = append(body, "", m.Styles.Hint.Render("j/k select · enter change · r refresh · n new post · l sign out"))
	return m.pane("Profile", right, body)
}

// accountLines renders the immutable account facts.
func (m *Model) accountLines() []string {
	h := &m.Home
	out := []string{m.Styles.Label.Render("account")}
	if h.account.ID != "" {
		out = append(out, m.row("id", h.account.ID))
	}
	if h.identity.IsSuperuser {
		out = append(out, m.row("role", "superuser"))
	}
	if h.account.PerkLevel > 0 {
		out = append(out, m.row("perks", fmt.Sprintf("level %d", h.account.PerkLevel)))
	}
	if locale := strings.Trim(strings.Join([]string{h.identity.Language, h.identity.Region}, " · "), " ·"); locale != "" {
		out = append(out, m.row("locale", locale))
	}
	if h.account.ActivatedAt != nil {
		out = append(out, m.row("activated", h.account.ActivatedAt.Format("2006-01-02")))
	}
	return out
}

// publisherLines renders the publishers, with the composer default marked.
func (m *Model) publisherLines() []string {
	h := &m.Home
	out := []string{m.Styles.Label.Render("publishers")}
	switch {
	case !h.publishersLoaded:
		out = append(out, m.Spinner.View()+" "+m.Styles.Muted.Render("loading…"))
	case len(h.publishers) == 0:
		out = append(out, m.Styles.Muted.Render("no publishers"))
	default:
		for _, publisher := range h.publishers {
			marker := "  "
			if row, ok := m.profileSettingAt(h.setting); ok && row.publisher.Name == publisher.Name && publisher.Name != "" {
				marker = m.Styles.Accent.Render("▸ ")
			}
			star := "  "
			if publisher.Name == m.defaultPublisher {
				star = m.Styles.Notice.Render("★ ")
			}
			line := marker + star + m.Styles.Foreground.Render("@"+publisher.Handle())
			if publisher.Display() != publisher.Handle() {
				line += " " + m.Styles.Muted.Render(publisher.Display())
			}
			line += " " + m.Styles.Muted.Render("("+publisher.KindLabel()+")")
			out = append(out, line)
		}
	}
	return out
}

// preferenceLines renders the notification topics with their delivery level.
func (m *Model) preferenceLines() []string {
	h := &m.Home
	out := []string{m.Styles.Label.Render("notification settings")}
	if !h.prefsLoaded {
		out = append(out, m.Spinner.View()+" "+m.Styles.Muted.Render("loading…"))
		return out
	}
	rows := m.profileSettings()
	start, end := scrollWindow(h.setting, h.setting-2, len(rows), 5)
	for i := start; i < end; i++ {
		row := rows[i]
		if row.kind != settingTopic {
			continue
		}
		marker := "  "
		if i == h.setting {
			marker = m.Styles.Accent.Render("▸ ")
		}
		level := m.preferenceLevel(row.topic.Topic)
		levelStyle := m.Styles.Foreground
		switch level {
		case api.NotificationPreferenceSilent:
			levelStyle = m.Styles.Muted
		case api.NotificationPreferenceReject:
			levelStyle = m.Styles.Error
		}
		line := marker + m.Styles.Foreground.Render(truncate(row.topic.Description, 30))
		line += "  " + levelStyle.Render(api.NotificationPreferenceLabel(level))
		out = append(out, line)
	}
	return out
}

// settingKind distinguishes the two interactive setting families.
type settingKind int

const (
	settingPublisher settingKind = iota
	settingTopic
)

// profileSetting is one interactive row on the profile page.
type profileSetting struct {
	kind      settingKind
	publisher api.Publisher
	topic     api.NotificationTopic
}

// profileSettings lists publishers first, then notification topics.
func (m *Model) profileSettings() []profileSetting {
	rows := make([]profileSetting, 0, len(m.Home.publishers)+len(api.DefaultNotificationTopics))
	for _, publisher := range m.Home.publishers {
		rows = append(rows, profileSetting{kind: settingPublisher, publisher: publisher})
	}
	for _, topic := range api.DefaultNotificationTopics {
		rows = append(rows, profileSetting{kind: settingTopic, topic: topic})
	}
	return rows
}

// profileSettingAt returns the setting at an index.
func (m *Model) profileSettingAt(index int) (profileSetting, bool) {
	rows := m.profileSettings()
	if index < 0 || index >= len(rows) {
		return profileSetting{}, false
	}
	return rows[index], true
}

// preferenceLevel reads the local delivery level for a topic.
func (m *Model) preferenceLevel(topic string) int {
	for _, pref := range m.Home.preferences {
		if pref.Topic == topic {
			return pref.Preference
		}
	}
	return api.NotificationPreferenceNormal
}

// profileExtrasMsg carries the publishers and notification preferences.
type profileExtrasMsg struct {
	publishers  []api.Publisher
	preferences []api.NotificationPreference
	err         error
}

// loadProfileExtrasCmd loads the settings data shown on the profile page.
func (m *Model) loadProfileExtrasCmd() tea.Cmd {
	return m.withTimeout(func(ctx context.Context) tea.Msg {
		if m.Deps.Content == nil {
			return profileExtrasMsg{}
		}
		if err := m.requireSession(ctx); err != nil {
			return profileExtrasMsg{err: err}
		}
		publishers, err := m.Deps.Content.Publishers(ctx)
		if err != nil {
			return profileExtrasMsg{err: err}
		}
		preferences, err := m.Deps.Content.NotificationPreferences(ctx)
		return profileExtrasMsg{publishers: publishers, preferences: preferences, err: err}
	})
}

// preferenceWrittenMsg reports a preference write.
type preferenceWrittenMsg struct {
	topic string
	level int
	err   error
}

// setPreferenceCmd writes a topic's delivery level.
func (m *Model) setPreferenceCmd(topic string, level int) tea.Cmd {
	return m.withTimeout(func(ctx context.Context) tea.Msg {
		if err := m.requireSession(ctx); err != nil {
			return preferenceWrittenMsg{topic: topic, level: level, err: err}
		}
		return preferenceWrittenMsg{topic: topic, level: level,
			err: m.Deps.Content.SetNotificationPreference(ctx, topic, level)}
	})
}

// applyProfileExtras stores the loaded settings data.
func (m *Model) applyProfileExtras(msg profileExtrasMsg) {
	if msg.err != nil {
		m.Home.publishersLoaded = true
		m.Home.prefsLoaded = true
		m.reportContentError("profile", msg.err)
		return
	}
	m.Home.publishers = msg.publishers
	m.Home.publishersLoaded = true
	if m.defaultPublisher == "" && len(msg.publishers) > 0 {
		m.defaultPublisher = msg.publishers[0].Name
	}
	m.Home.preferences = msg.preferences
	m.Home.prefsLoaded = true
	m.Home.setting = min(m.Home.setting, max(len(m.profileSettings())-1, 0))
}

// applyPreferenceWritten records a successful preference write.
func (m *Model) applyPreferenceWritten(msg preferenceWrittenMsg) {
	if msg.err != nil {
		m.reportContentError("notification settings", msg.err)
		return
	}
	found := false
	for i := range m.Home.preferences {
		if m.Home.preferences[i].Topic == msg.topic {
			m.Home.preferences[i].Preference = msg.level
			found = true
		}
	}
	if !found {
		m.Home.preferences = append(m.Home.preferences, api.NotificationPreference{Topic: msg.topic, Preference: msg.level})
	}
	m.setNotice(fmt.Sprintf("%s notifications: %s", msg.topic, api.NotificationPreferenceLabel(msg.level)))
}

// activateProfileSetting applies the highlighted setting.
func (m *Model) activateProfileSetting() tea.Cmd {
	row, ok := m.profileSettingAt(m.Home.setting)
	if !ok {
		return nil
	}
	switch row.kind {
	case settingPublisher:
		m.defaultPublisher = row.publisher.Name
		m.setNotice("composer publisher: @" + row.publisher.Handle())
		return nil
	case settingTopic:
		level := (m.preferenceLevel(row.topic.Topic) + 1) % 3
		if err := m.sessionGuard("sign in to change notification settings"); err != nil {
			return nil
		}
		return m.setPreferenceCmd(row.topic.Topic, level)
	}
	return nil
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
	case "j", "down":
		if h.setting < len(m.profileSettings())-1 {
			h.setting++
		}
		return m, nil
	case "k", "up":
		if h.setting > 0 {
			h.setting--
		}
		return m, nil
	case "g":
		h.setting = 0
		return m, nil
	case "G":
		h.setting = max(len(m.profileSettings())-1, 0)
		return m, nil
	case "enter":
		return m, m.activateProfileSetting()
	case "r":
		if h.refreshDisabled || m.Deps.Tokens == nil {
			m.setBanner("session expired — press l to sign out")
			return m, nil
		}
		h.publishersLoaded = false
		h.prefsLoaded = false
		m.clearBanner()
		return m, tea.Batch(m.fetchAccountCmd(), m.loadProfileExtrasCmd(), m.notificationBadgeCmd())
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
	if m.defaultPublisher == "" && len(m.Home.publishers) > 0 {
		m.defaultPublisher = m.Home.publishers[0].Name
	}
	return m, tea.Batch(
		tea.Tick(time.Second, func(t time.Time) tea.Msg { return homeTickMsg{at: t} }),
		m.ensureRealtime(),
		m.loadProfileExtrasCmd(),
		m.notificationBadgeCmd(),
	)
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

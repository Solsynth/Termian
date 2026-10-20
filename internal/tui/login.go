package tui

import (
	"time"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"src.solsynth.dev/sosys/termian/internal/api"
	"src.solsynth.dev/sosys/termian/internal/credentials"
)

// loginModel is the device-flow pane state.
type loginModel struct {
	code     api.DeviceCode
	hasCode  bool
	deadline time.Time
	interval time.Duration
	// finished stops polling after a terminal device error.
	finished bool
	width    int
}

// newLoginModel builds the initial device pane.
func newLoginModel() loginModel {
	return loginModel{width: DeviceCardWidth, interval: 5 * time.Second}
}

// setWidth records the card width.
func (l *loginModel) setWidth(w int) { l.width = w }

// loginView renders the device card.
func (m *Model) loginView() string {
	l := &m.Login
	var lines []string

	lines = append(lines, m.Styles.Title.Render("Termian"))
	lines = append(lines, m.Styles.Subtitle.Render("Sign in to Solian"))
	lines = append(lines, "")

	switch {
	case !l.hasCode && m.Banner != "":
		lines = append(lines, m.Styles.Muted.Render("preparing device authorization…"))
	case !l.hasCode:
		lines = append(lines, m.Spinner.View()+" "+m.Styles.Muted.Render("requesting a device code…"))
	default:
		lines = append(lines, m.Styles.Label.Render("your code"))
		lines = append(lines, m.Styles.UserCode.Render(letterSpace(l.code.UserCode)))
		lines = append(lines, m.Styles.Foreground.Render(l.code.VerificationURI))
		lines = append(lines, "")
		remaining := time.Until(l.deadline)
		status := m.Spinner.View() + " " + m.Styles.Muted.Render("waiting for approval…")
		if l.finished {
			status = m.Styles.Muted.Render("stopped")
		}
		lines = append(lines, status+"  "+m.Styles.Muted.Render(countdown(remaining)))
	}

	lines = append(lines, "")
	lines = append(lines, m.Styles.Hint.Render("c copy code · o open browser · p password login · esc quit"))

	card := m.Styles.Card.Width(l.width).Render(lipgloss.JoinVertical(lipgloss.Left, lines...))
	return card
}

// updateLoginKey handles keys on the device pane.
func (m *Model) updateLoginKey(key tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	switch key.String() {
	case "p", "b":
		m.showScreen(ScreenPassword)
		m.Password.reset()
		return m, nil
	case "esc":
		return m, tea.Quit
	case "c":
		if !m.Login.hasCode {
			return m, nil
		}
		m.setNotice("code copied to the clipboard")
		return m, tea.SetClipboard(m.Login.code.UserCode)
	case "o":
		if !m.Login.hasCode {
			return m, nil
		}
		url := m.Login.code.VerificationURIComplete
		if url == "" {
			url = m.Login.code.VerificationURI
		}
		cmd := openerCommand(url)
		return m, tea.ExecProcess(cmd, func(err error) tea.Msg { return browserDoneMsg{err: err} })
	}
	return m, nil
}

// updateLoginMsg handles asynchronous results on the device pane.
func (m *Model) updateLoginMsg(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case deviceCodeMsg:
		if msg.err != nil {
			m.setBanner("could not start device login: " + msg.err.Error())
			m.Login.finished = true
			return m, nil
		}
		m.Login.code = msg.code
		m.Login.hasCode = true
		m.Login.finished = false
		m.Login.deadline = time.Now().Add(time.Duration(msg.code.ExpiresIn) * time.Second)
		m.Login.interval = m.deviceInterval(msg.code.Interval)
		m.clearBanner()
		return m, m.schedulePoll(m.Login.interval)

	case pollDueMsg:
		if !m.Login.hasCode || m.Login.finished {
			return m, nil
		}
		return m, m.pollDeviceTokenCmd(m.Login.code.DeviceCode)

	case deviceTokenMsg:
		return m.handleDeviceToken(msg)

	case browserDoneMsg:
		if msg.err != nil {
			m.setBanner("could not open the browser: " + msg.err.Error())
		}
		return m, nil
	}
	return m, nil
}

// deviceInterval resolves the poll interval, honouring test overrides.
func (m *Model) deviceInterval(serverSeconds int) time.Duration {
	if m.Deps.PollInterval > 0 {
		return m.Deps.PollInterval
	}
	if serverSeconds <= 0 {
		return 5 * time.Second
	}
	return time.Duration(serverSeconds) * time.Second
}

// handleDeviceToken advances the poll loop after one token response.
func (m *Model) handleDeviceToken(msg deviceTokenMsg) (tea.Model, tea.Cmd) {
	if msg.err != nil {
		switch {
		case api.IsDeviceCode(msg.err, api.DeviceAuthorizationPending):
			return m, m.schedulePoll(m.Login.interval)
		case api.IsDeviceCode(msg.err, api.DeviceSlowDown):
			m.Login.interval += 5 * time.Second
			return m, m.schedulePoll(m.Login.interval)
		default:
			m.Login.finished = true
			m.setBanner(deviceErrorMessage(msg.err))
			return m, nil
		}
	}

	creds := credentials.Credentials{
		Server:       m.Deps.Server,
		Method:       credentials.MethodDevice,
		ClientID:     m.Deps.ClientID,
		DeviceID:     m.Deps.DeviceID,
		RefreshToken: msg.tok.RefreshToken,
	}
	return m, m.finishLoginCmd(creds, msg.tok.AccessToken, msg.tok.ExpiresIn)
}

// deviceErrorMessage renders a terminal device-flow failure.
func deviceErrorMessage(err error) string {
	switch {
	case api.IsDeviceCode(err, api.DeviceAccessDenied):
		return "sign-in was denied"
	case api.IsDeviceCode(err, api.DeviceExpiredToken),
		api.IsDeviceCode(err, api.DeviceInvalidGrant):
		return "the device code expired — press p for password login"
	}
	return "device login failed: " + err.Error()
}

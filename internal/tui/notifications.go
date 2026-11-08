package tui

import (
	"fmt"
	"strings"

	tea "charm.land/bubbletea/v2"
)

// notificationsModel manages notification state.
type notificationsModel struct {
	selected int
}

// newNotificationsModel creates an empty notifications model.
func newNotificationsModel() notificationsModel {
	return notificationsModel{}
}

// notificationsView renders the dedicated notifications full-screen view.
func (m *Model) notificationsView() string {
	width := m.contentWidth()
	height := m.bodyHeight()

	var body []string
	head := m.Styles.Title.Render("Notifications")
	right := m.Styles.Muted.Render("Activity Stream")
	gap := width - 13 - 15
	if gap < 1 {
		gap = 1
	}
	subHeader := head + strings.Repeat(" ", gap) + right
	body = append(body, subHeader, "")

	if m.guest {
		body = append(body, m.Styles.Muted.Render("Notifications require an active session — press [g] to sign in."))
		return clipOrPad(body, height)
	}

	unread := m.totalUnread()
	if unread > 0 {
		badge := m.Styles.Accent.Render(fmt.Sprintf("● You have %d unread chat message(s).", unread))
		body = append(body, badge)
		body = append(body, m.Styles.Foreground.Render("Press [2] to jump to Realtime Chat."))
		body = append(body, "")
	}

	body = append(body, m.Styles.Bold.Render("Recent Alerts & Mentions"))
	body = append(body, m.Styles.Muted.Render("No new mentions or notifications."))
	body = append(body, "")
	body = append(body, m.Styles.Muted.Render("All systems operational."))
	body = append(body, "")
	body = append(body, m.Styles.Hint.Render("[1] Posts  [2] Chat  [4] Profile  [/] Search  [q] Quit"))

	return clipOrPad(body, height)
}

// updateNotificationsKey handles keys on the notifications screen.
func (m *Model) updateNotificationsKey(key tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	switch key.String() {
	case "esc":
		m.showScreen(ScreenFeed)
		return m, nil
	case "j", "down":
		return m, nil
	case "k", "up":
		return m, nil
	}
	return m, nil
}

// clipOrPad trims or pads lines to target height.
func clipOrPad(lines []string, target int) string {
	if len(lines) > target {
		lines = lines[:target]
	}
	return strings.Join(lines, "\n")
}

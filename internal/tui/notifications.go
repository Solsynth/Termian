package tui

import (
	"context"
	"fmt"
	"net/url"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"src.solsynth.dev/sosys/termian/internal/api"
)

// notificationPageSize is how many notifications one fetch returns.
const notificationPageSize = 40

// notificationsModel is the Ring activity stream.
type notificationsModel struct {
	items    []api.Notification
	selected int
	loaded   bool
	loading  bool
	unread   int
}

// newNotificationsModel builds an empty stream.
func newNotificationsModel() notificationsModel { return notificationsModel{} }

// selectedNotification returns the highlighted notification.
func (n *notificationsModel) selectedNotification() *api.Notification {
	if n.selected < 0 || n.selected >= len(n.items) {
		return nil
	}
	return &n.items[n.selected]
}

// notificationsLoadedMsg carries one page of the stream.
type notificationsLoadedMsg struct {
	page api.Page[api.Notification]
	err  error
}

// loadNotificationsCmd fetches the newest notifications and the unread total.
func (m *Model) loadNotificationsCmd() tea.Cmd {
	return m.withTimeout(func(ctx context.Context) tea.Msg {
		if m.Deps.Content == nil {
			return notificationsLoadedMsg{err: nil}
		}
		if err := m.requireSession(ctx); err != nil {
			return notificationsLoadedMsg{err: err}
		}
		page, err := m.Deps.Content.Notifications(ctx, 0, notificationPageSize)
		return notificationsLoadedMsg{page: page, err: err}
	})
}

// notificationReadMsg reports a single read receipt.
type notificationReadMsg struct {
	id  string
	err error
}

// markNotificationReadCmd marks one notification viewed.
func (m *Model) markNotificationReadCmd(id string) tea.Cmd {
	return m.withTimeout(func(ctx context.Context) tea.Msg {
		if err := m.requireSession(ctx); err != nil {
			return notificationReadMsg{id: id, err: err}
		}
		return notificationReadMsg{id: id, err: m.Deps.Content.MarkNotificationRead(ctx, id)}
	})
}

// notificationAllReadMsg reports mark-all progress.
type notificationAllReadMsg struct {
	err error
}

// markAllNotificationsReadCmd marks every notification viewed.
func (m *Model) markAllNotificationsReadCmd() tea.Cmd {
	return m.withTimeout(func(ctx context.Context) tea.Msg {
		if err := m.requireSession(ctx); err != nil {
			return notificationAllReadMsg{err: err}
		}
		return notificationAllReadMsg{err: m.Deps.Content.MarkAllNotificationsRead(ctx)}
	})
}

// notificationDeletedMsg reports one removal.
type notificationDeletedMsg struct {
	id  string
	err error
}

// deleteNotificationCmd removes one notification.
func (m *Model) deleteNotificationCmd(id string) tea.Cmd {
	return m.withTimeout(func(ctx context.Context) tea.Msg {
		if err := m.requireSession(ctx); err != nil {
			return notificationDeletedMsg{id: id, err: err}
		}
		return notificationDeletedMsg{id: id, err: m.Deps.Content.DeleteNotification(ctx, id)}
	})
}

// notificationsView renders the activity stream.
func (m *Model) notificationsView() string {
	n := &m.Notifications
	width := m.contentWidth()
	height := m.bodyHeight()

	right := fmt.Sprintf("%d unread", n.unread)
	if len(n.items) > 0 {
		right += fmt.Sprintf(" · %d shown", len(n.items))
	}
	if m.guest {
		return m.pane("Notifications", "activity stream",
			[]string{m.Styles.Muted.Render("Notifications require a session — press [g] to sign in.")})
	}
	if !n.loaded {
		return m.pane("Notifications", "activity stream",
			[]string{m.Spinner.View() + " " + m.Styles.Muted.Render("loading activity…")})
	}
	if len(n.items) == 0 {
		return m.pane("Notifications", right, []string{
			m.Styles.Muted.Render("No notifications yet."),
			"",
			m.Styles.Muted.Render("Mentions, replies, reactions and account activity land here."),
		})
	}

	cards := make([][]string, len(n.items))
	for i, item := range n.items {
		cards[i] = m.renderNotificationCard(item, width, i == n.selected)
	}
	var body []string
	for _, i := range visibleCards(cards, n.selected, height) {
		body = append(body, cards[i]...)
	}
	return m.pane("Notifications", right, body)
}

// renderNotificationCard renders one notification as a compact card.
func (m *Model) renderNotificationCard(item api.Notification, width int, selected bool) []string {
	marker := "  "
	if selected {
		marker = m.Styles.Accent.Render("▸ ")
	}
	unread := "  "
	if item.Unread() {
		unread = m.Styles.Accent.Render("● ")
	}

	right := ago(item.CreatedAt)
	if item.Topic != "" {
		right = notificationTopicLabel(item.Topic) + " · " + right
	}
	titleWidth := max(width-8-ansi.StringWidth(right), 8)
	title := truncate(item.Title, titleWidth)
	gap := width - 4 - ansi.StringWidth(title) - ansi.StringWidth(right)
	if gap < 1 {
		gap = 1
	}
	head := marker + unread + m.Styles.Bold.Render(title)
	head += strings.Repeat(" ", gap) + m.Styles.Muted.Render(right)

	out := []string{head}
	if item.Subtitle != "" {
		out = append(out, "    "+m.Styles.Muted.Render(truncate(item.Subtitle, width-4)))
	}
	if body := oneLineText(item.Body()); body != "" {
		wrapped := wrapText(body, width-4)
		if len(wrapped) > 2 {
			wrapped = append(wrapped[:2], m.Styles.Muted.Render("…"))
		}
		for _, line := range wrapped {
			out = append(out, "    "+line)
		}
	}
	if uri := item.ActionURI(); uri != "" {
		out = append(out, "    "+m.Styles.Hint.Render("↗ "+truncate(uri, width-8)))
	}
	out = append(out, "")
	return out
}

// notificationTopicLabel maps a Ring topic onto a short label.
func notificationTopicLabel(topic string) string {
	switch {
	case strings.Contains(topic, "mention"):
		return "mention"
	case strings.Contains(topic, "reaction"):
		return "reaction"
	case strings.Contains(topic, "repl"):
		return "reply"
	case strings.Contains(topic, "award"):
		return "award"
	case strings.Contains(topic, "message"):
		return "message"
	case strings.Contains(topic, "wallet"), strings.Contains(topic, "subscription"), strings.Contains(topic, "order"):
		return "billing"
	case strings.Contains(topic, "auth"):
		return "security"
	case strings.Contains(topic, "invite"):
		return "invite"
	default:
		if topic == "" {
			return "activity"
		}
		return topic
	}
}

// updateNotificationKey handles keys on the activity stream.
func (m *Model) updateNotificationKey(key tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	n := &m.Notifications
	switch key.String() {
	case "esc":
		m.showScreen(ScreenFeed)
		return m, nil
	case "j", "down":
		if n.selected < len(n.items)-1 {
			n.selected++
		}
		return m, nil
	case "k", "up":
		if n.selected > 0 {
			n.selected--
		}
		return m, nil
	case "g":
		if m.guest {
			return m, m.leaveGuest()
		}
		n.selected = 0
		return m, nil
	case "G":
		if len(n.items) > 0 {
			n.selected = len(n.items) - 1
		}
		return m, nil
	case "R":
		n.loaded = false
		return m, m.loadNotificationsCmd()
	case "a":
		if err := m.sessionGuard("sign in to manage notifications"); err != nil {
			return m, nil
		}
		if n.unread == 0 {
			m.setNotice("nothing unread")
			return m, nil
		}
		return m, m.markAllNotificationsReadCmd()
	case "d":
		item := n.selectedNotification()
		if item == nil {
			return m, nil
		}
		if err := m.sessionGuard("sign in to manage notifications"); err != nil {
			return m, nil
		}
		return m, m.deleteNotificationCmd(item.ID)
	case "enter", "o":
		item := n.selectedNotification()
		if item == nil {
			return m, nil
		}
		cmds := []tea.Cmd{}
		if item.Unread() {
			cmds = append(cmds, m.markNotificationReadCmd(item.ID))
		}
		cmd, handled := m.openNotificationTarget(*item)
		if handled {
			cmds = append(cmds, cmd)
		}
		return m, tea.Batch(cmds...)
	}
	return m, nil
}

// openNotificationTarget follows a notification's action URI: post links open
// the post pane, everything else goes to the browser.
func (m *Model) openNotificationTarget(item api.Notification) (tea.Cmd, bool) {
	uri := item.ActionURI()
	if uri == "" {
		return nil, false
	}
	if id := postIDFromActionURI(uri); id != "" {
		return func() tea.Msg { return SwitchScreenMsg{Target: ScreenPost, Payload: id} }, true
	}
	parsed, err := url.Parse(uri)
	if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") {
		return nil, false
	}
	return tea.ExecProcess(openerCommand(uri), func(err error) tea.Msg { return browserDoneMsg{err: err} }), true
}

// postIDFromActionURI extracts a post ID from a Solian deep link.
func postIDFromActionURI(raw string) string {
	for _, marker := range []string{"/posts/", "posts/"} {
		idx := strings.LastIndex(raw, marker)
		if idx < 0 {
			continue
		}
		rest := raw[idx+len(marker):]
		if cut := strings.IndexAny(rest, "?#/"); cut >= 0 {
			rest = rest[:cut]
		}
		if rest != "" {
			return rest
		}
	}
	return ""
}

// applyNotificationsLoaded stores a fetched page.
func (m *Model) applyNotificationsLoaded(msg notificationsLoadedMsg) {
	n := &m.Notifications
	n.loading = false
	n.loaded = true
	if msg.err != nil {
		m.reportContentError("notifications", msg.err)
		return
	}
	n.items = msg.page.Items
	n.unread = 0
	for _, item := range n.items {
		if item.Unread() {
			n.unread++
		}
	}
	m.notificationsUnread = n.unread
	n.selected = min(n.selected, max(len(n.items)-1, 0))
	m.clearBanner()
}

// applyNotificationRead records a read receipt and moves the unread badge.
func (m *Model) applyNotificationRead(msg notificationReadMsg) {
	if msg.err != nil {
		m.reportContentError("notification", msg.err)
		return
	}
	for i := range m.Notifications.items {
		item := &m.Notifications.items[i]
		if item.ID == msg.id && item.Unread() {
			now := time.Now()
			item.ViewedAt = &now
			m.Notifications.unread = max(m.Notifications.unread-1, 0)
			m.notificationsUnread = max(m.notificationsUnread-1, 0)
		}
	}
}

// applyAllNotificationsRead clears the unread markers.
func (m *Model) applyAllNotificationsRead(msg notificationAllReadMsg) {
	if msg.err != nil {
		m.reportContentError("notifications", msg.err)
		return
	}
	now := time.Now()
	for i := range m.Notifications.items {
		if m.Notifications.items[i].Unread() {
			m.Notifications.items[i].ViewedAt = &now
		}
	}
	m.Notifications.unread = 0
	m.notificationsUnread = 0
	m.setNotice("all notifications read")
}

// applyNotificationDeleted drops a removed notification.
func (m *Model) applyNotificationDeleted(msg notificationDeletedMsg) {
	if msg.err != nil {
		m.reportContentError("delete notification", msg.err)
		return
	}
	n := &m.Notifications
	kept := n.items[:0]
	removedUnread := false
	for _, item := range n.items {
		if item.ID == msg.id {
			removedUnread = item.Unread()
			continue
		}
		kept = append(kept, item)
	}
	n.items = kept
	if removedUnread {
		n.unread = max(n.unread-1, 0)
		m.notificationsUnread = max(m.notificationsUnread-1, 0)
	}
	n.selected = min(n.selected, max(len(n.items)-1, 0))
	m.setNotice("notification deleted")
}

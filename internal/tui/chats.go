package tui

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"src.solsynth.dev/sosys/termian/internal/api"
)

// chatsModel is the chat room list pane.
type chatsModel struct {
	rooms    []api.ChatRoom
	summary  map[string]api.ChatSummary
	selected int
	loaded   bool
	// focused reports whether the conversation column owns the keys; the room
	// column is the default so j/k always walks the list first.
	focused bool
}

// newChatsModel builds an empty room list.
func newChatsModel() chatsModel { return chatsModel{} }

// selectedRoom returns the highlighted room.
func (c *chatsModel) selectedRoom() *api.ChatRoom {
	if c.selected < 0 || c.selected >= len(c.rooms) {
		return nil
	}
	return &c.rooms[c.selected]
}

// unread returns the unread count of a room.
func (c *chatsModel) unread(id string) int {
	if summary, ok := c.summary[id]; ok {
		return summary.UnreadCount
	}
	return 0
}

// roomsLoadedMsg carries the room list and its unread summary.
type roomsLoadedMsg struct {
	rooms   api.ChatRooms
	summary map[string]api.ChatSummary
	err     error
}

// loadChatsCmd fetches the room list.
func (m *Model) loadChatsCmd() tea.Cmd {
	return m.withTimeout(func(ctx context.Context) tea.Msg {
		if m.Deps.Content == nil {
			return roomsLoadedMsg{err: fmt.Errorf("no content client configured")}
		}
		if err := m.requireSession(ctx); err != nil {
			return roomsLoadedMsg{err: err}
		}
		rooms, err := m.Deps.Content.ChatRoomList(ctx, 0, 50)
		if err != nil {
			return roomsLoadedMsg{err: err}
		}
		// The summary is decoration: a failure must not hide the rooms.
		summary, _ := m.Deps.Content.ChatSummaryByRoom(ctx)
		return roomsLoadedMsg{rooms: rooms, summary: summary}
	})
}

// chatsView renders the two-column chat workspace: rooms on the left, the
// selected conversation on the right. Opening a room keeps the list visible,
// which is how the Solian app lays chat out.
func (m *Model) chatsView() string {
	c := &m.Chats
	height := m.bodyHeight()
	leftWidth := m.listWidth()
	rowsHeight := max(1, height-1)

	var left []string
	switch {
	case !c.loaded:
		left = append(left, m.Spinner.View()+" "+m.Styles.Muted.Render("loading rooms…"))
	case len(c.rooms) == 0:
		left = append(left, m.Styles.Muted.Render("no chat rooms yet"))
	default:
		// Two lines per room: the header row and the preview row.
		visible := max(rowsHeight/2, 1)
		start, end := scrollWindow(c.selected, c.selected-visible/2, len(c.rooms), visible)
		for i := start; i < end; i++ {
			left = append(left, m.renderRoomRow(c.rooms[i], leftWidth, i == c.selected)...)
		}
	}

	total := 0
	for _, summary := range c.summary {
		total += summary.UnreadCount
	}
	right := plural(len(c.rooms), "room", "rooms")
	if total > 0 {
		right += fmt.Sprintf(" · %d unread", total)
	}
	title := "Chats"
	if m.Chat.room.ID != "" {
		title = "#" + m.Chats.title(m.Chat.room)
	}
	return m.splitPane(title, right, left, m.chatColumn(rowsHeight), m.chatsHint())
}

// listWidth is the width of the room column in the chat workspace.
func (m *Model) listWidth() int {
	width := m.contentWidth()
	list := max(24, min(width*2/5, 44))
	return list
}

// chatColumn renders the right-hand side of the chat workspace: the open
// conversation, or a prompt when no room is selected.
func (m *Model) chatColumn(height int) []string {
	c := &m.Chat
	if c.room.ID == "" {
		return []string{
			m.Styles.Muted.Render("select a room with enter"),
			"",
			m.Styles.Hint.Render("j/k move · enter open"),
		}
	}

	composer := 0
	if c.typing {
		composer = 3
	} else if c.lastErr != "" {
		composer = 2
	}
	logHeight := max(height-composer, 3)

	var out []string
	switch {
	case !c.loaded:
		out = append(out, m.Spinner.View()+" "+m.Styles.Muted.Render("loading messages…"))
	case len(c.messages) == 0:
		out = append(out, m.Styles.Muted.Render("no messages yet — press i to write"))
	default:
		log := m.renderLog(m.detailWidth(), logHeight)
		// A transcript hangs from the bottom: pad above a short log.
		if pad := logHeight - len(log); pad > 0 {
			out = append(out, make([]string, pad)...)
		}
		out = append(out, log...)
	}

	if c.typing {
		out = append(out, m.Styles.Label.Render("message"), c.input.View())
		if c.sending {
			out = append(out, m.Spinner.View()+" "+m.Styles.Muted.Render("sending…"))
		}
	} else if c.lastErr != "" {
		out = append(out, "", m.Styles.Error.Render(c.lastErr))
	}
	return out
}

// detailWidth is the width of the conversation column.
func (m *Model) detailWidth() int {
	width := m.contentWidth() - m.listWidth() - 3
	return max(width, 20)
}

// renderRoomRow renders one room as two lines.
func (m *Model) renderRoomRow(room api.ChatRoom, width int, selected bool) []string {
	marker := "  "
	if selected {
		marker = m.Styles.Accent.Render("▸ ")
	}

	summary, hasSummary := m.Chats.summary[room.ID]
	title := m.Chats.title(room)

	badge := ""
	if summary.UnreadCount > 0 {
		badge = fmt.Sprintf(" (%d)", summary.UnreadCount)
	}
	kind := roomKind(room)

	// The header row is marker + title + badge, then the right-aligned kind.
	right := ansi.StringWidth(kind) + 1
	nameWidth := max(width-2-right-ansi.StringWidth(badge), 8)
	title = truncate(title, nameWidth)
	gap := width - 2 - ansi.StringWidth(title) - ansi.StringWidth(badge) - ansi.StringWidth(kind)
	if gap < 1 {
		gap = 1
	}
	head := marker + m.Styles.Bold.Render(title)
	if badge != "" {
		head += m.Styles.Accent.Render(badge)
	}
	head += strings.Repeat(" ", gap) + m.Styles.Muted.Render(kind)

	preview := ""
	if hasSummary && summary.LastMessage != nil {
		last := *summary.LastMessage
		text := strings.ReplaceAll(last.Text(), "\n", " ")
		if text == "" && len(last.Attachments) > 0 {
			text = "(" + plural(len(last.Attachments), "attachment", "attachments") + ")"
		}
		preview = text
		if !room.IsDirect() {
			preview = last.Sender.DisplayName() + ": " + preview
		}
	}
	if preview == "" && room.Description != nil {
		preview = strings.ReplaceAll(*room.Description, "\n", " ")
	}
	if preview == "" {
		preview = "no messages yet"
	}
	line := "  " + m.Styles.Muted.Render(truncate(preview, max(width-4, 10)))
	return []string{head, line}
}

// title names a room. Direct rooms are named after the other participant,
// which the unread summary reports as the last sender. Rooms without a summary
// keep the server name, which is empty for a fresh direct chat.
func (c *chatsModel) title(room api.ChatRoom) string {
	if !room.IsDirect() {
		return room.Title()
	}
	if summary, ok := c.summary[room.ID]; ok && summary.LastMessage != nil {
		if name := summary.LastMessage.Sender.DisplayName(); name != "" && name != "unknown" {
			return name
		}
	}
	if room.Name != nil && *room.Name != "" {
		return *room.Name
	}
	return "direct message"
}

// sortByActivity orders rooms by their newest message, then by name.
func (c *chatsModel) sortByActivity() {
	sort.SliceStable(c.rooms, func(i, j int) bool {
		left, right := c.rooms[i], c.rooms[j]
		lt, rt := time.Time{}, time.Time{}
		if summary, ok := c.summary[left.ID]; ok && summary.LastMessage != nil {
			lt = summary.LastMessage.CreatedAt
		}
		if summary, ok := c.summary[right.ID]; ok && summary.LastMessage != nil {
			rt = summary.LastMessage.CreatedAt
		}
		if !lt.Equal(rt) {
			return lt.After(rt)
		}
		return left.Title() < right.Title()
	})
}

// chatsHint is the key legend of the chat workspace.
func (m *Model) chatsHint() string {
	if m.guest {
		return "chats need a session — g sign in · tab sections · esc back"
	}
	if m.Chat.typing {
		return "enter send · alt+enter newline · esc cancel"
	}
	if m.Chats.focused {
		return m.chatHint()
	}
	return "j/k rooms · enter conversation · i write · tab sections · R reload · esc back"
}

// openRoom loads the highlighted room into the conversation column. Reopening
// the room that is already showing keeps its scrollback instead of reloading.
func (m *Model) openRoom() tea.Cmd {
	room := m.Chats.selectedRoom()
	if room == nil {
		return nil
	}
	if m.Chat.room.ID == room.ID && m.Chat.loaded {
		return nil
	}
	limit := m.Deps.ChatHistory
	m.Chat = newChatModel()
	m.Chat.setWidth(m.detailWidth())
	m.Chat.historyLimit = limit
	m.Chat.room = *room
	m.showScreen(ScreenChats)
	m.Chats.focused = false
	return m.loadMessagesCmd(room.ID)
}

// updateChatsMsg applies asynchronous room list results.
func (m *Model) updateChatsMsg(msg tea.Msg) (tea.Model, tea.Cmd) {
	c := &m.Chats
	switch msg := msg.(type) {
	case roomsLoadedMsg:
		if msg.err != nil {
			c.loaded = true
			if apiIsSessionExpired(msg.err) || isNoCredentials(msg.err) {
				m.setBanner("chats need a session — press g to sign in")
				return m, nil
			}
			m.setBanner("chats: " + msg.err.Error())
			return m, nil
		}
		c.loaded = true
		c.rooms = msg.rooms.Rooms
		c.summary = msg.summary
		c.sortByActivity()
		c.selected = min(c.selected, max(len(c.rooms)-1, 0))
		m.clearBanner()
		// Open the first room so the workspace is never half empty.
		if m.Chat.room.ID == "" && len(c.rooms) > 0 {
			c.selected = 0
			return m, m.openRoom()
		}
		return m, nil
	}
	return m, nil
}

// roomKind names the room type in one word.
func roomKind(room api.ChatRoom) string {
	switch {
	case room.IsCommunity:
		return "community"
	case room.IsDirect():
		return "direct"
	default:
		return "group"
	}
}

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

// chatsView renders the IRC-style workspace: rooms on the left, the
// transcript in the middle, and the room's members on the right. The member
// column only appears when the terminal is wide enough for three readable
// columns.
func (m *Model) chatsView() string {
	c := &m.Chats
	rowsHeight := max(1, m.bodyHeight()-1)

	var left []string
	switch {
	case !c.loaded:
		left = append(left, m.Spinner.View()+" "+m.Styles.Muted.Render("loading rooms…"))
	case len(c.rooms) == 0:
		left = append(left, m.Styles.Muted.Render("no chat rooms yet"))
	default:
		visible := max(rowsHeight, 1)
		start, end := scrollWindow(c.selected, c.selected-visible/2, len(c.rooms), visible)
		for i := start; i < end; i++ {
			left = append(left, m.renderRoomRow(c.rooms[i], m.listWidth(), i == c.selected)...)
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
		prefix := "#"
		if m.Chat.room.IsDirect() {
			prefix = "@"
		}
		title = prefix + m.roomTitle(m.Chat.room)
	}

	widths := []int{m.listWidth(), m.detailWidth()}
	columns := [][]string{left, m.chatColumn(rowsHeight)}
	if m.showMembers() {
		widths = append(widths, m.membersWidth())
		columns = append(columns, m.memberColumn(rowsHeight))
	}
	return m.splitWorkspace(title, right, widths, columns)
}

// listWidth is the width of the room column in the chat workspace.
func (m *Model) listWidth() int {
	width := m.contentWidth()
	list := max(24, min(width/3, 34))
	return list
}

// membersWidth is the width of the nicklist column.
func (m *Model) membersWidth() int {
	width := m.contentWidth()
	return max(14, min(width/5, 22))
}

// showMembers reports whether a third column fits next to the transcript.
func (m *Model) showMembers() bool {
	return m.Chat.room.ID != "" && m.contentWidth() >= 76
}

// chatColumn renders the middle column: the transcript and the IRC-style
// input line, or a prompt when no room is selected.
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
	switch {
	case c.typing && c.sending:
		composer = 2
	case c.typing:
		composer = 1
	case c.lastErr != "":
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
		out = append(out, c.input.View())
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
	if m.showMembers() {
		width -= m.membersWidth() + 3
	}
	return max(width, 20)
}

// renderRoomRow renders one room as one IRC-style line: the room name and
// the last message date.
func (m *Model) renderRoomRow(room api.ChatRoom, width int, selected bool) []string {
	marker := "  "
	if selected {
		marker = m.Styles.Accent.Render("▸ ")
	}

	title := truncate(m.roomTitle(room), max(width-4, 8))
	line := marker + m.Styles.Bold.Render(title)

	if stamp := m.roomStamp(room); stamp != "" {
		gap := width - 2 - ansi.StringWidth(title) - ansi.StringWidth(stamp)
		if gap < 1 {
			gap = 1
		}
		line += strings.Repeat(" ", gap) + m.Styles.Muted.Render(stamp)
	}
	return []string{line}
}

// roomStamp renders the last message date of a room.
func (m *Model) roomStamp(room api.ChatRoom) string {
	if summary, ok := m.Chats.summary[room.ID]; ok && summary.LastMessage != nil {
		return shortTime(summary.LastMessage.CreatedAt)
	}
	return ""
}

// roomTitle names a room. Direct rooms are named after the other participant,
// never the signed-in account.
func (m *Model) roomTitle(room api.ChatRoom) string {
	if !room.IsDirect() {
		return room.Title()
	}
	for _, member := range room.DirectMembers {
		if m.isSelf(member) {
			continue
		}
		if name := member.DisplayName(); name != "" && name != "unknown" {
			return name
		}
	}
	if summary, ok := m.Chats.summary[room.ID]; ok && summary.LastMessage != nil {
		sender := summary.LastMessage.Sender
		if !m.isSelf(sender) {
			if name := sender.DisplayName(); name != "" && name != "unknown" {
				return name
			}
		}
	}
	if room.Name != nil && *room.Name != "" {
		return *room.Name
	}
	return "direct message"
}

// selfAccountID resolves the signed-in account, preferring the loaded
// profile and falling back to the cached credential snapshot.
func (m *Model) selfAccountID() string {
	switch {
	case m.Home.account.ID != "":
		return m.Home.account.ID
	case m.Home.identity.ID != "":
		return m.Home.identity.ID
	case m.Deps.Tokens != nil:
		if creds := m.Deps.Tokens.Credentials(); creds != nil {
			return creds.AccountID
		}
	}
	return ""
}

// isSelf reports whether a room member is the signed-in account.
func (m *Model) isSelf(member api.ChatMember) bool {
	self := m.selfAccountID()
	if self == "" {
		return false
	}
	if strings.EqualFold(member.AccountID, self) {
		return true
	}
	return member.Account != nil && strings.EqualFold(member.Account.ID, self)
}

// memberColumn renders the nicklist, always omitting the signed-in account.
func (m *Model) memberColumn(height int) []string {
	c := &m.Chat
	out := []string{m.Styles.Label.Render("members")}
	switch {
	case !c.membersLoaded:
		out = append(out, m.Spinner.View()+" "+m.Styles.Muted.Render("loading…"))
	case len(c.members) == 0:
		out = append(out, m.Styles.Muted.Render("no other members"))
	default:
		for _, member := range c.members {
			if m.isSelf(member) {
				continue
			}
			name := truncate(member.DisplayName(), max(m.membersWidth()-2, 6))
			out = append(out, m.Styles.Foreground.Render(name))
		}
	}
	return clipLines(out, height)
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

// openRoom loads the highlighted room into the transcript and nicklist.
// Reopening the room that is already showing keeps its scrollback instead of
// reloading.
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
	return tea.Batch(m.loadMessagesCmd(room.ID), m.loadMembersCmd(room.ID))
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

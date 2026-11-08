package tui

import (
	"context"
	"fmt"
	"strings"
	"time"

	"src.solsynth.dev/sosys/termian/internal/config"

	"charm.land/bubbles/v2/textarea"
	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"src.solsynth.dev/sosys/termian/internal/api"
)

// chatModel is one chat room: its message log and the composer.
type chatModel struct {
	room     api.ChatRoom
	messages []api.ChatMessage
	loaded   bool
	sending  bool
	// fetching suppresses duplicate history requests while a page is in
	// flight; exhausted is set when the server has nothing older left.
	fetching  bool
	exhausted bool
	fetched   int
	// historyLimit caps the retained messages; zero uses the default.
	historyLimit int
	// members is the room's nicklist, including the signed-in account; the
	// view filters self out.
	members       []api.ChatMember
	membersLoaded bool

	input    textarea.Model
	typing   bool
	selected int
	lastErr  string
}

// newChatModel builds an empty room pane.
func newChatModel() chatModel {
	ta := textarea.New()
	ta.Placeholder = "write a message…"
	ta.ShowLineNumbers = false
	ta.Prompt = "> "
	ta.CharLimit = 4000
	ta.SetHeight(1)
	ta.SetWidth(DefaultInputWidth)
	// Enter sends; a newline needs alt+enter.
	ta.KeyMap.InsertNewline.SetKeys("alt+enter", "ctrl+j")
	return chatModel{input: ta}
}

// setWidth resizes the composer to the content width.
func (c *chatModel) setWidth(w int) { c.input.SetWidth(max(w-2, 10)) }

// messagesLoadedMsg carries one page of room messages. older is set when the
// page was fetched for scrollback rather than for the initial view.
type messagesLoadedMsg struct {
	roomID string
	page   api.Page[api.ChatMessage]
	first  int
	older  bool
	err    error
}

// sentMessageMsg reports a stored outgoing message.
type sentMessageMsg struct {
	roomID string
	msg    api.ChatMessage
	err    error
}

// membersLoadedMsg carries a room's nicklist.
type membersLoadedMsg struct {
	roomID  string
	members []api.ChatMember
	err     error
}

// chatPageSize is how many messages one history page holds.
const chatPageSize = 50

// loadMessagesCmd fetches the newest messages of a room.
func (m *Model) loadMessagesCmd(roomID string) tea.Cmd {
	return m.withTimeout(func(ctx context.Context) tea.Msg {
		if err := m.requireSession(ctx); err != nil {
			return messagesLoadedMsg{roomID: roomID, err: err}
		}
		page, err := m.Deps.Content.ChatMessages(ctx, roomID, 0, chatPageSize)
		return messagesLoadedMsg{roomID: roomID, page: page, err: err}
	})
}

// loadOlderMessagesCmd pages back through the room history. The offset is the
// number of rows already read from the server, which the pane tracks
// separately from the visible messages because event rows are filtered out.
func (m *Model) loadOlderMessagesCmd(roomID string) tea.Cmd {
	first := m.Chat.fetched
	return m.withTimeout(func(ctx context.Context) tea.Msg {
		if err := m.requireSession(ctx); err != nil {
			return messagesLoadedMsg{roomID: roomID, older: true, err: err}
		}
		page, err := m.Deps.Content.ChatMessages(ctx, roomID, first, chatPageSize)
		return messagesLoadedMsg{roomID: roomID, page: page, first: first, older: true, err: err}
	})
}

// loadMembersCmd fetches the nicklist of a room. The view filters the
// signed-in account out of it.
func (m *Model) loadMembersCmd(roomID string) tea.Cmd {
	return m.withTimeout(func(ctx context.Context) tea.Msg {
		if err := m.requireSession(ctx); err != nil {
			return membersLoadedMsg{roomID: roomID, err: err}
		}
		page, err := m.Deps.Content.ChatMembers(ctx, roomID, 0, 100)
		return membersLoadedMsg{roomID: roomID, members: page.Items, err: err}
	})
}

// sendMessageCmd posts the composer contents.
func (m *Model) sendMessageCmd(roomID, content string) tea.Cmd {
	return m.withTimeout(func(ctx context.Context) tea.Msg {
		if err := m.requireSession(ctx); err != nil {
			return sentMessageMsg{roomID: roomID, err: err}
		}
		msg, err := m.Deps.Content.SendChatMessage(ctx, roomID, api.NewMessage{
			Content:       content,
			ClientMessage: newClientMessageID(),
		})
		return sentMessageMsg{roomID: roomID, msg: msg, err: err}
	})
}

// newClientMessageID builds the idempotency key of one send.
func newClientMessageID() string {
	return fmt.Sprintf("termian-%d", time.Now().UnixNano())
}

// renderLog lays the message log out and windows it around the selection.
func (m *Model) renderLog(width, height int) []string {
	c := &m.Chat
	blocks := make([][]string, len(c.messages))
	for i, msg := range c.messages {
		blocks[i] = m.renderMessage(msg, width, i == c.selected)
	}
	var out []string
	for _, i := range visibleCards(blocks, c.selected, height) {
		out = append(out, blocks[i]...)
	}
	return out
}

// renderMessage renders one IRC-style line: timestamp, nick and text. Wrapped
// text and attachments align under the message body.
func (m *Model) renderMessage(msg api.ChatMessage, width int, selected bool) []string {
	gutter := "  "
	if selected {
		gutter = m.Styles.Accent.Render("▸ ")
	}

	when := shortTime(msg.CreatedAt)
	nick := msg.Sender.DisplayName()
	plainPrefix := "<" + nick + ">"
	prefix := m.Styles.Bold.Render(plainPrefix)
	prefixWidth := ansi.StringWidth(plainPrefix)
	if when != "" {
		prefix = m.Styles.Muted.Render(when) + " " + prefix
		prefixWidth += ansi.StringWidth(when) + 1
	}

	text := msg.Text()
	if msg.DeletedAt != nil {
		text = "(deleted)"
	}
	if text == "" && len(msg.Attachments) > 0 {
		text = "(" + plural(len(msg.Attachments), "attachment", "attachments") + ")"
	}

	lines := wrapText(text, max(width-2-prefixWidth-1, 10))
	out := []string{gutter + prefix}
	if len(lines) > 0 {
		out[0] += " " + lines[0]
	}
	indent := "  " + strings.Repeat(" ", prefixWidth+1)
	for _, line := range lines[1:] {
		out = append(out, indent+line)
	}
	for i, file := range msg.Attachments {
		out = append(out, indent+m.Styles.Muted.Render(attachmentLine(file, i+1)))
	}
	if reactions := reactionSummary(msg.Reactions); reactions != "" {
		out = append(out, indent+m.Styles.Muted.Render(reactions))
	}
	return out
}

// updateChatKey handles keys on the chat workspace. The room column holds the
// focus until the user steps into the conversation with enter or l.
func (m *Model) updateChatKey(key tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	c := &m.Chat
	if c.typing {
		switch key.String() {
		case "esc":
			c.typing = false
			c.input.Blur()
			c.input.Reset()
			return m, nil
		case "enter":
			content := strings.TrimSpace(c.input.Value())
			if content == "" || c.sending || c.room.ID == "" {
				return m, nil
			}
			c.sending = true
			c.lastErr = ""
			return m, m.sendMessageCmd(c.room.ID, content)
		}
		var cmd tea.Cmd
		c.input, cmd = c.input.Update(key)
		return m, cmd
	}

	if !m.Chats.focused {
		switch key.String() {
		case "esc":
			m.showScreen(ScreenHome)
			return m, nil
		case "j", "down":
			if m.Chats.selected < len(m.Chats.rooms)-1 {
				m.Chats.selected++
				return m, m.openRoom()
			}
			return m, nil
		case "k", "up":
			if m.Chats.selected > 0 {
				m.Chats.selected--
				return m, m.openRoom()
			}
			return m, nil
		case "enter", "l":
			m.Chats.focused = true
			return m, m.openRoom()
		case "R":
			return m, m.loadChatsCmd()
		case "i":
			if err := m.sessionGuard("sign in to send messages"); err != nil {
				return m, nil
			}
			c.typing = true
			c.input.Focus()
			return m, textarea.Blink
		}
		return m, nil
	}

	switch key.String() {
	case "esc":
		m.Chats.focused = false
		return m, nil
	case "h":
		m.Chats.focused = false
		return m, nil
	case "i", "enter":
		if err := m.sessionGuard("sign in to send messages"); err != nil {
			return m, nil
		}
		c.typing = true
		c.input.Focus()
		return m, textarea.Blink
	case "j", "down":
		if c.selected < len(c.messages)-1 {
			c.selected++
		}
		return m, nil
	case "k", "up":
		if c.selected > 0 {
			c.selected--
			return m, nil
		}
		// At the top of the log, keep scrolling back through the history.
		if c.room.ID == "" || c.fetching || c.exhausted {
			return m, nil
		}
		c.fetching = true
		return m, m.loadOlderMessagesCmd(c.room.ID)
	case "G":
		c.selected = max(len(c.messages)-1, 0)
		return m, nil
	case "g":
		c.selected = 0
		return m, nil
	case "R":
		if c.room.ID == "" {
			return m, nil
		}
		return m, tea.Batch(m.loadMessagesCmd(c.room.ID), m.loadMembersCmd(c.room.ID))
	case "o":
		if c.selected < 0 || c.selected >= len(c.messages) {
			return m, nil
		}
		files := c.messages[c.selected].Attachments
		if len(files) == 0 {
			return m, nil
		}
		return m, m.openAttachment(files[0])
	case "r":
		if c.selected < 0 || c.selected >= len(c.messages) {
			return m, nil
		}
		if err := m.sessionGuard("sign in to react"); err != nil {
			return m, nil
		}
		return m, m.toggleChatReactionCmd(c.room.ID, c.messages[c.selected].ID, reactionSymbol)
	}
	return m, nil
}

// toggleChatReactionCmd toggles a reaction on one message.
func (m *Model) toggleChatReactionCmd(roomID, messageID, symbol string) tea.Cmd {
	return m.withTimeout(func(ctx context.Context) tea.Msg {
		if err := m.requireSession(ctx); err != nil {
			return chatReactionMsg{roomID: roomID, messageID: messageID, symbol: symbol, err: err}
		}
		on, err := m.Deps.Content.ToggleChatReaction(ctx, roomID, messageID, symbol)
		return chatReactionMsg{roomID: roomID, messageID: messageID, symbol: symbol, on: on, err: err}
	})
}

// chatReactionMsg reports a completed message reaction toggle.
type chatReactionMsg struct {
	roomID    string
	messageID string
	symbol    string
	on        bool
	err       error
}

// trimHistory keeps the log bounded so a long scrollback cannot grow without
// limit; the configured chat_history wins.
func (c *chatModel) trimHistory() {
	limit := c.historyLimit
	if limit <= 0 {
		limit = config.DefaultChatHistory
	}
	if len(c.messages) <= limit {
		return
	}
	drop := len(c.messages) - limit
	c.messages = c.messages[drop:]
	c.selected = max(c.selected-drop, 0)
}

// updateChatMsg applies asynchronous room results.
func (m *Model) updateChatMsg(msg tea.Msg) (tea.Model, tea.Cmd) {
	c := &m.Chat
	switch msg := msg.(type) {
	case messagesLoadedMsg:
		c.fetching = false
		if msg.err != nil {
			c.loaded = true
			m.reportContentError("messages", msg.err)
			return m, nil
		}
		c.loaded = true
		// The server returns newest first; the log reads oldest first.
		items := make([]api.ChatMessage, 0, len(msg.page.Items))
		for i := len(msg.page.Items) - 1; i >= 0; i-- {
			items = append(items, msg.page.Items[i])
		}
		if msg.older {
			// Older pages prepend, so the transcript keeps reading in order.
			c.messages = append(items, c.messages...)
			c.selected += len(items)
			c.fetched += msg.page.Fetched
			if len(msg.page.Items) == 0 || msg.page.Fetched == 0 {
				c.exhausted = true
			}
		} else {
			c.messages = items
			c.selected = max(len(items)-1, 0)
			c.fetched = msg.page.Fetched
			c.exhausted = len(items) == 0
		}
		c.trimHistory()
		m.clearBanner()
		return m, nil

	case membersLoadedMsg:
		if msg.roomID != c.room.ID {
			return m, nil
		}
		c.membersLoaded = true
		if msg.err != nil {
			c.members = nil
			return m, nil
		}
		c.members = msg.members
		return m, nil

	case sentMessageMsg:
		c.sending = false
		if msg.err != nil {
			c.lastErr = msg.err.Error()
			m.reportContentError("send", msg.err)
			return m, nil
		}
		c.messages = append(c.messages, msg.msg)
		c.selected = len(c.messages) - 1
		c.typing = false
		c.input.Blur()
		c.input.Reset()
		c.lastErr = ""
		m.clearBanner()
		return m, nil

	case chatReactionMsg:
		if msg.err != nil {
			m.reportContentError("react", msg.err)
			return m, nil
		}
		for i := range c.messages {
			if c.messages[i].ID != msg.messageID {
				continue
			}
			if c.messages[i].Reactions == nil {
				c.messages[i].Reactions = map[string]int{}
			}
			if c.messages[i].ReactionsMade == nil {
				c.messages[i].ReactionsMade = map[string]bool{}
			}
			if msg.on {
				c.messages[i].Reactions[msg.symbol]++
				c.messages[i].ReactionsMade[msg.symbol] = true
			} else {
				c.messages[i].Reactions[msg.symbol]--
				if c.messages[i].Reactions[msg.symbol] <= 0 {
					delete(c.messages[i].Reactions, msg.symbol)
				}
				delete(c.messages[i].ReactionsMade, msg.symbol)
			}
		}
		return m, nil
	}
	return m, nil
}

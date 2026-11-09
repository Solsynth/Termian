package tui

import (
	"context"
	"encoding/json"
	"fmt"

	tea "charm.land/bubbletea/v2"

	"src.solsynth.dev/sosys/termian/internal/api"
)

// realtimeEventMsg carries one websocket frame.
type realtimeEventMsg struct {
	packet api.Packet
}

// realtimeErrMsg reports a gateway failure; the client keeps reconnecting.
type realtimeErrMsg struct {
	err error
}

// notificationBadgeMsg carries the server's unread notification total.
type notificationBadgeMsg struct {
	count int
	err   error
}

// ensureRealtime starts the websocket reader once per program and returns the
// command that waits for its first frame.
func (m *Model) ensureRealtime() tea.Cmd {
	if m.Deps.Realtime == nil || m.Deps.Tokens == nil || m.realtimeStarted {
		return nil
	}
	m.realtimeStarted = true
	rt := m.Deps.Realtime
	go rt.Run(m.Deps.Ctx)
	return m.waitRealtimeCmd()
}

// waitRealtimeCmd blocks until the next frame arrives.
func (m *Model) waitRealtimeCmd() tea.Cmd {
	rt := m.Deps.Realtime
	if rt == nil {
		return nil
	}
	return func() tea.Msg {
		select {
		case packet, ok := <-rt.Events():
			if !ok {
				return nil
			}
			return realtimeEventMsg{packet: packet}
		case <-m.Deps.Ctx.Done():
			return nil
		}
	}
}

// notificationBadgeCmd refreshes the unread notification total.
func (m *Model) notificationBadgeCmd() tea.Cmd {
	return m.withTimeout(func(ctx context.Context) tea.Msg {
		if m.Deps.Content == nil {
			return notificationBadgeMsg{}
		}
		count, err := m.Deps.Content.UnreadNotificationCount(ctx)
		return notificationBadgeMsg{count: count, err: err}
	})
}

// handleRealtimeEvent applies one websocket frame and re-arms the wait.
func (m *Model) handleRealtimeEvent(msg realtimeEventMsg) (tea.Model, tea.Cmd) {
	packet := msg.packet
	switch packet.Type {
	case "notifications.new":
		m.applyLiveNotification(packet.Data)
	case "messages.new":
		m.applyLiveMessage(packet.Data)
	case "messages.read":
		m.applyLiveRead(packet.Data)
	}
	return m, m.waitRealtimeCmd()
}

// applyLiveNotification inserts a pushed notification.
func (m *Model) applyLiveNotification(data json.RawMessage) {
	var item api.Notification
	if err := json.Unmarshal(data, &item); err != nil || item.ID == "" {
		return
	}
	if m.notificationsContain(item.ID) {
		return
	}
	m.notificationsUnread++
	if item.Unread() {
		m.Notifications.unread++
	}
	if m.Screen == ScreenNotifications {
		m.Notifications.items = append([]api.Notification{item}, m.Notifications.items...)
		m.Notifications.selected = 0
	}
	if m.Screen != ScreenNotifications {
		title := item.Title
		if title == "" {
			title = notificationTopicLabel(item.Topic)
		}
		m.setNotice("🔔 " + title)
	}
}

// notificationsContain reports whether an ID is already in the stream.
func (m *Model) notificationsContain(id string) bool {
	for _, item := range m.Notifications.items {
		if item.ID == id {
			return true
		}
	}
	return false
}

// applyLiveMessage inserts a pushed chat message.
func (m *Model) applyLiveMessage(data json.RawMessage) {
	var message api.ChatMessage
	if err := json.Unmarshal(data, &message); err != nil || message.ID == "" {
		return
	}
	if message.Type == "placeholder" {
		return
	}
	// Our own sends already landed through the REST response; the gateway echo
	// must not duplicate them.
	if message.Sender.Account != nil && m.Home.account.ID != "" && message.Sender.Account.ID == m.Home.account.ID {
		return
	}
	if message.RoomID == m.Chat.room.ID {
		for _, existing := range m.Chat.messages {
			if existing.ID == message.ID {
				return
			}
		}
		m.Chat.messages = append(m.Chat.messages, message)
		if m.Chat.historyLimit > 0 && len(m.Chat.messages) > m.Chat.historyLimit {
			m.Chat.messages = m.Chat.messages[len(m.Chat.messages)-m.Chat.historyLimit:]
		}
		if summary, ok := m.Chats.summary[message.RoomID]; ok {
			summary.LastMessage = &message
			summary.UnreadCount = 0
			summary.HasUnread = false
			m.Chats.summary[message.RoomID] = summary
		}
		return
	}
	if m.Deps.Content != nil {
		for _, room := range m.Chats.rooms {
			if room.ID != message.RoomID {
				continue
			}
			summary := m.Chats.summary[message.RoomID]
			summary.UnreadCount++
			summary.HasUnread = true
			summary.LastMessage = &message
			if m.Chats.summary == nil {
				m.Chats.summary = map[string]api.ChatSummary{}
			}
			m.Chats.summary[message.RoomID] = summary
			break
		}
	}
	if m.Screen != ScreenChats {
		m.unreadChat++
		m.setNotice(fmt.Sprintf("new message in %s", chatMessageRoomName(message, m.Chats.rooms)))
	}
}

// applyLiveRead clears a room's unread marker for the account's own receipt.
func (m *Model) applyLiveRead(data json.RawMessage) {
	var payload struct {
		RoomID    string `json:"chat_room_id"`
		AccountID string `json:"account_id"`
	}
	if err := json.Unmarshal(data, &payload); err != nil {
		return
	}
	if payload.RoomID == "" {
		return
	}
	if payload.AccountID != "" && m.Home.account.ID != "" && payload.AccountID != m.Home.account.ID {
		return
	}
	if summary, ok := m.Chats.summary[payload.RoomID]; ok {
		summary.UnreadCount = 0
		summary.HasUnread = false
		m.Chats.summary[payload.RoomID] = summary
	}
}

// chatMessageRoomName names a room for a status line.
func chatMessageRoomName(message api.ChatMessage, rooms []api.ChatRoom) string {
	for _, room := range rooms {
		if room.ID == message.RoomID {
			return room.Title()
		}
	}
	return "chat"
}

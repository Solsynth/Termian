package tui

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
)

// TestRouterSwitchScreens verifies global 1-4 navigation and state retention.
func TestRouterSwitchScreens(t *testing.T) {
	fake := &fakeAPI{}
	content := newFakeContent()
	deps := newContentDeps(t, fake, content, true)

	model := NewModelWithScreen(deps, ScreenFeed)
	model.Width, model.Height, model.sized = 100, 30, true
	model.Feed.loaded = true
	model.Feed.items = content.feed
	model.Feed.selected = 0

	// Switch to Chat with key "2"
	updated, cmd := model.Update(tea.KeyPressMsg{Code: '2', Text: "2"})
	m := updated.(*Model)
	if m.Screen != ScreenChats {
		t.Fatalf("screen = %s, want ScreenChats", m.Screen)
	}
	if cmd == nil {
		t.Fatal("switching to chats did not trigger load command")
	}

	// Switch to Notifications with key "3"
	updated, _ = m.Update(tea.KeyPressMsg{Code: '3', Text: "3"})
	m = updated.(*Model)
	if m.Screen != ScreenNotifications {
		t.Fatalf("screen = %s, want ScreenNotifications", m.Screen)
	}

	// Switch to Profile with key "4"
	updated, _ = m.Update(tea.KeyPressMsg{Code: '4', Text: "4"})
	m = updated.(*Model)
	if m.Screen != ScreenHome {
		t.Fatalf("screen = %s, want ScreenHome (Profile)", m.Screen)
	}

	// Switch back to Posts with key "1" and ensure feed state is retained
	updated, _ = m.Update(tea.KeyPressMsg{Code: '1', Text: "1"})
	m = updated.(*Model)
	if m.Screen != ScreenFeed {
		t.Fatalf("screen = %s, want ScreenFeed", m.Screen)
	}
	if len(m.Feed.items) != 1 || m.Feed.items[0].ID != "post-1" {
		t.Fatalf("feed state was lost on route switch: items=%+v", m.Feed.items)
	}
}

// TestRouterHotkeyGuard verifies typing in an active input does not trigger 1-4 or quit.
func TestRouterHotkeyGuard(t *testing.T) {
	fake := &fakeAPI{}
	content := newFakeContent()
	deps := newContentDeps(t, fake, content, true)

	model := NewModelWithScreen(deps, ScreenChats)
	model.Width, model.Height, model.sized = 100, 30, true
	model.Chats.loaded = true
	model.Chats.rooms = content.rooms.Rooms
	model.Chats.focused = true
	model.Chat.room = content.rooms.Rooms[0]
	model.Chat.loaded = true
	model.Chat.typing = true
	model.Chat.input.Focus()

	if !model.isInputFocused() {
		t.Fatal("isInputFocused() = false while chat input is typing")
	}

	// Type '1' - must be appended to the textarea, not switch screen
	updated, _ := model.Update(tea.KeyPressMsg{Code: '1', Text: "1"})
	m := updated.(*Model)
	if m.Screen != ScreenChats {
		t.Fatalf("screen changed while typing: %s", m.Screen)
	}
	if m.Chat.input.Value() != "1" {
		t.Fatalf("input value = %q, want '1'", m.Chat.input.Value())
	}

	// Type 'q' - must be appended to textarea, not quit
	updated, cmd := m.Update(tea.KeyPressMsg{Code: 'q', Text: "q"})
	m = updated.(*Model)
	if cmd != nil {
		// tea.Quit returns a non-nil cmd that terminates; it must not be quit!
	}
	if m.Chat.input.Value() != "1q" {
		t.Fatalf("input value = %q, want '1q'", m.Chat.input.Value())
	}
}

// TestRouterIncomingChatMsg verifies background chat events update unread counters.
func TestRouterIncomingChatMsg(t *testing.T) {
	fake := &fakeAPI{}
	content := newFakeContent()
	deps := newContentDeps(t, fake, content, true)

	model := NewModelWithScreen(deps, ScreenFeed)
	model.Width, model.Height, model.sized = 100, 30, true
	model.Feed.loaded = true

	if model.totalUnread() != 0 {
		t.Fatalf("initial unread = %d, want 0", model.totalUnread())
	}

	// Incoming chat event arrives while user is on Posts screen
	updated, _ := model.Update(IncomingChatMsg{
		RoomID:  "room-1",
		Sender:  "bob",
		Content: "hey there!",
	})
	m := updated.(*Model)

	if m.totalUnread() != 1 {
		t.Fatalf("unread count = %d, want 1", m.totalUnread())
	}

	// Header shows unread badge: ● [2] Chat (1)
	header := m.renderHeader()
	if !strings.Contains(header, "[2] Chat (1)") {
		t.Fatalf("header missing badge: %s", header)
	}

	// Switch to Chat clears unread count
	updated, _ = m.Update(tea.KeyPressMsg{Code: '2', Text: "2"})
	m = updated.(*Model)
	if m.unreadChat != 0 {
		t.Fatalf("unreadChat = %d, want 0 after entering chat", m.unreadChat)
	}
}

// TestRouterSwitchScreenMsg verifies programmatic deep linking.
func TestRouterSwitchScreenMsg(t *testing.T) {
	fake := &fakeAPI{}
	content := newFakeContent()
	deps := newContentDeps(t, fake, content, true)

	model := NewModelWithScreen(deps, ScreenFeed)
	model.Width, model.Height, model.sized = 100, 30, true
	model.Chats.rooms = content.rooms.Rooms

	// Deep link to Chat with room-1 payload
	updated, cmd := model.Update(SwitchScreenMsg{
		Target:  ScreenChats,
		Payload: "room-1",
	})
	m := updated.(*Model)
	if m.Screen != ScreenChats {
		t.Fatalf("screen = %s, want ScreenChats", m.Screen)
	}
	if cmd == nil {
		t.Fatal("openRoom command was not initiated")
	}
}

// TestRouterHeaderAndFooterLayout verifies persistent chrome layout.
func TestRouterHeaderAndFooterLayout(t *testing.T) {
	fake := &fakeAPI{}
	content := newFakeContent()
	deps := newContentDeps(t, fake, content, true)

	model := NewModelWithScreen(deps, ScreenFeed)
	model.Width, model.Height, model.sized = 100, 30, true
	model.Feed.loaded = true

	rendered := model.render()
	lines := strings.Split(rendered, "\n")
	if len(lines) != 30 {
		t.Fatalf("rendered lines = %d, want 30", len(lines))
	}

	// Top line is the header with title and tabs
	if !strings.Contains(lines[0], "Solian Terminal") || !strings.Contains(lines[0], "[1] Posts") {
		t.Fatalf("header line = %q", lines[0])
	}

	// Bottom lines are the footer frame
	footerLine := lines[len(lines)-2]
	if !strings.Contains(footerLine, "Switch Screen") {
		t.Fatalf("footer line = %q", footerLine)
	}
}

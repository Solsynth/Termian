package tui

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"

	"src.solsynth.dev/sosys/termian/internal/api"
)

// TestNotificationsLoadAndMarkAll covers the notification stream: loading,
// rendering topics and clearing every unread marker.
func TestNotificationsLoadAndMarkAll(t *testing.T) {
	fake := &fakeAPI{}
	content := newFakeContent()
	content.notifications = []api.Notification{
		{ID: "n1", Topic: "post.replies", Title: "Alice replied", Content: "hello", CreatedAt: timeNow(), Meta: map[string]any{"action_uri": "solian://posts/post-1"}},
		{ID: "n2", Topic: "posts.reactions.new", Title: "Bob reacted", Content: "heart", CreatedAt: timeNow(), ViewedAt: ptr(timeNow())},
	}
	deps := newContentDeps(t, fake, content, true)
	model := NewModelWithScreen(deps, ScreenNotifications)
	model.Width, model.Height, model.sized = 100, 30, true

	updated, _ := model.Update(applyCmd(model, model.loadNotificationsCmd()))
	model = updated.(*Model)
	if got := model.Notifications.unread; got != 1 {
		t.Fatalf("unread = %d, want 1", got)
	}
	if got := model.notificationsUnread; got != 1 {
		t.Fatalf("badge count = %d, want 1", got)
	}
	view := model.render()
	for _, want := range []string{"Alice replied", "reply", "Bob reacted", "solian://posts/post-1"} {
		if !strings.Contains(view, want) {
			t.Fatalf("notifications view missing %q:\n%s", want, view)
		}
	}

	updated, cmd := model.Update(tea.KeyPressMsg{Code: 'a', Text: "a"})
	model = updated.(*Model)
	if cmd == nil {
		t.Fatal("mark-all returned no command")
	}
	updated, _ = model.Update(cmd())
	model = updated.(*Model)
	if !content.called("notifread:all") {
		t.Fatal("mark all did not reach the API")
	}
	if model.Notifications.unread != 0 || model.notificationsUnread != 0 {
		t.Fatalf("unread still %d/%d", model.Notifications.unread, model.notificationsUnread)
	}
}

// TestComposeCarriesTypeTagsAttachmentsAndPublisher covers the richer publish
// payload.
func TestComposeCarriesTypeTagsAttachmentsAndPublisher(t *testing.T) {
	fake := &fakeAPI{}
	content := newFakeContent()
	deps := newContentDeps(t, fake, content, true)
	model := NewModelWithScreen(deps, ScreenFeed)
	model.Width, model.Height, model.sized = 100, 30, true

	model.openCompose(nil)
	model.Compose.postType = api.PostTypeArticle
	model.Compose.visibility = api.VisibilityUnlisted
	model.Compose.publisherName = "alice"
	model.Compose.titleInput.SetValue("A long read")
	model.Compose.tagsInput.SetValue("#go, tui, go")
	model.Compose.input.SetValue("body")
	model.Compose.attachments = []api.CloudFile{{ID: "file-1", Name: "cover.png"}}

	_, cmd := model.updateComposeKey(tea.KeyPressMsg{Code: 'd', Mod: tea.ModCtrl})
	if cmd == nil {
		t.Fatal("ctrl+d returned no command")
	}
	updated, _ := model.Update(cmd())
	model = updated.(*Model)

	if len(content.created) != 1 {
		t.Fatalf("created = %d, want 1", len(content.created))
	}
	req := content.created[0]
	if req.Type != api.PostTypeArticle || req.Visibility != api.VisibilityUnlisted {
		t.Fatalf("type/visibility = %d/%d", req.Type, req.Visibility)
	}
	if req.Title != "A long read" || req.Publisher != "alice" {
		t.Fatalf("title/publisher = %q/%q", req.Title, req.Publisher)
	}
	if len(req.Tags) != 2 || req.Tags[0] != "go" || req.Tags[1] != "tui" {
		t.Fatalf("tags = %#v", req.Tags)
	}
	if len(req.Attachments) != 1 || req.Attachments[0] != "file-1" {
		t.Fatalf("attachments = %#v", req.Attachments)
	}
}

// TestComposeLoadsMarkdownAsArticle covers ctrl+o: the file body, heading title
// and first paragraph description become an article draft.
func TestComposeLoadsMarkdownAsArticle(t *testing.T) {
	fake := &fakeAPI{}
	content := newFakeContent()
	deps := newContentDeps(t, fake, content, true)
	model := NewModelWithScreen(deps, ScreenFeed)
	model.Width, model.Height, model.sized = 100, 30, true

	path := filepath.Join(t.TempDir(), "notes.md")
	body := "# Terminal UI notes\n\nA practical article about building TUIs.\n\nMore text.\n"
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}

	model.openCompose(nil)
	_, cmd := model.updateComposeKey(tea.KeyPressMsg{Code: 'o', Mod: tea.ModCtrl})
	if cmd == nil {
		t.Fatal("ctrl+o did not open the picker")
	}
	model.Compose.path.SetValue(path)
	_, cmd = model.updateComposeKey(tea.KeyPressMsg{Code: tea.KeyEnter})
	if cmd == nil {
		t.Fatal("path prompt returned no command")
	}
	updated, _ := model.Update(cmd())
	model = updated.(*Model)

	if model.Compose.postType != api.PostTypeArticle {
		t.Fatalf("type = %d, want article", model.Compose.postType)
	}
	if got := model.Compose.titleInput.Value(); got != "Terminal UI notes" {
		t.Fatalf("title = %q", got)
	}
	if !strings.Contains(model.Compose.descriptionInput.Value(), "practical article") {
		t.Fatalf("description = %q", model.Compose.descriptionInput.Value())
	}
	if model.Compose.input.Value() != body {
		t.Fatal("markdown body was not loaded")
	}
}

// TestComposeRejectsInvalidBlogLink covers the blog mode validation.
func TestComposeRejectsInvalidBlogLink(t *testing.T) {
	fake := &fakeAPI{}
	content := newFakeContent()
	deps := newContentDeps(t, fake, content, true)
	model := NewModelWithScreen(deps, ScreenFeed)
	model.Width, model.Height, model.sized = 100, 30, true

	model.openCompose(nil)
	model.Compose.postType = api.PostTypeBlog
	model.Compose.input.SetValue("not a link")
	_, cmd := model.updateComposeKey(tea.KeyPressMsg{Code: 'd', Mod: tea.ModCtrl})
	if cmd != nil {
		t.Fatal("invalid blog link published")
	}
	if !strings.Contains(model.Banner, "valid http(s) link") {
		t.Fatalf("banner = %q", model.Banner)
	}

	model.Compose.input.SetValue("https://blog.example.com/posts/one")
	_, cmd = model.updateComposeKey(tea.KeyPressMsg{Code: 'd', Mod: tea.ModCtrl})
	if cmd == nil {
		t.Fatal("valid blog link did not publish")
	}
	model.Update(cmd())
	if len(content.created) != 1 || content.created[0].Type != api.PostTypeBlog {
		t.Fatalf("created = %#v", content.created)
	}
	if content.created[0].Title != "blog.example.com" {
		t.Fatalf("blog title = %q", content.created[0].Title)
	}
}

// TestProfileSettingsCyclePreference covers entering a notification topic on
// the profile page and writing the next level.
func TestProfileSettingsCyclePreference(t *testing.T) {
	fake := &fakeAPI{identity: api.Identity{ID: "acc-1", Name: "alice"}, account: api.Account{ID: "acc-1", Name: "alice"}}
	content := newFakeContent()
	content.preferences = nil
	deps := newContentDeps(t, fake, content, true)
	model := NewModelWithScreen(deps, ScreenHome)
	model.Width, model.Height, model.sized = 110, 40, true
	model.handleAuthed(authedMsg{id: fake.identity, acc: fake.account})
	applyCmd(model, model.loadProfileExtrasCmd())

	if len(model.Home.publishers) != 1 {
		t.Fatalf("publishers = %d", len(model.Home.publishers))
	}
	// Row 0 is the publisher, row 1 the first notification topic.
	model.Home.setting = 1
	cmd := model.activateProfileSetting()
	if cmd == nil {
		t.Fatal("topic activation returned no command")
	}
	applyCmd(model, cmd)
	if !content.called("pref:posts.mentions.new") {
		t.Fatal("preference write did not reach the API")
	}
	if got := model.preferenceLevel("posts.mentions.new"); got != api.NotificationPreferenceSilent {
		t.Fatalf("level = %d, want silent", got)
	}
	view := model.render()
	if !strings.Contains(view, "notification settings") || !strings.Contains(view, "silent") {
		t.Fatalf("profile settings missing:\n%s", view)
	}
}

// TestRealtimePushesNotificationAndMessage covers websocket payload handling.
func TestRealtimePushesNotificationAndMessage(t *testing.T) {
	fake := &fakeAPI{}
	content := newFakeContent()
	deps := newContentDeps(t, fake, content, true)
	model := NewModelWithScreen(deps, ScreenFeed)
	model.Width, model.Height, model.sized = 100, 30, true
	model.Chat.room = content.rooms.Rooms[0]
	model.Home.account.ID = "account-1"

	notification := api.Notification{ID: "live-1", Topic: "posts.mentions.new", Title: "You were mentioned"}
	raw, _ := json.Marshal(notification)
	model.handleRealtimeEvent(realtimeEventMsg{packet: api.Packet{Type: "notifications.new", Data: raw}})
	if model.notificationsUnread != 1 {
		t.Fatalf("badge = %d, want 1", model.notificationsUnread)
	}
	if !strings.Contains(model.Banner, "You were mentioned") {
		t.Fatalf("banner = %q", model.Banner)
	}

	message := api.ChatMessage{
		ID:      "live-msg-1",
		RoomID:  model.Chat.room.ID,
		Type:    "text",
		Content: ptr("live hello"),
		Sender:  api.ChatMember{Account: &api.ChatUser{ID: "account-2", Name: "bob"}},
	}
	raw, _ = json.Marshal(message)
	model.handleRealtimeEvent(realtimeEventMsg{packet: api.Packet{Type: "messages.new", Data: raw}})
	if len(model.Chat.messages) != 1 || model.Chat.messages[0].ID != "live-msg-1" {
		t.Fatalf("messages = %#v", model.Chat.messages)
	}
	// Duplicate delivery is ignored.
	model.handleRealtimeEvent(realtimeEventMsg{packet: api.Packet{Type: "messages.new", Data: raw}})
	if len(model.Chat.messages) != 1 {
		t.Fatal("duplicate websocket delivery appended twice")
	}
}

// TestChatViewShowsUnreadMarkerFocusAndPresence covers the chat affordances.
func TestChatViewShowsUnreadMarkerFocusAndPresence(t *testing.T) {
	fake := &fakeAPI{}
	content := newFakeContent()
	content.members[content.rooms.Rooms[0].ID] = []api.ChatMember{
		{ID: "m2", Account: &api.ChatUser{ID: "account-2", Name: "bob", Nick: "Bob"}, Status: &api.ChatStatus{IsOnline: true}},
	}
	deps := newContentDeps(t, fake, content, true)
	model := NewModelWithScreen(deps, ScreenChats)
	model.Width, model.Height, model.sized = 120, 40, true
	model.Chats.loaded = true
	model.Chats.rooms = content.rooms.Rooms
	model.Chats.summary = content.summary
	model.Chat.room = content.rooms.Rooms[0]
	model.Chat.loaded = true
	model.Chat.membersLoaded = true
	model.Chat.members = content.members[content.rooms.Rooms[0].ID]

	view := flatten(model.render())
	for _, want := range []string{"[*]", "roomsfocused", "conversation"} {
		if !strings.Contains(view, want) {
			t.Fatalf("chat view missing %q:\n%s", want, view)
		}
	}
	if !strings.Contains(view, "●") {
		t.Fatal("online member dot missing")
	}

	model.Chats.focused = true
	view = flatten(model.render())
	if !strings.Contains(view, "conversationfocused") {
		t.Fatalf("focused conversation label missing:\n%s", view)
	}
}

// timeNow is a stable timestamp for scripted notifications.
func timeNow() time.Time { return time.Date(2026, 11, 9, 12, 0, 0, 0, time.UTC) }

package tui

import (
	"context"
	"errors"
	"image"
	"image/color"
	"strings"
	"sync"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"src.solsynth.dev/sosys/termian/internal/api"
	"src.solsynth.dev/sosys/termian/internal/credentials"
)

// fakeContent is a scripted ContentAPI for the feed and chat panes.
type fakeContent struct {
	mu sync.Mutex

	feed      []api.Post
	feedTotal int
	posts     map[string]api.Post
	replies   map[string][]api.Post
	rooms     api.ChatRooms
	summary   map[string]api.ChatSummary
	messages  map[string][]api.ChatMessage

	// calls records the write calls, in order.
	calls []string
	// reactions counts toggles per post, so a test can assert the toggle
	// semantics without a server.
	reactions map[string]int
	// failWrite makes every write fail.
	failWrite error
	// sent holds the messages the test posted.
	sent []api.NewMessage
}

// newFakeContent builds a fake with one post and one room.
func newFakeContent() *fakeContent {
	author := api.Publisher{ID: "pub-1", Name: "alice", Nick: "Alice"}
	post := api.Post{
		ID:            "post-1",
		Content:       ptr("hello from the timeline"),
		Publisher:     &author,
		RepliesCount:  1,
		Reactions:     map[string]int{"heart": 2},
		ReactionsMade: map[string]bool{},
	}
	reply := api.Post{
		ID:            "reply-1",
		Content:       ptr("and a reply"),
		Publisher:     &author,
		RepliedPostID: ptr("post-1"),
		Reactions:     map[string]int{},
	}
	room := api.ChatRoom{ID: "room-1", Name: ptr("General"), Type: api.ChatRoomGroup}
	msg := api.ChatMessage{
		ID:        "msg-1",
		RoomID:    room.ID,
		Type:      "text",
		Content:   ptr("first message"),
		Sender:    api.ChatMember{ID: "member-1", Account: &api.ChatUser{Name: "alice", Nick: "Alice"}},
		CreatedAt: time.Now().Add(-time.Hour),
	}
	return &fakeContent{
		feed:      []api.Post{post},
		feedTotal: 1,
		posts:     map[string]api.Post{"post-1": post, "reply-1": reply},
		replies:   map[string][]api.Post{"post-1": {reply}},
		rooms:     api.ChatRooms{Rooms: []api.ChatRoom{room}},
		summary: map[string]api.ChatSummary{
			room.ID: {UnreadCount: 1, HasUnread: true, LastMessage: &msg},
		},
		messages:  map[string][]api.ChatMessage{room.ID: {msg}},
		reactions: map[string]int{},
	}
}

// ptr returns a pointer to v.
func ptr[T any](v T) *T { return &v }

// Feed returns the scripted timeline.
func (f *fakeContent) Feed(context.Context, int, int) (api.Page[api.Post], error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return api.Page[api.Post]{Items: f.feed, Total: f.feedTotal}, nil
}

// Bookmarks returns the scripted bookmarks.
func (f *fakeContent) Bookmarks(context.Context, int, int) (api.Page[api.Post], error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return api.Page[api.Post]{Items: f.feed, Total: f.feedTotal}, nil
}

// Post returns one scripted post.
func (f *fakeContent) Post(_ context.Context, id string) (api.Post, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	post, ok := f.posts[id]
	if !ok {
		return api.Post{}, errors.New("post not found")
	}
	return post, nil
}

// Replies returns the scripted replies of a post.
func (f *fakeContent) Replies(_ context.Context, id string, _, _ int) (api.Page[api.Post], error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	items := f.replies[id]
	return api.Page[api.Post]{Items: items, Total: len(items)}, nil
}

// CreatePost records a publish or a reply.
func (f *fakeContent) CreatePost(_ context.Context, req api.NewPost) (api.Post, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.failWrite != nil {
		return api.Post{}, f.failWrite
	}
	f.calls = append(f.calls, "create")
	post := api.Post{
		ID:        "created-1",
		Content:   ptr(req.Content),
		Publisher: &api.Publisher{Name: "alice", Nick: "Alice"},
	}
	if req.ReplyTo != "" {
		post.RepliedPostID = ptr(req.ReplyTo)
		f.replies[req.ReplyTo] = append([]api.Post{post}, f.replies[req.ReplyTo]...)
	}
	return post, nil
}

// ToggleReaction flips the scripted reaction state.
func (f *fakeContent) ToggleReaction(_ context.Context, id, symbol string) (bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.failWrite != nil {
		return false, f.failWrite
	}
	f.calls = append(f.calls, "react:"+id+":"+symbol)
	f.reactions[id+":"+symbol]++
	return f.reactions[id+":"+symbol]%2 == 1, nil
}

// Bookmark records a bookmark.
func (f *fakeContent) Bookmark(_ context.Context, id string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.failWrite != nil {
		return f.failWrite
	}
	f.calls = append(f.calls, "bookmark:"+id)
	return nil
}

// Unbookmark records a removed bookmark.
func (f *fakeContent) Unbookmark(_ context.Context, id string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.failWrite != nil {
		return f.failWrite
	}
	f.calls = append(f.calls, "unbookmark:"+id)
	return nil
}

// Publishers returns one scripted publisher.
func (f *fakeContent) Publishers(context.Context) ([]api.Publisher, error) {
	return []api.Publisher{{ID: "pub-1", Name: "alice", Nick: "Alice"}}, nil
}

// ChatRoomList returns the scripted rooms.
func (f *fakeContent) ChatRoomList(context.Context, int, int) (api.ChatRooms, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.rooms, nil
}

// ChatRoom returns the scripted room.
func (f *fakeContent) ChatRoom(_ context.Context, id string) (api.ChatRoom, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, room := range f.rooms.Rooms {
		if room.ID == id {
			return room, nil
		}
	}
	return api.ChatRoom{}, errors.New("room not found")
}

// ChatMessages returns the scripted log, newest first like the server.
func (f *fakeContent) ChatMessages(_ context.Context, roomID string, _, _ int) (api.Page[api.ChatMessage], error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	items := append([]api.ChatMessage(nil), f.messages[roomID]...)
	// Reverse into the server's newest-first order.
	for i, j := 0, len(items)-1; i < j; i, j = i+1, j-1 {
		items[i], items[j] = items[j], items[i]
	}
	return api.Page[api.ChatMessage]{Items: items, Total: len(items)}, nil
}

// UpdatePost records an edit and returns the patched post.
func (f *fakeContent) UpdatePost(_ context.Context, id string, patch api.PostPatch) (api.Post, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.failWrite != nil {
		return api.Post{}, f.failWrite
	}
	post, ok := f.posts[id]
	if !ok {
		return api.Post{}, errors.New("post not found")
	}
	if patch.Content != nil {
		post.Content = patch.Content
	}
	f.posts[id] = post
	for i := range f.feed {
		if f.feed[i].ID == id {
			f.feed[i] = post
		}
	}
	f.calls = append(f.calls, "update:"+id)
	return post, nil
}

// DeletePost records a removal.
func (f *fakeContent) DeletePost(_ context.Context, id string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.failWrite != nil {
		return f.failWrite
	}
	f.calls = append(f.calls, "delete:"+id)
	kept := f.feed[:0]
	for _, post := range f.feed {
		if post.ID != id {
			kept = append(kept, post)
		}
	}
	f.feed = kept
	delete(f.posts, id)
	return nil
}

// EditChatMessage records an edit.
func (f *fakeContent) EditChatMessage(_ context.Context, roomID, messageID, content string) (api.ChatMessage, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.failWrite != nil {
		return api.ChatMessage{}, f.failWrite
	}
	f.calls = append(f.calls, "chatedit:"+messageID)
	return api.ChatMessage{ID: messageID, RoomID: roomID, Type: "text", Content: ptr(content)}, nil
}

// DeleteChatMessage records a removal.
func (f *fakeContent) DeleteChatMessage(_ context.Context, roomID, messageID string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.failWrite != nil {
		return f.failWrite
	}
	f.calls = append(f.calls, "chatdelete:"+messageID)
	kept := f.messages[roomID][:0]
	for _, msg := range f.messages[roomID] {
		if msg.ID != messageID {
			kept = append(kept, msg)
		}
	}
	f.messages[roomID] = kept
	return nil
}

// ChatSummaryByRoom returns the scripted unread state.
func (f *fakeContent) ChatSummaryByRoom(context.Context) (map[string]api.ChatSummary, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.summary, nil
}

// SendChatMessage records an outgoing message.
func (f *fakeContent) SendChatMessage(_ context.Context, roomID string, req api.NewMessage) (api.ChatMessage, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.failWrite != nil {
		return api.ChatMessage{}, f.failWrite
	}
	f.sent = append(f.sent, req)
	msg := api.ChatMessage{
		ID:        "msg-sent",
		RoomID:    roomID,
		Type:      "text",
		Content:   ptr(req.Content),
		Sender:    api.ChatMember{ID: "member-1", Account: &api.ChatUser{Name: "alice", Nick: "Alice"}},
		CreatedAt: time.Now(),
	}
	f.messages[roomID] = append(f.messages[roomID], msg)
	return msg, nil
}

// ToggleChatReaction records a message reaction toggle.
func (f *fakeContent) ToggleChatReaction(_ context.Context, roomID, messageID, symbol string) (bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.failWrite != nil {
		return false, f.failWrite
	}
	f.calls = append(f.calls, "chatreact:"+messageID+":"+symbol)
	return true, nil
}

// called reports whether a recorded call matches prefix.
func (f *fakeContent) called(prefix string) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, call := range f.calls {
		if strings.HasPrefix(call, prefix) {
			return true
		}
	}
	return false
}

// newContentDeps builds deps wired to a fake content client and, optionally,
// a signed-in token source.
func newContentDeps(t *testing.T, fake *fakeAPI, content *fakeContent, signedIn bool) Deps {
	t.Helper()
	deps := newTestDeps(t, fake)
	deps.Content = content
	if signedIn {
		deps.Tokens.Seed(&credentials.Credentials{
			Server:       deps.Server,
			Method:       credentials.MethodDevice,
			RefreshToken: "rt-test",
		}, "at-test", 3600)
	}
	return deps
}

// waitFeed waits for the feed pane to hold posts.
func waitFeed(r *recorder) {
	r.waitState("the timeline", func(s Snapshot) bool {
		return s.Screen == ScreenFeed && s.FeedCount > 0
	})
}

// TestGuestFeedBrowsesTimeline covers the guest path: the login pane opens the
// public timeline, which renders without a session.
func TestGuestFeedBrowsesTimeline(t *testing.T) {
	fake := &fakeAPI{
		deviceCode:  api.DeviceCode{DeviceCode: "dc-1", UserCode: "LHJC-ZBPP", ExpiresIn: 600, Interval: 5},
		pollResults: []pollResult{{err: &api.DeviceError{Code: api.DeviceAuthorizationPending}}},
	}
	content := newFakeContent()
	deps := newTestDeps(t, fake)
	deps.Content = content

	rec := newRecorder(t, NewModel(deps), 100, 30)
	rec.wait("the device card", func(flat string) bool { return strings.Contains(flat, "browseasguest") })

	rec.typeText("g")
	rec.waitState("guest browsing", func(s Snapshot) bool {
		return s.Guest && s.Screen == ScreenFeed && s.FeedCount == 1
	})
	rec.wait("the post card", func(flat string) bool {
		return strings.Contains(flat, "Alice") && strings.Contains(flat, "hellofromthetimeline")
	})

	// Writes are refused locally instead of failing at the server.
	rec.typeText("r")
	rec.waitState("the sign-in hint", func(s Snapshot) bool {
		return strings.Contains(s.Banner, "sign in to react")
	})
	if content.called("react:") {
		t.Fatal("a guest reaction reached the API")
	}
	rec.finish()
}

// TestFeedOpensPostAndReplies covers opening a post and drilling into a reply.
func TestFeedOpensPostAndReplies(t *testing.T) {
	fake := &fakeAPI{}
	content := newFakeContent()
	deps := newContentDeps(t, fake, content, true)

	model := NewModelWithScreen(deps, ScreenFeed)
	rec := newRecorder(t, model, 100, 30)
	waitFeed(rec)

	rec.send(tea.KeyPressMsg{Code: tea.KeyEnter})
	rec.waitState("the post pane", func(s Snapshot) bool {
		return s.Screen == ScreenPost && s.PostID == "post-1" && s.ReplyCount == 1
	})
	rec.wait("the reply", func(flat string) bool { return strings.Contains(flat, "andareply") })

	// j moves the focus onto the reply, enter drills in.
	rec.typeText("j")
	rec.waitState("the reply focus", func(s Snapshot) bool { return s.PostSelected == 0 })
	rec.send(tea.KeyPressMsg{Code: tea.KeyEnter})
	rec.waitState("the reply pane", func(s Snapshot) bool { return s.PostID == "reply-1" })

	// esc walks back to the parent post, then to the feed.
	rec.send(tea.KeyPressMsg{Code: tea.KeyEsc})
	rec.waitState("the parent post", func(s Snapshot) bool { return s.PostID == "post-1" })
	rec.send(tea.KeyPressMsg{Code: tea.KeyEsc})
	rec.waitState("the feed", func(s Snapshot) bool { return s.Screen == ScreenFeed })
	rec.finish()
}

// TestFeedReactionAndBookmark covers both toggles and their banner feedback.
func TestFeedReactionAndBookmark(t *testing.T) {
	fake := &fakeAPI{}
	content := newFakeContent()
	deps := newContentDeps(t, fake, content, true)

	model := NewModelWithScreen(deps, ScreenFeed)
	rec := newRecorder(t, model, 100, 30)
	waitFeed(rec)

	rec.typeText("r")
	rec.waitState("the reaction notice", func(s Snapshot) bool {
		return s.BannerOK && strings.Contains(s.Banner, "reacted")
	})
	rec.typeText("r")
	rec.waitState("the removal notice", func(s Snapshot) bool {
		return s.BannerOK && strings.Contains(s.Banner, "removed")
	})

	rec.typeText("b")
	rec.waitState("the bookmark notice", func(s Snapshot) bool {
		return s.BannerOK && strings.Contains(s.Banner, "saved")
	})
	rec.wait("the saved marker", func(flat string) bool { return strings.Contains(flat, "★saved") })

	if !content.called("bookmark:post-1") {
		t.Fatal("bookmark call was not recorded")
	}
	rec.finish()
}

// TestFeedComposePublishes covers the composer: n opens it, ctrl+d posts and
// the new post lands at the top of the feed.
func TestFeedComposePublishes(t *testing.T) {
	fake := &fakeAPI{}
	content := newFakeContent()
	deps := newContentDeps(t, fake, content, true)

	model := NewModelWithScreen(deps, ScreenHome)
	model.Width, model.Height, model.sized = 100, 30, true
	model.Home.loaded = true
	// A feed that was already visited receives the new post in place.
	model.Feed.loaded = true
	model.Feed.items = content.feed
	model.Feed.total = 1

	updated, cmd := model.Update(tea.KeyPressMsg{Code: 'n', Text: "n"})
	final := updated.(*Model)
	if !final.Compose.active || final.Screen != ScreenCompose {
		t.Fatalf("compose did not open: screen=%s active=%v", final.Screen, final.Compose.active)
	}
	if cmd == nil {
		t.Fatal("compose did not focus the editor")
	}

	final.Compose.input.SetValue("a fresh post")
	updated, cmd = final.Update(tea.KeyPressMsg{Code: 'd', Mod: tea.ModCtrl})
	final = updated.(*Model)
	if !final.Compose.posting {
		t.Fatal("compose did not start posting")
	}
	msg := cmd()
	created, ok := msg.(postCreatedMsg)
	if !ok || created.err != nil || created.post.ID != "created-1" {
		t.Fatalf("create message = %+v", msg)
	}

	updated, _ = final.Update(created)
	final = updated.(*Model)
	if final.Screen != ScreenFeed || final.Feed.items[0].ID != "created-1" {
		t.Fatalf("screen=%s first=%q", final.Screen, final.Feed.items[0].ID)
	}
	if !strings.Contains(final.Banner, "posted") {
		t.Fatalf("banner = %q", final.Banner)
	}
}

// TestChatsOpenRoomAndSend covers the chat path: the room list, the log and a
// sent message appended to it.
func TestChatsOpenRoomAndSend(t *testing.T) {
	fake := &fakeAPI{}
	content := newFakeContent()
	deps := newContentDeps(t, fake, content, true)

	model := NewModelWithScreen(deps, ScreenChats)
	rec := newRecorder(t, model, 100, 30)
	rec.waitState("the room list", func(s Snapshot) bool {
		return s.Screen == ScreenChats && s.ChatCount == 1
	})
	rec.wait("the room row", func(flat string) bool {
		return strings.Contains(flat, "General") && strings.Contains(flat, "Alice:firstmessage")
	})

	rec.send(tea.KeyPressMsg{Code: tea.KeyEnter})
	rec.waitState("the room", func(s Snapshot) bool {
		return s.Screen == ScreenChats && s.ChatFocused && s.ChatRoomID == "room-1" && s.MessageCount == 1
	})
	rec.wait("the message", func(flat string) bool { return strings.Contains(flat, "firstmessage") })

	rec.typeText("i")
	rec.waitState("the composer", func(s Snapshot) bool { return s.ChatFocused })
	rec.typeText("hello there")
	rec.send(tea.KeyPressMsg{Code: tea.KeyEnter})
	rec.waitState("the sent message", func(s Snapshot) bool { return s.MessageCount == 2 })
	rec.wait("the sent text", func(flat string) bool { return strings.Contains(flat, "hellothere") })

	if len(content.sent) != 1 || content.sent[0].Content != "hello there" {
		t.Fatalf("sent = %+v", content.sent)
	}
	rec.finish()
}

// TestChatsNeedSession keeps the room list honest when there is no session.
func TestChatsNeedSession(t *testing.T) {
	fake := &fakeAPI{}
	content := newFakeContent()
	deps := newContentDeps(t, fake, content, false)

	model := NewModelWithScreen(deps, ScreenHome)
	model.Width, model.Height, model.sized = 100, 30, true
	model.Home.loaded = true

	updated, cmd := model.Update(tea.KeyPressMsg{Code: 'c', Text: "c"})
	final := updated.(*Model)
	if final.Screen != ScreenHome {
		t.Fatalf("screen = %s, want home", final.Screen)
	}
	if cmd != nil {
		t.Fatal("a session-less chat open started a request")
	}
	if !strings.Contains(final.Banner, "chats need a session") {
		t.Fatalf("banner = %q", final.Banner)
	}
}

// TestWriteFailureSurfaces keeps API failures visible in the pane.
func TestWriteFailureSurfaces(t *testing.T) {
	fake := &fakeAPI{}
	content := newFakeContent()
	content.failWrite = errors.New("server said no")
	deps := newContentDeps(t, fake, content, true)

	model := NewModelWithScreen(deps, ScreenFeed)
	model.Width, model.Height, model.sized = 100, 30, true
	model.Feed.items = content.feed
	model.Feed.loaded = true

	updated, cmd := model.Update(tea.KeyPressMsg{Code: 'r', Text: "r"})
	final := updated.(*Model)
	if cmd == nil {
		t.Fatal("reaction did not start a request")
	}
	updated, _ = final.Update(cmd())
	final = updated.(*Model)
	if !strings.Contains(final.Banner, "server said no") {
		t.Fatalf("banner = %q", final.Banner)
	}
}

// TestEditPostRewritesTheBody covers the edit path: e opens the composer with
// the body, ctrl+d saves, and every pane shows the new text.
func TestEditPostRewritesTheBody(t *testing.T) {
	fake := &fakeAPI{}
	content := newFakeContent()
	deps := newContentDeps(t, fake, content, true)

	model := NewModelWithScreen(deps, ScreenFeed)
	model.Width, model.Height, model.sized = 100, 30, true
	model.Feed.loaded = true
	model.Feed.items = content.feed
	model.Feed.total = 1

	updated, cmd := model.Update(tea.KeyPressMsg{Code: 'e', Text: "e"})
	final := updated.(*Model)
	if !final.Compose.active || final.Compose.editing == nil {
		t.Fatalf("edit did not open: active=%v editing=%v", final.Compose.active, final.Compose.editing)
	}
	if got := final.Compose.input.Value(); got != "hello from the timeline" {
		t.Fatalf("editor value = %q", got)
	}
	if cmd == nil {
		t.Fatal("edit did not focus the editor")
	}

	final.Compose.input.SetValue("rewritten")
	updated, cmd = final.Update(tea.KeyPressMsg{Code: 'd', Mod: tea.ModCtrl})
	final = updated.(*Model)
	msg := cmd()
	if _, ok := msg.(postUpdatedMsg); !ok {
		t.Fatalf("message = %T", msg)
	}
	updated, _ = final.Update(msg)
	final = updated.(*Model)

	if final.Screen != ScreenFeed {
		t.Fatalf("screen = %s, want feed", final.Screen)
	}
	if got := final.Feed.items[0].Body(); got != "rewritten" {
		t.Fatalf("feed body = %q", got)
	}
	if !content.called("update:post-1") {
		t.Fatal("update call was not recorded")
	}
}

// TestDeletePostAsksFirst covers the confirmation gate and the removal.
func TestDeletePostAsksFirst(t *testing.T) {
	fake := &fakeAPI{}
	content := newFakeContent()
	deps := newContentDeps(t, fake, content, true)

	model := NewModelWithScreen(deps, ScreenFeed)
	model.Width, model.Height, model.sized = 100, 30, true
	model.Feed.loaded = true
	model.Feed.items = content.feed
	model.Feed.total = 1

	updated, _ := model.Update(tea.KeyPressMsg{Code: 'd', Text: "d"})
	final := updated.(*Model)
	if !final.Confirm.active {
		t.Fatal("delete did not ask for confirmation")
	}
	if content.called("delete:") {
		t.Fatal("delete ran before confirmation")
	}

	// Cancelling leaves the post alone.
	updated, _ = final.Update(tea.KeyPressMsg{Code: 'n', Text: "n"})
	final = updated.(*Model)
	if final.Confirm.active || content.called("delete:") {
		t.Fatal("cancel did not abort the delete")
	}

	// Confirming removes it and empties the list.
	updated, _ = final.Update(tea.KeyPressMsg{Code: 'd', Text: "d"})
	final = updated.(*Model)
	updated, cmd := final.Update(tea.KeyPressMsg{Code: 'y', Text: "y"})
	final = updated.(*Model)
	if cmd == nil {
		t.Fatal("confirmation did not start the delete")
	}
	updated, _ = final.Update(cmd())
	final = updated.(*Model)
	if len(final.Feed.items) != 0 {
		t.Fatalf("feed still holds %d posts", len(final.Feed.items))
	}
	if !strings.Contains(final.Banner, "post deleted") {
		t.Fatalf("banner = %q", final.Banner)
	}
}

// TestChatScrollbackPagesOlderHistory covers k at the top of a room: the pane
// asks for the next page with the rows it has already read as the offset.
func TestChatScrollbackPagesOlderHistory(t *testing.T) {
	fake := &fakeAPI{}
	content := newFakeContent()
	older := api.ChatMessage{
		ID:        "msg-0",
		RoomID:    "room-1",
		Type:      "text",
		Content:   ptr("older message"),
		Sender:    api.ChatMember{ID: "member-1", Account: &api.ChatUser{Name: "bob", Nick: "Bob"}},
		CreatedAt: time.Now().Add(-2 * time.Hour),
	}
	content.messages["room-1"] = []api.ChatMessage{older, content.messages["room-1"][0]}

	deps := newContentDeps(t, fake, content, true)
	model := NewModelWithScreen(deps, ScreenChats)
	model.Width, model.Height, model.sized = 100, 30, true
	model.Chats.loaded = true
	model.Chats.rooms = content.rooms.Rooms

	updated, cmd := model.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	final := updated.(*Model)
	if cmd == nil {
		t.Fatal("opening the room did not load messages")
	}
	updated, _ = final.Update(cmd())
	final = updated.(*Model)
	if len(final.Chat.messages) != 2 {
		t.Fatalf("messages = %d, want 2", len(final.Chat.messages))
	}

	// Walk to the top, then one more k asks for history. The conversation
	// column owns the keys only after enter, so focus it explicitly.
	final.Chats.focused = true
	final.Chat.selected = 0
	updated, cmd = final.Update(tea.KeyPressMsg{Code: 'k', Text: "k"})
	final = updated.(*Model)
	if cmd == nil {
		t.Fatal("k at the top did not request history")
	}
	if !final.Chat.fetching {
		t.Fatal("history request was not marked in flight")
	}
	msg, ok := cmd().(messagesLoadedMsg)
	if !ok || !msg.older {
		t.Fatalf("message = %+v", msg)
	}
	updated, _ = final.Update(msg)
	final = updated.(*Model)
	if final.Chat.fetching {
		t.Fatal("history request stayed in flight")
	}
}

// TestAttachmentModes pins the three attachment policies.
func TestAttachmentModes(t *testing.T) {
	content := newFakeContent()
	content.feed[0].Attachments = []api.CloudFile{
		{ID: "f1", Name: "photo.jpg", MimeType: "image/jpeg", Size: 2048},
	}

	for _, mode := range []string{"off", "browser", "preview"} {
		deps := newContentDeps(t, &fakeAPI{}, content, true)
		deps.Attachments = mode
		model := NewModelWithScreen(deps, ScreenFeed)
		model.Width, model.Height, model.sized = 100, 30, true

		card := strings.Join(model.renderPostCard(content.feed[0], 80, 3, false), "\n")
		switch mode {
		case "off":
			if strings.Contains(card, "photo.jpg") {
				t.Fatalf("%s: attachments still rendered:\n%s", mode, card)
			}
		case "browser", "preview":
			if !strings.Contains(card, "photo.jpg") || !strings.Contains(card, "image/jpeg") {
				t.Fatalf("%s: attachment line missing:\n%s", mode, card)
			}
		}
	}
}

// TestImagePreviewRendersASCII keeps the decoder path honest without a server.
func TestImagePreviewRendersASCII(t *testing.T) {
	img := image.NewRGBA(image.Rect(0, 0, 8, 8))
	for y := range 8 {
		for x := range 8 {
			img.Set(x, y, color.RGBA{R: uint8(x * 32), G: uint8(y * 32), B: 128, A: 255})
		}
	}

	lines := renderImage(img, 8, 4, false)
	if len(lines) != 4 {
		t.Fatalf("lines = %d, want 4", len(lines))
	}
	for _, line := range lines {
		if got := ansi.StringWidth(line); got != 8 {
			t.Fatalf("line width = %d, want 8: %q", got, line)
		}
	}

	truecolor := renderImage(img, 8, 4, true)
	if len(truecolor) != 4 {
		t.Fatalf("truecolor lines = %d, want 4", len(truecolor))
	}
	if !strings.Contains(truecolor[0], "\x1b[38;2;") {
		t.Fatalf("truecolor preview is not 24-bit: %q", truecolor[0])
	}
	if strings.Contains(lines[0], "\x1b[") {
		t.Fatalf("ascii preview carries escapes: %q", lines[0])
	}
}

// TestTabSwitchesSections covers the tab strip: tab walks timeline → chats →
// bookmarks and shift+tab walks back.
func TestTabSwitchesSections(t *testing.T) {
	fake := &fakeAPI{}
	content := newFakeContent()
	deps := newContentDeps(t, fake, content, true)

	model := NewModelWithScreen(deps, ScreenFeed)
	model.Width, model.Height, model.sized = 100, 30, true
	model.Feed.loaded = true
	model.Feed.items = content.feed

	updated, cmd := model.Update(tea.KeyPressMsg{Code: tea.KeyTab})
	final := updated.(*Model)
	if final.Screen != ScreenChats || final.currentSection() != sectionChats {
		t.Fatalf("tab did not reach chats: screen=%s section=%s", final.Screen, final.currentSection())
	}
	if cmd == nil {
		t.Fatal("entering chats did not load the room list")
	}

	updated, cmd = final.Update(tea.KeyPressMsg{Code: tea.KeyTab})
	final = updated.(*Model)
	if final.Screen != ScreenFeed || final.Feed.mode != feedBookmarks {
		t.Fatalf("tab did not reach bookmarks: screen=%s mode=%s", final.Screen, final.Feed.mode)
	}
	if cmd == nil {
		t.Fatal("entering bookmarks did not load them")
	}

	updated, _ = final.Update(tea.KeyPressMsg{Code: tea.KeyTab, Mod: tea.ModShift})
	final = updated.(*Model)
	if final.currentSection() != sectionChats {
		t.Fatalf("shift+tab did not walk back: section=%s", final.currentSection())
	}
}

// TestSectionStripIsVisible keeps the tab strip in every content pane.
func TestSectionStripIsVisible(t *testing.T) {
	content := newFakeContent()
	deps := newContentDeps(t, &fakeAPI{}, content, true)
	model := NewModelWithScreen(deps, ScreenFeed)
	model.Width, model.Height, model.sized = 100, 30, true
	model.Feed.loaded = true

	flat := flatten(model.render())
	if !strings.Contains(flat, "timeline·chats·bookmarks") {
		t.Fatalf("tab strip missing:\n%s", flat)
	}
}

// TestChatWorkspaceOpensFirstRoom covers the split view: loading the room list
// opens the newest room in the conversation column.
func TestChatWorkspaceOpensFirstRoom(t *testing.T) {
	fake := &fakeAPI{}
	content := newFakeContent()
	deps := newContentDeps(t, fake, content, true)

	model := NewModelWithScreen(deps, ScreenChats)
	model.Width, model.Height, model.sized = 110, 30, true

	updated, cmd := model.Update(roomsLoadedMsg{rooms: content.rooms, summary: content.summary})
	final := updated.(*Model)
	if cmd == nil {
		t.Fatal("the first room was not opened")
	}
	updated, _ = final.Update(cmd())
	final = updated.(*Model)
	if final.Chat.room.ID != "room-1" || len(final.Chat.messages) != 1 {
		t.Fatalf("chat = %+v", final.Chat.room)
	}

	flat := flatten(final.render())
	if !strings.Contains(flat, "General") || !strings.Contains(flat, "firstmessage") {
		t.Fatalf("workspace is missing a column:\n%s", flat)
	}
}

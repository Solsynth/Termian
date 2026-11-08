package api

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
)

// TestTimelineWireContract pins the ranked timeline request, the cursor loop
// and the post filtering.
func TestTimelineWireContract(t *testing.T) {
	requests := 0
	client, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		requests++
		if r.URL.Path != "/sphere/timeline" {
			t.Errorf("path = %q", r.URL.Path)
		}
		query := r.URL.Query()
		if got := query.Get("take"); got != "5" {
			t.Errorf("take = %q", got)
		}
		if got := query.Get("mode"); got != "latest" {
			t.Errorf("mode = %q", got)
		}
		if got := query.Get("aggressive"); got != "true" {
			t.Errorf("aggressive = %q", got)
		}
		switch requests {
		case 1:
			if got := query.Get("cursor"); got != "" {
				t.Errorf("first cursor = %q", got)
			}
			io.WriteString(w, `{"items":[{"id":"e1","type":"presence.friend","data":{}}],"next_cursor":"2026-11-08T00:00:00Z"}`)
		case 2:
			if got := query.Get("cursor"); got != "2026-11-08T00:00:00Z" {
				t.Errorf("next cursor = %q", got)
			}
			io.WriteString(w, `{"items":[{"id":"p1","type":"posts.new","data":{"id":"p1","content":"hi","publisher":{"id":"u1","name":"alice","nick":"Alice"}}}],"next_cursor":"older","mode":"latest"}`)
		default:
			t.Errorf("unexpected request %d", requests)
		}
	})

	page, err := client.Timeline(context.Background(), "", 5, "latest", true)
	if err != nil {
		t.Fatalf("Timeline: %v", err)
	}
	if page.NextCursor != "older" || page.Mode != "latest" {
		t.Fatalf("page = %+v", page)
	}
	posts := page.Posts()
	if len(posts) != 1 {
		t.Fatalf("posts = %+v", posts)
	}
	if got := posts[0].AuthorName(); got != "Alice" {
		t.Errorf("author = %q", got)
	}
	if got := posts[0].AuthorHandle(); got != "alice" {
		t.Errorf("handle = %q", got)
	}
	if requests != 2 {
		t.Errorf("requests = %d, want 2", requests)
	}
}

// TestCreatePostWireContract pins the publish payload and the publisher query.
func TestCreatePostWireContract(t *testing.T) {
	var (
		query map[string][]string
		body  map[string]any
	)
	client, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/sphere/posts" {
			t.Errorf("request = %s %s", r.Method, r.URL.Path)
		}
		query = r.URL.Query()
		_ = json.NewDecoder(r.Body).Decode(&body)
		io.WriteString(w, `{"id":"new-1","content":"hello"}`)
	})

	post, err := client.CreatePost(context.Background(), NewPost{
		Content:   "hello",
		Publisher: "alice",
		ReplyTo:   "parent-1",
	})
	if err != nil {
		t.Fatalf("CreatePost: %v", err)
	}
	if post.ID != "new-1" {
		t.Fatalf("post = %+v", post)
	}
	if got := query["pub"]; len(got) != 1 || got[0] != "alice" {
		t.Errorf("pub query = %v", got)
	}
	if got := body["content"]; got != "hello" {
		t.Errorf("content = %v", got)
	}
	if got := body["replied_post_id"]; got != "parent-1" {
		t.Errorf("replied_post_id = %v", got)
	}
	if got := body["visibility"]; got != float64(0) {
		t.Errorf("visibility = %v", got)
	}
}

// TestToggleReactionUsesStatus covers the toggle contract: 204 means the
// reaction was removed, any other 2xx that it is present.
func TestToggleReactionUsesStatus(t *testing.T) {
	cases := []struct {
		status int
		want   bool
	}{
		{http.StatusNoContent, false},
		{http.StatusOK, true},
		{http.StatusCreated, true},
	}
	for _, tc := range cases {
		var body map[string]any
		client, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path != "/sphere/posts/p1/reactions" || r.Method != http.MethodPost {
				t.Errorf("request = %s %s", r.Method, r.URL.Path)
			}
			_ = json.NewDecoder(r.Body).Decode(&body)
			w.WriteHeader(tc.status)
		})

		on, err := client.ToggleReaction(context.Background(), "p1", "heart")
		if err != nil {
			t.Fatalf("status %d: %v", tc.status, err)
		}
		if on != tc.want {
			t.Errorf("status %d: on = %v, want %v", tc.status, on, tc.want)
		}
		if body["symbol"] != "heart" {
			t.Errorf("status %d: symbol = %v", tc.status, body["symbol"])
		}
	}
}

// TestChatMessagesDropEvents keeps system events out of the transcript and
// asks the server for a wider window to compensate.
func TestChatMessagesDropEvents(t *testing.T) {
	var take string
	client, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/messager/chat/r1/messages" {
			t.Errorf("path = %q", r.URL.Path)
		}
		take = r.URL.Query().Get("take")
		w.Header().Set("X-Total", "10")
		io.WriteString(w, `[
			{"id":"m1","type":"text","content":"hello","sender":{"account":{"name":"alice","nick":"Alice"}}},
			{"id":"m2","type":"system.call.member.joined","content":null,"sender":{"account":{"name":"bob"}}},
			{"id":"m3","type":"messages.delete","content":null,"sender":{"account":{"name":"bob"}}},
			{"id":"m4","type":"text","content":"second","sender":{"account":{"name":"bob","nick":"Bob"}}}
		]`)
	})

	page, err := client.ChatMessages(context.Background(), "r1", 0, 2)
	if err != nil {
		t.Fatalf("ChatMessages: %v", err)
	}
	if len(page.Items) != 2 {
		t.Fatalf("items = %+v", page.Items)
	}
	if page.Items[0].ID != "m1" || page.Items[1].ID != "m4" {
		t.Fatalf("items = %+v", page.Items)
	}
	if page.Total != 10 {
		t.Errorf("total = %d", page.Total)
	}
	if take != "50" {
		t.Errorf("take = %q, want the widened window", take)
	}
	if got := page.Items[0].Sender.DisplayName(); got != "Alice" {
		t.Errorf("sender = %q", got)
	}
}

// TestSendChatMessageWireContract pins the send payload.
func TestSendChatMessageWireContract(t *testing.T) {
	var body map[string]any
	client, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/messager/chat/r1/messages" {
			t.Errorf("request = %s %s", r.Method, r.URL.Path)
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		io.WriteString(w, `{"id":"m1","chat_room_id":"r1","type":"text","content":"hi","sender":{"account":{"name":"alice"}}}`)
	})

	msg, err := client.SendChatMessage(context.Background(), "r1", NewMessage{
		Content:       "hi",
		ClientMessage: "cid-1",
	})
	if err != nil {
		t.Fatalf("SendChatMessage: %v", err)
	}
	if msg.ID != "m1" || msg.Text() != "hi" {
		t.Fatalf("message = %+v", msg)
	}
	for field, want := range map[string]any{
		"content":           "hi",
		"client_message_id": "cid-1",
	} {
		if got := body[field]; got != want {
			t.Errorf("%s = %v, want %v", field, got, want)
		}
	}
}

// TestChatRoomListDecodesGroups covers the rooms/groups envelope.
func TestChatRoomListDecodesGroups(t *testing.T) {
	client, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/messager/chat" {
			t.Errorf("path = %q", r.URL.Path)
		}
		io.WriteString(w, `{"rooms":[{"id":"r1","name":"General","type":0}],
			"groups":[{"id":"g1","name":"Work","room_ids":["r1"]}]}`)
	})

	rooms, err := client.ChatRoomList(context.Background(), 0, 20)
	if err != nil {
		t.Fatalf("ChatRoomList: %v", err)
	}
	if len(rooms.Rooms) != 1 || rooms.Rooms[0].Title() != "General" {
		t.Fatalf("rooms = %+v", rooms.Rooms)
	}
	if len(rooms.Groups) != 1 || rooms.Groups[0].Name != "Work" {
		t.Fatalf("groups = %+v", rooms.Groups)
	}
}

// TestChatMembersWireContract pins the member listing endpoint and its page
// total.
func TestChatMembersWireContract(t *testing.T) {
	var take string
	client, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/messager/chat/r1/members" {
			t.Errorf("path = %q", r.URL.Path)
		}
		take = r.URL.Query().Get("take")
		w.Header().Set("X-Total", "2")
		io.WriteString(w, `[
			{"id":"m1","account_id":"a1","account":{"id":"a1","name":"alice","nick":"Alice"}},
			{"id":"m2","account_id":"a2","account":{"id":"a2","name":"bob","nick":"Bob"}}
		]`)
	})

	page, err := client.ChatMembers(context.Background(), "r1", 0, 100)
	if err != nil {
		t.Fatalf("ChatMembers: %v", err)
	}
	if take != "100" || page.Total != 2 || len(page.Items) != 2 {
		t.Fatalf("take=%q total=%d items=%d", take, page.Total, len(page.Items))
	}
	if got := page.Items[0].DisplayName(); got != "Alice" {
		t.Errorf("member = %q", got)
	}
}

// TestChatRoomListDecodesDirectMembers pins the other-participant snapshot the
// server embeds in direct rooms.
func TestChatRoomListDecodesDirectMembers(t *testing.T) {
	client, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, `{"rooms":[{"id":"dm1","type":1,"direct_members":[
			{"account_id":"a1","account":{"id":"a1","name":"alice","nick":"Alice"}}
		]}]}`)
	})

	rooms, err := client.ChatRoomList(context.Background(), 0, 20)
	if err != nil {
		t.Fatalf("ChatRoomList: %v", err)
	}
	if len(rooms.Rooms) != 1 || len(rooms.Rooms[0].DirectMembers) != 1 {
		t.Fatalf("rooms = %+v", rooms.Rooms)
	}
	if got := rooms.Rooms[0].DirectMembers[0].DisplayName(); got != "Alice" {
		t.Errorf("direct member = %q", got)
	}
}

// TestRootEndpointDropsGateway pins how a custom gateway path resolves.
func TestRootEndpointDropsGateway(t *testing.T) {
	cases := map[string]string{
		"https://api.solian.app":                "https://api.solian.app",
		"https://api.solian.app/stargate":       "https://api.solian.app",
		"https://api.solian.app/proxy/stargate": "https://api.solian.app/proxy",
	}
	for raw, want := range cases {
		client, err := New(raw, "solian", "*", testDevice, "test")
		if err != nil {
			t.Fatalf("New(%q): %v", raw, err)
		}
		if got := client.rootEndpoint("/sphere/posts"); got != want+"/sphere/posts" {
			t.Errorf("rootEndpoint(%q) = %q, want %q", raw, got, want+"/sphere/posts")
		}
	}
}

// TestContentEndpointsUseRootPath keeps the content calls off the /stargate
// base: the gateways are siblings, not children.
func TestContentEndpointsUseRootPath(t *testing.T) {
	var paths []string
	client, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		paths = append(paths, r.URL.Path)
		switch r.URL.Path {
		case "/messager/chat":
			io.WriteString(w, `{"rooms":[],"groups":[]}`)
		case "/sphere/timeline":
			io.WriteString(w, `{"items":[]}`)
		default:
			io.WriteString(w, `[]`)
		}
	})

	ctx := context.Background()
	if _, err := client.Timeline(ctx, "", 1, "", true); err != nil {
		t.Fatalf("Timeline: %v", err)
	}
	if _, err := client.Publishers(ctx); err != nil {
		t.Fatalf("Publishers: %v", err)
	}
	if _, err := client.ChatRoomList(ctx, 0, 1); err != nil {
		t.Fatalf("ChatRoomList: %v", err)
	}

	for _, path := range paths {
		if strings.HasPrefix(path, "/stargate") {
			t.Fatalf("content request went to the auth gateway: %q", path)
		}
	}
}

// TestUpdatePostWireContract pins the patch payload and the publisher query.
func TestUpdatePostWireContract(t *testing.T) {
	var (
		body   map[string]any
		method string
		query  map[string][]string
	)
	client, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		method = r.Method
		query = r.URL.Query()
		if r.URL.Path != "/sphere/posts/p1" {
			t.Errorf("path = %q", r.URL.Path)
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		io.WriteString(w, `{"id":"p1","content":"rewritten"}`)
	})

	content := "rewritten"
	post, err := client.UpdatePost(context.Background(), "p1", PostPatch{
		Content:   &content,
		Publisher: "alice",
	})
	if err != nil {
		t.Fatalf("UpdatePost: %v", err)
	}
	if method != http.MethodPatch {
		t.Errorf("method = %q", method)
	}
	if post.Body() != "rewritten" {
		t.Errorf("post = %+v", post)
	}
	if got := body["content"]; got != "rewritten" {
		t.Errorf("content = %v", got)
	}
	if _, ok := body["title"]; ok {
		t.Errorf("an unset field was sent: %v", body)
	}
	if got := query["pub"]; len(got) != 1 || got[0] != "alice" {
		t.Errorf("pub query = %v", got)
	}
}

// TestDeletePostWireContract pins the delete verb and path.
func TestDeletePostWireContract(t *testing.T) {
	var method, path string
	client, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		method, path = r.Method, r.URL.Path
		w.WriteHeader(http.StatusNoContent)
	})

	if err := client.DeletePost(context.Background(), "p1"); err != nil {
		t.Fatalf("DeletePost: %v", err)
	}
	if method != http.MethodDelete || path != "/sphere/posts/p1" {
		t.Fatalf("request = %s %s", method, path)
	}
}

// TestChatMessageEditAndDelete cover the two message mutations.
func TestChatMessageEditAndDelete(t *testing.T) {
	var (
		patchBody map[string]any
		deleted   bool
	)
	client, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/messager/chat/r1/messages/m1" {
			t.Errorf("path = %q", r.URL.Path)
		}
		switch r.Method {
		case http.MethodPatch:
			_ = json.NewDecoder(r.Body).Decode(&patchBody)
			io.WriteString(w, `{"id":"m1","chat_room_id":"r1","type":"text","content":"edited"}`)
		case http.MethodDelete:
			deleted = true
			io.WriteString(w, `{"id":"m1","chat_room_id":"r1","type":"text"}`)
		default:
			t.Errorf("method = %q", r.Method)
		}
	})

	msg, err := client.EditChatMessage(context.Background(), "r1", "m1", "edited")
	if err != nil {
		t.Fatalf("EditChatMessage: %v", err)
	}
	if msg.Text() != "edited" || patchBody["content"] != "edited" {
		t.Fatalf("message = %+v body = %v", msg, patchBody)
	}

	if err := client.DeleteChatMessage(context.Background(), "r1", "m1"); err != nil {
		t.Fatalf("DeleteChatMessage: %v", err)
	}
	if !deleted {
		t.Fatal("delete was not sent")
	}
}

// TestFileURL pins the public download URL of an attachment.
func TestFileURL(t *testing.T) {
	client, _ := newTestClient(t, func(http.ResponseWriter, *http.Request) {})
	if got, want := client.FileURL("abc"), "http://"+client.Base.Host+"/drive/files/abc"; got != want {
		t.Fatalf("FileURL = %q, want %q", got, want)
	}
}

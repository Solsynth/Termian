package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// login runs a headless device login so the content commands have a session.
func login(t *testing.T, f *fakeServer) {
	t.Helper()
	if res := f.run(t, "", "login", "--output", "json"); res.code != 0 {
		t.Fatalf("login exit = %d (stderr %s)", res.code, res.stderr)
	}
}

// TestFeedReadsWithoutSession pins the guest contract of the feed command.
func TestFeedReadsWithoutSession(t *testing.T) {
	f := newFakeServer(t)

	res := f.run(t, "", "feed", "--limit", "2", "--output", "json")
	if res.code != 0 {
		t.Fatalf("feed exit = %d (stderr %s)", res.code, res.stderr)
	}
	records := ndjson(t, res.stdout)
	if len(records) != 3 {
		t.Fatalf("records = %v", records)
	}
	if meta := records[2]; meta["type"] != "timeline" || meta["next_cursor"] != "2026-11-07T00:00:00Z" {
		t.Fatalf("timeline meta = %v", meta)
	}
	post, ok := records[0]["post"].(map[string]any)
	if !ok || post["id"] != "p1" {
		t.Fatalf("post = %v", records[0]["post"])
	}
	if post["content"] != "hello world" {
		t.Fatalf("content = %v", post["content"])
	}
	if records[0]["type"] != "post" {
		t.Fatalf("type = %v", records[0]["type"])
	}

	text := f.run(t, "", "feed", "--limit", "2")
	if text.code != 0 {
		t.Fatalf("feed text exit = %d", text.code)
	}
	if !strings.Contains(text.stdout, "hello world") || !strings.Contains(text.stdout, "alice") {
		t.Fatalf("text output = %q", text.stdout)
	}
}

// TestPostShowsReplies covers the detail command and its reply records.
func TestPostShowsReplies(t *testing.T) {
	f := newFakeServer(t)

	res := f.run(t, "", "post", "p1", "--replies", "5", "--output", "json")
	if res.code != 0 {
		t.Fatalf("post exit = %d (stderr %s)", res.code, res.stderr)
	}
	records := ndjson(t, res.stdout)
	if len(records) != 2 {
		t.Fatalf("records = %v", records)
	}
	if records[0]["type"] != "post" {
		t.Fatalf("first record = %v", records[0])
	}
	reply, ok := records[1]["reply"].(map[string]any)
	if !ok || reply["id"] != "r1" {
		t.Fatalf("reply = %v", records[1])
	}
	if records[1]["type"] != "reply" {
		t.Fatalf("second record type = %v", records[1]["type"])
	}
}

// TestPublishAndReply covers both write paths and their payloads.
func TestPublishAndReply(t *testing.T) {
	f := newFakeServer(t)
	login(t, f)

	published := f.run(t, "", "publish", "--content", "hello world", "--publisher", "alice", "--output", "json")
	if published.code != 0 {
		t.Fatalf("publish exit = %d (stderr %s)", published.code, published.stderr)
	}
	records := ndjson(t, published.stdout)
	if len(records) != 1 || records[0]["type"] != "created" {
		t.Fatalf("records = %v", records)
	}
	if len(f.created) != 1 || f.created[0]["content"] != "hello world" {
		t.Fatalf("created payload = %v", f.created)
	}

	replied := f.run(t, "", "reply", "p1", "--content", "a reply", "--output", "json")
	if replied.code != 0 {
		t.Fatalf("reply exit = %d (stderr %s)", replied.code, replied.stderr)
	}
	if len(f.created) != 2 || f.created[1]["replied_post_id"] != "p1" {
		t.Fatalf("created payload = %v", f.created)
	}
}

// TestPublishFromFile covers --file and the empty-post rejection.
func TestPublishFromFile(t *testing.T) {
	f := newFakeServer(t)
	login(t, f)

	res := f.run(t, "from stdin", "publish", "--file", "-", "--output", "json")
	if res.code != 0 {
		t.Fatalf("publish exit = %d (stderr %s)", res.code, res.stderr)
	}
	if len(f.created) != 1 || f.created[0]["content"] != "from stdin" {
		t.Fatalf("created payload = %v", f.created)
	}

	empty := f.run(t, "", "publish", "--content", "  ", "--output", "json")
	if empty.code != 2 {
		t.Fatalf("empty publish exit = %d, want 2 (stderr %s)", empty.code, empty.stderr)
	}
}

// TestSaveAndReact cover the toggles and their records.
func TestSaveAndReact(t *testing.T) {
	f := newFakeServer(t)
	login(t, f)

	saved := f.run(t, "", "save", "p1", "--output", "json")
	if saved.code != 0 {
		t.Fatalf("save exit = %d (stderr %s)", saved.code, saved.stderr)
	}
	records := ndjson(t, saved.stdout)
	if len(records) != 1 || records[0]["is_bookmarked"] != true {
		t.Fatalf("records = %v", records)
	}

	removed := f.run(t, "", "save", "p1", "--remove", "--output", "json")
	if removed.code != 0 {
		t.Fatalf("save --remove exit = %d (stderr %s)", removed.code, removed.stderr)
	}
	if recs := ndjson(t, removed.stdout); len(recs) != 1 || recs[0]["is_bookmarked"] != false {
		t.Fatalf("records = %v", recs)
	}

	// The fake answers 204, which the API reads as "reaction removed".
	reacted := f.run(t, "", "react", "p1", "--symbol", "heart", "--output", "json")
	if reacted.code != 0 {
		t.Fatalf("react exit = %d (stderr %s)", reacted.code, reacted.stderr)
	}
	if recs := ndjson(t, reacted.stdout); len(recs) != 1 || recs[0]["is_present"] != false {
		t.Fatalf("records = %v", recs)
	}
}

// TestBookmarksAndPublishersNeedSession keeps exit 3 for both.
func TestBookmarksAndPublishersNeedSession(t *testing.T) {
	f := newFakeServer(t)

	for _, args := range [][]string{
		{"bookmarks"},
		{"publishers"},
		{"chats"},
		{"chat", "r1"},
	} {
		res := f.run(t, "", append(args, "--output", "json")...)
		if res.code != 3 {
			t.Fatalf("%v exit = %d, want 3 (stderr %s)", args, res.code, res.stderr)
		}
	}
}

// TestChatsAndChatLog covers the room list and the transcript order.
func TestChatsAndChatLog(t *testing.T) {
	f := newFakeServer(t)
	login(t, f)

	rooms := f.run(t, "", "chats", "--output", "json")
	if rooms.code != 0 {
		t.Fatalf("chats exit = %d (stderr %s)", rooms.code, rooms.stderr)
	}
	records := ndjson(t, rooms.stdout)
	if len(records) != 1 || records[0]["type"] != "chat_room" {
		t.Fatalf("records = %v", records)
	}
	room, ok := records[0]["room"].(map[string]any)
	if !ok || room["title"] != "General" || room["unread"] != float64(3) {
		t.Fatalf("room = %v", records[0]["room"])
	}

	log := f.run(t, "", "chat", "r1", "--output", "json")
	if log.code != 0 {
		t.Fatalf("chat exit = %d (stderr %s)", log.code, log.stderr)
	}
	messages := ndjson(t, log.stdout)
	if len(messages) != 2 {
		t.Fatalf("messages = %v", messages)
	}
	first, _ := messages[0]["message"].(map[string]any)
	if first["id"] != "m1" {
		t.Fatalf("messages are not oldest first: %v", messages)
	}
}

// TestChatSend covers the send path and its record.
func TestChatSend(t *testing.T) {
	f := newFakeServer(t)
	login(t, f)

	res := f.run(t, "", "chat", "r1", "--send", "--content", "hello", "--output", "json")
	if res.code != 0 {
		t.Fatalf("chat --send exit = %d (stderr %s)", res.code, res.stderr)
	}
	records := ndjson(t, res.stdout)
	if len(records) != 1 || records[0]["type"] != "chat_sent" {
		t.Fatalf("records = %v", records)
	}
	if len(f.sent) != 1 || f.sent[0]["content"] != "hello" {
		t.Fatalf("sent payload = %v", f.sent)
	}
	if id, _ := f.sent[0]["client_message_id"].(string); !strings.HasPrefix(id, "termian-") {
		t.Fatalf("client_message_id = %v", f.sent[0]["client_message_id"])
	}
}

// TestPublishArticleWithTagsAndAttachment covers the richer publish flags.
func TestPublishArticleWithTagsAndAttachment(t *testing.T) {
	f := newFakeServer(t)
	login(t, f)

	dir := t.TempDir()
	article := filepath.Join(dir, "notes.md")
	if err := os.WriteFile(article, []byte("# Markdown article\n\nBody.\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	attachment := filepath.Join(dir, "cover.png")
	if err := os.WriteFile(attachment, []byte("png"), 0o600); err != nil {
		t.Fatal(err)
	}

	res := f.run(t, "", "publish", "--type", "article", "--file", article,
		"--tag", "go,tui", "--description", "a description", "--visibility", "2",
		"--attach", attachment, "--output", "json")
	if res.code != 0 {
		t.Fatalf("publish exit = %d (stderr %s)", res.code, res.stderr)
	}
	if len(f.created) != 1 {
		t.Fatalf("created = %v", f.created)
	}
	created := f.created[0]
	if created["type"] != float64(1) || created["title"] != "Markdown article" {
		t.Fatalf("created = %v", created)
	}
	if created["description"] != "a description" || created["visibility"] != float64(2) {
		t.Fatalf("created = %v", created)
	}
	tags, _ := created["tags"].([]any)
	if len(tags) != 2 || tags[0] != "go" || tags[1] != "tui" {
		t.Fatalf("tags = %v", created["tags"])
	}
	ids, _ := created["attachments"].([]any)
	if len(ids) != 1 || ids[0] != "file-1" {
		t.Fatalf("attachments = %v", created["attachments"])
	}
}

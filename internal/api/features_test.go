package api

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gorilla/websocket"

	"src.solsynth.dev/sosys/termian/internal/credentials"
)

// TestNotificationsWireContract pins the Ring endpoints used by the TUI.
func TestNotificationsWireContract(t *testing.T) {
	var paths []string
	client, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		paths = append(paths, r.Method+" "+r.URL.Path)
		switch {
		case r.URL.Path == "/metoer/notifications" && r.Method == http.MethodGet:
			if got := r.URL.Query().Get("take"); got != "20" {
				t.Errorf("take = %q", got)
			}
			io.WriteString(w, `[{"id":"n1","topic":"post.replies","title":"Alice replied","content":"hello","viewed_at":null,"created_at":"2026-11-09T12:00:00Z","meta":{"action_uri":"solian://posts/p1"}}]`)
		case r.URL.Path == "/metoer/notifications/count":
			io.WriteString(w, `3`)
		case r.URL.Path == "/metoer/notifications/n1/read":
			w.WriteHeader(http.StatusNoContent)
		case r.URL.Path == "/metoer/notifications/all/read":
			w.WriteHeader(http.StatusNoContent)
		case r.URL.Path == "/metoer/notifications/preferences":
			io.WriteString(w, `[{"topic":"post.replies","preference":1}]`)
		case r.URL.Path == "/metoer/notifications/preferences/post.replies" && r.Method == http.MethodPut:
			var body map[string]any
			_ = json.NewDecoder(r.Body).Decode(&body)
			if body["preference"] != float64(2) {
				t.Errorf("preference = %v", body["preference"])
			}
			w.WriteHeader(http.StatusNoContent)
		case r.URL.Path == "/metoer/notifications/n1" && r.Method == http.MethodDelete:
			w.WriteHeader(http.StatusNoContent)
		default:
			t.Errorf("unexpected %s %s", r.Method, r.URL.Path)
		}
	})

	page, err := client.Notifications(context.Background(), 0, 20)
	if err != nil {
		t.Fatalf("Notifications: %v", err)
	}
	if len(page.Items) != 1 || page.Items[0].ID != "n1" || !page.Items[0].Unread() {
		t.Fatalf("page = %+v", page)
	}
	if got := page.Items[0].ActionURI(); got != "solian://posts/p1" {
		t.Fatalf("action uri = %q", got)
	}
	if count, err := client.UnreadNotificationCount(context.Background()); err != nil || count != 3 {
		t.Fatalf("count = %d, %v", count, err)
	}
	if err := client.MarkNotificationRead(context.Background(), "n1"); err != nil {
		t.Fatal(err)
	}
	if err := client.MarkAllNotificationsRead(context.Background()); err != nil {
		t.Fatal(err)
	}
	prefs, err := client.NotificationPreferences(context.Background())
	if err != nil || len(prefs) != 1 || prefs[0].Preference != NotificationPreferenceSilent {
		t.Fatalf("prefs = %+v, %v", prefs, err)
	}
	if err := client.SetNotificationPreference(context.Background(), "post.replies", NotificationPreferenceReject); err != nil {
		t.Fatal(err)
	}
	if err := client.DeleteNotification(context.Background(), "n1"); err != nil {
		t.Fatal(err)
	}
	if len(paths) != 7 {
		t.Fatalf("requests = %v", paths)
	}
}

// TestUploadFileMultipart covers the direct Drive upload shape.
func TestUploadFileMultipart(t *testing.T) {
	client, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/drive/files/upload/direct" {
			t.Errorf("request = %s %s", r.Method, r.URL.Path)
		}
		if !strings.HasPrefix(r.Header.Get("Content-Type"), "multipart/form-data") {
			t.Errorf("content type = %q", r.Header.Get("Content-Type"))
		}
		if err := r.ParseMultipartForm(1 << 20); err != nil {
			t.Fatalf("parse multipart: %v", err)
		}
		file, header, err := r.FormFile("file")
		if err != nil {
			t.Fatalf("form file: %v", err)
		}
		defer file.Close()
		if header.Filename != "notes.md" {
			t.Errorf("filename = %q", header.Filename)
		}
		if got := r.FormValue("content_type"); got != "text/markdown" {
			t.Errorf("content_type = %q", got)
		}
		io.WriteString(w, `{"id":"file-1","name":"notes.md","mime_type":"text/markdown","size":6}`)
	})

	file, err := client.UploadFile(context.Background(), "notes.md", []byte("# hi\n"), "text/markdown")
	if err != nil {
		t.Fatalf("UploadFile: %v", err)
	}
	if file.ID != "file-1" || file.Name != "notes.md" {
		t.Fatalf("file = %+v", file)
	}
}

// TestRealtimeEndpoint covers the gateway URL and namespace.
func TestRealtimeEndpoint(t *testing.T) {
	client, _ := newTestClient(t, func(http.ResponseWriter, *http.Request) {})
	rt := NewRealtime(client, nil)
	got, err := rt.endpoint()
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(got, "http") && !strings.HasPrefix(got, "ws") {
		t.Fatalf("scheme not websocket: %q", got)
	}
	if !strings.Contains(got, "/ws?namespace="+RealtimeNamespace) {
		t.Fatalf("endpoint = %q", got)
	}
}

// TestRealtimeDeliversAndAcks drives one websocket session: the client resumes
// the buffer, receives a pushed frame and acknowledges its delivery stamp.
func TestRealtimeDeliversAndAcks(t *testing.T) {
	upgrader := websocket.Upgrader{CheckOrigin: func(*http.Request) bool { return true }}
	var gotAuth, gotNamespace, gotResume, gotAck atomic.Bool
	client, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/ws" {
			t.Errorf("path = %s", r.URL.Path)
		}
		gotNamespace.Store(r.URL.Query().Get("namespace") == RealtimeNamespace)
		gotAuth.Store(r.Header.Get("Authorization") == "Bearer rt-access")
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			t.Errorf("upgrade: %v", err)
			return
		}
		defer conn.Close()

		for range 2 {
			_, raw, err := conn.ReadMessage()
			if err != nil {
				return
			}
			var frame map[string]any
			_ = json.Unmarshal(raw, &frame)
			switch frame["type"] {
			case "session.resume":
				gotResume.Store(true)
			case "ping":
			}
		}
		_ = conn.WriteMessage(websocket.TextMessage, []byte(`{"type":"notifications.new","data":{"id":"n1","title":"live"},"_ack_id":"ack-1"}`))
		for range 4 {
			_, raw, err := conn.ReadMessage()
			if err != nil {
				return
			}
			if strings.Contains(string(raw), `"session.ack"`) && strings.Contains(string(raw), `"ack-1"`) {
				gotAck.Store(true)
				return
			}
		}
	})

	store, err := credentials.Open(credentials.ModeFile, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	tokens := NewTokenSource(client, store, &credentials.Credentials{Server: "test"})
	tokens.Seed(&credentials.Credentials{Server: "test", RefreshToken: "rt", RefreshExpiresAt: time.Now().Add(time.Hour)}, "rt-access", 3600)
	rt := NewRealtime(client, tokens)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go rt.Run(ctx)

	select {
	case packet := <-rt.Events():
		if packet.Type != "notifications.new" || packet.AckID != "ack-1" {
			t.Fatalf("packet = %+v", packet)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for a websocket frame")
	}
	deadline := time.Now().Add(2 * time.Second)
	for !gotAck.Load() && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if !gotAuth.Load() || !gotNamespace.Load() || !gotResume.Load() || !gotAck.Load() {
		t.Fatalf("auth=%v namespace=%v resume=%v ack=%v", gotAuth.Load(), gotNamespace.Load(), gotResume.Load(), gotAck.Load())
	}
}

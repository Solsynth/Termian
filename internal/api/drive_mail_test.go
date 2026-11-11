package api

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"testing"
)

func TestDriveWireContract(t *testing.T) {
	client, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/drive/files/root/children" && r.Method == http.MethodGet:
			w.Header().Set("X-Total", "1")
			io.WriteString(w, `[{"id":"f1","name":"Folder A","is_folder":true,"indexed":true}]`)
		case r.URL.Path == "/drive/files/f1/children" && r.Method == http.MethodGet:
			w.Header().Set("X-Total", "1")
			io.WriteString(w, `[{"id":"f2","name":"Doc.pdf","is_folder":false,"size":1024}]`)
		case r.URL.Path == "/drive/files/f2/info" && r.Method == http.MethodGet:
			io.WriteString(w, `{"id":"f2","name":"Doc.pdf","size":1024,"mime_type":"application/pdf"}`)
		case r.URL.Path == "/drive/files/folders" && r.Method == http.MethodPost:
			var body map[string]any
			_ = json.NewDecoder(r.Body).Decode(&body)
			if body["name"] != "NewFolder" {
				t.Errorf("folder name = %v", body["name"])
			}
			w.WriteHeader(http.StatusCreated)
			io.WriteString(w, `{"id":"f3","name":"NewFolder","is_folder":true}`)
		case r.URL.Path == "/drive/files/f2" && r.Method == http.MethodPatch:
			var body map[string]any
			_ = json.NewDecoder(r.Body).Decode(&body)
			if body["name"] != "Doc-Renamed.pdf" {
				t.Errorf("renamed name = %v", body["name"])
			}
			io.WriteString(w, `{"id":"f2","name":"Doc-Renamed.pdf"}`)
		case r.URL.Path == "/drive/files/f2/recycle" && r.Method == http.MethodPost:
			w.WriteHeader(http.StatusOK)
		case r.URL.Path == "/drive/files/f2/restore" && r.Method == http.MethodPost:
			w.WriteHeader(http.StatusOK)
		case r.URL.Path == "/drive/files/f2" && r.Method == http.MethodDelete:
			w.WriteHeader(http.StatusNoContent)
		case r.URL.Path == "/drive/files/f2" && r.Method == http.MethodGet:
			w.Header().Set("Content-Disposition", `attachment; filename="Doc.pdf"`)
			io.WriteString(w, "PDF-BODY-CONTENT")
		case r.URL.Path == "/drive/billing/usage" && r.Method == http.MethodGet:
			io.WriteString(w, `{"used_quota":2048,"total_quota":10485760,"total_file_count":12,"total_usage_bytes":2048}`)
		case r.URL.Path == "/drive/billing/quota" && r.Method == http.MethodGet:
			io.WriteString(w, `{"total_quota":10485760,"based_quota":5242880}`)
		default:
			t.Errorf("unexpected drive call %s %s", r.Method, r.URL.Path)
		}
	})

	ctx := context.Background()

	// 1. Root listing
	page, err := client.DriveList(ctx, "", 0, 50)
	if err != nil {
		t.Fatalf("DriveList root: %v", err)
	}
	if len(page.Items) != 1 || page.Items[0].Name != "Folder A" || !page.Items[0].IsFolder {
		t.Errorf("unexpected root page items: %+v", page.Items)
	}

	// 2. Folder listing
	childPage, err := client.DriveList(ctx, "f1", 0, 50)
	if err != nil {
		t.Fatalf("DriveList children: %v", err)
	}
	if len(childPage.Items) != 1 || childPage.Items[0].FileSize() != 1024 {
		t.Errorf("unexpected child page items: %+v", childPage.Items)
	}

	// 3. File Info
	info, err := client.DriveFileInfo(ctx, "f2")
	if err != nil {
		t.Fatalf("DriveFileInfo: %v", err)
	}
	if info.Name != "Doc.pdf" || info.FileMimeType() != "application/pdf" {
		t.Errorf("unexpected info: %+v", info)
	}

	// 4. Create Folder
	createdFolder, err := client.DriveCreateFolder(ctx, "NewFolder", "")
	if err != nil {
		t.Fatalf("DriveCreateFolder: %v", err)
	}
	if createdFolder.Name != "NewFolder" {
		t.Errorf("unexpected created folder: %+v", createdFolder)
	}

	// 5. Rename
	renamed, err := client.DriveRename(ctx, "f2", "Doc-Renamed.pdf")
	if err != nil {
		t.Fatalf("DriveRename: %v", err)
	}
	if renamed.Name != "Doc-Renamed.pdf" {
		t.Errorf("unexpected rename: %+v", renamed)
	}

	// 6. Recycle, restore, delete
	if err := client.DriveRecycle(ctx, "f2"); err != nil {
		t.Fatalf("DriveRecycle: %v", err)
	}
	if err := client.DriveRestore(ctx, "f2"); err != nil {
		t.Fatalf("DriveRestore: %v", err)
	}
	if err := client.DriveDelete(ctx, "f2"); err != nil {
		t.Fatalf("DriveDelete: %v", err)
	}

	// 7. Download
	bytes, filename, err := client.DriveDownload(ctx, "f2")
	if err != nil {
		t.Fatalf("DriveDownload: %v", err)
	}
	if string(bytes) != "PDF-BODY-CONTENT" || filename != "Doc.pdf" {
		t.Errorf("unexpected download: %q, filename %q", string(bytes), filename)
	}

	// 8. Usage & Quota
	usage, err := client.DriveUsage(ctx)
	if err != nil {
		t.Fatalf("DriveUsage: %v", err)
	}
	if usage.TotalUsageBytes != 2048 || usage.TotalFileCount != 12 {
		t.Errorf("unexpected usage: %+v", usage)
	}
	quota, err := client.DriveQuota(ctx)
	if err != nil {
		t.Fatalf("DriveQuota: %v", err)
	}
	if quota.TotalQuota != 10485760 {
		t.Errorf("unexpected quota: %+v", quota)
	}
}

func TestMailWireContract(t *testing.T) {
	client, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/postal/mailboxes" && r.Method == http.MethodGet:
			io.WriteString(w, `[{"id":"mb1","address":"test@solsynth.dev","name":"Primary","is_default":true}]`)
		case r.URL.Path == "/postal/mailboxes/mb1/threads" && r.Method == http.MethodGet:
			w.Header().Set("X-Total", "1")
			io.WriteString(w, `[{"id":"th1","mailbox_id":"mb1","subject":"Welcome","message_count":1,"unread_count":1}]`)
		case r.URL.Path == "/postal/threads/th1" && r.Method == http.MethodGet:
			io.WriteString(w, `[{"id":"em1","mailbox_id":"mb1","thread_id":"th1","subject":"Welcome","body":"Hello there!","from_address":"sender@solsynth.dev","from_name":"Sender","is_read":false}]`)
		case r.URL.Path == "/postal/emails/em1" && r.Method == http.MethodGet:
			io.WriteString(w, `{"id":"em1","mailbox_id":"mb1","subject":"Welcome","body":"Hello there!"}`)
		case r.URL.Path == "/postal/emails" && r.Method == http.MethodPost:
			var input SendMailInput
			_ = json.NewDecoder(r.Body).Decode(&input)
			if input.Subject != "Greetings" {
				t.Errorf("subject = %v", input.Subject)
			}
			w.WriteHeader(http.StatusCreated)
			io.WriteString(w, `{"id":"em2","mailbox_id":"mb1","subject":"Greetings","body":"Sent body"}`)
		case r.URL.Path == "/postal/emails/em1/read" && r.Method == http.MethodPost:
			w.WriteHeader(http.StatusOK)
		case r.URL.Path == "/postal/emails/em1/star" && r.Method == http.MethodPost:
			w.WriteHeader(http.StatusOK)
		case r.URL.Path == "/postal/emails/em1/move" && r.Method == http.MethodPost:
			w.WriteHeader(http.StatusOK)
		case r.URL.Path == "/postal/emails/em1" && r.Method == http.MethodDelete:
			w.WriteHeader(http.StatusNoContent)
		default:
			t.Errorf("unexpected mail call %s %s", r.Method, r.URL.Path)
		}
	})

	ctx := context.Background()

	// 1. Mailboxes
	mbs, err := client.Mailboxes(ctx)
	if err != nil {
		t.Fatalf("Mailboxes: %v", err)
	}
	if len(mbs) != 1 || mbs[0].Address != "test@solsynth.dev" {
		t.Errorf("unexpected mailboxes: %+v", mbs)
	}

	// 2. Threads
	threads, err := client.MailThreads(ctx, "mb1", "inbox", 0, 20)
	if err != nil {
		t.Fatalf("MailThreads: %v", err)
	}
	if len(threads.Items) != 1 || threads.Items[0].Subject != "Welcome" {
		t.Errorf("unexpected threads: %+v", threads.Items)
	}

	// 3. Thread details
	msgs, err := client.MailThread(ctx, "th1")
	if err != nil {
		t.Fatalf("MailThread: %v", err)
	}
	if len(msgs) != 1 || msgs[0].Body != "Hello there!" {
		t.Errorf("unexpected msgs: %+v", msgs)
	}

	// 4. Send mail
	created, err := client.SendMail(ctx, SendMailInput{
		MailboxID: "mb1",
		Subject:   "Greetings",
		Body:      "Hi!",
		To:        []MailRecipient{{Address: "dest@solsynth.dev"}},
	})
	if err != nil {
		t.Fatalf("SendMail: %v", err)
	}
	if created.Subject != "Greetings" {
		t.Errorf("unexpected created email: %+v", created)
	}

	// 5. Read, Star, Move, Delete
	if err := client.MarkMailRead(ctx, "em1", true); err != nil {
		t.Fatalf("MarkMailRead: %v", err)
	}
	if err := client.StarMail(ctx, "em1", true); err != nil {
		t.Fatalf("StarMail: %v", err)
	}
	if err := client.MoveMail(ctx, "em1", "trash"); err != nil {
		t.Fatalf("MoveMail: %v", err)
	}
	if err := client.DeleteMail(ctx, "em1", false); err != nil {
		t.Fatalf("DeleteMail: %v", err)
	}
}

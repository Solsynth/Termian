package tui

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"src.solsynth.dev/sosys/termian/internal/api"
)

func TestTUIDrive(t *testing.T) {
	fake := &fakeAPI{}
	content := newFakeContent()
	deps := newContentDeps(t, fake, content, true)

	model := NewModelWithScreen(deps, ScreenFeed)
	model.Width, model.Height, model.sized = 100, 30, true

	// 1. Switch to Drive with key "6"
	updated, cmd := model.Update(tea.KeyPressMsg{Code: '6', Text: "6"})
	m := updated.(*Model)
	if m.Screen != ScreenDrive {
		t.Fatalf("screen = %s, want ScreenDrive", m.Screen)
	}
	if cmd == nil {
		t.Fatal("switching to drive did not trigger load command")
	}

	// 2. Simulate load message
	updated, _ = m.Update(driveLoadedMsg{
		folderID: "",
		page:     content.feedDrivePage(),
	})
	m = updated.(*Model)
	if len(m.Drive.items) != 2 || !m.Drive.loaded {
		t.Fatalf("drive items not loaded: %+v", m.Drive.items)
	}

	// 3. View contains folder and file
	view := m.driveView()
	if !strings.Contains(view, "Folder A") || !strings.Contains(view, "Doc.txt") {
		t.Fatalf("driveView missing items: %s", view)
	}

	// 4. Cursor down
	updated, _ = m.Update(tea.KeyPressMsg{Code: 'j', Text: "j"})
	m = updated.(*Model)
	if m.Drive.selected != 1 {
		t.Fatalf("selected = %d, want 1", m.Drive.selected)
	}

	// 5. Cursor up
	updated, _ = m.Update(tea.KeyPressMsg{Code: 'k', Text: "k"})
	m = updated.(*Model)
	if m.Drive.selected != 0 {
		t.Fatalf("selected = %d, want 0", m.Drive.selected)
	}

	// 6. Enter folder
	updated, cmd = m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	m = updated.(*Model)
	if m.Drive.currentFolderID != "f1" || m.Drive.currentFolderName != "Folder A" {
		t.Fatalf("did not enter folder: id=%s name=%s", m.Drive.currentFolderID, m.Drive.currentFolderName)
	}
	if cmd == nil {
		t.Fatal("entering folder did not trigger load cmd")
	}

	// 7. Back to parent
	updated, cmd = m.Update(tea.KeyPressMsg{Code: tea.KeyBackspace})
	m = updated.(*Model)
	if m.Drive.currentFolderID != "" {
		t.Fatalf("did not return to root: id=%s", m.Drive.currentFolderID)
	}
	if cmd == nil {
		t.Fatal("leaving folder did not trigger load cmd")
	}

	// 8. New folder prompt
	updated, _ = m.Update(tea.KeyPressMsg{Code: 'n', Text: "n"})
	m = updated.(*Model)
	if !m.Drive.creatingFolder {
		t.Fatal("pressing n did not open creatingFolder prompt")
	}
	updated, _ = m.Update(tea.KeyPressMsg{Code: 'P', Text: "P"})
	m = updated.(*Model)
	if m.Drive.newFolderName != "P" {
		t.Fatalf("typed name = %s, want P", m.Drive.newFolderName)
	}
	updated, _ = m.Update(tea.KeyPressMsg{Code: tea.KeyEsc})
	m = updated.(*Model)
	if m.Drive.creatingFolder {
		t.Fatal("Esc did not cancel creatingFolder")
	}
}

func TestTUIMail(t *testing.T) {
	fake := &fakeAPI{}
	content := newFakeContent()
	deps := newContentDeps(t, fake, content, true)

	model := NewModelWithScreen(deps, ScreenFeed)
	model.Width, model.Height, model.sized = 100, 30, true

	// 1. Switch to Mail with key "5"
	updated, cmd := model.Update(tea.KeyPressMsg{Code: '5', Text: "5"})
	m := updated.(*Model)
	if m.Screen != ScreenMail {
		t.Fatalf("screen = %s, want ScreenMail", m.Screen)
	}
	if cmd == nil {
		t.Fatal("switching to mail did not trigger load command")
	}

	// 2. Simulate mailbox load
	boxes, _ := content.Mailboxes(deps.Ctx)
	updated, cmd = m.Update(mailboxesLoadedMsg{mailboxes: boxes})
	m = updated.(*Model)
	if len(m.Mail.mailboxes) != 1 || m.Mail.currentMailboxAddress() != "user@solian.app" {
		t.Fatalf("mailboxes not loaded: %+v", m.Mail.mailboxes)
	}
	if cmd == nil {
		t.Fatal("mailboxes loaded did not trigger threads load")
	}

	// 3. Simulate threads load
	threads, _ := content.MailThreads(deps.Ctx, "mb1", "inbox", 0, 50)
	updated, _ = m.Update(mailThreadsLoadedMsg{mailboxID: "mb1", folder: "inbox", page: threads})
	m = updated.(*Model)
	if len(m.Mail.threads) != 1 || !m.Mail.loaded {
		t.Fatalf("threads not loaded: %+v", m.Mail.threads)
	}

	// 4. View renders thread
	view := m.mailView()
	if !strings.Contains(view, "Hello") || !strings.Contains(view, "user@solian.app") {
		t.Fatalf("mailView missing content: %s", view)
	}

	// 5. Open thread
	updated, cmd = m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	m = updated.(*Model)
	if cmd == nil {
		t.Fatal("Enter did not load thread messages")
	}

	// 6. Simulate thread messages loaded
	emails, _ := content.MailThread(deps.Ctx, "th1")
	updated, _ = m.Update(mailThreadLoadedMsg{threadID: "th1", emails: emails})
	m = updated.(*Model)
	if m.Screen != ScreenMailThread || len(m.Mail.threadEmails) != 1 {
		t.Fatalf("screen = %s, emails = %d", m.Screen, len(m.Mail.threadEmails))
	}
	threadView := m.mailThreadView()
	if !strings.Contains(threadView, "Welcome to Solian!") {
		t.Fatalf("threadView missing body: %s", threadView)
	}

	// 7. Reply
	updated, _ = m.Update(tea.KeyPressMsg{Code: 'r', Text: "r"})
	m = updated.(*Model)
	if m.Screen != ScreenMailCompose || !m.Mail.composing {
		t.Fatalf("screen = %s, composing = %v", m.Screen, m.Mail.composing)
	}
	if m.Mail.composeTo != "bob@solian.app" || m.Mail.composeSubject != "Re: Hello" {
		t.Fatalf("unexpected compose fields: To=%s Subj=%s", m.Mail.composeTo, m.Mail.composeSubject)
	}

	// 8. Cancel compose with Esc
	updated, _ = m.Update(tea.KeyPressMsg{Code: tea.KeyEsc})
	m = updated.(*Model)
	if m.Screen != ScreenMail || m.Mail.composing {
		t.Fatalf("screen = %s, composing = %v", m.Screen, m.Mail.composing)
	}
}

func (f *fakeContent) feedDrivePage() api.Page[api.CloudFile] {
	return api.Page[api.CloudFile]{
		Items: []api.CloudFile{
			{ID: "f1", Name: "Folder A", IsFolder: true},
			{ID: "f2", Name: "Doc.txt", Size: 100},
		},
		Total: 2,
	}
}

package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestDriveCLI(t *testing.T) {
	f := newFakeServer(t)
	login(t, f)

	// 1. drive ls
	res := f.run(t, "", "drive", "ls", "--output", "json")
	if res.code != 0 {
		t.Fatalf("drive ls exit = %d (%s)", res.code, res.stderr)
	}
	records := ndjson(t, res.stdout)
	if len(records) != 1 || records[0]["type"] != "drive_file" {
		t.Fatalf("unexpected drive records: %v", records)
	}

	// 2. drive mkdir
	res = f.run(t, "", "drive", "mkdir", "Photos", "--output", "json")
	if res.code != 0 {
		t.Fatalf("drive mkdir exit = %d (%s)", res.code, res.stderr)
	}
	records = ndjson(t, res.stdout)
	if len(records) != 1 || records[0]["type"] != "drive_file" {
		t.Fatalf("unexpected mkdir records: %v", records)
	}

	// 3. drive upload
	tmpFile := filepath.Join(t.TempDir(), "notes.md")
	if err := os.WriteFile(tmpFile, []byte("my note"), 0o644); err != nil {
		t.Fatal(err)
	}
	res = f.run(t, "", "drive", "upload", tmpFile, "--output", "json")
	if res.code != 0 {
		t.Fatalf("drive upload exit = %d (%s)", res.code, res.stderr)
	}

	// 4. drive download
	dlOut := filepath.Join(t.TempDir(), "downloaded.txt")
	res = f.run(t, "", "drive", "download", "f1", "--out", dlOut, "--output", "json")
	if res.code != 0 {
		t.Fatalf("drive download exit = %d (%s)", res.code, res.stderr)
	}
	content, err := os.ReadFile(dlOut)
	if err != nil || string(content) != "file content here" {
		t.Fatalf("unexpected downloaded content: %q (%v)", string(content), err)
	}

	// 5. drive quota
	res = f.run(t, "", "drive", "quota", "--output", "json")
	if res.code != 0 {
		t.Fatalf("drive quota exit = %d (%s)", res.code, res.stderr)
	}
	records = ndjson(t, res.stdout)
	if len(records) != 1 || records[0]["type"] != "drive_quota" {
		t.Fatalf("unexpected quota records: %v", records)
	}

	// 6. drive rm
	res = f.run(t, "", "drive", "rm", "f1", "--output", "json")
	if res.code != 0 {
		t.Fatalf("drive rm exit = %d (%s)", res.code, res.stderr)
	}

	// 7. Text mode outputs
	textLs := f.run(t, "", "drive", "ls")
	if textLs.code != 0 || !strings.Contains(textLs.stdout, "Docs") {
		t.Fatalf("drive ls text: %s", textLs.stdout)
	}

	textQuota := f.run(t, "", "drive", "quota")
	if textQuota.code != 0 || !strings.Contains(textQuota.stdout, "Storage:") {
		t.Fatalf("drive quota text: %s", textQuota.stdout)
	}
}

func TestMailCLI(t *testing.T) {
	f := newFakeServer(t)
	login(t, f)

	// 1. mail mailboxes
	res := f.run(t, "", "mail", "mailboxes", "--output", "json")
	if res.code != 0 {
		t.Fatalf("mail mailboxes exit = %d (%s)", res.code, res.stderr)
	}
	records := ndjson(t, res.stdout)
	if len(records) != 1 || records[0]["type"] != "mailbox" {
		t.Fatalf("unexpected mailbox records: %v", records)
	}

	// 2. mail list
	res = f.run(t, "", "mail", "list", "--mailbox", "mb1", "--output", "json")
	if res.code != 0 {
		t.Fatalf("mail list exit = %d (%s)", res.code, res.stderr)
	}
	records = ndjson(t, res.stdout)
	if len(records) != 1 || records[0]["type"] != "mail_thread" {
		t.Fatalf("unexpected mail list records: %v", records)
	}

	// 3. mail read
	res = f.run(t, "", "mail", "read", "th1", "--output", "json")
	if res.code != 0 {
		t.Fatalf("mail read exit = %d (%s)", res.code, res.stderr)
	}
	records = ndjson(t, res.stdout)
	if len(records) != 1 || records[0]["type"] != "mail_email" {
		t.Fatalf("unexpected mail read records: %v", records)
	}

	// 4. mail send
	res = f.run(t, "", "mail", "send", "--to", "bob@solian.app", "--subject", "Test Mail", "--body", "Hi", "--output", "json")
	if res.code != 0 {
		t.Fatalf("mail send exit = %d (%s)", res.code, res.stderr)
	}
	records = ndjson(t, res.stdout)
	if len(records) != 1 || records[0]["type"] != "mail_sent" {
		t.Fatalf("unexpected mail send records: %v", records)
	}

	// 5. mail star
	res = f.run(t, "", "mail", "star", "em1", "--output", "json")
	if res.code != 0 {
		t.Fatalf("mail star exit = %d (%s)", res.code, res.stderr)
	}

	// 6. mail delete
	res = f.run(t, "", "mail", "delete", "em1", "--output", "json")
	if res.code != 0 {
		t.Fatalf("mail delete exit = %d (%s)", res.code, res.stderr)
	}

	// 7. Text mode outputs
	textMb := f.run(t, "", "mail", "mailboxes")
	if textMb.code != 0 || !strings.Contains(textMb.stdout, "alice@solian.app") {
		t.Fatalf("mail mailboxes text: %s", textMb.stdout)
	}

	textList := f.run(t, "", "mail", "list", "--mailbox", "mb1")
	if textList.code != 0 || !strings.Contains(textList.stdout, "Welcome") {
		t.Fatalf("mail list text: %s", textList.stdout)
	}

	textRead := f.run(t, "", "mail", "read", "th1")
	if textRead.code != 0 || !strings.Contains(textRead.stdout, "Hello Alice!") {
		t.Fatalf("mail read text: %s", textRead.stdout)
	}
}

package tui

import (
	"context"
	"fmt"
	"strings"

	tea "charm.land/bubbletea/v2"

	"src.solsynth.dev/sosys/termian/internal/api"
)

type mailModel struct {
	mailboxes       []api.Mailbox
	selectedMailbox int

	folder         string
	threads        []api.MailThread
	selectedThread int
	loaded         bool
	loading        bool
	err            error

	// Thread reader state
	activeThreadID string
	threadEmails   []api.MailEmail
	threadLoading  bool
	threadSelected int

	// Mail composer state
	composing            bool
	composeMailboxID     string
	composeTo            string
	composeSubject       string
	composeBody          string
	composeField         int // 0: To, 1: Subject, 2: Body
	composeReplyThreadID string
}

func newMailModel() mailModel {
	return mailModel{
		folder: "inbox",
	}
}

func (m *mailModel) currentMailboxID() string {
	if m.selectedMailbox >= 0 && m.selectedMailbox < len(m.mailboxes) {
		return m.mailboxes[m.selectedMailbox].ID
	}
	return ""
}

func (m *mailModel) currentMailboxAddress() string {
	if m.selectedMailbox >= 0 && m.selectedMailbox < len(m.mailboxes) {
		return m.mailboxes[m.selectedMailbox].Address
	}
	return ""
}

func (m *mailModel) selectedThreadItem() *api.MailThread {
	if m.selectedThread < 0 || m.selectedThread >= len(m.threads) {
		return nil
	}
	return &m.threads[m.selectedThread]
}

// Mail messages
type mailboxesLoadedMsg struct {
	mailboxes []api.Mailbox
	err       error
}

type mailThreadsLoadedMsg struct {
	mailboxID string
	folder    string
	page      api.Page[api.MailThread]
	err       error
}

type mailThreadLoadedMsg struct {
	threadID string
	emails   []api.MailEmail
	err      error
}

type mailSentMsg struct {
	email api.MailEmail
	err   error
}

type mailActionMsg struct {
	emailID string
	action  string
	err     error
}

func (m *Model) loadMailboxesCmd() tea.Cmd {
	return m.withTimeout(func(ctx context.Context) tea.Msg {
		if m.Deps.Content == nil {
			return mailboxesLoadedMsg{err: nil}
		}
		if err := m.requireSession(ctx); err != nil {
			return mailboxesLoadedMsg{err: err}
		}
		boxes, err := m.Deps.Content.Mailboxes(ctx)
		return mailboxesLoadedMsg{mailboxes: boxes, err: err}
	})
}

func (m *Model) loadMailThreadsCmd(mailboxID, folder string) tea.Cmd {
	m.Mail.loading = true
	return m.withTimeout(func(ctx context.Context) tea.Msg {
		if m.Deps.Content == nil {
			return mailThreadsLoadedMsg{mailboxID: mailboxID, folder: folder, err: nil}
		}
		if err := m.requireSession(ctx); err != nil {
			return mailThreadsLoadedMsg{mailboxID: mailboxID, folder: folder, err: err}
		}
		page, err := m.Deps.Content.MailThreads(ctx, mailboxID, folder, 0, 50)
		return mailThreadsLoadedMsg{mailboxID: mailboxID, folder: folder, page: page, err: err}
	})
}

func (m *Model) loadMailThreadReaderCmd(threadID string) tea.Cmd {
	m.Mail.threadLoading = true
	m.Mail.activeThreadID = threadID
	return m.withTimeout(func(ctx context.Context) tea.Msg {
		if m.Deps.Content == nil {
			return mailThreadLoadedMsg{threadID: threadID, err: nil}
		}
		if err := m.requireSession(ctx); err != nil {
			return mailThreadLoadedMsg{threadID: threadID, err: err}
		}
		emails, err := m.Deps.Content.MailThread(ctx, threadID)
		return mailThreadLoadedMsg{threadID: threadID, emails: emails, err: err}
	})
}

func (m *Model) sendMailCmd(input api.SendMailInput) tea.Cmd {
	return m.withTimeout(func(ctx context.Context) tea.Msg {
		if m.Deps.Content == nil {
			return mailSentMsg{err: nil}
		}
		if err := m.requireSession(ctx); err != nil {
			return mailSentMsg{err: err}
		}
		email, err := m.Deps.Content.SendMail(ctx, input)
		return mailSentMsg{email: email, err: err}
	})
}

func (m *Model) starMailCmd(emailID string, star bool) tea.Cmd {
	return m.withTimeout(func(ctx context.Context) tea.Msg {
		if m.Deps.Content == nil {
			return mailActionMsg{emailID: emailID, action: "star", err: nil}
		}
		if err := m.requireSession(ctx); err != nil {
			return mailActionMsg{emailID: emailID, action: "star", err: err}
		}
		err := m.Deps.Content.StarMail(ctx, emailID, star)
		return mailActionMsg{emailID: emailID, action: "star", err: err}
	})
}

func (m *Model) moveMailCmd(emailID, folder string) tea.Cmd {
	return m.withTimeout(func(ctx context.Context) tea.Msg {
		if m.Deps.Content == nil {
			return mailActionMsg{emailID: emailID, action: "move", err: nil}
		}
		if err := m.requireSession(ctx); err != nil {
			return mailActionMsg{emailID: emailID, action: "move", err: err}
		}
		err := m.Deps.Content.MoveMail(ctx, emailID, folder)
		return mailActionMsg{emailID: emailID, action: "move", err: err}
	})
}

func (m *Model) updateMailMsg(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case mailboxesLoadedMsg:
		if msg.err != nil {
			m.setBanner("failed to load mailboxes: " + msg.err.Error())
			return m, nil
		}
		m.Mail.mailboxes = msg.mailboxes
		// Select default mailbox
		for i, mb := range m.Mail.mailboxes {
			if mb.IsDefault {
				m.Mail.selectedMailbox = i
				break
			}
		}
		return m, m.loadMailThreadsCmd(m.Mail.currentMailboxID(), m.Mail.folder)

	case mailThreadsLoadedMsg:
		m.Mail.loading = false
		m.Mail.loaded = true
		if msg.err != nil {
			m.Mail.err = msg.err
			m.setBanner("failed to load threads: " + msg.err.Error())
			return m, nil
		}
		m.Mail.err = nil
		m.Mail.threads = msg.page.Items
		if m.Mail.selectedThread >= len(m.Mail.threads) {
			m.Mail.selectedThread = 0
		}
		return m, nil

	case mailThreadLoadedMsg:
		m.Mail.threadLoading = false
		if msg.err != nil {
			m.setBanner("could not load conversation: " + msg.err.Error())
			return m, nil
		}
		m.Mail.threadEmails = msg.emails
		m.Mail.threadSelected = len(msg.emails) - 1
		m.showScreen(ScreenMailThread)
		return m, nil

	case mailSentMsg:
		if msg.err != nil {
			m.setBanner("failed to send email: " + msg.err.Error())
			return m, nil
		}
		m.setNotice("Email sent successfully")
		m.Mail.composing = false
		m.showScreen(ScreenMail)
		return m, m.loadMailThreadsCmd(m.Mail.currentMailboxID(), m.Mail.folder)

	case mailActionMsg:
		if msg.err != nil {
			m.setBanner("mail action failed: " + msg.err.Error())
			return m, nil
		}
		m.setNotice("Done")
		return m, m.loadMailThreadsCmd(m.Mail.currentMailboxID(), m.Mail.folder)
	}
	return m, nil
}

func (m *Model) updateMailKey(key tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	switch key.String() {
	case "j", "down":
		if len(m.Mail.threads) > 0 && m.Mail.selectedThread < len(m.Mail.threads)-1 {
			m.Mail.selectedThread++
		}
		return m, nil

	case "k", "up":
		if m.Mail.selectedThread > 0 {
			m.Mail.selectedThread--
		}
		return m, nil

	case "[":
		folders := []string{"inbox", "sent", "starred", "trash", "spam"}
		for i, f := range folders {
			if f == m.Mail.folder {
				prevIdx := (i - 1 + len(folders)) % len(folders)
				m.Mail.folder = folders[prevIdx]
				m.Mail.selectedThread = 0
				return m, m.loadMailThreadsCmd(m.Mail.currentMailboxID(), m.Mail.folder)
			}
		}
		return m, nil

	case "]":
		folders := []string{"inbox", "sent", "starred", "trash", "spam"}
		for i, f := range folders {
			if f == m.Mail.folder {
				nextIdx := (i + 1) % len(folders)
				m.Mail.folder = folders[nextIdx]
				m.Mail.selectedThread = 0
				return m, m.loadMailThreadsCmd(m.Mail.currentMailboxID(), m.Mail.folder)
			}
		}
		return m, nil

	case "m", "M":
		if len(m.Mail.mailboxes) > 1 {
			m.Mail.selectedMailbox = (m.Mail.selectedMailbox + 1) % len(m.Mail.mailboxes)
			m.Mail.selectedThread = 0
			return m, m.loadMailThreadsCmd(m.Mail.currentMailboxID(), m.Mail.folder)
		}
		return m, nil

	case "enter", "l":
		th := m.Mail.selectedThreadItem()
		if th != nil {
			return m, m.loadMailThreadReaderCmd(th.ID)
		}
		return m, nil

	case "c":
		m.Mail.composing = true
		m.Mail.composeMailboxID = m.Mail.currentMailboxID()
		m.Mail.composeTo = ""
		m.Mail.composeSubject = ""
		m.Mail.composeBody = ""
		m.Mail.composeField = 0
		m.Mail.composeReplyThreadID = ""
		m.showScreen(ScreenMailCompose)
		return m, nil

	case "s":
		th := m.Mail.selectedThreadItem()
		if th != nil && th.LatestMessage != nil {
			return m, m.starMailCmd(th.LatestMessage.ID, !th.LatestMessage.IsStarred)
		}
		return m, nil

	case "d", "x":
		th := m.Mail.selectedThreadItem()
		if th != nil && th.LatestMessage != nil {
			emailID := th.LatestMessage.ID
			m.ask("Move conversation to trash?", "This will move the selected thread to trash.", func() tea.Cmd {
				return m.moveMailCmd(emailID, "trash")
			})
		}
		return m, nil

	case "r", "R":
		return m, m.loadMailThreadsCmd(m.Mail.currentMailboxID(), m.Mail.folder)
	}

	return m, nil
}

func (m *Model) updateMailThreadKey(key tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	switch key.String() {
	case "esc", "backspace", "h":
		m.showScreen(ScreenMail)
		return m, nil

	case "j", "down":
		if len(m.Mail.threadEmails) > 0 && m.Mail.threadSelected < len(m.Mail.threadEmails)-1 {
			m.Mail.threadSelected++
		}
		return m, nil

	case "k", "up":
		if m.Mail.threadSelected > 0 {
			m.Mail.threadSelected--
		}
		return m, nil

	case "r":
		// Reply to thread
		if len(m.Mail.threadEmails) > 0 {
			last := m.Mail.threadEmails[len(m.Mail.threadEmails)-1]
			m.Mail.composing = true
			m.Mail.composeMailboxID = last.MailboxID
			m.Mail.composeTo = last.FromAddress
			subj := last.Subject
			if !strings.HasPrefix(strings.ToLower(subj), "re:") {
				subj = "Re: " + subj
			}
			m.Mail.composeSubject = subj
			m.Mail.composeBody = ""
			m.Mail.composeField = 2 // focus body
			m.Mail.composeReplyThreadID = m.Mail.activeThreadID
			m.showScreen(ScreenMailCompose)
			return m, nil
		}
		return m, nil

	case "d", "x":
		if len(m.Mail.threadEmails) > 0 {
			emailID := m.Mail.threadEmails[m.Mail.threadSelected].ID
			m.ask("Move message to trash?", "This will move the message to trash.", func() tea.Cmd {
				return m.moveMailCmd(emailID, "trash")
			})
		}
		return m, nil
	}
	return m, nil
}

func (m *Model) updateMailComposeKey(key tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	switch key.String() {
	case "esc":
		m.Mail.composing = false
		m.showScreen(ScreenMail)
		return m, nil

	case "tab":
		m.Mail.composeField = (m.Mail.composeField + 1) % 3
		return m, nil

	case "shift+tab":
		m.Mail.composeField = (m.Mail.composeField - 1 + 3) % 3
		return m, nil

	case "ctrl+s", "ctrl+enter":
		to := strings.TrimSpace(m.Mail.composeTo)
		subj := strings.TrimSpace(m.Mail.composeSubject)
		if to == "" {
			m.setBanner("recipient address (To) is required")
			return m, nil
		}
		if subj == "" {
			m.setBanner("subject is required")
			return m, nil
		}
		input := api.SendMailInput{
			MailboxID:   m.Mail.composeMailboxID,
			Subject:     subj,
			Body:        m.Mail.composeBody,
			ContentType: "text/plain",
			ThreadID:    m.Mail.composeReplyThreadID,
			To:          []api.MailRecipient{{Address: to, Kind: "to"}},
		}
		return m, m.sendMailCmd(input)

	case "backspace":
		switch m.Mail.composeField {
		case 0:
			if len(m.Mail.composeTo) > 0 {
				m.Mail.composeTo = m.Mail.composeTo[:len(m.Mail.composeTo)-1]
			}
		case 1:
			if len(m.Mail.composeSubject) > 0 {
				m.Mail.composeSubject = m.Mail.composeSubject[:len(m.Mail.composeSubject)-1]
			}
		case 2:
			if len(m.Mail.composeBody) > 0 {
				m.Mail.composeBody = m.Mail.composeBody[:len(m.Mail.composeBody)-1]
			}
		}
		return m, nil

	case "enter":
		if m.Mail.composeField == 2 {
			m.Mail.composeBody += "\n"
			return m, nil
		}
		m.Mail.composeField = (m.Mail.composeField + 1) % 3
		return m, nil

	default:
		if len(key.Text) > 0 {
			switch m.Mail.composeField {
			case 0:
				m.Mail.composeTo += key.Text
			case 1:
				m.Mail.composeSubject += key.Text
			case 2:
				m.Mail.composeBody += key.Text
			}
		}
		return m, nil
	}
}

func (m *Model) mailView() string {
	innerWidth := m.Width - 4
	if innerWidth < 1 {
		innerWidth = 1
	}

	// 1. Folders pill bar
	folders := []string{"inbox", "sent", "starred", "trash", "spam"}
	var folderPills []string
	for _, f := range folders {
		label := strings.Title(f)
		if f == m.Mail.folder {
			folderPills = append(folderPills, m.Styles.Selected.Render(" "+label+" "))
		} else {
			folderPills = append(folderPills, m.Styles.Muted.Render(" "+label+" "))
		}
	}
	mailboxAddr := m.Mail.currentMailboxAddress()
	if mailboxAddr == "" {
		mailboxAddr = "No mailbox"
	}
	folderBar := strings.Join(folderPills, " ") + "   " + m.Styles.Title.Render(mailboxAddr)

	// 2. Thread list
	var threadRows []string
	if m.Mail.loading && !m.Mail.loaded {
		threadRows = append(threadRows, m.Styles.Muted.Render("  "+m.Spinner.View()+" Loading messages..."))
	} else if len(m.Mail.threads) == 0 {
		threadRows = append(threadRows, m.Styles.Muted.Render("  (no messages in "+m.Mail.folder+")"))
	} else {
		for i, th := range m.Mail.threads {
			selected := i == m.Mail.selectedThread

			unreadDot := " "
			if th.UnreadCount > 0 {
				unreadDot = "●"
			}

			star := "☆"
			if th.LatestMessage != nil && th.LatestMessage.IsStarred {
				star = "★"
			}

			sender := strings.Join(th.Participants, ", ")
			if sender == "" && th.LatestMessage != nil {
				sender = th.LatestMessage.Sender()
			}
			if len(sender) > 22 {
				sender = sender[:19] + "..."
			}

			subj := th.DisplaySubject()
			if th.MessageCount > 1 {
				subj = fmt.Sprintf("%s (%d)", subj, th.MessageCount)
			}
			maxSubjLen := innerWidth - 48
			if maxSubjLen > 10 && len(subj) > maxSubjLen {
				subj = subj[:maxSubjLen-3] + "..."
			}

			dateStr := ""
			if th.LatestAt != nil {
				dateStr = *th.LatestAt
				if tIdx := strings.Index(dateStr, "T"); tIdx != -1 {
					dateStr = dateStr[:tIdx]
				}
			}

			rowText := fmt.Sprintf(" %s %s  %-22s  %-*s  %s", unreadDot, star, sender, maxSubjLen, subj, dateStr)

			if selected {
				threadRows = append(threadRows, m.Styles.Selected.Render(padRight(rowText, innerWidth)))
			} else {
				threadRows = append(threadRows, m.Styles.Foreground.Render(padRight(rowText, innerWidth)))
			}
		}
	}

	// 3. Hints
	hints := m.Styles.Muted.Render("[Enter] Read  [c] Compose  [s] Star  [x] Trash  [[] / []] Folder  [m] Mailbox")

	lines := []string{folderBar, ""}
	lines = append(lines, threadRows...)
	lines = append(lines, "", hints)

	return strings.Join(lines, "\n")
}

func (m *Model) mailThreadView() string {
	innerWidth := m.Width - 4
	if innerWidth < 1 {
		innerWidth = 1
	}

	if m.Mail.threadLoading {
		return m.Styles.Muted.Render("  " + m.Spinner.View() + " Loading conversation...")
	}

	if len(m.Mail.threadEmails) == 0 {
		return m.Styles.Muted.Render("  (no messages found)")
	}

	var lines []string
	subj := m.Mail.threadEmails[0].Subject
	lines = append(lines, m.Styles.Title.Render("Conversation: "+subj), "")

	for i, em := range m.Mail.threadEmails {
		selected := i == m.Mail.threadSelected
		border := "───"
		if selected {
			border = "▶──"
		}
		lines = append(lines, m.Styles.Muted.Render(border+" Message "+fmt.Sprintf("%d/%d", i+1, len(m.Mail.threadEmails))+" ─────────────────────────────"))
		lines = append(lines, m.Styles.Foreground.Render("From: "+em.Sender()))
		lines = append(lines, m.Styles.Muted.Render("Date: "+em.CreatedAt))
		lines = append(lines, "")
		for _, bLine := range strings.Split(em.Body, "\n") {
			lines = append(lines, "  "+bLine)
		}
		lines = append(lines, "")
	}

	hints := m.Styles.Muted.Render("[r] Reply  [s] Star  [x] Trash  [Esc] Back to inbox")
	lines = append(lines, hints)

	return strings.Join(lines, "\n")
}

func (m *Model) mailComposeView() string {
	innerWidth := m.Width - 4
	if innerWidth < 1 {
		innerWidth = 1
	}

	toPrompt := "To:      "
	subjPrompt := "Subject: "
	bodyPrompt := "Body:    \n"

	cursor := func(field int) string {
		if m.Mail.composeField == field {
			return "█"
		}
		return ""
	}

	toLine := m.Styles.Accent.Render(toPrompt) + m.Mail.composeTo + cursor(0)
	subjLine := m.Styles.Accent.Render(subjPrompt) + m.Mail.composeSubject + cursor(1)

	lines := []string{
		m.Styles.Title.Render("Compose Email"),
		"",
		toLine,
		subjLine,
		m.Styles.Accent.Render(bodyPrompt),
	}

	bodyLines := strings.Split(m.Mail.composeBody, "\n")
	for i, l := range bodyLines {
		if i == len(bodyLines)-1 && m.Mail.composeField == 2 {
			lines = append(lines, "  "+l+cursor(2))
		} else {
			lines = append(lines, "  "+l)
		}
	}

	lines = append(lines, "", m.Styles.Muted.Render("[Tab] Switch Field  [Ctrl+S / Ctrl+Enter] Send  [Esc] Cancel"))

	return strings.Join(lines, "\n")
}

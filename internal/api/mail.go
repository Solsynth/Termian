package api

import (
	"context"
	"net/http"
	"net/url"
	"strconv"
	"strings"
)

// Mailbox is an email address configuration associated with an account.
type Mailbox struct {
	ID          string `json:"id"`
	AccountID   string `json:"account_id"`
	WorkspaceID string `json:"workspace_id"`
	Address     string `json:"address"`
	Name        string `json:"name"`
	IsDefault   bool   `json:"is_default"`
	IsVerified  bool   `json:"is_verified"`
	CreatedAt   string `json:"created_at"`
}

// DisplayName returns a human-friendly name for the mailbox.
func (m Mailbox) DisplayName() string {
	if strings.TrimSpace(m.Name) != "" {
		return m.Name
	}
	return m.Address
}

// MailRecipient is one recipient of an email message.
type MailRecipient struct {
	Address string `json:"address"`
	Name    string `json:"name,omitempty"`
	Kind    string `json:"kind,omitempty"` // to, cc, bcc
}

// MailAttachment is metadata for a file attached to an email.
type MailAttachment struct {
	ID       string `json:"id"`
	Filename string `json:"filename"`
	MimeType string `json:"mime_type"`
	Size     int64  `json:"size"`
}

// MailEmail represents a single email message.
type MailEmail struct {
	ID          string           `json:"id"`
	MailboxID   string           `json:"mailbox_id"`
	ThreadID    *string          `json:"thread_id,omitempty"`
	Subject     string           `json:"subject"`
	Body        string           `json:"body"`
	FromAddress string           `json:"from_address"`
	FromName    string           `json:"from_name"`
	IsRead      bool             `json:"is_read"`
	IsStarred   bool             `json:"is_starred"`
	IsDraft     bool             `json:"is_draft"`
	Folder      string           `json:"folder"`
	ContentType string           `json:"content_type"`
	CreatedAt   string           `json:"created_at"`
	Recipients  []MailRecipient  `json:"recipients,omitempty"`
	Attachments []MailAttachment `json:"attachments,omitempty"`
}

// Sender returns the formatted sender string.
func (e MailEmail) Sender() string {
	if strings.TrimSpace(e.FromName) != "" {
		return e.FromName + " <" + e.FromAddress + ">"
	}
	if e.FromAddress != "" {
		return e.FromAddress
	}
	return "unknown"
}

// MailThread represents an email conversation summary.
type MailThread struct {
	ID            string     `json:"id"`
	MailboxID     string     `json:"mailbox_id"`
	Subject       string     `json:"subject"`
	MessageCount  int        `json:"message_count"`
	UnreadCount   int        `json:"unread_count"`
	Participants  []string   `json:"participants"`
	LatestMessage *MailEmail `json:"latest_message,omitempty"`
	LatestAt      *string    `json:"latest_at,omitempty"`
}

// DisplaySubject returns the thread subject or a fallback.
func (t MailThread) DisplaySubject() string {
	if strings.TrimSpace(t.Subject) != "" {
		return t.Subject
	}
	if t.LatestMessage != nil && strings.TrimSpace(t.LatestMessage.Subject) != "" {
		return t.LatestMessage.Subject
	}
	return "(no subject)"
}

// SendMailInput describes a new outgoing email or draft.
type SendMailInput struct {
	MailboxID   string          `json:"mailbox_id"`
	To          []MailRecipient `json:"to"`
	Cc          []MailRecipient `json:"cc,omitempty"`
	Bcc         []MailRecipient `json:"bcc,omitempty"`
	Subject     string          `json:"subject"`
	Body        string          `json:"body"`
	ContentType string          `json:"content_type,omitempty"`
	ThreadID    string          `json:"thread_id,omitempty"`
	ReplyToID   string          `json:"reply_to_id,omitempty"`
	IsDraft     bool            `json:"is_draft,omitempty"`
}

// Mailboxes lists the mailboxes owned by the signed-in account.
func (c *Client) Mailboxes(ctx context.Context) ([]Mailbox, error) {
	var mailboxes []Mailbox
	err := c.doRoot(ctx, http.MethodGet, "/postal/mailboxes", nil, &mailboxes)
	return mailboxes, err
}

// MailThreads lists conversation threads, scoped by optional mailbox and folder.
func (c *Client) MailThreads(ctx context.Context, mailboxID, folder string, offset, take int) (Page[MailThread], error) {
	if take <= 0 {
		take = 20
	}
	values := url.Values{}
	values.Set("offset", strconv.Itoa(offset))
	values.Set("take", strconv.Itoa(take))
	if folder != "" {
		values.Set("folder", folder)
	}

	var path string
	if mailboxID != "" {
		path = "/postal/mailboxes/" + url.PathEscape(mailboxID) + "/threads?" + values.Encode()
	} else {
		path = "/postal/threads?" + values.Encode()
	}

	var items []MailThread
	meta, err := c.doRootPage(ctx, http.MethodGet, path, nil, &items)
	return Page[MailThread]{Items: items, Total: totalFrom(meta.Header)}, err
}

// MailThread loads all messages belonging to a conversation, oldest first.
func (c *Client) MailThread(ctx context.Context, threadID string) ([]MailEmail, error) {
	var emails []MailEmail
	err := c.doRoot(ctx, http.MethodGet, "/postal/threads/"+url.PathEscape(threadID), nil, &emails)
	return emails, err
}

// MailEmail fetches a single email message by ID.
func (c *Client) MailEmail(ctx context.Context, emailID string) (MailEmail, error) {
	var email MailEmail
	err := c.doRoot(ctx, http.MethodGet, "/postal/emails/"+url.PathEscape(emailID), nil, &email)
	return email, err
}

// SendMail sends an email or saves a draft.
func (c *Client) SendMail(ctx context.Context, input SendMailInput) (MailEmail, error) {
	if input.ContentType == "" {
		input.ContentType = "text/plain"
	}
	var created MailEmail
	err := c.doRoot(ctx, http.MethodPost, "/postal/emails", input, &created)
	return created, err
}

// MarkMailRead marks an email as read or unread.
func (c *Client) MarkMailRead(ctx context.Context, emailID string, read bool) error {
	action := "read"
	if !read {
		action = "unread"
	}
	return c.doRoot(ctx, http.MethodPost, "/postal/emails/"+url.PathEscape(emailID)+"/"+action, nil, nil)
}

// StarMail stars or unstars an email.
func (c *Client) StarMail(ctx context.Context, emailID string, star bool) error {
	action := "star"
	if !star {
		action = "unstar"
	}
	return c.doRoot(ctx, http.MethodPost, "/postal/emails/"+url.PathEscape(emailID)+"/"+action, nil, nil)
}

// MoveMail moves an email to a folder (e.g. "trash", "inbox", "spam", "archive").
func (c *Client) MoveMail(ctx context.Context, emailID, folder string) error {
	return c.doRoot(ctx, http.MethodPost, "/postal/emails/"+url.PathEscape(emailID)+"/move", map[string]string{"folder": folder}, nil)
}

// DeleteMail deletes an email (permanent or moved to trash).
func (c *Client) DeleteMail(ctx context.Context, emailID string, permanent bool) error {
	path := "/postal/emails/" + url.PathEscape(emailID)
	if permanent {
		path += "/permanent"
	}
	return c.doRoot(ctx, http.MethodDelete, path, nil, nil)
}

// EmptyMailTrash permanently empties the trash folder for a mailbox.
func (c *Client) EmptyMailTrash(ctx context.Context, mailboxID string) error {
	return c.doRoot(ctx, http.MethodDelete, "/postal/mailboxes/"+url.PathEscape(mailboxID)+"/trash", nil, nil)
}

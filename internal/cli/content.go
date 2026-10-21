package cli

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

	"src.solsynth.dev/sosys/termian/internal/api"
)

// postPayload is the post shape emitted by the content commands.
type postPayload struct {
	ID           string            `json:"id"`
	Author       string            `json:"author"`
	AuthorHandle string            `json:"author_handle"`
	Title        string            `json:"title,omitempty"`
	Content      string            `json:"content"`
	URL          string            `json:"url,omitempty"`
	PublishedAt  *time.Time        `json:"published_at,omitempty"`
	Replies      int               `json:"replies"`
	Reactions    map[string]int    `json:"reactions,omitempty"`
	ReactionsBy  map[string]bool   `json:"reactions_made,omitempty"`
	Bookmarked   bool              `json:"is_bookmarked"`
	ReplyTo      string            `json:"replied_post_id,omitempty"`
	Attachments  []attachmentShape `json:"attachments,omitempty"`
}

// attachmentShape is the metadata of one attachment.
type attachmentShape struct {
	Name     string `json:"name"`
	MimeType string `json:"mime_type"`
	Size     int64  `json:"size,omitempty"`
}

// postFrom converts a wire post into its record shape.
func (a *app) postFrom(post api.Post) postPayload {
	out := postPayload{
		ID:           post.ID,
		Author:       post.AuthorName(),
		AuthorHandle: post.AuthorHandle(),
		Title:        strings.TrimSpace(deref(post.Title)),
		Content:      post.Body(),
		Replies:      post.RepliesCount,
		Reactions:    post.Reactions,
		ReactionsBy:  post.ReactionsMade,
		Bookmarked:   post.IsBookmarked,
	}
	if post.PublishedAt != nil {
		at := *post.PublishedAt
		out.PublishedAt = &at
	}
	if post.RepliedPostID != nil {
		out.ReplyTo = *post.RepliedPostID
	}
	if base := a.webBase(); base != "" {
		out.URL = base + "/posts/" + post.ID
	}
	for _, file := range post.Attachments {
		out.Attachments = append(out.Attachments, attachmentShape{
			Name:     file.Name,
			MimeType: file.MimeType,
			Size:     file.Size,
		})
	}
	return out
}

// webBase is the web client that renders posts. Posts live at
// https://solian.app/posts/<id> on every deployment.
func (a *app) webBase() string { return "https://solian.app" }

// deref reads an optional string.
func deref(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}

// withSession obtains an access token, reporting the documented
// not-signed-in error when no session exists.
func (a *app) withSession(ctx context.Context) error {
	if a.creds == nil || a.tokens == nil {
		return api.ErrNoCredentials
	}
	_, err := a.tokens.Token(ctx)
	return err
}

// postRows renders posts as table rows.
func postRows(posts []postPayload) [][]string {
	rows := make([][]string, 0, len(posts))
	for _, post := range posts {
		content := strings.ReplaceAll(post.Content, "\n", " ")
		rows = append(rows, []string{
			post.ID,
			post.AuthorHandle,
			oneLine(content, 60),
			shortWhen(post.PublishedAt),
			fmt.Sprintf("%d", post.Replies),
			reactionText(post.Reactions),
		})
	}
	return rows
}

// postHeaders is the table header of a post listing.
var postHeaders = []string{"id", "author", "content", "published", "replies", "reactions"}

// oneLine shortens text to at most n runes, replacing newlines.
func oneLine(text string, n int) string {
	text = strings.Join(strings.Fields(text), " ")
	runes := []rune(text)
	if len(runes) <= n {
		return text
	}
	return string(runes[:n-1]) + "…"
}

// shortWhen renders a timestamp as a compact date.
func shortWhen(t *time.Time) string {
	if t == nil || t.IsZero() {
		return ""
	}
	return t.Local().Format("2006-01-02 15:04")
}

// reactionText renders reaction counts sorted by symbol.
func reactionText(counts map[string]int) string {
	if len(counts) == 0 {
		return ""
	}
	keys := make([]string, 0, len(counts))
	for symbol, count := range counts {
		if count > 0 {
			keys = append(keys, symbol)
		}
	}
	sort.Strings(keys)
	parts := make([]string, 0, len(keys))
	for _, symbol := range keys {
		parts = append(parts, fmt.Sprintf("%s×%d", symbol, counts[symbol]))
	}
	return strings.Join(parts, " ")
}

package tui

import (
	"context"
	"strings"

	tea "charm.land/bubbletea/v2"

	"src.solsynth.dev/sosys/termian/internal/api"
)

// postDeletedMsg reports a removed post.
type postDeletedMsg struct {
	postID string
	err    error
}

// deletePostCmd removes a post and its replies.
func (m *Model) deletePostCmd(id string) tea.Cmd {
	return m.withTimeout(func(ctx context.Context) tea.Msg {
		if err := m.requireSession(ctx); err != nil {
			return postDeletedMsg{postID: id, err: err}
		}
		return postDeletedMsg{postID: id, err: m.Deps.Content.DeletePost(ctx, id)}
	})
}

// confirmDeletePost asks before removing a post.
func (m *Model) confirmDeletePost(post api.Post) tea.Cmd {
	detail := truncate(oneLineText(post.Body()), m.contentWidth()-6)
	if detail == "" {
		detail = post.ID
	}
	return m.ask("Delete this post?", detail+"\n\nThis cannot be undone.", func() tea.Cmd {
		return m.deletePostCmd(post.ID)
	})
}

// applyPostDeleted drops a removed post from every pane.
func (m *Model) applyPostDeleted(msg postDeletedMsg) {
	if msg.err != nil {
		m.reportContentError("delete", msg.err)
		return
	}
	kept := m.Feed.items[:0]
	for _, post := range m.Feed.items {
		if post.ID != msg.postID {
			kept = append(kept, post)
		}
	}
	if m.Feed.mode == feedBookmarks && len(kept) != len(m.Feed.items) {
		m.Feed.total = max(m.Feed.total-1, len(kept))
	}
	m.Feed.items = kept
	m.Feed.selected = min(m.Feed.selected, max(len(kept)-1, 0))

	replies := m.Post.replies[:0]
	for _, reply := range m.Post.replies {
		if reply.ID != msg.postID {
			replies = append(replies, reply)
		}
	}
	m.Post.replies = replies
	m.Post.selected = min(m.Post.selected, len(replies)-1)

	if m.Post.post.ID == msg.postID {
		// The pane's own post is gone: fall back to the list.
		m.Post = newPostModel()
		m.postStack = nil
		m.showScreen(ScreenFeed)
	}
	m.setNotice("post deleted")
}

// oneLineText collapses whitespace so a post body fits a status line.
func oneLineText(s string) string {
	return strings.Join(strings.Fields(s), " ")
}

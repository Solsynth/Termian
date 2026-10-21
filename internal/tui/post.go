package tui

import (
	"context"
	"fmt"

	tea "charm.land/bubbletea/v2"

	"src.solsynth.dev/sosys/termian/internal/api"
)

// postModel is the post detail pane: one post and its replies.
type postModel struct {
	post    api.Post
	replies []api.Post
	loaded  bool
	// selected indexes the reply list; -1 means the post itself is focused.
	selected int
}

// newPostModel builds an empty detail pane.
func newPostModel() postModel { return postModel{selected: -1} }

// selectedReply returns the highlighted reply, if any.
func (p *postModel) selectedReply() *api.Post {
	if p.selected < 0 || p.selected >= len(p.replies) {
		return nil
	}
	return &p.replies[p.selected]
}

// focused returns the post the keys act on: the selected reply, else the post.
func (p *postModel) focused() *api.Post {
	if reply := p.selectedReply(); reply != nil {
		return reply
	}
	return &p.post
}

// postLoadedMsg carries a post refresh and its first page of replies.
type postLoadedMsg struct {
	post    api.Post
	page    api.Page[api.Post]
	replies bool
	err     error
}

// loadPostCmd fetches the post, and its replies when withReplies is set.
func (m *Model) loadPostCmd(id string, withReplies bool) tea.Cmd {
	return m.withTimeout(func(ctx context.Context) tea.Msg {
		if m.Deps.Content == nil {
			return postLoadedMsg{err: fmt.Errorf("no content client configured")}
		}
		post, err := m.Deps.Content.Post(ctx, id)
		if err != nil {
			return postLoadedMsg{err: err}
		}
		msg := postLoadedMsg{post: post}
		if withReplies {
			page, err := m.Deps.Content.Replies(ctx, id, 0, feedPageSize)
			if err != nil {
				return postLoadedMsg{post: post, err: err}
			}
			msg.page, msg.replies = page, true
		}
		return msg
	})
}

// loadRepliesCmd fetches the next page of replies.
func (m *Model) loadRepliesCmd(id string, first int) tea.Cmd {
	return m.withTimeout(func(ctx context.Context) tea.Msg {
		page, err := m.Deps.Content.Replies(ctx, id, first, feedPageSize)
		return postLoadedMsg{page: page, replies: true, err: err}
	})
}

// postView renders the post and its replies.
func (m *Model) postView() string {
	p := &m.Post
	width := m.contentWidth()

	title := p.post.AuthorName()
	right := "post"
	if p.post.RepliesCount > 0 {
		right = plural(p.post.RepliesCount, "reply", "replies")
	}

	if !p.loaded {
		return m.pane(title, right,
			[]string{m.Spinner.View() + " " + m.Styles.Muted.Render("loading post…")}, m.postHint())
	}

	// The post card and every reply card are windowed together so the focused
	// card stays on screen.
	cards := [][]string{m.renderPostCard(p.post, width, 0, p.selected < 0)}
	if len(p.replies) > 0 {
		cards = append(cards, []string{"", m.Styles.Label.Render("replies"), ""})
		for i, reply := range p.replies {
			card := m.renderPostCard(reply, width, 4, i == p.selected)
			// Indent replies so they read as a thread.
			for j := range card {
				card[j] = "  " + card[j]
			}
			cards = append(cards, card)
		}
	}

	var body []string
	if len(p.replies) == 0 && p.loaded {
		body = append(body, cards[0]...)
		body = append(body, m.Styles.Muted.Render("no replies yet — press c to write one"))
		return m.pane(title, right, body, m.postHint())
	}
	// The selection indexes replies; card 0 is the post, card 1 the divider.
	sel := 0
	if p.selected >= 0 {
		sel = p.selected + 2
	}
	for _, i := range visibleCards(cards, sel, m.bodyHeight()) {
		body = append(body, cards[i]...)
	}
	return m.pane(title, right, body, m.postHint())
}

// postHint is the key legend of the detail pane.
func (m *Model) postHint() string {
	if m.guest {
		return "j/k move · r react · esc back · g sign in"
	}
	hint := "j/k move · r react · b bookmark · c reply · e edit · d delete · o open file · R reload · esc back"
	if m.Deps.Attachments == "preview" {
		hint = "j/k move · r react · b bookmark · c reply · e edit · d delete · o open · p preview · R reload"
	}
	return hint
}

// updatePostKey handles keys on the detail pane.
func (m *Model) updatePostKey(key tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	p := &m.Post
	switch key.String() {
	case "esc":
		// Walk back through the drill-down stack, then to the list.
		if len(m.postStack) > 1 {
			m.postStack = m.postStack[:len(m.postStack)-1]
			id := m.postStack[len(m.postStack)-1]
			m.Post = newPostModel()
			m.Post.post.ID = id
			m.Post.loaded = true
			return m, m.loadPostCmd(id, true)
		}
		m.postStack = nil
		m.showScreen(ScreenFeed)
		return m, nil
	case "j", "down":
		if p.selected < len(p.replies)-1 {
			p.selected++
		}
		return m, nil
	case "k", "up":
		if p.selected >= 0 {
			p.selected--
		}
		return m, nil
	case "r":
		target := p.focused()
		if target == nil || target.ID == "" {
			return m, nil
		}
		if err := m.sessionGuard("sign in to react"); err != nil {
			return m, nil
		}
		return m, m.toggleReactionCmd(target.ID, reactionSymbol)
	case "b":
		target := p.focused()
		if target == nil || target.ID == "" {
			return m, nil
		}
		if err := m.sessionGuard("sign in to bookmark"); err != nil {
			return m, nil
		}
		return m, m.toggleBookmarkCmd(*target)
	case "c":
		if err := m.sessionGuard("sign in to reply"); err != nil {
			return m, nil
		}
		return m, m.openCompose(p.focused())
	case "o":
		target := p.focused()
		if target == nil || len(target.Attachments) == 0 {
			return m, nil
		}
		return m, m.openAttachment(target.Attachments[0])
	case "p":
		target := p.focused()
		if target == nil {
			return m, nil
		}
		files := previewableAttachments(target.Attachments, 1)
		if len(files) == 0 {
			m.setBanner("this post has no image to preview")
			return m, nil
		}
		delete(m.previews, files[0].ID)
		return m, m.previewCmd(files[0])
	case "e":
		target := p.focused()
		if target == nil || target.ID == "" {
			return m, nil
		}
		if err := m.sessionGuard("sign in to edit"); err != nil {
			return m, nil
		}
		return m, m.openEdit(*target)
	case "d":
		target := p.focused()
		if target == nil || target.ID == "" {
			return m, nil
		}
		if err := m.sessionGuard("sign in to delete"); err != nil {
			return m, nil
		}
		return m, m.confirmDeletePost(*target)
	case "R":
		if p.post.ID == "" {
			return m, nil
		}
		return m, m.loadPostCmd(p.post.ID, true)
	case "enter":
		reply := p.selectedReply()
		if reply == nil {
			return m, nil
		}
		m.postStack = append(m.postStack, reply.ID)
		m.Post = newPostModel()
		m.Post.post = *reply
		m.Post.loaded = true
		m.showScreen(ScreenPost)
		return m, m.loadPostCmd(reply.ID, true)
	}
	return m, nil
}

// updatePostMsg applies asynchronous detail results.
func (m *Model) updatePostMsg(msg tea.Msg) (tea.Model, tea.Cmd) {
	p := &m.Post
	switch msg := msg.(type) {
	case postLoadedMsg:
		if msg.err != nil {
			m.reportContentError("post", msg.err)
			return m, nil
		}
		var preview tea.Cmd
		if msg.post.ID != "" {
			p.post = msg.post
		}
		p.loaded = true
		if msg.replies {
			if msg.page.Items != nil {
				p.replies = msg.page.Items
			}
			p.selected = min(p.selected, len(p.replies)-1)
		}
		m.clearBanner()
		if m.Deps.Attachments == "preview" {
			if files := previewableAttachments(p.focused().Attachments, 1); len(files) > 0 {
				preview = m.previewCmd(files[0])
			}
		}
		return m, preview
	}
	return m, nil
}

// replyCreatedMsg reports a published reply.
type replyCreatedMsg struct {
	post api.Post
	err  error
}

// createReplyCmd publishes a reply to the focused post.
func (m *Model) createReplyCmd(parent api.Post, content string) tea.Cmd {
	return m.withTimeout(func(ctx context.Context) tea.Msg {
		if err := m.requireSession(ctx); err != nil {
			return replyCreatedMsg{err: err}
		}
		post, err := m.Deps.Content.CreatePost(ctx, api.NewPost{Content: content, ReplyTo: parent.ID})
		return replyCreatedMsg{post: post, err: err}
	})
}

// applyReplyCreated prepends a freshly published reply.
func (m *Model) applyReplyCreated(msg replyCreatedMsg) {
	if msg.err != nil {
		m.reportContentError("reply", msg.err)
		return
	}
	m.Post.replies = append([]api.Post{msg.post}, m.Post.replies...)
	m.Post.post.RepliesCount++
	m.Post.selected = 0
	m.setNotice("reply posted")
}

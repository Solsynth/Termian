package tui

import (
	"context"
	"strings"

	"charm.land/bubbles/v2/textarea"
	tea "charm.land/bubbletea/v2"

	"src.solsynth.dev/sosys/termian/internal/api"
)

// composeModel is the post composer overlay.
type composeModel struct {
	active  bool
	replyTo *api.Post
	// editing is the post being rewritten, nil for a new post.
	editing *api.Post
	input   textarea.Model
	posting bool
	// origin is the screen the composer was opened from.
	origin Screen
}

// newComposeModel builds an idle composer.
func newComposeModel() composeModel {
	ta := textarea.New()
	ta.Placeholder = "what is on your mind?"
	ta.ShowLineNumbers = false
	ta.Prompt = ""
	ta.CharLimit = 5000
	ta.SetHeight(6)
	ta.SetWidth(HomeCardWidth)
	ta.KeyMap.InsertNewline.SetKeys("alt+enter", "ctrl+j")
	return composeModel{input: ta}
}

// setWidth resizes the editor.
func (c *composeModel) setWidth(w int) { c.input.SetWidth(max(w-2, 10)) }

// openCompose opens the composer, optionally as a reply to parent.
func (m *Model) openCompose(parent *api.Post) tea.Cmd {
	m.Compose.active = true
	m.Compose.origin = m.Screen
	m.Compose.replyTo = parent
	m.Compose.editing = nil
	m.Compose.posting = false
	m.Compose.input.Reset()
	m.Compose.input.Focus()
	m.clearBanner()
	m.showScreen(ScreenCompose)
	return textarea.Blink
}

// openEdit opens the composer on an existing post.
func (m *Model) openEdit(post api.Post) tea.Cmd {
	m.Compose.active = true
	m.Compose.origin = m.Screen
	m.Compose.replyTo = nil
	m.Compose.editing = &post
	m.Compose.posting = false
	m.Compose.input.SetValue(post.Body())
	m.Compose.input.Focus()
	m.clearBanner()
	m.showScreen(ScreenCompose)
	return textarea.Blink
}

// closeCompose dismisses the composer and returns to the previous pane.
func (m *Model) closeCompose() {
	m.Compose.active = false
	m.Compose.editing = nil
	m.Compose.replyTo = nil
	m.Compose.input.Blur()
	m.Compose.input.Reset()
	switch {
	case m.Compose.origin == ScreenPost && m.Post.post.ID != "":
		m.showScreen(ScreenPost)
	case m.Compose.origin == ScreenHome:
		m.showScreen(ScreenHome)
	case m.Compose.origin == ScreenChat || m.Compose.origin == ScreenChats:
		m.showScreen(m.Compose.origin)
	default:
		m.showScreen(ScreenFeed)
	}
}

// composeView renders the composer.
func (m *Model) composeView() string {
	c := &m.Compose
	title := "New post"
	switch {
	case c.editing != nil:
		title = "Edit post"
	case c.replyTo != nil:
		title = "Reply to " + c.replyTo.AuthorName()
	}

	body := []string{}
	if c.editing != nil {
		body = append(body, m.Styles.Muted.Render("editing "+c.editing.ID), "")
	}
	if c.replyTo != nil {
		quote := truncate(strings.ReplaceAll(c.replyTo.Body(), "\n", " "), m.contentWidth()-4)
		body = append(body, m.Styles.Muted.Render("> "+quote), "")
	}
	body = append(body, c.input.View())
	if c.posting {
		body = append(body, "", m.Spinner.View()+" "+m.Styles.Muted.Render("posting…"))
	}
	verb := "post"
	if c.editing != nil {
		verb = "save"
	}
	return m.pane(title, "compose", body, "ctrl+d "+verb+" · esc cancel · alt+enter newline")
}

// updateComposeKey handles keys on the composer.
func (m *Model) updateComposeKey(key tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	c := &m.Compose
	switch key.String() {
	case "esc":
		m.closeCompose()
		return m, nil
	case "ctrl+d":
		content := strings.TrimSpace(c.input.Value())
		if content == "" || c.posting {
			return m, nil
		}
		c.posting = true
		switch {
		case c.editing != nil:
			return m, m.updatePostCmd(c.editing.ID, content)
		case c.replyTo != nil:
			return m, m.createReplyCmd(*c.replyTo, content)
		}
		return m, m.createPostCmd(content)
	}
	var cmd tea.Cmd
	c.input, cmd = c.input.Update(key)
	return m, cmd
}

// postCreatedMsg reports a published post.
type postCreatedMsg struct {
	post api.Post
	err  error
}

// createPostCmd publishes a top-level post as the account's default publisher.
func (m *Model) createPostCmd(content string) tea.Cmd {
	return m.withTimeout(func(ctx context.Context) tea.Msg {
		if err := m.requireSession(ctx); err != nil {
			return postCreatedMsg{err: err}
		}
		post, err := m.Deps.Content.CreatePost(ctx, api.NewPost{Content: content})
		return postCreatedMsg{post: post, err: err}
	})
}

// postUpdatedMsg reports an edited post.
type postUpdatedMsg struct {
	post api.Post
	err  error
}

// updatePostCmd rewrites the body of an existing post.
func (m *Model) updatePostCmd(id, content string) tea.Cmd {
	return m.withTimeout(func(ctx context.Context) tea.Msg {
		if err := m.requireSession(ctx); err != nil {
			return postUpdatedMsg{err: err}
		}
		post, err := m.Deps.Content.UpdatePost(ctx, id, api.PostPatch{Content: &content})
		return postUpdatedMsg{post: post, err: err}
	})
}

// updateComposeMsg applies asynchronous compose results.
func (m *Model) updateComposeMsg(msg tea.Msg) (tea.Model, tea.Cmd) {
	c := &m.Compose
	switch msg := msg.(type) {
	case postCreatedMsg:
		c.posting = false
		if msg.err != nil {
			m.reportContentError("post", msg.err)
			return m, nil
		}
		loaded := m.Feed.loaded
		if loaded {
			m.Feed.items = append([]api.Post{msg.post}, m.Feed.items...)
			m.Feed.total++
			m.Feed.selected = 0
		}
		m.closeCompose()
		m.showScreen(ScreenFeed)
		m.setNotice("posted")
		if !loaded {
			// The feed was never opened in this session, so load it instead
			// of showing an empty pane.
			m.Feed.reset(feedTimeline)
			return m, m.loadFeedCmd(feedTimeline, 0)
		}
		return m, nil

	case postUpdatedMsg:
		c.posting = false
		if msg.err != nil {
			m.reportContentError("edit", msg.err)
			return m, nil
		}
		m.updatePost(msg.post.ID, func(post *api.Post) { *post = msg.post })
		origin := c.origin
		m.closeCompose()
		m.showScreen(origin)
		m.setNotice("post updated")
		return m, nil

	case replyCreatedMsg:
		c.posting = false
		m.applyReplyCreated(msg)
		if msg.err == nil {
			m.closeCompose()
			m.showScreen(ScreenPost)
		}
		return m, nil
	}
	return m, nil
}

package tui

import (
	"context"
	"fmt"
	"mime"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"charm.land/bubbles/v2/textarea"
	"charm.land/bubbles/v2/textinput"
	tea "charm.land/bubbletea/v2"

	"src.solsynth.dev/sosys/termian/internal/api"
)

// composeField identifies the focused composer control.
type composeField int

const (
	fieldContent composeField = iota
	fieldTitle
	fieldDescription
	fieldTags
	composeFieldCount
)

// composePicker identifies the inline path prompt.
type composePicker int

const (
	pickerNone composePicker = iota
	pickerMarkdown
	pickerAttachment
)

// uploadTimeout bounds one Drive upload.
const uploadTimeout = 60 * time.Second

// composeModel is the post composer overlay.
type composeModel struct {
	active  bool
	replyTo *api.Post
	// editing is the post being rewritten, nil for a new post.
	editing *api.Post
	input   textarea.Model

	titleInput       textinput.Model
	descriptionInput textinput.Model
	tagsInput        textinput.Model
	focus            composeField

	publisherOptions []api.Publisher
	publisherName    string
	publishersLoaded bool

	postType   int
	visibility int

	attachments []api.CloudFile
	uploading   int

	posting bool
	picker  composePicker
	path    textinput.Model
	// origin is the screen the composer was opened from.
	origin Screen
}

// newComposeModel builds an idle composer.
func newComposeModel() composeModel {
	ta := textarea.New()
	ta.Placeholder = "what is on your mind?"
	ta.ShowLineNumbers = false
	ta.Prompt = ""
	ta.CharLimit = 100000
	ta.SetHeight(5)
	ta.SetWidth(DefaultInputWidth)
	ta.KeyMap.InsertNewline.SetKeys("alt+enter", "ctrl+j")

	title := textinput.New()
	title.Placeholder = "title (articles and blogs)"
	title.Prompt = ""
	title.CharLimit = 1024
	title.SetWidth(DefaultInputWidth)

	description := textinput.New()
	description.Placeholder = "short description"
	description.Prompt = ""
	description.CharLimit = 4096
	description.SetWidth(DefaultInputWidth)

	tags := textinput.New()
	tags.Placeholder = "tags, comma separated"
	tags.Prompt = ""
	tags.CharLimit = 512
	tags.SetWidth(DefaultInputWidth)

	path := textinput.New()
	path.Prompt = ""
	path.CharLimit = 4096
	path.SetWidth(DefaultInputWidth)

	return composeModel{
		input:            ta,
		titleInput:       title,
		descriptionInput: description,
		tagsInput:        tags,
		path:             path,
		focus:            fieldContent,
	}
}

// setWidth resizes every composer control.
func (c *composeModel) setWidth(w int) {
	inner := max(w-2, 10)
	c.input.SetWidth(inner)
	c.titleInput.SetWidth(inner)
	c.descriptionInput.SetWidth(inner)
	c.tagsInput.SetWidth(inner)
	c.path.SetWidth(inner)
}

// setStyles re-tints the composer controls.
func (c *composeModel) setStyles(styles Styles) {
	c.input.SetStyles(textareaStyles(styles))
	c.titleInput.SetStyles(inputStyles(styles))
	c.descriptionInput.SetStyles(inputStyles(styles))
	c.tagsInput.SetStyles(inputStyles(styles))
	c.path.SetStyles(inputStyles(styles))
}

// openCompose opens the composer, optionally as a reply to parent.
func (m *Model) openCompose(parent *api.Post) tea.Cmd {
	c := &m.Compose
	c.active = true
	c.origin = m.Screen
	c.replyTo = parent
	c.editing = nil
	c.posting = false
	c.picker = pickerNone
	c.postType = api.PostTypeStatus
	c.visibility = api.VisibilityPublic
	c.attachments = nil
	c.uploading = 0
	c.publisherName = m.defaultPublisher
	c.input.Reset()
	c.titleInput.SetValue("")
	c.descriptionInput.SetValue("")
	c.tagsInput.SetValue("")
	c.path.SetValue("")
	c.focusField(fieldContent)
	m.clearBanner()
	m.showScreen(ScreenCompose)
	cmds := []tea.Cmd{textarea.Blink}
	if !c.publishersLoaded && m.Deps.Content != nil {
		cmds = append(cmds, m.loadComposePublishersCmd())
	}
	return tea.Batch(cmds...)
}

// openEdit opens the composer on an existing post.
func (m *Model) openEdit(post api.Post) tea.Cmd {
	c := &m.Compose
	c.active = true
	c.origin = m.Screen
	c.replyTo = nil
	c.editing = &post
	c.posting = false
	c.picker = pickerNone
	c.postType = post.Type
	c.visibility = post.Visibility
	c.attachments = append([]api.CloudFile(nil), post.Attachments...)
	c.uploading = 0
	c.publisherName = ""
	if post.Publisher != nil {
		c.publisherName = post.Publisher.Name
	}
	c.input.SetValue(post.Body())
	c.titleInput.SetValue(derefString(post.Title))
	c.descriptionInput.SetValue(derefString(post.Description))
	c.tagsInput.SetValue(tagSlugs(post.Tags))
	c.path.SetValue("")
	c.focusField(fieldContent)
	m.clearBanner()
	m.showScreen(ScreenCompose)
	cmds := []tea.Cmd{textarea.Blink}
	if !c.publishersLoaded && m.Deps.Content != nil {
		cmds = append(cmds, m.loadComposePublishersCmd())
	}
	return tea.Batch(cmds...)
}

// derefString reads an optional string.
func derefString(value *string) string {
	if value == nil {
		return ""
	}
	return *value
}

// tagSlugs joins a post's tags back into the composer form.
func tagSlugs(tags []api.PostTag) string {
	out := make([]string, 0, len(tags))
	for _, tag := range tags {
		if tag.Slug != "" {
			out = append(out, tag.Slug)
		}
	}
	return strings.Join(out, ", ")
}

// closeCompose dismisses the composer and returns to the previous pane.
func (m *Model) closeCompose() {
	c := &m.Compose
	c.active = false
	c.editing = nil
	c.replyTo = nil
	c.picker = pickerNone
	c.input.Blur()
	c.titleInput.Blur()
	c.descriptionInput.Blur()
	c.tagsInput.Blur()
	c.path.Blur()
	c.input.Reset()
	switch {
	case c.origin == ScreenPost && m.Post.post.ID != "":
		m.showScreen(ScreenPost)
	case c.origin == ScreenHome:
		m.showScreen(ScreenHome)
	case c.origin == ScreenChat || c.origin == ScreenChats:
		m.showScreen(c.origin)
	default:
		m.showScreen(ScreenFeed)
	}
}

// focusField moves the cursor to a composer control.
func (c *composeModel) focusField(field composeField) {
	c.focus = field
	c.input.Blur()
	c.titleInput.Blur()
	c.descriptionInput.Blur()
	c.tagsInput.Blur()
	switch field {
	case fieldTitle:
		c.titleInput.Focus()
	case fieldDescription:
		c.descriptionInput.Focus()
	case fieldTags:
		c.tagsInput.Focus()
	default:
		c.focus = fieldContent
		c.input.Focus()
	}
}

// nextField advances through the form, wrapping around.
func (c *composeModel) nextField(delta int) {
	field := (int(c.focus) + delta + int(composeFieldCount)) % int(composeFieldCount)
	c.focusField(composeField(field))
}

// publisherLabel names the publisher the post will be made as.
func (c *composeModel) publisherLabel() string {
	if c.publisherName == "" {
		return "default publisher"
	}
	return "@" + c.publisherName
}

// cyclePublisher moves to the next publisher, wrapping through the server
// default.
func (c *composeModel) cyclePublisher() {
	if len(c.publisherOptions) == 0 {
		return
	}
	if c.publisherName == "" {
		c.publisherName = c.publisherOptions[0].Name
		return
	}
	for i, publisher := range c.publisherOptions {
		if publisher.Name != c.publisherName {
			continue
		}
		if i+1 >= len(c.publisherOptions) {
			c.publisherName = ""
			return
		}
		c.publisherName = c.publisherOptions[i+1].Name
		return
	}
	c.publisherName = c.publisherOptions[0].Name
}

// cyclePostType walks status -> article -> blog.
func (c *composeModel) cyclePostType() {
	c.postType = (c.postType + 1) % 3
}

// cycleVisibility walks the visibility levels the terminal exposes.
func (c *composeModel) cycleVisibility() {
	levels := []int{api.VisibilityPublic, api.VisibilityUnlisted, api.VisibilityPrivate, api.VisibilityFriends}
	for i, level := range levels {
		if level == c.visibility {
			c.visibility = levels[(i+1)%len(levels)]
			return
		}
	}
	c.visibility = api.VisibilityPublic
}

// fieldLine renders a labelled one-line input, marking the focused one.
func (m *Model) fieldLine(label string, input textinput.Model, focused bool) string {
	marker := "  "
	style := m.Styles.Label
	if focused {
		marker = m.Styles.Accent.Render("▸ ")
		style = m.Styles.Accent
	}
	return marker + style.Width(12).Render(label) + input.View()
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
	case c.postType == api.PostTypeArticle:
		title = "New article"
	case c.postType == api.PostTypeBlog:
		title = "New blog link"
	}

	body := []string{}
	if c.editing != nil {
		body = append(body, m.Styles.Muted.Render("editing "+c.editing.ID), "")
	}
	if c.replyTo != nil {
		quote := truncate(strings.ReplaceAll(c.replyTo.Body(), "\n", " "), m.contentWidth()-4)
		body = append(body, m.Styles.Muted.Render("> "+quote), "")
	}

	settings := fmt.Sprintf("as %s · %s · %s", c.publisherLabel(),
		api.PostTypeLabel(c.postType), api.VisibilityLabel(c.visibility))
	body = append(body, m.Styles.Muted.Render(settings), "")
	body = append(body, m.fieldLine("title", c.titleInput, c.focus == fieldTitle))
	body = append(body, m.fieldLine("description", c.descriptionInput, c.focus == fieldDescription))
	body = append(body, "")
	if c.focus == fieldContent {
		body = append(body, m.Styles.Accent.Render("▸ content"))
	} else {
		body = append(body, m.Styles.Label.Render("  content"))
	}
	body = append(body, c.input.View())
	body = append(body, m.fieldLine("tags", c.tagsInput, c.focus == fieldTags))
	if len(c.attachments) > 0 || c.uploading > 0 {
		body = append(body, "", m.Styles.Label.Render("attachments"))
		for i, file := range c.attachments {
			body = append(body, m.Styles.Foreground.Render(fmt.Sprintf("  %d. %s", i+1, attachmentLine(file, i+1))))
		}
		if c.uploading > 0 {
			body = append(body, "  "+m.Spinner.View()+" "+m.Styles.Muted.Render("uploading…"))
		}
	}
	if c.picker != pickerNone {
		label := "markdown file"
		if c.picker == pickerAttachment {
			label = "attachment file"
		}
		body = append(body, "", m.Styles.Accent.Render(label+": ")+c.path.View()+m.Styles.Muted.Render("  [enter] load  [esc] cancel"))
	}
	if c.posting {
		body = append(body, "", m.Spinner.View()+" "+m.Styles.Muted.Render("posting…"))
	}
	body = append(body, "", m.Styles.Hint.Render(
		"[tab] field · [ctrl+p] publisher · [ctrl+t] type · [ctrl+v] visibility · [ctrl+o] markdown · [ctrl+a] attach · [ctrl+d] publish · [esc] cancel"))
	return m.pane(title, "compose", body)
}

// updateComposeKey handles keys on the composer.
func (m *Model) updateComposeKey(key tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	c := &m.Compose
	if c.picker != pickerNone {
		switch key.String() {
		case "esc":
			c.picker = pickerNone
			c.path.Blur()
			c.path.SetValue("")
			return m, nil
		case "enter":
			path := strings.TrimSpace(c.path.Value())
			kind := c.picker
			c.picker = pickerNone
			c.path.Blur()
			c.path.SetValue("")
			if path == "" {
				return m, nil
			}
			if kind == pickerMarkdown {
				return m, m.loadMarkdownCmd(path)
			}
			return m, m.uploadAttachmentCmd(path)
		}
		var cmd tea.Cmd
		c.path, cmd = c.path.Update(key)
		return m, cmd
	}

	switch key.String() {
	case "esc":
		m.closeCompose()
		return m, nil
	case "tab":
		c.nextField(1)
		return m, nil
	case "shift+tab":
		c.nextField(-1)
		return m, nil
	case "ctrl+p":
		c.cyclePublisher()
		return m, nil
	case "ctrl+t":
		c.cyclePostType()
		return m, nil
	case "ctrl+v":
		c.cycleVisibility()
		return m, nil
	case "ctrl+o":
		c.openPicker(pickerMarkdown)
		return m, textinput.Blink
	case "ctrl+a":
		c.openPicker(pickerAttachment)
		return m, textinput.Blink
	case "ctrl+d":
		if c.posting {
			return m, nil
		}
		req, err := m.composeRequest()
		if err != nil {
			m.setBanner(err.Error())
			return m, nil
		}
		if c.uploading > 0 {
			m.setBanner("wait for the attachments to finish uploading")
			return m, nil
		}
		c.posting = true
		if c.editing != nil {
			return m, m.updatePostCmd(c.editing.ID, req)
		}
		if c.replyTo != nil {
			req.ReplyTo = c.replyTo.ID
		}
		return m, m.createPostCmd(req)
	}

	var cmd tea.Cmd
	switch c.focus {
	case fieldTitle:
		c.titleInput, cmd = c.titleInput.Update(key)
	case fieldDescription:
		c.descriptionInput, cmd = c.descriptionInput.Update(key)
	case fieldTags:
		c.tagsInput, cmd = c.tagsInput.Update(key)
	default:
		c.input, cmd = c.input.Update(key)
	}
	return m, cmd
}

// openPicker shows the inline path prompt.
func (c *composeModel) openPicker(kind composePicker) {
	c.picker = kind
	c.path.SetValue("")
	c.path.Focus()
}

// composeRequest validates the form and builds the create payload.
func (m *Model) composeRequest() (api.NewPost, error) {
	c := &m.Compose
	content := strings.TrimSpace(c.input.Value())
	title := strings.TrimSpace(c.titleInput.Value())
	description := strings.TrimSpace(c.descriptionInput.Value())
	if c.postType == api.PostTypeBlog {
		parsed, err := url.Parse(content)
		if err != nil || parsed.Host == "" || (parsed.Scheme != "http" && parsed.Scheme != "https") {
			return api.NewPost{}, fmt.Errorf("blog posts need a valid http(s) link")
		}
		if title == "" {
			title = parsed.Host
		}
	} else {
		if content == "" && len(c.attachments) == 0 {
			return api.NewPost{}, fmt.Errorf("write something before publishing")
		}
		if c.postType == api.PostTypeArticle && title == "" {
			return api.NewPost{}, fmt.Errorf("articles need a title (ctrl+o loads a markdown file and fills it)")
		}
	}
	attachments := make([]string, 0, len(c.attachments))
	for _, file := range c.attachments {
		if file.ID != "" {
			attachments = append(attachments, file.ID)
		}
	}
	return api.NewPost{
		Content:     content,
		Title:       title,
		Description: description,
		Type:        c.postType,
		Visibility:  c.visibility,
		Tags:        splitTags(c.tagsInput.Value()),
		Attachments: attachments,
		Publisher:   c.publisherName,
	}, nil
}

// splitTags normalizes the comma separated tag form.
func splitTags(raw string) []string {
	seen := map[string]bool{}
	var tags []string
	for _, field := range strings.FieldsFunc(raw, func(r rune) bool { return r == ',' || r == ' ' }) {
		tag := strings.TrimPrefix(strings.TrimSpace(field), "#")
		if tag == "" || seen[strings.ToLower(tag)] {
			continue
		}
		seen[strings.ToLower(tag)] = true
		tags = append(tags, tag)
	}
	return tags
}

// postCreatedMsg reports a published post.
type postCreatedMsg struct {
	post api.Post
	err  error
}

// createPostCmd publishes a post.
func (m *Model) createPostCmd(req api.NewPost) tea.Cmd {
	return m.withTimeout(func(ctx context.Context) tea.Msg {
		if err := m.requireSession(ctx); err != nil {
			return postCreatedMsg{err: err}
		}
		post, err := m.Deps.Content.CreatePost(ctx, req)
		return postCreatedMsg{post: post, err: err}
	})
}

// postUpdatedMsg reports an edited post.
type postUpdatedMsg struct {
	post api.Post
	err  error
}

// updatePostCmd rewrites an existing post with the full composer payload.
func (m *Model) updatePostCmd(id string, req api.NewPost) tea.Cmd {
	return m.withTimeout(func(ctx context.Context) tea.Msg {
		if err := m.requireSession(ctx); err != nil {
			return postUpdatedMsg{err: err}
		}
		title, description, content := req.Title, req.Description, req.Content
		tags, attachments := req.Tags, req.Attachments
		postType, visibility := req.Type, req.Visibility
		patch := api.PostPatch{
			Content:     &content,
			Title:       &title,
			Description: &description,
			Type:        &postType,
			Visibility:  &visibility,
			Tags:        &tags,
			Attachments: &attachments,
			Publisher:   req.Publisher,
		}
		post, err := m.Deps.Content.UpdatePost(ctx, id, patch)
		return postUpdatedMsg{post: post, err: err}
	})
}

// composePublishersMsg carries the publishers the account can post as.
type composePublishersMsg struct {
	publishers []api.Publisher
	err        error
}

// loadComposePublishersCmd loads the publisher picker options.
func (m *Model) loadComposePublishersCmd() tea.Cmd {
	return m.withTimeout(func(ctx context.Context) tea.Msg {
		if err := m.requireSession(ctx); err != nil {
			return composePublishersMsg{err: err}
		}
		publishers, err := m.Deps.Content.Publishers(ctx)
		return composePublishersMsg{publishers: publishers, err: err}
	})
}

// markdownLoadedMsg carries a local markdown file.
type markdownLoadedMsg struct {
	name        string
	body        string
	title       string
	description string
	err         error
}

// loadMarkdownCmd reads a local markdown file and derives article metadata.
func (m *Model) loadMarkdownCmd(path string) tea.Cmd {
	return m.withTimeout(func(context.Context) tea.Msg {
		data, err := os.ReadFile(path)
		if err != nil {
			return markdownLoadedMsg{name: path, err: err}
		}
		title, description := markdownMetadata(string(data))
		if title == "" {
			base := filepath.Base(path)
			title = strings.TrimSuffix(base, filepath.Ext(base))
		}
		return markdownLoadedMsg{
			name: path, body: string(data), title: title, description: description,
		}
	})
}

// markdownMetadata derives a title from the first heading and a description
// from the first prose paragraph.
func markdownMetadata(body string) (string, string) {
	var title, description string
	for _, raw := range strings.Split(body, "\n") {
		line := strings.TrimSpace(raw)
		if line == "" || strings.HasPrefix(line, "```") {
			continue
		}
		if title == "" && strings.HasPrefix(line, "#") {
			title = strings.TrimSpace(strings.TrimLeft(line, "#"))
			continue
		}
		if description == "" && !strings.HasPrefix(line, "#") && !strings.HasPrefix(line, ">") && !strings.HasPrefix(line, "!") {
			description = line
		}
		if title != "" && description != "" {
			break
		}
	}
	if len(description) > 240 {
		description = description[:239] + "…"
	}
	return title, description
}

// attachmentUploadedMsg reports one Drive upload.
type attachmentUploadedMsg struct {
	name string
	file api.CloudFile
	err  error
}

// uploadAttachmentCmd uploads a local file and attaches it to the draft.
func (m *Model) uploadAttachmentCmd(path string) tea.Cmd {
	c := &m.Compose
	c.uploading++
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(m.Deps.Ctx, uploadTimeout)
		defer cancel()
		if err := m.requireSession(ctx); err != nil {
			return attachmentUploadedMsg{name: path, err: err}
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return attachmentUploadedMsg{name: path, err: err}
		}
		contentType := mime.TypeByExtension(strings.ToLower(filepath.Ext(path)))
		file, err := m.Deps.Content.UploadFile(ctx, filepath.Base(path), data, contentType)
		return attachmentUploadedMsg{name: path, file: file, err: err}
	}
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
			if m.Feed.mode == feedBookmarks {
				m.Feed.total++
			}
			m.Feed.selected = 0
		}
		m.closeCompose()
		m.showScreen(ScreenFeed)
		m.setNotice("posted as " + api.PostTypeLabel(msg.post.Type))
		if !loaded {
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

	case composePublishersMsg:
		if msg.err != nil {
			m.reportContentError("publishers", msg.err)
			return m, nil
		}
		c.publisherOptions = msg.publishers
		c.publishersLoaded = true
		if c.publisherName == "" && len(msg.publishers) > 0 && c.editing == nil && c.replyTo == nil {
			// Keep the server default: the account's primary publisher is
			// implicit and does not need to be sent back.
			c.publisherName = m.defaultPublisher
		}
		return m, nil

	case markdownLoadedMsg:
		if msg.err != nil {
			m.setBanner("markdown: " + msg.err.Error())
			return m, nil
		}
		c.input.SetValue(msg.body)
		c.postType = api.PostTypeArticle
		if strings.TrimSpace(c.titleInput.Value()) == "" {
			c.titleInput.SetValue(msg.title)
		}
		if strings.TrimSpace(c.descriptionInput.Value()) == "" {
			c.descriptionInput.SetValue(msg.description)
		}
		c.focusField(fieldContent)
		m.setNotice("loaded " + filepath.Base(msg.name))
		return m, nil

	case attachmentUploadedMsg:
		c.uploading = max(c.uploading-1, 0)
		if msg.err != nil {
			m.setBanner("attachment " + filepath.Base(msg.name) + ": " + msg.err.Error())
			return m, nil
		}
		c.attachments = append(c.attachments, msg.file)
		m.setNotice("attached " + msg.file.Name)
		return m, nil
	}
	return m, nil
}

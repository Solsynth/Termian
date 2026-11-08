package tui

import (
	"context"
	"fmt"
	"sort"
	"strings"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"src.solsynth.dev/sosys/termian/internal/api"
)

// feedPageSize is how many posts one page holds.
const feedPageSize = 20

// feedMode selects which list the feed pane shows.
type feedMode int

const (
	// feedTimeline is the public timeline, readable without a session.
	feedTimeline feedMode = iota
	// feedBookmarks is the signed-in account's saved posts.
	feedBookmarks
)

// String names the mode for the pane header.
func (f feedMode) String() string {
	if f == feedBookmarks {
		return "bookmarks"
	}
	return "timeline"
}

// feedModel is the post list pane.
type feedModel struct {
	mode     feedMode
	items    []api.Post
	total    int // bookmarks only: the server's total
	cursor   string
	more     bool // timeline only: the server has another page
	selected int
	loaded   bool
	loading  bool
}

// newFeedModel builds an empty timeline pane.
func newFeedModel() feedModel { return feedModel{} }

// reset clears the list for a fresh load.
func (f *feedModel) reset(mode feedMode) {
	f.mode = mode
	f.items = nil
	f.total = 0
	f.cursor = ""
	f.more = false
	f.selected = 0
	f.loaded = false
	f.loading = false
}

// selectedPost returns the highlighted post.
func (f *feedModel) selectedPost() *api.Post {
	if f.selected < 0 || f.selected >= len(f.items) {
		return nil
	}
	return &f.items[f.selected]
}

// hasMore reports whether another page exists.
func (f *feedModel) hasMore() bool {
	if f.mode == feedBookmarks {
		return len(f.items) < f.total
	}
	return f.more
}

// feedLoadedMsg carries one page of posts.
type feedLoadedMsg struct {
	mode   feedMode
	posts  []api.Post
	total  int
	cursor string
	more   bool
	first  int
	err    error
}

// loadFeedCmd fetches one page starting at first.
func (m *Model) loadFeedCmd(mode feedMode, first int) tea.Cmd {
	return m.withTimeout(func(ctx context.Context) tea.Msg {
		if m.Deps.Content == nil {
			return feedLoadedMsg{mode: mode, first: first, err: fmt.Errorf("no content client configured")}
		}
		if mode == feedBookmarks {
			if err := m.requireSession(ctx); err != nil {
				return feedLoadedMsg{mode: mode, first: first, err: err}
			}
			page, err := m.Deps.Content.Bookmarks(ctx, first, feedPageSize)
			return feedLoadedMsg{
				mode: mode, posts: page.Items, total: page.Total, first: first, err: err,
			}
		}
		cursor := ""
		if first > 0 {
			cursor = m.Feed.cursor
		}
		page, err := m.Deps.Content.Timeline(ctx, cursor, feedPageSize, "", true)
		if err != nil {
			return feedLoadedMsg{mode: mode, first: first, err: err}
		}
		return feedLoadedMsg{
			mode: mode, posts: page.Posts(), cursor: page.NextCursor,
			more: page.NextCursor != "", first: first,
		}
	})
}

// requireSession makes sure an access token is available before a write or a
// private read.
func (m *Model) requireSession(ctx context.Context) error {
	if m.guest || m.Deps.Tokens == nil {
		return api.ErrNoCredentials
	}
	_, err := m.Deps.Tokens.Token(ctx)
	return err
}

// feedView renders the post list.
func (m *Model) feedView() string {
	f := &m.Feed
	width := m.contentWidth()
	height := m.bodyHeight()

	if !f.loaded {
		return m.pane("Feed", f.mode.String(),
			[]string{m.Spinner.View() + " " + m.Styles.Muted.Render("loading posts…")})
	}

	var body []string
	if len(f.items) == 0 {
		body = append(body, m.Styles.Muted.Render("no posts here yet"))
	}

	// Lay the cards out, then window them around the selection.
	lines := make([][]string, len(f.items))
	for i, post := range f.items {
		lines[i] = m.renderPostCard(post, width, 3, i == f.selected)
	}
	for _, i := range visibleCards(lines, f.selected, height) {
		body = append(body, lines[i]...)
	}
	count := fmt.Sprintf("%d posts", len(f.items))
	if f.hasMore() {
		count += "+"
	}
	return m.pane("Feed", f.mode.String()+" · "+count, body)
}

// renderPostCard renders one post as card lines.
func (m *Model) renderPostCard(post api.Post, width int, bodyLines int, selected bool) []string {
	marker := "  "
	if selected {
		marker = m.Styles.Accent.Render("▸ ")
	}

	head := marker + m.Styles.Bold.Render(post.AuthorName())
	handle := "@" + post.AuthorHandle()
	when := ago(post.When())
	right := handle
	if when != "" {
		right += " · " + when
	}
	gap := width - 2 - ansi.StringWidth(post.AuthorName()) - ansi.StringWidth(right)
	if gap < 1 {
		gap = 1
	}
	head += strings.Repeat(" ", gap) + m.Styles.Muted.Render(right)

	out := []string{head}

	body := post.Body()
	if title := postTitle(post); title != "" {
		// A title is a headline, not prose: one truncated line.
		out = append(out, "  "+m.Styles.Bold.Render(truncate(title, width-2)))
	}
	wrapped := wrapText(body, width-2)
	if bodyLines > 0 && len(wrapped) > bodyLines {
		wrapped = append(wrapped[:bodyLines], m.Styles.Muted.Render("…"))
	}
	for _, line := range wrapped {
		out = append(out, "  "+line)
	}

	if len(post.Attachments) > 0 {
		out = append(out, m.attachmentLines(post, width, bodyLines)...)
	}

	stats := postStats(post)
	if stats != "" {
		out = append(out, "  "+m.Styles.Muted.Render(stats))
	}
	out = append(out, "")
	return out
}

// attachmentLines renders a post's attachments according to the configured
// policy: names only, names plus an inline image preview, or nothing.
func (m *Model) attachmentLines(post api.Post, width int, bodyLines int) []string {
	switch m.Deps.Attachments {
	case "off":
		return nil
	}
	out := []string{}
	for i, file := range post.Attachments {
		out = append(out, "  "+m.Styles.Muted.Render(attachmentLine(file, i+1)))
	}
	if m.Deps.Attachments != "preview" {
		// Names only: the detail pane opens the file in the browser.
		return out
	}
	// Preview mode draws the first image inline. The feed keeps its cards
	// short, so only the detail pane pays for the preview.
	if bodyLines > 0 {
		return out
	}
	for _, file := range previewableAttachments(post.Attachments, 1) {
		for _, line := range m.previewLines(file) {
			out = append(out, "  "+line)
		}
	}
	return out
}

// postTitle returns the post title, empty when unset.
func postTitle(post api.Post) string {
	if post.Title == nil {
		return ""
	}
	return strings.TrimSpace(*post.Title)
}

// postStats renders the counters and reaction summary of a post.
func postStats(post api.Post) string {
	var parts []string
	if post.RepliesCount > 0 {
		parts = append(parts, plural(post.RepliesCount, "reply", "replies"))
	}
	if reactions := reactionSummary(post.Reactions); reactions != "" {
		parts = append(parts, reactions)
	}
	if post.BoostCount > 0 {
		parts = append(parts, plural(post.BoostCount, "boost", "boosts"))
	}
	if post.ViewsTotal > 0 {
		parts = append(parts, plural(post.ViewsTotal, "view", "views"))
	}
	if post.IsBookmarked {
		parts = append(parts, "★ saved")
	}
	return strings.Join(parts, " · ")
}

// reactionSummary renders the reaction counts, most frequent first.
func reactionSummary(counts map[string]int) string {
	if len(counts) == 0 {
		return ""
	}
	type row struct {
		symbol string
		count  int
	}
	rows := make([]row, 0, len(counts))
	for symbol, count := range counts {
		if count > 0 {
			rows = append(rows, row{symbol: symbol, count: count})
		}
	}
	sort.Slice(rows, func(i, j int) bool {
		if rows[i].count != rows[j].count {
			return rows[i].count > rows[j].count
		}
		return rows[i].symbol < rows[j].symbol
	})
	parts := make([]string, 0, len(rows))
	for _, r := range rows {
		parts = append(parts, fmt.Sprintf("%s×%d", reactionLabel(r.symbol), r.count))
	}
	return strings.Join(parts, " ")
}

// reactionLabel maps a reaction symbol onto a terminal-safe label.
func reactionLabel(symbol string) string {
	switch symbol {
	case "thumb_up":
		return "+1"
	case "thumb_down":
		return "-1"
	case "heart":
		return "♥"
	case "laugh":
		return "^^"
	case "cry":
		return "T_T"
	case "clap":
		return "clap"
	case "party":
		return "party"
	case "hello":
		return "hi"
	}
	if symbol == "" {
		return "?"
	}
	return symbol
}

// reactionSymbol is the reaction the r key toggles.
const reactionSymbol = "heart"

// updateFeedKey handles keys on the feed pane.
func (m *Model) updateFeedKey(key tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	f := &m.Feed
	switch key.String() {
	case "esc":
		if m.guest {
			return m, m.leaveGuest()
		}
		m.showScreen(ScreenHome)
		return m, nil
	case "j", "down":
		if f.selected < len(f.items)-1 {
			f.selected++
			if f.selected >= len(f.items)-3 && f.hasMore() && !f.loading {
				f.loading = true
				return m, m.loadFeedCmd(f.mode, len(f.items))
			}
		}
		return m, nil
	case "k", "up":
		if f.selected > 0 {
			f.selected--
		}
		return m, nil
	case "g":
		// Guest browsing has no session to keep: g returns to the login pane.
		if m.guest {
			return m, m.leaveGuest()
		}
		return m, nil
	case "G":
		if len(f.items) > 0 {
			f.selected = len(f.items) - 1
		}
		return m, nil
	case "t":
		mode := feedTimeline
		if f.mode == feedTimeline {
			mode = feedBookmarks
			if err := m.sessionGuard("bookmarks need a session — press g to sign in"); err != nil {
				return m, nil
			}
		}
		f.reset(mode)
		return m, m.loadFeedCmd(mode, 0)
	case "R":
		return m, m.loadFeedCmd(f.mode, 0)
	case "enter":
		post := f.selectedPost()
		if post == nil {
			return m, nil
		}
		m.postStack = []string{post.ID}
		m.Post = newPostModel()
		m.Post.post = *post
		m.Post.loaded = true
		m.showScreen(ScreenPost)
		return m, tea.Batch(m.loadPostCmd(post.ID, true), m.previewFocusedCmd())
	case "r":
		post := f.selectedPost()
		if post == nil {
			return m, nil
		}
		if err := m.sessionGuard("sign in to react"); err != nil {
			return m, nil
		}
		return m, m.toggleReactionCmd(post.ID, reactionSymbol)
	case "b":
		post := f.selectedPost()
		if post == nil {
			return m, nil
		}
		if err := m.sessionGuard("sign in to bookmark"); err != nil {
			return m, nil
		}
		return m, m.toggleBookmarkCmd(*post)
	case "c":
		if err := m.sessionGuard("sign in to post"); err != nil {
			return m, nil
		}
		return m, m.openCompose(nil)
	case "e":
		post := f.selectedPost()
		if post == nil {
			return m, nil
		}
		if err := m.sessionGuard("sign in to edit"); err != nil {
			return m, nil
		}
		return m, m.openEdit(*post)
	case "d":
		post := f.selectedPost()
		if post == nil {
			return m, nil
		}
		if err := m.sessionGuard("sign in to delete"); err != nil {
			return m, nil
		}
		return m, m.confirmDeletePost(*post)
	}
	return m, nil
}

// sessionGuard reports whether a write is possible, showing a banner when it
// is not.
func (m *Model) sessionGuard(message string) error {
	if m.guest || m.Deps.Tokens == nil || m.Deps.Tokens.Credentials() == nil {
		m.setBanner(message)
		return api.ErrNoCredentials
	}
	return nil
}

// updateFeedMsg applies asynchronous feed results.
func (m *Model) updateFeedMsg(msg tea.Msg) (tea.Model, tea.Cmd) {
	f := &m.Feed
	switch msg := msg.(type) {
	case feedLoadedMsg:
		f.loading = false
		if msg.err != nil {
			if apiIsSessionExpired(msg.err) || isNoCredentials(msg.err) {
				f.loaded = true
				m.setBanner("bookmarks need a session — press g to sign in")
				return m, nil
			}
			m.setBanner("feed: " + msg.err.Error())
			return m, nil
		}
		f.loaded = true
		f.total = msg.total
		f.cursor = msg.cursor
		f.more = msg.more
		if msg.first == 0 {
			f.items = msg.posts
			f.selected = min(f.selected, max(len(f.items)-1, 0))
		} else {
			f.items = append(f.items, msg.posts...)
		}
		m.clearBanner()
		return m, nil
	}
	return m, nil
}

// toggleReactionCmd toggles a reaction on a post.
func (m *Model) toggleReactionCmd(postID, symbol string) tea.Cmd {
	return m.withTimeout(func(ctx context.Context) tea.Msg {
		if err := m.requireSession(ctx); err != nil {
			return reactionMsg{postID: postID, symbol: symbol, err: err}
		}
		on, err := m.Deps.Content.ToggleReaction(ctx, postID, symbol)
		return reactionMsg{postID: postID, symbol: symbol, on: on, err: err}
	})
}

// reactionMsg reports a completed reaction toggle.
type reactionMsg struct {
	postID string
	symbol string
	on     bool
	err    error
}

// toggleBookmarkCmd adds or removes a bookmark.
func (m *Model) toggleBookmarkCmd(post api.Post) tea.Cmd {
	return m.withTimeout(func(ctx context.Context) tea.Msg {
		if err := m.requireSession(ctx); err != nil {
			return bookmarkMsg{postID: post.ID, err: err}
		}
		var err error
		if post.IsBookmarked {
			err = m.Deps.Content.Unbookmark(ctx, post.ID)
		} else {
			err = m.Deps.Content.Bookmark(ctx, post.ID)
		}
		return bookmarkMsg{postID: post.ID, bookmarked: !post.IsBookmarked, err: err}
	})
}

// bookmarkMsg reports a completed bookmark toggle.
type bookmarkMsg struct {
	postID     string
	bookmarked bool
	err        error
}

// applyReaction records a reaction toggle on every copy of the post.
func (m *Model) applyReaction(msg reactionMsg) {
	if msg.err != nil {
		m.reportContentError("react", msg.err)
		return
	}
	m.updatePost(msg.postID, func(post *api.Post) {
		if post.Reactions == nil {
			post.Reactions = map[string]int{}
		}
		if post.ReactionsMade == nil {
			post.ReactionsMade = map[string]bool{}
		}
		if msg.on {
			post.Reactions[msg.symbol]++
			post.ReactionsMade[msg.symbol] = true
		} else {
			post.Reactions[msg.symbol]--
			if post.Reactions[msg.symbol] <= 0 {
				delete(post.Reactions, msg.symbol)
			}
			delete(post.ReactionsMade, msg.symbol)
		}
	})
	state := "reacted"
	if !msg.on {
		state = "reaction removed"
	}
	m.setNotice(state)
}

// applyBookmark records a bookmark toggle on every copy of the post.
func (m *Model) applyBookmark(msg bookmarkMsg) {
	if msg.err != nil {
		m.reportContentError("bookmark", msg.err)
		return
	}
	m.updatePost(msg.postID, func(post *api.Post) { post.IsBookmarked = msg.bookmarked })
	if msg.bookmarked {
		m.setNotice("saved to bookmarks")
		return
	}
	m.setNotice("removed from bookmarks")
	if m.Feed.mode == feedBookmarks {
		kept := m.Feed.items[:0]
		for _, post := range m.Feed.items {
			if post.ID != msg.postID {
				kept = append(kept, post)
			}
		}
		m.Feed.items = kept
		m.Feed.total = max(m.Feed.total-1, len(kept))
		m.Feed.selected = min(m.Feed.selected, max(len(kept)-1, 0))
	}
}

// updatePost applies fn to every loaded copy of a post.
func (m *Model) updatePost(id string, fn func(post *api.Post)) {
	for i := range m.Feed.items {
		if m.Feed.items[i].ID == id {
			fn(&m.Feed.items[i])
		}
	}
	if m.Post.post.ID == id {
		fn(&m.Post.post)
	}
	for i := range m.Post.replies {
		if m.Post.replies[i].ID == id {
			fn(&m.Post.replies[i])
		}
	}
}

// reportContentError turns a content failure into a banner, keeping session
// problems actionable.
func (m *Model) reportContentError(what string, err error) {
	if apiIsSessionExpired(err) || isNoCredentials(err) {
		m.setBanner("session expired — press l to sign out, or g to browse as guest")
		return
	}
	m.setBanner(what + ": " + err.Error())
}

// isNoCredentials reports whether err means "this needs a session".
func isNoCredentials(err error) bool {
	return err != nil && err.Error() == api.ErrNoCredentials.Error()
}

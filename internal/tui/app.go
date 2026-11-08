// Package tui is the interactive Termian front end: a Bubble Tea program with a
// device-flow login pane, a password/TOTP challenge pane and a session shell.
package tui

import (
	"context"
	"fmt"
	"os/exec"
	"runtime"
	"strings"
	"sync"
	"time"

	"charm.land/bubbles/v2/spinner"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/colorprofile"
	"github.com/charmbracelet/x/ansi"

	"src.solsynth.dev/sosys/termian/internal/api"
	"src.solsynth.dev/sosys/termian/internal/credentials"
)

// Screen identifies the visible pane.
type Screen int

// Screens in navigation order.
const (
	ScreenLogin Screen = iota
	ScreenPassword
	ScreenHome
	ScreenFeed
	ScreenPost
	ScreenChats
	ScreenChat
	ScreenCompose
	ScreenNotifications
)

// Router aliases
const (
	ScreenProfile Screen = ScreenHome
	ScreenPosts   Screen = ScreenFeed
)

// String renders the screen name (used in tests and diagnostics).
func (s Screen) String() string {
	switch s {
	case ScreenLogin:
		return "login"
	case ScreenPassword:
		return "password"
	case ScreenHome:
		return "home"
	case ScreenFeed:
		return "feed"
	case ScreenPost:
		return "post"
	case ScreenChats:
		return "chats"
	case ScreenChat:
		return "chat"
	case ScreenCompose:
		return "compose"
	case ScreenNotifications:
		return "notifications"
	default:
		return "unknown"
	}
}

// IncomingChatMsg is emitted when real-time chat messages arrive via background goroutines.
type IncomingChatMsg struct {
	RoomID  string
	Sender  string
	Content string
}

// SwitchScreenMsg requests the router to jump to another screen.
type SwitchScreenMsg struct {
	Target  Screen
	Payload any
}

// RootModel is an alias for Model to match the sub-model router pattern.
type RootModel = Model

// NewRootModel constructs the router model.
func NewRootModel(deps Deps) *Model {
	return NewModel(deps)
}

// AuthAPI is the subset of the Stargate client the login panes depend on.
type AuthAPI interface {
	RequestDeviceCode(context.Context) (api.DeviceCode, error)
	PollDeviceToken(context.Context, string) (api.OAuthToken, error)
	CreateChallenge(context.Context, string) (api.Challenge, error)
	Factors(context.Context, string) ([]api.Factor, error)
	RequestFactorCode(context.Context, string, string) error
	SubmitFactor(context.Context, string, string, string) (api.Challenge, error)
	ExchangeChallenge(context.Context, string) (api.TokenPair, error)
	Challenge(context.Context, string) (api.Challenge, error)
	Identity(context.Context) (api.Identity, error)
	Account(context.Context) (api.Account, error)
}

// ContentAPI is the subset of the Solian gateways the feeds and chats use.
// Reads work without a session; writes need one.
type ContentAPI interface {
	Timeline(context.Context, string, int, string, bool) (api.TimelinePage, error)
	Bookmarks(context.Context, int, int) (api.Page[api.Post], error)
	Post(context.Context, string) (api.Post, error)
	Replies(context.Context, string, int, int) (api.Page[api.Post], error)
	CreatePost(context.Context, api.NewPost) (api.Post, error)
	UpdatePost(context.Context, string, api.PostPatch) (api.Post, error)
	DeletePost(context.Context, string) error
	ToggleReaction(context.Context, string, string) (bool, error)
	Bookmark(context.Context, string) error
	Unbookmark(context.Context, string) error
	Publishers(context.Context) ([]api.Publisher, error)
	ChatRoomList(context.Context, int, int) (api.ChatRooms, error)
	ChatMembers(context.Context, string, int, int) (api.Page[api.ChatMember], error)
	ChatRoom(context.Context, string) (api.ChatRoom, error)
	ChatMessages(context.Context, string, int, int) (api.Page[api.ChatMessage], error)
	ChatSummaryByRoom(context.Context) (map[string]api.ChatSummary, error)
	SendChatMessage(context.Context, string, api.NewMessage) (api.ChatMessage, error)
	EditChatMessage(context.Context, string, string, string) (api.ChatMessage, error)
	DeleteChatMessage(context.Context, string, string) error
	ToggleChatReaction(context.Context, string, string, string) (bool, error)
}

// commandTimeout bounds every asynchronous API call made from the UI.
const commandTimeout = 15 * time.Second

// Deps is the static configuration the model needs.
type Deps struct {
	// Ctx is cancelled when the program exits.
	Ctx context.Context
	// API performs the auth calls.
	API AuthAPI
	// Content performs the feed and chat calls.
	Content ContentAPI
	// Tokens owns the access token; nil before the first successful login.
	Tokens *api.TokenSource
	// Store persists the refresh token.
	Store *credentials.Store
	// Server is the normalized API base used as the credential key.
	Server string
	// ClientID is the OAuth client used by the device flow.
	ClientID string
	// DeviceID is this machine's stable identity.
	DeviceID string
	// Version is the Termian version shown in the UI.
	Version string
	// Attachments is the attachment policy: browser, preview or off.
	Attachments string
	// ChatHistory is how many messages a chat pane keeps per room.
	ChatHistory int
	// PollInterval overrides the server device poll interval (tests).
	PollInterval time.Duration
}

// Model is the root Bubble Tea model.
type Model struct {
	Screen   Screen
	Width    int
	Height   int
	Dark     bool
	Styles   Styles
	Banner   string
	BannerOK bool
	Spinner  spinner.Model
	Login    loginModel
	Password passwordModel
	Home     homeModel
	Feed     feedModel
	Post     postModel
	Chats    chatsModel
	Chat     chatModel
	Compose  composeModel
	Confirm  confirmModel
	Deps     Deps

	// guest is set when the session is browsing without a session, so the
	// write paths ask for a sign-in instead of failing at the server.
	guest bool
	// postStack holds the post IDs behind the visible detail pane, so esc can
	// walk back through drilled-in replies.
	postStack []string
	// previews caches rendered attachment previews by file ID.
	previews map[string][]string
	// truecolor reports whether the terminal can draw 24-bit previews.
	truecolor bool

	sized bool

	// mu guards the mutable UI state so observers (tests, diagnostics) can take
	// a consistent snapshot while the program goroutine updates it.
	mu sync.Mutex

	unreadChat    int
	quickSwitch   bool
	quickInput    string
	quickSelected int
}

// Snapshot is a consistent view of the model's state.
type Snapshot struct {
	Screen         Screen
	PasswordStage  passwordStage
	LoginFinished  bool
	HomeLoaded     bool
	Banner         string
	BannerOK       bool
	AccountName    string
	SessionCredsOK bool
	RefreshBlocked bool
	Guest          bool
	FeedCount      int
	FeedSelected   int
	PostID         string
	PostSelected   int
	ReplyCount     int
	ChatCount      int
	ChatRoomID     string
	MessageCount   int
	ChatFocused    bool
	Section        string
	Composing      bool
	Editing        bool
	Confirming     bool
}

// Snapshot returns the current UI state under the model lock.
func (m *Model) Snapshot() Snapshot {
	m.mu.Lock()
	defer m.mu.Unlock()

	snap := Snapshot{
		Screen:         m.Screen,
		PasswordStage:  m.Password.Stage,
		LoginFinished:  m.Login.finished,
		HomeLoaded:     m.Home.loaded,
		Banner:         m.Banner,
		BannerOK:       m.BannerOK,
		AccountName:    m.Home.account.Name,
		RefreshBlocked: m.Home.refreshDisabled,
		Guest:          m.guest,
		FeedCount:      len(m.Feed.items),
		FeedSelected:   m.Feed.selected,
		PostID:         m.Post.post.ID,
		PostSelected:   m.Post.selected,
		ReplyCount:     len(m.Post.replies),
		ChatCount:      len(m.Chats.rooms),
		ChatRoomID:     m.Chat.room.ID,
		MessageCount:   len(m.Chat.messages),
		ChatFocused:    m.Chats.focused,
		Section:        m.currentSection().String(),
		Composing:      m.Compose.active,
		Editing:        m.Compose.editing != nil,
		Confirming:     m.Confirm.active,
	}
	if m.Deps.Tokens != nil {
		snap.SessionCredsOK = m.Deps.Tokens.Credentials() != nil
	}
	return snap
}

// NewModel builds the initial model on the device login pane.
func NewModel(deps Deps) *Model {
	return NewModelWithScreen(deps, ScreenLogin)
}

// NewModelWithScreen builds a model starting on a specific screen.
func NewModelWithScreen(deps Deps, screen Screen) *Model {
	sp := spinner.New()
	sp.Spinner = spinner.Dot

	m := &Model{
		Screen:   screen,
		Dark:     true,
		Spinner:  sp,
		Deps:     deps,
		Login:    newLoginModel(),
		Home:     newHomeModel(),
		Feed:     newFeedModel(),
		Post:     newPostModel(),
		Chats:    newChatsModel(),
		Chat:     newChatModel(),
		Compose:  newComposeModel(),
		previews: map[string][]string{},
	}
	m.Styles = newStyles(m.Dark)
	m.Password = newPasswordModel(m.Styles)
	m.Compose.input.SetStyles(textareaStyles(m.Styles))
	m.Chat.input.SetStyles(textareaStyles(m.Styles))
	if screen == ScreenPassword {
		m.Password.reset()
	}
	return m
}

// Init implements tea.Model.
func (m *Model) Init() tea.Cmd {
	cmds := []tea.Cmd{m.Spinner.Tick, tea.RequestBackgroundColor}
	switch {
	case m.Screen == ScreenHome && m.Deps.Tokens != nil:
		cmds = append(cmds, tea.Tick(time.Second, func(t time.Time) tea.Msg { return homeTickMsg{at: t} }))
		cmds = append(cmds, m.fetchAccountCmd())
	case m.Screen == ScreenFeed, m.Screen == ScreenPost, m.Screen == ScreenChats:
		cmds = append(cmds, m.initContentCmd())
	default:
		cmds = append(cmds, m.startDeviceFlowCmd())
	}
	return tea.Batch(cmds...)
}

// initContentCmd loads the pane the program starts on.
func (m *Model) initContentCmd() tea.Cmd {
	switch m.Screen {
	case ScreenFeed:
		return m.loadFeedCmd(m.Feed.mode, 0)
	case ScreenChats:
		return m.loadChatsCmd()
	case ScreenPost:
		if m.Post.post.ID != "" {
			return m.loadPostCmd(m.Post.post.ID, true)
		}
	}
	return nil
}

// View implements tea.Model.
func (m *Model) View() tea.View {
	m.mu.Lock()
	defer m.mu.Unlock()

	v := tea.NewView(m.render())
	v.AltScreen = true
	v.WindowTitle = "Termian — Solian"
	return v
}

// render draws the active screen framed by the global UI shell (or centered login card).
func (m *Model) render() string {
	if m.tooSmall() {
		return lipgloss.Place(max(m.Width, 1), max(m.Height, 1), lipgloss.Center, lipgloss.Center,
			m.Styles.Muted.Render("terminal too small"))
	}

	if m.Screen == ScreenLogin || m.Screen == ScreenPassword {
		return m.renderLoginFlow()
	}

	header := m.renderHeader()
	body := m.renderScreenBody()
	framed := m.frameBody(body)
	footer := m.renderFooter()

	return lipgloss.JoinVertical(lipgloss.Left, header, framed, footer)
}

// renderLoginFlow renders the pre-session login cards centered on the terminal.
func (m *Model) renderLoginFlow() string {
	var body string
	switch m.Screen {
	case ScreenLogin:
		body = m.loginView()
	case ScreenPassword:
		body = m.passwordView()
	}

	if m.Confirm.active {
		body = lipgloss.JoinVertical(lipgloss.Center, body, "", m.confirmView())
	} else if m.Banner != "" {
		body = lipgloss.JoinVertical(lipgloss.Center, body, "", m.bannerView())
	}

	if !m.sized {
		return body
	}
	return lipgloss.Place(m.Width, m.Height, lipgloss.Center, lipgloss.Center, body)
}

// renderScreenBody renders the active screen's body.
func (m *Model) renderScreenBody() string {
	if m.quickSwitch {
		return m.quickSwitchView()
	}

	var body string
	switch m.Screen {
	case ScreenHome:
		body = m.homeView()
	case ScreenFeed:
		body = m.feedView()
	case ScreenPost:
		body = m.postView()
	case ScreenChats, ScreenChat:
		body = m.chatsView()
	case ScreenCompose:
		body = m.composeView()
	case ScreenNotifications:
		body = m.notificationsView()
	default:
		body = m.Styles.Muted.Render("screen not found")
	}

	if m.Confirm.active {
		body = lipgloss.JoinVertical(lipgloss.Left, body, "", m.confirmView())
	}
	return body
}

// frameBody wraps the active screen content inside the persistent shell borders.
func (m *Model) frameBody(body string) string {
	borderStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("#555555"))
	innerWidth := m.Width - 4
	if innerWidth < 1 {
		innerWidth = 1
	}
	bodyHeight := m.bodyHeight()

	rawLines := strings.Split(body, "\n")
	clipped := clipLines(rawLines, bodyHeight)

	rows := make([]string, 0, bodyHeight)
	for _, l := range clipped {
		rows = append(rows, borderStyle.Render("│ ")+padRight(l, innerWidth)+borderStyle.Render(" │"))
	}
	for len(rows) < bodyHeight {
		rows = append(rows, borderStyle.Render("│ ")+strings.Repeat(" ", innerWidth)+borderStyle.Render(" │"))
	}

	return lipgloss.JoinVertical(lipgloss.Left, rows...)
}

// renderHeader draws the persistent global top navigation bar framed by box borders.
func (m *Model) renderHeader() string {
	width := m.Width
	if width < MinWidth {
		width = MinWidth
	}

	borderStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("#555555"))
	titleStyle := lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("#FFFFFF"))

	title := borderStyle.Render("┌─ ") + titleStyle.Render("Solian Terminal") + borderStyle.Render(" ─")

	tabStyle := lipgloss.NewStyle().Padding(0, 1)
	activeTabStyle := tabStyle.Bold(true).Foreground(lipgloss.Color("#FFFFFF")).Background(lipgloss.Color("#7D56F4"))
	inactiveTabStyle := tabStyle.Foreground(lipgloss.Color("#888888"))
	unreadDot := lipgloss.NewStyle().Foreground(lipgloss.Color("#FF5F87")).Bold(true).Render("● ")

	var tabs []string

	// [1] Posts
	postsActive := m.Screen == ScreenFeed || m.Screen == ScreenPost || m.Screen == ScreenCompose
	if postsActive {
		tabs = append(tabs, activeTabStyle.Render("[1] Posts"))
	} else {
		tabs = append(tabs, inactiveTabStyle.Render("[1] Posts"))
	}

	// [2] Chat
	chatActive := m.Screen == ScreenChats || m.Screen == ScreenChat
	unread := m.totalUnread()
	chatLabel := "[2] Chat"
	if unread > 0 {
		chatLabel = fmt.Sprintf("[2] Chat (%d)", unread)
	}
	if chatActive {
		tabs = append(tabs, activeTabStyle.Render(chatLabel))
	} else {
		if unread > 0 {
			tabs = append(tabs, unreadDot+inactiveTabStyle.Render(chatLabel))
		} else {
			tabs = append(tabs, inactiveTabStyle.Render(chatLabel))
		}
	}

	// [3] Notifications
	notifsActive := m.Screen == ScreenNotifications
	notifLabel := "[3] Notifications"
	if width < 85 {
		notifLabel = "[3] Notifs"
	}
	if notifsActive {
		tabs = append(tabs, activeTabStyle.Render(notifLabel))
	} else {
		tabs = append(tabs, inactiveTabStyle.Render(notifLabel))
	}

	// [4] Profile
	profileActive := m.Screen == ScreenHome
	if profileActive {
		tabs = append(tabs, activeTabStyle.Render("[4] Profile"))
	} else {
		tabs = append(tabs, inactiveTabStyle.Render("[4] Profile"))
	}

	tabsBar := strings.Join(tabs, " ")

	titleWidth := ansi.StringWidth(title)
	tabsWidth := ansi.StringWidth(tabsBar)

	gap := width - titleWidth - tabsWidth - 2
	if gap < 1 {
		gap = 1
	}

	return title + borderStyle.Render(strings.Repeat("─", gap)) + tabsBar + borderStyle.Render("─┐")
}

// renderFooter draws the persistent global footer with user identity, keybinds and status.
func (m *Model) renderFooter() string {
	width := m.Width
	if width < MinWidth {
		width = MinWidth
	}
	borderStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("#555555"))
	sepStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("#555555"))
	sep := sepStyle.Render(" │ ")

	userHandle := "@guest"
	if m.Home.account.Name != "" {
		userHandle = "@" + m.Home.account.Name
	} else if m.Home.identity.Name != "" {
		userHandle = "@" + m.Home.identity.Name
	}
	deviceTag := "ed25519"
	if m.Deps.DeviceID != "" {
		deviceTag = m.Deps.DeviceID
	}
	userTag := lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("#AAAAAA")).Render(fmt.Sprintf("%s (%s)", userHandle, deviceTag))

	var status string
	if m.Banner != "" {
		if m.BannerOK {
			status = m.Styles.Notice.Render(m.Banner)
		} else {
			status = m.Styles.Error.Render(m.Banner)
		}
	}

	var keybinds string
	switch {
	case m.Confirm.active:
		keybinds = "[y] Confirm │ [n] Cancel"
	case m.Compose.active:
		keybinds = "[Ctrl+D] Publish │ [Esc] Cancel"
	case (m.Screen == ScreenChats || m.Screen == ScreenChat) && m.Chat.typing:
		keybinds = "[Enter] Send │ [Alt+Enter] Newline │ [Esc] Normal Mode"
	case m.Screen == ScreenChats || m.Screen == ScreenChat:
		if m.Chats.focused {
			keybinds = "[1-4] Switch Screen │ [i] Focus Chat │ [Esc] Normal Mode │ [q] Quit"
		} else {
			keybinds = "[1-4] Switch Screen │ [Enter/i] Focus Chat │ [j/k] Drawer │ [q] Quit"
		}
	case m.Screen == ScreenFeed:
		keybinds = "[1-4] Switch Screen │ [/] Search │ [c] New Post │ [q] Quit"
	case m.Screen == ScreenPost:
		keybinds = "[1-4] Switch Screen │ [j/k] Scroll │ [n] Reply │ [Esc] Feed │ [q] Quit"
	case m.Screen == ScreenNotifications:
		keybinds = "[1-4] Switch Screen │ [j/k] Scroll │ [q] Quit"
	case m.Screen == ScreenHome:
		keybinds = "[1-4] Switch Screen │ [r] Refresh │ [l] Logout │ [q] Quit"
	default:
		keybinds = "[1-4] Switch Screen │ [q] Quit"
	}

	keyStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("#888888"))
	statusLine := userTag + sep + keyStyle.Render(keybinds)
	if status != "" {
		statusLine += sep + status
	}

	innerWidth := width - 4
	if innerWidth < 1 {
		innerWidth = 1
	}

	line1 := borderStyle.Render("├" + strings.Repeat("─", width-2) + "┤")
	line2 := borderStyle.Render("│ ") + padRight(statusLine, innerWidth) + borderStyle.Render(" │")
	line3 := borderStyle.Render("└" + strings.Repeat("─", width-2) + "┘")

	return lipgloss.JoinVertical(lipgloss.Left, line1, line2, line3)
}

// isInputFocused queries active models to verify if text entry is active,
// avoiding hijacking global keys like '1', '2', 'c', or 'q'.
func (m *Model) isInputFocused() bool {
	if m.quickSwitch {
		return true
	}
	if m.Confirm.active {
		return true
	}
	if m.Compose.active {
		return true
	}
	if (m.Screen == ScreenChats || m.Screen == ScreenChat) && m.Chat.typing {
		return true
	}
	if m.Screen == ScreenPassword {
		return true
	}
	return false
}

// totalUnread computes all unread messages across rooms and background events.
func (m *Model) totalUnread() int {
	total := m.unreadChat
	for _, s := range m.Chats.summary {
		total += s.UnreadCount
	}
	return total
}

// bannerView renders the status line.
func (m *Model) bannerView() string {
	if m.BannerOK {
		return m.Styles.Notice.Render(m.Banner)
	}
	return m.Styles.Error.Render(m.Banner)
}

// setBanner shows a failure message below the active card.
func (m *Model) setBanner(msg string) {
	if msg == "" {
		m.clearBanner()
		return
	}
	m.Banner = msg
	m.BannerOK = false
}

// setNotice shows a success message below the active card.
func (m *Model) setNotice(msg string) {
	m.Banner = msg
	m.BannerOK = true
}

// clearBanner removes the status line.
func (m *Model) clearBanner() {
	m.Banner = ""
	m.BannerOK = false
}

// tooSmall reports whether the layout does not fit the terminal.
func (m *Model) tooSmall() bool {
	return m.sized && (m.Width < MinWidth || m.Height < MinHeight)
}

// Update implements tea.Model.
func (m *Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	m.mu.Lock()
	defer m.mu.Unlock()

	// Global keys win over every screen handler.
	if key, ok := msg.(tea.KeyPressMsg); ok {
		if key.String() == "ctrl+c" {
			return m, tea.Quit
		}
		if m.tooSmall() {
			return m, nil
		}
	}

	switch msg := msg.(type) {
	case IncomingChatMsg:
		// Forward background chat messages to chat model
		if msg.RoomID == "" || (m.Chat.room.ID != "" && msg.RoomID == m.Chat.room.ID) {
			newMsg := api.ChatMessage{
				ID:        fmt.Sprintf("msg-in-%d", time.Now().UnixNano()),
				RoomID:    m.Chat.room.ID,
				Type:      "text",
				Content:   &msg.Content,
				Sender:    api.ChatMember{Account: &api.ChatUser{Name: msg.Sender, Nick: msg.Sender}},
				CreatedAt: time.Now(),
			}
			m.Chat.messages = append(m.Chat.messages, newMsg)
		}
		if msg.RoomID != "" {
			if summary, ok := m.Chats.summary[msg.RoomID]; ok {
				summary.UnreadCount++
				m.Chats.summary[msg.RoomID] = summary
			}
		}
		if m.Screen != ScreenChats && m.Screen != ScreenChat {
			m.unreadChat++
		}
		return m, nil

	case SwitchScreenMsg:
		m.showScreen(msg.Target)
		if msg.Target == ScreenChats || msg.Target == ScreenChat {
			m.unreadChat = 0
			if roomID, ok := msg.Payload.(string); ok && roomID != "" {
				for i, r := range m.Chats.rooms {
					if r.ID == roomID {
						m.Chats.selected = i
						return m, m.openRoom()
					}
				}
			}
		} else if msg.Target == ScreenFeed || msg.Target == ScreenPost {
			if postID, ok := msg.Payload.(string); ok && postID != "" {
				m.postStack = []string{postID}
				m.Post = newPostModel()
				m.Post.post.ID = postID
				m.Post.loaded = true
				m.showScreen(ScreenPost)
				return m, m.loadPostCmd(postID, true)
			}
		}
		return m, nil

	case tea.WindowSizeMsg:
		m.Width, m.Height = msg.Width, msg.Height
		m.sized = true
		contentWidth := m.contentWidth()
		m.Login.setWidth(cardWidth(DeviceCardWidth, msg.Width))
		m.Password.setWidth(cardWidth(PasswordCardWidth, msg.Width))
		m.Chat.setWidth(m.detailWidth())
		m.Compose.setWidth(contentWidth)
		return m, nil

	case tea.ColorProfileMsg:
		// True color terminals get half-block previews; others get ASCII art.
		m.truecolor = msg.Profile == colorprofile.TrueColor
		return m, nil

	case tea.BackgroundColorMsg:
		dark := msg.IsDark()
		if dark != m.Dark {
			m.Dark = dark
			m.Styles = newStyles(dark)
			m.Password.applyStyles(m.Styles)
			m.Chat.input.SetStyles(textareaStyles(m.Styles))
			m.Compose.input.SetStyles(textareaStyles(m.Styles))
		}
		return m, nil

	case spinner.TickMsg:
		var cmd tea.Cmd
		m.Spinner, cmd = m.Spinner.Update(msg)
		return m, cmd

	case tea.KeyPressMsg:
		return m.handleKey(msg)
	}

	return m.handleScreenMsg(msg)
}

// handleKey routes a key press to the active screen, or to the confirmation
// modal when one is open.
func (m *Model) handleKey(key tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	if m.quickSwitch {
		return m.updateQuickSwitchKey(key)
	}
	if m.Confirm.active {
		return m.updateConfirmKey(key)
	}

	// Global hotkey routing when not typing in an input buffer
	if !m.isInputFocused() && m.Screen != ScreenLogin && m.Screen != ScreenPassword {
		switch key.String() {
		case "1":
			m.showScreen(ScreenFeed)
			if !m.Feed.loaded {
				return m, m.loadFeedCmd(m.Feed.mode, 0)
			}
			return m, nil
		case "2":
			m.showScreen(ScreenChats)
			m.unreadChat = 0
			if !m.Chats.loaded {
				return m, m.loadChatsCmd()
			}
			return m, nil
		case "3":
			m.showScreen(ScreenNotifications)
			return m, nil
		case "4":
			m.showScreen(ScreenHome)
			if m.Deps.Tokens != nil && !m.Home.loaded {
				return m, m.fetchAccountCmd()
			}
			return m, nil
		case "ctrl+k", "/":
			m.quickSwitch = true
			m.quickInput = ""
			m.quickSelected = 0
			return m, nil
		case "q":
			return m, tea.Quit
		}
	}

	// Tab switches sections from every content pane.
	switch m.Screen {
	case ScreenFeed, ScreenPost, ScreenChats, ScreenChat:
		if updated, cmd, handled := m.sectionKey(key); handled {
			return updated, cmd
		}
	}
	switch m.Screen {
	case ScreenLogin:
		return m.updateLoginKey(key)
	case ScreenPassword:
		return m.updatePasswordKey(key)
	case ScreenHome:
		return m.updateHomeKey(key)
	case ScreenFeed:
		return m.updateFeedKey(key)
	case ScreenPost:
		return m.updatePostKey(key)
	case ScreenChats, ScreenChat:
		return m.updateChatKey(key)
	case ScreenCompose:
		return m.updateComposeKey(key)
	case ScreenNotifications:
		return m.updateNotificationsKey(key)
	}
	return m, nil
}

// handleScreenMsg routes asynchronous results to the pane that owns them.
func (m *Model) handleScreenMsg(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case deviceCodeMsg, pollDueMsg, deviceTokenMsg, browserDoneMsg:
		return m.updateLoginMsg(msg)
	case challengeMsg, factorsMsg, factorCodeMsg, stepResultMsg, exchangedMsg, challengeTickMsg:
		return m.updatePasswordMsg(msg)
	case authedMsg, refreshedMsg, loggedOutMsg, homeTickMsg:
		return m.updateHomeMsg(msg)
	case feedLoadedMsg:
		return m.updateFeedMsg(msg)
	case reactionMsg:
		m.applyReaction(msg)
		return m, nil
	case bookmarkMsg:
		m.applyBookmark(msg)
		return m, nil
	case postLoadedMsg:
		return m.updatePostMsg(msg)
	case roomsLoadedMsg:
		return m.updateChatsMsg(msg)
	case messagesLoadedMsg, membersLoadedMsg, sentMessageMsg, chatReactionMsg:
		return m.updateChatMsg(msg)
	case postCreatedMsg, replyCreatedMsg, postUpdatedMsg:
		return m.updateComposeMsg(msg)
	case postDeletedMsg:
		m.applyPostDeleted(msg)
		return m, nil
	case previewLoadedMsg:
		m.storePreview(msg)
		return m, nil
	case attachmentOpenMsg:
		if msg.err != nil {
			m.setBanner("could not open the attachment: " + msg.err.Error())
			return m, nil
		}
		m.setNotice("opened in the browser")
		return m, nil
	}
	return m, nil
}

// showScreen switches panes and clears transient status.
func (m *Model) showScreen(screen Screen) {
	m.Screen = screen
	m.clearBanner()
}

// withTimeout runs fn against a per-call timeout derived from the program
// context and returns its message.
func (m *Model) withTimeout(fn func(ctx context.Context) tea.Msg) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(m.Deps.Ctx, commandTimeout)
		defer cancel()
		return fn(ctx)
	}
}

// startDeviceFlowCmd requests a device authorization.
func (m *Model) startDeviceFlowCmd() tea.Cmd {
	return m.withTimeout(func(ctx context.Context) tea.Msg {
		code, err := m.Deps.API.RequestDeviceCode(ctx)
		return deviceCodeMsg{code: code, err: err}
	})
}

// pollDeviceTokenCmd performs one token poll.
func (m *Model) pollDeviceTokenCmd(deviceCode string) tea.Cmd {
	return m.withTimeout(func(ctx context.Context) tea.Msg {
		tok, err := m.Deps.API.PollDeviceToken(ctx, deviceCode)
		return deviceTokenMsg{tok: tok, err: err}
	})
}

// schedulePoll arms the next device token poll.
func (m *Model) schedulePoll(interval time.Duration) tea.Cmd {
	return tea.Tick(interval, func(t time.Time) tea.Msg { return pollDueMsg{at: t} })
}

// scheduleChallengePoll arms the 2 s cross-device challenge poll.
func (m *Model) scheduleChallengePoll() tea.Cmd {
	return tea.Tick(2*time.Second, func(time.Time) tea.Msg { return challengeTickMsg{due: true} })
}

// fetchChallengeCmd re-reads a challenge after a poll tick.
func (m *Model) fetchChallengeCmd(challengeID string) tea.Cmd {
	return m.withTimeout(func(ctx context.Context) tea.Msg {
		ch, err := m.Deps.API.Challenge(ctx, challengeID)
		return challengeTickMsg{ch: ch, err: err}
	})
}

// fetchAccountCmd loads the identity and account using the current token.
func (m *Model) fetchAccountCmd() tea.Cmd {
	return m.withTimeout(m.fetchAccount)
}

// fetchAccount performs the identity and account calls with a supplied context.
func (m *Model) fetchAccount(ctx context.Context) tea.Msg {
	if m.Deps.Tokens != nil {
		if _, err := m.Deps.Tokens.Token(ctx); err != nil {
			return authedMsg{err: err}
		}
	}
	id, err := m.Deps.API.Identity(ctx)
	if err != nil {
		return authedMsg{err: err}
	}
	acc, err := m.Deps.API.Account(ctx)
	if err != nil {
		return authedMsg{id: id, err: err}
	}
	return authedMsg{id: id, acc: acc}
}

// finishLoginCmd persists freshly issued credentials, seeds the token source and
// loads the account, which moves the UI to the home screen.
func (m *Model) finishLoginCmd(creds credentials.Credentials, access string, expiresIn int64) tea.Cmd {
	return m.withTimeout(func(ctx context.Context) tea.Msg {
		if creds.ObtainedAt.IsZero() {
			creds.ObtainedAt = time.Now()
		}
		if m.Deps.Store == nil {
			return authedMsg{err: fmt.Errorf("no credential store configured")}
		}
		if m.Deps.Tokens == nil {
			return authedMsg{err: fmt.Errorf("no token source configured")}
		}
		if err := m.Deps.Store.Save(&creds); err != nil {
			return authedMsg{err: fmt.Errorf("store credentials: %w", err)}
		}
		m.Deps.Tokens.Seed(&creds, access, expiresIn)

		msg, ok := m.fetchAccount(ctx).(authedMsg)
		if !ok || msg.err != nil {
			return msg
		}
		// Cache the account snapshot so a cold start can greet the user.
		updated := creds
		updated.AccountID = msg.id.ID
		updated.AccountName = msg.id.Name
		updated.Nick = msg.id.Nick
		if err := m.Deps.Store.Save(&updated); err != nil {
			return authedMsg{id: msg.id, acc: msg.acc, err: fmt.Errorf("store credentials: %w", err)}
		}
		// Re-seed so the token source holds the record carrying the snapshot.
		m.Deps.Tokens.Seed(&updated, m.Deps.Tokens.Access(), expiresIn)
		return msg
	})
}

// openerCommand returns the platform command that hands a URL to the browser.
func openerCommand(url string) *exec.Cmd {
	switch runtime.GOOS {
	case "darwin":
		return exec.Command("open", url) //nolint:gosec // fixed binary, user-supplied verification URL
	case "windows":
		return exec.Command("rundll32", "url.dll,FileProtocolHandler", url) //nolint:gosec
	default:
		return exec.Command("xdg-open", url) //nolint:gosec
	}
}

// letterSpace spreads a code so it reads as a code, not a word.
func letterSpace(s string) string {
	return strings.Join(strings.Split(s, ""), " ")
}

// countdown formats a remaining duration as mm:ss.
func countdown(d time.Duration) string {
	if d < 0 {
		d = 0
	}
	total := int(d.Seconds())
	return fmt.Sprintf("%02d:%02d", total/60, total%60)
}

// resetContentPanes drops every cached feed and chat view, so a new identity
// never sees the previous one's data.
func (m *Model) resetContentPanes() {
	m.Feed = newFeedModel()
	m.Post = newPostModel()
	m.Chats = newChatsModel()
	m.Chat = newChatModel()
	m.Chat.setWidth(m.detailWidth())
	m.Compose = newComposeModel()
	m.Compose.setWidth(m.contentWidth())
	m.Compose.input.SetStyles(textareaStyles(m.Styles))
	m.Chat.input.SetStyles(textareaStyles(m.Styles))
	m.postStack = nil
}

// enterGuest switches to guest browsing: public timelines and post details
// stay available, everything that needs a session asks for a sign-in.
func (m *Model) enterGuest() tea.Cmd {
	m.guest = true
	m.resetContentPanes()
	m.showScreen(ScreenFeed)
	return m.loadFeedCmd(feedTimeline, 0)
}

// leaveGuest drops the guest session and returns to the device login pane.
func (m *Model) leaveGuest() tea.Cmd {
	m.guest = false
	m.Login = newLoginModel()
	m.Login.setWidth(cardWidth(DeviceCardWidth, m.Width))
	m.showScreen(ScreenLogin)
	return m.startDeviceFlowCmd()
}

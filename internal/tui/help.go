package tui

import (
	"strings"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
)

type keybindingItem struct {
	key  string
	desc string
}

type keybindingSection struct {
	title string
	items []keybindingItem
}

// helpSections returns the global and contextual keybindings for the active screen.
func (m *Model) helpSections() []keybindingSection {
	var sections []keybindingSection

	// 1. Navigation / Global
	sections = append(sections, keybindingSection{
		title: "Navigation & Global",
		items: []keybindingItem{
			{key: "1 - 6", desc: "Switch screen (Posts, Chat, Notifs, Profile, Mail, Drive)"},
			{key: "/, Ctrl+K", desc: "Open command palette"},
			{key: "Tab", desc: "Cycle section or feed tab"},
			{key: "?", desc: "Toggle this keybinding dialog"},
			{key: "q", desc: "Quit Termian"},
		},
	})

	// 2. Current Screen context
	var screenTitle string
	var screenItems []keybindingItem

	switch m.Screen {
	case ScreenFeed:
		screenTitle = "Posts & Feed"
		screenItems = []keybindingItem{
			{key: "j / k", desc: "Navigate posts down / up"},
			{key: "Enter / o", desc: "Open post thread & replies"},
			{key: "c", desc: "Compose new post"},
			{key: "r", desc: "Like / React to post"},
			{key: "b", desc: "Bookmark / Unbookmark post"},
			{key: "e", desc: "Edit own post"},
			{key: "d", desc: "Delete own post"},
		}
	case ScreenPost:
		screenTitle = "Post Thread"
		screenItems = []keybindingItem{
			{key: "j / k", desc: "Scroll post and replies"},
			{key: "Enter", desc: "Drill into selected reply"},
			{key: "n", desc: "Reply to post"},
			{key: "r", desc: "Like / React to post"},
			{key: "b", desc: "Bookmark post"},
			{key: "Esc", desc: "Back to feed / parent post"},
		}
	case ScreenChats, ScreenChat:
		screenTitle = "Chat"
		if m.Chat.typing {
			screenItems = []keybindingItem{
				{key: "Enter", desc: "Send chat message"},
				{key: "Alt+Enter", desc: "Insert newline"},
				{key: "Esc", desc: "Normal mode (stop typing)"},
			}
		} else if m.Chats.focused {
			screenItems = []keybindingItem{
				{key: "j / k", desc: "Navigate messages"},
				{key: "i", desc: "Focus input & write message"},
				{key: "Esc / h", desc: "Return to room list"},
			}
		} else {
			screenItems = []keybindingItem{
				{key: "j / k", desc: "Navigate room list"},
				{key: "Enter / l", desc: "Open selected room"},
			}
		}
	case ScreenNotifications:
		screenTitle = "Notifications"
		screenItems = []keybindingItem{
			{key: "j / k", desc: "Navigate notifications"},
			{key: "Enter", desc: "Open referenced post or room"},
			{key: "a", desc: "Mark all notifications read"},
			{key: "d", desc: "Dismiss notification"},
			{key: "R", desc: "Reload notifications"},
		}
	case ScreenHome:
		screenTitle = "Profile & Settings"
		screenItems = []keybindingItem{
			{key: "j / k", desc: "Navigate settings items"},
			{key: "Enter", desc: "Toggle / edit setting"},
			{key: "r", desc: "Refresh profile & token info"},
			{key: "n", desc: "Compose new post"},
			{key: "l", desc: "Log out of session"},
		}
	case ScreenCompose:
		screenTitle = "Composer"
		screenItems = []keybindingItem{
			{key: "Ctrl+D", desc: "Publish post"},
			{key: "Ctrl+O", desc: "Toggle markdown editor mode"},
			{key: "Ctrl+A", desc: "Attach file"},
			{key: "Ctrl+P", desc: "Select publisher identity"},
			{key: "Esc", desc: "Cancel and discard draft"},
		}
	case ScreenDrive:
		screenTitle = "Drive & Cloud Storage"
		screenItems = []keybindingItem{
			{key: "j / k", desc: "Navigate files and folders"},
			{key: "Enter", desc: "Open folder / view file"},
			{key: "Backspace", desc: "Go up to parent folder"},
			{key: "n", desc: "Create new folder"},
			{key: "x", desc: "Delete / recycle item"},
			{key: "r", desc: "Refresh directory & quota"},
		}
	case ScreenMail:
		screenTitle = "Mail (ElecPostal)"
		screenItems = []keybindingItem{
			{key: "j / k", desc: "Navigate email threads"},
			{key: "Enter", desc: "Read conversation thread"},
			{key: "c", desc: "Compose new email"},
			{key: "[ / ]", desc: "Switch folder (Inbox, Sent, Starred, Trash)"},
			{key: "s", desc: "Toggle star"},
			{key: "x", desc: "Move conversation to trash"},
			{key: "r", desc: "Refresh messages"},
		}
	case ScreenMailThread:
		screenTitle = "Conversation View"
		screenItems = []keybindingItem{
			{key: "j / k", desc: "Navigate messages"},
			{key: "r", desc: "Reply to sender"},
			{key: "x", desc: "Move to trash"},
			{key: "Esc", desc: "Back to mail list"},
		}
	case ScreenMailCompose:
		screenTitle = "Mail Composer"
		screenItems = []keybindingItem{
			{key: "Tab", desc: "Next input field"},
			{key: "Shift+Tab", desc: "Previous input field"},
			{key: "Ctrl+S", desc: "Send email"},
			{key: "Esc", desc: "Cancel draft"},
		}
	}

	if screenTitle != "" && len(screenItems) > 0 {
		sections = append(sections, keybindingSection{
			title: screenTitle,
			items: screenItems,
		})
	}

	return sections
}

// updateHelpKey handles input events when the keybinding dialog is displayed.
func (m *Model) updateHelpKey(key tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	switch key.String() {
	case "esc", "?", "q", "enter":
		m.helpOpen = false
		return m, nil
	case "1":
		m.helpOpen = false
		m.showScreen(ScreenFeed)
		if !m.Feed.loaded {
			return m, m.loadFeedCmd(m.Feed.mode, 0)
		}
		return m, nil
	case "2":
		m.helpOpen = false
		m.showScreen(ScreenChats)
		m.unreadChat = 0
		if !m.Chats.loaded {
			return m, m.loadChatsCmd()
		}
		return m, nil
	case "3":
		m.helpOpen = false
		m.showScreen(ScreenNotifications)
		if !m.Notifications.loaded {
			m.Notifications.loaded = false
			return m, m.loadNotificationsCmd()
		}
		return m, nil
	case "4":
		m.helpOpen = false
		m.showScreen(ScreenHome)
		if m.Deps.Tokens != nil && !m.Home.loaded {
			return m, m.fetchAccountCmd()
		}
		return m, nil
	}
	return m, nil
}

// helpView renders the keybinding popup card.
func (m *Model) helpView() string {
	width := min(m.contentWidth(), 64)
	if width < 38 {
		width = 38
	}
	innerWidth := width - 6

	sections := m.helpSections()
	var lines []string

	title := m.Styles.Bold.Render("Keyboard Shortcuts")
	sub := m.Styles.Hint.Render("Quick reference for Termian keybindings")
	lines = append(lines, title, sub, m.Styles.Muted.Render(strings.Repeat("─", innerWidth)))

	for i, sec := range sections {
		if i > 0 {
			lines = append(lines, "")
		}
		lines = append(lines, m.Styles.Accent.Bold(true).Render(sec.title))
		for _, it := range sec.items {
			keyPill := m.Styles.UserCode.Render(padRight(it.key, 12))
			descText := m.Styles.Foreground.Render(it.desc)
			lines = append(lines, "  "+keyPill+" "+descText)
		}
	}

	lines = append(lines, m.Styles.Muted.Render(strings.Repeat("─", innerWidth)))
	lines = append(lines, m.Styles.Hint.Render("esc / ? / enter to close"))

	card := m.Styles.Card.Width(width).Render(lipgloss.JoinVertical(lipgloss.Left, lines...))
	return card
}

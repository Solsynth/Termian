package tui

import (
	"fmt"
	"strings"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
)

type paletteCategory string

const (
	categoryNav    paletteCategory = "Navigation"
	categoryAction paletteCategory = "Actions"
	categoryChat   paletteCategory = "Rooms"
)

type quickItem struct {
	id          string
	title       string
	description string
	category    paletteCategory
	shortcut    string
	icon        string
	action      func(m *Model) (tea.Model, tea.Cmd)
}

func (m *Model) quickItems() []quickItem {
	var items []quickItem

	// 1. Navigation items
	items = append(items, quickItem{
		id:          "nav-feed",
		title:       "Posts & Feeds",
		description: "Browse timelines, trending posts and bookmarks",
		category:    categoryNav,
		shortcut:    "1",
		icon:        "✦",
		action: func(m *Model) (tea.Model, tea.Cmd) {
			m.showScreen(ScreenFeed)
			if !m.Feed.loaded {
				return m, m.loadFeedCmd(m.Feed.mode, 0)
			}
			return m, nil
		},
	})
	items = append(items, quickItem{
		id:          "nav-chat",
		title:       "Chat & Rooms",
		description: "Realtime messaging channels and discussions",
		category:    categoryNav,
		shortcut:    "2",
		icon:        "💬",
		action: func(m *Model) (tea.Model, tea.Cmd) {
			m.showScreen(ScreenChats)
			m.unreadChat = 0
			if !m.Chats.loaded {
				return m, m.loadChatsCmd()
			}
			return m, nil
		},
	})
	items = append(items, quickItem{
		id:          "nav-notifications",
		title:       "Notifications",
		description: "View activity, mentions, and updates",
		category:    categoryNav,
		shortcut:    "3",
		icon:        "🔔",
		action: func(m *Model) (tea.Model, tea.Cmd) {
			m.showScreen(ScreenNotifications)
			if !m.Notifications.loaded {
				m.Notifications.loaded = false
				return m, m.loadNotificationsCmd()
			}
			return m, nil
		},
	})
	items = append(items, quickItem{
		id:          "nav-profile",
		title:       "Profile & Settings",
		description: "Manage identity, device, and preferences",
		category:    categoryNav,
		shortcut:    "4",
		icon:        "👤",
		action: func(m *Model) (tea.Model, tea.Cmd) {
			m.showScreen(ScreenHome)
			if m.Deps.Tokens != nil && !m.Home.loaded {
				return m, m.fetchAccountCmd()
			}
			return m, nil
		},
	})
	items = append(items, quickItem{
		id:          "nav-mail",
		title:       "Mail & Conversations",
		description: "Browse ElecPostal emails, threads, and inbox",
		category:    categoryNav,
		shortcut:    "5",
		icon:        "✉",
		action: func(m *Model) (tea.Model, tea.Cmd) {
			m.showScreen(ScreenMail)
			if !m.Mail.loaded {
				if len(m.Mail.mailboxes) == 0 {
					return m, m.loadMailboxesCmd()
				}
				return m, m.loadMailThreadsCmd(m.Mail.currentMailboxID(), m.Mail.folder)
			}
			return m, nil
		},
	})
	items = append(items, quickItem{
		id:          "nav-drive",
		title:       "Drive & Storage",
		description: "Browse files, folders and manage storage",
		category:    categoryNav,
		shortcut:    "6",
		icon:        "📁",
		action: func(m *Model) (tea.Model, tea.Cmd) {
			m.showScreen(ScreenDrive)
			if !m.Drive.loaded {
				return m, tea.Batch(
					m.loadDriveCmd(m.Drive.currentFolderID),
					m.loadDriveUsageCmd(),
				)
			}
			return m, nil
		},
	})

	// 2. Actions
	items = append(items, quickItem{
		id:          "act-compose",
		title:       "New Post",
		description: "Compose and publish a post or article",
		category:    categoryAction,
		shortcut:    "c",
		icon:        "✎",
		action: func(m *Model) (tea.Model, tea.Cmd) {
			if err := m.sessionGuard("sign in to post"); err != nil {
				return m, nil
			}
			return m, m.openCompose(nil)
		},
	})
	items = append(items, quickItem{
		id:          "act-refresh",
		title:       "Refresh Timeline",
		description: "Fetch latest posts from the server",
		category:    categoryAction,
		shortcut:    "r",
		icon:        "⟳",
		action: func(m *Model) (tea.Model, tea.Cmd) {
			m.showScreen(ScreenFeed)
			return m, m.loadFeedCmd(m.Feed.mode, 0)
		},
	})
	items = append(items, quickItem{
		id:          "act-shortcuts",
		title:       "Keyboard Shortcuts",
		description: "Display the keybindings reference popup",
		category:    categoryAction,
		shortcut:    "?",
		icon:        "?",
		action: func(m *Model) (tea.Model, tea.Cmd) {
			m.helpOpen = true
			return m, nil
		},
	})
	items = append(items, quickItem{
		id:          "act-mark-all-read",
		title:       "Mark Notifications Read",
		description: "Mark all unread notifications as viewed",
		category:    categoryAction,
		shortcut:    "a",
		icon:        "✓",
		action: func(m *Model) (tea.Model, tea.Cmd) {
			return m, m.markAllNotificationsReadCmd()
		},
	})
	items = append(items, quickItem{
		id:          "act-toggle-theme",
		title:       "Toggle Color Theme",
		description: "Switch between dark and light appearance",
		category:    categoryAction,
		shortcut:    "t",
		icon:        "◐",
		action: func(m *Model) (tea.Model, tea.Cmd) {
			m.Dark = !m.Dark
			m.Styles = newStyles(m.Dark)
			m.Password.applyStyles(m.Styles)
			m.Chat.input.SetStyles(textareaStyles(m.Styles))
			m.Compose.setStyles(m.Styles)
			return m, nil
		},
	})
	if !m.guest && m.Deps.Tokens != nil {
		items = append(items, quickItem{
			id:          "act-logout",
			title:       "Sign Out",
			description: "Revoke session and clear local credentials",
			category:    categoryAction,
			shortcut:    "l",
			icon:        "⏻",
			action: func(m *Model) (tea.Model, tea.Cmd) {
				return m, m.ask("Sign Out", "Are you sure you want to sign out?", func() tea.Cmd {
					return m.logoutCmd()
				})
			},
		})
	}

	// 3. Chat Rooms
	for _, room := range m.Chats.rooms {
		r := room
		title := m.roomTitle(r)
		items = append(items, quickItem{
			id:          "room-" + r.ID,
			title:       "# " + title,
			description: "Jump to chat room: " + title,
			category:    categoryChat,
			shortcut:    "↵",
			icon:        "#",
			action: func(m *Model) (tea.Model, tea.Cmd) {
				m.showScreen(ScreenChats)
				m.unreadChat = 0
				for i, rm := range m.Chats.rooms {
					if rm.ID == r.ID {
						m.Chats.selected = i
						return m, m.openRoom()
					}
				}
				return m, nil
			},
		})
	}

	if m.quickInput == "" {
		return items
	}

	query := strings.ToLower(strings.TrimSpace(m.quickInput))
	var filtered []quickItem
	for _, it := range items {
		matched := strings.Contains(strings.ToLower(it.title), query) ||
			strings.Contains(strings.ToLower(it.description), query) ||
			strings.Contains(strings.ToLower(string(it.category)), query) ||
			strings.EqualFold(it.shortcut, query)
		if matched {
			filtered = append(filtered, it)
		}
	}
	return filtered
}

func (m *Model) updateQuickSwitchKey(key tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	switch key.String() {
	case "esc":
		m.quickSwitch = false
		m.quickInput = ""
		return m, nil
	case "down", "ctrl+n":
		items := m.quickItems()
		if len(items) > 0 {
			m.quickSelected = (m.quickSelected + 1) % len(items)
		}
		return m, nil
	case "up", "ctrl+p":
		items := m.quickItems()
		if len(items) > 0 {
			m.quickSelected = (m.quickSelected - 1 + len(items)) % len(items)
		}
		return m, nil
	case "enter":
		items := m.quickItems()
		if len(items) > 0 {
			idx := max(0, min(m.quickSelected, len(items)-1))
			action := items[idx].action
			m.quickSwitch = false
			m.quickInput = ""
			m.quickSelected = 0
			return action(m)
		}
		m.quickSwitch = false
		return m, nil
	case "backspace":
		if len(m.quickInput) > 0 {
			m.quickInput = m.quickInput[:len(m.quickInput)-1]
			m.quickSelected = 0
		}
		return m, nil
	case "ctrl+u":
		m.quickInput = ""
		m.quickSelected = 0
		return m, nil
	}

	if len(key.Text) > 0 {
		m.quickInput += key.Text
		m.quickSelected = 0
		return m, nil
	}

	return m, nil
}

func (m *Model) quickSwitchView() string {
	width := min(m.contentWidth(), 64)
	if width < 38 {
		width = 38
	}
	innerWidth := width - 6 // inner width within card border & padding

	items := m.quickItems()
	var lines []string

	// Header line
	headerTitle := m.Styles.Bold.Render("Command Palette")
	countBadge := m.Styles.Hint.Render(fmt.Sprintf("%d commands", len(items)))
	if m.quickInput != "" {
		countBadge = m.Styles.Hint.Render(fmt.Sprintf("%d matches", len(items)))
	}
	headerGap := innerWidth - ansi.StringWidth(headerTitle) - ansi.StringWidth(countBadge)
	if headerGap < 1 {
		headerGap = 1
	}
	headerRow := headerTitle + strings.Repeat(" ", headerGap) + countBadge
	lines = append(lines, headerRow)

	// Input row
	prompt := m.Styles.Accent.Bold(true).Render("❯ ")
	cursor := m.Styles.Muted.Render("▌")
	inputLine := prompt + m.quickInput + cursor
	lines = append(lines, inputLine)
	lines = append(lines, m.Styles.Muted.Render(strings.Repeat("─", innerWidth)))

	// Item list
	if len(items) == 0 {
		lines = append(lines, m.Styles.Muted.Render("  No matching commands."))
	} else {
		maxItems := 7
		start := max(0, m.quickSelected-maxItems/2)
		end := min(len(items), start+maxItems)
		if end-start < maxItems && len(items) >= maxItems {
			start = max(0, end-maxItems)
		}

		for i := start; i < end; i++ {
			it := items[i]
			isSelected := (i == m.quickSelected)

			var label string
			if it.icon != "" && it.icon != "#" {
				label = it.icon + " " + it.title
			} else {
				label = it.title
			}

			shortcutBadge := ""
			if it.shortcut != "" {
				shortcutBadge = m.Styles.UserCode.Render(it.shortcut)
			}

			availTextWidth := innerWidth - 4 - ansi.StringWidth(shortcutBadge)
			if availTextWidth < 10 {
				availTextWidth = 10
			}
			label = ansi.Truncate(label, availTextWidth, "…")
			gap := innerWidth - 2 - ansi.StringWidth(label) - ansi.StringWidth(shortcutBadge)
			if gap < 1 {
				gap = 1
			}

			if isSelected {
				indicator := m.Styles.Selected.Render("▸ ")
				titleStyle := m.Styles.Selected.Render(label)
				row := indicator + titleStyle + strings.Repeat(" ", gap) + shortcutBadge
				lines = append(lines, row)
			} else {
				indicator := "  "
				titleStyle := m.Styles.Foreground.Render(label)
				row := indicator + titleStyle + strings.Repeat(" ", gap) + m.Styles.Muted.Render(shortcutBadge)
				lines = append(lines, row)
			}
		}
	}

	// Bottom preview & help
	lines = append(lines, m.Styles.Muted.Render(strings.Repeat("─", innerWidth)))
	if len(items) > 0 && m.quickSelected >= 0 && m.quickSelected < len(items) {
		selectedItem := items[m.quickSelected]
		desc := selectedItem.description
		if desc != "" {
			descLine := m.Styles.Subtitle.Render(ansi.Truncate("✦ "+desc, innerWidth, "…"))
			lines = append(lines, descLine)
		}
	}
	lines = append(lines, m.Styles.Hint.Render("↑/↓ navigate · enter select · esc close"))

	card := m.Styles.Card.Width(width).Render(lipgloss.JoinVertical(lipgloss.Left, lines...))
	return card
}

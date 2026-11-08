package tui

import (
	"strings"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
)

type quickItem struct {
	label  string
	action func(m *Model) (tea.Model, tea.Cmd)
}

func (m *Model) quickItems() []quickItem {
	var items []quickItem
	items = append(items, quickItem{
		label: "[1] Posts (Timeline & Bookmarks)",
		action: func(m *Model) (tea.Model, tea.Cmd) {
			m.showScreen(ScreenFeed)
			if !m.Feed.loaded {
				return m, m.loadFeedCmd(m.Feed.mode, 0)
			}
			return m, nil
		},
	})
	items = append(items, quickItem{
		label: "[2] Chat (Realtime Discussions)",
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
		label: "[3] Notifications",
		action: func(m *Model) (tea.Model, tea.Cmd) {
			m.showScreen(ScreenNotifications)
			return m, nil
		},
	})
	items = append(items, quickItem{
		label: "[4] Profile & Session",
		action: func(m *Model) (tea.Model, tea.Cmd) {
			m.showScreen(ScreenHome)
			if m.Deps.Tokens != nil && !m.Home.loaded {
				return m, m.fetchAccountCmd()
			}
			return m, nil
		},
	})

	for _, room := range m.Chats.rooms {
		r := room
		title := m.roomTitle(r)
		items = append(items, quickItem{
			label: "# " + title,
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

	query := strings.ToLower(m.quickInput)
	var filtered []quickItem
	for _, it := range items {
		if strings.Contains(strings.ToLower(it.label), query) {
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
	case "down", "j", "ctrl+n":
		items := m.quickItems()
		if len(items) > 0 {
			m.quickSelected = (m.quickSelected + 1) % len(items)
		}
		return m, nil
	case "up", "k", "ctrl+p":
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
	}

	if len(key.Text) > 0 {
		m.quickInput += key.Text
		m.quickSelected = 0
		return m, nil
	}

	return m, nil
}

func (m *Model) quickSwitchView() string {
	width := min(m.contentWidth(), 56)
	if width < 30 {
		width = 30
	}

	items := m.quickItems()
	var lines []string

	title := m.Styles.Bold.Render("Quick Switcher (Ctrl+K or /)")
	inputLine := m.Styles.Accent.Render("> ") + m.quickInput + m.Styles.Muted.Render("▌")
	lines = append(lines, title, inputLine, strings.Repeat("─", width-4))

	if len(items) == 0 {
		lines = append(lines, m.Styles.Muted.Render("No matches found."))
	} else {
		maxItems := 6
		start := max(0, m.quickSelected-maxItems/2)
		end := min(len(items), start+maxItems)
		for i := start; i < end; i++ {
			it := items[i]
			if i == m.quickSelected {
				lines = append(lines, m.Styles.Selected.Render("▸ "+it.label))
			} else {
				lines = append(lines, "  "+m.Styles.Foreground.Render(it.label))
			}
		}
	}

	lines = append(lines, strings.Repeat("─", width-4))
	lines = append(lines, m.Styles.Hint.Render("↑/↓ move · enter select · esc close"))

	card := m.Styles.Card.Width(width).Render(lipgloss.JoinVertical(lipgloss.Left, lines...))
	return card
}

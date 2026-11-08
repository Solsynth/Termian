package tui

import "charm.land/bubbletea/v2"

// section is a top-level browsing area, switched with tab.
type section int

const (
	sectionTimeline section = iota
	sectionChats
	sectionBookmarks
)

// String names the section for the header.
func (s section) String() string {
	switch s {
	case sectionChats:
		return "chats"
	case sectionBookmarks:
		return "bookmarks"
	default:
		return "timeline"
	}
}

// currentSection reports which section the visible pane belongs to.
func (m *Model) currentSection() section {
	switch m.Screen {
	case ScreenChats, ScreenChat:
		return sectionChats
	case ScreenFeed, ScreenPost, ScreenCompose:
		if m.Feed.mode == feedBookmarks {
			return sectionBookmarks
		}
		return sectionTimeline
	}
	return sectionTimeline
}

// switchSection moves to a section, retaining state if already loaded.
func (m *Model) switchSection(target section) tea.Cmd {
	switch target {
	case sectionTimeline:
		if m.guest || m.Deps.Tokens != nil {
			if m.Feed.mode != feedTimeline {
				m.Feed.reset(feedTimeline)
				m.showScreen(ScreenFeed)
				return m.loadFeedCmd(feedTimeline, 0)
			}
			m.showScreen(ScreenFeed)
			if !m.Feed.loaded {
				return m.loadFeedCmd(feedTimeline, 0)
			}
			return nil
		}
		return nil
	case sectionBookmarks:
		if err := m.sessionGuard("bookmarks need a session — press g to sign in"); err != nil {
			return nil
		}
		if m.Feed.mode != feedBookmarks {
			m.Feed.reset(feedBookmarks)
			m.showScreen(ScreenFeed)
			return m.loadFeedCmd(feedBookmarks, 0)
		}
		m.showScreen(ScreenFeed)
		if !m.Feed.loaded {
			return m.loadFeedCmd(feedBookmarks, 0)
		}
		return nil
	case sectionChats:
		if err := m.sessionGuard("chats need a session — press g to sign in"); err != nil {
			return nil
		}
		m.showScreen(ScreenChats)
		if m.Chats.loaded {
			return nil
		}
		return m.loadChatsCmd()
	}
	return nil
}

// cycleSection advances the tab strip, wrapping around.
func (m *Model) cycleSection() tea.Cmd {
	next := map[section]section{
		sectionTimeline:  sectionChats,
		sectionChats:     sectionBookmarks,
		sectionBookmarks: sectionTimeline,
	}[m.currentSection()]
	return m.switchSection(next)
}

// sectionKey handles the tab key, which every content pane shares.
func (m *Model) sectionKey(key tea.KeyPressMsg) (tea.Model, tea.Cmd, bool) {
	switch key.String() {
	case "tab":
		return m, m.cycleSection(), true
	case "shift+tab":
		prev := map[section]section{
			sectionTimeline:  sectionBookmarks,
			sectionChats:     sectionTimeline,
			sectionBookmarks: sectionChats,
		}[m.currentSection()]
		return m, m.switchSection(prev), true
	}
	return m, nil, false
}

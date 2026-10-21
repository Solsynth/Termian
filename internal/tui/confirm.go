package tui

import (
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
)

// confirmModel is the modal that guards destructive actions.
type confirmModel struct {
	active bool
	title  string
	detail string
	// action runs when the user confirms.
	action func() tea.Cmd
}

// ask opens the confirmation modal.
func (m *Model) ask(title, detail string, action func() tea.Cmd) tea.Cmd {
	m.Confirm = confirmModel{active: true, title: title, detail: detail, action: action}
	m.clearBanner()
	return nil
}

// confirmView renders the modal card.
func (m *Model) confirmView() string {
	c := &m.Confirm
	width := min(m.contentWidth(), 60)
	lines := []string{
		m.Styles.Bold.Render(c.title),
		"",
	}
	lines = append(lines, wrapText(c.detail, width-6)...)
	lines = append(lines, "", m.Styles.Hint.Render("y confirm · n cancel"))
	return m.Styles.Card.Width(width).Render(lipgloss.JoinVertical(lipgloss.Left, lines...))
}

// updateConfirmKey handles the modal keys.
func (m *Model) updateConfirmKey(key tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	c := &m.Confirm
	switch key.String() {
	case "y", "enter":
		action := c.action
		m.Confirm = confirmModel{}
		if action == nil {
			return m, nil
		}
		return m, action()
	case "n", "esc", "q":
		m.Confirm = confirmModel{}
		return m, nil
	}
	return m, nil
}

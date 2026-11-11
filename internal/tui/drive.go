package tui

import (
	"context"
	"fmt"
	"strings"

	tea "charm.land/bubbletea/v2"

	"src.solsynth.dev/sosys/termian/internal/api"
)

type folderCrumb struct {
	id   string
	name string
}

type driveModel struct {
	items             []api.CloudFile
	selected          int
	currentFolderID   string
	currentFolderName string
	folderStack       []folderCrumb
	loaded            bool
	loading           bool
	err               error

	usage       api.DriveUsage
	usageLoaded bool

	creatingFolder bool
	newFolderName  string
}

func newDriveModel() driveModel {
	return driveModel{
		currentFolderName: "Drive",
	}
}

func (d *driveModel) selectedItem() *api.CloudFile {
	if d.selected < 0 || d.selected >= len(d.items) {
		return nil
	}
	return &d.items[d.selected]
}

// driveLoadedMsg carries files for the active folder.
type driveLoadedMsg struct {
	folderID string
	page     api.Page[api.CloudFile]
	err      error
}

// driveUsageLoadedMsg carries storage quota and usage.
type driveUsageLoadedMsg struct {
	usage api.DriveUsage
	err   error
}

// driveFolderCreatedMsg reports a new folder.
type driveFolderCreatedMsg struct {
	folder api.CloudFile
	err    error
}

// driveItemRecycledMsg reports a deleted/recycled item.
type driveItemRecycledMsg struct {
	id  string
	err error
}

func (m *Model) loadDriveCmd(folderID string) tea.Cmd {
	m.Drive.loading = true
	return m.withTimeout(func(ctx context.Context) tea.Msg {
		if m.Deps.Content == nil {
			return driveLoadedMsg{folderID: folderID, err: nil}
		}
		if err := m.requireSession(ctx); err != nil {
			return driveLoadedMsg{folderID: folderID, err: err}
		}
		page, err := m.Deps.Content.DriveList(ctx, folderID, 0, 100)
		return driveLoadedMsg{folderID: folderID, page: page, err: err}
	})
}

func (m *Model) loadDriveUsageCmd() tea.Cmd {
	return m.withTimeout(func(ctx context.Context) tea.Msg {
		if m.Deps.Content == nil {
			return driveUsageLoadedMsg{err: nil}
		}
		if err := m.requireSession(ctx); err != nil {
			return driveUsageLoadedMsg{err: err}
		}
		usage, err := m.Deps.Content.DriveUsage(ctx)
		return driveUsageLoadedMsg{usage: usage, err: err}
	})
}

func (m *Model) createDriveFolderCmd(name, parentID string) tea.Cmd {
	return m.withTimeout(func(ctx context.Context) tea.Msg {
		if m.Deps.Content == nil {
			return driveFolderCreatedMsg{err: nil}
		}
		if err := m.requireSession(ctx); err != nil {
			return driveFolderCreatedMsg{err: err}
		}
		folder, err := m.Deps.Content.DriveCreateFolder(ctx, name, parentID)
		return driveFolderCreatedMsg{folder: folder, err: err}
	})
}

func (m *Model) recycleDriveItemCmd(id string) tea.Cmd {
	return m.withTimeout(func(ctx context.Context) tea.Msg {
		if m.Deps.Content == nil {
			return driveItemRecycledMsg{id: id, err: nil}
		}
		if err := m.requireSession(ctx); err != nil {
			return driveItemRecycledMsg{id: id, err: err}
		}
		err := m.Deps.Content.DriveRecycle(ctx, id)
		return driveItemRecycledMsg{id: id, err: err}
	})
}

func (m *Model) updateDriveMsg(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case driveLoadedMsg:
		m.Drive.loading = false
		m.Drive.loaded = true
		if msg.err != nil {
			m.Drive.err = msg.err
			m.setBanner("failed to load drive: " + msg.err.Error())
			return m, nil
		}
		m.Drive.err = nil
		m.Drive.items = msg.page.Items
		if m.Drive.selected >= len(m.Drive.items) {
			m.Drive.selected = 0
		}
		if !m.Drive.usageLoaded {
			return m, m.loadDriveUsageCmd()
		}
		return m, nil

	case driveUsageLoadedMsg:
		if msg.err == nil {
			m.Drive.usage = msg.usage
			m.Drive.usageLoaded = true
		}
		return m, nil

	case driveFolderCreatedMsg:
		if msg.err != nil {
			m.setBanner("could not create folder: " + msg.err.Error())
			return m, nil
		}
		m.setNotice("created folder " + msg.folder.Name)
		return m, m.loadDriveCmd(m.Drive.currentFolderID)

	case driveItemRecycledMsg:
		if msg.err != nil {
			m.setBanner("could not delete item: " + msg.err.Error())
			return m, nil
		}
		m.setNotice("moved item to recycle bin")
		return m, m.loadDriveCmd(m.Drive.currentFolderID)
	}
	return m, nil
}

func (m *Model) updateDriveKey(key tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	if m.Drive.creatingFolder {
		switch key.String() {
		case "esc":
			m.Drive.creatingFolder = false
			m.Drive.newFolderName = ""
			return m, nil
		case "enter":
			name := strings.TrimSpace(m.Drive.newFolderName)
			m.Drive.creatingFolder = false
			m.Drive.newFolderName = ""
			if name == "" {
				return m, nil
			}
			return m, m.createDriveFolderCmd(name, m.Drive.currentFolderID)
		case "backspace":
			if len(m.Drive.newFolderName) > 0 {
				m.Drive.newFolderName = m.Drive.newFolderName[:len(m.Drive.newFolderName)-1]
			}
			return m, nil
		default:
			if len(key.Text) > 0 {
				m.Drive.newFolderName += key.Text
			}
			return m, nil
		}
	}

	switch key.String() {
	case "j", "down":
		if len(m.Drive.items) > 0 && m.Drive.selected < len(m.Drive.items)-1 {
			m.Drive.selected++
		}
		return m, nil

	case "k", "up":
		if m.Drive.selected > 0 {
			m.Drive.selected--
		}
		return m, nil

	case "enter", "l":
		item := m.Drive.selectedItem()
		if item == nil {
			return m, nil
		}
		if item.IsFolder {
			// Drill down into folder
			m.Drive.folderStack = append(m.Drive.folderStack, folderCrumb{
				id:   m.Drive.currentFolderID,
				name: m.Drive.currentFolderName,
			})
			m.Drive.currentFolderID = item.ID
			m.Drive.currentFolderName = item.Name
			m.Drive.selected = 0
			return m, m.loadDriveCmd(item.ID)
		}
		// Open file attachment
		return m, m.openAttachment(*item)

	case "esc", "backspace", "h", "u":
		// Go up to parent folder
		if len(m.Drive.folderStack) > 0 {
			lastIdx := len(m.Drive.folderStack) - 1
			parent := m.Drive.folderStack[lastIdx]
			m.Drive.folderStack = m.Drive.folderStack[:lastIdx]
			m.Drive.currentFolderID = parent.id
			m.Drive.currentFolderName = parent.name
			m.Drive.selected = 0
			return m, m.loadDriveCmd(parent.id)
		}
		return m, nil

	case "n", "m":
		m.Drive.creatingFolder = true
		m.Drive.newFolderName = ""
		return m, nil

	case "d", "x":
		item := m.Drive.selectedItem()
		if item == nil {
			return m, nil
		}
		targetID := item.ID
		targetName := item.Name
		m.ask("Delete "+targetName+"?", "This will move the item to the recycle bin.", func() tea.Cmd {
			return m.recycleDriveItemCmd(targetID)
		})
		return m, nil

	case "r", "R":
		return m, tea.Batch(
			m.loadDriveCmd(m.Drive.currentFolderID),
			m.loadDriveUsageCmd(),
		)
	}

	return m, nil
}

func (m *Model) driveView() string {
	innerWidth := m.Width - 4
	if innerWidth < 1 {
		innerWidth = 1
	}

	// 1. Breadcrumbs
	crumbParts := []string{"📁 Drive"}
	for _, c := range m.Drive.folderStack {
		if c.name != "" && c.name != "Drive" {
			crumbParts = append(crumbParts, c.name)
		}
	}
	if m.Drive.currentFolderID != "" {
		crumbParts = append(crumbParts, m.Drive.currentFolderName)
	}
	breadcrumb := m.Styles.Title.Render(strings.Join(crumbParts, " / "))

	// 2. Storage summary
	var usageStr string
	if m.Drive.usageLoaded {
		pct := 0.0
		if m.Drive.usage.TotalQuota > 0 {
			pct = float64(m.Drive.usage.UsedQuota) / float64(m.Drive.usage.TotalQuota) * 100
		}
		usageStr = m.Styles.Muted.Render(fmt.Sprintf(
			"Storage: %s / %s (%.1f%%) • %d items",
			formatBytes(m.Drive.usage.UsedQuota),
			formatBytes(m.Drive.usage.TotalQuota),
			pct,
			len(m.Drive.items),
		))
	} else {
		usageStr = m.Styles.Muted.Render(fmt.Sprintf("%d items", len(m.Drive.items)))
	}

	headerLine := breadcrumb + "  " + usageStr

	// 3. New folder inline prompt
	var promptLine string
	if m.Drive.creatingFolder {
		promptLine = m.Styles.Accent.Render("New folder name: ") +
			m.Styles.Bold.Render(m.Drive.newFolderName+"█") +
			m.Styles.Muted.Render(" (Enter to create, Esc to cancel)")
	}

	// 4. Items table
	var itemRows []string
	if m.Drive.loading && !m.Drive.loaded {
		itemRows = append(itemRows, m.Styles.Muted.Render("  "+m.Spinner.View()+" Loading files..."))
	} else if len(m.Drive.items) == 0 {
		itemRows = append(itemRows, m.Styles.Muted.Render("  (folder is empty — press 'n' to create a subfolder)"))
	} else {
		for i, item := range m.Drive.items {
			selected := i == m.Drive.selected

			icon := "📄"
			sizeText := formatBytes(item.FileSize())
			if item.IsFolder {
				icon = "📁"
				sizeText = "-"
			}

			dateStr := item.CreatedAt
			if tIdx := strings.Index(dateStr, "T"); tIdx != -1 {
				dateStr = dateStr[:tIdx]
			}

			name := item.Name
			maxNameLen := innerWidth - 35
			if maxNameLen > 10 && len(name) > maxNameLen {
				name = name[:maxNameLen-3] + "..."
			}

			rowText := fmt.Sprintf(" %s  %-*s  %10s   %s", icon, maxNameLen, name, sizeText, dateStr)

			if selected {
				itemRows = append(itemRows, m.Styles.Selected.Render(padRight(rowText, innerWidth)))
			} else {
				itemRows = append(itemRows, m.Styles.Foreground.Render(padRight(rowText, innerWidth)))
			}
		}
	}

	// 5. Help footer
	hints := m.Styles.Muted.Render("Enter: Open  Backspace: Parent  n: New Folder  x: Delete  r: Refresh")

	lines := []string{headerLine}
	if promptLine != "" {
		lines = append(lines, promptLine)
	}
	lines = append(lines, "")
	lines = append(lines, itemRows...)
	lines = append(lines, "", hints)

	return strings.Join(lines, "\n")
}

func formatBytes(b int64) string {
	if b <= 0 {
		return "0 B"
	}
	const unit = 1024
	if b < unit {
		return fmt.Sprintf("%d B", b)
	}
	div, exp := int64(unit), 0
	for n := b / unit; n >= unit; n /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %cB", float64(b)/float64(div), "KMGTPE"[exp])
}

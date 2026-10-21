package tui

import (
	"bytes"
	"context"
	"fmt"
	"image"
	_ "image/gif"  // register the GIF decoder
	_ "image/jpeg" // register the JPEG decoder
	_ "image/png"  // register the PNG decoder
	"io"
	"net/http"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"

	"src.solsynth.dev/sosys/termian/internal/api"
)

// Attachment preview limits. A preview is decoration: it must never cost more
// than a bounded download or more lines than the pane can spare.
const (
	// previewMaxBytes caps one attachment download.
	previewMaxBytes = 8 << 20
	// previewTimeout bounds one attachment download.
	previewTimeout = 20 * time.Second
	// previewCols is the preview width in cells.
	previewCols = 40
	// previewRows is the preview height in cells.
	previewRows = 10
	// previewCacheSize is how many rendered previews are kept.
	previewCacheSize = 24
)

// asciiRamp shades a preview when the terminal has no true color.
const asciiRamp = " .:-=+*#%@"

// previewLoadedMsg carries a rendered attachment preview.
type previewLoadedMsg struct {
	fileID string
	lines  []string
	err    error
}

// isImage reports whether a file can be previewed as an image.
func isImage(file api.CloudFile) bool {
	return strings.HasPrefix(file.MimeType, "image/")
}

// attachmentLine renders the one-line summary of an attachment.
func attachmentLine(file api.CloudFile, index int) string {
	label := file.Name
	if label == "" {
		label = file.ID
	}
	kind := file.MimeType
	if kind == "" {
		kind = "file"
	}
	size := ""
	if file.Size > 0 {
		size = " · " + humanBytes(file.Size)
	}
	prefix := ""
	if index > 0 {
		prefix = fmt.Sprintf("[%d] ", index)
	}
	return prefix + label + " (" + kind + size + ")"
}

// humanBytes renders a byte count compactly.
func humanBytes(n int64) string {
	switch {
	case n >= 1<<20:
		return fmt.Sprintf("%.1f MB", float64(n)/(1<<20))
	case n >= 1<<10:
		return fmt.Sprintf("%.0f KB", float64(n)/(1<<10))
	default:
		return fmt.Sprintf("%d B", n)
	}
}

// fileURL resolves the download URL of an attachment. The API redirects to the
// storage backend, so the URL is usable by a browser or by this client.
func (m *Model) fileURL(file api.CloudFile) string {
	if file.URL != "" {
		return file.URL
	}
	if client, ok := m.Deps.Content.(*api.Client); ok {
		return client.FileURL(file.ID)
	}
	return ""
}

// attachmentOpenMsg reports the outcome of handing a file to the browser.
type attachmentOpenMsg struct {
	err error
}

// openAttachment hands an attachment to the browser. The TUI keeps running:
// the opener is a separate process the user comes back from.
func (m *Model) openAttachment(file api.CloudFile) tea.Cmd {
	url := m.fileURL(file)
	if url == "" {
		m.setBanner("this attachment has no download URL")
		return nil
	}
	return func() tea.Msg {
		cmd := openerCommand(url)
		if err := cmd.Start(); err != nil {
			return attachmentOpenMsg{err: err}
		}
		// Reap the child so it does not linger as a zombie.
		go func() { _ = cmd.Wait() }()
		return attachmentOpenMsg{}
	}
}

// previewCmd downloads and renders an image attachment.
func (m *Model) previewCmd(file api.CloudFile) tea.Cmd {
	if cached, ok := m.previews[file.ID]; ok {
		return func() tea.Msg { return previewLoadedMsg{fileID: file.ID, lines: cached} }
	}
	if m.previews == nil {
		m.previews = map[string][]string{}
	}
	// Reserve the slot so a second render does not queue the same download.
	m.previews[file.ID] = nil
	url := m.fileURL(file)
	if url == "" {
		return func() tea.Msg {
			return previewLoadedMsg{fileID: file.ID, err: fmt.Errorf("no download URL")}
		}
	}
	truecolor := m.truecolor
	return m.withTimeout(func(ctx context.Context) tea.Msg {
		lines, err := renderPreview(ctx, url, truecolor)
		return previewLoadedMsg{fileID: file.ID, lines: lines, err: err}
	})
}

// renderPreview downloads an image and renders it as terminal art.
func renderPreview(ctx context.Context, url string, truecolor bool) ([]string, error) {
	ctx, cancel := context.WithTimeout(ctx, previewTimeout)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer func() { _ = res.Body.Close() }()
	if res.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("download failed: HTTP %d", res.StatusCode)
	}

	data, err := io.ReadAll(io.LimitReader(res.Body, previewMaxBytes+1))
	if err != nil {
		return nil, err
	}
	if len(data) > previewMaxBytes {
		return nil, fmt.Errorf("image is larger than %s", humanBytes(previewMaxBytes))
	}

	img, _, err := image.Decode(bytes.NewReader(data))
	if err != nil {
		return nil, fmt.Errorf("decode image: %w", err)
	}
	return renderImage(img, previewCols, previewRows, truecolor), nil
}

// renderImage draws img into cols×rows cells. With true color each cell is a
// half block carrying two pixel rows; otherwise luminance is mapped onto an
// ASCII ramp so the preview still reads on a 16-color terminal.
func renderImage(img image.Image, cols, rows int, truecolor bool) []string {
	if cols < 2 || rows < 1 {
		return nil
	}
	bounds := img.Bounds()
	width, height := bounds.Dx(), bounds.Dy()
	if width == 0 || height == 0 {
		return nil
	}

	// Keep the aspect ratio: a cell is about twice as tall as it is wide, so a
	// true-color preview samples two pixel rows per cell.
	sampleRows := rows
	if truecolor {
		sampleRows = rows * 2
	}
	scaleX := float64(width) / float64(cols)
	scaleY := float64(height) / float64(sampleRows)

	lines := make([]string, 0, rows)
	var sb strings.Builder
	for row := 0; row < rows; row++ {
		sb.Reset()
		for col := 0; col < cols; col++ {
			x := bounds.Min.X + int(float64(col)*scaleX)
			y := bounds.Min.Y + int(float64(row)*scaleY)
			if truecolor {
				top := y
				bottom := bounds.Min.Y + int(float64(row*2+1)*scaleY)
				tr, tg, tb := pixel(img, x, top)
				br, bg, bb := pixel(img, x, bottom)
				fmt.Fprintf(&sb, "\x1b[38;2;%d;%d;%dm\x1b[48;2;%d;%d;%dm▀", tr, tg, tb, br, bg, bb)
				continue
			}
			r, g, b := pixel(img, x, y)
			// Rec. 601 luma, then the ASCII ramp.
			luma := (299*int(r) + 587*int(g) + 114*int(b)) / 1000
			idx := luma * (len(asciiRamp) - 1) / 255
			sb.WriteByte(asciiRamp[idx])
		}
		if truecolor {
			sb.WriteString("\x1b[0m")
		}
		lines = append(lines, sb.String())
	}
	return lines
}

// pixel reads one pixel, clamped to the image bounds.
func pixel(img image.Image, x, y int) (uint8, uint8, uint8) {
	bounds := img.Bounds()
	x = max(bounds.Min.X, min(x, bounds.Max.X-1))
	y = max(bounds.Min.Y, min(y, bounds.Max.Y-1))
	r, g, b, _ := img.At(x, y).RGBA()
	return uint8(r >> 8), uint8(g >> 8), uint8(b >> 8)
}

// storePreview caches a rendered preview, evicting the oldest entry.
func (m *Model) storePreview(msg previewLoadedMsg) {
	if msg.err != nil || len(msg.lines) == 0 {
		delete(m.previews, msg.fileID)
		return
	}
	if len(m.previews) >= previewCacheSize {
		for id := range m.previews {
			delete(m.previews, id)
			break
		}
	}
	m.previews[msg.fileID] = msg.lines
}

// previewLines returns the cached preview of a file, or a placeholder.
func (m *Model) previewLines(file api.CloudFile) []string {
	if lines, ok := m.previews[file.ID]; ok && len(lines) > 0 {
		return lines
	}
	return []string{m.Styles.Muted.Render("loading preview…")}
}

// previewFocusedCmd requests the preview of the focused post's first image.
// It is a no-op outside preview mode and when the preview is cached.
func (m *Model) previewFocusedCmd() tea.Cmd {
	if m.Deps.Attachments != "preview" {
		return nil
	}
	target := m.Post.focused()
	if target == nil {
		return nil
	}
	files := previewableAttachments(target.Attachments, 1)
	if len(files) == 0 {
		return nil
	}
	if lines, ok := m.previews[files[0].ID]; ok && lines != nil {
		return nil
	}
	return m.previewCmd(files[0])
}

// previewableAttachments picks the images worth rendering in a pane.
func previewableAttachments(files []api.CloudFile, limit int) []api.CloudFile {
	out := make([]api.CloudFile, 0, limit)
	for _, file := range files {
		if !isImage(file) {
			continue
		}
		out = append(out, file)
		if len(out) == limit {
			break
		}
	}
	return out
}

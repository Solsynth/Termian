package cli

import (
	"context"
	"fmt"
	"mime"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/spf13/cobra"

	"src.solsynth.dev/sosys/termian/internal/ui"
)

func (a *app) driveCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "drive",
		Short: "Manage and browse cloud files on Drive",
	}

	cmd.AddCommand(
		a.driveListCommand(),
		a.driveUploadCommand(),
		a.driveDownloadCommand(),
		a.driveMkdirCommand(),
		a.driveRmCommand(),
		a.driveQuotaCommand(),
	)
	return cmd
}

func (a *app) driveListCommand() *cobra.Command {
	var offset, take int
	cmd := &cobra.Command{
		Use:     "ls [folder-id]",
		Aliases: []string{"list"},
		Short:   "List files and folders in Drive",
		Args:    cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			folderID := ""
			if len(args) > 0 {
				folderID = args[0]
			}
			return a.runDriveList(cmd.Context(), folderID, offset, take)
		},
	}
	cmd.Flags().IntVar(&offset, "offset", 0, "offset into results")
	cmd.Flags().IntVar(&take, "take", 50, "items per page")
	return cmd
}

func (a *app) runDriveList(ctx context.Context, folderID string, offset, take int) error {
	if err := a.prepare(); err != nil {
		return err
	}
	if err := a.withSession(ctx); err != nil {
		return err
	}
	page, err := a.client.DriveList(ctx, folderID, offset, take)
	if err != nil {
		return err
	}

	if a.out.JSON() {
		for _, file := range page.Items {
			if err := a.out.Emit(map[string]any{"type": ui.RecordDriveFile, "file": file}); err != nil {
				return err
			}
		}
		return nil
	}

	if len(page.Items) == 0 {
		fmt.Fprintln(a.stdout, "No files or folders found.")
		return nil
	}

	rows := make([][]string, 0, len(page.Items))
	for _, f := range page.Items {
		kind := "file"
		sizeStr := formatBytes(f.FileSize())
		if f.IsFolder {
			kind = "dir"
			sizeStr = "-"
		}
		dateStr := f.CreatedAt
		if tIndex := strings.Index(dateStr, "T"); tIndex != -1 {
			dateStr = dateStr[:tIndex]
		}
		rows = append(rows, []string{
			kind,
			f.Name,
			sizeStr,
			dateStr,
			f.ID,
		})
	}

	return a.out.Table(
		ui.RecordDriveFile,
		[]string{"TYPE", "NAME", "SIZE", "CREATED", "ID"},
		rows,
	)
}

func (a *app) driveUploadCommand() *cobra.Command {
	var folderID string
	var customName string
	cmd := &cobra.Command{
		Use:   "upload <file-path>",
		Short: "Upload a local file to Drive",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return a.runDriveUpload(cmd.Context(), args[0], folderID, customName)
		},
	}
	cmd.Flags().StringVarP(&folderID, "folder", "f", "", "target folder ID")
	cmd.Flags().StringVarP(&customName, "name", "n", "", "custom destination filename")
	return cmd
}

func (a *app) runDriveUpload(ctx context.Context, filePath, folderID, customName string) error {
	if err := a.prepare(); err != nil {
		return err
	}
	if err := a.withSession(ctx); err != nil {
		return err
	}

	data, err := os.ReadFile(filePath)
	if err != nil {
		return fmt.Errorf("read file %q: %w", filePath, err)
	}

	name := filepath.Base(filePath)
	if customName != "" {
		name = customName
	}

	contentType := mime.TypeByExtension(filepath.Ext(name))
	if contentType == "" {
		contentType = http.DetectContentType(data)
	}

	uploaded, err := a.client.UploadDriveFile(ctx, name, data, contentType, folderID)
	if err != nil {
		return err
	}

	if a.out.JSON() {
		return a.out.Emit(map[string]any{"type": ui.RecordDriveFile, "file": uploaded})
	}

	fmt.Fprintf(a.stdout, "Uploaded %s (%s, %s)\n", uploaded.Name, formatBytes(uploaded.FileSize()), uploaded.ID)
	return nil
}

func (a *app) driveDownloadCommand() *cobra.Command {
	var outPath string
	cmd := &cobra.Command{
		Use:   "download <file-id>",
		Short: "Download a file from Drive",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return a.runDriveDownload(cmd.Context(), args[0], outPath)
		},
	}
	cmd.Flags().StringVarP(&outPath, "out", "o", "", "output file path (or '-' for stdout)")
	return cmd
}

func (a *app) runDriveDownload(ctx context.Context, fileID, outPath string) error {
	if err := a.prepare(); err != nil {
		return err
	}
	if err := a.withSession(ctx); err != nil {
		return err
	}

	data, name, err := a.client.DriveDownload(ctx, fileID)
	if err != nil {
		return err
	}

	if outPath == "-" {
		_, err := a.stdout.Write(data)
		return err
	}

	target := outPath
	if target == "" {
		target = name
	}
	if target == "" {
		target = fileID
	}

	if err := os.WriteFile(target, data, 0o644); err != nil {
		return fmt.Errorf("write %q: %w", target, err)
	}

	if a.out.JSON() {
		return a.out.Emit(map[string]any{
			"type":      "drive_download",
			"file_id":   fileID,
			"path":      target,
			"size":      len(data),
			"file_name": name,
		})
	}

	fmt.Fprintf(a.stdout, "Saved %s (%s) to %s\n", name, formatBytes(int64(len(data))), target)
	return nil
}

func (a *app) driveMkdirCommand() *cobra.Command {
	var folderID string
	cmd := &cobra.Command{
		Use:   "mkdir <name>",
		Short: "Create a new folder in Drive",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return a.runDriveMkdir(cmd.Context(), args[0], folderID)
		},
	}
	cmd.Flags().StringVarP(&folderID, "folder", "f", "", "parent folder ID")
	return cmd
}

func (a *app) runDriveMkdir(ctx context.Context, name, folderID string) error {
	if err := a.prepare(); err != nil {
		return err
	}
	if err := a.withSession(ctx); err != nil {
		return err
	}

	folder, err := a.client.DriveCreateFolder(ctx, name, folderID)
	if err != nil {
		return err
	}

	if a.out.JSON() {
		return a.out.Emit(map[string]any{"type": ui.RecordDriveFile, "file": folder})
	}

	fmt.Fprintf(a.stdout, "Created folder %q (%s)\n", folder.Name, folder.ID)
	return nil
}

func (a *app) driveRmCommand() *cobra.Command {
	var permanent bool
	cmd := &cobra.Command{
		Use:   "rm <file-id>",
		Short: "Recycle or permanently delete a file or folder",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return a.runDriveRm(cmd.Context(), args[0], permanent)
		},
	}
	cmd.Flags().BoolVarP(&permanent, "permanent", "p", false, "permanently delete instead of recycling")
	return cmd
}

func (a *app) runDriveRm(ctx context.Context, fileID string, permanent bool) error {
	if err := a.prepare(); err != nil {
		return err
	}
	if err := a.withSession(ctx); err != nil {
		return err
	}

	if permanent {
		if err := a.client.DriveDelete(ctx, fileID); err != nil {
			return err
		}
	} else {
		if err := a.client.DriveRecycle(ctx, fileID); err != nil {
			return err
		}
	}

	if a.out.JSON() {
		return a.out.Emit(map[string]any{
			"type":      "drive_deleted",
			"file_id":   fileID,
			"permanent": permanent,
		})
	}

	if permanent {
		fmt.Fprintf(a.stdout, "Permanently deleted %s\n", fileID)
	} else {
		fmt.Fprintf(a.stdout, "Moved %s to recycle bin\n", fileID)
	}
	return nil
}

func (a *app) driveQuotaCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "quota",
		Short: "Show Drive storage quota and usage",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return a.runDriveQuota(cmd.Context())
		},
	}
}

func (a *app) runDriveQuota(ctx context.Context) error {
	if err := a.prepare(); err != nil {
		return err
	}
	if err := a.withSession(ctx); err != nil {
		return err
	}

	usage, err := a.client.DriveUsage(ctx)
	if err != nil {
		return err
	}

	if a.out.JSON() {
		return a.out.Emit(map[string]any{"type": ui.RecordDriveQuota, "usage": usage})
	}

	pct := 0.0
	if usage.TotalQuota > 0 {
		pct = float64(usage.UsedQuota) / float64(usage.TotalQuota) * 100
	}
	fmt.Fprintf(a.stdout, "Storage: %s / %s (%.1f%%)\n",
		formatBytes(usage.UsedQuota), formatBytes(usage.TotalQuota), pct)
	fmt.Fprintf(a.stdout, "Files:   %s\n", strconv.FormatInt(usage.TotalFileCount, 10))
	return nil
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

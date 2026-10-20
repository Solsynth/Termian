package cli

import (
	"context"
	"time"

	"github.com/spf13/cobra"

	"src.solsynth.dev/sosys/termian/internal/api"
	"src.solsynth.dev/sosys/termian/internal/ui"
)

// refreshCommand rotates the refresh token and reports the new lifetime.
func (a *app) refreshCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "refresh",
		Short: "Rotate the access token",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return a.runRefresh(cmd.Context())
		},
	}
}

// refreshedRecord is the "refreshed" NDJSON record.
type refreshedRecord struct {
	Type             string         `json:"type"`
	AccessExpiresAt  time.Time      `json:"access_expires_at"`
	RefreshExpiresAt *time.Time     `json:"refresh_expires_at,omitempty"`
	Server           string         `json:"server"`
	Storage          storagePayload `json:"storage"`
}

// runRefresh forces one rotation.
func (a *app) runRefresh(ctx context.Context) error {
	if err := a.prepare(); err != nil {
		return err
	}
	if a.creds == nil {
		return api.ErrNoCredentials
	}

	if err := a.tokens.ForceRefresh(ctx); err != nil {
		return err
	}

	creds := a.tokens.Credentials()
	if creds == nil {
		return api.ErrSessionExpired
	}

	rec := refreshedRecord{
		Type:            ui.RecordRefreshed,
		AccessExpiresAt: a.tokens.AccessExpiry(),
		Server:          a.cfg.Server,
		Storage:         a.storage(),
	}
	if !creds.RefreshExpiresAt.IsZero() {
		at := creds.RefreshExpiresAt
		rec.RefreshExpiresAt = &at
	}

	if a.out.JSON() {
		return a.out.Emit(rec)
	}
	a.out.Textf("access token refreshed (valid until %s)", rec.AccessExpiresAt.Format(time.RFC3339))
	if rec.RefreshExpiresAt != nil {
		a.out.Textf("refresh token valid until %s", rec.RefreshExpiresAt.Format(time.RFC3339))
	}
	return nil
}

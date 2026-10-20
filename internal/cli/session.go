package cli

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/spf13/cobra"

	"src.solsynth.dev/sosys/termian/internal/api"
	"src.solsynth.dev/sosys/termian/internal/ui"
)

// whoamiCommand prints the signed-in account.
func (a *app) whoamiCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "whoami",
		Short: "Show the signed-in account and session",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return a.runWhoami(cmd.Context())
		},
	}
}

// whoamiRecord is the "account" NDJSON record.
type whoamiRecord struct {
	Type    string         `json:"type"`
	Account accountPayload `json:"account"`
	Session sessionPayload `json:"session"`
	Server  string         `json:"server"`
	Storage storagePayload `json:"storage"`
}

// runWhoami loads the account behind the stored session.
func (a *app) runWhoami(ctx context.Context) error {
	if err := a.prepare(); err != nil {
		return err
	}
	if a.creds == nil {
		return api.ErrNoCredentials
	}

	id, acc, err := a.loadAccount(ctx)
	if err != nil {
		return err
	}

	rec := whoamiRecord{
		Type:    ui.RecordAccount,
		Account: accountFrom(id, acc),
		Session: sessionPayload{ID: id.SessionID, TokenType: id.TokenType},
		Server:  a.cfg.Server,
		Storage: a.storage(),
	}

	if a.out.JSON() {
		return a.out.Emit(rec)
	}

	rows := [][]string{
		{"id", rec.Account.ID},
		{"name", rec.Account.Name},
		{"nick", rec.Account.Nick},
		{"level", fmt.Sprintf("%d", rec.Account.Level)},
		{"experience", fmt.Sprintf("%d", rec.Account.Experience)},
	}
	if rec.Account.JoinedAt != "" {
		rows = append(rows, []string{"joined", rec.Account.JoinedAt})
	}
	rows = append(rows,
		[]string{"session", rec.Session.ID},
		[]string{"token type", rec.Session.TokenType},
		[]string{"server", rec.Server},
		[]string{"storage", rec.Storage.Backend},
	)
	return a.out.Table("account", []string{"field", "value"}, rows)
}

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

// logoutCommand revokes and deletes the stored session.
func (a *app) logoutCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "logout",
		Short: "Sign out and delete stored credentials",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return a.runLogout(cmd.Context())
		},
	}
}

// loggedOutRecord is the "logged_out" NDJSON record.
type loggedOutRecord struct {
	Type   string `json:"type"`
	Server string `json:"server"`
	// Revoked reports whether the server session was revoked; a network failure
	// leaves local credentials deleted anyway.
	Revoked bool `json:"revoked"`
}

// runLogout revokes the session (best effort) and deletes local credentials.
func (a *app) runLogout(ctx context.Context) error {
	if err := a.prepare(); err != nil {
		return err
	}

	hadCreds := a.creds != nil
	revokeErr := error(nil)
	if hadCreds {
		revokeErr = a.tokens.Logout(ctx)
	}

	rec := loggedOutRecord{
		Type:    ui.RecordLoggedOut,
		Server:  a.cfg.Server,
		Revoked: hadCreds && revokeErr == nil,
	}
	if a.out.JSON() {
		if err := a.out.Emit(rec); err != nil {
			return err
		}
	} else if hadCreds {
		fmt.Fprintln(a.stdout, "signed out")
	} else {
		fmt.Fprintln(a.stdout, "no stored credentials")
	}

	if revokeErr != nil && !errors.Is(revokeErr, api.ErrSessionExpired) {
		// Local credentials are gone; report the server-side problem via the
		// error record but keep the exit code successful only when nothing
		// was left to revoke.
		return revokeErr
	}
	return nil
}

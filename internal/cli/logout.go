package cli

import (
	"context"
	"errors"
	"fmt"

	"github.com/spf13/cobra"

	"src.solsynth.dev/sosys/termian/internal/api"
	"src.solsynth.dev/sosys/termian/internal/ui"
)

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

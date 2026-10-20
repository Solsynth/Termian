package cli

import (
	"context"
	"fmt"

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

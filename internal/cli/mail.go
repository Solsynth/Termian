package cli

import (
	"context"
	"fmt"
	"strings"

	"github.com/spf13/cobra"

	"src.solsynth.dev/sosys/termian/internal/api"
	"src.solsynth.dev/sosys/termian/internal/ui"
)

func (a *app) mailCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "mail",
		Short: "Manage and compose ElecPostal emails",
	}

	cmd.AddCommand(
		a.mailboxesCommand(),
		a.mailListCommand(),
		a.mailReadCommand(),
		a.mailSendCommand(),
		a.mailStarCommand(),
		a.mailDeleteCommand(),
	)
	return cmd
}

func (a *app) mailboxesCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "mailboxes",
		Short: "List your ElecPostal mailboxes",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return a.runMailboxes(cmd.Context())
		},
	}
}

func (a *app) runMailboxes(ctx context.Context) error {
	if err := a.prepare(); err != nil {
		return err
	}
	if err := a.withSession(ctx); err != nil {
		return err
	}

	boxes, err := a.client.Mailboxes(ctx)
	if err != nil {
		return err
	}

	if a.out.JSON() {
		for _, mb := range boxes {
			if err := a.out.Emit(map[string]any{"type": ui.RecordMailbox, "mailbox": mb}); err != nil {
				return err
			}
		}
		return nil
	}

	if len(boxes) == 0 {
		fmt.Fprintln(a.stdout, "No mailboxes configured.")
		return nil
	}

	rows := make([][]string, 0, len(boxes))
	for _, mb := range boxes {
		def := ""
		if mb.IsDefault {
			def = "*"
		}
		rows = append(rows, []string{
			def,
			mb.Address,
			mb.DisplayName(),
			mb.ID,
		})
	}

	return a.out.Table(
		ui.RecordMailbox,
		[]string{"DEF", "ADDRESS", "NAME", "ID"},
		rows,
	)
}

func (a *app) mailListCommand() *cobra.Command {
	var mailboxID, folder string
	var offset, take int
	cmd := &cobra.Command{
		Use:     "list",
		Aliases: []string{"ls"},
		Short:   "List email conversation threads",
		Args:    cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return a.runMailList(cmd.Context(), mailboxID, folder, offset, take)
		},
	}
	cmd.Flags().StringVarP(&mailboxID, "mailbox", "m", "", "mailbox ID")
	cmd.Flags().StringVarP(&folder, "folder", "f", "inbox", "folder name: inbox, sent, starred, trash, spam")
	cmd.Flags().IntVar(&offset, "offset", 0, "offset into results")
	cmd.Flags().IntVar(&take, "take", 20, "items per page")
	return cmd
}

func (a *app) runMailList(ctx context.Context, mailboxID, folder string, offset, take int) error {
	if err := a.prepare(); err != nil {
		return err
	}
	if err := a.withSession(ctx); err != nil {
		return err
	}

	threads, err := a.client.MailThreads(ctx, mailboxID, folder, offset, take)
	if err != nil {
		return err
	}

	if a.out.JSON() {
		for _, th := range threads.Items {
			if err := a.out.Emit(map[string]any{"type": ui.RecordMailThread, "thread": th}); err != nil {
				return err
			}
		}
		return nil
	}

	if len(threads.Items) == 0 {
		fmt.Fprintf(a.stdout, "No threads found in %s.\n", folder)
		return nil
	}

	rows := make([][]string, 0, len(threads.Items))
	for _, th := range threads.Items {
		status := " "
		if th.UnreadCount > 0 {
			status = "*"
		}
		participants := strings.Join(th.Participants, ", ")
		if participants == "" && th.LatestMessage != nil {
			participants = th.LatestMessage.Sender()
		}
		if len(participants) > 30 {
			participants = participants[:27] + "..."
		}
		subj := th.DisplaySubject()
		if th.MessageCount > 1 {
			subj = fmt.Sprintf("%s (%d)", subj, th.MessageCount)
		}
		dateStr := ""
		if th.LatestAt != nil {
			dateStr = *th.LatestAt
			if tIdx := strings.Index(dateStr, "T"); tIdx != -1 {
				dateStr = dateStr[:tIdx]
			}
		}
		rows = append(rows, []string{
			status,
			participants,
			subj,
			dateStr,
			th.ID,
		})
	}

	return a.out.Table(
		ui.RecordMailThread,
		[]string{"NEW", "FROM", "SUBJECT", "DATE", "ID"},
		rows,
	)
}

func (a *app) mailReadCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "read <thread-id-or-email-id>",
		Short: "Read an email or thread conversation",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return a.runMailRead(cmd.Context(), args[0])
		},
	}
}

func (a *app) runMailRead(ctx context.Context, id string) error {
	if err := a.prepare(); err != nil {
		return err
	}
	if err := a.withSession(ctx); err != nil {
		return err
	}

	// Try reading as thread first
	emails, err := a.client.MailThread(ctx, id)
	if err != nil || len(emails) == 0 {
		// Fall back to reading single email
		email, err2 := a.client.MailEmail(ctx, id)
		if err2 != nil {
			if err != nil {
				return err
			}
			return err2
		}
		emails = []api.MailEmail{email}
	}

	if a.out.JSON() {
		for _, em := range emails {
			if err := a.out.Emit(map[string]any{"type": ui.RecordMailEmail, "email": em}); err != nil {
				return err
			}
		}
		return nil
	}

	for i, em := range emails {
		if i > 0 {
			fmt.Fprintln(a.stdout, "\n------------------------------------------------------------")
		}
		fmt.Fprintf(a.stdout, "From:    %s\n", em.Sender())
		toAddresses := make([]string, 0, len(em.Recipients))
		for _, r := range em.Recipients {
			toAddresses = append(toAddresses, r.Address)
		}
		if len(toAddresses) > 0 {
			fmt.Fprintf(a.stdout, "To:      %s\n", strings.Join(toAddresses, ", "))
		}
		fmt.Fprintf(a.stdout, "Date:    %s\n", em.CreatedAt)
		fmt.Fprintf(a.stdout, "Subject: %s\n\n", em.Subject)
		fmt.Fprintln(a.stdout, em.Body)
	}

	return nil
}

func (a *app) mailSendCommand() *cobra.Command {
	var mailboxID, to, cc, bcc, subject, body string
	cmd := &cobra.Command{
		Use:   "send",
		Short: "Send an email message",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if to == "" {
				return &usageError{msg: "--to recipient address is required"}
			}
			if subject == "" {
				return &usageError{msg: "--subject is required"}
			}
			return a.runMailSend(cmd.Context(), mailboxID, to, cc, bcc, subject, body)
		},
	}
	cmd.Flags().StringVarP(&mailboxID, "mailbox", "m", "", "mailbox ID to send from")
	cmd.Flags().StringVar(&to, "to", "", "recipient address (comma-separated)")
	cmd.Flags().StringVar(&cc, "cc", "", "CC recipient address (comma-separated)")
	cmd.Flags().StringVar(&bcc, "bcc", "", "BCC recipient address (comma-separated)")
	cmd.Flags().StringVarP(&subject, "subject", "s", "", "subject line")
	cmd.Flags().StringVarP(&body, "body", "b", "", "email body content")
	return cmd
}

func (a *app) runMailSend(ctx context.Context, mailboxID, to, cc, bcc, subject, body string) error {
	if err := a.prepare(); err != nil {
		return err
	}
	if err := a.withSession(ctx); err != nil {
		return err
	}

	if mailboxID == "" {
		// Pick default mailbox
		boxes, err := a.client.Mailboxes(ctx)
		if err != nil {
			return err
		}
		for _, b := range boxes {
			if b.IsDefault {
				mailboxID = b.ID
				break
			}
		}
		if mailboxID == "" && len(boxes) > 0 {
			mailboxID = boxes[0].ID
		}
		if mailboxID == "" {
			return fmt.Errorf("no mailbox configured to send from")
		}
	}

	parseRecipients := func(raw, kind string) []api.MailRecipient {
		if strings.TrimSpace(raw) == "" {
			return nil
		}
		parts := strings.Split(raw, ",")
		res := make([]api.MailRecipient, 0, len(parts))
		for _, p := range parts {
			addr := strings.TrimSpace(p)
			if addr != "" {
				res = append(res, api.MailRecipient{Address: addr, Kind: kind})
			}
		}
		return res
	}

	input := api.SendMailInput{
		MailboxID:   mailboxID,
		To:          parseRecipients(to, "to"),
		Cc:          parseRecipients(cc, "cc"),
		Bcc:         parseRecipients(bcc, "bcc"),
		Subject:     subject,
		Body:        body,
		ContentType: "text/plain",
	}

	sent, err := a.client.SendMail(ctx, input)
	if err != nil {
		return err
	}

	if a.out.JSON() {
		return a.out.Emit(map[string]any{"type": ui.RecordMailSent, "email": sent})
	}

	fmt.Fprintf(a.stdout, "Sent email %q (%s)\n", sent.Subject, sent.ID)
	return nil
}

func (a *app) mailStarCommand() *cobra.Command {
	var remove bool
	cmd := &cobra.Command{
		Use:   "star <email-id>",
		Short: "Star or unstar an email",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return a.runMailStar(cmd.Context(), args[0], !remove)
		},
	}
	cmd.Flags().BoolVarP(&remove, "remove", "r", false, "unstar the email")
	return cmd
}

func (a *app) runMailStar(ctx context.Context, emailID string, star bool) error {
	if err := a.prepare(); err != nil {
		return err
	}
	if err := a.withSession(ctx); err != nil {
		return err
	}

	if err := a.client.StarMail(ctx, emailID, star); err != nil {
		return err
	}

	if a.out.JSON() {
		return a.out.Emit(map[string]any{
			"type":       "mail_starred",
			"email_id":   emailID,
			"is_starred": star,
		})
	}

	if star {
		fmt.Fprintf(a.stdout, "Starred %s\n", emailID)
	} else {
		fmt.Fprintf(a.stdout, "Unstarred %s\n", emailID)
	}
	return nil
}

func (a *app) mailDeleteCommand() *cobra.Command {
	var permanent bool
	cmd := &cobra.Command{
		Use:     "delete <email-id>",
		Aliases: []string{"rm"},
		Short:   "Delete an email (trash or permanent)",
		Args:    cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return a.runMailDelete(cmd.Context(), args[0], permanent)
		},
	}
	cmd.Flags().BoolVarP(&permanent, "permanent", "p", false, "permanently delete the email")
	return cmd
}

func (a *app) runMailDelete(ctx context.Context, emailID string, permanent bool) error {
	if err := a.prepare(); err != nil {
		return err
	}
	if err := a.withSession(ctx); err != nil {
		return err
	}

	if err := a.client.DeleteMail(ctx, emailID, permanent); err != nil {
		return err
	}

	if a.out.JSON() {
		return a.out.Emit(map[string]any{
			"type":      "mail_deleted",
			"email_id":  emailID,
			"permanent": permanent,
		})
	}

	if permanent {
		fmt.Fprintf(a.stdout, "Permanently deleted email %s\n", emailID)
	} else {
		fmt.Fprintf(a.stdout, "Moved email %s to trash\n", emailID)
	}
	return nil
}

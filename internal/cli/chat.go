package cli

import (
	"context"
	"fmt"
	"strings"

	"github.com/spf13/cobra"

	"src.solsynth.dev/sosys/termian/internal/api"
	"src.solsynth.dev/sosys/termian/internal/ui"
)

// chatRoomPayload is the room shape emitted by the chat commands.
type chatRoomPayload struct {
	ID          string `json:"id"`
	Title       string `json:"title"`
	Kind        string `json:"kind"`
	Unread      int    `json:"unread"`
	IsCommunity bool   `json:"is_community,omitempty"`
	IsPublic    bool   `json:"is_public,omitempty"`
	LastMessage string `json:"last_message,omitempty"`
	LastSender  string `json:"last_sender,omitempty"`
}

// chatMessagePayload is the message shape emitted by the chat commands.
type chatMessagePayload struct {
	ID        string         `json:"id"`
	RoomID    string         `json:"room_id"`
	Sender    string         `json:"sender"`
	Account   string         `json:"account,omitempty"`
	Content   string         `json:"content"`
	CreatedAt string         `json:"created_at"`
	EditedAt  string         `json:"edited_at,omitempty"`
	Reactions map[string]int `json:"reactions,omitempty"`
}

// chatsCommand lists the chat rooms.
func (a *app) chatsCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "chats",
		Short: "List your chat rooms",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return a.runChats(cmd.Context())
		},
	}
}

// runChats prints the room list with unread counts.
func (a *app) runChats(ctx context.Context) error {
	if err := a.prepare(); err != nil {
		return err
	}
	if err := a.withSession(ctx); err != nil {
		return err
	}
	rooms, err := a.client.ChatRoomList(ctx, 0, 100)
	if err != nil {
		return err
	}
	summary, summaryErr := a.client.ChatSummaryByRoom(ctx)
	if summaryErr != nil && !a.out.JSON() {
		a.out.Progressf("unread counts unavailable: %v", summaryErr)
	}

	payloads := make([]chatRoomPayload, 0, len(rooms.Rooms))
	for _, room := range rooms.Rooms {
		payload := chatRoomPayload{
			ID:          room.ID,
			Title:       room.Title(),
			Kind:        roomKind(room),
			IsCommunity: room.IsCommunity,
			IsPublic:    room.IsPublic,
		}
		if state, ok := summary[room.ID]; ok {
			payload.Unread = state.UnreadCount
			if state.LastMessage != nil {
				payload.LastMessage = strings.ReplaceAll(state.LastMessage.Text(), "\n", " ")
				payload.LastSender = state.LastMessage.Sender.DisplayName()
			}
		}
		payloads = append(payloads, payload)
	}

	if a.out.JSON() {
		for _, room := range payloads {
			if err := a.out.Emit(map[string]any{"type": ui.RecordChatRoom, "room": room}); err != nil {
				return err
			}
		}
		return nil
	}
	rows := make([][]string, 0, len(payloads))
	for _, room := range payloads {
		last := room.LastMessage
		if last != "" && room.LastSender != "" {
			last = room.LastSender + ": " + last
		}
		rows = append(rows, []string{
			room.ID,
			oneLine(room.Title, 24),
			room.Kind,
			fmt.Sprintf("%d", room.Unread),
			oneLine(last, 50),
		})
	}
	return a.out.Table(ui.RecordChatRoom, []string{"id", "title", "kind", "unread", "last"}, rows)
}

// roomKind names the room type.
func roomKind(room api.ChatRoom) string {
	switch {
	case room.IsCommunity:
		return "community"
	case room.IsDirect():
		return "direct"
	default:
		return "group"
	}
}

// chatCommand reads and writes one room.
func (a *app) chatCommand() *cobra.Command {
	var (
		limit   int
		content string
		file    string
		send    bool
	)
	cmd := &cobra.Command{
		Use:   "chat <room-id>",
		Short: "Read a chat room, or send a message with --send",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if send {
				return a.runSend(cmd.Context(), args[0], content, file)
			}
			return a.runChatLog(cmd.Context(), args[0], limit)
		},
	}
	cmd.Flags().IntVar(&limit, "limit", 30, "number of messages to print")
	cmd.Flags().BoolVar(&send, "send", false, "send a message instead of reading the room")
	cmd.Flags().StringVar(&content, "content", "", "message text for --send")
	cmd.Flags().StringVar(&file, "file", "", "read the message from a file (- for stdin)")
	return cmd
}

// runChatLog prints the newest messages of a room, oldest first.
func (a *app) runChatLog(ctx context.Context, roomID string, limit int) error {
	if err := a.prepare(); err != nil {
		return err
	}
	if err := a.withSession(ctx); err != nil {
		return err
	}
	page, err := a.client.ChatMessages(ctx, roomID, 0, limit)
	if err != nil {
		return err
	}
	if a.out.JSON() {
		for i := len(page.Items) - 1; i >= 0; i-- {
			if err := a.out.Emit(map[string]any{
				"type":    ui.RecordChatMessage,
				"message": messageFrom(page.Items[i]),
			}); err != nil {
				return err
			}
		}
		return nil
	}
	if len(page.Items) == 0 {
		fmt.Fprintln(a.stdout, "no messages")
		return nil
	}
	rows := make([][]string, 0, len(page.Items))
	for i := len(page.Items) - 1; i >= 0; i-- {
		msg := messageFrom(page.Items[i])
		rows = append(rows, []string{
			msg.CreatedAt,
			oneLine(msg.Sender, 16),
			oneLine(msg.Content, 70),
		})
	}
	return a.out.Table(ui.RecordChatMessage, []string{"time", "sender", "message"}, rows)
}

// runSend posts one message.
func (a *app) runSend(ctx context.Context, roomID, content, file string) error {
	if err := a.prepare(); err != nil {
		return err
	}
	if err := a.withSession(ctx); err != nil {
		return err
	}
	text, err := a.composeText(content, file)
	if err != nil {
		return err
	}
	msg, err := a.client.SendChatMessage(ctx, roomID, api.NewMessage{
		Content:       text,
		ClientMessage: newClientMessageID(),
	})
	if err != nil {
		return err
	}
	payload := messageFrom(msg)
	if a.out.JSON() {
		return a.out.Emit(map[string]any{"type": ui.RecordChatSent, "message": payload})
	}
	fmt.Fprintf(a.stdout, "sent %s\n", payload.ID)
	return nil
}

// messageFrom converts a wire message into its record shape.
func messageFrom(msg api.ChatMessage) chatMessagePayload {
	out := chatMessagePayload{
		ID:        msg.ID,
		RoomID:    msg.RoomID,
		Sender:    msg.Sender.DisplayName(),
		Content:   msg.Text(),
		CreatedAt: msg.CreatedAt.Local().Format("2006-01-02 15:04:05"),
		Reactions: msg.Reactions,
	}
	if msg.Sender.Account != nil {
		out.Account = msg.Sender.Account.Name
	}
	if msg.EditedAt != nil {
		out.EditedAt = msg.EditedAt.Local().Format("2006-01-02 15:04:05")
	}
	if out.Content == "" && len(msg.Attachments) > 0 {
		out.Content = fmt.Sprintf("(%d attachment(s))", len(msg.Attachments))
	}
	return out
}

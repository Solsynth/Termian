package api

import (
	"context"
	"net/http"
	"time"
)

// ChatRoom is one messager room: a direct message, a group or a community.
type ChatRoom struct {
	ID          string     `json:"id"`
	Name        *string    `json:"name"`
	Description *string    `json:"description"`
	Slug        *string    `json:"slug"`
	Type        int        `json:"type"`
	IsPublic    bool       `json:"is_public"`
	IsCommunity bool       `json:"is_community"`
	RealmID     *string    `json:"realm_id"`
	AccountID   *string    `json:"account_id"`
	Picture     *CloudFile `json:"picture"`
	CreatedAt   *time.Time `json:"created_at"`
	UpdatedAt   *time.Time `json:"updated_at"`
	// DirectMembers is the other participants of a direct room, already
	// filtered to exclude the signed-in account by the server.
	DirectMembers []ChatMember `json:"direct_members"`
}

// ChatRoomType codes sent by the server.
const (
	// ChatRoomGroup is a named group room.
	ChatRoomGroup = 0
	// ChatRoomDirect is a one-to-one room.
	ChatRoomDirect = 1
)

// IsDirect reports whether the room is a one-to-one conversation.
func (r ChatRoom) IsDirect() bool { return r.Type == ChatRoomDirect }

// Title is the room name, falling back to a direct-chat label.
func (r ChatRoom) Title() string {
	if r.Name != nil && *r.Name != "" {
		return *r.Name
	}
	if r.IsDirect() {
		return "direct message"
	}
	return "unnamed room"
}

// ChatRooms is the room list payload: rooms plus the account's grouping.
type ChatRooms struct {
	Rooms  []ChatRoom  `json:"rooms"`
	Groups []ChatGroup `json:"groups"`
}

// ChatGroup is a sidebar grouping of rooms.
type ChatGroup struct {
	ID      string   `json:"id"`
	Name    string   `json:"name"`
	Color   string   `json:"color"`
	Icon    string   `json:"icon"`
	Order   int      `json:"order"`
	RoomIDs []string `json:"room_ids"`
}

// ChatMember is the sender of a message.
type ChatMember struct {
	ID        string    `json:"id"`
	RoomID    string    `json:"chat_room_id"`
	AccountID string    `json:"account_id"`
	Nick      *string   `json:"nick"`
	Account   *ChatUser `json:"account"`
	JoinedAt  time.Time `json:"joined_at"`
}

// ChatUser is the account embedded in a member.
type ChatUser struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	Nick string `json:"nick"`
}

// DisplayName is the member nickname, then the account nickname and name.
func (m ChatMember) DisplayName() string {
	if m.Nick != nil && *m.Nick != "" {
		return *m.Nick
	}
	if m.Account != nil {
		if m.Account.Nick != "" {
			return m.Account.Nick
		}
		if m.Account.Name != "" {
			return m.Account.Name
		}
	}
	return "unknown"
}

// ChatMessage is one message in a room.
type ChatMessage struct {
	ID            string          `json:"id"`
	RoomID        string          `json:"chat_room_id"`
	SenderID      string          `json:"sender_id"`
	Sender        ChatMember      `json:"sender"`
	Content       *string         `json:"content"`
	Type          string          `json:"type"`
	CreatedAt     time.Time       `json:"created_at"`
	EditedAt      *time.Time      `json:"edited_at"`
	Attachments   []CloudFile     `json:"attachments"`
	Reactions     map[string]int  `json:"reactions_count"`
	ReactionsMade map[string]bool `json:"reactions_made"`
	RepliedID     *string         `json:"replied_message_id"`
	ThreadID      *string         `json:"thread_id"`
	IsThreadRoot  bool            `json:"is_thread_root"`
	IsEncrypted   bool            `json:"is_encrypted"`
	Meta          map[string]any  `json:"meta"`
	DeletedAt     *time.Time      `json:"deleted_at"`
}

// Text is the message body, empty when the server sent none.
func (m ChatMessage) Text() string {
	if m.Content != nil {
		return *m.Content
	}
	return ""
}

// ChatSummary is the per-room unread state served by GET /messager/chat/summary.
type ChatSummary struct {
	UnreadCount int          `json:"unread_count"`
	HasUnread   bool         `json:"has_unread"`
	LastMessage *ChatMessage `json:"last_message"`
}

// ChatRoomList lists the signed-in account's rooms and groups.
func (c *Client) ChatRoomList(ctx context.Context, offset, take int) (ChatRooms, error) {
	var out ChatRooms
	err := c.doRoot(ctx, http.MethodGet, pagePath("/messager/chat", offset, take), nil, &out)
	return out, err
}

// ChatMembers lists the members of a room. The server caps a page at 100.
func (c *Client) ChatMembers(ctx context.Context, roomID string, offset, take int) (Page[ChatMember], error) {
	var items []ChatMember
	meta, err := c.doRootPage(ctx, http.MethodGet,
		pagePath("/messager/chat/"+roomID+"/members", offset, take), nil, &items)
	return Page[ChatMember]{Items: items, Total: totalFrom(meta.Header)}, err
}

// ChatRoom fetches one room.
func (c *Client) ChatRoom(ctx context.Context, id string) (ChatRoom, error) {
	var room ChatRoom
	err := c.doRoot(ctx, http.MethodGet, "/messager/chat/"+id, nil, &room)
	return room, err
}

// ChatMessages lists the newest messages of a room, newest first.
//
// The room log interleaves real messages with system events (call presence,
// attachment finalisation, reaction receipts). Those are dropped here so
// callers see a readable transcript; a delete event removes its target.
func (c *Client) ChatMessages(ctx context.Context, roomID string, offset, take int) (Page[ChatMessage], error) {
	// Events can dominate a window, so ask for more than the caller wants.
	fetch := take * 4
	if fetch < 50 {
		fetch = 50
	}
	if fetch > 200 {
		fetch = 200
	}
	var items []ChatMessage
	meta, err := c.doRootPage(ctx, http.MethodGet,
		pagePath("/messager/chat/"+roomID+"/messages", offset, fetch), nil, &items)
	if err != nil {
		return Page[ChatMessage]{Total: totalFrom(meta.Header)}, err
	}
	kept := make([]ChatMessage, 0, len(items))
	for _, msg := range items {
		if !msg.IsVisible() {
			continue
		}
		kept = append(kept, msg)
		if len(kept) == take {
			break
		}
	}
	return Page[ChatMessage]{Items: kept, Total: totalFrom(meta.Header), Fetched: len(items)}, nil
}

// IsVisible reports whether a message belongs in a readable transcript.
func (m ChatMessage) IsVisible() bool {
	switch m.Type {
	case "", "text":
		return true
	}
	return false
}

// ChatSummaryByRoom reads the unread state of every room.
func (c *Client) ChatSummaryByRoom(ctx context.Context) (map[string]ChatSummary, error) {
	var out map[string]ChatSummary
	err := c.doRoot(ctx, http.MethodGet, "/messager/chat/summary", nil, &out)
	return out, err
}

// NewMessage is the payload of a sent message.
type NewMessage struct {
	Content       string
	ClientMessage string
	ReplyTo       string
	ThreadID      string
	AttachmentIDs []string
	ForwardedID   string
}

// SendChatMessage posts a message and returns the stored row.
func (c *Client) SendChatMessage(ctx context.Context, roomID string, req NewMessage) (ChatMessage, error) {
	body := map[string]any{
		"content":           req.Content,
		"client_message_id": req.ClientMessage,
		"meta":              map[string]any{},
	}
	if req.ReplyTo != "" {
		body["replied_message_id"] = req.ReplyTo
	}
	if req.ThreadID != "" {
		body["thread_id"] = req.ThreadID
	}
	if req.ForwardedID != "" {
		body["forwarded_message_id"] = req.ForwardedID
	}
	if len(req.AttachmentIDs) > 0 {
		body["attachments_id"] = req.AttachmentIDs
	}
	var msg ChatMessage
	err := c.doRoot(ctx, http.MethodPost, "/messager/chat/"+roomID+"/messages", body, &msg)
	return msg, err
}

// EditChatMessage replaces the text of one of the account's messages.
func (c *Client) EditChatMessage(ctx context.Context, roomID, messageID, content string) (ChatMessage, error) {
	var msg ChatMessage
	err := c.doRoot(ctx, http.MethodPatch,
		"/messager/chat/"+roomID+"/messages/"+messageID,
		map[string]any{"content": content}, &msg)
	return msg, err
}

// DeleteChatMessage removes one of the account's messages.
func (c *Client) DeleteChatMessage(ctx context.Context, roomID, messageID string) error {
	return c.doRoot(ctx, http.MethodDelete, "/messager/chat/"+roomID+"/messages/"+messageID, nil, nil)
}

// ToggleChatReaction adds a reaction, or removes the account's reaction with
// the same symbol. It reports whether the reaction is now present.
func (c *Client) ToggleChatReaction(ctx context.Context, roomID, messageID, symbol string) (bool, error) {
	meta, err := c.doRootStatus(ctx, http.MethodPost,
		"/messager/chat/"+roomID+"/messages/"+messageID+"/reactions",
		map[string]any{"symbol": symbol, "attitude": 0})
	if err != nil {
		return false, err
	}
	return meta.Status != http.StatusNoContent, nil
}

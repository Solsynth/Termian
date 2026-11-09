package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"sync"
	"sync/atomic"
	"time"

	"github.com/gorilla/websocket"
)

// RealtimeNamespace is the Solian websocket gateway namespace.
const RealtimeNamespace = "dev.solsynth.solian"

// Packet is one decoded frame from the websocket gateway.
type Packet struct {
	Type  string          `json:"type"`
	Data  json.RawMessage `json:"data"`
	AckID string          `json:"_ack_id"`
}

// Realtime owns the Solian websocket connection: dial, heartbeat, at-least-once
// acknowledgement, buffer resume and exponential reconnect.
type Realtime struct {
	client *Client
	tokens *TokenSource

	events chan Packet
	state  chan error

	mu            sync.Mutex
	writeMu       sync.Mutex
	conn          *websocket.Conn
	room          string
	subscriptions map[string]bool
	started       bool
	connected     atomic.Bool
}

// NewRealtime builds the gateway client for a signed-in API client.
func NewRealtime(client *Client, tokens *TokenSource) *Realtime {
	return &Realtime{
		client:        client,
		tokens:        tokens,
		events:        make(chan Packet, 64),
		state:         make(chan error, 4),
		subscriptions: map[string]bool{},
	}
}

// Events streams every decoded packet until the context is cancelled.
func (r *Realtime) Events() <-chan Packet { return r.events }

// State reports connection errors. Successful connections are silent.
func (r *Realtime) State() <-chan error { return r.state }

// Connected reports whether the websocket is currently open.
func (r *Realtime) Connected() bool { return r.connected.Load() }

// Run dials and reconnects until ctx is cancelled.
func (r *Realtime) Run(ctx context.Context) {
	r.mu.Lock()
	if r.started {
		r.mu.Unlock()
		return
	}
	r.started = true
	r.mu.Unlock()

	delay := 500 * time.Millisecond
	for ctx.Err() == nil {
		err := r.serve(ctx)
		if ctx.Err() != nil {
			return
		}
		if err != nil {
			select {
			case r.state <- err:
			default:
			}
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(delay):
		}
		if delay < 30*time.Second {
			delay *= 2
			if delay > 30*time.Second {
				delay = 30 * time.Second
			}
		}
	}
}

// SubscribeRoom asks the gateway for a chat room's live messages.
func (r *Realtime) SubscribeRoom(roomID string) {
	if roomID == "" {
		return
	}
	r.mu.Lock()
	previous := r.room
	previousSubscribed := previous != "" && r.subscriptions[previous]
	r.room = roomID
	r.subscriptions[roomID] = true
	r.mu.Unlock()
	if previous != "" && previous != roomID && previousSubscribed {
		r.send(map[string]any{"type": "messages.unsubscribe", "data": map[string]any{"chat_room_id": previous}, "endpoint": "messager"})
		r.mu.Lock()
		delete(r.subscriptions, previous)
		r.mu.Unlock()
	}
	r.send(map[string]any{"type": "messages.subscribe", "data": map[string]any{"chat_room_id": roomID}, "endpoint": "messager"})
}

// UnsubscribeRoom drops a chat room subscription.
func (r *Realtime) UnsubscribeRoom(roomID string) {
	r.mu.Lock()
	if r.room == roomID {
		r.room = ""
	}
	delete(r.subscriptions, roomID)
	r.mu.Unlock()
	r.send(map[string]any{"type": "messages.unsubscribe", "data": map[string]any{"chat_room_id": roomID}, "endpoint": "messager"})
}

// send writes one JSON frame if a socket is open.
func (r *Realtime) send(frame map[string]any) {
	r.mu.Lock()
	conn := r.conn
	r.mu.Unlock()
	if conn == nil {
		return
	}
	data, err := json.Marshal(frame)
	if err != nil {
		return
	}
	r.writeMu.Lock()
	defer r.writeMu.Unlock()
	_ = conn.WriteMessage(websocket.TextMessage, data)
}

// serve holds one connection until it fails or ctx is cancelled.
func (r *Realtime) serve(ctx context.Context) error {
	if r.tokens == nil {
		return errors.New("realtime: no session")
	}
	token, err := r.tokens.Token(ctx)
	if err != nil {
		return err
	}
	uri, err := r.endpoint()
	if err != nil {
		return err
	}
	header := http.Header{}
	if token != "" {
		header.Set("Authorization", "Bearer "+token)
	}
	dialer := websocket.Dialer{
		HandshakeTimeout: 15 * time.Second,
		NetDialContext:   (&net.Dialer{Timeout: 15 * time.Second}).DialContext,
	}
	conn, _, err := dialer.DialContext(ctx, uri, header)
	if err != nil {
		return fmt.Errorf("realtime: %w", err)
	}
	r.mu.Lock()
	r.conn = conn
	r.mu.Unlock()
	r.connected.Store(true)
	defer func() {
		r.connected.Store(false)
		r.mu.Lock()
		if r.conn == conn {
			r.conn = nil
		}
		r.mu.Unlock()
		_ = conn.Close()
	}()

	// The gateway replays pending frames for this account and device, so a
	// fresh connection asks for the buffer before anything else.
	r.send(map[string]any{"type": "session.resume", "data": nil})
	r.send(map[string]any{"type": "ping", "data": nil})
	r.mu.Lock()
	rooms := make([]string, 0, len(r.subscriptions))
	for room := range r.subscriptions {
		rooms = append(rooms, room)
	}
	r.mu.Unlock()
	for _, room := range rooms {
		r.send(map[string]any{"type": "messages.subscribe", "data": map[string]any{"chat_room_id": room}, "endpoint": "messager"})
	}

	heartbeat := time.NewTicker(60 * time.Second)
	defer heartbeat.Stop()
	done := make(chan struct{})
	defer close(done)
	go func() {
		for {
			select {
			case <-ctx.Done():
				_ = conn.Close()
				return
			case <-done:
				return
			case <-heartbeat.C:
				r.send(map[string]any{"type": "ping", "data": nil})
			}
		}
	}()

	for {
		_, payload, err := conn.ReadMessage()
		if err != nil {
			return err
		}
		var packet Packet
		if err := json.Unmarshal(payload, &packet); err != nil {
			continue
		}
		if packet.AckID != "" {
			// At-least-once delivery: a repeated frame is acked again because
			// the gateway never saw the previous acknowledgement.
			r.send(map[string]any{"type": "session.ack", "data": map[string]any{"ack_id": packet.AckID}})
		}
		if packet.Type == "error" || packet.Type == "error.dupe" {
			return fmt.Errorf("realtime: gateway error %s", packet.Type)
		}
		select {
		case r.events <- packet:
		case <-ctx.Done():
			return ctx.Err()
		}
	}
}

// endpoint builds the websocket URL from the HTTP API base.
func (r *Realtime) endpoint() (string, error) {
	raw := r.client.rootEndpoint("/ws")
	parsed, err := url.Parse(raw)
	if err != nil {
		return "", err
	}
	switch parsed.Scheme {
	case "https":
		parsed.Scheme = "wss"
	case "http":
		parsed.Scheme = "ws"
	}
	query := parsed.Query()
	query.Set("namespace", RealtimeNamespace)
	parsed.RawQuery = query.Encode()
	return parsed.String(), nil
}

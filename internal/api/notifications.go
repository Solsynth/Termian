package api

import (
	"context"
	"net/http"
	"net/url"
)

// Notifications lists the signed-in account's Ring notifications, newest first.
func (c *Client) Notifications(ctx context.Context, offset, take int) (Page[Notification], error) {
	var items []Notification
	meta, err := c.doRootPage(ctx, http.MethodGet,
		pagePath("/metoer/notifications", offset, take), nil, &items)
	return Page[Notification]{Items: items, Total: totalFrom(meta.Header)}, err
}

// UnreadNotificationCount returns the server's unread total.
func (c *Client) UnreadNotificationCount(ctx context.Context) (int, error) {
	var raw any
	if err := c.doRoot(ctx, http.MethodGet, "/metoer/notifications/count", nil, &raw); err != nil {
		return 0, err
	}
	switch value := raw.(type) {
	case float64:
		return int(value), nil
	case map[string]any:
		if n, ok := value["count"].(float64); ok {
			return int(n), nil
		}
	}
	// Some deployments answer with a bare JSON number, which the generic
	// decoder still yields as float64; anything else means zero.
	return 0, nil
}

// MarkNotificationRead marks one notification as viewed.
func (c *Client) MarkNotificationRead(ctx context.Context, id string) error {
	return c.doRoot(ctx, http.MethodPost, "/metoer/notifications/"+url.PathEscape(id)+"/read", nil, nil)
}

// MarkAllNotificationsRead marks every notification as viewed.
func (c *Client) MarkAllNotificationsRead(ctx context.Context) error {
	return c.doRoot(ctx, http.MethodPost, "/metoer/notifications/all/read", nil, nil)
}

// DeleteNotification removes one notification.
func (c *Client) DeleteNotification(ctx context.Context, id string) error {
	return c.doRoot(ctx, http.MethodDelete, "/metoer/notifications/"+url.PathEscape(id), nil, nil)
}

// DeleteAllNotifications removes every notification.
func (c *Client) DeleteAllNotifications(ctx context.Context) error {
	return c.doRoot(ctx, http.MethodDelete, "/metoer/notifications", nil, nil)
}

// NotificationPreferences lists the account's per-topic delivery levels.
func (c *Client) NotificationPreferences(ctx context.Context) ([]NotificationPreference, error) {
	var items []NotificationPreference
	err := c.doRoot(ctx, http.MethodGet, "/metoer/notifications/preferences", nil, &items)
	return items, err
}

// SetNotificationPreference writes a per-topic delivery level.
func (c *Client) SetNotificationPreference(ctx context.Context, topic string, level int) error {
	return c.doRoot(ctx, http.MethodPut,
		"/metoer/notifications/preferences/"+url.PathEscape(topic),
		map[string]any{"preference": level}, nil)
}

// DeleteNotificationPreference resets a topic to the server default.
func (c *Client) DeleteNotificationPreference(ctx context.Context, topic string) error {
	return c.doRoot(ctx, http.MethodDelete,
		"/metoer/notifications/preferences/"+url.PathEscape(topic), nil, nil)
}

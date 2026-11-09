package api

import "time"

// Challenge is an authentication challenge: the server-side state machine for a
// password/TOTP/email-code login.
type Challenge struct {
	ID               string     `json:"id"`
	AccountID        string     `json:"account_id"`
	DeviceID         string     `json:"device_id"`
	DeviceName       *string    `json:"device_name,omitempty"`
	Platform         int        `json:"platform"`
	StepTotal        int        `json:"step_total"`
	StepRemain       int        `json:"step_remain"`
	FailedAttempts   int        `json:"failed_attempts"`
	BlacklistFactors []string   `json:"blacklist_factors"`
	ExpiredAt        *time.Time `json:"expired_at,omitempty"`
	DeclinedAt       *time.Time `json:"declined_at,omitempty"`
	ApprovedAt       *time.Time `json:"approved_at,omitempty"`
}

// Factor is one login factor configured on an account.
type Factor struct {
	ID          string     `json:"id"`
	Type        int        `json:"type"`
	Trustworthy int        `json:"trustworthy"`
	EnabledAt   *time.Time `json:"enabled_at,omitempty"`
	ExpiredAt   *time.Time `json:"expired_at,omitempty"`
}

// Factor type codes as sent by Stargate.
const (
	FactorPassword     = 0
	FactorEmailCode    = 1
	FactorInApp        = 2
	FactorTOTP         = 3
	FactorPIN          = 4
	FactorRecoveryCode = 5
	FactorNFC          = 6
	FactorPasskey      = 7
	FactorQR           = 8
)

// Usable reports whether Termian can complete this factor from a terminal.
func (f Factor) Usable() bool {
	switch f.Type {
	case FactorPassword, FactorEmailCode, FactorTOTP, FactorPIN:
		return true
	default:
		return false
	}
}

// NeedsSecret reports whether the factor expects a typed secret.
func (f Factor) NeedsSecret() bool { return f.Usable() }

// SecretIsOpaque reports whether the typed secret must be masked (passwords and
// PINs) rather than shown as a short code.
func (f Factor) SecretIsOpaque() bool {
	return f.Type == FactorPassword || f.Type == FactorPIN
}

// KindLabel names the publisher kind.
func (p Publisher) KindLabel() string {
	switch p.Type {
	case 1:
		return "organization"
	case 2:
		return "fediverse"
	default:
		return "individual"
	}
}

// Label is the human name of the factor.
func (f Factor) Label() string {
	switch f.Type {
	case FactorPassword:
		return "password"
	case FactorEmailCode:
		return "email code"
	case FactorInApp:
		return "in-app approval"
	case FactorTOTP:
		return "authenticator code"
	case FactorPIN:
		return "PIN"
	case FactorRecoveryCode:
		return "recovery code"
	case FactorNFC:
		return "NFC token"
	case FactorPasskey:
		return "passkey"
	case FactorQR:
		return "QR login"
	default:
		return "factor"
	}
}

// TokenPair is the challenge-flow token response.
type TokenPair struct {
	Token            string `json:"token"`
	RefreshToken     string `json:"refresh_token"`
	ExpiresIn        int64  `json:"expires_in"`
	RefreshExpiresIn int64  `json:"refresh_expires_in"`
}

// OAuthToken is the device-flow (OIDC) token response.
type OAuthToken struct {
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token"`
	TokenType    string `json:"token_type"`
	Scope        string `json:"scope"`
	IDToken      string `json:"id_token"`
	ExpiresIn    int64  `json:"expires_in"`
}

// DeviceCode is the RFC 8628 device authorization response.
type DeviceCode struct {
	DeviceCode              string `json:"device_code"`
	UserCode                string `json:"user_code"`
	VerificationURI         string `json:"verification_uri"`
	VerificationURIComplete string `json:"verification_uri_complete"`
	ExpiresIn               int    `json:"expires_in"`
	Interval                int    `json:"interval"`
}

// Identity is the payload of GET /auth/me.
type Identity struct {
	ID          string     `json:"id"`
	Name        string     `json:"name"`
	Nick        string     `json:"nick"`
	Language    string     `json:"language"`
	Region      string     `json:"region"`
	IsSuperuser bool       `json:"is_superuser"`
	ActivatedAt *time.Time `json:"activated_at,omitempty"`
	SessionID   string     `json:"session_id"`
	TokenType   string     `json:"token_type"`
}

// Account is the payload of GET /accounts/me.
type Account struct {
	ID          string     `json:"id"`
	Name        string     `json:"name"`
	Nick        string     `json:"nick"`
	ActivatedAt *time.Time `json:"activated_at,omitempty"`
	Profile     *Profile   `json:"profile,omitempty"`
	PerkLevel   int        `json:"perk_level"`
	CreatedAt   *time.Time `json:"created_at,omitempty"`
}

// Profile is the public account profile.
type Profile struct {
	Level      int        `json:"level"`
	Experience int        `json:"experience"`
	CreatedAt  *time.Time `json:"created_at,omitempty"`
}

// PostTag is a tag attached to a post.
type PostTag struct {
	ID          string `json:"id"`
	Slug        string `json:"slug"`
	Name        string `json:"name"`
	Description string `json:"description"`
}

// Display prefers the human name, then the slug with a hash.
func (t PostTag) Display() string {
	if t.Name != "" {
		return t.Name
	}
	if t.Slug != "" {
		return "#" + t.Slug
	}
	return ""
}

// Notification is one Ring notification (GET /metoer/notifications).
type Notification struct {
	ID        string         `json:"id"`
	AppID     string         `json:"app_id"`
	Topic     string         `json:"topic"`
	Title     string         `json:"title"`
	Subtitle  string         `json:"subtitle"`
	Content   string         `json:"content"`
	Meta      map[string]any `json:"meta"`
	ViewedAt  *time.Time     `json:"viewed_at"`
	AccountID string         `json:"account_id"`
	CreatedAt time.Time      `json:"created_at"`
}

// Body is the notification's message text.
func (n Notification) Body() string { return n.Content }

// Unread reports whether the notification has never been viewed.
func (n Notification) Unread() bool { return n.ViewedAt == nil }

// ActionURI is the deep link the notification carries, if any.
func (n Notification) ActionURI() string {
	if n.Meta == nil {
		return ""
	}
	uri, _ := n.Meta["action_uri"].(string)
	return uri
}

// Preference levels for a notification topic.
const (
	NotificationPreferenceNormal = 0
	NotificationPreferenceSilent = 1
	NotificationPreferenceReject = 2
)

// NotificationPreference is the delivery level of one notification topic.
type NotificationPreference struct {
	ID         string `json:"id"`
	AccountID  string `json:"account_id"`
	Topic      string `json:"topic"`
	Preference int    `json:"preference"`
}

// NotificationPreferenceLabel names a preference level.
func NotificationPreferenceLabel(level int) string {
	switch level {
	case NotificationPreferenceSilent:
		return "silent"
	case NotificationPreferenceReject:
		return "reject"
	default:
		return "normal"
	}
}

// NotificationTopic is one topic the account can tune.
type NotificationTopic struct {
	Topic       string
	Description string
}

// DefaultNotificationTopics mirrors the Ring topics a Solian client knows.
var DefaultNotificationTopics = []NotificationTopic{
	{Topic: "posts.mentions.new", Description: "Post mentions"},
	{Topic: "post.replies", Description: "Post replies"},
	{Topic: "posts.reactions.new", Description: "New reactions"},
	{Topic: "posts.awards.new", Description: "Post awards"},
	{Topic: "subscriptions.discontinued_in_app", Description: "Subscription discontinued"},
	{Topic: "subscriptions.begun", Description: "Subscription started"},
	{Topic: "subscriptions.orders", Description: "Provider orders"},
	{Topic: "gifts.claimed", Description: "Gift claimed"},
	{Topic: "wallets.transactions", Description: "Wallet transactions"},
	{Topic: "auth.verification", Description: "Auth verification"},
	{Topic: "invites.realms", Description: "Realm invites"},
}

package hosted

// sessionInfo is the authenticated identity and token pair an SSH connection
// carries into the TUI. It is stored on the SSH context after the public key
// exchange and read once when the Bubble Tea program is built.
type sessionInfo struct {
	AccountID        string
	KeyID            string
	KeyLabel         string
	Fingerprint      string
	AccessToken      string
	RefreshToken     string
	ExpiresIn        int64
	RefreshExpiresIn int64
}

// sessionContextKey scopes the value inside the SSH connection context.
type sessionContextKey struct{}

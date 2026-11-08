package hosted

// sessionInfo is the authenticated identity and token pair exchanged for the
// SSH key the handshake verified, then handed to the Bubble Tea program.
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

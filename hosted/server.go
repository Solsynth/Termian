// Package hosted serves the Termian TUI over SSH. Each connection authenticates
// with a Solian-registered SSH public key: the gateway exchanges the key with
// Stargate for a session, then runs one Bubble Tea program bound to that
// account. No local credential store or keyring is involved.
package hosted

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"charm.land/ssh"
	"charm.land/wish/v2"
	"charm.land/wish/v2/activeterm"
	wishbubbletea "charm.land/wish/v2/bubbletea"
	"charm.land/wish/v2/logging"
	wishrecover "charm.land/wish/v2/recover"
	gossh "golang.org/x/crypto/ssh"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"

	grpctls "src.solsynth.dev/sosys/lighthouse/shared/platform/grpctls"
	gen "src.solsynth.dev/sosys/lighthouse/shared/proto"

	"src.solsynth.dev/sosys/termian/internal/api"
	"src.solsynth.dev/sosys/termian/internal/credentials"
	"src.solsynth.dev/sosys/termian/internal/deviceid"
	"src.solsynth.dev/sosys/termian/internal/tui"
)

// Defaults applied by Serve when a Config field is empty.
const (
	DefaultPurpose     = "termian-hosted"
	DefaultAttachments = "preview"
	// hostedDevicePrefix names the synthetic device Stargate records for
	// hosted sessions. It must match the prefix the exchange RPC uses.
	hostedDevicePrefix = "termian" + "-hosted-"
	// exchangeTimeout bounds one key exchange; a slow Stargate must not hold
	// an SSH handshake open indefinitely.
	exchangeTimeout = 10 * time.Second
	shutdownTimeout = 5 * time.Second
)

// Config is the hosted server's static configuration.
type Config struct {
	// Listen is the SSH listen address, e.g. ":2222".
	Listen string
	// HostKeyPath points at an ed25519 host key; wish generates one when the
	// file is missing.
	HostKeyPath string
	// StargateGRPC is the address of Stargate's internal gRPC surface.
	StargateGRPC string
	// GRPCTLS enables TLS to Stargate.
	GRPCTLS bool
	// GRPCCAFile verifies Stargate's certificate. Empty skips verification,
	// matching the fleet's self-signed default.
	GRPCCAFile string
	// GRPCCertFile and GRPCKeyFile identify this server to a Stargate that
	// enforces mTLS.
	GRPCCertFile string
	GRPCKeyFile  string
	// GRPCServerName overrides the name verified against Stargate's
	// certificate. Empty uses the dial target's host.
	GRPCServerName string
	// ExchangeToken is the deployment's hosted exchange credential.
	ExchangeToken string
	// Purpose is echoed in Stargate's audit record, e.g. "termian-hosted".
	Purpose string
	// APIBase is the Stargate HTTP base used for content and refresh calls.
	APIBase string
	// ClientID is the OAuth client id used when refreshing tokens.
	ClientID string
	// Scope is the OAuth scope requested by login flows (unused by the
	// exchange but kept for parity with the TUI).
	Scope string
	// Timeout bounds each HTTP request.
	Timeout time.Duration
	// Version is shown in the TUI and the SSH client's user agent.
	Version string
	// Attachments mirrors the client policy; hosted defaults to preview since
	// the server has no display to open files on.
	Attachments string
	// ChatHistory is how many messages a chat pane keeps per room.
	ChatHistory int
}

// Serve dials Stargate, builds the SSH server and serves until ctx is
// cancelled.
func Serve(ctx context.Context, cfg Config, log *slog.Logger) error {
	if log == nil {
		log = slog.Default()
	}
	if strings.TrimSpace(cfg.ExchangeToken) == "" {
		return errors.New("hosted: exchange token is required")
	}
	if strings.TrimSpace(cfg.StargateGRPC) == "" {
		return errors.New("hosted: Stargate gRPC address is required")
	}

	transport := insecure.NewCredentials()
	if cfg.GRPCTLS {
		creds, err := grpctls.Client(grpctls.Files{
			CAFile:     cfg.GRPCCAFile,
			CertFile:   cfg.GRPCCertFile,
			KeyFile:    cfg.GRPCKeyFile,
			ServerName: cfg.GRPCServerName,
		})
		if err != nil {
			return fmt.Errorf("hosted: load Stargate gRPC TLS: %w", err)
		}
		transport = creds
	}
	conn, err := grpc.NewClient(cfg.StargateGRPC, grpc.WithTransportCredentials(transport))
	if err != nil {
		return fmt.Errorf("hosted: dial Stargate gRPC: %w", err)
	}
	defer conn.Close()

	srv, err := NewServer(cfg, gen.NewDyAccountKeyServiceClient(conn), log)
	if err != nil {
		return err
	}

	errCh := make(chan error, 1)
	go func() { errCh <- srv.ListenAndServe() }()

	select {
	case <-ctx.Done():
		shutdownCtx, cancel := context.WithTimeout(context.Background(), shutdownTimeout)
		defer cancel()
		_ = srv.Shutdown(shutdownCtx)
		return nil
	case err := <-errCh:
		if errors.Is(err, ssh.ErrServerClosed) {
			return nil
		}
		return fmt.Errorf("hosted: serve SSH: %w", err)
	}
}

// NewServer builds the wish server. It is separate from Serve so tests can
// run it against a fake exchange client and their own listener.
func NewServer(cfg Config, exchange gen.DyAccountKeyServiceClient, log *slog.Logger) (*ssh.Server, error) {
	if exchange == nil {
		return nil, errors.New("hosted: missing Stargate key service client")
	}
	if cfg.Listen == "" {
		return nil, errors.New("hosted: missing listen address")
	}
	if cfg.APIBase == "" {
		return nil, errors.New("hosted: missing Stargate API base")
	}
	if cfg.Purpose == "" {
		cfg.Purpose = DefaultPurpose
	}
	if cfg.Attachments == "" {
		cfg.Attachments = DefaultAttachments
	}

	gateway := &gateway{cfg: cfg, exchange: exchange, log: log}

	return wish.NewServer(
		wish.WithAddress(cfg.Listen),
		wish.WithHostKeyPath(cfg.HostKeyPath),
		wish.WithVersion("Termian "+cfg.Version),
		wish.WithPublicKeyAuth(gateway.authenticate),
		wish.WithMiddleware(
			wishbubbletea.Middleware(gateway.program),
			logging.MiddlewareWithLogger(&logAdapter{log: log}),
			activeterm.Middleware(),
			wishrecover.MiddlewareWithLogger(&logAdapter{log: log}),
		),
	)
}

// gateway holds the per-server state the SSH callbacks need.
type gateway struct {
	cfg      Config
	exchange gen.DyAccountKeyServiceClient
	log      *slog.Logger
}

// authenticate checks that the offered key belongs to a Solian account. It
// deliberately mints nothing: the SSH protocol calls this handler for unsigned
// public-key queries too, so exchanging here would create sessions before the
// client has proved possession of the key. The session is exchanged in
// program, using the key the handshake actually authenticated.
func (g *gateway) authenticate(ctx ssh.Context, key ssh.PublicKey) bool {
	ctxTimeout, cancel := context.WithTimeout(ctx, exchangeTimeout)
	defer cancel()

	pub := strings.TrimSpace(string(gossh.MarshalAuthorizedKey(key)))
	response, err := g.exchange.ValidateSSHPublicKey(ctxTimeout, &gen.DyValidateSSHPublicKeyRequest{
		PublicKey:   pub,
		Fingerprint: gossh.FingerprintSHA256(key),
		Purpose:     g.cfg.Purpose,
	})
	if err != nil {
		g.log.Warn("SSH key validation failed", "remote", ctx.RemoteAddr(), "error", err)
		return false
	}
	if !response.GetValid() {
		g.log.Info("SSH key rejected",
			"remote", ctx.RemoteAddr(),
			"fingerprint", response.GetFingerprint(),
			"reason", response.GetReason())
		return false
	}
	g.log.Info("hosted SSH key accepted",
		"account_id", response.GetAccountId(),
		"key_id", response.GetKeyId(),
		"remote", ctx.RemoteAddr())
	return true
}

// exchangeSession turns the authenticated key into tokens. It runs only after
// the SSH handshake has verified the signature for this key.
func (g *gateway) exchangeSession(ctx ssh.Context, key ssh.PublicKey) (*sessionInfo, error) {
	ctxTimeout, cancel := context.WithTimeout(ctx, exchangeTimeout)
	defer cancel()

	pub := strings.TrimSpace(string(gossh.MarshalAuthorizedKey(key)))
	request := &gen.DyExchangeSSHPublicKeyForSessionRequest{
		ServiceToken: g.cfg.ExchangeToken,
		PublicKey:    pub,
		Fingerprint:  gossh.FingerprintSHA256(key),
		Purpose:      g.cfg.Purpose,
		DeviceName:   "Hosted Termian",
		UserAgent:    "Termian/" + g.cfg.Version + " ssh/" + ctx.ClientVersion(),
	}
	if remote := ctx.RemoteAddr(); remote != nil {
		if host, _, err := net.SplitHostPort(remote.String()); err == nil {
			request.IpAddress = host
		}
	}

	response, err := g.exchange.ExchangeSSHPublicKeyForSession(ctxTimeout, request)
	if err != nil {
		g.log.Warn("SSH key exchange failed", "remote", ctx.RemoteAddr(), "error", err)
		return nil, err
	}
	if !response.GetValid() {
		g.log.Info("SSH key exchange rejected",
			"remote", ctx.RemoteAddr(),
			"fingerprint", response.GetFingerprint(),
			"reason", response.GetReason())
		return nil, fmt.Errorf("key rejected: %s", response.GetReason())
	}
	if response.GetAccessToken() == "" || response.GetRefreshToken() == "" {
		g.log.Warn("SSH key exchange returned no tokens", "account_id", response.GetAccountId())
		return nil, errors.New("key exchange returned no tokens")
	}

	return &sessionInfo{
		AccountID:        response.GetAccountId(),
		KeyID:            response.GetKeyId(),
		KeyLabel:         response.GetKeyLabel(),
		Fingerprint:      response.GetFingerprint(),
		AccessToken:      response.GetAccessToken(),
		RefreshToken:     response.GetRefreshToken(),
		ExpiresIn:        response.GetExpiresIn(),
		RefreshExpiresIn: response.GetRefreshExpiresIn(),
	}, nil
}

// program builds the Bubble Tea model for one authenticated session.
func (g *gateway) program(sess ssh.Session) (tea.Model, []tea.ProgramOption) {
	key := sess.PublicKey()
	if key == nil {
		wish.Fatalln(sess, "authentication did not use a public key")
		return nil, nil
	}
	pending, err := g.exchangeSession(sess.Context(), key)
	if err != nil {
		wish.Fatalf(sess, "authentication failed: %v\r\n", err)
		return nil, nil
	}

	device := deviceid.Identity{
		ID:       hostedDevicePrefix + pending.AccountID,
		Name:     "Hosted Termian",
		Platform: deviceid.PlatformLinux,
	}
	client, err := api.New(g.cfg.APIBase, g.cfg.ClientID, g.cfg.Scope, device, g.cfg.Version)
	if err != nil {
		wish.Fatalf(sess, "cannot build API client: %v\r\n", err)
		return nil, nil
	}
	if g.cfg.Timeout > 0 {
		client.HTTP.Timeout = g.cfg.Timeout
	}

	store := credentials.NewMemory()
	now := time.Now().UTC()
	refreshExpiry := now.Add(time.Duration(pending.RefreshExpiresIn) * time.Second)
	creds := &credentials.Credentials{
		Server:           g.cfg.APIBase,
		Method:           credentials.MethodSSH,
		DeviceID:         device.ID,
		AccountID:        pending.AccountID,
		RefreshToken:     pending.RefreshToken,
		RefreshExpiresAt: refreshExpiry,
		ObtainedAt:       now,
	}
	tokens := api.NewTokenSource(client, store, creds)
	tokens.Seed(creds, pending.AccessToken, pending.ExpiresIn)

	return tui.NewModelWithScreen(tui.Deps{
		Ctx:         sess.Context(),
		API:         client,
		Content:     client,
		Realtime:    api.NewRealtime(client, tokens),
		Tokens:      tokens,
		Store:       store,
		Server:      g.cfg.APIBase,
		ClientID:    g.cfg.ClientID,
		DeviceID:    device.ID,
		Version:     g.cfg.Version,
		Attachments: g.cfg.Attachments,
		ChatHistory: g.cfg.ChatHistory,
	}, tui.ScreenHome), nil
}

// logAdapter routes wish's Printf-style logs through slog.
type logAdapter struct{ log *slog.Logger }

func (a *logAdapter) Printf(format string, v ...any) {
	a.log.Info(fmt.Sprintf(format, v...))
}

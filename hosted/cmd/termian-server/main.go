// Command termian-server serves the Termian TUI over SSH, authenticating with
// Solian SSH keys. It is the hosted-mode counterpart of the client binary and
// lives in its own module so client installs stay free of the SSH server's
// dependencies.
package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/spf13/cobra"

	"src.solsynth.dev/sosys/termian/hosted"
	"src.solsynth.dev/sosys/termian/internal/api"
	"src.solsynth.dev/sosys/termian/internal/config"
	"src.solsynth.dev/sosys/termian/internal/version"
)

// options collects the server flags.
type options struct {
	server         string
	clientID       string
	configPath     string
	timeout        time.Duration
	attachments    string
	chatHistory    int
	listen         string
	hostKey        string
	stargateGRPC   string
	grpcTLS        bool
	grpcCA         string
	grpcCert       string
	grpcKey        string
	grpcServerName string
	exchangeToken  string
	purpose        string
}

func main() {
	if err := run(); err != nil {
		var usage *usageError
		if errors.As(err, &usage) {
			fmt.Fprintf(os.Stderr, "error: %s\n", usage.msg)
			os.Exit(2)
		}
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}
}

// usageError marks invalid invocations, which exit 2 rather than 1.
type usageError struct{ msg string }

func (e *usageError) Error() string { return e.msg }

func run() error {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	var opts options
	root := &cobra.Command{
		Use:   "termian-server",
		Short: "Serve the Termian TUI over SSH",
		Long: "Run an SSH server that serves the interactive client.\n\n" +
			"Each connection authenticates with an SSH key registered on a Solian\n" +
			"account. Stargate validates the key and the gateway exchanges it for a\n" +
			"session, so `ssh <host>` is the only login step.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return serve(cmd.Context(), opts)
		},
	}
	pf := root.Flags()
	pf.StringVar(&opts.server, "server", "", "Stargate API base (default https://api.solian.app)")
	pf.StringVar(&opts.clientID, "client-id", "", "OAuth client id (default solian)")
	pf.StringVar(&opts.configPath, "config", "", "path to config.toml")
	pf.DurationVar(&opts.timeout, "timeout", api.DefaultTimeout, "per-request timeout")
	pf.StringVar(&opts.attachments, "attachments", "", "attachment handling: browser, preview or off")
	pf.IntVar(&opts.chatHistory, "chat-history", 0, "messages kept per chat room")
	pf.StringVar(&opts.listen, "listen", ":2222", "SSH listen address")
	pf.StringVar(&opts.stargateGRPC, "stargate-grpc", "127.0.0.1:9090", "Stargate gRPC address for key validation")
	pf.BoolVar(&opts.grpcTLS, "stargate-grpc-tls", false, "use TLS to Stargate")
	pf.StringVar(&opts.grpcCA, "stargate-grpc-ca", "", "CA bundle verifying Stargate's gRPC certificate (empty skips verification)")
	pf.StringVar(&opts.grpcCert, "stargate-grpc-cert", "", "client certificate for Stargate gRPC mTLS")
	pf.StringVar(&opts.grpcKey, "stargate-grpc-key", "", "client key for Stargate gRPC mTLS")
	pf.StringVar(&opts.grpcServerName, "stargate-grpc-server-name", "", "override the name verified against Stargate's certificate")
	pf.StringVar(&opts.exchangeToken, "exchange-token", os.Getenv("TERMIAN_EXCHANGE_TOKEN"), "hosted exchange service token (also TERMIAN_EXCHANGE_TOKEN)")
	pf.StringVar(&opts.purpose, "purpose", hosted.DefaultPurpose, "purpose echoed in Stargate's audit record")
	pf.StringVar(&opts.hostKey, "host-key", "", "SSH host key path (default $XDG_STATE_HOME/termian/host_ed25519)")
	if err := root.ExecuteContext(ctx); err != nil {
		return err
	}
	return nil
}

func serve(ctx context.Context, opts options) error {
	if strings.TrimSpace(opts.exchangeToken) == "" {
		return &usageError{msg: "hosted mode needs --exchange-token or TERMIAN_EXCHANGE_TOKEN"}
	}

	cfg, err := config.Load(config.Overrides{
		Server:      opts.server,
		ClientID:    opts.clientID,
		Attachments: opts.attachments,
		ChatHistory: opts.chatHistory,
		Path:        opts.configPath,
	})
	if err != nil {
		return err
	}
	base, err := api.NormalizeBase(cfg.Server)
	if err != nil {
		return err
	}

	hostKey := opts.hostKey
	if hostKey == "" {
		hostKey = filepath.Join(cfg.StateDir, "host_ed25519")
	}
	if err := os.MkdirAll(filepath.Dir(hostKey), 0o700); err != nil {
		return err
	}

	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelInfo}))
	logger.Info("hosted Termian starting",
		"listen", opts.listen, "server", base.String(), "grpc", opts.stargateGRPC)
	return hosted.Serve(ctx, hosted.Config{
		Listen:         opts.listen,
		HostKeyPath:    hostKey,
		StargateGRPC:   opts.stargateGRPC,
		GRPCTLS:        opts.grpcTLS,
		GRPCCAFile:     opts.grpcCA,
		GRPCCertFile:   opts.grpcCert,
		GRPCKeyFile:    opts.grpcKey,
		GRPCServerName: opts.grpcServerName,
		ExchangeToken:  opts.exchangeToken,
		Purpose:        opts.purpose,
		APIBase:        base.String(),
		ClientID:       cfg.ClientID,
		Scope:          cfg.Scope,
		Timeout:        opts.timeout,
		Version:        version.Version,
		Attachments:    cfg.Attachments,
		ChatHistory:    cfg.ChatHistory,
	}, logger)
}

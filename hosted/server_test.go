package hosted

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/json"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"charm.land/wish/v2/testsession"
	"golang.org/x/crypto/ssh"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/status"
	"google.golang.org/grpc/test/bufconn"

	gen "src.solsynth.dev/sosys/lighthouse/shared/proto"
)

type fakeExchange struct {
	gen.UnimplementedDyAccountKeyServiceServer
	publicKey string
	accountID string
}

func (f *fakeExchange) ExchangeSSHPublicKeyForSession(_ context.Context, req *gen.DyExchangeSSHPublicKeyForSessionRequest) (*gen.DyExchangeSSHPublicKeyForSessionResponse, error) {
	if req.GetServiceToken() != "exchange-secret" {
		return nil, status.Error(codes.PermissionDenied, "bad service token")
	}
	if req.GetPublicKey() != f.publicKey {
		return &gen.DyExchangeSSHPublicKeyForSessionResponse{
			Fingerprint: req.GetFingerprint(),
			Reason:      "KEY_NOT_FOUND",
		}, nil
	}
	return &gen.DyExchangeSSHPublicKeyForSessionResponse{
		Valid:            true,
		AccountId:        f.accountID,
		KeyId:            "key-id",
		KeyLabel:         "laptop",
		Fingerprint:      req.GetFingerprint(),
		Purpose:          req.GetPurpose(),
		AccessToken:      "access-token",
		RefreshToken:     "refresh-token",
		ExpiresIn:        3600,
		RefreshExpiresIn: 86400,
	}, nil
}

func TestServerServesAuthenticatedSession(t *testing.T) {
	_, private, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	signer, err := ssh.NewSignerFromKey(private)
	if err != nil {
		t.Fatal(err)
	}
	publicLine := strings.TrimSpace(string(ssh.MarshalAuthorizedKey(signer.PublicKey())))
	const accountID = "8b1c9d2e-4f5a-4b6c-9d8e-7a6b5c4d3e2f"

	lis := bufconn.Listen(1 << 20)
	grpcServer := grpc.NewServer()
	gen.RegisterDyAccountKeyServiceServer(grpcServer, &fakeExchange{publicKey: publicLine, accountID: accountID})
	go func() { _ = grpcServer.Serve(lis) }()
	t.Cleanup(grpcServer.Stop)

	conn, err := grpc.NewClient("passthrough:///bufconn",
		grpc.WithContextDialer(func(ctx context.Context, _ string) (net.Conn, error) {
			return lis.DialContext(ctx)
		}),
		grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()

	requested := make(chan string, 1)
	apiServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer access-token" {
			t.Errorf("request %s sent authorization %q", r.URL.Path, r.Header.Get("Authorization"))
		}
		switch r.URL.Path {
		case "/stargate/auth/me":
			_ = json.NewEncoder(w).Encode(map[string]any{
				"id": accountID, "name": "alice", "nick": "Alice",
				"session_id": "session", "token_type": "user",
			})
		case "/stargate/accounts/me":
			_ = json.NewEncoder(w).Encode(map[string]any{
				"id": accountID, "name": "alice", "nick": "Alice",
			})
		default:
			http.NotFound(w, r)
			return
		}
		select {
		case requested <- r.URL.Path:
		default:
		}
	}))
	defer apiServer.Close()

	server, err := NewServer(Config{
		Listen:        "127.0.0.1:0",
		HostKeyPath:   filepath.Join(t.TempDir(), "host_ed25519"),
		APIBase:       apiServer.URL,
		ClientID:      "solian",
		Scope:         "*",
		ExchangeToken: "exchange-secret",
		Purpose:       "termian-hosted",
		Version:       "test",
		Attachments:   "off",
		ChatHistory:   200,
		StargateGRPC:  "bufconn",
	}, gen.NewDyAccountKeyServiceClient(conn), slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatal(err)
	}

	addr := testsession.Listen(t, server)
	client, err := ssh.Dial("tcp", addr, &ssh.ClientConfig{
		User:            "termian",
		Auth:            []ssh.AuthMethod{ssh.PublicKeys(signer)},
		HostKeyCallback: ssh.InsecureIgnoreHostKey(), //nolint:gosec // test host key
	})
	if err != nil {
		t.Fatalf("SSH dial: %v", err)
	}
	defer client.Close()
	session, err := client.NewSession()
	if err != nil {
		t.Fatal(err)
	}
	defer session.Close()
	if err := session.RequestPty("xterm-256color", 40, 120, ssh.TerminalModes{}); err != nil {
		t.Fatal(err)
	}
	if err := session.Shell(); err != nil {
		t.Fatal(err)
	}

	select {
	case path := <-requested:
		if path != "/stargate/auth/me" {
			t.Fatalf("first API call was %s", path)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("TUI did not start an authenticated API call")
	}
}

func TestServerRejectsUnregisteredKey(t *testing.T) {
	_, private, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	signer, err := ssh.NewSignerFromKey(private)
	if err != nil {
		t.Fatal(err)
	}

	lis := bufconn.Listen(1 << 20)
	grpcServer := grpc.NewServer()
	gen.RegisterDyAccountKeyServiceServer(grpcServer, &fakeExchange{publicKey: "not-this-key"})
	go func() { _ = grpcServer.Serve(lis) }()
	t.Cleanup(grpcServer.Stop)

	conn, err := grpc.NewClient("passthrough:///bufconn",
		grpc.WithContextDialer(func(ctx context.Context, _ string) (net.Conn, error) {
			return lis.DialContext(ctx)
		}),
		grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()

	server, err := NewServer(Config{
		Listen:        "127.0.0.1:0",
		HostKeyPath:   filepath.Join(t.TempDir(), "host_ed25519"),
		APIBase:       "http://127.0.0.1:1",
		ClientID:      "solian",
		Scope:         "*",
		ExchangeToken: "exchange-secret",
		Purpose:       "termian-hosted",
		Version:       "test",
	}, gen.NewDyAccountKeyServiceClient(conn), slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatal(err)
	}

	addr := testsession.Listen(t, server)
	if _, err := ssh.Dial("tcp", addr, &ssh.ClientConfig{
		User:            "termian",
		Auth:            []ssh.AuthMethod{ssh.PublicKeys(signer)},
		HostKeyCallback: ssh.InsecureIgnoreHostKey(), //nolint:gosec // test host key
		Timeout:         5 * time.Second,
	}); err == nil {
		t.Fatal("SSH dial succeeded with an unregistered key")
	}
}

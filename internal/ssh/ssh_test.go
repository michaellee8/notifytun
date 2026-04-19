package ssh_test

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/x509"
	"encoding/pem"
	"errors"
	"io"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	tunnelssh "github.com/michaellee8/notifytun/internal/ssh"
	"golang.org/x/crypto/ssh"
	"golang.org/x/crypto/ssh/agent"
	"golang.org/x/crypto/ssh/knownhosts"
)

func TestResolveTargetSimple(t *testing.T) {
	cfg := tunnelssh.ResolveTarget("user@example.com", "", "")
	if cfg.User != "user" || cfg.Host != "example.com" || cfg.Port != "22" {
		t.Fatalf("unexpected config: %+v", cfg)
	}
}

func TestResolveTargetWithPort(t *testing.T) {
	cfg := tunnelssh.ResolveTarget("user@example.com:2222", "", "")
	if cfg.Host != "example.com" || cfg.Port != "2222" {
		t.Fatalf("unexpected config: %+v", cfg)
	}
}

func TestResolveTargetFromSSHConfig(t *testing.T) {
	dir := t.TempDir()
	configPath := filepath.Join(dir, "config")
	err := os.WriteFile(configPath, []byte(`
Host myvm
    HostName 10.0.0.5
    User michael
    Port 2222
    IdentityFile ~/.ssh/id_ed25519
`), 0o600)
	if err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	cfg := tunnelssh.ResolveTarget("myvm", "", configPath)
	if cfg.Host != "10.0.0.5" || cfg.User != "michael" || cfg.Port != "2222" {
		t.Fatalf("unexpected config: %+v", cfg)
	}
	home, err := os.UserHomeDir()
	if err != nil {
		t.Fatalf("UserHomeDir: %v", err)
	}
	wantKeyPath := filepath.Join(home, ".ssh", "id_ed25519")
	if cfg.KeyPath != wantKeyPath {
		t.Fatalf("expected key path %q, got %+v", wantKeyPath, cfg)
	}
}

func TestResolveTargetKeyOverride(t *testing.T) {
	cfg := tunnelssh.ResolveTarget("user@host", "/path/to/key", "")
	if cfg.KeyPath != "/path/to/key" {
		t.Fatalf("expected override key path, got %+v", cfg)
	}
}

func TestConnectFailsWithoutAuthMethods(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("SSH_AUTH_SOCK", "")

	_, err := tunnelssh.Connect(context.Background(), tunnelssh.ConnConfig{
		Host: "example.com",
		Port: "22",
		User: "michael",
	}, "true")
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "no SSH authentication methods available") {
		t.Fatalf("expected auth methods error, got %v", err)
	}
}

func TestConnectFailsWithoutKnownHosts(t *testing.T) {
	home := t.TempDir()
	keyPath := filepath.Join(home, "id_ed25519")
	if err := writeTestPrivateKey(keyPath); err != nil {
		t.Fatalf("writeTestPrivateKey: %v", err)
	}

	t.Setenv("HOME", home)
	t.Setenv("SSH_AUTH_SOCK", "")

	_, err := tunnelssh.Connect(context.Background(), tunnelssh.ConnConfig{
		Host:    "example.com",
		Port:    "22",
		User:    "michael",
		KeyPath: keyPath,
	}, "true")
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "load known_hosts") {
		t.Fatalf("expected known_hosts error, got %v", err)
	}
}

func TestConnectUsesSSHAgentAuth(t *testing.T) {
	home := t.TempDir()
	sshDir := filepath.Join(home, ".ssh")
	if err := os.MkdirAll(sshDir, 0o700); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}

	agentKey := newTestPrivateKey(t)
	agentSock := filepath.Join(t.TempDir(), "agent.sock")
	startTestAgent(t, agentSock, agentKey)

	t.Setenv("HOME", home)
	t.Setenv("SSH_AUTH_SOCK", agentSock)

	hostKey := newTestPrivateKey(t)
	hostSigner, err := ssh.NewSignerFromKey(hostKey)
	if err != nil {
		t.Fatalf("NewSignerFromKey(host): %v", err)
	}
	clientSigner, err := ssh.NewSignerFromKey(agentKey)
	if err != nil {
		t.Fatalf("NewSignerFromKey(client): %v", err)
	}

	addr, serverDone := startTestSSHServer(t, hostSigner, clientSigner.PublicKey())
	knownHostsLine := knownhosts.Line([]string{knownhosts.Normalize(addr)}, hostSigner.PublicKey())
	if err := os.WriteFile(filepath.Join(sshDir, "known_hosts"), []byte(knownHostsLine+"\n"), 0o600); err != nil {
		t.Fatalf("WriteFile(known_hosts): %v", err)
	}

	host, port, err := net.SplitHostPort(addr)
	if err != nil {
		t.Fatalf("SplitHostPort: %v", err)
	}

	sess, err := tunnelssh.Connect(context.Background(), tunnelssh.ConnConfig{
		Host: host,
		Port: port,
		User: "agentuser",
	}, "echo ok")
	if err != nil {
		t.Fatalf("Connect: %v", err)
	}
	defer sess.Close()

	stdout, err := io.ReadAll(sess.Stdout)
	if err != nil {
		t.Fatalf("ReadAll(stdout): %v", err)
	}
	if err := sess.Wait(); err != nil {
		t.Fatalf("Wait: %v", err)
	}
	if got := strings.TrimSpace(string(stdout)); got != "ok" {
		t.Fatalf("expected stdout %q, got %q", "ok", got)
	}
	if err := <-serverDone; err != nil {
		t.Fatalf("server error: %v", err)
	}
}

func TestConnectFallsBackToDefaultKeyWhenConfiguredIdentityFileIsMissing(t *testing.T) {
	home := t.TempDir()
	sshDir := filepath.Join(home, ".ssh")
	if err := os.MkdirAll(sshDir, 0o700); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}

	t.Setenv("HOME", home)
	t.Setenv("SSH_AUTH_SOCK", "")

	defaultKey := newTestPrivateKey(t)
	defaultKeyPath := filepath.Join(sshDir, "id_ed25519")
	if err := writePrivateKey(defaultKeyPath, defaultKey); err != nil {
		t.Fatalf("writePrivateKey(default): %v", err)
	}

	hostKey := newTestPrivateKey(t)
	hostSigner, err := ssh.NewSignerFromKey(hostKey)
	if err != nil {
		t.Fatalf("NewSignerFromKey(host): %v", err)
	}
	clientSigner, err := ssh.NewSignerFromKey(defaultKey)
	if err != nil {
		t.Fatalf("NewSignerFromKey(client): %v", err)
	}

	addr, serverDone := startTestSSHServer(t, hostSigner, clientSigner.PublicKey())
	if err := writeKnownHostsFile(filepath.Join(sshDir, "known_hosts"), addr, hostSigner.PublicKey()); err != nil {
		t.Fatalf("writeKnownHostsFile: %v", err)
	}

	host, port, err := net.SplitHostPort(addr)
	if err != nil {
		t.Fatalf("SplitHostPort: %v", err)
	}

	configPath := filepath.Join(t.TempDir(), "config")
	config := "\nHost fallbackvm\n    HostName " + host + "\n    User agentuser\n    Port " + port + "\n    IdentityFile ~/.ssh/missing_identity\n"
	if err := os.WriteFile(configPath, []byte(config), 0o600); err != nil {
		t.Fatalf("WriteFile(config): %v", err)
	}

	cfg := tunnelssh.ResolveTarget("fallbackvm", "", configPath)
	sess, err := tunnelssh.Connect(context.Background(), cfg, "echo ok")
	if err != nil {
		t.Fatalf("Connect: %v", err)
	}
	defer sess.Close()

	stdout, err := io.ReadAll(sess.Stdout)
	if err != nil {
		t.Fatalf("ReadAll(stdout): %v", err)
	}
	if err := sess.Wait(); err != nil {
		t.Fatalf("Wait: %v", err)
	}
	if got := strings.TrimSpace(string(stdout)); got != "ok" {
		t.Fatalf("expected stdout %q, got %q", "ok", got)
	}
	if err := <-serverDone; err != nil {
		t.Fatalf("server error: %v", err)
	}
}

func TestConnectFailsWhenExplicitOverrideKeyIsMissing(t *testing.T) {
	home := t.TempDir()
	sshDir := filepath.Join(home, ".ssh")
	if err := os.MkdirAll(sshDir, 0o700); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}

	t.Setenv("HOME", home)
	t.Setenv("SSH_AUTH_SOCK", "")

	defaultKeyPath := filepath.Join(sshDir, "id_ed25519")
	if err := writePrivateKey(defaultKeyPath, newTestPrivateKey(t)); err != nil {
		t.Fatalf("writePrivateKey(default): %v", err)
	}
	if err := os.WriteFile(filepath.Join(sshDir, "known_hosts"), nil, 0o600); err != nil {
		t.Fatalf("WriteFile(known_hosts): %v", err)
	}

	missingOverride := filepath.Join(t.TempDir(), "missing_override")
	cfg := tunnelssh.ResolveTarget("agentuser@127.0.0.1:65534", missingOverride, "")

	_, err := tunnelssh.Connect(context.Background(), cfg, "echo ok")
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "build auth methods") {
		t.Fatalf("expected build auth methods error, got %v", err)
	}
	if !strings.Contains(err.Error(), missingOverride) {
		t.Fatalf("expected explicit override path in error, got %v", err)
	}
}

func TestConnectCancelsDuringHandshake(t *testing.T) {
	home := t.TempDir()
	sshDir := filepath.Join(home, ".ssh")
	if err := os.MkdirAll(sshDir, 0o700); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}

	t.Setenv("HOME", home)
	t.Setenv("SSH_AUTH_SOCK", "")

	keyPath := filepath.Join(t.TempDir(), "id_ed25519")
	if err := writeTestPrivateKey(keyPath); err != nil {
		t.Fatalf("writeTestPrivateKey: %v", err)
	}
	if err := os.WriteFile(filepath.Join(sshDir, "known_hosts"), nil, 0o600); err != nil {
		t.Fatalf("WriteFile(known_hosts): %v", err)
	}

	addr, serverDone := startStalledHandshakeServer(t, 300*time.Millisecond)
	host, port, err := net.SplitHostPort(addr)
	if err != nil {
		t.Fatalf("SplitHostPort: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()

	start := time.Now()
	_, err = tunnelssh.Connect(ctx, tunnelssh.ConnConfig{
		Host:    host,
		Port:    port,
		User:    "canceluser",
		KeyPath: keyPath,
	}, "echo ok")
	elapsed := time.Since(start)
	if err == nil {
		t.Fatal("expected error")
	}
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("expected context deadline exceeded, got %v", err)
	}
	if elapsed >= 250*time.Millisecond {
		t.Fatalf("expected cancellation before stalled handshake released, took %s", elapsed)
	}
	if err := <-serverDone; err != nil {
		t.Fatalf("server error: %v", err)
	}
}

func TestConnectCancelsDuringRemoteCommandStart(t *testing.T) {
	home := t.TempDir()
	sshDir := filepath.Join(home, ".ssh")
	if err := os.MkdirAll(sshDir, 0o700); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}

	t.Setenv("HOME", home)
	t.Setenv("SSH_AUTH_SOCK", "")

	clientKey := newTestPrivateKey(t)
	keyPath := filepath.Join(t.TempDir(), "id_ed25519")
	if err := writePrivateKey(keyPath, clientKey); err != nil {
		t.Fatalf("writePrivateKey: %v", err)
	}

	hostKey := newTestPrivateKey(t)
	hostSigner, err := ssh.NewSignerFromKey(hostKey)
	if err != nil {
		t.Fatalf("NewSignerFromKey(host): %v", err)
	}
	clientSigner, err := ssh.NewSignerFromKey(clientKey)
	if err != nil {
		t.Fatalf("NewSignerFromKey(client): %v", err)
	}

	addr, serverDone := startTestSSHServerWithHandler(t, hostSigner, clientSigner.PublicKey(), handleDelayedExec(300*time.Millisecond))
	if err := writeKnownHostsFile(filepath.Join(sshDir, "known_hosts"), addr, hostSigner.PublicKey()); err != nil {
		t.Fatalf("writeKnownHostsFile: %v", err)
	}

	host, port, err := net.SplitHostPort(addr)
	if err != nil {
		t.Fatalf("SplitHostPort: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()

	start := time.Now()
	sess, err := tunnelssh.Connect(ctx, tunnelssh.ConnConfig{
		Host:    host,
		Port:    port,
		User:    "agentuser",
		KeyPath: keyPath,
	}, "echo ok")
	elapsed := time.Since(start)
	if sess != nil {
		_ = sess.Close()
	}
	if err == nil {
		t.Fatal("expected error")
	}
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("expected context deadline exceeded, got %v", err)
	}
	if elapsed >= 250*time.Millisecond {
		t.Fatalf("expected cancellation before delayed exec reply, took %s", elapsed)
	}
	if err := <-serverDone; err != nil {
		t.Fatalf("server error: %v", err)
	}
}

func writeTestPrivateKey(path string) error {
	return writePrivateKey(path, newTestPrivateKey(nil))
}

func newTestPrivateKey(t *testing.T) ed25519.PrivateKey {
	if t != nil {
		t.Helper()
	}

	_, privateKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		if t != nil {
			t.Fatalf("GenerateKey: %v", err)
		}
		panic(err)
	}

	return privateKey
}

func startTestAgent(t *testing.T, socketPath string, key ed25519.PrivateKey) {
	t.Helper()

	keyring := agent.NewKeyring()
	if err := keyring.Add(agent.AddedKey{PrivateKey: key}); err != nil {
		t.Fatalf("agent add key: %v", err)
	}

	listener, err := net.Listen("unix", socketPath)
	if err != nil {
		t.Fatalf("listen agent socket: %v", err)
	}

	t.Cleanup(func() {
		_ = listener.Close()
	})

	go func() {
		for {
			conn, err := listener.Accept()
			if err != nil {
				return
			}

			go func() {
				defer conn.Close()
				_ = agent.ServeAgent(keyring, conn)
			}()
		}
	}()
}

func startTestSSHServer(t *testing.T, hostSigner ssh.Signer, authorizedKey ssh.PublicKey) (string, <-chan error) {
	return startTestSSHServerWithHandler(t, hostSigner, authorizedKey, handleSession)
}

func startTestSSHServerWithHandler(t *testing.T, hostSigner ssh.Signer, authorizedKey ssh.PublicKey, handler func(ssh.Channel, <-chan *ssh.Request) error) (string, <-chan error) {
	t.Helper()

	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen ssh server: %v", err)
	}

	done := make(chan error, 1)
	go func() {
		defer close(done)
		defer listener.Close()

		conn, err := listener.Accept()
		if err != nil {
			if errors.Is(err, net.ErrClosed) {
				done <- nil
				return
			}
			done <- err
			return
		}
		defer conn.Close()

		serverConfig := &ssh.ServerConfig{
			PublicKeyCallback: func(connMeta ssh.ConnMetadata, key ssh.PublicKey) (*ssh.Permissions, error) {
				if connMeta.User() != "agentuser" {
					return nil, errors.New("unexpected user")
				}
				if !bytes.Equal(key.Marshal(), authorizedKey.Marshal()) {
					return nil, errors.New("unauthorized key")
				}
				return nil, nil
			},
		}
		serverConfig.AddHostKey(hostSigner)

		sshConn, chans, reqs, err := ssh.NewServerConn(conn, serverConfig)
		if err != nil {
			done <- err
			return
		}
		defer sshConn.Close()

		go ssh.DiscardRequests(reqs)

		for newChannel := range chans {
			if newChannel.ChannelType() != "session" {
				_ = newChannel.Reject(ssh.UnknownChannelType, "unsupported channel type")
				continue
			}

			channel, requests, err := newChannel.Accept()
			if err != nil {
				done <- err
				return
			}

			if err := handler(channel, requests); err != nil {
				done <- err
				return
			}

			done <- nil
			return
		}

		done <- nil
	}()

	return listener.Addr().String(), done
}

func startStalledHandshakeServer(t *testing.T, delay time.Duration) (string, <-chan error) {
	t.Helper()

	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen stalled ssh server: %v", err)
	}

	done := make(chan error, 1)
	go func() {
		defer close(done)
		defer listener.Close()

		conn, err := listener.Accept()
		if err != nil {
			if errors.Is(err, net.ErrClosed) {
				done <- nil
				return
			}
			done <- err
			return
		}
		defer conn.Close()

		time.Sleep(delay)
		done <- nil
	}()

	return listener.Addr().String(), done
}

func handleDelayedExec(delay time.Duration) func(ssh.Channel, <-chan *ssh.Request) error {
	return func(channel ssh.Channel, requests <-chan *ssh.Request) error {
		defer channel.Close()

		for req := range requests {
			switch req.Type {
			case "exec":
				time.Sleep(delay)
				if err := req.Reply(true, nil); err != nil {
					return nil
				}
				return nil
			default:
				if err := req.Reply(false, nil); err != nil {
					return err
				}
			}
		}

		return nil
	}
}

func writePrivateKey(path string, privateKey ed25519.PrivateKey) error {
	keyBytes, err := x509.MarshalPKCS8PrivateKey(privateKey)
	if err != nil {
		return err
	}

	block := &pem.Block{
		Type:  "PRIVATE KEY",
		Bytes: keyBytes,
	}

	return os.WriteFile(path, pem.EncodeToMemory(block), 0o600)
}

func writeKnownHostsFile(path, addr string, publicKey ssh.PublicKey) error {
	line := knownhosts.Line([]string{knownhosts.Normalize(addr)}, publicKey)
	return os.WriteFile(path, []byte(line+"\n"), 0o600)
}

func handleSession(channel ssh.Channel, requests <-chan *ssh.Request) error {
	defer channel.Close()

	for req := range requests {
		switch req.Type {
		case "exec":
			if err := req.Reply(true, nil); err != nil {
				return err
			}
			if _, err := channel.Write([]byte("ok\n")); err != nil {
				return err
			}
			if _, err := channel.SendRequest("exit-status", false, ssh.Marshal(struct{ Status uint32 }{Status: 0})); err != nil {
				return err
			}
			return nil
		default:
			if err := req.Reply(false, nil); err != nil {
				return err
			}
		}
	}

	return nil
}

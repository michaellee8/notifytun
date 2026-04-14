package ssh

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	sshconfig "github.com/kevinburke/ssh_config"
	gossh "golang.org/x/crypto/ssh"
	"golang.org/x/crypto/ssh/agent"
	"golang.org/x/crypto/ssh/knownhosts"
)

const (
	defaultSSHPort = "22"
	connectTimeout = 10 * time.Second
	initialBackoff = 1 * time.Second
	maxBackoff     = 30 * time.Second
)

// ConnConfig holds resolved SSH connection parameters.
type ConnConfig struct {
	Host    string
	Port    string
	User    string
	KeyPath string
}

// ResolveTarget resolves a target string (user@host[:port], host alias) into connection config.
func ResolveTarget(target, sshKeyOverride, sshConfigPath string) ConnConfig {
	cfg := ConnConfig{Port: defaultSSHPort}

	if idx := strings.Index(target, "@"); idx >= 0 {
		cfg.User = target[:idx]
		target = target[idx+1:]
	}

	if host, port, ok := splitTargetHostPort(target); ok {
		cfg.Host = host
		cfg.Port = port
	} else {
		cfg.Host = target
	}

	alias := cfg.Host
	if parsed, err := readSSHConfig(sshConfigPath); err == nil && parsed != nil {
		if hostname, err := parsed.Get(alias, "HostName"); err == nil && hostname != "" {
			cfg.Host = hostname
		}
		if user, err := parsed.Get(alias, "User"); err == nil && user != "" && cfg.User == "" {
			cfg.User = user
		}
		if port, err := parsed.Get(alias, "Port"); err == nil && port != "" && cfg.Port == defaultSSHPort {
			cfg.Port = port
		}
		if keyPath, err := parsed.Get(alias, "IdentityFile"); err == nil && keyPath != "" && sshKeyOverride == "" {
			cfg.KeyPath = expandHomePath(keyPath)
		}
	}

	if sshKeyOverride != "" {
		cfg.KeyPath = expandHomePath(sshKeyOverride)
	}

	return cfg
}

// Session represents an active SSH session running a remote command.
type Session struct {
	client  *gossh.Client
	session *gossh.Session
	Stdout  io.Reader
	Stderr  io.Reader
}

// Connect establishes an SSH connection and starts a remote command.
func Connect(ctx context.Context, cfg ConnConfig, remoteCmd string) (*Session, error) {
	authMethods, authCleanup, err := buildAuthMethods(cfg.KeyPath)
	if err != nil {
		return nil, fmt.Errorf("build auth methods: %w", err)
	}
	defer authCleanup()

	if len(authMethods) == 0 {
		return nil, fmt.Errorf("no SSH authentication methods available")
	}

	hostKeyCallback, err := loadKnownHosts()
	if err != nil {
		return nil, fmt.Errorf("load known_hosts: %w", err)
	}

	sshCfg := &gossh.ClientConfig{
		User:            cfg.User,
		Auth:            authMethods,
		HostKeyCallback: hostKeyCallback,
		Timeout:         connectTimeout,
	}

	addr := net.JoinHostPort(cfg.Host, cfg.Port)
	dialer := &net.Dialer{Timeout: connectTimeout}
	conn, err := dialer.DialContext(ctx, "tcp", addr)
	if err != nil {
		return nil, fmt.Errorf("ssh dial %s: %w", addr, err)
	}

	clientConn, chans, reqs, err := gossh.NewClientConn(conn, addr, sshCfg)
	if err != nil {
		_ = conn.Close()
		return nil, fmt.Errorf("ssh dial %s: %w", addr, err)
	}

	client := gossh.NewClient(clientConn, chans, reqs)
	session, err := client.NewSession()
	if err != nil {
		_ = client.Close()
		return nil, fmt.Errorf("new session: %w", err)
	}

	stdout, err := session.StdoutPipe()
	if err != nil {
		_ = session.Close()
		_ = client.Close()
		return nil, fmt.Errorf("stdout pipe: %w", err)
	}

	stderr, err := session.StderrPipe()
	if err != nil {
		_ = session.Close()
		_ = client.Close()
		return nil, fmt.Errorf("stderr pipe: %w", err)
	}

	if err := session.Start(remoteCmd); err != nil {
		_ = session.Close()
		_ = client.Close()
		return nil, fmt.Errorf("start remote command: %w", err)
	}

	return &Session{
		client:  client,
		session: session,
		Stdout:  stdout,
		Stderr:  stderr,
	}, nil
}

// Wait blocks until the remote command exits.
func (s *Session) Wait() error {
	if s == nil || s.session == nil {
		return nil
	}
	return s.session.Wait()
}

// Close closes the SSH session and underlying client connection.
func (s *Session) Close() error {
	if s == nil {
		return nil
	}

	var sessionErr error
	if s.session != nil {
		sessionErr = s.session.Close()
	}

	var clientErr error
	if s.client != nil {
		clientErr = s.client.Close()
	}

	return errors.Join(sessionErr, clientErr)
}

// Backoff implements exponential reconnect backoff capped at 30 seconds.
type Backoff struct {
	current time.Duration
}

// NewBackoff creates a backoff starting at 1 second.
func NewBackoff() *Backoff {
	return &Backoff{current: initialBackoff}
}

// Next returns the current backoff and advances the sequence.
func (b *Backoff) Next() time.Duration {
	delay := b.current
	b.current *= 2
	if b.current > maxBackoff {
		b.current = maxBackoff
	}
	return delay
}

// Reset returns the backoff to its initial value.
func (b *Backoff) Reset() {
	b.current = initialBackoff
}

func buildAuthMethods(keyPath string) ([]gossh.AuthMethod, func(), error) {
	var methods []gossh.AuthMethod
	var cleanupFns []func()

	if keyPath != "" {
		signer, err := loadSignerFromFile(keyPath)
		if err != nil {
			return nil, func() {}, err
		}
		methods = append(methods, gossh.PublicKeys(signer))
	}

	if agentMethod, cleanup, err := loadAgentAuthMethod(); err == nil && agentMethod != nil {
		methods = append(methods, agentMethod)
		cleanupFns = append(cleanupFns, cleanup)
	}

	if keyPath == "" {
		for _, path := range defaultKeyPaths() {
			signer, err := loadSignerFromFile(path)
			if err != nil {
				continue
			}
			methods = append(methods, gossh.PublicKeys(signer))
		}
	}

	cleanup := func() {
		for i := len(cleanupFns) - 1; i >= 0; i-- {
			if cleanupFns[i] != nil {
				cleanupFns[i]()
			}
		}
	}

	return methods, cleanup, nil
}

func loadKnownHosts() (gossh.HostKeyCallback, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return nil, err
	}

	knownHostsPath := filepath.Join(home, ".ssh", "known_hosts")
	if _, err := os.Stat(knownHostsPath); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, fmt.Errorf("no known_hosts file found at %s - connect via regular ssh first", knownHostsPath)
		}
		return nil, err
	}

	return knownhosts.New(knownHostsPath)
}

func readSSHConfig(sshConfigPath string) (*sshconfig.Config, error) {
	var configPath string
	if sshConfigPath != "" {
		configPath = sshConfigPath
	} else {
		home, err := os.UserHomeDir()
		if err != nil {
			return nil, err
		}
		configPath = filepath.Join(home, ".ssh", "config")
	}

	file, err := os.Open(configPath)
	if err != nil {
		return nil, err
	}
	defer file.Close()

	return sshconfig.Decode(file)
}

func expandHomePath(path string) string {
	if !strings.HasPrefix(path, "~/") {
		return path
	}

	home, err := os.UserHomeDir()
	if err != nil {
		return path
	}

	return filepath.Join(home, path[2:])
}

func splitTargetHostPort(target string) (string, string, bool) {
	if strings.HasPrefix(target, "[") {
		host, port, err := net.SplitHostPort(target)
		if err == nil {
			return host, port, true
		}
	}

	if strings.Count(target, ":") != 1 {
		return "", "", false
	}

	idx := strings.LastIndex(target, ":")
	if idx <= 0 || idx == len(target)-1 {
		return "", "", false
	}

	port := target[idx+1:]
	if _, err := strconv.Atoi(port); err != nil {
		return "", "", false
	}

	return target[:idx], port, true
}

func loadSignerFromFile(path string) (gossh.Signer, error) {
	key, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read key file %s: %w", path, err)
	}

	signer, err := gossh.ParsePrivateKey(key)
	if err != nil {
		return nil, fmt.Errorf("parse key file %s: %w", path, err)
	}

	return signer, nil
}

func loadAgentAuthMethod() (gossh.AuthMethod, func(), error) {
	agentSock := os.Getenv("SSH_AUTH_SOCK")
	if agentSock == "" {
		return nil, func() {}, nil
	}

	conn, err := net.Dial("unix", agentSock)
	if err != nil {
		return nil, func() {}, err
	}

	agentClient := agent.NewClient(conn)
	cleanup := func() {
		_ = conn.Close()
	}

	return gossh.PublicKeysCallback(agentClient.Signers), cleanup, nil
}

func defaultKeyPaths() []string {
	home, err := os.UserHomeDir()
	if err != nil {
		return nil
	}

	return []string{
		filepath.Join(home, ".ssh", "id_ed25519"),
		filepath.Join(home, ".ssh", "id_rsa"),
		filepath.Join(home, ".ssh", "id_ecdsa"),
	}
}

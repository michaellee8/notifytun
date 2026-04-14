package ssh_test

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/x509"
	"encoding/pem"
	"os"
	"path/filepath"
	"strings"
	"testing"

	tunnelssh "github.com/michaellee8/notifytun/internal/ssh"
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

func TestBackoffSequence(t *testing.T) {
	b := tunnelssh.NewBackoff()
	expected := []int{1, 2, 4, 8, 16, 30, 30, 30}

	for i, want := range expected {
		if got := int(b.Next().Seconds()); got != want {
			t.Fatalf("attempt %d: expected %ds, got %ds", i+1, want, got)
		}
	}
}

func TestBackoffReset(t *testing.T) {
	b := tunnelssh.NewBackoff()
	_ = b.Next()
	_ = b.Next()
	_ = b.Next()
	b.Reset()

	if got := int(b.Next().Seconds()); got != 1 {
		t.Fatalf("expected reset backoff to 1s, got %ds", got)
	}
}

func writeTestPrivateKey(path string) error {
	_, privateKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return err
	}

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

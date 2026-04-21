# Beeep Notifier Migration Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Replace notifytun's current macOS/Linux local notification code with a single `beeep`-backed native notifier, keep `generic` as the custom-command escape hatch, and update the docs to match the new backend contract.

**Architecture:** `internal/notifier` collapses to two backends: `auto` returns a `Beeep` notifier that delegates to `github.com/gen2brain/beeep`, and `generic` keeps the existing stdin-JSON command runner. CLI commands and docs stop advertising `macos` and `linux`; they only expose `auto` and `generic`. Verification includes the Linux test suite and a Windows cross-build.

**Tech Stack:** Go 1.25.5, `github.com/gen2brain/beeep` v0.11.2, Cobra, Viper, stdlib `context`, `os/exec`, and cross-compilation with `go build`.

**Spec:** [docs/superpowers/specs/2026-04-21-beeep-notifier-design.md](../specs/2026-04-21-beeep-notifier-design.md)

---

## File plan

Created:
- `internal/notifier/beeep.go` — native notifier wrapper over `beeep`.
- `internal/notifier/beeep_test.go` — internal unit tests for the `beeep` forwarding shim.

Rewritten:
- `internal/notifier/notifier.go` — backend selection reduced to `auto` and `generic`.
- `internal/notifier/notifier_test.go` — factory-level tests for the new backend contract.

Modified:
- `go.mod` — add direct dependency on `github.com/gen2brain/beeep`.
- `go.sum` — add `beeep` and its transitive module checksums via `go mod tidy`.
- `internal/cli/local.go` — help string updated to advertise only `auto` and `generic`.
- `internal/cli/testnotify.go` — help string updated to advertise only `auto` and `generic`.
- `internal/cli/local_test.go` — add help-text coverage and replace stale `linux` fixture values.
- `internal/cli/testnotify_test.go` — add help-text coverage for the new backend surface.
- `README.md` — rewrite the local backend support story, requirements, config docs, and troubleshooting.
- `config.example.toml` — document only `auto` and `generic`.
- `docs/superpowers/specs/2026-04-14-notifytun-design.md` — add a short superseded note for the notifier backend design.

Deleted:
- `internal/notifier/macos.go`
- `internal/notifier/linux.go`

---

### Task 1: Replace the native notifier implementation with `beeep`

**Files:**
- Create: `internal/notifier/beeep.go`
- Create: `internal/notifier/beeep_test.go`
- Rewrite: `internal/notifier/notifier.go`
- Rewrite: `internal/notifier/notifier_test.go`
- Modify: `go.mod`
- Modify: `go.sum`
- Delete: `internal/notifier/macos.go`
- Delete: `internal/notifier/linux.go`

- [ ] **Step 1: Rewrite `internal/notifier/notifier_test.go` with the new backend-contract tests**

```go
package notifier_test

import (
	"reflect"
	"strings"
	"testing"

	"github.com/michaellee8/notifytun/internal/notifier"
)

func TestNewAutoReturnsBeeepNotifier(t *testing.T) {
	n, err := notifier.New("auto", "")
	if err != nil {
		t.Fatalf("New auto: %v", err)
	}

	if got := reflect.TypeOf(n).String(); got != "*notifier.Beeep" {
		t.Fatalf("expected *notifier.Beeep, got %s", got)
	}
}

func TestNewGenericRequiresCmd(t *testing.T) {
	if _, err := notifier.New("generic", ""); err == nil {
		t.Fatal("expected error when generic backend has no notify-cmd")
	}
}

func TestNewGenericReturnsGenericNotifier(t *testing.T) {
	n, err := notifier.New("generic", "echo")
	if err != nil {
		t.Fatalf("New generic: %v", err)
	}

	if got := reflect.TypeOf(n).String(); got != "*notifier.Generic" {
		t.Fatalf("expected *notifier.Generic, got %s", got)
	}
}

func TestNewRejectsRemovedAndUnknownBackends(t *testing.T) {
	for _, backend := range []string{"macos", "linux", "bogus"} {
		_, err := notifier.New(backend, "")
		if err == nil {
			t.Fatalf("expected error for backend %q", backend)
		}
		if !strings.Contains(err.Error(), "unknown backend: "+backend) {
			t.Fatalf("unexpected error for %q: %v", backend, err)
		}
	}
}
```

- [ ] **Step 2: Create `internal/notifier/beeep_test.go` with a failing forwarding test**

```go
package notifier

import (
	"context"
	"errors"
	"testing"

	beeep "github.com/gen2brain/beeep"
)

func TestBeeepNotifyForwardsFields(t *testing.T) {
	prevNotify := beeepNotify
	prevAppName := beeep.AppName
	t.Cleanup(func() {
		beeepNotify = prevNotify
		beeep.AppName = prevAppName
	})

	var gotTitle string
	var gotBody string
	var gotIcon any

	beeepNotify = func(title, body string, icon any) error {
		gotTitle = title
		gotBody = body
		gotIcon = icon
		return nil
	}

	err := NewBeeep().Notify(context.Background(), Notification{
		Title: "Build passed",
		Body:  "All tests are green",
		Tool:  "codex",
	})
	if err != nil {
		t.Fatalf("Notify: %v", err)
	}

	if beeep.AppName != "notifytun" {
		t.Fatalf("expected beeep.AppName to be notifytun, got %q", beeep.AppName)
	}
	if gotTitle != "Build passed" {
		t.Fatalf("expected title to be forwarded, got %q", gotTitle)
	}
	if gotBody != "All tests are green" {
		t.Fatalf("expected body to be forwarded, got %q", gotBody)
	}
	if gotIcon != "" {
		t.Fatalf("expected empty icon, got %#v", gotIcon)
	}
}

func TestBeeepNotifyReturnsUnderlyingError(t *testing.T) {
	prevNotify := beeepNotify
	t.Cleanup(func() {
		beeepNotify = prevNotify
	})

	want := errors.New("boom")
	beeepNotify = func(title, body string, icon any) error {
		return want
	}

	err := NewBeeep().Notify(context.Background(), Notification{
		Title: "Build passed",
		Body:  "All tests are green",
	})
	if !errors.Is(err, want) {
		t.Fatalf("expected wrapped beeep error %v, got %v", want, err)
	}
}
```

- [ ] **Step 3: Run the notifier tests to verify they fail for the right reason**

Run: `go test ./internal/notifier -run 'Test(NewAutoReturnsBeeepNotifier|NewGenericReturnsGenericNotifier|NewRejectsRemovedAndUnknownBackends|BeeepNotify)'`

Expected: FAIL. `TestNewAutoReturnsBeeepNotifier` should fail because `auto` still returns the old platform-specific backend, and `beeep_test.go` should fail to compile because `Beeep`, `NewBeeep`, and `beeepNotify` do not exist yet.

- [ ] **Step 4: Add the `beeep` dependency**

Run: `go get github.com/gen2brain/beeep@v0.11.2`

Run: `go mod tidy`

Expected: `go.mod` gains a direct `github.com/gen2brain/beeep v0.11.2` requirement and `go.sum` gains the transitive checksums needed by `beeep`.

- [ ] **Step 5: Create `internal/notifier/beeep.go`**

```go
package notifier

import (
	"context"

	beeep "github.com/gen2brain/beeep"
)

const beeepAppName = "notifytun"

var beeepNotify = beeep.Notify

// Beeep delivers notifications through github.com/gen2brain/beeep.
type Beeep struct{}

// NewBeeep creates a native cross-platform notifier backed by beeep.
func NewBeeep() *Beeep {
	return &Beeep{}
}

// Notify sends the notification through beeep.
func (b *Beeep) Notify(ctx context.Context, n Notification) error {
	_ = ctx
	beeep.AppName = beeepAppName
	return beeepNotify(n.Title, n.Body, "")
}
```

- [ ] **Step 6: Rewrite `internal/notifier/notifier.go` and delete the old native backends**

Replace `internal/notifier/notifier.go` with:

```go
package notifier

import (
	"context"
	"fmt"
	"io"
)

// Notification is the payload delivered to the local notification backend.
type Notification struct {
	Title string
	Body  string
	Tool  string
}

// Notifier delivers a notification to the local machine.
type Notifier interface {
	Notify(ctx context.Context, n Notification) error
}

// CommandOutputConfigurer lets notifiers that shell out expose the underlying
// command's stdout and stderr to caller-provided writers.
type CommandOutputConfigurer interface {
	SetCommandOutput(stdout, stderr io.Writer)
}

// New builds a notifier for the selected backend.
func New(backend, notifyCmd string) (Notifier, error) {
	switch backend {
	case "auto":
		return NewBeeep(), nil
	case "generic":
		if notifyCmd == "" {
			return nil, fmt.Errorf("--notify-cmd is required for generic backend")
		}
		return NewGeneric(notifyCmd)
	default:
		return nil, fmt.Errorf("unknown backend: %s", backend)
	}
}
```

Delete these files completely:

- `internal/notifier/macos.go`
- `internal/notifier/linux.go`

- [ ] **Step 7: Run the notifier tests and then the full suite**

Run: `go test ./internal/notifier`

Expected: PASS. The new `Beeep` notifier tests and the new backend-constructor tests should all pass.

Run: `go test ./...`

Expected: PASS. The rest of the repo should still build and pass on Linux after the notifier swap.

- [ ] **Step 8: Cross-build the Windows binary to verify the new native path compiles**

Run: `GOOS=windows GOARCH=amd64 go build -o /tmp/notifytun.exe ./cmd/notifytun`

Expected: success with no compiler errors. `/tmp/notifytun.exe` exists and the repo worktree stays clean because the artifact is outside the repo root.

- [ ] **Step 9: Commit**

```bash
git add go.mod go.sum internal/notifier/beeep.go internal/notifier/beeep_test.go internal/notifier/notifier.go internal/notifier/notifier_test.go
git rm internal/notifier/macos.go internal/notifier/linux.go
git commit -m "feat(notifier): replace native backends with beeep"
```

---

### Task 2: Update the CLI backend contract to advertise only `auto` and `generic`

**Files:**
- Modify: `internal/cli/local.go`
- Modify: `internal/cli/testnotify.go`
- Modify: `internal/cli/local_test.go`
- Modify: `internal/cli/testnotify_test.go`

- [ ] **Step 1: Add failing help-text tests for the `local` and `test-notify` commands**

Append this test to `internal/cli/local_test.go`:

```go
func TestLocalCmdHelpListsSupportedBackends(t *testing.T) {
	cmd := NewLocalCmd()

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	cmd.SetOut(&stdout)
	cmd.SetErr(&stderr)
	cmd.SetArgs([]string{"--help"})

	if err := cmd.Execute(); err != nil {
		t.Fatalf("Execute: %v", err)
	}

	help := stdout.String() + stderr.String()
	if !strings.Contains(help, "Notifier backend: auto, generic") {
		t.Fatalf("expected help to advertise auto/generic backends, got %q", help)
	}
	if strings.Contains(help, "macos") || strings.Contains(help, "linux") {
		t.Fatalf("expected help to omit removed backends, got %q", help)
	}
}
```

At the top of `internal/cli/local_test.go`, extend the import block to include:

```go
import (
	"bytes"
```

Append this test to `internal/cli/testnotify_test.go`:

```go
func TestTestNotifyHelpListsSupportedBackends(t *testing.T) {
	cmd := cli.NewTestNotifyCmd()

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	cmd.SetOut(&stdout)
	cmd.SetErr(&stderr)
	cmd.SetArgs([]string{"--help"})

	if err := cmd.Execute(); err != nil {
		t.Fatalf("Execute: %v", err)
	}

	help := stdout.String() + stderr.String()
	if !strings.Contains(help, "Notifier backend: auto, generic") {
		t.Fatalf("expected help to advertise auto/generic backends, got %q", help)
	}
	if strings.Contains(help, "macos") || strings.Contains(help, "linux") {
		t.Fatalf("expected help to omit removed backends, got %q", help)
	}
}
```

- [ ] **Step 2: Run the CLI tests to verify the help-text assertions fail**

Run: `go test ./internal/cli -run 'Test(LocalCmdHelpListsSupportedBackends|TestTestNotifyHelpListsSupportedBackends)'`

Expected: FAIL because both commands still say `Notifier backend: auto, macos, linux, generic`.

- [ ] **Step 3: Update the help strings and stale test fixtures**

In `internal/cli/local.go`, change:

```go
cmd.Flags().StringVar(&opts.backend, "backend", opts.backend, "Notifier backend: auto, macos, linux, generic")
```

to:

```go
cmd.Flags().StringVar(&opts.backend, "backend", opts.backend, "Notifier backend: auto, generic")
```

In `internal/cli/testnotify.go`, change:

```go
cmd.Flags().StringVar(&backend, "backend", "auto", "Notifier backend: auto, macos, linux, generic")
```

to:

```go
cmd.Flags().StringVar(&backend, "backend", "auto", "Notifier backend: auto, generic")
```

In `internal/cli/local_test.go`, update the stale removed-backend fixture in `TestLocalOptionsPreserveExplicitFlags`:

```go
opts := localOptions{
	target:       "flag@example.com",
	remoteBin:    "notifytun-custom",
	backend:      "generic",
	notifyCmd:    "printf hi",
	configFile:   configPath,
	targetSet:    true,
	remoteBinSet: true,
	backendSet:   true,
	notifyCmdSet: true,
}
```

and change the expectation from:

```go
if opts.backend != "linux" {
	t.Fatalf("expected explicit backend to win, got %q", opts.backend)
}
```

to:

```go
if opts.backend != "generic" {
	t.Fatalf("expected explicit backend to win, got %q", opts.backend)
}
```

- [ ] **Step 4: Run the CLI package tests**

Run: `go test ./internal/cli`

Expected: PASS. Existing generic-command tests still pass, and the new help-text assertions should now pass too.

- [ ] **Step 5: Commit**

```bash
git add internal/cli/local.go internal/cli/local_test.go internal/cli/testnotify.go internal/cli/testnotify_test.go
git commit -m "refactor(cli): drop removed native backend names"
```

---

### Task 3: Rewrite the docs around `auto` + `generic` and add the historical spec note

**Files:**
- Modify: `README.md`
- Modify: `config.example.toml`
- Modify: `docs/superpowers/specs/2026-04-14-notifytun-design.md`

- [ ] **Step 1: Update the README backend story and troubleshooting**

Edit the top feature bullets in `README.md` to:

```md
- Native cross-platform local notifications via `beeep`
- Generic command backend when you want full control over delivery
```

Replace the local requirements subsection:

```md
- A local machine running:
  - macOS for the `macos` backend
  - Linux with `notify-send` available for the `linux` backend
  - any platform with `--backend generic --notify-cmd ...`
```

with:

```md
- A local machine with a desktop notification environment supported by `beeep`
  (macOS, Linux, or Windows), or any platform where you prefer
  `--backend generic --notify-cmd ...`
```

Update the backend/config docs to say:

```md
- `local.backend`: `auto` or `generic`
- `local.notify-cmd`: command used by the `generic` backend
```

and:

```md
- `--backend`: `auto`, `generic`
- `--notify-cmd`: required for `generic`
```

Replace the troubleshooting section:

```md
### Linux notifications do not appear

The `linux` backend uses `notify-send`. Install it or switch to `--backend generic --notify-cmd ...`.
```

with:

```md
### Native notifications do not appear

Run `notifytun test-notify --backend auto` to verify the native path on your machine.
If native delivery still fails, switch to `--backend generic --notify-cmd ...`
to use a notifier command you control directly.
```

Also update the `--notify-cmd is required` bullets so they only mention:

```md
- you selected `--backend generic` without a command
```

- [ ] **Step 2: Update `config.example.toml` and add the superseded note to the old 2026-04-14 spec**

In `config.example.toml`, change:

```toml
# Notifier backend: auto, macos, linux, generic
# auto detects based on OS
# backend = "auto"
```

to:

```toml
# Notifier backend: auto, generic
# auto uses the native beeep-backed notifier
# backend = "auto"
```

At the top of `docs/superpowers/specs/2026-04-14-notifytun-design.md`, directly under the existing 2026-04-20 note, add:

```md
> **Note (2026-04-21):** The local notifier backend design in this document has been superseded by [2026-04-21-beeep-notifier-design.md](2026-04-21-beeep-notifier-design.md).
```

- [ ] **Step 3: Verify the old backend contract strings are gone from the live CLI/docs surface**

Run:

```bash
rg -n 'auto, macos, linux, generic|\[--backend auto\|macos\|linux\|generic\]|local.backend.*macos.*linux.*generic|linux.*notify-send|auto backend could not find a native notifier on the current platform' README.md config.example.toml internal/cli/local.go internal/cli/testnotify.go
```

Expected: no matches.

Run:

```bash
rg -n "2026-04-21-beeep-notifier-design.md" docs/superpowers/specs/2026-04-14-notifytun-design.md
```

Expected: one match for the new superseded note.

- [ ] **Step 4: Run the final verification suite**

Run: `go test ./...`

Expected: PASS.

Run: `GOOS=windows GOARCH=amd64 go build -o /tmp/notifytun.exe ./cmd/notifytun`

Expected: success with no compiler errors.

- [ ] **Step 5: Commit**

```bash
git add README.md config.example.toml docs/superpowers/specs/2026-04-14-notifytun-design.md
git commit -m "docs: update backend docs for beeep migration"
```

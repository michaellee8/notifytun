package cli

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"log"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/michaellee8/notifytun/internal/notifier"
	"github.com/michaellee8/notifytun/internal/proto"
	tunnelssh "github.com/michaellee8/notifytun/internal/ssh"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"
)

const (
	heartbeatTimeout          = 45 * time.Second
	stableConnTime            = 60 * time.Second
	defaultNotifQueueCapacity = 8
	defaultRemoteBin          = "notifytun"
)

type localOptions struct {
	target     string
	remoteBin  string
	backend    string
	notifyCmd  string
	sshKey     string
	configFile string

	targetSet    bool
	remoteBinSet bool
	backendSet   bool
	notifyCmdSet bool
	sshKeySet    bool
}

type streamEvent struct {
	line []byte
	err  error
	eof  bool
}

type notifDispatcher struct {
	ctx      context.Context
	cancel   context.CancelFunc
	notifier notifier.Notifier

	mu           sync.Mutex
	cond         *sync.Cond
	queue        []*proto.NotifMessage
	droppedCount int
	closed       bool
	done         chan struct{}
	capacity     int
}

// NewLocalCmd connects to the remote VM and delivers desktop notifications locally.
func NewLocalCmd() *cobra.Command {
	opts := localOptions{
		remoteBin: defaultRemoteBin,
		backend:   "auto",
	}

	cmd := &cobra.Command{
		Use:           "local",
		Short:         "Connect to remote VM and deliver notifications locally",
		Args:          cobra.NoArgs,
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			opts.targetSet = cmd.Flags().Changed("target")
			opts.remoteBinSet = cmd.Flags().Changed("remote-bin")
			opts.backendSet = cmd.Flags().Changed("backend")
			opts.notifyCmdSet = cmd.Flags().Changed("notify-cmd")
			opts.sshKeySet = cmd.Flags().Changed("ssh-key")

			if err := opts.loadAndApplyConfig(); err != nil {
				return err
			}
			if opts.target == "" {
				return fmt.Errorf("--target is required (or set local.target in config)")
			}
			return runLocal(cmd.Context(), opts.target, opts.remoteBin, opts.backend, opts.notifyCmd, opts.sshKey)
		},
	}

	cmd.Flags().StringVar(&opts.target, "target", "", "SSH target (user@host or SSH config alias)")
	cmd.Flags().StringVar(&opts.remoteBin, "remote-bin", opts.remoteBin, "Path to notifytun on remote")
	cmd.Flags().StringVar(&opts.backend, "backend", opts.backend, "Notifier backend: auto, macos, linux, generic")
	cmd.Flags().StringVar(&opts.notifyCmd, "notify-cmd", "", "Custom command for generic backend")
	cmd.Flags().StringVar(&opts.sshKey, "ssh-key", "", "Path to SSH private key")
	cmd.Flags().StringVar(&opts.configFile, "config", "", "Config file path")

	return cmd
}

func (o *localOptions) loadAndApplyConfig() error {
	configPath := o.configFile
	explicitConfig := configPath != ""
	if configPath == "" {
		configPath = defaultLocalConfigPath()
	}
	if configPath == "" {
		return nil
	}

	cfg := viper.New()
	cfg.SetConfigFile(configPath)
	if err := cfg.ReadInConfig(); err != nil {
		if configFileMissing(err) {
			if explicitConfig {
				return fmt.Errorf("read config: %w", err)
			}
			return nil
		}
		return fmt.Errorf("read config: %w", err)
	}

	if !o.targetSet {
		o.target = cfg.GetString("local.target")
	}
	if !o.remoteBinSet {
		if value := cfg.GetString("local.remote-bin"); value != "" {
			o.remoteBin = value
		}
	}
	if !o.backendSet {
		if value := cfg.GetString("local.backend"); value != "" {
			o.backend = value
		}
	}
	if !o.sshKeySet {
		o.sshKey = cfg.GetString("local.ssh-key")
	}
	if !o.notifyCmdSet {
		o.notifyCmd = cfg.GetString("local.notify-cmd")
	}

	return nil
}

func defaultLocalConfigPath() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(home, ".notifytun", "config.toml")
}

func configFileMissing(err error) bool {
	var notFound viper.ConfigFileNotFoundError
	return errors.As(err, &notFound) || errors.Is(err, os.ErrNotExist)
}

func runLocal(ctx context.Context, target, remoteBin, backend, notifyCmd, sshKey string) error {
	ctx, cancel := signal.NotifyContext(ctx, syscall.SIGINT, syscall.SIGTERM)
	defer cancel()

	n, err := notifier.New(backend, notifyCmd)
	if err != nil {
		return fmt.Errorf("init notifier: %w", err)
	}
	dispatcher := newNotifDispatcher(ctx, n, defaultNotifQueueCapacity)
	defer func() {
		if err := dispatcher.Close(); err != nil {
			log.Printf("warning: close notifier dispatcher: %v", err)
		}
	}()

	backoff := tunnelssh.NewBackoff()
	remoteCommand := buildRemoteAttachCommand(remoteBin)

	for {
		if ctx.Err() != nil {
			return nil
		}

		log.Printf("connecting to %s...", target)
		connCfg := tunnelssh.ResolveTarget(target, sshKey, "")
		sess, err := tunnelssh.Connect(ctx, connCfg, remoteCommand)
		if err != nil {
			if ctx.Err() != nil {
				return nil
			}

			log.Printf("connection failed: %v", err)
			delay := backoff.Next()
			log.Printf("reconnecting in %s...", delay)
			if err := waitForReconnect(ctx, delay); err != nil {
				return nil
			}
			continue
		}

		log.Printf("connected to %s", target)
		connStart := time.Now()

		go logRemoteStderr(sess.Stderr)

		streamErr := processStream(ctx, sess.Stdout, dispatcher)
		_ = sess.Close()

		if ctx.Err() != nil {
			return nil
		}

		if time.Since(connStart) > stableConnTime {
			backoff.Reset()
		}

		if streamErr != nil {
			log.Printf("connection lost: %v", streamErr)
		} else {
			log.Printf("connection closed")
		}

		delay := backoff.Next()
		log.Printf("reconnecting in %s...", delay)
		if err := waitForReconnect(ctx, delay); err != nil {
			return nil
		}
	}
}

func waitForReconnect(ctx context.Context, delay time.Duration) error {
	timer := time.NewTimer(delay)
	defer timer.Stop()

	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

func buildRemoteAttachCommand(remoteBin string) string {
	if remoteBin == defaultRemoteBin {
		script := `if command -v notifytun >/dev/null 2>&1; then exec notifytun attach; ` +
			`elif [ -x "$HOME/go/bin/notifytun" ]; then exec "$HOME/go/bin/notifytun" attach; ` +
			`else echo "notifytun: not found in PATH or ~/go/bin" >&2; exit 127; fi`
		return "sh -lc " + strconv.Quote(script)
	}
	return "sh -lc " + strconv.Quote(shellQuote(remoteBin)+" attach")
}

func shellQuote(value string) string {
	return "'" + strings.ReplaceAll(value, "'", `'"'"'`) + "'"
}

func logRemoteStderr(stderr io.Reader) {
	scanner := bufio.NewScanner(stderr)
	for scanner.Scan() {
		log.Printf("remote stderr: %s", scanner.Text())
	}
	if err := scanner.Err(); err != nil && !errors.Is(err, io.EOF) {
		log.Printf("warning: remote stderr read failed: %v", err)
	}
}

func processStream(ctx context.Context, stdout io.Reader, dispatcher *notifDispatcher) error {
	return processStreamWithTimeout(ctx, stdout, dispatcher, heartbeatTimeout)
}

func processStreamWithTimeout(ctx context.Context, stdout io.Reader, dispatcher *notifDispatcher, timeout time.Duration) error {
	reader := bufio.NewReader(stdout)
	heartbeatTimer := time.NewTimer(timeout)
	defer heartbeatTimer.Stop()

	events := make(chan streamEvent)
	done := make(chan struct{})
	defer close(done)

	go func() {
		defer close(events)

		for {
			line, err := reader.ReadBytes('\n')
			if len(line) > 0 {
				event := streamEvent{
					line: append([]byte(nil), bytes.TrimRight(line, "\r\n")...),
				}
				select {
				case events <- event:
				case <-done:
					return
				}
			}

			if err == nil {
				continue
			}

			event := streamEvent{eof: true}
			if !errors.Is(err, io.EOF) {
				event = streamEvent{err: fmt.Errorf("stream read error: %w", err)}
			}
			select {
			case events <- event:
			case <-done:
			}
			return
		}
	}()

	for {
		select {
		case <-ctx.Done():
			return nil

		case <-heartbeatTimer.C:
			return fmt.Errorf("heartbeat timeout (%s)", timeout)

		case event, ok := <-events:
			if !ok {
				return fmt.Errorf("stream EOF")
			}
			if event.err != nil {
				return event.err
			}
			if event.eof {
				return fmt.Errorf("stream EOF")
			}

			msg, err := proto.Decode(event.line)
			if err != nil {
				log.Printf("warning: malformed JSONL line: %v", err)
				continue
			}

			switch typed := msg.(type) {
			case *proto.NotifMessage:
				dispatcher.Enqueue(typed)
			case *proto.HeartbeatMessage:
				resetTimer(heartbeatTimer, timeout)
			case nil:
			}
		}
	}
}

func newNotifDispatcher(parent context.Context, n notifier.Notifier, capacity int) *notifDispatcher {
	if capacity < 1 {
		capacity = 1
	}
	ctx, cancel := context.WithCancel(parent)
	dispatcher := &notifDispatcher{
		ctx:      ctx,
		cancel:   cancel,
		done:     make(chan struct{}),
		notifier: n,
		capacity: capacity,
	}
	dispatcher.cond = sync.NewCond(&dispatcher.mu)
	go dispatcher.run()
	return dispatcher
}

func (d *notifDispatcher) Enqueue(msg *proto.NotifMessage) {
	if d == nil || msg == nil {
		return
	}

	d.mu.Lock()
	defer d.mu.Unlock()

	if d.closed {
		return
	}

	if len(d.queue) >= d.capacity {
		d.droppedCount++
		log.Printf("warning: local notifier queue full; coalescing notification backlog (dropped=%d)", d.droppedCount)
		d.cond.Signal()
		return
	}

	d.queue = append(d.queue, msg)
	d.cond.Signal()
}

func (d *notifDispatcher) Close() error {
	if d == nil {
		return nil
	}

	d.mu.Lock()
	d.closed = true
	d.cancel()
	d.cond.Broadcast()
	d.mu.Unlock()

	<-d.done
	return nil
}

func (d *notifDispatcher) run() {
	defer close(d.done)

	for {
		msg := d.next()
		if msg == nil {
			return
		}

		handleNotif(d.ctx, msg, d.notifier)
	}
}

func (d *notifDispatcher) next() *proto.NotifMessage {
	d.mu.Lock()
	defer d.mu.Unlock()

	for {
		if d.ctx.Err() != nil {
			return nil
		}
		if len(d.queue) > 0 {
			msg := d.queue[0]
			d.queue[0] = nil
			d.queue = d.queue[1:]
			return msg
		}
		if d.droppedCount > 0 {
			count := d.droppedCount
			d.droppedCount = 0
			return &proto.NotifMessage{
				Title:   "notifytun",
				Body:    fmt.Sprintf("%d notifications skipped while local delivery was saturated", count),
				Summary: true,
			}
		}
		if d.closed {
			return nil
		}
		d.cond.Wait()
	}
}

func resetTimer(timer *time.Timer, timeout time.Duration) {
	if !timer.Stop() {
		select {
		case <-timer.C:
		default:
		}
	}
	timer.Reset(timeout)
}

func handleNotif(ctx context.Context, msg *proto.NotifMessage, n notifier.Notifier) {
	if msg.Backlog {
		log.Printf("backlog: [%s] %s - %s", msg.Tool, msg.Title, msg.Body)
		return
	}

	if err := n.Notify(ctx, notifier.Notification{
		Title: msg.Title,
		Body:  msg.Body,
		Tool:  msg.Tool,
	}); err != nil {
		log.Printf("warning: notification delivery failed: %v", err)
	}
}

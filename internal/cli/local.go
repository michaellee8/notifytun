package cli

import (
	"bufio"
	"bytes"
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io"
	"log"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"syscall"
	"time"

	"github.com/michaellee8/notifytun/internal/notifier"
	"github.com/michaellee8/notifytun/internal/proto"
	tunnelssh "github.com/michaellee8/notifytun/internal/ssh"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"
	_ "modernc.org/sqlite"
)

const (
	heartbeatTimeout = 45 * time.Second
	stableConnTime   = 60 * time.Second
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

const localSpoolSchema = `
CREATE TABLE IF NOT EXISTS local_spool (
	id      INTEGER PRIMARY KEY AUTOINCREMENT,
	payload BLOB NOT NULL
);
`

type notifSpool struct {
	ctx      context.Context
	cancel   context.CancelFunc
	db       *sql.DB
	dir      string
	wake     chan struct{}
	done     chan struct{}
	notifier notifier.Notifier
}

// NewLocalCmd connects to the remote VM and delivers desktop notifications locally.
func NewLocalCmd() *cobra.Command {
	opts := localOptions{
		remoteBin: "notifytun",
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
	spool, err := newNotifSpool(ctx, n)
	if err != nil {
		return fmt.Errorf("init local spool: %w", err)
	}
	defer func() {
		if err := spool.Close(); err != nil {
			log.Printf("warning: close local spool: %v", err)
		}
	}()

	backoff := tunnelssh.NewBackoff()
	remoteCommand := "sh -lc " + strconv.Quote(remoteBin+" attach")

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

		streamErr := processStream(ctx, sess.Stdout, spool)
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

func logRemoteStderr(stderr io.Reader) {
	scanner := bufio.NewScanner(stderr)
	for scanner.Scan() {
		log.Printf("remote stderr: %s", scanner.Text())
	}
	if err := scanner.Err(); err != nil && !errors.Is(err, io.EOF) {
		log.Printf("warning: remote stderr read failed: %v", err)
	}
}

func processStream(ctx context.Context, stdout io.Reader, spool *notifSpool) error {
	return processStreamWithTimeout(ctx, stdout, spool, heartbeatTimeout)
}

func processStreamWithTimeout(ctx context.Context, stdout io.Reader, spool *notifSpool, timeout time.Duration) error {
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
				if err := spool.Enqueue(typed); err != nil {
					return err
				}
			case *proto.HeartbeatMessage:
				resetTimer(heartbeatTimer, timeout)
			case nil:
			}
		}
	}
}

func newNotifSpool(parent context.Context, n notifier.Notifier) (*notifSpool, error) {
	dir, err := os.MkdirTemp("", "notifytun-local-*")
	if err != nil {
		return nil, fmt.Errorf("create spool dir: %w", err)
	}

	dbPath := filepath.Join(dir, "spool.db")
	sqlDB, err := sql.Open("sqlite", dbPath)
	if err != nil {
		_ = os.RemoveAll(dir)
		return nil, fmt.Errorf("open spool db: %w", err)
	}

	stmts := []string{
		"PRAGMA busy_timeout = 5000",
		"PRAGMA journal_mode = WAL",
		localSpoolSchema,
	}
	for _, stmt := range stmts {
		if _, err := sqlDB.Exec(stmt); err != nil {
			_ = sqlDB.Close()
			_ = os.RemoveAll(dir)
			return nil, fmt.Errorf("init spool db: %w", err)
		}
	}

	ctx, cancel := context.WithCancel(parent)
	spool := &notifSpool{
		ctx:      ctx,
		cancel:   cancel,
		db:       sqlDB,
		dir:      dir,
		wake:     make(chan struct{}, 1),
		done:     make(chan struct{}),
		notifier: n,
	}
	go spool.run()
	return spool, nil
}

func (s *notifSpool) Enqueue(msg *proto.NotifMessage) error {
	if s == nil {
		return fmt.Errorf("notification spool is nil")
	}

	payload, err := proto.Encode(msg)
	if err != nil {
		return fmt.Errorf("encode notification: %w", err)
	}

	if _, err := s.db.Exec(`INSERT INTO local_spool (payload) VALUES (?)`, payload); err != nil {
		return fmt.Errorf("spool notification: %w", err)
	}

	select {
	case s.wake <- struct{}{}:
	default:
	}
	return nil
}

func (s *notifSpool) Close() error {
	if s == nil {
		return nil
	}

	s.cancel()
	<-s.done
	return errors.Join(s.db.Close(), os.RemoveAll(s.dir))
}

func (s *notifSpool) run() {
	defer close(s.done)

	for {
		id, payload, err := s.next()
		if err != nil {
			if s.ctx.Err() != nil {
				return
			}
			log.Printf("warning: local spool read failed: %v", err)
			select {
			case <-s.ctx.Done():
				return
			case <-time.After(50 * time.Millisecond):
			}
			continue
		}

		if id == 0 {
			select {
			case <-s.ctx.Done():
				return
			case <-s.wake:
				continue
			}
		}

		msg, err := proto.Decode(bytes.TrimSpace(payload))
		if err != nil {
			log.Printf("warning: local spool decode failed: %v", err)
			if deleteErr := s.delete(id); deleteErr != nil {
				log.Printf("warning: local spool delete failed: %v", deleteErr)
			}
			continue
		}

		notifMsg, ok := msg.(*proto.NotifMessage)
		if !ok {
			if deleteErr := s.delete(id); deleteErr != nil {
				log.Printf("warning: local spool delete failed: %v", deleteErr)
			}
			continue
		}

		handleNotif(s.ctx, notifMsg, s.notifier)
		if err := s.delete(id); err != nil {
			log.Printf("warning: local spool delete failed: %v", err)
		}
	}
}

func (s *notifSpool) next() (int64, []byte, error) {
	var (
		id      int64
		payload []byte
	)

	err := s.db.QueryRow(`SELECT id, payload FROM local_spool ORDER BY id LIMIT 1`).Scan(&id, &payload)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return 0, nil, nil
		}
		return 0, nil, err
	}

	return id, payload, nil
}

func (s *notifSpool) delete(id int64) error {
	if _, err := s.db.Exec(`DELETE FROM local_spool WHERE id = ?`, id); err != nil {
		return fmt.Errorf("delete notification %d: %w", id, err)
	}
	return nil
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

package cli

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/michaellee8/notifytun/internal/db"
	"github.com/michaellee8/notifytun/internal/proto"
	"github.com/michaellee8/notifytun/internal/socket"
	"github.com/spf13/cobra"
)

const (
	heartbeatInterval = 15 * time.Second
	backlogThreshold  = 3
)

type messageWriter func(any) error

// NewAttachCmd streams queued and live notifications over stdout.
func NewAttachCmd() *cobra.Command {
	var (
		dbPath     string
		socketPath string
	)

	home, _ := os.UserHomeDir()
	defaultDB := home + "/.notifytun/notifytun.db"
	defaultSocket := home + "/.notifytun/notifytun.sock"

	cmd := &cobra.Command{
		Use:           "attach",
		Short:         "Stream notifications over stdout (invoked by local over SSH)",
		Hidden:        true,
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runAttach(cmd.Context(), dbPath, socketPath)
		},
	}

	cmd.Flags().StringVar(&dbPath, "db", defaultDB, "SQLite database path")
	cmd.Flags().StringVar(&socketPath, "socket", defaultSocket, "Unix socket path")

	return cmd
}

func runAttach(ctx context.Context, dbPath, socketPath string) error {
	ctx, cancel := signal.NotifyContext(ctx, syscall.SIGINT, syscall.SIGTERM)
	defer cancel()

	d, err := db.Open(dbPath)
	if err != nil {
		return fmt.Errorf("open db: %w", err)
	}
	defer d.Close()

	listener, err := socket.Listen(socketPath)
	if err != nil {
		return fmt.Errorf("listen socket: %w", err)
	}
	defer listener.Close()

	if err := replayBacklog(d, writeMessage, time.Now); err != nil {
		return err
	}

	return liveLoop(ctx, d, listener, writeMessage, time.Now, heartbeatInterval)
}

func replayBacklog(d *db.DB, write messageWriter, now func() time.Time) error {
	rows, err := d.QueryUndelivered()
	if err != nil {
		return err
	}

	isBacklog := len(rows) > backlogThreshold
	for _, row := range rows {
		msg := &proto.NotifMessage{
			ID:        row.ID,
			Title:     row.Title,
			Body:      row.Body,
			Tool:      row.Tool,
			CreatedAt: row.CreatedAt,
			Backlog:   isBacklog,
		}
		if err := write(msg); err != nil {
			return err
		}
		if err := d.MarkDelivered(row.ID); err != nil {
			return err
		}
	}

	if isBacklog {
		return write(&proto.NotifMessage{
			ID:        0,
			Title:     "notifytun",
			Body:      fmt.Sprintf("%d notifications delivered while disconnected", len(rows)),
			CreatedAt: now().UTC().Format(time.RFC3339Nano),
			Summary:   true,
		})
	}

	return nil
}

func streamUndelivered(d *db.DB, write messageWriter) error {
	rows, err := d.QueryUndelivered()
	if err != nil {
		return err
	}

	for _, row := range rows {
		msg := &proto.NotifMessage{
			ID:        row.ID,
			Title:     row.Title,
			Body:      row.Body,
			Tool:      row.Tool,
			CreatedAt: row.CreatedAt,
		}
		if err := write(msg); err != nil {
			return err
		}
		if err := d.MarkDelivered(row.ID); err != nil {
			return err
		}
	}

	return nil
}

func liveLoop(
	ctx context.Context,
	d *db.DB,
	listener *socket.Listener,
	write messageWriter,
	now func() time.Time,
	interval time.Duration,
) error {
	nextHeartbeat := now().Add(interval)

	for {
		if ctx.Err() != nil {
			return nil
		}

		waitFor := time.Until(nextHeartbeat)
		if waitFor < 0 {
			waitFor = 0
		}

		waitCtx, cancel := context.WithTimeout(ctx, waitFor)
		err := listener.Wait(waitCtx)
		cancel()

		if ctx.Err() != nil {
			return nil
		}

		if err != nil && !errors.Is(err, context.DeadlineExceeded) {
			return err
		}

		if errors.Is(err, context.DeadlineExceeded) || !nextHeartbeat.After(now()) {
			if err := write(&proto.HeartbeatMessage{
				Ts: now().UTC().Format(time.RFC3339Nano),
			}); err != nil {
				return nil
			}
			nextHeartbeat = now().Add(interval)
		}

		if err := streamUndelivered(d, write); err != nil {
			return nil
		}
	}
}

func writeMessage(msg any) error {
	line, err := proto.Encode(msg)
	if err != nil {
		return err
	}
	_, err = os.Stdout.Write(line)
	return err
}

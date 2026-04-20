package socket

import (
	"context"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"time"
)

// Listener waits for unixgram wakeup packets.
type Listener struct {
	conn *net.UnixConn
	path string
}

// Listen binds a unix datagram socket, removing any stale socket file first.
func Listen(path string) (*Listener, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, fmt.Errorf("create socket directory: %w", err)
	}
	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		return nil, fmt.Errorf("remove stale socket: %w", err)
	}

	conn, err := net.ListenUnixgram("unixgram", &net.UnixAddr{Name: path, Net: "unixgram"})
	if err != nil {
		return nil, fmt.Errorf("listen unixgram: %w", err)
	}

	return &Listener{conn: conn, path: path}, nil
}

// Wait blocks until a wakeup packet is received or the context is canceled.
func (l *Listener) Wait(ctx context.Context) error {
	if deadline, ok := ctx.Deadline(); ok {
		if err := l.conn.SetReadDeadline(deadline); err != nil {
			return fmt.Errorf("set read deadline: %w", err)
		}
	}
	defer func() {
		_ = l.conn.SetReadDeadline(time.Time{})
	}()

	// Always watch for ctx cancellation. If a deadline is set, the read
	// deadline will fire first in the deadline case; otherwise ctx.Done
	// wins if the parent context is cancelled before any wakeup arrives.
	done := make(chan struct{})
	defer close(done)

	go func() {
		select {
		case <-ctx.Done():
			_ = l.conn.SetReadDeadline(time.Now())
		case <-done:
		}
	}()

	buf := make([]byte, 1)
	if _, _, err := l.conn.ReadFromUnix(buf); err != nil {
		var netErr net.Error
		if errors.As(err, &netErr) && netErr.Timeout() {
			if ctxErr := ctx.Err(); ctxErr != nil {
				return ctxErr
			}
			if deadline, ok := ctx.Deadline(); ok && !time.Now().Before(deadline) {
				return context.DeadlineExceeded
			}
		}
		return err
	}

	return nil
}

// Close closes the socket and removes the socket file.
func (l *Listener) Close() error {
	err := l.conn.Close()
	if removeErr := os.Remove(l.path); removeErr != nil && !os.IsNotExist(removeErr) && err == nil {
		err = removeErr
	}
	return err
}

// SendWakeup sends a single wakeup byte. Missing listeners are ignored.
func SendWakeup(path string) error {
	conn, err := net.DialUnix("unixgram", nil, &net.UnixAddr{Name: path, Net: "unixgram"})
	if err != nil {
		return nil
	}
	defer conn.Close()

	if _, err := conn.Write([]byte{0x01}); err != nil {
		return nil
	}

	return nil
}

package proto

import (
	"encoding/json"
	"fmt"
)

const (
	TypeNotif     = "notif"
	TypeHeartbeat = "heartbeat"
)

// NotifMessage is a notification event sent from the remote side.
type NotifMessage struct {
	Type      string `json:"type"`
	ID        int64  `json:"id"`
	Title     string `json:"title"`
	Body      string `json:"body"`
	Tool      string `json:"tool"`
	CreatedAt string `json:"created_at"`
	Backlog   bool   `json:"backlog,omitempty"`
	Summary   bool   `json:"summary,omitempty"`
}

// HeartbeatMessage is a keep-alive event sent by attach.
type HeartbeatMessage struct {
	Type string `json:"type"`
	Ts   string `json:"ts"`
}

// Encode serializes a single protocol message to one JSONL line.
func Encode(msg any) ([]byte, error) {
	switch m := msg.(type) {
	case *NotifMessage:
		m.Type = TypeNotif
	case *HeartbeatMessage:
		m.Type = TypeHeartbeat
	default:
		return nil, fmt.Errorf("unknown message type: %T", msg)
	}

	data, err := json.Marshal(msg)
	if err != nil {
		return nil, err
	}

	return append(data, '\n'), nil
}

// Decode parses a single JSONL message without the trailing newline.
// Unknown message types are ignored and return (nil, nil).
func Decode(line []byte) (any, error) {
	var base struct {
		Type string `json:"type"`
	}
	if err := json.Unmarshal(line, &base); err != nil {
		return nil, fmt.Errorf("invalid JSON: %w", err)
	}

	switch base.Type {
	case TypeNotif:
		var msg NotifMessage
		if err := json.Unmarshal(line, &msg); err != nil {
			return nil, fmt.Errorf("invalid notif message: %w", err)
		}
		return &msg, nil
	case TypeHeartbeat:
		var msg HeartbeatMessage
		if err := json.Unmarshal(line, &msg); err != nil {
			return nil, fmt.Errorf("invalid heartbeat message: %w", err)
		}
		return &msg, nil
	default:
		return nil, nil
	}
}

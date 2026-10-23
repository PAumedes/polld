// Package notify delivers desktop notifications.
//
// Two backends implement Notifier: a native D-Bus client speaking the
// freedesktop Notifications protocol, and a fallback that shells out to
// notify-send. The "auto" backend tries D-Bus first — the right answer on
// any desktop session — and falls back to notify-send, which also covers
// minimal environments where only the CLI is installed.
//
// See docs/adr/0003.
package notify

import (
	"context"
	"fmt"
	"html"

	"github.com/PAumedes/polld/internal/config"
)

// Notification is one user-facing notification.
type Notification struct {
	Summary string
	Body    string
	// Link, when set, is rendered as an "Open" anchor after the body.
	Link string
	// Urgency is "low", "normal", or "critical".
	Urgency string
	AppName string
	Icon    string
}

// Notifier delivers notifications. Implementations may hold a bus
// connection; Close releases it.
type Notifier interface {
	Notify(ctx context.Context, n Notification) error
	Close() error
}

// New builds the configured backend. "auto" prefers D-Bus and falls back to
// the notify-send command when no session bus is reachable.
func New(cfg config.NotificationsConfig) (Notifier, error) {
	switch cfg.Backend {
	case "dbus":
		return newDBus(cfg)
	case "command":
		return newCommand(cfg)
	case "auto":
		if n, err := newDBus(cfg); err == nil {
			return n, nil
		}
		return newCommand(cfg)
	default:
		return nil, fmt.Errorf("unknown notification backend %q", cfg.Backend)
	}
}

// formatBody produces notification-spec markup: the body HTML-escaped with an
// optional appended link anchor. Escaping the user content first is what lets
// us safely add our own markup — the spec allows a small HTML subset.
func formatBody(n Notification) string {
	body := html.EscapeString(n.Body)
	if n.Link != "" {
		body += fmt.Sprintf("\n<a href=%q>Open</a>", html.EscapeString(n.Link))
	}
	return body
}

// urgencyByte maps config urgency to the spec's 0/1/2 levels.
func urgencyByte(urgency string) byte {
	switch urgency {
	case "low":
		return 0
	case "critical":
		return 2
	default:
		return 1
	}
}

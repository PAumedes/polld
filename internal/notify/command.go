package notify

import (
	"context"
	"fmt"
	"os/exec"

	"github.com/PAumedes/polld/internal/config"
)

// commandNotifier shells out to notify-send — the fallback for environments
// where speaking D-Bus directly is not an option.
type commandNotifier struct {
	appName string
	icon    string
	urgency string
}

func newCommand(cfg config.NotificationsConfig) (Notifier, error) {
	if _, err := exec.LookPath("notify-send"); err != nil {
		return nil, fmt.Errorf("notify-send not found in PATH: %w", err)
	}
	return &commandNotifier{appName: cfg.AppName, icon: cfg.Icon, urgency: cfg.Urgency}, nil
}

// commandArgs builds the notify-send argument list; kept pure for testing.
func commandArgs(n Notification) []string {
	var args []string
	if n.AppName != "" {
		args = append(args, "-a", n.AppName)
	}
	if n.Urgency != "" {
		args = append(args, "-u", n.Urgency)
	}
	if n.Icon != "" {
		args = append(args, "-i", n.Icon)
	}
	return append(args, n.Summary, formatBody(n))
}

// Notify implements Notifier.
func (n *commandNotifier) Notify(ctx context.Context, notif Notification) error {
	if notif.AppName == "" {
		notif.AppName = n.appName
	}
	if notif.Urgency == "" {
		notif.Urgency = n.urgency
	}
	if notif.Icon == "" {
		notif.Icon = n.icon
	}
	cmd := exec.CommandContext(ctx, "notify-send", commandArgs(notif)...)
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("notify-send: %w (%s)", err, string(out))
	}
	return nil
}

// Close implements Notifier; the command backend holds no resources.
func (n *commandNotifier) Close() error { return nil }

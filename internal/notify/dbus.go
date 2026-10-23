package notify

import (
	"context"
	"fmt"

	"github.com/godbus/dbus/v5"

	"github.com/PAumedes/polld/internal/config"
)

// dbusNotifier talks to org.freedesktop.Notifications directly — the protocol
// GNOME, KDE, and every freedesktop-compliant notification daemon implement.
type dbusNotifier struct {
	conn    *dbus.Conn
	appName string
	icon    string
	urgency byte
}

func newDBus(cfg config.NotificationsConfig) (Notifier, error) {
	conn, err := dbus.ConnectSessionBus()
	if err != nil {
		return nil, fmt.Errorf("connecting to session bus: %w", err)
	}
	return &dbusNotifier{
		conn:    conn,
		appName: cfg.AppName,
		icon:    cfg.Icon,
		urgency: urgencyByte(cfg.Urgency),
	}, nil
}

// Notify implements Notifier. replaces_id is always 0: each change is a new
// notification rather than replacing the previous one — polld's value is in
// the stream of distinct events.
func (n *dbusNotifier) Notify(ctx context.Context, notif Notification) error {
	hints := map[string]dbus.Variant{"urgency": dbus.MakeVariant(n.urgency)}
	call := n.conn.Object("org.freedesktop.Notifications", "/org/freedesktop/Notifications").CallWithContext(
		ctx,
		"org.freedesktop.Notifications.Notify",
		0,
		firstNonEmpty(notif.AppName, n.appName),
		uint32(0),
		firstNonEmpty(notif.Icon, n.icon),
		notif.Summary,
		formatBody(notif),
		[]string{},
		hints,
		int32(-1),
	)
	return call.Err
}

// Close implements Notifier.
func (n *dbusNotifier) Close() error {
	return n.conn.Close()
}

func firstNonEmpty(a, b string) string {
	if a != "" {
		return a
	}
	return b
}

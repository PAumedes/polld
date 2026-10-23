package notify

import (
	"strings"
	"testing"

	"github.com/PAumedes/polld/internal/config"
)

func TestFormatBodyAppendsLink(t *testing.T) {
	n := Notification{Summary: "s", Body: "b", Link: "https://x.io/1"}
	got := formatBody(n)
	if !strings.Contains(got, `<a href="https://x.io/1">Open</a>`) {
		t.Errorf("body = %q", got)
	}
	if !strings.HasPrefix(got, "b") {
		t.Errorf("body = %q, want original body first", got)
	}
}

func TestFormatBodyNoLink(t *testing.T) {
	n := Notification{Summary: "s", Body: "b"}
	if got := formatBody(n); got != "b" {
		t.Errorf("body = %q, want %q", got, "b")
	}
}

func TestFormatBodyEscapesMarkup(t *testing.T) {
	n := Notification{Summary: "s", Body: "a < b & c > d"}
	got := formatBody(n)
	if strings.Contains(got, "a < b") {
		t.Errorf("unescaped markup in %q", got)
	}
}

func TestCommandArgs(t *testing.T) {
	n := Notification{Summary: "s", Body: "b", Urgency: "critical", AppName: "polld", Icon: "dialog-info"}
	args := commandArgs(n)
	joined := strings.Join(args, " ")
	for _, want := range []string{"-u critical", "-a polld", "-i dialog-info", "s"} {
		if !strings.Contains(joined, want) {
			t.Errorf("args %q missing %q", joined, want)
		}
	}
}

func TestUrgencyByte(t *testing.T) {
	cases := map[string]byte{"low": 0, "normal": 1, "critical": 2}
	for u, want := range cases {
		if got := urgencyByte(u); got != want {
			t.Errorf("urgencyByte(%q) = %d, want %d", u, got, want)
		}
	}
	if got := urgencyByte("bogus"); got != 1 {
		t.Errorf("unknown urgency should map to normal, got %d", got)
	}
}

func TestNewBackendSelection(t *testing.T) {
	cfg := func(backend string) config.NotificationsConfig {
		return config.NotificationsConfig{Backend: backend, AppName: "polld", Urgency: "normal"}
	}
	if _, err := New(cfg("bogus")); err == nil {
		t.Error("bogus backend should error")
	}
	if n, err := New(cfg("command")); err != nil {
		t.Skipf("notify-send not available on this machine: %v", err)
	} else {
		defer func() { _ = n.Close() }()
		if _, ok := n.(*commandNotifier); !ok {
			t.Errorf("backend=command gave %T", n)
		}
	}
}

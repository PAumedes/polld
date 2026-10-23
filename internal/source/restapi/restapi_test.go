package restapi

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/PAumedes/polld/internal/config"
)

const changesFixture = `{
  "service": "release-monitor",
  "changes": [
    {"uid": "r1", "summary": "Deploy v2.1", "detail": "prod rollout", "href": "https://svc/d/1", "rev": "a"},
    {"uid": "r2", "summary": "Deploy v2.2", "detail": "canary", "href": "https://svc/d/2", "rev": "b"}
  ]
}`

func newServer(t *testing.T, body string, status int, check func(r *http.Request)) *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if check != nil {
			check(r)
		}
		w.WriteHeader(status)
		_, _ = fmt.Fprint(w, body)
	}))
}

func baseOptions(extra map[string]any) map[string]any {
	opts := map[string]any{
		"items": "changes",
		"mapping": map[string]any{
			"id":    "uid",
			"title": "summary",
			"body":  "detail",
			"link":  "href",
		},
	}
	for k, v := range extra {
		opts[k] = v
	}
	return opts
}

func TestPollMapsItemsToEvents(t *testing.T) {
	srv := newServer(t, changesFixture, 200, nil)
	defer srv.Close()

	src, err := New(config.SourceConfig{Name: "svc", Type: "rest", URL: srv.URL, Options: baseOptions(nil)}, srv.Client())
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	events, err := src.Poll(context.Background())
	if err != nil {
		t.Fatalf("Poll: %v", err)
	}
	if len(events) != 2 {
		t.Fatalf("got %d events", len(events))
	}
	e := events[0]
	if e.ID != "r1" || e.Title != "Deploy v2.1" || e.Body != "prod rollout" || e.Link != "https://svc/d/1" {
		t.Errorf("event = %+v", e)
	}
	if e.Kind != "rest" {
		t.Errorf("Kind = %q", e.Kind)
	}
	if e.Fingerprint != "" {
		t.Errorf("Fingerprint = %q, want empty (no mapping)", e.Fingerprint)
	}
}

func TestFingerprintAndHeadersAndNestedPaths(t *testing.T) {
	var sawAuth string
	srv := newServer(t, `{"data":{"list":[{"meta":{"key":"k1","ver":"9"},"msg":"hi"}]}}`, 200, func(r *http.Request) {
		sawAuth = r.Header.Get("X-Token")
	})
	defer srv.Close()

	opts := baseOptions(map[string]any{
		"items":   "data.list",
		"headers": map[string]any{"X-Token": "abc"},
		"mapping": map[string]any{
			"id":          "meta.key",
			"fingerprint": "meta.ver",
			"title":       "msg",
		},
	})
	src, err := New(config.SourceConfig{Name: "svc", Type: "rest", URL: srv.URL, Options: opts}, srv.Client())
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	events, err := src.Poll(context.Background())
	if err != nil {
		t.Fatalf("Poll: %v", err)
	}
	if sawAuth != "abc" {
		t.Errorf("X-Token header = %q", sawAuth)
	}
	if len(events) != 1 || events[0].ID != "k1" || events[0].Fingerprint != "9" {
		t.Fatalf("events = %+v", events)
	}
}

func TestMissingIDSkipsItem(t *testing.T) {
	srv := newServer(t, `{"changes":[{"uid":"ok","summary":"a"},{"summary":"no id"}]}`, 200, nil)
	defer srv.Close()

	src, err := New(config.SourceConfig{Name: "svc", Type: "rest", URL: srv.URL, Options: baseOptions(nil)}, srv.Client())
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	events, err := src.Poll(context.Background())
	if err != nil {
		t.Fatalf("Poll: %v", err)
	}
	if len(events) != 1 || events[0].ID != "ok" {
		t.Fatalf("events = %+v", events)
	}
}

func TestNon2xxIsAnError(t *testing.T) {
	srv := newServer(t, `{"changes":[]}`, 503, nil)
	defer srv.Close()

	src, _ := New(config.SourceConfig{Name: "svc", Type: "rest", URL: srv.URL, Options: baseOptions(nil)}, srv.Client())
	if _, err := src.Poll(context.Background()); err == nil {
		t.Fatal("expected error on 503")
	}
}

func TestNewValidatesMapping(t *testing.T) {
	cases := []struct {
		name    string
		opts    map[string]any
		wantErr string
	}{
		{name: "no items", opts: map[string]any{"mapping": map[string]any{"id": "x", "title": "y"}}, wantErr: "items"},
		{name: "no id", opts: map[string]any{"items": "c", "mapping": map[string]any{"title": "y"}}, wantErr: "mapping.id"},
		{name: "no title", opts: map[string]any{"items": "c", "mapping": map[string]any{"id": "x"}}, wantErr: "mapping.title"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := New(config.SourceConfig{Name: "s", Type: "rest", URL: "https://x", Options: tc.opts}, nil)
			if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
				t.Fatalf("err = %v, want %q", err, tc.wantErr)
			}
		})
	}
}

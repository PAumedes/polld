package gitlab

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/PAumedes/polld/internal/config"
)

const mrListFixture = `[
  {
    "id": 9001, "iid": 42, "project_id": 7,
    "title": "Add retry logic", "state": "opened",
    "web_url": "https://gitlab.example.com/platform/api/-/merge_requests/42",
    "source_branch": "feat/retry", "target_branch": "main",
    "updated_at": "2026-10-23T10:00:00.000Z",
    "author": {"name": "Ada Lovelace", "username": "ada"}
  },
  {
    "id": 9002, "iid": 43, "project_id": 7,
    "title": "Bump deps", "state": "merged",
    "web_url": "https://gitlab.example.com/platform/api/-/merge_requests/43",
    "source_branch": "chore/deps", "target_branch": "main",
    "updated_at": "2026-10-23T11:00:00.000Z",
    "author": {"name": "Grace Hopper", "username": "grace"}
  }
]`

const eventsFixture = `[
  {
    "id": 501, "action_name": "merged", "target_type": "MergeRequest",
    "target_iid": 43, "target_title": "Bump deps",
    "created_at": "2026-10-23T12:00:00.000Z",
    "author": {"name": "Grace Hopper", "username": "grace"}
  },
  {
    "id": 502, "action_name": "pushed", "target_type": null,
    "created_at": "2026-10-23T12:30:00.000Z",
    "author": {"name": "Ada Lovelace", "username": "ada"},
    "push_data": {"ref": "main", "commit_to": "abc123", "commit_count": 3}
  },
  {
    "id": 503, "action_name": "commented", "target_type": "Issue",
    "target_iid": 9, "target_title": "Flaky test",
    "created_at": "2026-10-23T12:45:00.000Z",
    "author": {"name": "Ada Lovelace", "username": "ada"}
  }
]`

type route struct {
	status int
	body   string
}

func okJSON(body string) route { return route{status: http.StatusOK, body: body} }

type handler struct {
	t       *testing.T
	routes  map[string]route
	seenHdr []string
}

func (h *handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	h.seenHdr = append(h.seenHdr, r.Header.Get("PRIVATE-TOKEN"))
	rt, ok := h.routes[r.Method+" "+r.URL.EscapedPath()+"?"+r.URL.RawQuery]
	if !ok {
		h.t.Errorf("unexpected request %s %s?%s", r.Method, r.URL.EscapedPath(), r.URL.RawQuery)
		http.Error(w, `{"message":"404 Not Found"}`, http.StatusNotFound)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(rt.status)
	_, _ = fmt.Fprint(w, rt.body)
}

func newTestServer(t *testing.T, routes map[string]route) (*httptest.Server, *handler) {
	h := &handler{t: t, routes: routes}
	return httptest.NewServer(h), h
}

func srcConfig(url string, watch map[string]any) config.SourceConfig {
	return config.SourceConfig{
		Name: "test",
		Type: "gitlab",
		URL:  url,
		Options: map[string]any{
			"token_env": "POLLD_TEST_TOKEN",
			"watch":     watch,
		},
	}
}

func TestMergeRequestsGlobalScope(t *testing.T) {
	t.Setenv("POLLD_TEST_TOKEN", "tok")
	srv, h := newTestServer(t, map[string]route{
		"GET /api/v4/merge_requests?order_by=updated_at&per_page=50&scope=all&sort=desc&state=all": okJSON(mrListFixture),
	})
	defer srv.Close()

	src, err := New(srcConfig(srv.URL, map[string]any{
		"merge_requests": map[string]any{},
	}), srv.Client())
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	events, err := src.Poll(context.Background())
	if err != nil {
		t.Fatalf("Poll: %v", err)
	}
	if len(events) != 2 {
		t.Fatalf("got %d events, want 2", len(events))
	}
	e := events[0]
	if e.ID != "mr-7-42" {
		t.Errorf("ID = %q", e.ID)
	}
	if e.Fingerprint != "2026-10-23T10:00:00Z" {
		t.Errorf("Fingerprint = %q", e.Fingerprint)
	}
	if !strings.Contains(e.Title, "!42") || !strings.Contains(e.Title, "opened") {
		t.Errorf("Title = %q", e.Title)
	}
	if !strings.Contains(e.Body, "Ada") {
		t.Errorf("Body = %q", e.Body)
	}
	if e.Link == "" || e.Kind != "merge_request" {
		t.Errorf("Link/Kind = %q/%q", e.Link, e.Kind)
	}
	if h.seenHdr[0] != "tok" {
		t.Errorf("PRIVATE-TOKEN = %q, want tok", h.seenHdr[0])
	}
}

func TestMergeRequestsPerProjectAndStateFilter(t *testing.T) {
	t.Setenv("POLLD_TEST_TOKEN", "tok")
	srv, _ := newTestServer(t, map[string]route{
		"GET /api/v4/projects/platform%2Fapi/merge_requests?order_by=updated_at&per_page=50&sort=desc&state=all": okJSON(mrListFixture),
	})
	defer srv.Close()

	src, err := New(srcConfig(srv.URL, map[string]any{
		"merge_requests": map[string]any{
			"projects": []any{"platform/api"},
			"states":   []any{"opened"},
		},
	}), srv.Client())
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	events, err := src.Poll(context.Background())
	if err != nil {
		t.Fatalf("Poll: %v", err)
	}
	if len(events) != 1 || events[0].ID != "mr-7-42" {
		t.Fatalf("state filter failed: %v", events)
	}
}

func TestProjectEventsFilteredByAction(t *testing.T) {
	t.Setenv("POLLD_TEST_TOKEN", "tok")
	srv, _ := newTestServer(t, map[string]route{
		"GET /api/v4/projects/platform%2Fapi/events?per_page=50": okJSON(eventsFixture),
	})
	defer srv.Close()

	src, err := New(srcConfig(srv.URL, map[string]any{
		"events": map[string]any{
			"projects": []any{"platform/api"},
			"actions":  []any{"merged", "pushed"},
		},
	}), srv.Client())
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	events, err := src.Poll(context.Background())
	if err != nil {
		t.Fatalf("Poll: %v", err)
	}
	if len(events) != 2 {
		t.Fatalf("got %d events, want 2 (commented filtered out)", len(events))
	}
	mr, push := events[0], events[1]
	if mr.ID != "event-501" || mr.Fingerprint != "" {
		t.Errorf("MR event ID/FP = %q/%q", mr.ID, mr.Fingerprint)
	}
	if want := srv.URL + "/platform/api/-/merge_requests/43"; mr.Link != want {
		t.Errorf("MR link = %q, want %q", mr.Link, want)
	}
	if !strings.Contains(push.Title, "main") || !strings.Contains(push.Body, "Ada") {
		t.Errorf("push event = %q / %q", push.Title, push.Body)
	}
	if want := srv.URL + "/platform/api/-/commits/main"; push.Link != want {
		t.Errorf("push link = %q, want %q", push.Link, want)
	}
}

func TestPollAggregatesErrors(t *testing.T) {
	t.Setenv("POLLD_TEST_TOKEN", "tok")
	srv, _ := newTestServer(t, map[string]route{
		"GET /api/v4/projects/ok%2Fone/events?per_page=50":      okJSON(`[]`),
		"GET /api/v4/projects/missing%2Ftwo/events?per_page=50": {status: 404, body: `{"message":"404"}`},
	})
	defer srv.Close()

	src, err := New(srcConfig(srv.URL, map[string]any{
		"events": map[string]any{
			"projects": []any{"ok/one", "missing/two"},
		},
	}), srv.Client())
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	_, err = src.Poll(context.Background())
	if err == nil || !strings.Contains(err.Error(), "missing/two") {
		t.Fatalf("err = %v, want to name the failing project", err)
	}
}

func TestNewValidatesConfig(t *testing.T) {
	t.Setenv("POLLD_TEST_TOKEN", "tok")
	cases := []struct {
		name    string
		watch   map[string]any
		token   string
		wantErr string
	}{
		{name: "empty watch", watch: map[string]any{}, token: "tok", wantErr: "watch"},
		{name: "events without projects", watch: map[string]any{"events": map[string]any{}}, token: "tok", wantErr: "projects"},
		{name: "missing token env value", watch: map[string]any{"merge_requests": map[string]any{}}, token: "", wantErr: "token"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if tc.token == "" {
				t.Setenv("POLLD_TEST_TOKEN", "")
			}
			_, err := New(srcConfig("https://x", tc.watch), nil)
			if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
				t.Fatalf("err = %v, want %q", err, tc.wantErr)
			}
		})
	}
}

func TestPollWorksWithoutToken(t *testing.T) {
	srv, h := newTestServer(t, map[string]route{
		"GET /api/v4/merge_requests?order_by=updated_at&per_page=50&scope=all&sort=desc&state=all": okJSON(`[]`),
	})
	defer srv.Close()

	cfg := srcConfig(srv.URL, map[string]any{"merge_requests": map[string]any{}})
	delete(cfg.Options, "token_env")
	src, err := New(cfg, srv.Client())
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if _, err := src.Poll(context.Background()); err != nil {
		t.Fatalf("Poll: %v", err)
	}
	if len(h.seenHdr) != 1 || h.seenHdr[0] != "" {
		t.Errorf("expected no PRIVATE-TOKEN header, got %v", h.seenHdr)
	}
}

func TestEventJSONRoundTripsThroughFixture(t *testing.T) {
	var events []evResp
	if err := json.Unmarshal([]byte(eventsFixture), &events); err != nil {
		t.Fatal(err)
	}
	if events[1].PushData.CommitTo != "abc123" {
		t.Error("fixture decode sanity check failed")
	}
}

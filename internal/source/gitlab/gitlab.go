// Package gitlab is a source adapter for the GitLab REST API v4.
//
// It supports two watchers, both configured per source:
//
//	merge_requests — new or updated merge requests, globally (scope=all) or
//	                 per project; dedup fingerprint is updated_at, so state
//	                 transitions and pushes to the branch re-notify.
//	events         — project events (pushes, issues, MR actions, comments);
//	                 these have unique server IDs so each fires once.
//
// Configuration shape (inside the source's inline options):
//
//	token_env: GITLAB_TOKEN        # env var holding a personal access token
//	watch:
//	  merge_requests:
//	    projects: [group/app]      # optional; empty = everything visible
//	    states: [opened, merged]   # optional; empty = all states
//	  events:
//	    projects: [group/app]      # required
//	    actions: [pushed, merged]  # optional; empty = all actions
package gitlab

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/PAumedes/polld/internal/config"
	"github.com/PAumedes/polld/internal/source"
)

const (
	apiPath        = "/api/v4"
	perPage        = 50
	defaultTimeout = 15 * time.Second
)

func init() {
	source.Register("gitlab", func(cfg config.SourceConfig, client *http.Client) (source.Source, error) {
		return New(cfg, client)
	})
}

type options struct {
	TokenEnv string `yaml:"token_env"`
	Watch    watch  `yaml:"watch"`
}

type watch struct {
	MergeRequests *mrWatch `yaml:"merge_requests"`
	Events        *evWatch `yaml:"events"`
}

type mrWatch struct {
	Projects []string `yaml:"projects"`
	States   []string `yaml:"states"`
}

type evWatch struct {
	Projects []string `yaml:"projects"`
	Actions  []string `yaml:"actions"`
}

type gitlabSource struct {
	name    string
	base    string
	token   string
	client  *http.Client
	mr      *mrWatch
	ev      *evWatch
	mrState map[string]bool
	evAct   map[string]bool
}

// New builds the adapter from the source's inline options.
func New(cfg config.SourceConfig, client *http.Client) (source.Source, error) {
	var opts options
	if err := config.Decode(cfg.Options, &opts); err != nil {
		return nil, err
	}
	if opts.Watch.MergeRequests == nil && opts.Watch.Events == nil {
		return nil, errors.New("watch: configure merge_requests and/or events")
	}
	if opts.Watch.Events != nil && len(opts.Watch.Events.Projects) == 0 {
		return nil, errors.New("watch.events: projects is required (there is no global events endpoint)")
	}
	token := ""
	if opts.TokenEnv != "" {
		token = os.Getenv(opts.TokenEnv)
		if token == "" {
			return nil, fmt.Errorf("token: environment variable %s is empty or unset", opts.TokenEnv)
		}
	}
	if client == nil {
		client = &http.Client{Timeout: defaultTimeout}
	}
	s := &gitlabSource{
		name:   cfg.Name,
		base:   strings.TrimRight(cfg.URL, "/"),
		token:  token,
		client: client,
		mr:     opts.Watch.MergeRequests,
		ev:     opts.Watch.Events,
	}
	if s.mr != nil {
		s.mrState = toSet(s.mr.States)
	}
	if s.ev != nil {
		s.evAct = toSet(s.ev.Actions)
	}
	return s, nil
}

// toSet builds a lookup set; nil means "no filter, accept everything".
func toSet(items []string) map[string]bool {
	if len(items) == 0 {
		return nil
	}
	m := make(map[string]bool, len(items))
	for _, i := range items {
		m[i] = true
	}
	return m
}

// Name implements source.Source.
func (s *gitlabSource) Name() string { return s.name }

// Poll implements source.Source, merging results from every configured watcher.
func (s *gitlabSource) Poll(ctx context.Context) ([]source.Event, error) {
	var out []source.Event
	var errs []error
	if s.mr != nil {
		evs, err := s.pollMergeRequests(ctx)
		if err != nil {
			errs = append(errs, err)
		}
		out = append(out, evs...)
	}
	if s.ev != nil {
		evs, err := s.pollProjectEvents(ctx)
		if err != nil {
			errs = append(errs, err)
		}
		out = append(out, evs...)
	}
	return out, errors.Join(errs...)
}

// --- merge requests ---

type mrResp struct {
	ID           int       `json:"id"`
	IID          int       `json:"iid"`
	ProjectID    int       `json:"project_id"`
	Title        string    `json:"title"`
	State        string    `json:"state"`
	WebURL       string    `json:"web_url"`
	SourceBranch string    `json:"source_branch"`
	TargetBranch string    `json:"target_branch"`
	UpdatedAt    time.Time `json:"updated_at"`
	Author       struct {
		Name     string `json:"name"`
		Username string `json:"username"`
	} `json:"author"`
}

func (s *gitlabSource) pollMergeRequests(ctx context.Context) ([]source.Event, error) {
	if len(s.mr.Projects) == 0 {
		return s.pollMRs(ctx, apiPath+"/merge_requests", "all")
	}
	var out []source.Event
	var errs []error
	for _, p := range s.mr.Projects {
		evs, err := s.pollMRs(ctx, apiPath+"/projects/"+url.PathEscape(p)+"/merge_requests", "")
		if err != nil {
			errs = append(errs, fmt.Errorf("%s: %w", p, err))
			continue
		}
		out = append(out, evs...)
	}
	return out, errors.Join(errs...)
}

func (s *gitlabSource) pollMRs(ctx context.Context, path, scope string) ([]source.Event, error) {
	q := url.Values{
		"state":    {"all"},
		"order_by": {"updated_at"},
		"sort":     {"desc"},
		"per_page": {strconv.Itoa(perPage)},
	}
	if scope != "" {
		q.Set("scope", scope)
	}
	var mrs []mrResp
	if err := s.getJSON(ctx, path, q, &mrs); err != nil {
		return nil, err
	}
	out := make([]source.Event, 0, len(mrs))
	for _, m := range mrs {
		if s.mrState != nil && !s.mrState[m.State] {
			continue
		}
		out = append(out, mrToEvent(m))
	}
	return out, nil
}

func mrToEvent(m mrResp) source.Event {
	return source.Event{
		ID:          fmt.Sprintf("mr-%d-%d", m.ProjectID, m.IID),
		Fingerprint: m.UpdatedAt.Format(time.RFC3339Nano),
		Kind:        "merge_request",
		Title:       fmt.Sprintf("MR !%d %s: %s", m.IID, m.State, m.Title),
		Body:        fmt.Sprintf("%s · %s → %s", authorName(m.Author.Name, m.Author.Username), m.SourceBranch, m.TargetBranch),
		Link:        m.WebURL,
		OccurredAt:  m.UpdatedAt,
	}
}

// --- project events ---

type evResp struct {
	ID          int       `json:"id"`
	ActionName  string    `json:"action_name"`
	TargetType  string    `json:"target_type"`
	TargetIID   int       `json:"target_iid"`
	TargetTitle string    `json:"target_title"`
	CreatedAt   time.Time `json:"created_at"`
	Author      struct {
		Name     string `json:"name"`
		Username string `json:"username"`
	} `json:"author"`
	PushData *struct {
		Ref         string `json:"ref"`
		CommitTo    string `json:"commit_to"`
		CommitCount int    `json:"commit_count"`
	} `json:"push_data"`
}

func (s *gitlabSource) pollProjectEvents(ctx context.Context) ([]source.Event, error) {
	var out []source.Event
	var errs []error
	for _, p := range s.ev.Projects {
		var resp []evResp
		err := s.getJSON(ctx, apiPath+"/projects/"+url.PathEscape(p)+"/events", url.Values{"per_page": {strconv.Itoa(perPage)}}, &resp)
		if err != nil {
			errs = append(errs, fmt.Errorf("%s: %w", p, err))
			continue
		}
		for _, e := range resp {
			if s.evAct != nil && !s.evAct[e.ActionName] {
				continue
			}
			out = append(out, s.evToEvent(p, e))
		}
	}
	return out, errors.Join(errs...)
}

func (s *gitlabSource) evToEvent(project string, e evResp) source.Event {
	title := e.ActionName
	if e.TargetType != "" && e.TargetTitle != "" {
		title = fmt.Sprintf("%s %s: %s", e.ActionName, strings.ToLower(e.TargetType), e.TargetTitle)
	} else if e.ActionName == "pushed" && e.PushData != nil {
		title = fmt.Sprintf("pushed %d commit(s) to %s", e.PushData.CommitCount, e.PushData.Ref)
	}
	return source.Event{
		ID:         fmt.Sprintf("event-%d", e.ID),
		Kind:       "event",
		Title:      title,
		Body:       fmt.Sprintf("%s on %s", authorName(e.Author.Name, e.Author.Username), project),
		Link:       s.eventLink(project, e),
		OccurredAt: e.CreatedAt,
	}
}

// eventLink maps an event to the most specific web page GitLab has for it.
func (s *gitlabSource) eventLink(project string, e evResp) string {
	base := s.base + "/" + project
	switch e.TargetType {
	case "MergeRequest":
		return fmt.Sprintf("%s/-/merge_requests/%d", base, e.TargetIID)
	case "Issue":
		return fmt.Sprintf("%s/-/issues/%d", base, e.TargetIID)
	case "Milestone":
		return fmt.Sprintf("%s/-/milestones/%d", base, e.TargetIID)
	default:
		if e.ActionName == "pushed" && e.PushData != nil && e.PushData.Ref != "" {
			return fmt.Sprintf("%s/-/commits/%s", base, e.PushData.Ref)
		}
		return base
	}
}

// --- shared HTTP plumbing ---

func (s *gitlabSource) getJSON(ctx context.Context, path string, q url.Values, out any) error {
	u := s.base + path
	if len(q) > 0 {
		u += "?" + q.Encode()
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return err
	}
	if s.token != "" {
		req.Header.Set("PRIVATE-TOKEN", s.token)
	}
	req.Header.Set("Accept", "application/json")
	resp, err := s.client.Do(req)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("GET %s: %s", path, resp.Status)
	}
	return json.NewDecoder(resp.Body).Decode(out)
}

func authorName(name, username string) string {
	if name != "" {
		return name
	}
	return username
}

// Package restapi is the generic source adapter: it turns any JSON REST
// endpoint into events using field mappings from the configuration, so a new
// private service needs no new code — only a config entry.
//
// Paths use gjson syntax (https://github.com/tidwall/gjson): dot-separated
// keys into the JSON document, evaluated once for `items` (which must resolve
// to an array) and once per item for each mapping field.
//
// Configuration shape (inside the source's inline options):
//
//	items: "changes"            # required: path to the array of items
//	headers:                    # optional: extra request headers
//	  Authorization: "Bearer ${SVC_TOKEN}"
//	mapping:
//	  id: "uid"                 # required: stable identifier (dedup key)
//	  title: "summary"          # required: notification title
//	  body: "detail"            # optional
//	  link: "href"              # optional: URL the notification points to
//	  fingerprint: "rev"        # optional: re-notify when this value changes
//	  occurred_at: "at"         # optional: RFC3339 timestamp
package restapi

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/tidwall/gjson"

	"github.com/PAumedes/polld/internal/config"
	"github.com/PAumedes/polld/internal/source"
)

func init() {
	source.Register("rest", func(cfg config.SourceConfig, client *http.Client) (source.Source, error) {
		return New(cfg, client)
	})
}

type options struct {
	Headers map[string]string `yaml:"headers"`
	Items   string            `yaml:"items"`
	Mapping struct {
		ID          string `yaml:"id"`
		Title       string `yaml:"title"`
		Body        string `yaml:"body"`
		Link        string `yaml:"link"`
		Fingerprint string `yaml:"fingerprint"`
		OccurredAt  string `yaml:"occurred_at"`
	} `yaml:"mapping"`
}

type restSource struct {
	name    string
	url     string
	headers map[string]string
	items   string
	mapping options
	client  *http.Client
}

// New builds the adapter, validating that the required mapping fields exist.
func New(cfg config.SourceConfig, client *http.Client) (source.Source, error) {
	var opts options
	if err := config.Decode(cfg.Options, &opts); err != nil {
		return nil, err
	}
	if opts.Items == "" {
		return nil, errors.New("items: required (path to the JSON array)")
	}
	if opts.Mapping.ID == "" {
		return nil, errors.New("mapping.id: required")
	}
	if opts.Mapping.Title == "" {
		return nil, errors.New("mapping.title: required")
	}
	if client == nil {
		client = &http.Client{Timeout: 15 * time.Second}
	}
	return &restSource{
		name:    cfg.Name,
		url:     cfg.URL,
		headers: opts.Headers,
		items:   opts.Items,
		mapping: opts,
		client:  client,
	}, nil
}

// Name implements source.Source.
func (s *restSource) Name() string { return s.name }

// Poll implements source.Source.
func (s *restSource) Poll(ctx context.Context) ([]source.Event, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, s.url, nil)
	if err != nil {
		return nil, err
	}
	for k, v := range s.headers {
		req.Header.Set(k, v)
	}
	req.Header.Set("Accept", "application/json")
	resp, err := s.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("GET %s: %s", s.url, resp.Status)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, 32<<20))
	if err != nil {
		return nil, fmt.Errorf("reading response: %w", err)
	}
	return s.decode(body), nil
}

// decode maps the response items onto events. Items whose mapped id is empty
// are skipped — a partial row should not fail the whole poll.
func (s *restSource) decode(body []byte) []source.Event {
	items := gjson.GetBytes(body, s.items)
	if !items.IsArray() {
		return nil
	}
	var out []source.Event
	for _, item := range items.Array() {
		e := source.Event{
			ID:          item.Get(s.mapping.Mapping.ID).String(),
			Fingerprint: item.Get(s.mapping.Mapping.Fingerprint).String(),
			Kind:        "rest",
			Title:       item.Get(s.mapping.Mapping.Title).String(),
			Body:        item.Get(s.mapping.Mapping.Body).String(),
			Link:        item.Get(s.mapping.Mapping.Link).String(),
		}
		if s.mapping.Mapping.OccurredAt != "" {
			if ts, err := time.Parse(time.RFC3339, item.Get(s.mapping.Mapping.OccurredAt).String()); err == nil {
				e.OccurredAt = ts
			}
		}
		if e.ID == "" {
			continue
		}
		out = append(out, e)
	}
	return out
}

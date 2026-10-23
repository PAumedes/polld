package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func writeTemp(t *testing.T, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestLoadAppliesDefaults(t *testing.T) {
	path := writeTemp(t, `
sources:
  - name: work
    type: gitlab
    url: https://gitlab.example.com
`)
	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.PollInterval.Duration != time.Minute {
		t.Errorf("PollInterval = %v, want 1m", cfg.PollInterval.Duration)
	}
	if cfg.Notifications.Backend != "auto" {
		t.Errorf("Backend = %q, want auto", cfg.Notifications.Backend)
	}
	if cfg.Notifications.AppName != "polld" {
		t.Errorf("AppName = %q, want polld", cfg.Notifications.AppName)
	}
	if cfg.Notifications.Urgency != "normal" {
		t.Errorf("Urgency = %q, want normal", cfg.Notifications.Urgency)
	}
	if cfg.StateFile == "" {
		t.Error("StateFile should default to an XDG state path")
	}
}

func TestLoadParsesInlineOptionsAndPerSourceInterval(t *testing.T) {
	path := writeTemp(t, `
poll_interval: 45s
sources:
  - name: work
    type: gitlab
    url: https://gitlab.example.com
    interval: 90s
    token_env: GITLAB_TOKEN
    watch:
      events:
        projects: [platform/api]
`)
	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	src := cfg.Sources[0]
	if src.Interval.Duration != 90*time.Second {
		t.Errorf("interval = %v, want 90s", src.Interval.Duration)
	}
	if src.Options["token_env"] != "GITLAB_TOKEN" {
		t.Errorf("token_env = %v", src.Options["token_env"])
	}
	watch, ok := src.Options["watch"].(map[string]any)
	if !ok {
		t.Fatalf("watch options = %#v", src.Options["watch"])
	}
	if _, ok := watch["events"]; !ok {
		t.Error("watch.events missing")
	}
}

func TestLoadExpandsEnvVarsInOptions(t *testing.T) {
	t.Setenv("SVC_TOKEN", "s3cr3t")
	path := writeTemp(t, `
sources:
  - name: svc
    type: rest
    url: https://svc.internal/api
    headers:
      Authorization: "Bearer ${SVC_TOKEN}"
`)
	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	headers := cfg.Sources[0].Options["headers"].(map[string]any)
	if headers["Authorization"] != "Bearer s3cr3t" {
		t.Errorf("Authorization = %v", headers["Authorization"])
	}
}

func TestLoadMissingFile(t *testing.T) {
	_, err := Load(filepath.Join(t.TempDir(), "nope.yaml"))
	if err == nil {
		t.Fatal("expected error for missing file")
	}
}

func TestLoadInvalidYAML(t *testing.T) {
	path := writeTemp(t, "sources: [")
	if _, err := Load(path); err == nil {
		t.Fatal("expected error for invalid YAML")
	}
}

func TestValidate(t *testing.T) {
	valid := `
sources:
  - name: a
    type: gitlab
    url: https://gitlab.example.com
`
	cases := []struct {
		name    string
		yaml    string
		wantErr string
	}{
		{name: "valid", yaml: valid},
		{name: "no sources", yaml: `poll_interval: 1m`, wantErr: "no sources"},
		{name: "empty name", yaml: `sources: [{type: gitlab, url: "https://x.io"}]`, wantErr: "name"},
		{name: "duplicate names", yaml: `sources: [{name: a, type: gitlab, url: "https://x.io"}, {name: a, type: rest, url: "https://y.io"}]`, wantErr: "duplicate"},
		{name: "bad url", yaml: `sources: [{name: a, type: gitlab, url: "not a url"}]`, wantErr: "url"},
		{name: "non-http url", yaml: `sources: [{name: a, type: gitlab, url: "ftp://x.io"}]`, wantErr: "url"},
		{name: "bad urgency", yaml: `notifications: {urgency: loud}
sources: [{name: a, type: gitlab, url: "https://x.io"}]`, wantErr: "urgency"},
		{name: "bad backend", yaml: `notifications: {backend: telepathy}
sources: [{name: a, type: gitlab, url: "https://x.io"}]`, wantErr: "backend"},
		{name: "negative interval", yaml: `sources: [{name: a, type: gitlab, url: "https://x.io", interval: -5s}]`, wantErr: "interval"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			path := writeTemp(t, tc.yaml)
			cfg, err := Load(path)
			if err != nil {
				t.Fatalf("Load: %v", err)
			}
			err = cfg.Validate()
			if tc.wantErr == "" {
				if err != nil {
					t.Fatalf("Validate: %v", err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
				t.Fatalf("Validate err = %v, want substring %q", err, tc.wantErr)
			}
		})
	}
}

func TestDecodeOptions(t *testing.T) {
	opts := map[string]any{
		"token_env": "TOKEN",
		"watch": map[string]any{
			"projects": []any{"a/b", "c/d"},
		},
	}
	var out struct {
		TokenEnv string `yaml:"token_env"`
		Watch    struct {
			Projects []string `yaml:"projects"`
		} `yaml:"watch"`
	}
	if err := Decode(opts, &out); err != nil {
		t.Fatalf("Decode: %v", err)
	}
	if out.TokenEnv != "TOKEN" || len(out.Watch.Projects) != 2 {
		t.Errorf("decoded = %+v", out)
	}
}

func TestDefaultPathRespectsXDG(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	if p := DefaultPath(); !strings.HasPrefix(p, os.Getenv("XDG_CONFIG_HOME")) {
		t.Errorf("DefaultPath = %q, want under XDG_CONFIG_HOME", p)
	}
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	if p := DefaultStatePath(); !strings.HasPrefix(p, os.Getenv("XDG_STATE_HOME")) {
		t.Errorf("DefaultStatePath = %q, want under XDG_STATE_HOME", p)
	}
}

// Package config loads, validates, and normalizes the polld YAML configuration.
//
// The schema is deliberately flat: global settings, a notifications block, and a
// list of sources. Each source has a small set of common keys (name, type, url,
// interval); every other key lands in SourceConfig.Options via yaml ",inline" so
// adapters own their own fields without this package knowing about them.
package config

import (
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"time"

	"gopkg.in/yaml.v3"
)

const (
	defaultPollInterval = time.Minute
	appDir              = "polld"
	configFileName      = "config.yaml"
	stateFileName       = "state.json"
)

// Duration wraps time.Duration so YAML strings like "90s" or "1m30s" parse.
type Duration struct {
	time.Duration
}

// UnmarshalYAML implements yaml.Unmarshaler.
func (d *Duration) UnmarshalYAML(value *yaml.Node) error {
	var s string
	if err := value.Decode(&s); err != nil {
		return err
	}
	if s == "" {
		d.Duration = 0
		return nil
	}
	v, err := time.ParseDuration(s)
	if err != nil {
		return fmt.Errorf("invalid duration %q: %w", s, err)
	}
	d.Duration = v
	return nil
}

// Config is the root of the configuration file.
type Config struct {
	PollInterval  Duration            `yaml:"poll_interval"`
	StateFile     string              `yaml:"state_file"`
	Notifications NotificationsConfig `yaml:"notifications"`
	Sources       []SourceConfig      `yaml:"sources"`
}

// NotificationsConfig controls how desktop notifications are delivered.
type NotificationsConfig struct {
	// Backend is "auto" (D-Bus, falling back to notify-send), "dbus", or "command".
	Backend string `yaml:"backend"`
	AppName string `yaml:"app_name"`
	// Urgency is "low", "normal", or "critical" per the freedesktop spec.
	Urgency string `yaml:"urgency"`
	// Icon is a freedesktop icon name or absolute path; empty uses the default.
	Icon string `yaml:"icon"`
}

// SourceConfig describes one thing to poll. Type selects the adapter; every
// other key is captured in Options and decoded by that adapter (see Decode).
type SourceConfig struct {
	Name     string         `yaml:"name"`
	Type     string         `yaml:"type"`
	URL      string         `yaml:"url"`
	Interval Duration       `yaml:"interval"`
	Options  map[string]any `yaml:",inline"`
}

// DefaultPath returns the configuration file location following the XDG Base
// Directory spec: $XDG_CONFIG_HOME/polld/config.yaml or ~/.config/polld/config.yaml.
// POLLD_CONFIG takes precedence when set.
func DefaultPath() string {
	if p := os.Getenv("POLLD_CONFIG"); p != "" {
		return p
	}
	return filepath.Join(xdgDir("XDG_CONFIG_HOME", ".config"), appDir, configFileName)
}

// DefaultStatePath returns the dedup-state file location:
// $XDG_STATE_HOME/polld/state.json or ~/.local/state/polld/state.json.
func DefaultStatePath() string {
	return filepath.Join(xdgDir("XDG_STATE_HOME", filepath.Join(".local", "state")), appDir, stateFileName)
}

func xdgDir(env, fallback string) string {
	if dir := os.Getenv(env); dir != "" {
		return dir
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return fallback
	}
	return filepath.Join(home, fallback)
}

// Load reads, parses, expands, and normalizes the configuration at path.
// Validation is a separate step so callers can report file errors and
// schema errors differently.
func Load(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var cfg Config
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("parsing %s: %w", path, err)
	}
	cfg.setDefaults()
	for i := range cfg.Sources {
		cfg.Sources[i].Options = expandValue(cfg.Sources[i].Options).(map[string]any)
		if cfg.Sources[i].Options == nil {
			cfg.Sources[i].Options = map[string]any{}
		}
	}
	return &cfg, nil
}

func (c *Config) setDefaults() {
	if c.PollInterval.Duration == 0 {
		c.PollInterval.Duration = defaultPollInterval
	}
	if c.StateFile == "" {
		c.StateFile = DefaultStatePath()
	}
	if c.Notifications.Backend == "" {
		c.Notifications.Backend = "auto"
	}
	if c.Notifications.AppName == "" {
		c.Notifications.AppName = "polld"
	}
	if c.Notifications.Urgency == "" {
		c.Notifications.Urgency = "normal"
	}
}

// expandValue expands ${VAR} environment references in every string found in
// the options tree (strings, lists, and maps). Tokens are never literals in the
// file — they are environment references resolved at load time.
func expandValue(v any) any {
	switch t := v.(type) {
	case string:
		return os.ExpandEnv(t)
	case map[string]any:
		for k, e := range t {
			t[k] = expandValue(e)
		}
		return t
	case []any:
		for i, e := range t {
			t[i] = expandValue(e)
		}
		return t
	default:
		return v
	}
}

// Validate checks cross-field and semantic constraints that parsing cannot.
func (c *Config) Validate() error {
	if len(c.Sources) == 0 {
		return errors.New("config has no sources")
	}
	switch c.Notifications.Backend {
	case "auto", "dbus", "command":
	default:
		return fmt.Errorf("notifications.backend %q: must be auto, dbus, or command", c.Notifications.Backend)
	}
	switch c.Notifications.Urgency {
	case "low", "normal", "critical":
	default:
		return fmt.Errorf("notifications.urgency %q: must be low, normal, or critical", c.Notifications.Urgency)
	}
	seen := make(map[string]bool, len(c.Sources))
	for i, s := range c.Sources {
		if s.Name == "" {
			return fmt.Errorf("sources[%d]: name is required", i)
		}
		if s.Type == "" {
			return fmt.Errorf("source %q: type is required", s.Name)
		}
		if s.Interval.Duration < 0 {
			return fmt.Errorf("source %q: interval must be positive", s.Name)
		}
		if seen[s.Name] {
			return fmt.Errorf("source %q: duplicate name", s.Name)
		}
		seen[s.Name] = true
		if s.URL == "" {
			return fmt.Errorf("source %q: url is required", s.Name)
		}
		u, err := url.Parse(s.URL)
		if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
			return fmt.Errorf("source %q: url %q is not a valid http(s) URL", s.Name, s.URL)
		}
	}
	return nil
}

// Interval returns the effective poll interval for a source.
func (s SourceConfig) IntervalOr(fallback time.Duration) time.Duration {
	if s.Interval.Duration > 0 {
		return s.Interval.Duration
	}
	return fallback
}

// Decode re-marshals an Options map into a typed adapter configuration.
// Adapters declare their fields with yaml tags and call this once at
// construction; unknown keys are ignored so adapters can evolve independently.
func Decode(options map[string]any, out any) error {
	raw, err := yaml.Marshal(options)
	if err != nil {
		return fmt.Errorf("encoding options: %w", err)
	}
	if err := yaml.Unmarshal(raw, out); err != nil {
		return fmt.Errorf("decoding source options: %w", err)
	}
	return nil
}

// ExpandEnv exposes the same ${VAR} expansion used at load, for callers that
// build configs programmatically (mainly tests and adapters' token handling).
func ExpandEnv(s string) string {
	return os.ExpandEnv(s)
}

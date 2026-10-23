package source

import (
	"context"
	"errors"
	"net/http"
	"sort"
	"strings"
	"testing"

	"github.com/PAumedes/polld/internal/config"
)

type fakeSource struct{ name string }

func (f *fakeSource) Name() string                          { return f.name }
func (f *fakeSource) Poll(context.Context) ([]Event, error) { return nil, nil }

func TestRegistryRoundTrip(t *testing.T) {
	t.Cleanup(func() { delete(registry, "fake-for-test") })
	Register("fake-for-test", func(cfg config.SourceConfig, _ *http.Client) (Source, error) {
		return &fakeSource{name: cfg.Name}, nil
	})
	src, err := New(config.SourceConfig{Name: "n", Type: "fake-for-test"}, nil)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if src.Name() != "n" {
		t.Errorf("Name = %q", src.Name())
	}
}

func TestNewUnknownTypeListsKnownTypes(t *testing.T) {
	t.Cleanup(func() { delete(registry, "fake-for-test") })
	Register("fake-for-test", func(cfg config.SourceConfig, _ *http.Client) (Source, error) {
		return &fakeSource{name: cfg.Name}, nil
	})
	_, err := New(config.SourceConfig{Name: "n", Type: "bogus"}, nil)
	if err == nil {
		t.Fatal("expected error")
	}
	if got := err.Error(); !strings.Contains(got, "bogus") || !strings.Contains(got, "fake-for-test") {
		t.Errorf("error = %q, want to name the unknown type and list known types", got)
	}
}

func TestTypesSorted(t *testing.T) {
	t.Cleanup(func() { delete(registry, "zz-fake"); delete(registry, "aa-fake") })
	Register("zz-fake", func(config.SourceConfig, *http.Client) (Source, error) { return nil, nil })
	Register("aa-fake", func(config.SourceConfig, *http.Client) (Source, error) { return nil, nil })
	types := Types()
	if !sort.StringsAreSorted(types) {
		t.Errorf("Types = %v, want sorted", types)
	}
}

func TestFactoryErrorPropagates(t *testing.T) {
	t.Cleanup(func() { delete(registry, "fail-fake") })
	Register("fail-fake", func(config.SourceConfig, *http.Client) (Source, error) {
		return nil, errors.New("bad config")
	})
	if _, err := New(config.SourceConfig{Type: "fail-fake"}, nil); err == nil {
		t.Fatal("expected factory error to propagate")
	}
}

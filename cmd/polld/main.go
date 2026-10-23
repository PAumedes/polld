// Command polld polls configured HTTP services and turns changes into
// desktop notifications. It runs as a foreground process under a systemd
// user service; systemd owns the daemonization (restart policy, logging).
//
// Flags:
//
//	-config PATH   configuration file (default: $XDG_CONFIG_HOME/polld/config.yaml)
//	-check-config  validate the configuration and exit
//	-once          poll every source once and exit (debugging / systemd timer)
//	-v             verbose (debug) logging
//	-version       print version and exit
package main

import (
	"context"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"github.com/PAumedes/polld/internal/config"
	"github.com/PAumedes/polld/internal/daemon"
	"github.com/PAumedes/polld/internal/notify"
	"github.com/PAumedes/polld/internal/state"

	// Links every built-in source adapter into the registry.
	_ "github.com/PAumedes/polld/internal/source/all"
)

// version is stamped by goreleaser/ldflags (-X main.version=...).
var version = "dev"

func main() {
	os.Exit(run(os.Args[1:]))
}

func run(args []string) int {
	fs := flag.NewFlagSet("polld", flag.ContinueOnError)
	configPath := fs.String("config", config.DefaultPath(), "path to the YAML configuration")
	checkOnly := fs.Bool("check-config", false, "validate the configuration and exit")
	once := fs.Bool("once", false, "poll every source once and exit")
	verbose := fs.Bool("v", false, "debug logging")
	showVersion := fs.Bool("version", false, "print version and exit")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if *showVersion {
		fmt.Println("polld", version)
		return 0
	}

	logLevel := slog.LevelInfo
	if *verbose {
		logLevel = slog.LevelDebug
	}
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: logLevel}))

	cfg, err := config.Load(*configPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "polld: loading config: %v\n", err)
		return 1
	}
	if err := cfg.Validate(); err != nil {
		fmt.Fprintf(os.Stderr, "polld: invalid config: %v\n", err)
		return 2
	}
	if *checkOnly {
		fmt.Println("polld: configuration OK")
		return 0
	}

	store, err := state.Open(cfg.StateFile)
	if err != nil {
		fmt.Fprintf(os.Stderr, "polld: opening state: %v\n", err)
		return 1
	}
	notifier, err := notify.New(cfg.Notifications)
	if err != nil {
		fmt.Fprintf(os.Stderr, "polld: notification backend: %v\n", err)
		return 1
	}
	defer func() { _ = notifier.Close() }()

	d, err := daemon.New(cfg, store, notifier, logger)
	if err != nil {
		fmt.Fprintf(os.Stderr, "polld: %v\n", err)
		return 2
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	if *once {
		if err := d.RunOnce(ctx); err != nil {
			fmt.Fprintf(os.Stderr, "polld: %v\n", err)
			return 1
		}
		return 0
	}

	logger.Info("polld started", "sources", len(cfg.Sources), "state", cfg.StateFile)
	if err := d.Run(ctx); err != nil {
		fmt.Fprintf(os.Stderr, "polld: %v\n", err)
		return 1
	}
	logger.Info("polld stopped")
	return 0
}

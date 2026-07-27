package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/nigelteosw/eggy/internal/bootstrap"
	"github.com/nigelteosw/eggy/internal/config"
	"github.com/nigelteosw/eggy/internal/home"
)

// envFilePath prefers <home>/.env, the layout's own slot for secrets, and
// falls back to EGGY_ENV_FILE or a ./.env in the working directory so a
// local checkout keeps working unchanged.
func envFilePath(layout home.Layout) string {
	if override := os.Getenv("EGGY_ENV_FILE"); override != "" {
		return override
	}
	if _, err := os.Stat(layout.Env()); err == nil {
		return layout.Env()
	}
	return ".env"
}

func main() {
	if err := run(); err != nil {
		slog.Error("eggyd stopped", "error", err)
		os.Exit(1)
	}
}

func run() error {
	homeDir := flag.String("home", "", "path to Eggy's home directory (default $EGGY_HOME, else /data)")
	configPath := flag.String("config", "", "path to config.yaml (default <home>/config.yaml)")
	flag.Parse()
	// The home directory is resolved before anything else, because
	// config.yaml and .env are themselves artifacts inside it.
	layout := home.Resolve(*homeDir, os.Getenv)
	if err := layout.Ensure(); err != nil {
		return err
	}
	if *configPath == "" {
		*configPath = layout.Config()
	}
	getenv, err := config.DotEnv(envFilePath(layout), os.Getenv)
	if err != nil {
		return fmt.Errorf("load .env: %w", err)
	}
	config, secrets, err := config.LoadOrCreateConfig(*configPath, getenv)
	if err != nil {
		return err
	}
	logger, logFiles, err := bootstrap.NewLogger(layout, secrets)
	if err != nil {
		return fmt.Errorf("open logs: %w", err)
	}
	defer logFiles.Close()
	slog.SetDefault(logger)
	if resolved := home.At(config.DataDir); resolved.Root != layout.Root {
		logger.Warn("config data_dir points outside this home directory; artifacts will be split",
			"home", layout.Root, "data_dir", resolved.Root)
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	var restartRequested atomic.Bool
	requestRestart := func() {
		go func() {
			time.Sleep(1500 * time.Millisecond)
			restartRequested.Store(true)
			stop()
		}()
	}
	app, err := bootstrap.NewApp(config, secrets, bootstrap.AppOptions{FakeAdapters: getenv("EGGY_FAKE_ADAPTERS") == "1", ConfigPath: *configPath, RequestRestart: requestRestart, Logger: logger})
	if err != nil {
		return err
	}
	server := &http.Server{Addr: config.Server.Listen, Handler: app.Handler(), ReadHeaderTimeout: 10 * time.Second, ReadTimeout: 30 * time.Second, WriteTimeout: 2 * time.Minute, IdleTimeout: 2 * time.Minute}
	errorsChannel := make(chan error, 2)
	go func() {
		if err := app.Run(ctx); err != nil && !errors.Is(err, context.Canceled) {
			errorsChannel <- err
		}
	}()
	go func() {
		slog.Info("eggyd listening", "address", config.Server.Listen)
		if err := server.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			errorsChannel <- err
		}
	}()
	select {
	case err := <-errorsChannel:
		stop()
		return err
	case <-ctx.Done():
		shutdownContext, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		if err := server.Shutdown(shutdownContext); err != nil {
			return err
		}
		if restartRequested.Load() {
			exePath, err := os.Executable()
			if err != nil {
				return fmt.Errorf("resolve executable path for restart: %w", err)
			}
			slog.Info("eggyd restarting")
			return syscall.Exec(exePath, os.Args, os.Environ())
		}
		return nil
	}
}

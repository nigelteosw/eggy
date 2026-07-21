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
)

func main() {
	if err := run(); err != nil {
		slog.Error("eggyd stopped", "error", err)
		os.Exit(1)
	}
}

func run() error {
	defaultConfig := os.Getenv("EGGY_CONFIG")
	if defaultConfig == "" {
		defaultConfig = "/data/config.yaml"
	}
	configPath := flag.String("config", defaultConfig, "path to config.yaml")
	flag.Parse()
	envPath := os.Getenv("EGGY_ENV_FILE")
	if envPath == "" {
		envPath = ".env"
	}
	getenv, err := bootstrap.DotEnv(envPath, os.Getenv)
	if err != nil {
		return fmt.Errorf("load .env: %w", err)
	}
	config, secrets, err := bootstrap.LoadOrCreateConfig(*configPath, getenv)
	if err != nil {
		return err
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
	app, err := bootstrap.NewApp(config, secrets, bootstrap.AppOptions{FakeAdapters: getenv("EGGY_FAKE_ADAPTERS") == "1", ConfigPath: *configPath, RequestRestart: requestRestart})
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

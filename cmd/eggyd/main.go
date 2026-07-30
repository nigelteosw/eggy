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
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/nigelteosw/eggy/internal/bootstrap"
	"github.com/nigelteosw/eggy/internal/config"
	"github.com/nigelteosw/eggy/internal/home"
	"github.com/nigelteosw/eggy/internal/web"
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

// run supervises startup rather than performing it once. A failure to build
// the agent -- almost always a config.yaml the daemon cannot load -- puts the
// process into safe mode instead of exiting, because on a container deployment
// exiting is unrecoverable: the file that needs fixing is on a volume only
// this process can reach. Safe mode serves the repair page until the owner
// saves a config that loads, then this loop tries again, all without a
// redeploy.
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
	// The logger opens before the config loads, so that a config that fails to
	// load is itself logged and redacted. Only the secrets with fixed variable
	// names are known this early; startup adds the rest once the file parses.
	envSecrets := config.SecretsFromEnv(getenv)
	logger, logFiles, err := bootstrap.NewLogger(layout, envSecrets)
	if err != nil {
		return fmt.Errorf("open logs: %w", err)
	}
	defer logFiles.Close()
	slog.SetDefault(logger)
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	for {
		app, cfg, err := startup(layout, *configPath, getenv, logger, logFiles)
		if err == nil {
			return serveApp(ctx, cfg, app, logger)
		}
		logger.Error("startup failed, entering safe mode", "error", err, "config", *configPath)
		repaired, safeModeErr := serveSafeMode(ctx, safeModeListen(getenv), *configPath, err, getenv, envSecrets, logger)
		if safeModeErr != nil {
			return safeModeErr
		}
		if !repaired {
			// Shut down on signal rather than looping: safe mode ended because
			// the platform is stopping the container, not because anything was
			// fixed.
			return nil
		}
		logger.Info("config repaired, retrying startup")
	}
}

// startup performs one attempt at becoming the real Eggy.
func startup(layout home.Layout, configPath string, getenv func(string) string, logger *slog.Logger, logFiles *bootstrap.Logs) (*bootstrap.App, config.Config, error) {
	cfg, secrets, err := config.LoadOrCreateConfig(configPath, getenv)
	if err != nil {
		return nil, config.Config{}, err
	}
	// Provider API keys and MCP bearer tokens are only known now: config.yaml
	// chooses the variable names they come from.
	logFiles.Redact(secrets.Values()...)
	if resolved := home.At(cfg.DataDir); resolved.Root != layout.Root {
		logger.Warn("config data_dir points outside this home directory; artifacts will be split",
			"home", layout.Root, "data_dir", resolved.Root)
	}
	app, err := bootstrap.NewApp(cfg, secrets, bootstrap.AppOptions{
		FakeAdapters: getenv("EGGY_FAKE_ADAPTERS") == "1", ConfigPath: configPath, Logger: logger,
	})
	if err != nil {
		return nil, config.Config{}, err
	}
	return app, cfg, nil
}

func serveApp(ctx context.Context, cfg config.Config, app *bootstrap.App, logger *slog.Logger) error {
	server := newServer(cfg.Server.Listen, app.Handler())
	errorsChannel := make(chan error, 2)
	go func() {
		if err := app.Run(ctx); err != nil && !errors.Is(err, context.Canceled) {
			errorsChannel <- err
		}
	}()
	go func() {
		logger.Info("eggyd listening", "address", cfg.Server.Listen)
		if err := server.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			errorsChannel <- err
		}
	}()
	select {
	case err := <-errorsChannel:
		return err
	case <-ctx.Done():
		return shutdown(server)
	}
}

// serveSafeMode runs the repair surface until the owner saves a config that
// loads, reporting whether that happened. The one alternative is ctx ending,
// which means the platform is stopping the container.
func serveSafeMode(ctx context.Context, address, configPath string, failure error, getenv func(string) string, secrets config.Secrets, logger *slog.Logger) (bool, error) {
	repaired := make(chan struct{})
	var once sync.Once
	handler := web.NewSafeModeHandler(web.SafeMode{
		ConfigPath: configPath, Failure: failure, Getenv: getenv,
		Repaired: func() { once.Do(func() { close(repaired) }) },
		Web: web.WebUIConfig{
			UserEmail: secrets.UIUserEmail, Password: secrets.UIPassword,
			SigningKey: []byte(secrets.EncryptionKey),
		},
	})
	if secrets.UIUserEmail == "" || secrets.UIPassword == "" || secrets.EncryptionKey == "" {
		// Worth saying plainly: without a web credential the repair page
		// renders but cannot be signed into, and the config has to be fixed
		// some other way.
		logger.Warn("safe mode cannot be signed into: set EGGY_UI_USER_EMAIL, EGGY_UI_PASSWORD, and EGGY_ENCRYPTION_KEY")
	}
	// Ready reports the startup failure, so /readyz is unhealthy for the whole
	// time safe mode is up while /healthz stays 200: the process is alive, and
	// a platform that reroutes away from a failing health check would take the
	// repair page down with it.
	server := newServer(address, web.NewHTTPHandler(web.Routes{
		Ready: func() error { return failure },
		Web:   handler,
	}))
	listenErrors := make(chan error, 1)
	go func() {
		logger.Warn("safe mode listening", "address", address)
		if err := server.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			listenErrors <- err
		}
	}()
	select {
	case err := <-listenErrors:
		return false, err
	case <-repaired:
		// Graceful: the save request that triggered this is still in flight,
		// and the owner should get its response rather than a dropped
		// connection.
		return true, shutdown(server)
	case <-ctx.Done():
		return false, shutdown(server)
	}
}

func newServer(address string, handler http.Handler) *http.Server {
	return &http.Server{
		Addr: address, Handler: handler,
		ReadHeaderTimeout: 10 * time.Second, ReadTimeout: 30 * time.Second,
		WriteTimeout: 2 * time.Minute, IdleTimeout: 2 * time.Minute,
	}
}

func shutdown(server *http.Server) error {
	shutdownContext, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	return server.Shutdown(shutdownContext)
}

// safeModeListen mirrors what the config would have resolved to, without the
// config: PORT is the platform's override and :8080 the default.
func safeModeListen(getenv func(string) string) string {
	if raw := strings.TrimSpace(getenv("PORT")); raw != "" {
		if port, err := strconv.Atoi(raw); err == nil && port >= 1 && port <= 65535 {
			return ":" + strconv.Itoa(port)
		}
	}
	return ":8080"
}

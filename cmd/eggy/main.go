package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"strings"

	"github.com/nigelteosw/eggy/internal/bootstrap"
	"github.com/nigelteosw/eggy/internal/commands"
	"github.com/nigelteosw/eggy/internal/config"
	"github.com/nigelteosw/eggy/internal/home"
)

// envFilePath mirrors eggyd: <home>/.env when it exists, an explicit
// EGGY_ENV_FILE override first, and ./.env for a local checkout.
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
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, "eggy:", err)
		os.Exit(1)
	}
}

func run(arguments []string) error {
	flags := flag.NewFlagSet("eggy", flag.ContinueOnError)
	homeDir := flags.String("home", "", "path to Eggy's home directory (default $EGGY_HOME, else /data)")
	configPath := flags.String("config", "", "path to config.yaml (default <home>/config.yaml)")
	if err := flags.Parse(arguments); err != nil {
		return err
	}
	// The CLI reads the same home as eggyd, so `eggy config get` and the
	// running daemon can never disagree about which config.yaml is live.
	layout := home.Resolve(*homeDir, os.Getenv)
	if *configPath == "" {
		*configPath = layout.Config()
	}
	args := flags.Args()
	if len(args) == 0 {
		fmt.Println(commands.HelpText(""))
		return nil
	}
	if args[0] == "help" {
		fmt.Println(commands.HelpText(strings.Join(args[1:], " ")))
		return nil
	}
	if args[0] == "config" {
		result, handled, err := commands.ExecuteConfigCLI(context.Background(), *configPath, args)
		if err != nil {
			return err
		}
		if !handled {
			return fmt.Errorf("unknown command %q", strings.Join(args, " "))
		}
		fmt.Println(result.RenderPlainText())
		return nil
	}
	getenv, err := config.DotEnv(envFilePath(layout), os.Getenv)
	if err != nil {
		return err
	}
	if args[0] == "mcp" {
		config, secrets, err := config.LoadMCPConfig(*configPath, getenv)
		if err != nil {
			return err
		}
		result, handled, err := bootstrap.ExecuteMCPCLI(context.Background(), config, secrets, bootstrap.AppOptions{}, args)
		if err != nil {
			return err
		}
		if !handled {
			return fmt.Errorf("unknown command %q", strings.Join(args, " "))
		}
		fmt.Println(result.RenderPlainText())
		return nil
	}
	config, secrets, err := config.LoadOrCreateConfig(*configPath, getenv)
	if err != nil {
		return err
	}
	app, err := bootstrap.NewApp(config, secrets, bootstrap.AppOptions{FakeAdapters: true, ConfigPath: *configPath})
	if err != nil {
		return err
	}
	result, handled, err := app.ExecuteCLI(context.Background(), args)
	if err != nil {
		return err
	}
	if !handled {
		return fmt.Errorf("unknown command %q", strings.Join(args, " "))
	}
	fmt.Println(result.RenderPlainText())
	return nil
}

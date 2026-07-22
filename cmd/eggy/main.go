package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"strings"

	"github.com/nigelteosw/eggy/internal/bootstrap"
)

func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, "eggy:", err)
		os.Exit(1)
	}
}

func run(arguments []string) error {
	flags := flag.NewFlagSet("eggy", flag.ContinueOnError)
	defaultConfig := os.Getenv("EGGY_CONFIG")
	if defaultConfig == "" {
		defaultConfig = "/data/config.yaml"
	}
	configPath := flags.String("config", defaultConfig, "path to config.yaml")
	if err := flags.Parse(arguments); err != nil {
		return err
	}
	args := flags.Args()
	if len(args) == 0 {
		fmt.Println(bootstrap.HelpText(""))
		return nil
	}
	if args[0] == "help" {
		fmt.Println(bootstrap.HelpText(strings.Join(args[1:], " ")))
		return nil
	}
	if args[0] == "config" {
		result, handled, err := bootstrap.ExecuteConfigCLI(context.Background(), *configPath, args)
		if err != nil {
			return err
		}
		if !handled {
			return fmt.Errorf("unknown command %q", strings.Join(args, " "))
		}
		fmt.Println(result.RenderPlainText())
		return nil
	}
	envPath := os.Getenv("EGGY_ENV_FILE")
	if envPath == "" {
		envPath = ".env"
	}
	getenv, err := bootstrap.DotEnv(envPath, os.Getenv)
	if err != nil {
		return err
	}
	if args[0] == "mcp" {
		config, secrets, err := bootstrap.LoadMCPConfig(*configPath, getenv)
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
	config, secrets, err := bootstrap.LoadOrCreateConfig(*configPath, getenv)
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

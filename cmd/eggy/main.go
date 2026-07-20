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
	if flags.NArg() == 0 {
		return fmt.Errorf("usage: eggy [-config path] status|repositories|runs|stop <id>|schedules|memory|new|config")
	}
	if flags.Arg(0) == "config" {
		output, err := configMain(*configPath, flags.Args()[1:])
		if err != nil {
			return err
		}
		fmt.Println(output)
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
	config, secrets, err := bootstrap.LoadOrCreateConfig(*configPath, getenv)
	if err != nil {
		return err
	}
	app, err := bootstrap.NewApp(config, secrets, bootstrap.AppOptions{FakeAdapters: true, ConfigPath: *configPath})
	if err != nil {
		return err
	}
	command := "/" + strings.Join(flags.Args(), " ")
	output, handled, err := app.ExecuteCommand(context.Background(), command)
	if err != nil {
		return err
	}
	if !handled {
		return fmt.Errorf("unknown command %q", flags.Arg(0))
	}
	fmt.Println(output)
	return nil
}

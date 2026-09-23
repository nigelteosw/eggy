// Package config is the one authority over config.yaml: its shape, its
// defaults, what makes it valid, and every write to it. Telegram commands and
// the web panel both change config through the functions here, under one file
// lock and the same validation, so there is never a second way to edit it.
//
// Files:
//
//   - config.go           the Config shape, secrets from the environment, loading, and defaults
//   - config_validate.go  every rule that decides whether a config is usable
//   - config_mutate.go    locked, atomic, comment-preserving writes, one function per setting
//   - config_init.go      first boot and upgrading an older file in place
//   - config_backfill.go  adding sections a newer build has started reading
//   - inputs.go           turning a web form or chat arguments into a typed input
//   - accounts.go, local_accounts.go   who may use this deployment
//   - discord.go          the optional Discord section and its writes
//   - setup.go            the first-run setup form
//   - dotenv.go           the optional .env file beside the home
package config

// Command eggyd is the Eggy daemon: the single production process.
//
// main.go resolves the home directory and supervises startup. When config.yaml
// is missing it serves first-run setup; when the App fails to build it serves
// safe mode (a repair page) instead of exiting; and when the App asks to
// restart it builds a fresh one from the current config. migrate_login.go is
// the one-off --migrate-local-login upgrade from Google Sign-In to local
// accounts.
package main

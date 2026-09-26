// Package local is the in-process scheduler: adding, listing, and removing an
// account's schedules, finding the ones due across every account, and
// recording each run's outcome as the schedule's owner. The daemon's tick
// (internal/bootstrap/run.go) turns due schedules into events. cron.go parses
// and evaluates cron expressions.
package local

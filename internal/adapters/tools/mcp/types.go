package mcp

import (
	"errors"
	"time"
)

type ServerState string

const (
	StateDisabled      ServerState = "disabled"
	StateLoginRequired ServerState = "login_required"
	StateReady         ServerState = "ready"
	StateUnavailable   ServerState = "unavailable"
	StateCooldown      ServerState = "cooldown"
)

var ErrServerNotFound = errors.New("MCP server is not configured")

type ServerStatus struct {
	Name           string
	State          ServerState
	Tools          int
	ReloadRequired bool
	Warnings       []string
	Diagnostic     string
}

type ProbeResult struct {
	Server     string
	State      ServerState
	Tools      int
	Latency    time.Duration
	Diagnostic string
}

package terminal

import (
	"context"
	"encoding/base64"
	"fmt"
	"strings"
)

// execResult is the part of machine.exec's reply Eggy reads. The core sends
// output as base64, as it takes input.
type execResult struct {
	Code     *int   `json:"code"`
	Stdout   string `json:"stdout"`
	Stderr   string `json:"stderr"`
	TimedOut bool   `json:"timed_out"`
}

// run executes argv in the account's core, as the account, and fails on a
// non-zero exit with the end of what the program said.
func (c *core) run(ctx context.Context, argv ...string) error {
	var r execResult
	if err := c.client.call(ctx, "machine.exec", map[string]any{"argv": argv}, &r); err != nil {
		return err
	}
	if r.TimedOut {
		return fmt.Errorf("%s timed out", argv[0])
	}
	if r.Code == nil || *r.Code == 0 {
		return nil
	}
	msg := strings.TrimSpace(decode64(r.Stderr))
	if msg == "" {
		msg = strings.TrimSpace(decode64(r.Stdout))
	}
	if len(msg) > 400 {
		msg = msg[len(msg)-400:]
	}
	return fmt.Errorf("%s failed (exit %d): %s", argv[0], *r.Code, msg)
}

func decode64(s string) string {
	b, err := base64.StdEncoding.DecodeString(s)
	if err != nil {
		return ""
	}
	return string(b)
}

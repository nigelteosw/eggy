package services

import (
	"context"

	"github.com/nigelteosw/eggy/internal/kernel/destination"
)

// webThread builds a ctx addressed to a web thread. Duplicated in the repo
// package's tests for the same reason as the store fakes.
func webThread(id string) context.Context {
	return destination.With(context.Background(), destination.Destination{Kind: destination.Web, ThreadID: id})
}

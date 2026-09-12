package ports

import (
	"context"
	"errors"
	"strings"
)

// ErrNoPrincipal is returned when a private operation is attempted with no
// account on the context. Every private read and write fails closed on it:
// there is no default account, so a missing one is a bug in the caller, never
// a request on somebody's behalf.
var ErrNoPrincipal = errors.New("no account principal on context")

// Principal is who a request or turn is acting as. It is resolved once at a
// trusted ingress -- a verified browser session, a verified Telegram sender,
// the stored owner of a schedule -- and carried unchanged through the kernel.
// Nothing downstream may derive it from a JSON body, a URL, a tool argument
// or message text.
//
// It has no role. Every account holds every capability; the only
// authorization question anywhere is whether the account owns the record.
type Principal struct {
	AccountID string
}

type principalKey struct{}

// WithPrincipal returns ctx carrying p.
func WithPrincipal(ctx context.Context, p Principal) context.Context {
	return context.WithValue(ctx, principalKey{}, p)
}

// PrincipalFromContext returns the acting account or ErrNoPrincipal. A
// principal with an empty account ID counts as absent, so a zero value stored
// by mistake cannot become an account that owns nothing and reads everything.
func PrincipalFromContext(ctx context.Context) (Principal, error) {
	p, ok := ctx.Value(principalKey{}).(Principal)
	if !ok || strings.TrimSpace(p.AccountID) == "" {
		return Principal{}, ErrNoPrincipal
	}
	return p, nil
}

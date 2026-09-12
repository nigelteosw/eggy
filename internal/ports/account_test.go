package ports

import (
	"context"
	"errors"
	"testing"
)

func TestPrincipalRequired(t *testing.T) {
	if _, err := PrincipalFromContext(context.Background()); err == nil {
		t.Fatal("missing account must fail closed")
	} else if !errors.Is(err, ErrNoPrincipal) {
		t.Fatalf("err = %v, want ErrNoPrincipal", err)
	}
	// An empty account is not an account: a zero Principal stored by a bug
	// must not read as "everyone".
	if _, err := PrincipalFromContext(WithPrincipal(context.Background(), Principal{})); !errors.Is(err, ErrNoPrincipal) {
		t.Fatalf("empty principal err = %v, want ErrNoPrincipal", err)
	}
}

func TestPrincipalRoundTrips(t *testing.T) {
	ctx := WithPrincipal(context.Background(), Principal{AccountID: "nigel"})
	p, err := PrincipalFromContext(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if p.AccountID != "nigel" {
		t.Fatalf("AccountID = %q", p.AccountID)
	}
}

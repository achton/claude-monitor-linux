package notify

import (
	"context"
	"testing"
	"time"
)

func TestSignInWarningFiresOncePerExpiry(t *testing.T) {
	e, f := newEval(t)
	ctx := context.Background()

	for _, at := range []time.Time{
		time.Now().Add(10 * 24 * time.Hour), // too far off
		time.Now().Add(-time.Hour),          // already expired
	} {
		if err := e.EvaluateSignIn(ctx, "acct", at); err != nil {
			t.Fatal(err)
		}
	}
	if len(f.msgs) != 0 {
		t.Fatalf("unexpected warnings:\n%s", f.bodies())
	}

	soon := time.Now().Add(2 * 24 * time.Hour)
	for range 2 {
		if err := e.EvaluateSignIn(ctx, "acct", soon); err != nil {
			t.Fatal(err)
		}
	}
	if len(f.msgs) != 1 {
		t.Fatalf("warnings: got %d, want 1:\n%s", len(f.msgs), f.bodies())
	}
}

package store

import (
	"context"
	"testing"
	"time"
)

func TestSchemaAndUpsert(t *testing.T) {
	ctx := context.Background()
	s, err := OpenInMemory()
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	if err := s.UpsertAccount(ctx, nil, "org-1", "label", "a@b", "Max"); err != nil {
		t.Fatal(err)
	}
	if err := s.UpsertAccount(ctx, nil, "org-1", "label", "a@b", "Max"); err != nil {
		t.Fatal(err)
	}

	accs, err := s.ListAccounts(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(accs) != 1 {
		t.Fatalf("want 1 account, got %d", len(accs))
	}
	if accs[0].DisplayName() != "label" {
		t.Errorf("display name: got %q", accs[0].DisplayName())
	}
}

func TestUsageInsertAndLatest(t *testing.T) {
	ctx := context.Background()
	s, _ := OpenInMemory()
	defer s.Close()
	_ = s.UpsertAccount(ctx, nil, "org-1", "lbl", "", "")
	if err := s.InsertUsageReading(ctx, nil, "org-1", 50, 23, 50, 0, "", "", `{"x":1}`, "oauth_usage"); err != nil {
		t.Fatal(err)
	}
	got, err := s.LatestUsage(ctx, "org-1")
	if err != nil {
		t.Fatal(err)
	}
	if got.PrimaryPercent.Float64 != 50 {
		t.Errorf("primary: got %v", got.PrimaryPercent.Float64)
	}
}

func TestNotificationLogDebounce(t *testing.T) {
	ctx := context.Background()
	s, _ := OpenInMemory()
	defer s.Close()
	_ = s.UpsertAccount(ctx, nil, "org-1", "lbl", "", "")
	reset := time.Now().Add(2 * time.Hour)
	fired, err := s.MarkNotificationFired(ctx, "org-1", "weekly", 90, reset)
	if err != nil || !fired {
		t.Fatalf("first fire: %v %v", fired, err)
	}
	fired, err = s.MarkNotificationFired(ctx, "org-1", "weekly", 90, reset)
	if err != nil {
		t.Fatal(err)
	}
	if fired {
		t.Error("second fire should be deduped")
	}
}

func TestSyntheticIdempotency(t *testing.T) {
	ctx := context.Background()
	s, _ := OpenInMemory()
	defer s.Close()
	_ = s.UpsertAccount(ctx, nil, "org-1", "lbl", "", "")
	if err := s.InsertSyntheticUsage(ctx, nil, "org-1", time.Now(), 80, 0, 80, 0); err != nil {
		t.Fatal(err)
	}
	yes, err := s.HasRecentSynthetic(ctx, nil, "org-1", time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if !yes {
		t.Error("expected recent synthetic to be detected")
	}
}

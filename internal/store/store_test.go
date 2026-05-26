package store

import (
	"context"
	"testing"
	"time"
)

func TestUsageInsertAndLatest(t *testing.T) {
	ctx := context.Background()
	s, err := OpenInMemory()
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	if err := s.InsertUsageReading(ctx, nil, 23, 50, 0, "", "", `{"x":1}`); err != nil {
		t.Fatal(err)
	}
	got, err := s.LatestUsage(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if got.SessionPercent.Float64 != 23 {
		t.Errorf("session: got %v", got.SessionPercent.Float64)
	}
	if got.WeeklyPercent.Float64 != 50 {
		t.Errorf("weekly: got %v", got.WeeklyPercent.Float64)
	}
	if got.PrimaryPercent() != 50 {
		t.Errorf("primary: got %v", got.PrimaryPercent())
	}
}

func TestNotificationLogDebounce(t *testing.T) {
	ctx := context.Background()
	s, _ := OpenInMemory()
	defer s.Close()
	reset := time.Now().Add(2 * time.Hour)
	fired, err := s.MarkNotificationFired(ctx, "weekly", 90, reset)
	if err != nil || !fired {
		t.Fatalf("first fire: %v %v", fired, err)
	}
	fired, err = s.MarkNotificationFired(ctx, "weekly", 90, reset)
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
	if err := s.InsertSyntheticUsage(ctx, nil, time.Now(), 0, 80, 0); err != nil {
		t.Fatal(err)
	}
	yes, err := s.HasRecentSynthetic(ctx, nil, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if !yes {
		t.Error("expected recent synthetic to be detected")
	}
}

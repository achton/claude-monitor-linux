package notify

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/achton/claude-monitor-linux/internal/api"
	"github.com/achton/claude-monitor-linux/internal/store"
)

type sent struct {
	summary string
	body    string
	urgency Urgency
}

// fakeSender records notifications instead of talking to D-Bus.
type fakeSender struct{ msgs []sent }

func (f *fakeSender) Send(_, summary, body string, u Urgency) (uint32, error) {
	f.msgs = append(f.msgs, sent{summary, body, u})
	return uint32(len(f.msgs)), nil
}

func (f *fakeSender) bodies() string {
	var b strings.Builder
	for _, m := range f.msgs {
		b.WriteString(m.summary + " | " + m.body + "\n")
	}
	return b.String()
}

func newEval(t *testing.T) (*Evaluator, *fakeSender) {
	t.Helper()
	s, err := store.OpenInMemory()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	f := &fakeSender{}
	return &Evaluator{
		Store:      s,
		Notifier:   f,
		Thresholds: []int{75, 90, 95},
		AppName:    "test",
	}, f
}

func limit(kind, group string, pct float64, reset time.Time) api.Limit {
	return api.Limit{Kind: kind, Group: group, Percent: pct, Severity: "normal", ResetsAt: reset}
}

// Every reported limit is evaluated, not just a fixed session/weekly pair.
func TestEvaluatesEveryLimit(t *testing.T) {
	e, f := newEval(t)
	future := time.Now().Add(2 * time.Hour)

	err := e.EvaluateReading(context.Background(), "acct", api.UsageReading{Limits: []api.Limit{
		limit(api.KindSession, api.GroupSession, 80, future),
		limit(api.KindWeeklyAll, api.GroupWeekly, 12, future),
		{Kind: api.KindWeeklyScoped, Group: api.GroupWeekly, Percent: 91,
			ResetsAt: future, ScopeModel: "Fable"},
	}})
	if err != nil {
		t.Fatal(err)
	}
	if len(f.msgs) != 2 {
		t.Fatalf("notifications: got %d, want 2 (session + scoped):\n%s", len(f.msgs), f.bodies())
	}
	got := f.bodies()
	for _, want := range []string{"Session (5h)", "Weekly · Fable"} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %q:\n%s", want, got)
		}
	}
	// The limit below every threshold stays quiet.
	if strings.Contains(got, "Weekly (7d)") {
		t.Errorf("weekly at 12%% should not alert:\n%s", got)
	}
}

// One poll fires the highest crossed threshold only, not 75 and 90 and 95.
func TestOnlyHighestThresholdPerLimit(t *testing.T) {
	e, f := newEval(t)
	future := time.Now().Add(2 * time.Hour)

	if err := e.EvaluateReading(context.Background(), "acct", api.UsageReading{
		Limits: []api.Limit{limit(api.KindSession, api.GroupSession, 96, future)},
	}); err != nil {
		t.Fatal(err)
	}
	if len(f.msgs) != 1 {
		t.Fatalf("notifications: got %d, want 1:\n%s", len(f.msgs), f.bodies())
	}
	if !strings.Contains(f.msgs[0].body, "96%") {
		t.Errorf("body: %q", f.msgs[0].body)
	}
	if f.msgs[0].urgency != UrgencyCritical {
		t.Errorf("urgency: got %v, want critical at 95+", f.msgs[0].urgency)
	}
}

// A repeat poll inside the same reset window must not re-alert.
func TestDebouncedWithinWindow(t *testing.T) {
	e, f := newEval(t)
	future := time.Now().Add(2 * time.Hour)
	r := api.UsageReading{Limits: []api.Limit{limit(api.KindSession, api.GroupSession, 80, future)}}

	for i := 0; i < 3; i++ {
		if err := e.EvaluateReading(context.Background(), "acct", r); err != nil {
			t.Fatal(err)
		}
	}
	if len(f.msgs) != 1 {
		t.Errorf("notifications: got %d, want 1:\n%s", len(f.msgs), f.bodies())
	}
}

// A new reset window re-arms the same threshold.
func TestNewWindowRefires(t *testing.T) {
	e, f := newEval(t)
	ctx := context.Background()

	if err := e.EvaluateReading(ctx, "acct", api.UsageReading{
		Limits: []api.Limit{limit(api.KindSession, api.GroupSession, 80, time.Now().Add(time.Hour))},
	}); err != nil {
		t.Fatal(err)
	}
	if err := e.EvaluateReading(ctx, "acct", api.UsageReading{
		Limits: []api.Limit{limit(api.KindSession, api.GroupSession, 80, time.Now().Add(6*time.Hour))},
	}); err != nil {
		t.Fatal(err)
	}
	if len(f.msgs) != 2 {
		t.Errorf("notifications: got %d, want 2:\n%s", len(f.msgs), f.bodies())
	}
}

// A percentage belonging to an expired window is stale; alerting on it would
// warn about usage that has already reset.
func TestSkipsExpiredWindow(t *testing.T) {
	e, f := newEval(t)
	if err := e.EvaluateReading(context.Background(), "acct", api.UsageReading{
		Limits: []api.Limit{limit(api.KindSession, api.GroupSession, 99, time.Now().Add(-time.Minute))},
	}); err != nil {
		t.Fatal(err)
	}
	if len(f.msgs) != 0 {
		t.Errorf("expected silence for an expired window, got:\n%s", f.bodies())
	}
}

// A limit at 100% reports as rate-limited and does not also fire 95.
func TestRateLimited(t *testing.T) {
	e, f := newEval(t)
	if err := e.EvaluateReading(context.Background(), "acct", api.UsageReading{
		Limits: []api.Limit{limit(api.KindWeeklyAll, api.GroupWeekly, 100, time.Now().Add(time.Hour))},
	}); err != nil {
		t.Fatal(err)
	}
	if len(f.msgs) != 1 {
		t.Fatalf("notifications: got %d, want 1:\n%s", len(f.msgs), f.bodies())
	}
	if !strings.Contains(f.msgs[0].body, "rate-limited") {
		t.Errorf("body: %q", f.msgs[0].body)
	}
}

// Credit spend has no reset window. It must still alert, and still debounce —
// keyed on the calendar month, else the first alert would be the only one ever.
func TestSpendWithoutResetWindow(t *testing.T) {
	e, f := newEval(t)
	ctx := context.Background()
	r := api.UsageReading{Limits: []api.Limit{
		{Kind: api.KindSpend, Group: api.GroupSpend, Percent: 92, Severity: "normal"},
	}}

	if err := e.EvaluateReading(ctx, "acct", r); err != nil {
		t.Fatal(err)
	}
	if len(f.msgs) != 1 {
		t.Fatalf("notifications: got %d, want 1:\n%s", len(f.msgs), f.bodies())
	}
	if !strings.Contains(f.msgs[0].summary, "Extra usage") {
		t.Errorf("summary: %q", f.msgs[0].summary)
	}
	// No reset window, so no "resets ..." suffix to render.
	if strings.Contains(f.msgs[0].body, "resets") {
		t.Errorf("spend body should not claim a reset: %q", f.msgs[0].body)
	}
	if err := e.EvaluateReading(ctx, "acct", r); err != nil {
		t.Fatal(err)
	}
	if len(f.msgs) != 1 {
		t.Errorf("spend should debounce within the month, got:\n%s", f.bodies())
	}
}

// A limit the client has never heard of still alerts, keyed by its raw kind.
func TestUnknownLimitAlerts(t *testing.T) {
	e, f := newEval(t)
	if err := e.EvaluateReading(context.Background(), "acct", api.UsageReading{
		Limits: []api.Limit{limit("weekly_frobnicator", api.GroupWeekly, 97, time.Now().Add(time.Hour))},
	}); err != nil {
		t.Fatal(err)
	}
	if len(f.msgs) != 1 || !strings.Contains(f.msgs[0].summary, "weekly_frobnicator") {
		t.Errorf("unknown limit did not alert as expected:\n%s", f.bodies())
	}
}

// A nil Evaluator or a missing sink must be a no-op, not a panic: the tray
// constructs the Evaluator before notifications are known to be available.
func TestNilSafe(t *testing.T) {
	var e *Evaluator
	if err := e.EvaluateReading(context.Background(), "acct", api.UsageReading{}); err != nil {
		t.Errorf("nil evaluator: %v", err)
	}
	if err := (&Evaluator{}).EvaluateReading(context.Background(), "acct", api.UsageReading{}); err != nil {
		t.Errorf("empty evaluator: %v", err)
	}
}

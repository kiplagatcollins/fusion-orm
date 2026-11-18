package fusion

import (
	"bytes"
	"context"
	"log/slog"
	"strings"
	"testing"
)

type hookKey struct{}

type testQueryHook struct {
	before []string
	after  []string
	events []*QueryEvent
}

func (h *testQueryHook) BeforeQuery(ctx context.Context, event *QueryEvent) context.Context {
	h.before = append(h.before, event.SQL)
	return context.WithValue(ctx, hookKey{}, "propagated")
}

func (h *testQueryHook) AfterQuery(ctx context.Context, event *QueryEvent) {
	h.after = append(h.after, event.SQL)
	h.events = append(h.events, event)
}

func TestQueryHookWrapsExecution(t *testing.T) {
	db := openTestDB(t)
	hook := &testQueryHook{}
	tx := db.QueryHook(hook).Exec(`CREATE TABLE hook_users (id INTEGER PRIMARY KEY, name TEXT)`)
	if tx.Error != nil {
		t.Fatal(tx.Error)
	}
	if err := db.QueryHook(hook).Exec(`INSERT INTO hook_users (name) VALUES (?)`, "alice").Error; err != nil {
		t.Fatal(err)
	}
	if len(hook.before) != 2 || len(hook.after) != 2 {
		t.Fatalf("hook calls = %d/%d", len(hook.before), len(hook.after))
	}
	for i := range hook.before {
		if hook.before[i] != hook.after[i] {
			t.Errorf("hook order = %v / %v", hook.before, hook.after)
		}
	}
	last := hook.events[1]
	if last.Error != nil || last.RowsAffected != 1 || last.Duration < 0 {
		t.Errorf("event = %+v", last)
	}
	if last.Context.Value(hookKey{}) != "propagated" {
		t.Error("BeforeQuery context did not reach AfterQuery")
	}
	if len(last.Vars) != 1 || last.Vars[0] != "alice" {
		t.Errorf("event vars = %v", last.Vars)
	}
}

type hookWriter struct{ bytes.Buffer }

func (w *hookWriter) Write(p []byte) (int, error) { return w.Buffer.Write(p) }

func TestSlogQueryHook(t *testing.T) {
	db := openTestDB(t)
	var out hookWriter
	hook := &SlogQueryHook{Logger: slog.New(slog.NewTextHandler(&out, nil)), Level: slog.LevelInfo}
	if err := db.QueryHook(hook).Exec(`INSERT INTO slog_users (name) VALUES (?)`, "secret-value").Error; err == nil {
		t.Fatal("expected error for missing table")
	}
	if !strings.Contains(out.String(), "query") || !strings.Contains(out.String(), "INSERT INTO") {
		t.Errorf("slog output = %q", out.String())
	}
	if strings.Contains(out.String(), "secret-value") {
		t.Errorf("bound parameter leaked: %q", out.String())
	}
}

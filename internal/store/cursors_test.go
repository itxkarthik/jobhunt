package store

import (
	"context"
	"testing"
	"time"
)

func TestCursor(t *testing.T) {
	ctx := context.Background()
	s := openMem(t)

	if _, ok, err := s.Cursor(ctx, "agent"); err != nil || ok {
		t.Fatalf("new cursor: ok=%v err=%v, want missing", ok, err)
	}
	t1 := time.Unix(1_800_000_000, 0)
	steps := []struct {
		name    string
		advance time.Time
		want    time.Time
	}{
		{"create", t1, t1},
		{"forward", t1.Add(time.Hour), t1.Add(time.Hour)},
		{"never backwards", t1, t1.Add(time.Hour)},
	}
	for _, st := range steps {
		if err := s.AdvanceCursor(ctx, "agent", st.advance); err != nil {
			t.Fatalf("%s: %v", st.name, err)
		}
		got, ok, err := s.Cursor(ctx, "agent")
		if err != nil || !ok || !got.Equal(st.want) {
			t.Fatalf("%s: cursor = %v ok=%v err=%v, want %v", st.name, got, ok, err, st.want)
		}
	}
	if _, ok, _ := s.Cursor(ctx, "db"); ok {
		t.Error("cursors must be independent per name")
	}
}

func TestValidCursorName(t *testing.T) {
	tests := []struct {
		name string
		ok   bool
	}{
		{"agent", true},
		{"my-agent.v2_prod", true},
		{"0", true},
		{"", false},
		{"Agent", false},
		{"-agent", false},
		{"a b", false},
		{"a/b", false},
		{"x'; DROP TABLE seen; --", false},
		{string(make([]byte, 65)), false},
	}
	for _, tt := range tests {
		if err := ValidCursorName(tt.name); (err == nil) != tt.ok {
			t.Errorf("ValidCursorName(%q) err=%v, want ok=%v", tt.name, err, tt.ok)
		}
	}
	s := openMem(t)
	if err := s.AdvanceCursor(context.Background(), "Bad Name", time.Now()); err == nil {
		t.Error("AdvanceCursor accepted an invalid name")
	}
}

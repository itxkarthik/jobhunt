package main

import (
	"testing"
	"time"

	"github.com/itxkarthik/jobhunt/internal/model"
)

func TestParseRound(t *testing.T) {
	tests := []struct {
		in      string
		want    model.Round
		wantErr bool
	}{
		{"seed,2_500_000,USD,2026-09-02,https://a.example/x", model.Round{Stage: "seed", Amount: 2.5e6, Currency: "USD",
			Date: time.Date(2026, 9, 2, 0, 0, 0, 0, time.UTC), SourceURL: "https://a.example/x"}, false},
		{" series-a , , , , https://a.example/y ", model.Round{Stage: "series-a", SourceURL: "https://a.example/y"}, false},
		{"seed,2M,USD,2026-09-02,https://a", model.Round{}, true},
		{"seed,1,USD,02/09/2026,https://a", model.Round{}, true},
		{"seed,1,USD", model.Round{}, true},
	}
	for _, tt := range tests {
		got, err := parseRound(tt.in)
		if (err != nil) != tt.wantErr || (!tt.wantErr && got != tt.want) {
			t.Errorf("parseRound(%q) = %+v, %v", tt.in, got, err)
		}
	}
}

func TestMoney(t *testing.T) {
	for in, want := range map[float64]string{2.5e6: "2.5M", 375e6: "375M", 1.25e9: "1.3B", 50e3: "50K", 999: "999", 1_049_000: "1M"} {
		if got := money(in); got != want {
			t.Errorf("money(%v) = %s, want %s", in, got, want)
		}
	}
}

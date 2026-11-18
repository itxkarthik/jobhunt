package model

import "testing"

func TestParseStage(t *testing.T) {
	tests := []struct {
		in      string
		want    Stage
		wantErr bool
	}{
		{"", StageUnknown, false},
		{"Series A", StageSeriesA, false},
		{"series_b", StageSeriesB, false},
		{"angel", StagePreSeed, false},
		{"Series E", StageGrowth, false},
		{"seed", StageSeed, false},
		{"IPO", StagePublic, false},
		{"unicorn", "", true},
	}
	for _, tt := range tests {
		got, err := ParseStage(tt.in)
		if (err != nil) != tt.wantErr || got != tt.want {
			t.Errorf("ParseStage(%q) = %q, %v; want %q, err %v", tt.in, got, err, tt.want, tt.wantErr)
		}
	}
}

func TestParseHeadcount(t *testing.T) {
	tests := []struct {
		in      string
		want    Headcount
		wantErr bool
	}{
		{"", HeadcountUnknown, false},
		{"11-50", Headcount11to50, false},
		{"7", Headcount1to10, false},
		{"10", Headcount1to10, false},
		{"11", Headcount11to50, false},
		{"350", Headcount201to500, false},
		{"5000+", Headcount1kPlus, false},
		{"0", "", true},
		{"lots", "", true},
	}
	for _, tt := range tests {
		got, err := ParseHeadcount(tt.in)
		if (err != nil) != tt.wantErr || got != tt.want {
			t.Errorf("ParseHeadcount(%q) = %q, %v; want %q, err %v", tt.in, got, err, tt.want, tt.wantErr)
		}
	}
}

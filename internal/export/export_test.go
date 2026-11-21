package export

import (
	"bytes"
	"encoding/csv"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/itxkarthik/jobhunt/internal/model"
)

func rows() []Row {
	minSalary := 600000.0
	return []Row{{Status: "applied", Job: model.Job{
		Key: "abc123", Title: "Backend | Intern", Company: "Acme", City: "Pune", JobType: model.TypeInternship,
		Areas: []model.Area{model.AreaBackend, model.AreaDevOps}, SalaryMin: &minSalary, Currency: "INR",
		PostedAt: time.Date(2026, 10, 20, 0, 0, 0, 0, time.UTC), Sources: []string{"greenhouse"}, URL: "https://x/1", Score: 88,
	}}}
}

func TestFormats(t *testing.T) {
	tests := []struct {
		format string
		check  func(t *testing.T, out string)
	}{
		{"csv", func(t *testing.T, out string) {
			recs, err := csv.NewReader(strings.NewReader(out)).ReadAll()
			if err != nil || len(recs) != 2 {
				t.Fatalf("csv: %v, %d records", err, len(recs))
			}
			got := map[string]string{}
			for i, h := range recs[0] {
				got[h] = recs[1][i]
			}
			if got["status"] != "applied" || got["areas"] != "backend;devops" || got["salary_min"] != "600000" || got["posted"] != "2026-10-20" {
				t.Errorf("csv row = %v", got)
			}
		}},
		{"json", func(t *testing.T, out string) {
			var v []map[string]any
			if err := json.Unmarshal([]byte(out), &v); err != nil || len(v) != 1 || v[0]["status"] != "applied" || v[0]["key"] != "abc123" {
				t.Errorf("json = %s (%v)", out, err)
			}
		}},
		{"md", func(t *testing.T, out string) {
			if !strings.Contains(out, `[Backend \| Intern](https://x/1)`) || !strings.Contains(out, "| 88 | applied |") {
				t.Errorf("md = %s", out)
			}
		}},
	}
	for _, tt := range tests {
		t.Run(tt.format, func(t *testing.T) {
			var b bytes.Buffer
			if err := Write(&b, tt.format, rows()); err != nil {
				t.Fatal(err)
			}
			tt.check(t, b.String())
		})
	}
	if err := Write(&bytes.Buffer{}, "xml", nil); err == nil {
		t.Error("xml: want error")
	}
}

func TestFormatFor(t *testing.T) {
	for in, want := range map[string]string{"a.json": "json", "a.md": "md", "a.csv": "csv", "out": "csv"} {
		if got := FormatFor(in); got != want {
			t.Errorf("FormatFor(%q) = %q", in, got)
		}
	}
}

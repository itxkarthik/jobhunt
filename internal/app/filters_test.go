package app

import (
	"math"
	"reflect"
	"slices"
	"testing"
	"time"

	"github.com/itxkarthik/jobhunt/internal/config"
	"github.com/itxkarthik/jobhunt/internal/model"
)

var defaults = config.Defaults{Countries: []string{"IN"}, Types: []string{"internship"}}

func TestParseFilters(t *testing.T) {
	tests := []struct {
		name    string
		in      FilterInput
		check   func(model.Filters) bool
		wantErr bool
	}{
		{"defaults fill empty fields", FilterInput{}, func(f model.Filters) bool {
			return slices.Equal(f.Countries, []string{"IN"}) && slices.Equal(f.Types, []model.JobType{model.TypeInternship})
		}, false},
		{"explicit values win", FilterInput{Countries: []string{"us", "remote-global"}, Types: []string{"new-grad"}}, func(f model.Filters) bool {
			return slices.Equal(f.Countries, []string{"US", model.RemoteGlobal}) && slices.Equal(f.Types, []model.JobType{model.TypeNewGrad})
		}, false},
		{"area aliases", FilterInput{Areas: []string{"ML/AI", "sre"}}, func(f model.Filters) bool {
			return slices.Equal(f.Areas, []model.Area{model.AreaMLAI, model.AreaDevOps})
		}, false},
		{"keywords trimmed, blanks dropped", FilterInput{Keywords: []string{" Go ", "", "Rust"}}, func(f model.Filters) bool {
			return slices.Equal(f.Keywords, []string{"Go", "Rust"})
		}, false},
		{"age and currency", FilterInput{PostedWithin: "7d", Currency: " inr "}, func(f model.Filters) bool {
			return f.PostedWithin == 7*24*time.Hour && f.Currency == "INR"
		}, false},
		{"any lifts the default", FilterInput{Countries: []string{"any"}, Types: []string{" ANY "}, Modes: []string{"any"}, Areas: []string{"any"}}, func(f model.Filters) bool {
			return f.Countries == nil && f.Types == nil && f.Modes == nil && f.Areas == nil
		}, false},
		{"any with other values", FilterInput{Countries: []string{"any", "US"}}, nil, true},
		{"unknown country", FilterInput{Countries: []string{"Atlantis"}}, nil, true},
		{"unknown mode", FilterInput{Modes: []string{"teleport"}}, nil, true},
		{"unknown type", FilterInput{Types: []string{"forever"}}, nil, true},
		{"unknown area", FilterInput{Areas: []string{"alchemy"}}, nil, true},
		{"unknown experience", FilterInput{Experience: []string{"wizard"}}, nil, true},
		{"bad age", FilterInput{PostedWithin: "soon"}, nil, true},
		{"negative salary", FilterInput{MinSalary: -1}, nil, true},
		{"NaN salary", FilterInput{MinSalary: math.NaN()}, nil, true},
		{"infinite salary", FilterInput{MinSalary: math.Inf(1)}, nil, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f, err := ParseFilters(defaults, tt.in)
			if (err != nil) != tt.wantErr {
				t.Fatalf("err = %v, wantErr %v", err, tt.wantErr)
			}
			if err == nil && !tt.check(f) {
				t.Errorf("filters = %+v", f)
			}
		})
	}
}

func TestInputOfRoundTrip(t *testing.T) {
	f := model.Filters{
		Countries: []string{"IN", model.RemoteGlobal}, Modes: []model.WorkMode{model.ModeRemote},
		Types: []model.JobType{model.TypeInternship}, Areas: []model.Area{model.AreaBackend},
		Experience: []model.Experience{model.ExpIntern}, Keywords: []string{"Go"}, Exclude: []string{"senior"},
		PostedWithin: 3 * 24 * time.Hour, VisaOnly: true, MinSalary: 100000, Currency: "INR",
		StrictCountry: true, IncludeUnknown: true,
	}
	got, err := ParseFilters(defaults, InputOf(f))
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, f) {
		t.Errorf("round trip:\n got %+v\nwant %+v", got, f)
	}
}

func TestAge(t *testing.T) {
	tests := []struct {
		in   string
		want time.Duration
		out  string
	}{
		{"24h", 24 * time.Hour, "1d"},
		{"3d", 3 * 24 * time.Hour, "3d"},
		{"90m", 90 * time.Minute, "1h30m0s"},
		{"0d", 0, ""},
		{"-1h", 0, ""},
		{"week", 0, ""},
	}
	for _, tt := range tests {
		got, err := ParseAge(tt.in)
		if (err == nil) != (tt.want > 0) || got != tt.want {
			t.Errorf("ParseAge(%q) = %v, %v; want %v", tt.in, got, err, tt.want)
		}
		if s := FormatAge(got); s != tt.out {
			t.Errorf("FormatAge(%v) = %q, want %q", got, s, tt.out)
		}
	}
}

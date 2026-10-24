package model

import (
	"slices"
	"testing"
)

func TestParseLocation(t *testing.T) {
	tests := []struct {
		in        string
		countries []string
		city      string
	}{
		{"Bangalore, Karnataka", []string{"IN"}, "Bengaluru"},
		{"New Delhi, India", []string{"IN"}, "Delhi"},
		{"Worldwide", []string{RemoteGlobal}, ""},
		{"Remote - US", []string{"US"}, ""},
		{"USA, Canada, USA timezones", []string{"CA", "US"}, ""},
		{"Europe, USA, Canada, APAC", []string{"CA", RegionEurope, RegionAPAC, "US"}, ""},
		{"Tokyo, Japan", []string{"JP"}, "Tokyo"},
		{"London, UK", []string{"GB"}, "London"},
		{"Indianapolis", nil, ""}, // "india" must not match inside a word
		{"", nil, ""},
		{"Remote", nil, ""},
	}
	for _, tt := range tests {
		c, city := ParseLocation(tt.in)
		slices.Sort(c)
		want := slices.Clone(tt.countries)
		slices.Sort(want)
		if !slices.Equal(c, want) || city != tt.city {
			t.Errorf("ParseLocation(%q) = %v, %q; want %v, %q", tt.in, c, city, want, tt.city)
		}
	}
}

func TestCountryAllows(t *testing.T) {
	tests := []struct {
		name   string
		codes  []string
		c      string
		remote bool
		strict bool
		want   bool
	}{
		{"direct", []string{"IN"}, "IN", false, false, true},
		{"worldwide remote", []string{RemoteGlobal}, "IN", true, false, true},
		{"worldwide strict", []string{RemoteGlobal}, "IN", true, true, false},
		{"apac remote", []string{RegionAPAC}, "IN", true, false, true},
		{"europe remote", []string{RegionEurope}, "IN", true, false, false},
		{"region but onsite", []string{RegionAPAC}, "IN", false, false, false},
		{"other country", []string{"US"}, "IN", true, false, false},
	}
	for _, tt := range tests {
		if got := CountryAllows(tt.codes, tt.c, tt.remote, tt.strict); got != tt.want {
			t.Errorf("%s: got %v, want %v", tt.name, got, tt.want)
		}
	}
}

func TestNormalizeCountry(t *testing.T) {
	for in, want := range map[string]string{"uk": "GB", "IN": "IN", "india": "IN", "remote-global": RemoteGlobal, "uae": "AE", "jp": "JP"} {
		if got, ok := NormalizeCountry(in); !ok || got != want {
			t.Errorf("NormalizeCountry(%q) = %q, %v; want %q", in, got, ok, want)
		}
	}
}

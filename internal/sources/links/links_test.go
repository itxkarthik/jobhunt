package links

import (
	"net/url"
	"strings"
	"testing"

	"github.com/itxkarthik/jobhunt/internal/model"
)

func TestFor(t *testing.T) {
	tests := []struct {
		name    string
		f       model.Filters
		want    map[string][]string // site -> substrings its URL must contain
		without []string            // sites that must not appear
	}{
		{
			name: "india internship backend",
			f:    model.Filters{Countries: []string{"IN"}, Types: []model.JobType{model.TypeInternship}, Areas: []model.Area{model.AreaBackend}},
			want: map[string][]string{
				"LinkedIn":          {"linkedin.com/jobs/search", "geoId=102713980", "f_E=1"},
				"Internshala":       {"internshala.com"},
				"Naukri":            {"naukri.com"},
				"YC startup jobs":   {"ycombinator.com/jobs/role/software-engineer/india"},
				"Work at a Startup": {"workatastartup.com"},
			},
			without: []string{"TokyoDev"},
		},
		{
			name:    "japan full-time",
			f:       model.Filters{Countries: []string{"JP"}, Types: []model.JobType{model.TypeFullTime}},
			want:    map[string][]string{"TokyoDev": {"tokyodev.com"}, "Japan Dev": {"japan-dev.com"}},
			without: []string{"Internshala", "Naukri"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := map[string]string{}
			for _, l := range For(tt.f) {
				if _, err := url.Parse(l.URL); err != nil {
					t.Errorf("%s: bad URL %q", l.Site, l.URL)
				}
				got[l.Site] = l.URL
			}
			for site, subs := range tt.want {
				u, ok := got[site]
				if !ok {
					t.Errorf("missing %s (have %v)", site, keys(got))
					continue
				}
				for _, s := range subs {
					if !strings.Contains(u, s) {
						t.Errorf("%s URL %q missing %q", site, u, s)
					}
				}
			}
			for _, site := range tt.without {
				if _, ok := got[site]; ok {
					t.Errorf("unexpected %s", site)
				}
			}
		})
	}
}

func keys(m map[string]string) []string {
	var out []string
	for k := range m {
		out = append(out, k)
	}
	return out
}

func TestDirectories(t *testing.T) {
	in := Directories([]string{"IN"})
	us := Directories([]string{"US"})
	if len(in) != len(us)+2 {
		t.Errorf("India adds the Indian funding news sites: %d vs %d", len(in), len(us))
	}
	for _, l := range in {
		if !strings.HasPrefix(l.URL, "https://") {
			t.Errorf("%s: %s", l.Site, l.URL)
		}
	}
}

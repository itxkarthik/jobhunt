package app

import (
	"errors"
	"fmt"
	"slices"
	"strings"

	"github.com/itxkarthik/jobhunt/internal/company"
	"github.com/itxkarthik/jobhunt/internal/config"
	"github.com/itxkarthik/jobhunt/internal/model"
)

// ErrNoProfile means config.yaml has no profile section.
var ErrNoProfile = errors.New("no profile in config.yaml: add a profile section (see config.example.yaml or jobhunt profile show)")

// ProfileInput fills the fields of in that are empty from p, so flags you
// pass win over the profile.
func ProfileInput(p config.Profile, in FilterInput) FilterInput {
	fill := func(dst *[]string, src []string) {
		if len(*dst) == 0 {
			*dst = slices.Clone(src)
		}
	}
	fill(&in.Types, p.Types)
	fill(&in.Experience, p.Experience)
	fill(&in.Areas, p.Areas)
	fill(&in.Countries, p.Countries)
	fill(&in.Modes, p.Modes)
	if in.PostedWithin == "" {
		in.PostedWithin = p.PostedWithin
	}
	return in
}

// FillFromProfile copies the profile's searches into the fields of base
// that are empty, e.g. a preset that names no areas.
func FillFromProfile(base *model.Filters, prof model.Filters) {
	if len(base.Types) == 0 {
		base.Types = prof.Types
	}
	if len(base.Experience) == 0 {
		base.Experience = prof.Experience
	}
	if len(base.Areas) == 0 {
		base.Areas = prof.Areas
	}
	if len(base.Countries) == 0 {
		base.Countries = prof.Countries
	}
	if len(base.Modes) == 0 {
		base.Modes = prof.Modes
	}
	if base.PostedWithin == 0 {
		base.PostedWithin = prof.PostedWithin
	}
}

// ApplyProfile sets the profile's skips and ranking preferences on f.
func ApplyProfile(p config.Profile, f *model.Filters) error {
	for _, s := range p.SkipAreas {
		a, err := model.ParseArea(s)
		if err != nil {
			return fmt.Errorf("profile skip_areas: %w", err)
		}
		if !slices.Contains(f.SkipAreas, a) {
			f.SkipAreas = append(f.SkipAreas, a)
		}
	}
	f.SkipRequiresExperience = f.SkipRequiresExperience || p.SkipRequiresExperience
	f.SkipOnsiteAbroad = f.SkipOnsiteAbroad || p.SkipOnsiteAbroad
	f.Stack = clean(p.Stack)
	f.Projects = clean(p.Projects)
	return nil
}

func clean(xs []string) []string {
	var out []string
	for _, x := range xs {
		if x = strings.TrimSpace(x); x != "" {
			out = append(out, x)
		}
	}
	return out
}

// ProfileFilters returns the job filters a profile asks for, on top of
// the config defaults.
func ProfileFilters(def config.Defaults, p config.Profile) (model.Filters, error) {
	f, err := ParseFilters(def, ProfileInput(p, FilterInput{}))
	if err != nil {
		return f, fmt.Errorf("profile: %w", err)
	}
	return f, ApplyProfile(p, &f)
}

// ProfileCompanyInput is the companies-list filter a profile asks for.
// Fields set in in win.
func ProfileCompanyInput(p config.Profile, in company.FilterInput) company.FilterInput {
	c := p.Companies
	fill := func(dst *[]string, src []string) {
		if len(*dst) == 0 {
			*dst = slices.Clone(src)
		}
	}
	fill(&in.Stages, c.Stages)
	fill(&in.Headcounts, c.Headcounts)
	fill(&in.Countries, c.Countries)
	fill(&in.Cities, c.Cities)
	in.RemoteOK = in.RemoteOK || c.RemoteOK
	if in.FundedWithin == "" && in.FundedBefore == "" {
		in.FundedWithin, in.FundedBefore = c.FundedWithin, c.FundedBefore
	}
	return in
}

// CheckProfile validates every part of p.
func CheckProfile(def config.Defaults, p config.Profile) error {
	if _, err := ProfileFilters(def, p); err != nil {
		return err
	}
	if _, err := company.ParseFilter(ProfileCompanyInput(p, company.FilterInput{}), ParseAge); err != nil {
		return fmt.Errorf("profile companies: %w", err)
	}
	if p.MinScore < 0 || p.MinScore > 100 {
		return errors.New("profile min_score must be 0-100")
	}
	return nil
}

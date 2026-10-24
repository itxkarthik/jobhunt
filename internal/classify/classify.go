// Package classify infers job type, experience, work mode, CS areas, and visa
// sponsorship from a job's title and description using configurable keyword
// lists. Anything without a clear signal stays "unknown".
package classify

import (
	_ "embed"
	"fmt"
	"os"
	"regexp"
	"strconv"
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/itxkarthik/jobhunt/internal/model"
)

//go:embed keywords.yaml
var defaultKeywords []byte

// GenericTag marks jobs whose title names no specific area.
const GenericTag = "generic-swe"

// Rules is the YAML shape of keywords.yaml.
type Rules struct {
	JobTypes           map[string][]string `yaml:"job_types"`
	Experience         map[string][]string `yaml:"experience"`
	WorkModes          map[string][]string `yaml:"work_modes"`
	Visa               map[string][]string `yaml:"visa"`
	Areas              map[string][]string `yaml:"areas"`
	MinDescriptionHits int                 `yaml:"min_description_hits"`
	GenericTitles      []string            `yaml:"generic_titles"`
	GenericAreas       []string            `yaml:"generic_areas"`
}

type matcher []*regexp.Regexp

func compile(terms []string) matcher {
	m := make(matcher, 0, len(terms))
	for _, t := range terms {
		t = strings.ToLower(strings.TrimSpace(t))
		if t == "" {
			continue
		}
		// Custom boundaries so terms like "c++" and ".net" work.
		m = append(m, regexp.MustCompile(`(?:^|[^a-z0-9+#])`+regexp.QuoteMeta(t)+`(?:$|[^a-z0-9+#])`))
	}
	return m
}

// any reports whether any term matches s (already lowercased).
func (m matcher) any(s string) bool {
	for _, re := range m {
		if re.MatchString(s) {
			return true
		}
	}
	return false
}

// hits counts distinct terms matching s.
func (m matcher) hits(s string) int {
	n := 0
	for _, re := range m {
		if re.MatchString(s) {
			n++
		}
	}
	return n
}

type Classifier struct {
	jobTypes     map[model.JobType]matcher
	experience   map[model.Experience]matcher
	modes        map[model.WorkMode]matcher
	visaYes      matcher
	visaNo       matcher
	areas        []areaRule
	minDescHits  int
	generic      matcher
	GenericAreas []model.Area
}

type areaRule struct {
	area model.Area
	m    matcher
}

// Default returns a classifier built from the embedded keyword lists.
func Default() *Classifier {
	c, err := FromYAML(defaultKeywords)
	if err != nil {
		panic(err) // embedded file is validated by tests
	}
	return c
}

// Load returns the default classifier, or one built from path when set.
func Load(path string) (*Classifier, error) {
	if path == "" {
		return Default(), nil
	}
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	return FromYAML(b)
}

func FromYAML(b []byte) (*Classifier, error) {
	var r Rules
	if err := yaml.Unmarshal(b, &r); err != nil {
		return nil, fmt.Errorf("keywords: %w", err)
	}
	c := &Classifier{
		jobTypes:    map[model.JobType]matcher{},
		experience:  map[model.Experience]matcher{},
		modes:       map[model.WorkMode]matcher{},
		visaYes:     compile(r.Visa["yes"]),
		visaNo:      compile(r.Visa["no"]),
		minDescHits: r.MinDescriptionHits,
		generic:     compile(r.GenericTitles),
	}
	if c.minDescHits <= 0 {
		c.minDescHits = 3
	}
	for k, v := range r.JobTypes {
		t, err := model.ParseJobType(k)
		if err != nil {
			return nil, fmt.Errorf("keywords job_types: %w", err)
		}
		c.jobTypes[t] = compile(v)
	}
	for k, v := range r.Experience {
		e, err := model.ParseExperience(k)
		if err != nil {
			return nil, fmt.Errorf("keywords experience: %w", err)
		}
		c.experience[e] = compile(v)
	}
	for k, v := range r.WorkModes {
		m, err := model.ParseWorkMode(k)
		if err != nil {
			return nil, fmt.Errorf("keywords work_modes: %w", err)
		}
		c.modes[m] = compile(v)
	}
	// Iterate in canonical order so Areas come out deterministic.
	for _, a := range model.AllAreas {
		if terms, ok := r.Areas[string(a)]; ok {
			c.areas = append(c.areas, areaRule{a, compile(terms)})
		}
	}
	for k := range r.Areas {
		if _, err := model.ParseArea(k); err != nil {
			return nil, fmt.Errorf("keywords areas: %w", err)
		}
	}
	for _, k := range r.GenericAreas {
		a, err := model.ParseArea(k)
		if err != nil {
			return nil, fmt.Errorf("keywords generic_areas: %w", err)
		}
		c.GenericAreas = append(c.GenericAreas, a)
	}
	return c, nil
}

// Classify fills in fields the source left unknown. Structured values from
// the source win over keyword guesses, except that an explicit internship or
// new-grad title overrides a generic "full-time" from the source.
func (c *Classifier) Classify(j *model.Job) {
	title := strings.ToLower(j.Title)
	desc := strings.ToLower(j.Description)
	loc := strings.ToLower(j.Location)

	j.JobType = c.jobType(j.JobType, title)
	j.Experience = c.experienceLevel(j.Experience, j.JobType, title, desc)
	j.WorkMode = c.workMode(j.WorkMode, title, loc, desc)
	if j.VisaSponsorship == nil {
		j.VisaSponsorship = c.visa(desc)
	}
	if len(j.Areas) == 0 {
		j.Areas = c.Areas(title, desc)
		if len(j.Areas) == 0 && c.generic.any(title) && !hasTag(j.Tags, GenericTag) {
			j.Tags = append(j.Tags, GenericTag)
		}
	}
	if j.WorkMode == "" {
		j.WorkMode = model.ModeUnknown
	}
	if j.JobType == "" {
		j.JobType = model.TypeUnknown
	}
	if j.Experience == "" {
		j.Experience = model.ExpUnknown
	}
}

func (c *Classifier) jobType(cur model.JobType, title string) model.JobType {
	// Title signals, most specific first.
	for _, t := range []model.JobType{model.TypeInternship, model.TypeNewGrad, model.TypeContract, model.TypePartTime} {
		if c.jobTypes[t].any(title) {
			if t == model.TypeContract || t == model.TypePartTime {
				if cur != "" && cur != model.TypeUnknown && cur != model.TypeFullTime {
					return cur
				}
			}
			return t
		}
	}
	return cur
}

var yearsRe = regexp.MustCompile(`(\d{1,2})\s*(?:\+|plus)?\s*(?:-|–|to)?\s*(?:\d{1,2})?\s*\+?\s*(?:years|yrs|year)`)

func (c *Classifier) experienceLevel(cur model.Experience, t model.JobType, title, desc string) model.Experience {
	if cur != "" && cur != model.ExpUnknown {
		return cur
	}
	switch t {
	case model.TypeInternship:
		return model.ExpIntern
	case model.TypeNewGrad:
		return model.ExpEntry
	}
	for _, e := range []model.Experience{model.ExpSenior, model.ExpMid, model.ExpEntry} {
		if c.experience[e].any(title) {
			return e
		}
	}
	// Fall back to the smallest "N years" requirement in the description.
	minYears := -1
	for _, m := range yearsRe.FindAllStringSubmatch(desc, -1) {
		n, err := strconv.Atoi(m[1])
		if err != nil || n > 30 {
			continue
		}
		if minYears < 0 || n < minYears {
			minYears = n
		}
	}
	switch {
	case minYears < 0:
		return model.ExpUnknown
	case minYears <= 1:
		return model.ExpEntry
	case minYears <= 4:
		return model.ExpMid
	default:
		return model.ExpSenior
	}
}

func (c *Classifier) workMode(cur model.WorkMode, title, loc, desc string) model.WorkMode {
	if cur != "" && cur != model.ModeUnknown {
		return cur
	}
	head := title + " | " + loc
	switch {
	case c.modes[model.ModeHybrid].any(head):
		return model.ModeHybrid
	case c.modes[model.ModeRemote].any(head):
		return model.ModeRemote
	case c.modes[model.ModeOnsite].any(head):
		return model.ModeOnsite
	case c.modes[model.ModeHybrid].any(desc):
		return model.ModeHybrid
	}
	// "remote" alone in a description is too noisy ("remote team members");
	// require a stronger phrase.
	if c.modes[model.ModeRemote].hits(desc) >= 2 {
		return model.ModeRemote
	}
	if c.modes[model.ModeOnsite].any(desc) {
		return model.ModeOnsite
	}
	return model.ModeUnknown
}

func (c *Classifier) visa(desc string) *bool {
	// Negative phrases contain the positive ones ("no visa sponsorship"), so check them first.
	if c.visaNo.any(desc) {
		f := false
		return &f
	}
	if c.visaYes.any(desc) {
		t := true
		return &t
	}
	return nil
}

// Areas returns all areas whose terms appear in the title, or enough distinct
// terms in the description.
func (c *Classifier) Areas(title, desc string) []model.Area {
	var out []model.Area
	for _, r := range c.areas {
		if r.m.any(title) || r.m.hits(desc) >= c.minDescHits {
			out = append(out, r.area)
		}
	}
	return out
}

// MatchesArea reports whether j belongs to area a, counting generic SWE
// titles as members of GenericAreas.
func (c *Classifier) MatchesArea(j *model.Job, a model.Area) bool {
	if j.HasArea(a) {
		return true
	}
	if len(j.Areas) == 0 && hasTag(j.Tags, GenericTag) {
		for _, g := range c.GenericAreas {
			if g == a {
				return true
			}
		}
	}
	return false
}

func hasTag(tags []string, t string) bool {
	for _, x := range tags {
		if x == t {
			return true
		}
	}
	return false
}

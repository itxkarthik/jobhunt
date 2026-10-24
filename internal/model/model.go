// Package model defines the unified job schema and search filters shared by
// every source, the classifier, the store, and the UIs.
package model

import (
	"fmt"
	"strings"
	"time"
)

type WorkMode string

const (
	ModeRemote  WorkMode = "remote"
	ModeOnsite  WorkMode = "onsite"
	ModeHybrid  WorkMode = "hybrid"
	ModeUnknown WorkMode = "unknown"
)

var AllWorkModes = []WorkMode{ModeRemote, ModeOnsite, ModeHybrid}

type JobType string

const (
	TypeInternship JobType = "internship"
	TypeFullTime   JobType = "full-time"
	TypePartTime   JobType = "part-time"
	TypeContract   JobType = "contract"
	TypeNewGrad    JobType = "new-grad"
	TypeUnknown    JobType = "unknown"
)

var AllJobTypes = []JobType{TypeInternship, TypeFullTime, TypePartTime, TypeContract, TypeNewGrad}

type Area string

const (
	AreaBackend    Area = "backend"
	AreaFrontend   Area = "frontend"
	AreaFullStack  Area = "full-stack"
	AreaMobile     Area = "mobile"
	AreaMLAI       Area = "ml-ai"
	AreaDataEng    Area = "data-engineering"
	AreaDataSci    Area = "data-science"
	AreaDevOps     Area = "devops"
	AreaSystems    Area = "systems"
	AreaHPC        Area = "hpc-gpu"
	AreaSecurity   Area = "security"
	AreaEmbedded   Area = "embedded"
	AreaQA         Area = "qa"
	AreaResearch   Area = "research"
	AreaBlockchain Area = "blockchain"
	AreaGameDev    Area = "gamedev"
)

var AllAreas = []Area{
	AreaBackend, AreaFrontend, AreaFullStack, AreaMobile, AreaMLAI, AreaDataEng,
	AreaDataSci, AreaDevOps, AreaSystems, AreaHPC, AreaSecurity, AreaEmbedded,
	AreaQA, AreaResearch, AreaBlockchain, AreaGameDev,
}

// areaAliases lets users type the spellings from the README ("ML/AI", "sre").
var areaAliases = map[string]Area{
	"ml": AreaMLAI, "ai": AreaMLAI, "ml/ai": AreaMLAI, "ml-ai": AreaMLAI,
	"data-eng": AreaDataEng, "de": AreaDataEng,
	"ds": AreaDataSci, "sre": AreaDevOps, "cloud": AreaDevOps, "devops/sre": AreaDevOps,
	"infra": AreaSystems, "systems/infra": AreaSystems,
	"hpc": AreaHPC, "gpu": AreaHPC, "testing": AreaQA, "qa/testing": AreaQA,
	"web3": AreaBlockchain, "game-dev": AreaGameDev, "games": AreaGameDev,
	"fullstack": AreaFullStack, "full-stack": AreaFullStack,
}

type Experience string

const (
	ExpIntern  Experience = "intern"
	ExpEntry   Experience = "entry"
	ExpMid     Experience = "mid"
	ExpSenior  Experience = "senior"
	ExpUnknown Experience = "unknown"
)

var AllExperience = []Experience{ExpIntern, ExpEntry, ExpMid, ExpSenior}

// Job is the normalized record every source produces.
type Job struct {
	ID              string     `json:"id"` // "<source>:<native id>"
	Source          string     `json:"source"`
	Title           string     `json:"title"`
	Company         string     `json:"company"`
	URL             string     `json:"url"`
	Countries       []string   `json:"countries,omitempty"` // ISO alpha-2, RemoteGlobal, or Region* codes
	City            string     `json:"city,omitempty"`
	Location        string     `json:"location,omitempty"` // raw location text from the source
	WorkMode        WorkMode   `json:"work_mode"`
	JobType         JobType    `json:"job_type"`
	Areas           []Area     `json:"areas,omitempty"`
	Experience      Experience `json:"experience"`
	SalaryMin       *float64   `json:"salary_min,omitempty"`
	SalaryMax       *float64   `json:"salary_max,omitempty"`
	Currency        string     `json:"currency,omitempty"`
	VisaSponsorship *bool      `json:"visa_sponsorship,omitempty"`
	PostedAt        time.Time  `json:"posted_at"`
	Description     string     `json:"description,omitempty"`
	Tags            []string   `json:"tags,omitempty"`

	// Populated after fetching.
	Sources     []string  `json:"sources,omitempty"`
	FirstSeenAt time.Time `json:"first_seen_at,omitempty"`
	Score       float64   `json:"score,omitempty"`
	New         bool      `json:"new,omitempty"`
}

// HasArea reports whether the job was tagged with a.
func (j *Job) HasArea(a Area) bool {
	for _, x := range j.Areas {
		if x == a {
			return true
		}
	}
	return false
}

// Filters is what the user asked for. Sources use it to build server-side
// queries where they can; everything is re-applied centrally afterwards.
type Filters struct {
	Countries    []string      `json:"countries,omitempty" yaml:"countries,omitempty"`
	Modes        []WorkMode    `json:"modes,omitempty" yaml:"modes,omitempty"`
	Types        []JobType     `json:"types,omitempty" yaml:"types,omitempty"`
	Areas        []Area        `json:"areas,omitempty" yaml:"areas,omitempty"`
	Keywords     []string      `json:"keywords,omitempty" yaml:"keywords,omitempty"`
	Exclude      []string      `json:"exclude,omitempty" yaml:"exclude,omitempty"`
	Experience   []Experience  `json:"experience,omitempty" yaml:"experience,omitempty"`
	PostedWithin time.Duration `json:"posted_within,omitempty" yaml:"posted_within,omitempty"`
	VisaOnly     bool          `json:"visa_only,omitempty" yaml:"visa_only,omitempty"`
	MinSalary    float64       `json:"min_salary,omitempty" yaml:"min_salary,omitempty"`
	Currency     string        `json:"currency,omitempty" yaml:"currency,omitempty"`

	StrictCountry  bool `json:"strict_country,omitempty" yaml:"strict_country,omitempty"`
	IncludeUnknown bool `json:"include_unknown,omitempty" yaml:"include_unknown,omitempty"`
}

func (f Filters) WantsType(t JobType) bool {
	for _, x := range f.Types {
		if x == t {
			return true
		}
	}
	return false
}

func (f Filters) WantsMode(m WorkMode) bool {
	if len(f.Modes) == 0 {
		return true
	}
	for _, x := range f.Modes {
		if x == m {
			return true
		}
	}
	return false
}

// OnlyRemote is true when the user restricted modes to remote.
func (f Filters) OnlyRemote() bool {
	return len(f.Modes) == 1 && f.Modes[0] == ModeRemote
}

func (f Filters) WantsCountry(c string) bool {
	for _, x := range f.Countries {
		if x == c {
			return true
		}
	}
	return false
}

func ParseWorkMode(s string) (WorkMode, error) {
	s = norm(s)
	if s == "on-site" || s == "office" {
		s = "onsite"
	}
	for _, m := range AllWorkModes {
		if string(m) == s {
			return m, nil
		}
	}
	return "", fmt.Errorf("unknown mode %q (want remote, onsite, hybrid)", s)
}

func ParseJobType(s string) (JobType, error) {
	s = norm(s)
	switch s {
	case "intern":
		s = "internship"
	case "fulltime", "full_time":
		s = "full-time"
	case "parttime", "part_time":
		s = "part-time"
	case "newgrad", "new_grad", "graduate":
		s = "new-grad"
	}
	for _, t := range AllJobTypes {
		if string(t) == s {
			return t, nil
		}
	}
	return "", fmt.Errorf("unknown type %q (want internship, full-time, part-time, contract, new-grad)", s)
}

func ParseArea(s string) (Area, error) {
	s = norm(s)
	if a, ok := areaAliases[s]; ok {
		return a, nil
	}
	for _, a := range AllAreas {
		if string(a) == s {
			return a, nil
		}
	}
	return "", fmt.Errorf("unknown area %q", s)
}

func ParseExperience(s string) (Experience, error) {
	s = norm(s)
	if s == "junior" || s == "fresher" {
		s = "entry"
	}
	for _, e := range AllExperience {
		if string(e) == s {
			return e, nil
		}
	}
	return "", fmt.Errorf("unknown experience %q (want intern, entry, mid, senior)", s)
}

func norm(s string) string {
	return strings.ReplaceAll(strings.ToLower(strings.TrimSpace(s)), " ", "-")
}

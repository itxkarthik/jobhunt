// Package classify infers job type, experience, work mode, CS areas, and visa
// sponsorship from a job's title and description using configurable keyword
// lists. Anything without a clear signal stays "unknown".
package classify

import (
	"crypto/sha256"
	_ "embed"
	"encoding/hex"
	"fmt"
	"os"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"unicode/utf8"

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
	RequiresExperience map[string][]string `yaml:"requires_experience"`
	Pay                map[string][]string `yaml:"pay"`
	Areas              map[string][]string `yaml:"areas"`
	MinDescriptionHits int                 `yaml:"min_description_hits"`
	GenericTitles      []string            `yaml:"generic_titles"`
	GenericAreas       []string            `yaml:"generic_areas"`
}

// All rule lists share one lexicon, so each text is scanned once per job
// and every list just checks which of its terms were marked.
type matcher []int // term ids in Classifier.lex

// marks records which lexicon terms occur in one text.
type marks []bool

func (m matcher) any(mk marks) bool {
	for _, id := range m {
		if mk[id] {
			return true
		}
	}
	return false
}

func (m matcher) hits(mk marks) int {
	n := 0
	for _, id := range m {
		if mk[id] {
			n++
		}
	}
	return n
}

func (c *Classifier) compile(terms []string) matcher {
	var m matcher
	for _, t := range terms {
		if id := c.lex.add(t); id >= 0 {
			m = append(m, id)
		}
	}
	return m
}

type Classifier struct {
	jobTypes     map[model.JobType]matcher
	experience   map[model.Experience]matcher
	modes        map[model.WorkMode]matcher
	visaYes      matcher
	visaNo       matcher
	reqExpYes    matcher
	reqExpNo     matcher
	unpaid       matcher
	unpaidTitle  matcher
	paid         matcher
	eitherPaid   matcher
	payWord      matcher
	amountHints  []hint
	expWord      matcher // "experience"
	yearWord     matcher // "year", "years", "yrs"
	areas        []areaRule
	minDescHits  int
	generic      matcher
	GenericAreas []model.Area
	lex          *TermSet
	version      string
}

// logicVersion changes whenever Classify's code changes what it decides
// for the same rules, so results cached under the old logic are redone.
const logicVersion = "3"

// Version identifies the classifier's rules and logic: two classifiers
// with the same Version classify every job the same way.
func (c *Classifier) Version() string { return c.version }

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
	b, err := os.ReadFile(path) //nolint:gosec // G304: the user's own keywords file from config
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
		minDescHits: r.MinDescriptionHits,
		lex:         NewTermSet(nil),
	}
	sum := sha256.Sum256(append([]byte(logicVersion+"\n"), b...))
	c.version = hex.EncodeToString(sum[:8])
	c.visaYes = c.compile(r.Visa["yes"])
	c.visaNo = c.compile(r.Visa["no"])
	c.reqExpYes = c.compile(r.RequiresExperience["yes"])
	c.reqExpNo = c.compile(r.RequiresExperience["no"])
	c.unpaid = c.compile(r.Pay["unpaid"])
	c.unpaidTitle = c.compile(r.Pay["title_unpaid"])
	c.paid = c.compile(r.Pay["paid"])
	c.eitherPaid = c.compile(r.Pay["either"])
	c.payWord = c.compile(r.Pay["words"])
	for _, h := range r.Pay["amount_hints"] {
		if id := c.lex.add(h); id >= 0 {
			c.amountHints = append(c.amountHints, hint{id, h})
		}
	}
	c.expWord = c.compile([]string{"experience"})
	c.yearWord = c.compile([]string{"year", "years", "yrs"})
	c.generic = c.compile(r.GenericTitles)
	if c.minDescHits <= 0 {
		c.minDescHits = 3
	}
	for k, v := range r.JobTypes {
		t, err := model.ParseJobType(k)
		if err != nil {
			return nil, fmt.Errorf("keywords job_types: %w", err)
		}
		c.jobTypes[t] = c.compile(v)
	}
	for k, v := range r.Experience {
		e, err := model.ParseExperience(k)
		if err != nil {
			return nil, fmt.Errorf("keywords experience: %w", err)
		}
		c.experience[e] = c.compile(v)
	}
	for k, v := range r.WorkModes {
		m, err := model.ParseWorkMode(k)
		if err != nil {
			return nil, fmt.Errorf("keywords work_modes: %w", err)
		}
		c.modes[m] = c.compile(v)
	}
	// Iterate in canonical order so Areas come out deterministic.
	for _, a := range model.AllAreas {
		if terms, ok := r.Areas[string(a)]; ok {
			c.areas = append(c.areas, areaRule{a, c.compile(terms)})
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
	title := c.lex.mark(Tokenize(j.Title))
	// One lower-cased copy of the description serves the tokens and the
	// "N years" checks.
	lowerDesc := strings.ToLower(j.Description)
	desc := c.lex.mark(tokenizeLower(lowerDesc))
	loc := c.lex.mark(Tokenize(j.Location))

	j.JobType = c.jobType(j.JobType, title)
	if !j.HasSalary() && j.Paid == nil {
		c.pay(j, title, desc, lowerDesc)
	}
	j.Experience = c.experienceLevel(j.Experience, j.JobType, title, lowerDesc)
	j.WorkMode = c.workMode(j.WorkMode, title, loc, desc)
	if j.VisaSponsorship == nil {
		j.VisaSponsorship = c.visa(desc)
	}
	if len(j.Areas) == 0 {
		j.Areas = c.areasTok(title, desc)
		if len(j.Areas) == 0 && c.generic.any(title) && !hasTag(j.Tags, GenericTag) {
			j.Tags = append(j.Tags, GenericTag)
		}
	}
	if c.requiresExperience(desc, lowerDesc) && !hasTag(j.Tags, RequiresExperienceTag) {
		j.Tags = append(j.Tags, RequiresExperienceTag)
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

// RequiresExperienceTag marks postings that ask for prior work or
// internship experience.
const RequiresExperienceTag = "requires-experience"

// expYearsRe reads "2+ years of experience", "1-2 years of relevant
// industry experience": a number of years followed closely by "experience".
var expYearsRe = regexp.MustCompile(`(\d{1,2})\s*(?:\+|plus)?\s*(?:(?:-|–|to)\s*\d{1,2}\s*)?\+?\s*(?:years?|yrs)\b(?:\s+of)?(?:\s+[a-z-]+){0,3}?\s+experience`)

func (c *Classifier) requiresExperience(desc marks, lowerDesc string) bool {
	if c.reqExpNo.any(desc) {
		return false
	}
	if c.reqExpYes.any(desc) {
		return true
	}
	// The regex runs only on descriptions with both words in them.
	if !c.expWord.any(desc) || !c.yearWord.any(desc) {
		return false
	}
	for _, snip := range yearSnippets(lowerDesc) {
		for _, m := range expYearsRe.FindAllStringSubmatch(snip, -1) {
			if n, err := strconv.Atoi(m[1]); err == nil && n >= 1 && n <= 30 {
				return true
			}
		}
	}
	return false
}

// Every match of expYearsRe and yearsRe contains "year" or "yrs". Before
// it comes a number range of at most about a dozen non-space bytes ("12
// plus – 15 +"); after it, expYearsRe reads at most five more words ("of",
// three words, "experience"). Any amount of whitespace may separate the
// parts, so the snippets are bounded by non-space bytes and words, not by
// a byte count. Running the regexes over these snippets finds the same
// phrases as running them over a whole description, which is thousands of
// characters on most postings and made them most of a search's CPU time.
const (
	snipBefore = 32 // non-space bytes kept before the word; a match needs about 12
	snipWords  = 5  // whole words kept after the word: "of", three words, "experience"
)

// isSpace is the regexp package's \s: ASCII space, \t, \n, \f and \r.
func isSpace(b byte) bool {
	return b == ' ' || b == '\t' || b == '\n' || b == '\f' || b == '\r'
}

// yearSnippets returns the parts of lower around each "year" and "yrs",
// with overlapping parts merged. A part never starts inside a number or a
// UTF-8 sequence.
func yearSnippets(lower string) []string {
	type span struct{ lo, hi int }
	var spans []span
	add := func(word string) {
		for off := 0; ; {
			i := strings.Index(lower[off:], word)
			if i < 0 {
				return
			}
			i += off
			// Back over snipBefore non-space bytes, then to the start of a
			// number or UTF-8 sequence.
			lo := i
			for n := 0; lo > 0 && n < snipBefore; {
				lo--
				if !isSpace(lower[lo]) {
					n++
				}
			}
			for lo > 0 && (isDigit(lower[lo-1]) || !utf8.RuneStart(lower[lo])) {
				lo--
			}
			// Forward to the end of the word holding it, then over
			// snipWords more words and the whitespace between them.
			hi := i + len(word)
			for hi < len(lower) && !isSpace(lower[hi]) {
				hi++
			}
			for range snipWords {
				for hi < len(lower) && isSpace(lower[hi]) {
					hi++
				}
				for hi < len(lower) && !isSpace(lower[hi]) {
					hi++
				}
			}
			spans = append(spans, span{lo, hi})
			off = i + len(word)
		}
	}
	add("year")
	add("yrs")
	if len(spans) == 0 {
		return nil
	}
	slices.SortFunc(spans, func(a, b span) int { return a.lo - b.lo })
	merged := spans[:1]
	for _, sp := range spans[1:] {
		if last := &merged[len(merged)-1]; sp.lo <= last.hi {
			last.hi = max(last.hi, sp.hi)
		} else {
			merged = append(merged, sp)
		}
	}
	out := make([]string, len(merged))
	for i, sp := range merged {
		out[i] = lower[sp.lo:sp.hi]
	}
	return out
}

func isDigit(b byte) bool { return '0' <= b && b <= '9' }

func (c *Classifier) jobType(cur model.JobType, title marks) model.JobType {
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

func (c *Classifier) experienceLevel(cur model.Experience, t model.JobType, title marks, lowerDesc string) model.Experience {
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
	for _, snip := range yearSnippets(lowerDesc) {
		for _, m := range yearsRe.FindAllStringSubmatch(snip, -1) {
			n, err := strconv.Atoi(m[1])
			if err != nil || n > 30 {
				continue
			}
			if minYears < 0 || n < minYears {
				minYears = n
			}
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

func (c *Classifier) workMode(cur model.WorkMode, title, loc, desc marks) model.WorkMode {
	if cur != "" && cur != model.ModeUnknown {
		return cur
	}
	head := func(m model.WorkMode) bool { return c.modes[m].any(title) || c.modes[m].any(loc) }
	switch {
	case head(model.ModeHybrid):
		return model.ModeHybrid
	case head(model.ModeRemote):
		return model.ModeRemote
	case head(model.ModeOnsite):
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

func (c *Classifier) visa(desc marks) *bool {
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
	return c.areasTok(c.lex.mark(Tokenize(title)), c.lex.mark(Tokenize(desc)))
}

func (c *Classifier) areasTok(title, desc marks) []model.Area {
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
		if slices.Contains(c.GenericAreas, a) {
			return true
		}
	}
	return false
}

func hasTag(tags []string, t string) bool {
	return slices.Contains(tags, t)
}

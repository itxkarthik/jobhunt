// Package mcpserver exposes jobhunt to AI agents over the Model Context
// Protocol: `jobhunt mcp` serves it on stdin/stdout, with no network
// listener.
//
// Agents can search jobs and read and update the companies list. Every
// argument an agent sends is untrusted: it is validated and sanitized like
// fetched job text before anything is stored, a contact or funding round
// is only kept with the URL where it was published, and errors other than
// validation messages reach the agent as a generic "internal error" (the
// details go to stderr). No tool returns config values, keys or tokens.
package mcpserver

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/itxkarthik/jobhunt/internal/app"
	"github.com/itxkarthik/jobhunt/internal/company"
	"github.com/itxkarthik/jobhunt/internal/config"
	"github.com/itxkarthik/jobhunt/internal/feed"
	"github.com/itxkarthik/jobhunt/internal/model"
	"github.com/itxkarthik/jobhunt/internal/search"
	"github.com/itxkarthik/jobhunt/internal/sources"
	"github.com/itxkarthik/jobhunt/internal/store"
)

// Options configures the server.
type Options struct {
	App      *app.App
	ReadOnly bool // only the read tools
	Version  string
	Log      *slog.Logger // stderr; never stdout, which carries the protocol
	// CheckBoard vets a new board before add_board saves it; default
	// App.CheckBoard (one live request). Tests use a fake.
	CheckBoard app.BoardChecker
}

const (
	maxJobs     = 100
	defaultJobs = 20
	maxDescList = 600
	maxNote     = 2000
)

// New builds the MCP server.
func New(o Options) *mcp.Server {
	if o.Log == nil {
		o.Log = slog.New(slog.DiscardHandler)
	}
	if o.CheckBoard == nil {
		o.CheckBoard = o.App.CheckBoard
	}
	h := &handlers{o: o}
	s := mcp.NewServer(&mcp.Implementation{Name: "jobhunt", Version: o.Version}, &mcp.ServerOptions{
		Instructions: instructions,
	})
	ro := &mcp.ToolAnnotations{ReadOnlyHint: true}
	mcp.AddTool(s, &mcp.Tool{Name: "search_jobs", Annotations: ro, Description: "Search every enabled job source with the same filters as `jobhunt search` " +
		"(cached results are reused, so repeat calls are cheap). Empty countries/modes/types/areas take the user's config defaults; send [\"any\"] to lift one. " +
		"Returns ranked jobs with descriptions cut short; use get_job for the full text."}, h.searchJobs)
	mcp.AddTool(s, &mcp.Tool{Name: "get_job", Annotations: ro, Description: "One job by key (or a unique key prefix) from earlier searches, with its full description and tracker status."}, h.getJob)
	mcp.AddTool(s, &mcp.Tool{Name: "list_companies", Annotations: ro, Description: "The companies list: funded startups and other companies the user watches, " +
		"newest funding first, with open job counts (jobs seen in searches in the last 14 days). Filter by stage, headcount, country, tags, funding window, contacts or hiring level."}, h.listCompanies)
	mcp.AddTool(s, &mcp.Tool{Name: "get_company", Annotations: ro, Description: "One company by name, key or key prefix, with its rounds, boards, contacts (each with its source) and open jobs."}, h.getCompany)
	mcp.AddTool(s, &mcp.Tool{Name: "get_profile", Annotations: ro, Description: "What the user is looking for, from their config: roles and levels, areas, stack in priority order, " +
		"project keywords, countries, and which companies are worth their time (stage, headcount, countries, cities, remote hiring, funding window). " +
		"Use it to decide what to research. set is false when there is no profile."}, h.getProfile)
	mcp.AddTool(s, &mcp.Tool{Name: "list_sources", Annotations: ro, Description: "Job sources with whether each is enabled and ready and its last run. Never includes keys."}, h.listSources)
	if o.ReadOnly {
		return s
	}
	mcp.AddTool(s, &mcp.Tool{Name: "upsert_company", Description: "Add a company or update it with what you learned. Only fields you send change; rounds, contacts, boards and tags merge. " +
		"Every round and contact needs source_url: the page where it was published. Contacts only when the person published them; never guess an address. " +
		"Mark guessed headcounts with headcount_source \"estimate\"; leave unknowns out."}, h.upsertCompany)
	mcp.AddTool(s, &mcp.Tool{Name: "add_contact", Description: "Add a contact to a company: a person's name and role, email or profile URL, and source_url, the page where they published it. " +
		"Never guess an email; jobhunt never sends email."}, h.addContact)
	mcp.AddTool(s, &mcp.Tool{Name: "add_board", Description: "Add a company's job board (ats: greenhouse, lever, ashby, workable or recruitee; slug as in the board's API URL). " +
		"The board is fetched once and refused if it is dead or empty; afterwards every search includes it."}, h.addBoard)
	mcp.AddTool(s, &mcp.Tool{Name: "track_job", Description: "Set a job's tracker status (saved, applied, oa, interview, offer, rejected) with an optional note. " +
		"Use saved for leads; only use applied when the user says they applied."}, h.trackJob)
	mcp.AddTool(s, &mcp.Tool{Name: "add_note", Description: "Append a dated note to a company's notes, e.g. a review summary or why it fits, with its source."}, h.addNote)
	return s
}

const instructions = `jobhunt finds jobs and tracks companies for the user. Read before you write:
call list_companies or get_company to see what is known, then fill gaps.
Every fact you store needs the URL you found it at. Store contacts only when
the person published them; never guess or construct an email address. Do not
crawl sites whose terms forbid automated access (LinkedIn, Crunchbase,
Glassdoor, Wellfound): cite what search results show instead. Never apply,
send email or contact anyone for the user; drafting is fine.`

// Run serves on stdin/stdout until ctx ends or the client disconnects.
func Run(ctx context.Context, o Options) error {
	return New(o).Run(ctx, &mcp.StdioTransport{})
}

type handlers struct{ o Options }

// userError is a validation message safe to show the agent.
type userError struct{ error }

func invalid(format string, a ...any) error { return userError{fmt.Errorf(format, a...)} }

// public turns an internal failure into a generic message, logging the
// details; validation errors pass through.
func (h *handlers) public(tool string, err error) error {
	if err == nil {
		return nil
	}
	if ue, ok := errors.AsType[userError](err); ok {
		return ue.error
	}
	if ie, ok := errors.AsType[*app.InvalidError](err); ok {
		return ie
	}
	if errors.Is(err, store.ErrNoCompany) || errors.Is(err, store.ErrNoJob) || errors.Is(err, store.ErrAmbiguousKey) || errors.Is(err, store.ErrNotTracked) {
		return err
	}
	h.o.Log.Error("mcp tool failed", "tool", tool, "err", err)
	return errors.New("internal error")
}

// ---- search_jobs

type SearchInput struct {
	app.FilterInput
	Limit      int    `json:"limit,omitempty" jsonschema:"maximum jobs to return, 1-100 (default 20)"`
	NewOnly    bool   `json:"new_only,omitempty" jsonschema:"only jobs never seen in an earlier search"`
	Sort       string `json:"sort,omitempty" jsonschema:"score (default) or date"`
	Refresh    bool   `json:"refresh,omitempty" jsonschema:"ignore cached results (slower; spends paid sources' quota)"`
	ShowHidden bool   `json:"show_hidden,omitempty" jsonschema:"include jobs the user hid"`
}

type JobBrief struct {
	Key         string            `json:"key"`
	Title       string            `json:"title"`
	Company     string            `json:"company"`
	Location    string            `json:"location,omitempty"`
	Countries   []string          `json:"countries,omitempty"`
	Mode        model.WorkMode    `json:"mode"`
	Type        model.JobType     `json:"type"`
	Experience  model.Experience  `json:"experience"`
	Areas       []model.Area      `json:"areas,omitempty"`
	Score       float64           `json:"score"`
	URL         string            `json:"url"`
	PostedAt    string            `json:"posted_at,omitempty"`
	New         bool              `json:"new,omitempty"`
	Sources     []string          `json:"sources,omitempty"`
	Description string            `json:"description,omitempty"`
	Tracked     store.Status      `json:"tracked,omitempty"`
	SalaryMin   *float64          `json:"salary_min,omitempty"`
	SalaryMax   *float64          `json:"salary_max,omitempty"`
	Currency    string            `json:"currency,omitempty"`
	ScoreParts  []model.ScorePart `json:"score_parts,omitempty"`
}

type SearchOutput struct {
	Jobs    []JobBrief         `json:"jobs"`
	Matched int                `json:"matched"`
	Fetched int                `json:"fetched"`
	Sources []feed.SourceEvent `json:"sources"`
	Note    string             `json:"note,omitempty"`
}

func (h *handlers) searchJobs(ctx context.Context, _ *mcp.CallToolRequest, in SearchInput) (*mcp.CallToolResult, SearchOutput, error) {
	var out SearchOutput
	a := h.o.App
	f, err := app.ParseFilters(a.Config.Defaults, in.FilterInput)
	if err != nil {
		return nil, out, invalid("%v", err)
	}
	switch {
	case in.Limit == 0:
		in.Limit = defaultJobs
	case in.Limit < 0 || in.Limit > maxJobs:
		return nil, out, invalid("limit must be 1-%d", maxJobs)
	}
	if in.Sort != "" && in.Sort != "score" && in.Sort != "date" {
		return nil, out, invalid("sort must be score or date")
	}
	eng := a.Engine()
	eng.Refresh, eng.NewOnly, eng.ShowHidden, eng.SortBy = in.Refresh, in.NewOnly, in.ShowHidden, in.Sort
	res, err := eng.Run(ctx, f)
	if err != nil {
		return nil, out, h.public("search_jobs", err)
	}
	statuses, err := a.Store.StatusMap(ctx)
	if err != nil {
		return nil, out, h.public("search_jobs", err)
	}
	out.Matched, out.Fetched = len(res.Jobs), res.Total
	for _, r := range res.Reports {
		ev := feed.SourceOf(r)
		ev.Error = sources.RedactURLs(ev.Error) // a source's error may quote its request URL
		out.Sources = append(out.Sources, ev)
	}
	out.Jobs = []JobBrief{}
	for i := range res.Jobs[:min(in.Limit, len(res.Jobs))] {
		out.Jobs = append(out.Jobs, brief(&res.Jobs[i], statuses[res.Jobs[i].Key], maxDescList))
	}
	if len(res.Jobs) > in.Limit {
		out.Note = fmt.Sprintf("showing %d of %d; raise limit or narrow the filters", in.Limit, len(res.Jobs))
	}
	return nil, out, nil
}

func brief(j *model.Job, st store.Status, maxDesc int) JobBrief {
	b := JobBrief{
		Key: j.Key, Title: j.Title, Company: j.Company, Location: j.Location, Countries: j.Countries,
		Mode: j.WorkMode, Type: j.JobType, Experience: j.Experience, Areas: j.Areas, Score: j.Score,
		URL: j.URL, New: j.New, SalaryMin: j.SalaryMin, SalaryMax: j.SalaryMax, Currency: j.Currency, Sources: j.Sources, Tracked: st, ScoreParts: j.ScoreParts,
		Description: clip(j.Description, maxDesc),
	}
	if b.Location == "" {
		b.Location = j.City
	}
	if !j.PostedAt.IsZero() {
		b.PostedAt = j.PostedAt.UTC().Format(time.RFC3339)
	}
	return b
}

func clip(s string, n int) string {
	if n <= 0 || utf8.RuneCountInString(s) <= n {
		return s
	}
	r := []rune(s)
	return string(r[:n]) + "…"
}

// ---- get_job

type KeyInput struct {
	Key string `json:"key" jsonschema:"job key or a unique prefix of it (at least 4 characters)"`
}

func (h *handlers) getJob(ctx context.Context, _ *mcp.CallToolRequest, in KeyInput) (*mcp.CallToolResult, JobBrief, error) {
	key, err := h.resolveJob(ctx, in.Key)
	if err != nil {
		return nil, JobBrief{}, h.public("get_job", err)
	}
	j, err := h.o.App.Store.Job(ctx, key)
	if err != nil {
		return nil, JobBrief{}, h.public("get_job", err)
	}
	if j == nil {
		return nil, JobBrief{}, store.ErrNoJob
	}
	st, err := h.o.App.Store.StatusMap(ctx)
	if err != nil {
		return nil, JobBrief{}, h.public("get_job", err)
	}
	return nil, brief(j, st[key], 0), nil
}

func (h *handlers) resolveJob(ctx context.Context, key string) (string, error) {
	key = strings.ToLower(strings.TrimSpace(key))
	if len(key) < 4 {
		return "", invalid("key must be at least 4 characters")
	}
	return h.o.App.Store.ResolveKey(ctx, key)
}

// ---- companies

type ListCompaniesInput struct {
	app.CompanyQuery
}

type ListCompaniesOutput struct {
	Companies []app.CompanyView `json:"companies"`
}

func (h *handlers) listCompanies(ctx context.Context, _ *mcp.CallToolRequest, in ListCompaniesInput) (*mcp.CallToolResult, ListCompaniesOutput, error) {
	if _, err := company.ParseFilter(in.FilterInput, app.ParseAge); err != nil {
		return nil, ListCompaniesOutput{}, invalid("%v", err)
	}
	for _, l := range in.Hiring {
		if _, err := model.ParseExperience(l); err != nil && l != app.Any {
			return nil, ListCompaniesOutput{}, invalid("%v", err)
		}
	}
	if in.Limit < 0 || in.Limit > 500 {
		return nil, ListCompaniesOutput{}, invalid("limit must be 0-500")
	}
	if in.Limit == 0 {
		in.Limit = 100
	}
	cs, err := app.Companies(ctx, h.o.App.Store, in.CompanyQuery)
	if err != nil {
		return nil, ListCompaniesOutput{}, h.public("list_companies", err)
	}
	if cs == nil {
		cs = []app.CompanyView{}
	}
	for i := range cs {
		for j := range cs[i].Jobs {
			cs[i].Jobs[j].Description = clip(cs[i].Jobs[j].Description, maxDescList)
		}
	}
	return nil, ListCompaniesOutput{Companies: cs}, nil
}

type NameInput struct {
	Name string `json:"name" jsonschema:"company name, key or key prefix"`
}

func (h *handlers) getCompany(ctx context.Context, _ *mcp.CallToolRequest, in NameInput) (*mcp.CallToolResult, app.CompanyView, error) {
	c, err := app.FindCompany(ctx, h.o.App.Store, in.Name)
	if err != nil {
		return nil, app.CompanyView{}, h.public("get_company", err)
	}
	jobs, err := h.o.App.Store.CompanyJobs(ctx, []string{c.Key}, time.Now().Add(-app.OpenJobWindow))
	if err != nil {
		return nil, app.CompanyView{}, h.public("get_company", err)
	}
	js := jobs[c.Key]
	for i := range js {
		js[i].Description = clip(js[i].Description, maxDescList)
	}
	return nil, app.CompanyView{Company: *c, OpenJobs: len(js), Jobs: js}, nil
}

// CompanyInput is what an agent may set on a company.
type CompanyInput = app.CompanyInput

type CompanyOutput struct {
	Company model.Company `json:"company"`
	Created bool          `json:"created"`
}

func (h *handlers) upsertCompany(ctx context.Context, _ *mcp.CallToolRequest, in CompanyInput) (*mcp.CallToolResult, CompanyOutput, error) {
	c, rm, err := in.Company("agent", time.Now())
	if err != nil {
		return nil, CompanyOutput{}, invalid("%v", err)
	}
	return h.save(ctx, "upsert_company", c, rm, false)
}

// save validates c, checks new boards, then stores it.
func (h *handlers) save(ctx context.Context, tool string, c model.Company, rm company.Removal, mustExist bool) (*mcp.CallToolResult, CompanyOutput, error) {
	if mustExist {
		old, err := app.FindCompany(ctx, h.o.App.Store, c.Name)
		if err != nil {
			return nil, CompanyOutput{}, h.public(tool, err)
		}
		c.Name = old.Name
	}
	if len(c.Boards) > 0 { // check boards only for a valid update
		probe := c
		if err := company.Clean(&probe); err != nil {
			return nil, CompanyOutput{}, invalid("%v", err)
		}
	}
	if err := app.AddBoards(ctx, h.o.App.Store, c, h.o.CheckBoard); err != nil {
		if be, ok := errors.AsType[*app.BoardError](err); ok {
			return nil, CompanyOutput{}, invalid("%v", be)
		}
		return nil, CompanyOutput{}, h.public(tool, err)
	}
	got, created, err := app.UpsertCompany(ctx, h.o.App.Store, c, rm)
	if err != nil {
		return nil, CompanyOutput{}, h.public(tool, err)
	}
	if len(c.Boards) > 0 {
		if err := h.o.App.RefreshBoards(ctx); err != nil {
			h.o.Log.Warn("refreshing boards", "err", err)
		}
	}
	return nil, CompanyOutput{Company: got, Created: created}, nil
}

type ContactInput struct {
	Company   string `json:"company" jsonschema:"company name, key or key prefix (the company must exist)"`
	Name      string `json:"name,omitempty"`
	Role      string `json:"role,omitempty" jsonschema:"e.g. CEO, Founder, CTO"`
	Email     string `json:"email,omitempty" jsonschema:"only as the person published it"`
	URL       string `json:"url,omitempty" jsonschema:"their profile, e.g. an X or personal page"`
	SourceURL string `json:"source_url" jsonschema:"the page where the person published this contact"`
}

func (h *handlers) addContact(ctx context.Context, _ *mcp.CallToolRequest, in ContactInput) (*mcp.CallToolResult, CompanyOutput, error) {
	c := model.Company{Name: in.Company, Sources: []string{"agent"}, Contacts: []model.Contact{{
		Name: in.Name, Role: in.Role, Email: in.Email, URL: in.URL, SourceURL: in.SourceURL,
	}}}
	return h.save(ctx, "add_contact", c, company.Removal{}, true)
}

type BoardInput struct {
	Company string `json:"company" jsonschema:"company name; added if new"`
	ATS     string `json:"ats" jsonschema:"greenhouse, lever, ashby, workable or recruitee"`
	Slug    string `json:"slug" jsonschema:"the board id in the ATS API URL, e.g. acme in jobs.ashbyhq.com/acme"`
	Country string `json:"country,omitempty" jsonschema:"where the company hires, if known"`
	Remote  bool   `json:"remote,omitempty" jsonschema:"the company hires remotely"`
}

func (h *handlers) addBoard(ctx context.Context, _ *mcp.CallToolRequest, in BoardInput) (*mcp.CallToolResult, CompanyOutput, error) {
	b, err := company.ParseBoard(in.ATS + ":" + in.Slug)
	if err != nil {
		return nil, CompanyOutput{}, invalid("%v", err)
	}
	c := model.Company{Name: in.Company, Country: in.Country, Remote: in.Remote, Boards: []model.Board{b}, Sources: []string{"agent"}}
	return h.save(ctx, "add_board", c, company.Removal{}, false)
}

type TrackInput struct {
	Key    string `json:"key" jsonschema:"job key or unique prefix"`
	Status string `json:"status" jsonschema:"saved, applied, oa, interview, offer or rejected"`
	Note   string `json:"note,omitempty"`
}

type TrackOutput struct {
	Key    string       `json:"key"`
	Status store.Status `json:"status"`
}

func (h *handlers) trackJob(ctx context.Context, _ *mcp.CallToolRequest, in TrackInput) (*mcp.CallToolResult, TrackOutput, error) {
	st, err := store.ParseStatus(in.Status)
	if err != nil {
		return nil, TrackOutput{}, invalid("%v", err)
	}
	note := sources.SafeText(strings.TrimSpace(in.Note), true)
	if utf8.RuneCountInString(note) > maxNote {
		return nil, TrackOutput{}, invalid("note is longer than %d characters", maxNote)
	}
	key, err := h.resolveJob(ctx, in.Key)
	if err != nil {
		return nil, TrackOutput{}, h.public("track_job", err)
	}
	if err := h.o.App.Store.SetStatus(ctx, key, st, note); err != nil {
		return nil, TrackOutput{}, h.public("track_job", err)
	}
	return nil, TrackOutput{Key: key, Status: st}, nil
}

type NoteInput struct {
	Company   string `json:"company" jsonschema:"company name, key or key prefix (the company must exist)"`
	Note      string `json:"note"`
	SourceURL string `json:"source_url,omitempty" jsonschema:"where the note's facts came from"`
}

func (h *handlers) addNote(ctx context.Context, _ *mcp.CallToolRequest, in NoteInput) (*mcp.CallToolResult, CompanyOutput, error) {
	note := strings.TrimSpace(sources.SafeText(in.Note, true))
	if note == "" || len(note) > maxNote {
		return nil, CompanyOutput{}, invalid("note must be 1-%d bytes", maxNote)
	}
	if in.SourceURL != "" {
		u := sources.SafeURL(in.SourceURL)
		if u == "" {
			return nil, CompanyOutput{}, invalid("source_url must be an http(s) URL")
		}
		note += " (" + u + ")"
	}
	got, err := app.AppendNote(ctx, h.o.App.Store, in.Company, note, "agent", time.Now())
	if err != nil {
		return nil, CompanyOutput{}, h.public("add_note", err)
	}
	return nil, CompanyOutput{Company: got}, nil
}

// ---- list_sources

type SourceInfo struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	Enabled     bool   `json:"enabled"`
	Ready       bool   `json:"ready"`
	NotReady    string `json:"not_ready,omitempty"`
	LastRun     string `json:"last_run,omitempty"`
	LastStatus  string `json:"last_status,omitempty"`
	LastCount   int    `json:"last_count,omitempty"`
	LastError   string `json:"last_error,omitempty"`
}

type SourcesOutput struct {
	Sources []SourceInfo `json:"sources"`
	Boards  int          `json:"boards"`
}

// ProfileOutput is the user's profile. It holds no secrets.
type ProfileOutput struct {
	Set     bool           `json:"set"`
	Profile config.Profile `json:"profile"`
}

func (h *handlers) getProfile(_ context.Context, _ *mcp.CallToolRequest, _ struct{}) (*mcp.CallToolResult, ProfileOutput, error) {
	p := h.o.App.Config.Profile
	return nil, ProfileOutput{Set: !p.IsZero(), Profile: p}, nil
}

func (h *handlers) listSources(ctx context.Context, _ *mcp.CallToolRequest, _ struct{}) (*mcp.CallToolResult, SourcesOutput, error) {
	a := h.o.App
	health, err := a.Store.Health(ctx)
	if err != nil {
		return nil, SourcesOutput{}, h.public("list_sources", err)
	}
	on := map[string]sources.Source{}
	for _, s := range a.Sources {
		on[s.Name()] = s
	}
	out := SourcesOutput{Boards: len(a.Boards.All())}
	for _, info := range app.Catalog {
		si := SourceInfo{Name: info.Name, Description: info.Description}
		if src, ok := on[info.Name]; ok {
			si.Enabled, si.Ready = true, true
			if r, ok := src.(sources.Readier); ok {
				if err := r.Ready(); err != nil {
					si.Ready, si.NotReady = false, err.Error()
				}
			}
		}
		if hl, ok := health[info.Name]; ok {
			src := feed.SourceOf(search.Report{Source: info.Name, Status: search.Status(hl.Last.Status), Count: hl.Last.Count, Err: hl.Last.Error})
			si.LastRun, si.LastStatus, si.LastCount, si.LastError = hl.Last.RunAt.UTC().Format(time.RFC3339), string(src.Status), src.Count, sources.RedactURLs(src.Error)
		}
		out.Sources = append(out.Sources, si)
	}
	return nil, out, nil
}

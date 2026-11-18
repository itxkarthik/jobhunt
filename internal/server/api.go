package server

import (
	"cmp"
	"context"
	"net/http"
	"regexp"
	"slices"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/itxkarthik/jobhunt/internal/app"
	"github.com/itxkarthik/jobhunt/internal/feed"
	"github.com/itxkarthik/jobhunt/internal/model"
	"github.com/itxkarthik/jobhunt/internal/search"
	"github.com/itxkarthik/jobhunt/internal/sources"
	"github.com/itxkarthik/jobhunt/internal/sources/links"
	"github.com/itxkarthik/jobhunt/internal/store"
)

// APIVersion versions the /api request and response shapes.
const APIVersion = 1

const (
	maxNoteLen  = 2000  // one note added with a status change
	maxNotesLen = 20000 // a tracked job's whole notes, edited in place
)

var presetName = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,63}$`)

// jobOut is a job plus its tracker status, if tracked.
type jobOut struct {
	model.Job
	Status store.Status `json:"status,omitempty"`
}

type sourceOut struct {
	feed.SourceEvent
	Link string `json:"link,omitempty"` // browser fallback when skipped or failed
}

type linkOut struct {
	Site string `json:"site"`
	URL  string `json:"url"`
}

type searchRequest struct {
	app.FilterInput
	Refresh    bool   `json:"refresh,omitempty"`
	NewOnly    bool   `json:"new_only,omitempty"`
	ShowHidden bool   `json:"show_hidden,omitempty"`
	Sort       string `json:"sort,omitempty"`  // score (default) or date
	Limit      int    `json:"limit,omitempty"` // 0 = all
}

type searchResponse struct {
	RunAt   time.Time   `json:"run_at"`
	Fetched int         `json:"fetched"`
	Matched int         `json:"matched"`
	Jobs    []jobOut    `json:"jobs"`
	Sources []sourceOut `json:"sources"`
	Links   []linkOut   `json:"links"` // sites jobhunt can't query, pre-filled
}

type trackedOut struct {
	Key       string       `json:"key"`
	Status    store.Status `json:"status"`
	Notes     string       `json:"notes,omitempty"`
	CreatedAt time.Time    `json:"created_at"`
	UpdatedAt time.Time    `json:"updated_at"`
	AppliedAt time.Time    `json:"applied_at,omitzero"`
	Job       *model.Job   `json:"job,omitempty"` // absent once the job left the cache
}

type eventOut struct {
	At     time.Time    `json:"at"`
	Status store.Status `json:"status"`
	Note   string       `json:"note,omitempty"`
}

type presetOut struct {
	Name      string          `json:"name"`
	Filters   app.FilterInput `json:"filters"`
	UpdatedAt time.Time       `json:"updated_at"`
}

type lastRun struct {
	RunAt      time.Time `json:"run_at"`
	Status     string    `json:"status"`
	Count      int       `json:"count"`
	Error      string    `json:"error,omitempty"`
	DurationMS int64     `json:"duration_ms"`
}

type sourceStatus struct {
	Name         string    `json:"name"`
	Description  string    `json:"description,omitempty"`
	Credential   string    `json:"credential,omitempty"` // config field it needs, never its value
	Enabled      bool      `json:"enabled"`
	Ready        bool      `json:"ready"`
	NotReady     string    `json:"not_ready,omitempty"` // e.g. which key is missing; never the key
	Last         *lastRun  `json:"last,omitempty"`
	LastSuccess  time.Time `json:"last_success,omitzero"`
	RecentErrors int       `json:"recent_errors"`
}

func (s *Server) apiRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/meta", s.handle(s.meta))
	mux.HandleFunc("POST /api/search", s.handle(s.search))
	mux.HandleFunc("GET /api/events", s.handle(s.events))
	mux.HandleFunc("GET /api/links", s.handle(s.links))
	mux.HandleFunc("GET /api/jobs/{key}", s.handle(s.job))
	mux.HandleFunc("POST /api/jobs/{key}/hide", s.handle(s.hide(true)))
	mux.HandleFunc("DELETE /api/jobs/{key}/hide", s.handle(s.hide(false)))
	mux.HandleFunc("GET /api/tracker", s.handle(s.trackerList))
	mux.HandleFunc("PUT /api/tracker/{key}", s.handle(s.trackerSet))
	mux.HandleFunc("DELETE /api/tracker/{key}", s.handle(s.trackerDelete))
	mux.HandleFunc("PUT /api/tracker/{key}/notes", s.handle(s.trackerNotes))
	mux.HandleFunc("GET /api/tracker/{key}/events", s.handle(s.trackerEvents))
	mux.HandleFunc("GET /api/presets", s.handle(s.presetList))
	mux.HandleFunc("GET /api/presets/{name}", s.handle(s.presetGet))
	mux.HandleFunc("PUT /api/presets/{name}", s.handle(s.presetPut))
	mux.HandleFunc("DELETE /api/presets/{name}", s.handle(s.presetDelete))
	mux.HandleFunc("GET /api/sources", s.handle(s.sourcesList))
}

// meta describes what the UI can offer: defaults and allowed values.
func (s *Server) meta(w http.ResponseWriter, _ *http.Request) error {
	def, err := app.ParseFilters(s.opts.Defaults, app.FilterInput{})
	if err != nil {
		return err
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"api_version":  APIVersion,
		"feed_version": feed.Version,
		"defaults":     app.InputOf(def),
		"options": map[string]any{
			"countries":  model.FilterCountries,
			"modes":      model.AllWorkModes,
			"types":      model.AllJobTypes,
			"areas":      model.AllAreas,
			"experience": model.AllExperience,
			"stages":     model.AllStages,
			"headcounts": model.AllHeadcounts,
			"statuses":   store.Statuses,
			"sorts":      []string{"score", "date"},
		},
	})
	return nil
}

func (s *Server) search(w http.ResponseWriter, r *http.Request) error {
	var req searchRequest
	if err := decode(r, &req); err != nil {
		return err
	}
	f, o, err := s.parseSearch(req)
	if err != nil {
		return err
	}
	res, err := s.runSearch(r.Context(), f, o, nil)
	if err != nil {
		return err
	}
	jobs, err := s.withStatus(r.Context(), res.Jobs)
	if err != nil {
		return err
	}
	if req.Limit > 0 && len(jobs) > req.Limit {
		jobs = jobs[:req.Limit]
	}
	out := searchResponse{
		RunAt: res.RunAt.UTC(), Fetched: res.Total, Matched: len(res.Jobs),
		Jobs: jobs, Sources: make([]sourceOut, 0, len(res.Reports)),
	}
	for _, rp := range res.Reports {
		out.Sources = append(out.Sources, sourceOut{feed.SourceOf(rp), rp.Link})
	}
	out.Links = linksFor(f)
	writeJSON(w, http.StatusOK, out)
	return nil
}

// links returns the pre-filled searches on sites jobhunt can't query for
// the filters in the query string (named as for GET /api/events), without
// running a search. Stream clients use it alongside /api/events.
func (s *Server) links(w http.ResponseWriter, r *http.Request) error {
	req, err := searchRequestOf(r.URL.Query())
	if err != nil {
		return err
	}
	f, _, err := s.parseSearch(req)
	if err != nil {
		return err
	}
	writeJSON(w, http.StatusOK, linksFor(f))
	return nil
}

func linksFor(f model.Filters) []linkOut {
	out := []linkOut{}
	for _, l := range links.For(f) {
		out = append(out, linkOut{l.Site, l.URL})
	}
	return out
}

func (s *Server) parseSearch(req searchRequest) (model.Filters, SearchOptions, error) {
	f, err := app.ParseFilters(s.opts.Defaults, req.FilterInput)
	if err != nil {
		return f, SearchOptions{}, badRequest("%v", err)
	}
	if req.Sort != "" && req.Sort != "score" && req.Sort != "date" {
		return f, SearchOptions{}, badRequest("sort must be score or date")
	}
	if req.Limit < 0 {
		return f, SearchOptions{}, badRequest("limit must be 0 (all) or more")
	}
	return f, SearchOptions{Refresh: req.Refresh, NewOnly: req.NewOnly, ShowHidden: req.ShowHidden, Sort: req.Sort}, nil
}

// runSearch runs one search at a time; a second request waits its turn
// (or gives up when its client does).
func (s *Server) runSearch(ctx context.Context, f model.Filters, o SearchOptions, progress func(search.Report)) (*search.Result, error) {
	select {
	case s.searching <- struct{}{}:
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	defer func() { <-s.searching }()
	return s.opts.Search(ctx, f, o, progress)
}

func (s *Server) withStatus(ctx context.Context, jobs []model.Job) ([]jobOut, error) {
	statuses, err := s.opts.Store.StatusMap(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]jobOut, len(jobs))
	for i, j := range jobs {
		out[i] = jobOut{j, statuses[j.Key]}
	}
	return out, nil
}

// resolve expands the {key} path value, which may be a unique prefix.
func (s *Server) resolve(r *http.Request) (string, error) {
	return s.opts.Store.ResolveKey(r.Context(), r.PathValue("key"))
}

func (s *Server) job(w http.ResponseWriter, r *http.Request) error {
	key, err := s.resolve(r)
	if err != nil {
		return err
	}
	j, err := s.opts.Store.Job(r.Context(), key)
	if err != nil {
		return err
	}
	if j == nil {
		return notFound("job %s is tracked but no longer cached", key)
	}
	out, err := s.withStatus(r.Context(), []model.Job{*j})
	if err != nil {
		return err
	}
	writeJSON(w, http.StatusOK, out[0])
	return nil
}

func (s *Server) hide(hidden bool) func(http.ResponseWriter, *http.Request) error {
	return func(w http.ResponseWriter, r *http.Request) error {
		key, err := s.resolve(r)
		if err != nil {
			return err
		}
		if hidden {
			err = s.opts.Store.Hide(r.Context(), key)
		} else {
			err = s.opts.Store.Unhide(r.Context(), key)
		}
		if err != nil {
			return err
		}
		w.WriteHeader(http.StatusNoContent)
		return nil
	}
}

func (s *Server) trackerList(w http.ResponseWriter, r *http.Request) error {
	var statuses []store.Status
	for v := range strings.SplitSeq(r.URL.Query().Get("status"), ",") {
		if v = strings.TrimSpace(v); v == "" {
			continue
		}
		st, err := store.ParseStatus(v)
		if err != nil {
			return badRequest("%v", err)
		}
		statuses = append(statuses, st)
	}
	rows, err := s.opts.Store.TrackedJobs(r.Context(), statuses...)
	if err != nil {
		return err
	}
	out := make([]trackedOut, 0, len(rows))
	for _, t := range rows {
		out = append(out, trackedOf(t))
	}
	writeJSON(w, http.StatusOK, out)
	return nil
}

func trackedOf(t store.Tracked) trackedOut {
	return trackedOut{
		Key: t.Key, Status: t.Status, Notes: t.Notes, CreatedAt: t.CreatedAt.UTC(),
		UpdatedAt: t.UpdatedAt.UTC(), AppliedAt: t.AppliedAt.UTC(), Job: t.Job,
	}
}

func (s *Server) trackerSet(w http.ResponseWriter, r *http.Request) error {
	var body struct {
		Status string `json:"status"`
		Note   string `json:"note,omitempty"`
	}
	if err := decode(r, &body); err != nil {
		return err
	}
	st, err := store.ParseStatus(body.Status)
	if err != nil {
		return badRequest("%v", err)
	}
	note := strings.TrimSpace(body.Note)
	if utf8.RuneCountInString(note) > maxNoteLen {
		return badRequest("note is longer than %d characters", maxNoteLen)
	}
	key, err := s.resolve(r)
	if err != nil {
		return err
	}
	if err := s.opts.Store.SetStatus(r.Context(), key, st, note); err != nil {
		return err
	}
	return s.writeTracked(r.Context(), w, key)
}

// trackerNotes replaces a tracked job's notes, e.g. after editing them in
// the web UI. Unlike the note on PUT /api/tracker/{key}, it isn't added to
// the status history.
func (s *Server) trackerNotes(w http.ResponseWriter, r *http.Request) error {
	var body struct {
		Notes string `json:"notes"`
	}
	if err := decode(r, &body); err != nil {
		return err
	}
	notes := strings.TrimSpace(body.Notes)
	if utf8.RuneCountInString(notes) > maxNotesLen {
		return badRequest("notes are longer than %d characters", maxNotesLen)
	}
	key, err := s.resolve(r)
	if err != nil {
		return err
	}
	if err := s.opts.Store.SetNotes(r.Context(), key, notes); err != nil {
		return err
	}
	return s.writeTracked(r.Context(), w, key)
}

// writeTracked answers with key's tracker row.
func (s *Server) writeTracked(ctx context.Context, w http.ResponseWriter, key string) error {
	rows, err := s.opts.Store.TrackedJobs(ctx)
	if err != nil {
		return err
	}
	i := slices.IndexFunc(rows, func(t store.Tracked) bool { return t.Key == key })
	if i < 0 {
		return notFound("job %s is not tracked", key)
	}
	writeJSON(w, http.StatusOK, trackedOf(rows[i]))
	return nil
}

func (s *Server) trackerDelete(w http.ResponseWriter, r *http.Request) error {
	key, err := s.resolve(r)
	if err != nil {
		return err
	}
	if err := s.opts.Store.Untrack(r.Context(), key); err != nil {
		return err
	}
	w.WriteHeader(http.StatusNoContent)
	return nil
}

func (s *Server) trackerEvents(w http.ResponseWriter, r *http.Request) error {
	key, err := s.resolve(r)
	if err != nil {
		return err
	}
	evs, err := s.opts.Store.Events(r.Context(), key)
	if err != nil {
		return err
	}
	out := make([]eventOut, 0, len(evs))
	for _, e := range evs {
		out = append(out, eventOut{e.At.UTC(), e.Status, e.Note})
	}
	writeJSON(w, http.StatusOK, out)
	return nil
}

func (s *Server) presetList(w http.ResponseWriter, r *http.Request) error {
	ps, err := s.opts.Store.Presets(r.Context())
	if err != nil {
		return err
	}
	out := make([]presetOut, 0, len(ps))
	for _, p := range ps {
		out = append(out, presetOf(p))
	}
	writeJSON(w, http.StatusOK, out)
	return nil
}

func presetOf(p store.Preset) presetOut {
	return presetOut{Name: p.Name, Filters: app.InputOf(p.Filters), UpdatedAt: p.UpdatedAt.UTC()}
}

// presetNameOf validates the {name} path value. The CLI accepts any name;
// the API keeps to names that are safe in URLs and file names.
func presetNameOf(r *http.Request) (string, error) {
	name := r.PathValue("name")
	if !presetName.MatchString(name) {
		return "", badRequest("bad preset name %q (letters, digits, '.', '_', '-'; max 64)", name)
	}
	return name, nil
}

func (s *Server) presetGet(w http.ResponseWriter, r *http.Request) error {
	name, err := presetNameOf(r)
	if err != nil {
		return err
	}
	p, err := s.opts.Store.Preset(r.Context(), name)
	if err != nil {
		return err
	}
	writeJSON(w, http.StatusOK, presetOf(p))
	return nil
}

func (s *Server) presetPut(w http.ResponseWriter, r *http.Request) error {
	name, err := presetNameOf(r)
	if err != nil {
		return err
	}
	var in app.FilterInput
	if err := decode(r, &in); err != nil {
		return err
	}
	f, err := app.ParseFilters(s.opts.Defaults, in)
	if err != nil {
		return badRequest("%v", err)
	}
	if err := s.opts.Store.SavePreset(r.Context(), name, f); err != nil {
		return err
	}
	p, err := s.opts.Store.Preset(r.Context(), name)
	if err != nil {
		return err
	}
	writeJSON(w, http.StatusOK, presetOf(p))
	return nil
}

func (s *Server) presetDelete(w http.ResponseWriter, r *http.Request) error {
	name, err := presetNameOf(r)
	if err != nil {
		return err
	}
	if err := s.opts.Store.DeletePreset(r.Context(), name); err != nil {
		return err
	}
	w.WriteHeader(http.StatusNoContent)
	return nil
}

// sourcesList merges the enabled sources with their recorded health.
// Sources that ran before but are now disabled are listed too.
func (s *Server) sourcesList(w http.ResponseWriter, r *http.Request) error {
	health, err := s.opts.Store.Health(r.Context())
	if err != nil {
		return err
	}
	byName := map[string]*sourceStatus{}
	for _, d := range s.opts.Catalog {
		byName[d.Name] = &sourceStatus{Name: d.Name, Description: d.Description, Credential: d.Credential}
	}
	for _, src := range s.opts.Sources {
		st, ok := byName[src.Name()]
		if !ok {
			st = &sourceStatus{Name: src.Name()}
			byName[st.Name] = st
		}
		st.Enabled, st.Ready = true, true
		if rd, ok := src.(sources.Readier); ok {
			if err := rd.Ready(); err != nil {
				st.Ready, st.NotReady = false, err.Error()
			}
		}
	}
	for name, h := range health {
		st, ok := byName[name]
		if !ok {
			st = &sourceStatus{Name: name}
			byName[name] = st
		}
		l := h.Last
		st.Last = &lastRun{RunAt: l.RunAt.UTC(), Status: l.Status, Count: l.Count, Error: l.Error, DurationMS: l.Duration.Milliseconds()}
		st.LastSuccess, st.RecentErrors = h.LastSuccess.UTC(), h.RecentErrors
	}
	out := make([]sourceStatus, 0, len(byName))
	for _, st := range byName {
		out = append(out, *st)
	}
	// Catalog order, as `jobhunt sources` prints; anything else (e.g. a
	// source that has since been removed) after it, by name.
	rank := func(name string) int {
		if i := slices.IndexFunc(s.opts.Catalog, func(d SourceDesc) bool { return d.Name == name }); i >= 0 {
			return i
		}
		return len(s.opts.Catalog)
	}
	slices.SortFunc(out, func(a, b sourceStatus) int {
		return cmp.Or(cmp.Compare(rank(a.Name), rank(b.Name)), cmp.Compare(a.Name, b.Name))
	})
	writeJSON(w, http.StatusOK, out)
	return nil
}

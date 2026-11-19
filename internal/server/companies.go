package server

import (
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/itxkarthik/jobhunt/internal/app"
	"github.com/itxkarthik/jobhunt/internal/company"
	"github.com/itxkarthik/jobhunt/internal/model"
)

func (s *Server) companyRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/companies", s.handle(s.companyList))
	mux.HandleFunc("GET /api/companies/{key}", s.handle(s.companyGet))
	mux.HandleFunc("PUT /api/companies/{key}", s.handle(s.companyPut))
	mux.HandleFunc("DELETE /api/companies/{key}", s.handle(s.companyDelete))
	mux.HandleFunc("GET /api/companies/{key}/draft", s.handle(s.companyDraft))
	mux.HandleFunc("POST /api/boards", s.handle(s.boardAdd))
}

const maxCompanies = 500

// companyList takes the CLI's filters as query parameters; lists are
// comma-separated.
func (s *Server) companyList(w http.ResponseWriter, r *http.Request) error {
	q := r.URL.Query()
	list := func(k string) []string {
		var out []string
		for v := range strings.SplitSeq(q.Get(k), ",") {
			if v = strings.TrimSpace(v); v != "" {
				out = append(out, v)
			}
		}
		return out
	}
	flag := func(k string) (bool, error) {
		v := q.Get(k)
		if v == "" {
			return false, nil
		}
		b, err := strconv.ParseBool(v)
		if err != nil {
			return false, badRequest("%s must be true or false", k)
		}
		return b, nil
	}
	in := app.CompanyQuery{
		Stages: list("stages"), Headcounts: list("headcounts"), Countries: list("countries"), Tags: list("tags"),
		FundedWithin: q.Get("funded_within"), FundedBefore: q.Get("funded_before"), Query: q.Get("q"),
		Hiring: list("hiring"),
	}
	var err error
	if in.HasContact, err = flag("has_contact"); err != nil {
		return err
	}
	if in.Jobs, err = flag("jobs"); err != nil {
		return err
	}
	if v := q.Get("limit"); v != "" {
		if in.Limit, err = strconv.Atoi(v); err != nil || in.Limit < 0 || in.Limit > maxCompanies {
			return badRequest("limit must be 0-%d", maxCompanies)
		}
	}
	if in.Limit == 0 {
		in.Limit = maxCompanies
	}
	if _, err := company.ParseFilter(in.FilterInput, app.ParseAge); err != nil {
		return badRequest("%v", err)
	}
	for _, h := range in.Hiring {
		if _, err := model.ParseExperience(h); err != nil && h != app.Any {
			return badRequest("%v", err)
		}
	}
	cs, err := app.Companies(r.Context(), s.opts.Store, in)
	if err != nil {
		return err
	}
	if cs == nil {
		cs = []app.CompanyView{}
	}
	writeJSON(w, http.StatusOK, cs)
	return nil
}

// findCompany resolves {key}: a key, a unique key prefix, or a name.
func (s *Server) findCompany(r *http.Request) (*model.Company, error) {
	return app.FindCompany(r.Context(), s.opts.Store, r.PathValue("key"))
}

func (s *Server) companyGet(w http.ResponseWriter, r *http.Request) error {
	c, err := s.findCompany(r)
	if err != nil {
		return err
	}
	jobs, err := s.opts.Store.CompanyJobs(r.Context(), []string{c.Key}, time.Now().Add(-app.OpenJobWindow))
	if err != nil {
		return err
	}
	js := jobs[c.Key]
	writeJSON(w, http.StatusOK, app.CompanyView{Company: *c, OpenJobs: len(js), Jobs: js})
	return nil
}

// companyPut adds or updates the company whose key is {key}. The body is
// the same update an agent sends through MCP; name defaults to the stored
// one and must otherwise have the key {key}.
func (s *Server) companyPut(w http.ResponseWriter, r *http.Request) error {
	var in app.CompanyInput
	if err := decode(r, &in); err != nil {
		return err
	}
	key := strings.TrimSpace(r.PathValue("key"))
	if in.Name == "" {
		old, err := s.opts.Store.Company(r.Context(), key)
		if err != nil {
			return err
		}
		if old == nil {
			return badRequest("name is required for a new company")
		}
		in.Name = old.Name
	}
	if company.Key(in.Name) != key {
		return badRequest("name %q has key %q, not %q", in.Name, company.Key(in.Name), key)
	}
	c, rm, err := in.Company("web", time.Now())
	if err != nil {
		return badRequest("%v", err)
	}
	if len(c.Boards) > 0 {
		if s.opts.CheckBoard == nil {
			return &apiError{http.StatusServiceUnavailable, "adding boards isn't available"}
		}
		probe := c
		if err := company.Clean(&probe); err != nil {
			return badRequest("%v", err)
		}
		if err := app.AddBoards(r.Context(), s.opts.Store, c, s.opts.CheckBoard); err != nil {
			return err
		}
	}
	got, created, err := app.UpsertCompany(r.Context(), s.opts.Store, c, rm)
	if err != nil {
		return err
	}
	s.boardsChanged(r, len(c.Boards)+len(rm.Boards) > 0)
	code := http.StatusOK
	if created {
		code = http.StatusCreated
	}
	writeJSON(w, code, got)
	return nil
}

func (s *Server) companyDelete(w http.ResponseWriter, r *http.Request) error {
	c, err := s.findCompany(r)
	if err != nil {
		return err
	}
	if _, err := s.opts.Store.DeleteCompany(r.Context(), c.Key); err != nil {
		return err
	}
	s.boardsChanged(r, len(c.Boards) > 0)
	w.WriteHeader(http.StatusNoContent)
	return nil
}

// companyDraft returns an outreach email for the user to send themselves,
// optionally about one job (?job=<key or prefix>).
func (s *Server) companyDraft(w http.ResponseWriter, r *http.Request) error {
	c, err := s.findCompany(r)
	if err != nil {
		return err
	}
	var job *model.Job
	if k := strings.TrimSpace(r.URL.Query().Get("job")); k != "" {
		key, err := s.opts.Store.ResolveKey(r.Context(), k)
		if err != nil {
			return err
		}
		if job, err = s.opts.Store.Job(r.Context(), key); err != nil {
			return err
		}
	}
	writeJSON(w, http.StatusOK, company.NewDraft(c, job, company.DraftOptions{Stack: s.opts.Stack}))
	return nil
}

// boardAdd checks a job board once and adds it to a company (created if
// new). A dead or empty board is refused.
func (s *Server) boardAdd(w http.ResponseWriter, r *http.Request) error {
	var in struct {
		Company string `json:"company"`
		ATS     string `json:"ats"`
		Slug    string `json:"slug"`
		Country string `json:"country,omitempty"`
		Remote  bool   `json:"remote,omitempty"`
	}
	if err := decode(r, &in); err != nil {
		return err
	}
	b, err := company.ParseBoard(in.ATS + ":" + in.Slug)
	if err != nil {
		return badRequest("%v", err)
	}
	if s.opts.CheckBoard == nil {
		return &apiError{http.StatusServiceUnavailable, "adding boards isn't available"}
	}
	c := model.Company{Name: in.Company, Country: in.Country, Remote: in.Remote, Boards: []model.Board{b}, Sources: []string{"web"}}
	probe := c
	if err := company.Clean(&probe); err != nil {
		return badRequest("%v", err)
	}
	if err := app.AddBoards(r.Context(), s.opts.Store, c, s.opts.CheckBoard); err != nil {
		return err
	}
	got, _, err := app.UpsertCompany(r.Context(), s.opts.Store, c, company.Removal{})
	if err != nil {
		return err
	}
	s.boardsChanged(r, true)
	writeJSON(w, http.StatusOK, got)
	return nil
}

// boardsChanged refreshes the ATS sources' boards after a change, so the
// next search includes it. A failure only delays that to the next search.
func (s *Server) boardsChanged(r *http.Request, changed bool) {
	if !changed || s.opts.RefreshBoards == nil {
		return
	}
	if err := s.opts.RefreshBoards(r.Context()); err != nil {
		s.log.Warn("refreshing boards", "err", err)
	}
}

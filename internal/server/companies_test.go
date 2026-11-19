package server

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/itxkarthik/jobhunt/internal/app"
	"github.com/itxkarthik/jobhunt/internal/company"
	"github.com/itxkarthik/jobhunt/internal/model"
	"github.com/itxkarthik/jobhunt/internal/sources"
	"github.com/itxkarthik/jobhunt/internal/sources/ats"
)

func TestCompaniesAPI(t *testing.T) {
	refreshed := 0
	s, st := newTestServer(t, func(o *Options) {
		o.CheckBoard = func(_ context.Context, b ats.Company) (int, error) {
			switch b.Slug {
			case "dead":
				return 0, &sources.StatusError{Code: 404, URL: "https://api.lever.co/v0/postings/dead"}
			case "empty":
				return 0, nil
			}
			return 3, nil
		}
		o.RefreshBoards = func(context.Context) error { refreshed++; return nil }
		o.Stack = []string{"Go"}
	})
	src := "https://example.com/news/acme"
	put := `{"name":"Acme AI","country":"India","stage":"seed","headcount":"20",
		"rounds":[{"stage":"seed","amount":2000000,"currency":"USD","date":"2026-09-01T00:00:00Z","source_url":"` + src + `"}],
		"contacts":[{"name":"Asha Rao","role":"CEO","email":"asha@acme.ai","source_url":"` + src + `"}]}`

	w := serve(s, req(http.MethodPut, "/api/companies/acme%20ai", put))
	if w.Code != http.StatusCreated {
		t.Fatalf("create = %d %s", w.Code, w.Body)
	}
	var c model.Company
	if err := json.Unmarshal(w.Body.Bytes(), &c); err != nil || c.Country != "IN" || c.Sources[0] != "web" || len(c.Contacts) != 1 {
		t.Fatalf("created %+v, %v", c, err)
	}
	if w := serve(s, req(http.MethodPut, "/api/companies/acme%20ai", `{"city":"Bengaluru"}`)); w.Code != http.StatusOK || !strings.Contains(w.Body.String(), `"city":"Bengaluru"`) {
		t.Errorf("update without name = %d %s", w.Code, w.Body)
	}

	if w := serve(s, req(http.MethodPut, "/api/companies/acme%20ai", `{"notes":"call Asha"}`)); !strings.Contains(w.Body.String(), `"notes":"call Asha"`) {
		t.Errorf("set notes = %s", w.Body)
	}
	if w := serve(s, req(http.MethodPut, "/api/companies/acme%20ai", `{"notes":""}`)); !strings.Contains(w.Body.String(), `"notes":"call Asha"`) {
		t.Errorf("empty notes field should leave them: %s", w.Body)
	}
	if w := serve(s, req(http.MethodPut, "/api/companies/acme%20ai", `{"remove":{"notes":true}}`)); w.Code != http.StatusOK || strings.Contains(w.Body.String(), `"notes"`) {
		t.Errorf("clear notes = %d %s", w.Code, w.Body)
	}

	bad := []struct {
		name, method, path, body string
		want                     int
	}{
		{"key mismatch", http.MethodPut, "/api/companies/other", `{"name":"Acme AI"}`, http.StatusBadRequest},
		{"new without name", http.MethodPut, "/api/companies/newco", `{"city":"Pune"}`, http.StatusBadRequest},
		{"contact without source", http.MethodPut, "/api/companies/acme%20ai", `{"contacts":[{"email":"x@acme.ai"}]}`, http.StatusBadRequest},
		{"bad url", http.MethodPut, "/api/companies/acme%20ai", `{"website":"javascript:alert(1)"}`, http.StatusBadRequest},
		{"unknown field", http.MethodPut, "/api/companies/acme%20ai", `{"name":"Acme AI","evil":1}`, http.StatusBadRequest},
		{"not json", http.MethodPut, "/api/companies/acme%20ai", ``, http.StatusUnsupportedMediaType},
		{"dead board", http.MethodPost, "/api/boards", `{"company":"Acme AI","ats":"lever","slug":"dead"}`, http.StatusBadRequest},
		{"empty board", http.MethodPost, "/api/boards", `{"company":"Acme AI","ats":"lever","slug":"empty"}`, http.StatusBadRequest},
		{"bad ats", http.MethodPost, "/api/boards", `{"company":"Acme AI","ats":"monster","slug":"acme"}`, http.StatusBadRequest},
		{"unknown company", http.MethodGet, "/api/companies/nobody", ``, http.StatusNotFound},
		{"bad filter", http.MethodGet, "/api/companies?stages=series-z", ``, http.StatusBadRequest},
		{"bad window", http.MethodGet, "/api/companies?funded_within=1d&funded_before=7d", ``, http.StatusBadRequest},
		{"bad limit", http.MethodGet, "/api/companies?limit=9999", ``, http.StatusBadRequest},
		{"bad flag", http.MethodGet, "/api/companies?has_contact=maybe", ``, http.StatusBadRequest},
	}
	for _, tt := range bad {
		if w := serve(s, req(tt.method, tt.path, tt.body)); w.Code != tt.want {
			t.Errorf("%s = %d %s, want %d", tt.name, w.Code, w.Body, tt.want)
		}
	}

	w = serve(s, req(http.MethodPost, "/api/boards", `{"company":"Acme AI","ats":"ashby","slug":"acme"}`))
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), `"slug":"acme"`) || refreshed == 0 {
		t.Errorf("add board = %d %s (refreshed %d)", w.Code, w.Body, refreshed)
	}

	var list []app.CompanyView
	w = serve(s, req(http.MethodGet, "/api/companies?stages=seed&countries=IN&has_contact=true", ""))
	if err := json.Unmarshal(w.Body.Bytes(), &list); err != nil || len(list) != 1 || list[0].Name != "Acme AI" {
		t.Errorf("list = %d %s", w.Code, w.Body)
	}
	if w := serve(s, req(http.MethodGet, "/api/companies?stages=growth", "")); strings.TrimSpace(w.Body.String()) != "[]" {
		t.Errorf("empty list = %s", w.Body)
	}

	var d company.Draft
	w = serve(s, req(http.MethodGet, "/api/companies/acme/draft", ""))
	if err := json.Unmarshal(w.Body.Bytes(), &d); err != nil || d.To != "asha@acme.ai" || !strings.HasPrefix(d.Mailto, "mailto:") || !strings.Contains(d.Body, "I work mostly in Go") {
		t.Errorf("draft = %d %s", w.Code, w.Body)
	}

	if w := serve(s, req(http.MethodDelete, "/api/companies/acme", "")); w.Code != http.StatusNoContent {
		t.Errorf("delete = %d %s", w.Code, w.Body)
	}
	if cs, _ := st.Companies(context.Background()); len(cs) != 0 {
		t.Errorf("%d companies left", len(cs))
	}
}

func TestBoardsUnavailableWithoutChecker(t *testing.T) {
	s, _ := newTestServer(t, nil)
	if w := serve(s, req(http.MethodPost, "/api/boards", `{"company":"Acme","ats":"lever","slug":"acme"}`)); w.Code != http.StatusServiceUnavailable {
		t.Errorf("no checker = %d %s", w.Code, w.Body)
	}
}

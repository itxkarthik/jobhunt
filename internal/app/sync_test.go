package app

import (
	"context"
	"errors"
	"testing"

	"github.com/itxkarthik/jobhunt/internal/company"
	"github.com/itxkarthik/jobhunt/internal/model"
	"github.com/itxkarthik/jobhunt/internal/sources"
	"github.com/itxkarthik/jobhunt/internal/sources/ats"
	"github.com/itxkarthik/jobhunt/internal/sources/hn"
	"github.com/itxkarthik/jobhunt/internal/sources/sourcetest"
	"github.com/itxkarthik/jobhunt/internal/store"
)

func TestSync(t *testing.T) {
	ctx := context.Background()
	st, err := store.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	srv := sourcetest.Serve(t, map[string]string{
		"/api/v1/search_by_date": "../sources/hn/testdata/search.json",
		"/api/v1/items/49922569": "../sources/hn/testdata/leads.json",
	})
	src := hn.New(sourcetest.Client())
	src.BaseURL = srv.URL + "/api/v1"
	ok := func(context.Context, ats.Company) (int, error) { return 1, nil }
	importers := func() []Importer {
		return []Importer{
			{Name: "broken", Run: func(context.Context) (*ImportSummary, error) { return nil, errors.New("upstream down") }},
			{Name: "edgar", Skip: "set importers.edgar.user_agent"},
			{Name: "hn", Run: func(ctx context.Context) (*ImportSummary, error) {
				return ImportHN(ctx, st, src, ok, HNOptions{CheckBoards: true}, false)
			}},
		}
	}

	res, err := Sync(ctx, importers())
	if err != nil || len(res) != 3 {
		t.Fatalf("sync: %+v, %v", res, err)
	}
	if res[0].Error != "upstream down" || res[1].Skipped == "" || res[2].Error != "" || len(res[2].Summary.Added) != 3 {
		t.Errorf("results: %+v / %+v / %+v", res[0], res[1], res[2].Summary)
	}

	// Positions persist: the second run adds nothing.
	res, _ = Sync(ctx, importers())
	if s := res[2].Summary; s.Found != 0 || len(s.Added)+len(s.Updated) != 0 {
		t.Errorf("second run: %+v", s)
	}

	cctx, cancel := context.WithCancel(ctx)
	cancel()
	if res, err := Sync(cctx, importers()); !errors.Is(err, context.Canceled) || len(res) != 0 {
		t.Errorf("cancelled: %+v, %v", res, err)
	}
}

func TestCheckAddedBoards(t *testing.T) {
	ctx := context.Background()
	st, err := store.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	if _, _, err := UpsertCompany(ctx, st, model.Company{Name: "Acme", Boards: []model.Board{{ATS: "lever", Slug: "gone"}, {ATS: "ashby", Slug: "flaky"}, {ATS: "ashby", Slug: "acme"}}}, company.Removal{}); err != nil {
		t.Fatal(err)
	}
	boards := []ats.Company{
		{Name: "Acme", ATS: "lever", Slug: "gone", Added: true},
		{Name: "Acme", ATS: "ashby", Slug: "flaky", Added: true},
		{Name: "Acme", ATS: "ashby", Slug: "acme", Added: true},
		{Name: "Stripe", ATS: "greenhouse", Slug: "stripe"}, // companies.yaml: never checked here
	}
	check := func(_ context.Context, c ats.Company) (int, error) {
		switch c.Slug {
		case "gone":
			return 0, &sources.StatusError{Code: 404}
		case "flaky":
			return 0, &sources.StatusError{Code: 503}
		case "stripe":
			t.Error("companies.yaml board checked")
		}
		return 4, nil
	}
	sum, err := CheckAddedBoards(ctx, st, boards, check, true)
	if err != nil || sum.Read != 3 || sum.Found != 1 || len(sum.Updated) != 1 || len(sum.Errors) != 1 {
		t.Fatalf("summary %+v, %v", sum, err)
	}
	c, _ := FindCompany(ctx, st, "acme")
	if len(c.Boards) != 2 { // the 404 board is pruned; the 503 one stays
		t.Errorf("boards = %v", c.Boards)
	}
}

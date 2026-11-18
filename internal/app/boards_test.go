package app

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/itxkarthik/jobhunt/internal/company"
	"github.com/itxkarthik/jobhunt/internal/model"
	"github.com/itxkarthik/jobhunt/internal/sources/ats"
	"github.com/itxkarthik/jobhunt/internal/store"
)

func TestBoardsOf(t *testing.T) {
	got := BoardsOf([]model.Company{
		{Name: "Acme", Country: "IN", Boards: []model.Board{{ATS: "greenhouse", Slug: "acme"}}},
		{Name: "Beta", Country: "IN", Remote: true, Boards: []model.Board{{ATS: "lever", Slug: "beta"}}},
		{Name: "Gamma", Boards: []model.Board{{ATS: "ashby", Slug: "gamma"}}},
		{Name: "No board"},
	})
	if len(got) != 3 {
		t.Fatalf("got %+v", got)
	}
	if got[0].Remote || got[0].Countries[0] != "IN" || !got[1].Remote || !got[2].Remote || got[2].Countries != nil {
		t.Errorf("got %+v", got)
	}
}

func TestAddBoards(t *testing.T) {
	ctx := context.Background()
	st, err := store.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	var checked []string
	check := func(_ context.Context, b ats.Company) (int, error) {
		checked = append(checked, b.Slug)
		switch b.Slug {
		case "dead":
			return 0, errors.New("HTTP 404")
		case "empty":
			return 0, nil
		}
		return 3, nil
	}
	board := func(slug string) model.Company {
		return model.Company{Name: "Acme", Boards: []model.Board{{ATS: "greenhouse", Slug: slug}}}
	}
	tests := []struct {
		slug, wantErr string
	}{
		{"acme", ""},
		{"dead", "HTTP 404"},
		{"empty", "lists no jobs"},
	}
	for _, tt := range tests {
		err := AddBoards(ctx, st, board(tt.slug), check)
		if (tt.wantErr == "") != (err == nil) || (err != nil && !strings.Contains(err.Error(), tt.wantErr)) {
			t.Errorf("%s: err = %v, want %q", tt.slug, err, tt.wantErr)
		}
	}
	if _, _, err := UpsertCompany(ctx, st, board("acme"), company.Removal{}); err != nil {
		t.Fatal(err)
	}
	checked = nil
	if err := AddBoards(ctx, st, board("ACME"), check); err != nil || len(checked) != 0 {
		t.Errorf("a board the company has was fetched again: %v, %v", checked, err)
	}

	// RefreshBoards puts the stored board in the shared list.
	a := &App{Store: st, Boards: ats.NewBoards(nil)}
	if err := a.RefreshBoards(ctx); err != nil {
		t.Fatal(err)
	}
	if all := a.Boards.All(); len(all) != 1 || all[0].Slug != "acme" || !all[0].Added {
		t.Errorf("boards = %+v", all)
	}
}

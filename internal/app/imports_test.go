package app

import (
	"context"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/itxkarthik/jobhunt/internal/model"
	"github.com/itxkarthik/jobhunt/internal/sources/edgar"
	"github.com/itxkarthik/jobhunt/internal/sources/sourcetest"
	"github.com/itxkarthik/jobhunt/internal/store"
)

func TestImportEDGAR(t *testing.T) {
	ctx := context.Background()
	st, err := store.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	td := "../sources/edgar/testdata/"
	srv := sourcetest.Serve(t, map[string]string{
		"/Archives/edgar/daily-index/2026/QTR4/form.20261117.idx":         td + "form.20261117.idx",
		"/Archives/edgar/data/1994140/000199414026000006/primary_doc.xml": td + "lyzr.xml",
		"/Archives/edgar/data/2170001/000217000126000001/primary_doc.xml": td + "brightloop.xml",
		"/Archives/edgar/data/2170002/000217000226000001/primary_doc.xml": td + "kettle.xml",
	})
	im := &edgar.Importer{Client: sourcetest.Client(), Base: srv.URL}
	o := edgar.Options{Since: 24 * time.Hour, Now: time.Date(2026, 11, 18, 12, 0, 0, 0, time.UTC)}

	sum, err := ImportEDGAR(ctx, st, im, o, true)
	if err != nil || len(sum.Added) != 2 {
		t.Fatalf("dry run: %+v, %v", sum, err)
	}
	if cs, _ := st.Companies(ctx); len(cs) != 0 {
		t.Errorf("dry run saved %d companies", len(cs))
	}

	sum, err = ImportEDGAR(ctx, st, im, o, false)
	if err != nil || strings.Join(sum.Added, ",") != "Lyzr, Inc.,BRIGHTLOOP ROBOTICS INC" || !sum.Complete {
		t.Fatalf("import: %+v, %v", sum, err)
	}
	c, err := FindCompany(ctx, st, "Lyzr")
	if err != nil || c.Country != "US" || len(c.Rounds) != 1 || len(c.Contacts) != 1 || c.Sources[0] != "edgar" {
		t.Errorf("lyzr = %+v, %v", c, err)
	}

	before := len(srv.Requests())
	sum, err = ImportEDGAR(ctx, st, im, o, false)
	if err != nil || len(sum.Added)+len(sum.Updated) != 0 || len(srv.Requests()) != before {
		t.Errorf("repeat run: %+v, %v, %d new requests", sum, err, len(srv.Requests())-before)
	}

	// --redo reads the day again; saving merges, so nothing is duplicated.
	before = len(srv.Requests())
	o.Redo = true
	sum, err = ImportEDGAR(ctx, st, im, o, false)
	if err != nil || len(srv.Requests()) == before || len(sum.Added) != 0 || len(sum.Updated) != 2 {
		t.Errorf("redo: %+v, %v, %d requests", sum, err, len(srv.Requests())-before)
	}
	if c, _ := FindCompany(ctx, st, "Lyzr"); len(c.Rounds) != 1 || len(c.Contacts) != 1 {
		t.Errorf("redo duplicated data: %+v", c)
	}
	o.Redo = false

	// A redo cut short by --max-filings resumes on the next plain run.
	o.Redo, o.MaxFilings = true, 1
	if sum, err = ImportEDGAR(ctx, st, im, o, false); err != nil || sum.Complete || sum.Fetched != 1 {
		t.Fatalf("capped redo: %+v, %v", sum, err)
	}
	o.Redo, o.MaxFilings = false, 0
	before = len(srv.Requests())
	if sum, err = ImportEDGAR(ctx, st, im, o, false); err != nil || !sum.Complete || sum.Fetched != 2 {
		t.Errorf("resume after capped redo: %+v, %v", sum, err)
	}
	// A dry-run redo leaves the saved progress alone.
	o.Redo = true
	if _, err = ImportEDGAR(ctx, st, im, o, true); err != nil {
		t.Fatal(err)
	}
	o.Redo = false
	before = len(srv.Requests())
	if sum, err = ImportEDGAR(ctx, st, im, o, false); err != nil || len(srv.Requests()) != before {
		t.Errorf("dry-run redo changed progress: %+v, %v, %d requests", sum, err, len(srv.Requests())-before)
	}

	// Broader filters read the same day again and find the biotech filing.
	o.Industries = []string{"any"}
	sum, err = ImportEDGAR(ctx, st, im, o, false)
	if err != nil || !slices.Contains(sum.Added, "Kettle Biosciences, Inc.") {
		t.Errorf("broader run: %+v, %v", sum, err)
	}
}

func TestSaveCompaniesStorageError(t *testing.T) {
	ctx := context.Background()
	st, err := store.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	sum := &ImportSummary{}
	if err := saveCompanies(ctx, st, []model.Company{{Name: ""}}, sum); err != nil || len(sum.Skipped) != 1 {
		t.Errorf("invalid record: %v, %+v", err, sum)
	}
	st.Close()
	sum = &ImportSummary{}
	if err := saveCompanies(ctx, st, []model.Company{{Name: "Acme"}}, sum); err == nil || len(sum.Skipped) != 0 {
		t.Errorf("storage error was not returned: %v, %+v", err, sum)
	}
}

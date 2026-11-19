package main

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/itxkarthik/jobhunt/internal/app"
	"github.com/itxkarthik/jobhunt/internal/company"
	"github.com/itxkarthik/jobhunt/internal/feed"
	"github.com/itxkarthik/jobhunt/internal/model"
	"github.com/itxkarthik/jobhunt/internal/search"
)

func TestDigest(t *testing.T) {
	ctx := context.Background()
	st := openStore(t)
	now := time.Unix(1_800_000_000, 0)
	jobs := []model.Job{job("a", 90), job("b", 80)}
	var cs []model.Company
	var notes []string
	run := func(format string, hook *feed.Webhook, dry bool) (string, error) {
		var out bytes.Buffer
		dg := &digester{
			search:    func(context.Context) (*search.Result, error) { return &search.Result{Jobs: jobs}, nil },
			companies: func(context.Context) ([]model.Company, error) { return cs, nil },
			cursors:   st, cursor: "digest", format: format, out: &out, webhook: hook, dryRun: dry,
			notify: func(title, _ string) error { notes = append(notes, title); return nil },
			newID:  func() string { return "id" }, now: func() time.Time { now = now.Add(time.Hour); return now },
		}
		err := dg.once(ctx)
		return out.String(), err
	}

	out, err := run("md", nil, false)
	if err != nil || !strings.Contains(out, "First digest") || !strings.Contains(out, "Intern a") || !strings.Contains(out, "Intern b") {
		t.Fatalf("first digest: %v\n%s", err, out)
	}
	// Nothing new: silent, no notification.
	if out, err := run("md", nil, false); err != nil || out != "" || len(notes) != 1 {
		t.Fatalf("second digest = %q, %v (notifications %v)", out, err, notes)
	}

	// A new job and a new company appear exactly once.
	jobs = append(jobs, job("c", 70))
	c, _, err := app.UpsertCompany(ctx, st, model.Company{Name: "Acme"}, company.Removal{})
	if err != nil {
		t.Fatal(err)
	}
	cs = []model.Company{c}
	out, err = run("json", nil, true) // dry run: shown, not recorded
	if err != nil || !strings.Contains(out, `"key": "c"`) {
		t.Fatalf("dry run: %v\n%s", err, out)
	}
	var d app.Digest
	if err := json.Unmarshal([]byte(out), &d); err != nil || d.First || len(d.Jobs) != 1 || len(d.Companies) != 1 || d.Companies[0].Change != feed.Created {
		t.Fatalf("json digest %+v, %v", d, err)
	}

	// A failing webhook records nothing either.
	var fail atomic.Bool
	fail.Store(true)
	var got []byte
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if fail.Load() {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		if r.Header.Get("Content-Type") != "application/json" {
			t.Errorf("content type %q", r.Header.Get("Content-Type"))
		}
		got, _ = io.ReadAll(r.Body)
	}))
	defer srv.Close()
	hook, err := feed.NewWebhook(srv.URL, []byte("0123456789abcdef"), "test")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := run("md", hook, false); err == nil || !strings.Contains(err.Error(), "not recorded") {
		t.Fatalf("webhook failure: %v", err)
	}
	fail.Store(false)
	out, err = run("md", hook, false)
	if err != nil || strings.Contains(out, "Intern a") || !strings.Contains(out, "Intern c") || !strings.Contains(out, "**Acme** (created)") {
		t.Fatalf("delivered digest: %v\n%s", err, out)
	}
	var wd struct {
		Type     string `json:"type"`
		Markdown string `json:"markdown"`
		Jobs     []app.DigestJob
	}
	if err := json.Unmarshal(got, &wd); err != nil || wd.Type != "digest" || len(wd.Jobs) != 1 || !strings.HasPrefix(wd.Markdown, "# jobhunt digest") {
		t.Errorf("webhook payload %s, %v", got, err)
	}
	if out, _ := run("md", hook, false); out != "" {
		t.Errorf("repeated: %s", out)
	}

	// An update to the company comes back once, as updated.
	c, _, _ = app.UpsertCompany(ctx, st, model.Company{Name: "Acme", City: "Pune"}, company.Removal{})
	cs = []model.Company{c}
	if out, _ := run("md", nil, false); !strings.Contains(out, "**Acme** (updated)") || strings.Contains(out, "new job") {
		t.Errorf("update digest:\n%s", out)
	}
	if out, _ := run("md", nil, false); out != "" {
		t.Errorf("update repeated: %s", out)
	}
}

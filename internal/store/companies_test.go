package store

import (
	"context"
	"errors"
	"path/filepath"
	"sync"
	"testing"

	"github.com/itxkarthik/jobhunt/internal/model"
)

func TestCompanies(t *testing.T) {
	ctx := context.Background()
	st, err := Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()

	put := func(key, name string, tags ...string) (model.Company, bool) {
		t.Helper()
		c, created, err := st.UpdateCompany(ctx, key, func(old *model.Company) (model.Company, error) {
			c := model.Company{Key: key, Name: name}
			if old != nil {
				c = *old
			}
			c.Tags = append(c.Tags, tags...)
			return c, nil
		})
		if err != nil {
			t.Fatal(err)
		}
		return c, created
	}
	if _, created := put("acme", "Acme", "ai"); !created {
		t.Error("first put not reported as created")
	}
	if c, created := put("acme", "Acme", "fintech"); created || len(c.Tags) != 2 {
		t.Errorf("second put: created=%v tags=%v", created, c.Tags)
	}
	put("acme labs", "Acme Labs")
	put("beta", "beta")

	got, err := st.Company(ctx, "acme")
	if err != nil || got == nil || len(got.Tags) != 2 || got.CreatedAt.IsZero() {
		t.Fatalf("Company = %+v, %v", got, err)
	}
	if none, err := st.Company(ctx, "nope"); none != nil || err != nil {
		t.Errorf("missing company = %+v, %v", none, err)
	}
	all, err := st.Companies(ctx)
	if err != nil || len(all) != 3 || all[0].Name != "Acme" || all[2].Name != "beta" {
		t.Errorf("Companies = %+v, %v", all, err)
	}

	for _, tt := range []struct {
		in, want string
		err      error
	}{
		{"acme", "acme", nil}, // exact match beats the longer "acme labs"
		{"acme l", "acme labs", nil},
		{"be", "beta", nil},
		{"ac%", "", ErrNoCompany}, // LIKE wildcards are literal
		{"zzz", "", ErrNoCompany},
		{"", "", ErrNoCompany},
	} {
		k, err := st.ResolveCompany(ctx, tt.in)
		if k != tt.want || !errors.Is(err, tt.err) {
			t.Errorf("ResolveCompany(%q) = %q, %v", tt.in, k, err)
		}
	}
	put("acme two", "Acme Two")
	if _, err := st.ResolveCompany(ctx, "acme "); !errors.Is(err, ErrAmbiguousKey) {
		t.Errorf("ambiguous prefix: %v", err)
	}

	// The callback's error rolls back; a changed key is refused.
	if _, _, err := st.UpdateCompany(ctx, "acme", func(*model.Company) (model.Company, error) { return model.Company{}, errors.New("no") }); err == nil {
		t.Error("callback error ignored")
	}
	if _, _, err := st.UpdateCompany(ctx, "acme", func(*model.Company) (model.Company, error) { return model.Company{Key: "other"}, nil }); err == nil {
		t.Error("key change accepted")
	}

	if ok, err := st.DeleteCompany(ctx, "beta"); !ok || err != nil {
		t.Errorf("delete = %v, %v", ok, err)
	}
	if ok, _ := st.DeleteCompany(ctx, "beta"); ok {
		t.Error("second delete reported a row")
	}
}

// Concurrent updates must not lose each other's changes.
func TestUpdateCompanyConcurrent(t *testing.T) {
	ctx := context.Background()
	st, err := Open(filepath.Join(t.TempDir(), "jobhunt.db")) // a file, so connections really run in parallel
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	var wg sync.WaitGroup
	for range 20 {
		wg.Go(func() {
			_, _, err := st.UpdateCompany(ctx, "acme", func(old *model.Company) (model.Company, error) {
				c := model.Company{Key: "acme", Name: "Acme"}
				if old != nil {
					c = *old
				}
				c.Tags = append(c.Tags, "x")
				return c, nil
			})
			if err != nil {
				t.Error(err)
			}
		})
	}
	wg.Wait()
	c, _ := st.Company(ctx, "acme")
	if len(c.Tags) != 20 {
		t.Errorf("tags = %d, want 20", len(c.Tags))
	}
}

package main

import (
	"context"
	"slices"
	"testing"

	"github.com/spf13/cobra"

	"github.com/itxkarthik/jobhunt/internal/app"
	"github.com/itxkarthik/jobhunt/internal/config"
	"github.com/itxkarthik/jobhunt/internal/model"
	"github.com/itxkarthik/jobhunt/internal/store"
)

func TestPresetOverrides(t *testing.T) {
	st, err := store.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	a := &app.App{Store: st, Config: &config.Config{Defaults: config.Defaults{Countries: []string{"IN"}}}}
	if err := st.SavePreset(context.Background(), "p", model.Filters{
		Countries: []string{"JP"}, Types: []model.JobType{model.TypeInternship}, Keywords: []string{"Go"},
	}); err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		args          []string
		wantCountries []string
		wantKeywords  []string
	}{
		{[]string{"-p", "p"}, []string{"JP"}, []string{"Go"}},                 // preset as saved; config default IN ignored
		{[]string{"-p", "p", "-k", "Rust"}, []string{"JP"}, []string{"Rust"}}, // explicit flag wins
		{[]string{"-p", "p", "-c", "US"}, []string{"US"}, []string{"Go"}},
		{[]string{"-k", "Rust"}, []string{"IN"}, []string{"Rust"}}, // no preset: config default
	}
	for _, tt := range tests {
		var ff filterFlags
		var got model.Filters
		cmd := &cobra.Command{Use: "x", RunE: func(cmd *cobra.Command, _ []string) error {
			var err error
			got, err = ff.resolve(cmd, a)
			return err
		}}
		ff.register(cmd)
		cmd.SetArgs(tt.args)
		if err := cmd.ExecuteContext(context.Background()); err != nil {
			t.Fatalf("%v: %v", tt.args, err)
		}
		if !slices.Equal(got.Countries, tt.wantCountries) || !slices.Equal(got.Keywords, tt.wantKeywords) {
			t.Errorf("%v: countries=%v keywords=%v", tt.args, got.Countries, got.Keywords)
		}
		if tt.args[0] == "-p" && !got.WantsType(model.TypeInternship) {
			t.Errorf("%v: lost preset type", tt.args)
		}
	}
}

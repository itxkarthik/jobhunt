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

func TestProfileFlags(t *testing.T) {
	st, err := store.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	a := &app.App{Store: st, Config: &config.Config{
		Defaults: config.Defaults{Countries: []string{"IN"}},
		Profile:  config.Profile{Countries: []string{"IN"}, Areas: []string{"backend"}, Stack: []string{"go"}, SkipRequiresExperience: true},
	}}
	if err := st.SavePreset(context.Background(), "p", model.Filters{Types: []model.JobType{model.TypeInternship}}); err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		args          []string
		wantCountries []string
		wantAreas     []model.Area
	}{
		{[]string{"--profile"}, []string{"IN"}, []model.Area{model.AreaBackend}},
		{[]string{"--profile", "-c", "US"}, []string{"US"}, []model.Area{model.AreaBackend}},
		{[]string{"--profile", "-c", "any", "-a", "any"}, nil, nil},
		{[]string{"--profile", "-p", "p"}, []string{"IN"}, []model.Area{model.AreaBackend}},             // preset leaves them empty: profile fills
		{[]string{"--profile", "-p", "p", "-c", "any", "-a", "any"}, nil, nil},                          // "any" stays any
		{[]string{"--profile", "-p", "p", "-c", "US"}, []string{"US"}, []model.Area{model.AreaBackend}}, // flag > profile
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
		if !slices.Equal(got.Countries, tt.wantCountries) || !slices.Equal(got.Areas, tt.wantAreas) {
			t.Errorf("%v: countries=%v areas=%v", tt.args, got.Countries, got.Areas)
		}
		if !got.SkipRequiresExperience || len(got.Stack) != 1 {
			t.Errorf("%v: profile extras missing: %+v", tt.args, got)
		}
	}
}

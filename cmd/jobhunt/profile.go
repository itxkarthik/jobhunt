package main

import (
	"fmt"
	"strings"

	"github.com/spf13/cobra"
	"gopkg.in/yaml.v3"

	"github.com/itxkarthik/jobhunt/internal/app"
	"github.com/itxkarthik/jobhunt/internal/config"
)

func newProfileCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "profile",
		Short: "Your profile: the roles, stack and companies you want",
		Long: `The profile in config.yaml says what you're looking for. search, feed and
tui use it with --profile; companies uses its companies section with
--profile; digest uses it by default.`,
	}
	var asJSON bool
	show := &cobra.Command{
		Use:   "show",
		Short: "Print the profile and check it",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			cfg, err := config.Load()
			if err != nil {
				return err
			}
			p := cfg.Profile
			w := cmd.OutOrStdout()
			if asJSON {
				return writeJSON(w, p)
			}
			if p.IsZero() {
				path, _ := config.Path()
				fmt.Fprintf(w, "No profile set. Add a profile section to %s; config.example.yaml has a commented one.\n", path)
				return nil
			}
			if err := app.CheckProfile(cfg.Defaults, p); err != nil {
				return err
			}
			f, err := app.ProfileFilters(cfg.Defaults, p)
			if err != nil {
				return err
			}
			b, err := yaml.Marshal(map[string]config.Profile{"profile": p})
			if err != nil {
				return err
			}
			fmt.Fprint(w, string(b))
			fmt.Fprintf(w, "\nJobs (--profile): %s\n", summary(f))
			var notes []string
			if f.SkipRequiresExperience {
				notes = append(notes, "skips postings asking for prior experience")
			}
			if len(f.SkipAreas) > 0 {
				notes = append(notes, fmt.Sprintf("skips %s unless also in a wanted area", joinStr(strs(f.SkipAreas))))
			}
			if f.SkipOnsiteAbroad {
				notes = append(notes, "skips onsite roles outside your countries")
			}
			if len(f.Stack) > 0 {
				notes = append(notes, "ranks "+strings.Join(f.Stack, " > "))
			}
			if len(notes) > 0 {
				fmt.Fprintf(w, "  %s\n", strings.Join(notes, "; "))
			}
			return nil
		},
	}
	show.Flags().BoolVar(&asJSON, "json", false, "print JSON")
	cmd.AddCommand(show)
	return cmd
}

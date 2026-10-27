package main

import (
	"github.com/spf13/cobra"

	"github.com/itxkarthik/jobhunt/internal/app"
	"github.com/itxkarthik/jobhunt/internal/tui"
)

func newTUICmd() *cobra.Command {
	var ff filterFlags
	cmd := &cobra.Command{
		Use:   "tui",
		Short: "Interactive browser: filters, results table, details, tracker keys",
		Example: `  jobhunt tui
  jobhunt tui -c IN -t internship -a backend`,
		RunE: func(cmd *cobra.Command, _ []string) error {
			a, err := app.Open()
			if err != nil {
				return err
			}
			defer a.Close()
			f, err := ff.toFilters(a.Config)
			if err != nil {
				return err
			}
			return tui.Run(a, f)
		},
	}
	ff.register(cmd)
	return cmd
}

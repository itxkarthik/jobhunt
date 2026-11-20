package main

import (
	"github.com/spf13/cobra"

	"github.com/itxkarthik/jobhunt/internal/app"
	"github.com/itxkarthik/jobhunt/internal/tui"
)

func newTUICmd() *cobra.Command {
	var (
		ff      filterFlags
		newOnly bool
	)
	cmd := &cobra.Command{
		Use:   "tui",
		Short: "Interactive browser: filters, results table, details, tracker keys",
		Example: `  jobhunt tui
  jobhunt tui -c IN -t internship -a backend
  jobhunt tui --profile --new-only   # what appeared since your last search; n toggles`,
		RunE: func(cmd *cobra.Command, _ []string) error {
			a, err := app.Open()
			if err != nil {
				return err
			}
			defer a.Close()
			f, err := ff.resolve(cmd, a)
			if err != nil {
				return err
			}
			return tui.Run(a, f, newOnly)
		},
	}
	ff.register(cmd)
	cmd.Flags().BoolVar(&newOnly, "new-only", false, "start showing only jobs not seen in any earlier search (n toggles)")
	return cmd
}

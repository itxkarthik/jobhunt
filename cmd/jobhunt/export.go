package main

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"

	"github.com/itxkarthik/jobhunt/internal/app"
	"github.com/itxkarthik/jobhunt/internal/export"
	"github.com/itxkarthik/jobhunt/internal/model"
)

func newExportCmd() *cobra.Command {
	var (
		ff      filterFlags
		so      searchOpts
		format  string
		out     string
		tracked bool
	)
	cmd := &cobra.Command{
		Use:   "export",
		Short: "Export search results or the tracker to CSV, JSON, or Markdown",
		Example: `  jobhunt export -c IN -t internship -a backend -o jobs.csv
  jobhunt export --preset india-backend --format md -o jobs.md
  jobhunt export --tracked -o applications.json`,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if format == "" {
				format = export.FormatFor(out)
			}
			a, err := app.Open()
			if err != nil {
				return err
			}
			defer a.Close()
			statuses, err := a.Store.StatusMap(cmd.Context())
			if err != nil {
				return err
			}
			var rows []export.Row
			if tracked {
				ts, err := a.Store.TrackedJobs(cmd.Context())
				if err != nil {
					return err
				}
				for _, t := range ts {
					j := model.Job{Key: t.Key}
					if t.Job != nil {
						j = *t.Job
					}
					rows = append(rows, export.Row{Job: j, Status: string(t.Status)})
				}
			} else {
				f, err := ff.resolve(cmd, a)
				if err != nil {
					return err
				}
				fmt.Fprintf(cmd.ErrOrStderr(), "Searching %d sources…\n", len(a.Sources))
				res, err := so.run(cmd.Context(), a, f)
				if err != nil {
					return err
				}
				for _, j := range res.Jobs {
					rows = append(rows, export.Row{Job: j, Status: string(statuses[j.Key])})
				}
			}
			if out == "" || out == "-" {
				return export.Write(cmd.OutOrStdout(), format, rows)
			}
			fh, err := os.Create(out)
			if err != nil {
				return err
			}
			if err := export.Write(fh, format, rows); err != nil {
				_ = fh.Close() // the write error matters more
				return err
			}
			if err := fh.Close(); err != nil {
				return err
			}
			fmt.Fprintf(cmd.ErrOrStderr(), "wrote %d jobs to %s (%s)\n", len(rows), out, format)
			return nil
		},
	}
	ff.register(cmd)
	so.register(cmd)
	cmd.Flags().StringVarP(&format, "format", "f", "", "csv, json or md (default: from -o extension, else csv)")
	cmd.Flags().StringVarP(&out, "output", "o", "", "output file (default stdout)")
	cmd.Flags().BoolVar(&tracked, "tracked", false, "export the tracker instead of running a search")
	return cmd
}

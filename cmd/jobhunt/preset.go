package main

import (
	"encoding/json"
	"fmt"
	"text/tabwriter"

	"github.com/spf13/cobra"

	"github.com/itxkarthik/jobhunt/internal/app"
	"github.com/itxkarthik/jobhunt/internal/model"
)

func newPresetCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "preset",
		Short: "Save and reuse filter sets",
		Example: `  jobhunt preset save india-backend -c IN -t internship -a backend
  jobhunt search --preset india-backend
  jobhunt search -p india-backend -k Go        # preset plus an override
  jobhunt preset list`,
	}
	var ff filterFlags
	save := &cobra.Command{
		Use:   "save <name> [filter flags]",
		Short: "Save the given filters under a name (overwrites)",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			a, err := app.Open()
			if err != nil {
				return err
			}
			defer a.Close()
			f, err := ff.resolve(cmd, a)
			if err != nil {
				return err
			}
			if err := a.Store.SavePreset(cmd.Context(), args[0], f); err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "saved preset %s: %s\n", args[0], summary(f))
			return nil
		},
	}
	ff.register(save)

	list := &cobra.Command{
		Use: "list", Short: "List presets",
		RunE: func(cmd *cobra.Command, _ []string) error {
			a, err := app.Open()
			if err != nil {
				return err
			}
			defer a.Close()
			ps, err := a.Store.Presets(cmd.Context())
			if err != nil {
				return err
			}
			if len(ps) == 0 {
				fmt.Fprintln(cmd.OutOrStdout(), "No presets. Create one: jobhunt preset save <name> -c IN -t internship -a backend")
				return nil
			}
			tw := tabwriter.NewWriter(cmd.OutOrStdout(), 0, 0, 2, ' ', 0)
			for _, p := range ps {
				fmt.Fprintf(tw, "%s\t%s\n", p.Name, summary(p.Filters))
			}
			tw.Flush()
			return nil
		},
	}
	show := &cobra.Command{
		Use: "show <name>", Short: "Print a preset as JSON", Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			a, err := app.Open()
			if err != nil {
				return err
			}
			defer a.Close()
			p, err := a.Store.Preset(cmd.Context(), args[0])
			if err != nil {
				return err
			}
			b, _ := json.MarshalIndent(p.Filters, "", "  ")
			fmt.Fprintln(cmd.OutOrStdout(), string(b))
			return nil
		},
	}
	del := &cobra.Command{
		Use: "delete <name>", Aliases: []string{"rm"}, Short: "Delete a preset", Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			a, err := app.Open()
			if err != nil {
				return err
			}
			defer a.Close()
			if err := a.Store.DeletePreset(cmd.Context(), args[0]); err != nil {
				return err
			}
			fmt.Fprintln(cmd.OutOrStdout(), "deleted", args[0])
			return nil
		},
	}
	cmd.AddCommand(save, list, show, del)
	return cmd
}

// summary renders filters compactly: "IN · internship · backend · kw:Go".
func summary(f model.Filters) string {
	var parts []string
	add := func(prefix string, xs []string) {
		if len(xs) > 0 {
			parts = append(parts, prefix+joinStr(xs))
		}
	}
	add("", f.Countries)
	add("", strs(f.Types))
	add("", strs(f.Modes))
	add("", strs(f.Areas))
	add("level:", strs(f.Experience))
	add("kw:", f.Keywords)
	add("-", f.Exclude)
	if f.PostedWithin > 0 {
		parts = append(parts, fmt.Sprintf("≤%.0fd", f.PostedWithin.Hours()/24))
	}
	if f.VisaOnly {
		parts = append(parts, "visa")
	}
	if f.MinSalary > 0 {
		parts = append(parts, fmt.Sprintf("≥%.0f %s", f.MinSalary, f.Currency))
	}
	if len(parts) == 0 {
		return "(no filters)"
	}
	out := parts[0]
	for _, p := range parts[1:] {
		out += " · " + p
	}
	return out
}

func strs[T ~string](xs []T) []string {
	out := make([]string, len(xs))
	for i, x := range xs {
		out[i] = string(x)
	}
	return out
}

func joinStr(xs []string) string {
	out := ""
	for i, x := range xs {
		if i > 0 {
			out += ","
		}
		out += x
	}
	return out
}

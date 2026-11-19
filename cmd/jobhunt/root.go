package main

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/spf13/cobra"

	"github.com/itxkarthik/jobhunt/internal/config"
)

func newRootCmd() *cobra.Command {
	root := &cobra.Command{
		Use:           "jobhunt",
		Short:         "Search many job & internship sources at once",
		SilenceUsage:  true,
		SilenceErrors: true,
	}
	root.AddCommand(newSearchCmd(), newTUICmd(), newTrackerCmd(), newCompaniesCmd(),
		newMCPCmd(), newPresetCmd(), newExportCmd(),
		newWatchCmd(), newFeedCmd(), newDigestCmd(), newProfileCmd(), newServeCmd(), newSourcesCmd(), newConfigCmd())
	root.AddCommand(newHideCmds()...)
	return root
}

func newConfigCmd() *cobra.Command {
	cmd := &cobra.Command{Use: "config", Short: "Manage ~/.config/jobhunt/config.yaml"}
	cmd.AddCommand(&cobra.Command{
		Use:   "path",
		Short: "Print the config file path",
		RunE: func(cmd *cobra.Command, _ []string) error {
			p, err := config.Path()
			if err != nil {
				return err
			}
			fmt.Fprintln(cmd.OutOrStdout(), p)
			return nil
		},
	})
	cmd.AddCommand(&cobra.Command{
		Use:   "init",
		Short: "Write a starter config file (won't overwrite)",
		RunE: func(cmd *cobra.Command, _ []string) error {
			p, err := config.Path()
			if err != nil {
				return err
			}
			if _, err := os.Stat(p); err == nil {
				return fmt.Errorf("%s already exists", p)
			}
			if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
				return err
			}
			if err := os.WriteFile(p, config.Example, 0o600); err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "wrote %s — add your API keys there\n", p)
			return nil
		},
	})
	return cmd
}

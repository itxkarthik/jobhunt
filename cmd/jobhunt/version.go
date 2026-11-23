package main

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/itxkarthik/jobhunt/internal/version"
)

func newVersionCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "version",
		Short: "Print the version, commit and build date",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			fmt.Fprint(cmd.OutOrStdout(), versionText())
			return nil
		},
	}
}

// versionText is what `jobhunt version` and `jobhunt --version` print,
// with the attribution NOTICE asks for.
func versionText() string {
	return fmt.Sprintf("jobhunt %s\n%s\nLicensed under AGPL-3.0-only with additional terms; see LICENSE and NOTICE.\n",
		version.Get(), version.Attribution)
}

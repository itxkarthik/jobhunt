package main

import (
	"log/slog"
	"os"
	"os/signal"
	"runtime/debug"

	"github.com/spf13/cobra"

	"github.com/itxkarthik/jobhunt/internal/app"
	"github.com/itxkarthik/jobhunt/internal/mcpserver"
)

func newMCPCmd() *cobra.Command {
	var readOnly bool
	cmd := &cobra.Command{
		Use:   "mcp",
		Short: "Serve jobhunt to AI agents over MCP (stdio)",
		Long: `mcp runs a Model Context Protocol server on stdin and stdout, for an
agent host such as Claude Code to start. It opens no network port.

Read tools: search_jobs, get_job, list_companies, get_company, list_sources.
Write tools: upsert_company, add_contact, add_board, track_job, add_note.
--read-only leaves the write tools out.

Everything an agent sends is validated and sanitized before it is stored.
Contacts and funding rounds need the URL where they were published, and no
tool returns keys or tokens. Logs go to stderr.`,
		Example: `  claude mcp add jobhunt -- jobhunt mcp
  jobhunt mcp --read-only`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			a, err := app.Open()
			if err != nil {
				return err
			}
			defer a.Close()
			ctx, stop := signal.NotifyContext(cmd.Context(), os.Interrupt)
			defer stop()
			return mcpserver.Run(ctx, mcpserver.Options{
				App:      a,
				ReadOnly: readOnly,
				Version:  buildVersion(),
				Log:      slog.New(slog.NewTextHandler(cmd.ErrOrStderr(), nil)),
			})
		},
	}
	cmd.Flags().BoolVar(&readOnly, "read-only", false, "offer only the read tools")
	return cmd
}

func buildVersion() string {
	if bi, ok := debug.ReadBuildInfo(); ok && bi.Main.Version != "" {
		return bi.Main.Version
	}
	return "dev"
}

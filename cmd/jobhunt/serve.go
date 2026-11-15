package main

import (
	"context"
	"crypto/rand"
	"fmt"
	"io"
	"log/slog"
	"net"
	"slices"
	"strconv"

	"github.com/spf13/cobra"

	"github.com/itxkarthik/jobhunt/internal/app"
	"github.com/itxkarthik/jobhunt/internal/browser"
	"github.com/itxkarthik/jobhunt/internal/model"
	"github.com/itxkarthik/jobhunt/internal/search"
	"github.com/itxkarthik/jobhunt/internal/server"
)

func newServeCmd() *cobra.Command {
	var (
		addr       string
		open       bool
		allowHosts []string
	)
	cmd := &cobra.Command{
		Use:   "serve",
		Short: "Run the local web UI and JSON API",
		Long: `serve starts jobhunt's local HTTP server: the JSON API under /api/ (see
docs/api.md) and, from the web UI milestone on, the browser interface.

It listens on 127.0.0.1 only by default. Every /api request needs the bearer
token printed at startup ("Authorization: Bearer <token>"); set serve.token
or JOBHUNT_SERVE_TOKEN for a stable one. Requests with a foreign Host header
are rejected, which stops websites from reaching the API via DNS rebinding.`,
		Example: `  jobhunt serve
  jobhunt serve --open
  JOBHUNT_SERVE_TOKEN=… jobhunt serve --addr 127.0.0.1:8080`,
		RunE: func(cmd *cobra.Command, _ []string) error {
			a, err := app.Open()
			if err != nil {
				return err
			}
			defer a.Close()
			out, log := cmd.OutOrStdout(), cmd.ErrOrStderr()

			token, fixed := a.Config.Serve.Token, a.Config.Serve.Token != ""
			if !fixed {
				token = rand.Text()
			}
			hosts, err := serveHosts(addr, allowHosts, log)
			if err != nil {
				return err
			}
			srv, err := server.New(server.Options{
				Store:    a.Store,
				Search:   engineSearch(a),
				Sources:  a.Sources,
				Catalog:  catalog(),
				Defaults: a.Config.Defaults,
				Token:    token,
				Hosts:    hosts,
				Logger:   slog.New(slog.NewTextHandler(log, nil)),
			})
			if err != nil {
				return err
			}
			var lc net.ListenConfig
			ln, err := lc.Listen(cmd.Context(), "tcp", addr)
			if err != nil {
				return err
			}
			base := baseURL(ln.Addr())
			printServeBanner(out, base, token, fixed)
			if open {
				// The token rides in the URL fragment, which browsers never
				// send to the server or put in Referer headers.
				if err := browser.Open(base + "/#token=" + token); err != nil {
					fmt.Fprintf(log, "could not open a browser: %v\n", err)
				}
			}
			return srv.Serve(cmd.Context(), ln)
		},
	}
	fs := cmd.Flags()
	fs.StringVar(&addr, "addr", "127.0.0.1:7777", "address to listen on (loopback by default)")
	fs.BoolVar(&open, "open", false, "open the web UI in your browser")
	fs.StringSliceVar(&allowHosts, "allow-host", nil, "extra Host names to accept, needed when listening beyond loopback")
	return cmd
}

// serveHosts returns the extra Host names to accept: --allow-host plus the
// address being listened on, so the URL in the banner always works (e.g.
// --addr 127.0.0.2:7777). Listening beyond loopback is allowed but loud,
// since the API exposes your tracker notes.
func serveHosts(addr string, extra []string, log io.Writer) ([]string, error) {
	host, _, err := net.SplitHostPort(addr)
	if err != nil {
		return nil, fmt.Errorf("bad --addr %q: %w", addr, err)
	}
	hosts := slices.Clone(extra)
	ip := net.ParseIP(host)
	unspecified := host == "" || (ip != nil && ip.IsUnspecified())
	if !unspecified {
		hosts = append(hosts, host)
	}
	if server.IsLoopback(addr) {
		return hosts, nil
	}
	fmt.Fprintf(log, "warning: listening on %s, beyond loopback; anyone on your network with the token can use the API\n", addr)
	if unspecified && len(extra) == 0 {
		fmt.Fprintln(log, "note: only localhost Host headers are accepted; add --allow-host <name-or-ip> for other clients")
	}
	return hosts, nil
}

// baseURL is the URL to print for a listener. A wildcard listener is
// reachable (and allowed) via loopback.
func baseURL(a net.Addr) string {
	tcp, ok := a.(*net.TCPAddr)
	if ok && tcp.IP.IsUnspecified() {
		return "http://" + net.JoinHostPort("127.0.0.1", strconv.Itoa(tcp.Port))
	}
	return "http://" + a.String()
}

func printServeBanner(w io.Writer, base, token string, fixed bool) {
	fmt.Fprintf(w, "jobhunt is serving on %s — Ctrl+C to stop\n", base)
	if fixed {
		fmt.Fprintln(w, "  API token: from serve.token / JOBHUNT_SERVE_TOKEN")
		fmt.Fprintf(w, "  web UI:    %s/#token=<your token>\n", base)
		return
	}
	fmt.Fprintf(w, "  web UI:    %s/#token=%s\n", base, token)
	fmt.Fprintf(w, "  API:       curl -H 'Authorization: Bearer %s' %s/api/health\n", token, base)
}

// engineSearch adapts the app's search engine to the server.
func engineSearch(a *app.App) server.SearchFunc {
	return func(ctx context.Context, f model.Filters, o server.SearchOptions, progress func(search.Report)) (*search.Result, error) {
		eng := a.Engine()
		eng.Refresh, eng.NewOnly, eng.ShowHidden, eng.SortBy = o.Refresh, o.NewOnly, o.ShowHidden, o.Sort
		eng.Progress = progress
		return eng.Run(ctx, f)
	}
}

// catalog describes every known source for /api/sources.
func catalog() []server.SourceDesc {
	out := make([]server.SourceDesc, 0, len(app.Catalog))
	for _, info := range app.Catalog {
		out = append(out, server.SourceDesc{Name: info.Name, Description: info.Description, Credential: info.Key})
	}
	return out
}

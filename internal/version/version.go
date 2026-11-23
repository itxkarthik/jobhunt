// Package version reports which build of jobhunt is running.
package version

import (
	"fmt"
	"regexp"
	"runtime"
	"runtime/debug"
	"strings"
)

// Set at build time by `make build`, `make install` and releases:
//
//	-ldflags "-X github.com/itxkarthik/jobhunt/internal/version.Version=v0.8.0 …"
//
// When they're empty, Get falls back to what Go records in the binary.
var (
	Version = "" // e.g. v0.8.0, or v0.8.0-3-g1a2b3c4-dirty between tags
	Commit  = "" // short commit hash
	Date    = "" // build date, YYYY-MM-DD
)

// Attribution is the author credit NOTICE requires wherever jobhunt shows
// its about information.
const Attribution = "Originally created by itxkarthik (https://github.com/itxkarthik/jobhunt)"

// pseudoRe matches the pseudo-version Go gives a build between tags
// ("v0.0.0-20261120070503-02a825c0b60a+dirty"). It only repeats the commit
// and date, so such builds report "dev".
var pseudoRe = regexp.MustCompile(`-(0\.)?\d{14}-[0-9a-f]{12}`)

// Info describes the running build.
type Info struct {
	Version string `json:"version"`
	Commit  string `json:"commit,omitempty"`
	Date    string `json:"date,omitempty"`
	Go      string `json:"go"`
}

// Get returns the build's version details: the -ldflags values when set,
// otherwise what Go recorded (the module version for `go install …@v0.8.0`,
// and the VCS revision and time for a build from a checkout).
func Get() Info {
	i := Info{Version: Version, Commit: Commit, Date: Date, Go: runtime.Version()}
	if bi, ok := debug.ReadBuildInfo(); ok {
		if v := bi.Main.Version; i.Version == "" && v != "(devel)" && !pseudoRe.MatchString(v) {
			i.Version = v // a release installed with go install …@v0.8.0
		}
		modified := false
		for _, s := range bi.Settings {
			switch s.Key {
			case "vcs.revision":
				if i.Commit == "" {
					i.Commit = s.Value[:min(7, len(s.Value))]
				}
			case "vcs.time":
				if i.Date == "" {
					i.Date, _, _ = strings.Cut(s.Value, "T")
				}
			case "vcs.modified":
				modified = s.Value == "true"
			}
		}
		if Commit == "" && modified && i.Commit != "" {
			i.Commit += "-dirty"
		}
	}
	if i.Version == "" {
		i.Version = "dev"
	}
	return i
}

// String is the one-line form: "v0.8.0 (1a2b3c4, 2026-11-23, go1.27.4)".
func (i Info) String() string {
	var extra []string
	for _, s := range []string{i.Commit, i.Date, i.Go} {
		if s != "" {
			extra = append(extra, s)
		}
	}
	return fmt.Sprintf("%s (%s)", i.Version, strings.Join(extra, ", "))
}

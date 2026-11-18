// Package web is jobhunt's browser UI: static files embedded in the binary
// and served by internal/server at "/".
//
// It is plain HTML, CSS and JavaScript modules with no build step and no
// external requests, and it talks to jobhunt only through the JSON API
// (docs/api.md), so anything it does a script can do too.
//
// Job text comes from the open internet. The UI renders it with
// textContent and DOM APIs only, never as HTML, and links only http(s)
// URLs; web_test.go guards both rules.
package web

import (
	"embed"
	"io/fs"
)

//go:embed static
var files embed.FS

// FS returns the UI files, rooted so that index.html is at the top.
func FS() fs.FS {
	sub, err := fs.Sub(files, "static")
	if err != nil {
		panic(err) // "static" is embedded above; this can't happen
	}
	return sub
}

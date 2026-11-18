package web

import (
	"io/fs"
	"path"
	"regexp"
	"slices"
	"strings"
	"testing"
)

func readAll(t *testing.T) map[string]string {
	t.Helper()
	out := map[string]string{}
	err := fs.WalkDir(FS(), ".", func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		b, err := fs.ReadFile(FS(), p)
		out[p] = string(b)
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	return out
}

func TestShell(t *testing.T) {
	files := readAll(t)
	index, ok := files["index.html"]
	if !ok {
		t.Fatal("index.html is missing")
	}
	for _, ref := range []string{`href="app.css"`, `src="js/app.js"`} {
		if !strings.Contains(index, ref) {
			t.Errorf("index.html doesn't reference %s", ref)
		}
	}
	// Every module the router can load must exist.
	for _, view := range []string{"search", "tracker", "sources"} {
		if _, ok := files["js/"+view+".js"]; !ok {
			t.Errorf("js/%s.js is missing", view)
		}
	}
}

// The UI renders text from the open internet. These patterns would let it
// run as HTML or script, so they must not appear anywhere in the UI code.
func TestNoHTMLSinks(t *testing.T) {
	sinks := regexp.MustCompile(`innerHTML|outerHTML|insertAdjacentHTML|document\.write|\beval\(|new Function\(|createContextualFragment|DOMParser|\.srcdoc`)
	for p, src := range readAll(t) {
		if path.Ext(p) != ".js" {
			continue
		}
		if m := sinks.FindString(src); m != "" {
			t.Errorf("%s uses %s; build DOM nodes and set textContent instead", p, m)
		}
	}
}

// The CSP allows only 'self', and the UI must work offline: no inline
// script or style, no handler attributes, no external URLs.
func TestNoExternalOrInlineCode(t *testing.T) {
	// Any quoted absolute or protocol-relative URL counts: job URLs come
	// from the API at run time, never from the UI code.
	external := regexp.MustCompile("(?i)[\"'`(=]\\s*(https?:)?//|@import")
	inline := regexp.MustCompile(`(?i)<script(\s[^>]*)?>[^<\s]|<style|\sstyle\s*=|\son[a-z]+\s*=`)
	for p, src := range readAll(t) {
		if !slices.Contains([]string{".html", ".css", ".js"}, path.Ext(p)) {
			continue // e.g. licence texts, which the browser never runs
		}
		if m := external.FindString(src); m != "" {
			t.Errorf("%s loads an external resource: %q", p, m)
		}
		if path.Ext(p) == ".html" {
			if m := inline.FindString(src); m != "" {
				t.Errorf("%s has inline code the CSP would block: %q", p, m)
			}
		}
	}
}

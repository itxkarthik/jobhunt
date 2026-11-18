package main

import (
	"bytes"
	"net"
	"net/url"
	"slices"
	"strings"
	"testing"

	"github.com/itxkarthik/jobhunt/internal/app"
)

func TestServeHosts(t *testing.T) {
	tests := []struct {
		addr     string
		extra    []string
		want     []string
		wantWarn bool
	}{
		{"127.0.0.1:7777", nil, []string{"127.0.0.1"}, false},
		{"127.0.0.2:7777", nil, []string{"127.0.0.2"}, false}, // regression: banner URL got 403
		{"localhost:7777", nil, []string{"localhost"}, false},
		{"[::1]:7777", nil, []string{"::1"}, false},
		{"192.168.1.5:7777", nil, []string{"192.168.1.5"}, true},
		{"0.0.0.0:7777", []string{"jobs.lan"}, []string{"jobs.lan"}, true},
		{":7777", nil, nil, true},
	}
	for _, tt := range tests {
		var log bytes.Buffer
		got, err := serveHosts(tt.addr, tt.extra, &log)
		if err != nil {
			t.Fatalf("%s: %v", tt.addr, err)
		}
		if !slices.Equal(got, tt.want) {
			t.Errorf("%s: hosts = %v, want %v", tt.addr, got, tt.want)
		}
		if warned := strings.Contains(log.String(), "warning"); warned != tt.wantWarn {
			t.Errorf("%s: warning printed = %v, want %v (%q)", tt.addr, warned, tt.wantWarn, log.String())
		}
	}
	if _, err := serveHosts("no-port", nil, &bytes.Buffer{}); err == nil {
		t.Error("bad address accepted")
	}
}

func TestBaseURL(t *testing.T) {
	for addr, want := range map[string]string{
		"127.0.0.2:7777": "http://127.0.0.2:7777",
		"0.0.0.0:7777":   "http://127.0.0.1:7777",
		"[::]:7777":      "http://127.0.0.1:7777",
	} {
		a, err := net.ResolveTCPAddr("tcp", addr)
		if err != nil {
			t.Fatal(err)
		}
		if got := baseURL(a); got != want {
			t.Errorf("baseURL(%s) = %s, want %s", addr, got, want)
		}
	}
}

func TestCatalogListsEverySource(t *testing.T) {
	got := catalog()
	if len(got) != len(app.Catalog) {
		t.Fatalf("catalog has %d sources, want %d", len(got), len(app.Catalog))
	}
	for i, info := range app.Catalog {
		if d := got[i]; d.Name != info.Name || d.Description != info.Description || d.Credential != info.Key {
			t.Errorf("catalog[%d] = %+v, want %s", i, d, info.Name)
		}
	}
}

func TestUIURL(t *testing.T) {
	for _, tok := range []string{"ABCDEFGHJKLMNPQRSTUVWXYZ23", "a/b+c&d#e%f g=h"} {
		u := uiURL("http://127.0.0.1:7777", tok)
		frag, ok := strings.CutPrefix(u, "http://127.0.0.1:7777/#token=")
		if !ok {
			t.Fatalf("uiURL(%q) = %q", tok, u)
		}
		if strings.ContainsAny(frag, "/# ") {
			t.Errorf("uiURL(%q) = %q: fragment isn't escaped", tok, u)
		}
		// The web UI decodes the fragment with decodeURIComponent, which
		// matches url.PathUnescape for these characters.
		if got, err := url.PathUnescape(frag); err != nil || got != tok {
			t.Errorf("round trip of %q = %q, %v", tok, got, err)
		}
	}
}

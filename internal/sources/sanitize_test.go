package sources

import (
	"testing"

	"github.com/itxkarthik/jobhunt/internal/model"
)

func TestSafeText(t *testing.T) {
	tests := []struct {
		in        string
		multiline bool
		want      string
	}{
		{"Backend Intern", false, "Backend Intern"},
		{"Evil\x1b[2J\x1b]0;pwned\x07 Intern", false, "Evil[2J]0;pwned Intern"},
		{"clip\x1b]52;c;ZXZpbA==\x07board", false, "clip]52;c;ZXZpbA==board"},
		{"line1\nline2\tx", false, "line1 line2 x"},
		{"line1\nline2\tx\r", true, "line1\nline2\tx"},
		{"admin\u202etxt.exe", false, "admintxt.exe"},
		{"C1\u009bcontrol", false, "C1control"},
		{"Bengaluru — ₹ ok", false, "Bengaluru — ₹ ok"},
	}
	for _, tt := range tests {
		if got := SafeText(tt.in, tt.multiline); got != tt.want {
			t.Errorf("SafeText(%q, %v) = %q, want %q", tt.in, tt.multiline, got, tt.want)
		}
	}
}

func TestSafeURL(t *testing.T) {
	tests := map[string]string{
		"https://boards.greenhouse.io/x/jobs/1": "https://boards.greenhouse.io/x/jobs/1",
		"\thttp://a.example/b\n":                "http://a.example/b", // surrounding whitespace is trimmed
		"javascript:alert(1)":                   "",
		"file:///etc/passwd":                    "",
		"https://a.example/\x1b[31m":            "",
		"/relative/path":                        "",
		"":                                      "",
	}
	for in, want := range tests {
		if got := SafeURL(in); got != want {
			t.Errorf("SafeURL(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestSanitizeJob(t *testing.T) {
	j := model.Job{Title: "A\x1b[1mB", Description: "x\ny\x07", URL: "javascript:x", Tags: []string{"t\x1b"}}
	SanitizeJob(&j)
	if j.Title != "A[1mB" || j.Description != "x\ny" || j.URL != "" || j.Tags[0] != "t" {
		t.Errorf("got %+v", j)
	}
}

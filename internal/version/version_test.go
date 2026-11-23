package version

import "testing"

func TestPseudoVersion(t *testing.T) {
	for v, pseudo := range map[string]bool{
		"v0.8.0": false, "v0.8.1-rc.1": false,
		"v0.0.0-20261120070503-02a825c0b60a+dirty": true, "v0.8.1-0.20261123101500-1a2b3c4d5e6f": true,
	} {
		if pseudoRe.MatchString(v) != pseudo {
			t.Errorf("%s: pseudo = %v", v, !pseudo)
		}
	}
	if got := (Info{Version: "v0.8.0", Commit: "1a2b3c4", Date: "2026-11-23", Go: "go1.27.4"}).String(); got != "v0.8.0 (1a2b3c4, 2026-11-23, go1.27.4)" {
		t.Errorf("String() = %q", got)
	}
}

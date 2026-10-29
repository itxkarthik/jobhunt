package notify

import (
	"slices"
	"testing"
)

func TestCommand(t *testing.T) {
	tests := []struct {
		goos     string
		wantName string
		wantArgs []string
		wantErr  bool
	}{
		{"linux", "notify-send", []string{"--app-name=jobhunt", "--icon=dialog-information", "3 new jobs", `Go "Intern"`}, false},
		{"darwin", "osascript", []string{"-e", `display notification "Go \"Intern\"" with title "3 new jobs"`}, false},
		{"plan9", "", nil, true},
	}
	for _, tt := range tests {
		name, args, err := command(tt.goos, "3 new jobs", `Go "Intern"`)
		if (err != nil) != tt.wantErr || name != tt.wantName || !slices.Equal(args, tt.wantArgs) {
			t.Errorf("%s: %s %q %v", tt.goos, name, args, err)
		}
	}
}

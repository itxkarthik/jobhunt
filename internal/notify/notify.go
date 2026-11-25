// Package notify sends desktop notifications via the platform's CLI tool:
// notify-send on Linux/BSD, osascript on macOS.
package notify

import (
	"context"
	"errors"
	"os/exec"
	"runtime"
	"strings"
	"time"
)

// run executes a command; replaced in tests.
var run = func(ctx context.Context, name string, args ...string) error {
	return exec.CommandContext(ctx, name, args...).Run() //nolint:gosec // G204: name is notify-send/osascript; args are argv, not a shell
}

// ErrUnsupported means no notifier exists for this platform.
var ErrUnsupported = errors.New("desktop notifications not supported on " + runtime.GOOS)

// Send shows a notification.
func Send(title, body string) error {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	name, args, err := command(runtime.GOOS, title, body)
	if err != nil {
		return err
	}
	return run(ctx, name, args...)
}

func command(goos, title, body string) (string, []string, error) {
	switch goos {
	case "darwin":
		script := "display notification " + quote(body) + " with title " + quote(title)
		return "osascript", []string{"-e", script}, nil
	case "linux", "freebsd", "openbsd", "netbsd":
		// Job titles reach the body, so "--" keeps one starting with "-"
		// from being read as an option, and markup is escaped: servers
		// render <b>, <a> and sometimes <img> in the body.
		return "notify-send", []string{"--app-name=jobhunt", "--icon=dialog-information", "--", markup.Replace(title), markup.Replace(body)}, nil
	}
	return "", nil, ErrUnsupported
}

// markup escapes text for the notification body's markup (freedesktop
// notification spec).
var markup = strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;")

// quote makes an AppleScript string literal.
func quote(s string) string {
	return `"` + strings.NewReplacer(`\`, `\\`, `"`, `\"`).Replace(s) + `"`
}

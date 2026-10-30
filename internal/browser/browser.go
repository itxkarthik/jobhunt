// Package browser opens URLs in the user's default browser.
package browser

import (
	"errors"
	"os/exec"
	"runtime"
	"strings"
)

// Open launches the system URL handler without waiting for it.
func Open(url string) error {
	if !strings.HasPrefix(url, "http://") && !strings.HasPrefix(url, "https://") {
		return errors.New("not an http(s) URL")
	}
	// The URL is validated above and passed as a single argv entry, never
	// through a shell, so it can't inject commands.
	// No CommandContext: the browser must outlive this process.
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "darwin":
		cmd = exec.Command("open", url) //nolint:gosec,noctx // fixed binary, validated URL
	case "windows":
		cmd = exec.Command("rundll32", "url.dll,FileProtocolHandler", url) //nolint:gosec,noctx // fixed binary, validated URL
	default:
		cmd = exec.Command("xdg-open", url) //nolint:gosec,noctx // fixed binary, validated URL
	}
	if err := cmd.Start(); err != nil {
		return err
	}
	go cmd.Wait() //nolint:errcheck // reap the child; its exit status doesn't matter
	return nil
}

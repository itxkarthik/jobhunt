//go:build unix

package file

import (
	"context"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/itxkarthik/jobhunt/internal/model"
)

// A FIFO with no writer would block os.Open forever; /dev/zero never ends.
func TestSpecialFilesDontBlock(t *testing.T) {
	fifo := filepath.Join(t.TempDir(), "jobs.ndjson")
	if err := syscall.Mkfifo(fifo, 0o600); err != nil {
		t.Skip(err)
	}
	for _, p := range []string{fifo, "/dev/zero"} {
		done := make(chan error, 1)
		go func() {
			_, err := New([]string{p}).Fetch(context.Background(), model.Filters{})
			done <- err
		}()
		select {
		case err := <-done:
			if err == nil || !strings.Contains(err.Error(), "not a regular file") {
				t.Errorf("%s: %v", p, err)
			}
		case <-time.After(2 * time.Second):
			t.Fatalf("%s: Fetch blocked", p)
		}
	}
}

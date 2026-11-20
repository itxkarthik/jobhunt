package main

import (
	"context"
	"fmt"
	"runtime/debug"
	"time"
)

// minInterval keeps repeated searches polite to the sources' rate limits.
const minInterval = 15 * time.Minute

func checkInterval(flag string, d time.Duration) error {
	if d < minInterval {
		return fmt.Errorf("--%s %s is too often; use at least %s (sources rate-limit and cache)", flag, d, minInterval)
	}
	return nil
}

// repeat runs fn now and then every interval until ctx is done, or just
// once when once is set. Errors go to report and never stop the loop;
// cancellation ends it cleanly.
func repeat(ctx context.Context, interval time.Duration, once bool, fn func(context.Context) error, report func(error)) {
	for {
		if err := fn(ctx); err != nil {
			if ctx.Err() != nil {
				return
			}
			report(err)
		}
		if once {
			return
		}
		// Hand the run's memory (a search holds a few hundred MB) back to
		// the OS for the long wait, instead of keeping it until the next run.
		debug.FreeOSMemory()
		select {
		case <-ctx.Done():
			return
		case <-time.After(interval):
		}
	}
}

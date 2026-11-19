// Copyright (C) 2026 itxkarthik. Originally created by itxkarthik
// (https://github.com/itxkarthik/jobhunt).
// SPDX-License-Identifier: AGPL-3.0-only
// See LICENSE and NOTICE for the license and its additional terms.

// Command jobhunt searches many job sources at once.
package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"syscall"
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	err := newRootCmd().ExecuteContext(ctx)
	stop()
	if err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}

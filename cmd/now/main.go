// Copyright (c) 2026 Canonical Inc.
// Originally by Gustavo Niemeyer.
// Use of source code is governed by the MIT-style license in the LICENSE file.

// Command now generates a one-shot script from a request and runs it
// after user approval.
package main

import (
	"context"
	"errors"
	"fmt"
	"os"

	"github.com/canonical/now/internal/cli"
)

func main() {
	err := cli.Run(context.Background(), cli.RunOptions{
		Argv:   os.Args[1:],
		Stdin:  os.Stdin,
		Stdout: os.Stdout,
		Stderr: os.Stderr,
	})
	if errors.Is(err, cli.ErrAborted) {
		fmt.Fprintln(os.Stderr, "aborted")
		os.Exit(1)
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}


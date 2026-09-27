// Copyright (c) 2026 Canonical Inc.
// Originally by Gustavo Niemeyer.
// Use of source code is governed by the MIT-style license in the LICENSE file.

package cli

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"os"
)

// ApprovalOptions carries the inputs for the approval step.
type ApprovalOptions struct {
	// Script is the generated script shown for approval.
	Script string
	// Yes auto-approves without asking.
	Yes bool
	// Quiet auto-approves like Yes, and also hides the script.
	Quiet bool
	// Stderr shows the script and the prompt; stdout is reserved for
	// the script's own output.
	Stderr io.Writer
	// TTY is where the approval is read from; the controlling terminal
	// by default (/dev/tty), so a "-" placeholder consuming piped
	// stdin does not interfere.
	TTY io.Reader
}

// Approve shows the script on stderr and asks for confirmation on the
// controlling terminal.
//
// With Yes or Quiet, the script is approved without asking; Quiet
// additionally hides it. Otherwise the user is asked on the controlling
// terminal — read from /dev/tty rather than stdin, so a "-" placeholder
// consuming piped stdin does not interfere — to press ENTER to approve;
// any other input, EOF, or an interrupt cancels. Without a controlling
// terminal there is no way to ask, so approval fails as an error.
func Approve(ctx context.Context, opts ApprovalOptions) (bool, error) {
	if !opts.Quiet {
		fmt.Fprintln(opts.Stderr, opts.Script)
	}

	if opts.Yes || opts.Quiet {
		return true, nil
	}

	tty := opts.TTY
	if tty == nil {
		f, err := os.Open("/dev/tty")
		if err != nil {
			return false, fmt.Errorf("cannot ask for approval without a terminal")
		}
		defer f.Close()
		tty = f
	}

	fmt.Fprintln(opts.Stderr, "[ ENTER | CTRL-C ]")

	type read struct {
		line string
		err  error
		eof  bool
	}
	lines := make(chan read, 1)
	go func() {
		sc := bufio.NewScanner(tty)
		if sc.Scan() {
			lines <- read{line: sc.Text()}
		} else {
			// EOF (nil error) must not read as an empty line.
			lines <- read{err: sc.Err(), eof: true}
		}
	}()

	select {
	case <-ctx.Done():
		fmt.Fprintln(opts.Stderr, "")
		return false, nil
	case r := <-lines:
		if r.err != nil {
			return false, r.err
		}
		return !r.eof && r.line == "", nil
	}
}
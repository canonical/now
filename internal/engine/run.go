// Copyright (c) 2026 Canonical Inc.
// Originally by Gustavo Niemeyer.
// Use of source code is governed by the MIT-style license in the LICENSE file.

package engine

import (
	"context"
	"fmt"
	"io"
	"os/exec"
	"strings"

	"github.com/canonical/now/internal/busybox"
	"github.com/canonical/now/internal/prompt"
	"github.com/canonical/now/internal/sandbox"
)

// RunOptions carries the inputs for script execution.
type RunOptions struct {
	// Args are delivered to the script in "$@".
	Args []string
	// Stdout and Stderr receive the script output.
	Stdout io.Writer
	Stderr io.Writer
	// Trace prints each command to Stderr as it executes, like the
	// shell's -x.
	Trace bool
	// Commands are the external commands allowed in the script, from -c.
	// Those shadowing a busybox applet are aliased to their absolute
	// path so the script runs the real command.
	Commands []prompt.Command
	// Busybox is the resolved busybox that runs the script; the caller
	// must have probed it first, so a missing busybox surfaces before
	// the model is called.
	Busybox busybox.Options
	// SandboxOn confines the execution with bwrap when set. The caller
	// must have probed Sandbox.Bwrap first, before generating the
	// script, so confinement problems surface before any model call.
	SandboxOn bool
	// Sandbox carries the probed confinement configuration used when
	// SandboxOn is set.
	Sandbox sandbox.Options
}

// Run executes the script with the arguments in "$@".
func Run(ctx context.Context, script string, opts RunOptions) error {
	busybox := opts.Busybox.Path
	if busybox == "" {
		return fmt.Errorf("cannot run script: busybox not probed")
	}

	var stdin strings.Builder
	stdin.WriteString(aliasPrelude(opts.Commands))
	stdin.WriteString(script)

	// -e aborts on the first failing command, so a half-executed
	// script never reads as success.
	shellArgs := []string{"sh", "-e"}
	if opts.Trace {
		shellArgs = append(shellArgs, "-x")
	}
	shellArgs = append(shellArgs, "-s")
	if len(opts.Args) > 0 {
		// The separator keeps arguments that start with "-" from
		// being parsed as shell options after -s.
		shellArgs = append(shellArgs, "--")
		shellArgs = append(shellArgs, opts.Args...)
	}

	if opts.SandboxOn {
		// Probing already happened in the caller, before generating
		// the script; the grants arrive ready.
		bargs, err := sandbox.Args(opts.Sandbox, busybox, shellArgs...)
		if err != nil {
			return err
		}
		cmd := exec.CommandContext(ctx, bargs[0], bargs[1:]...)
		cmd.Stdin = strings.NewReader(stdin.String())
		cmd.Stdout = opts.Stdout
		cmd.Stderr = opts.Stderr
		if err := cmd.Run(); err != nil {
			// The script ran and failed — distinct from not running at all.
			return fmt.Errorf("script failed: %w", err)
		}
		return nil
	}

	cmd := exec.CommandContext(ctx, busybox, shellArgs...)
	cmd.Stdin = strings.NewReader(stdin.String())
	cmd.Stdout = opts.Stdout
	cmd.Stderr = opts.Stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("script failed: %w", err)
	}
	return nil
}

// aliasPrelude returns shell lines aliasing every allowed command to its
// absolute path. Shadowing ones must win over the busybox applets, and
// inside the sandbox the others would not resolve through $PATH at all —
// so all commands are aliased uniformly, granting exactly what the user
// resolved at parse time.
func aliasPrelude(commands []prompt.Command) string {
	var b strings.Builder
	for _, c := range commands {
		b.WriteString("alias " + c.Name + "=" + c.Path + "\n")
	}
	return b.String()
}
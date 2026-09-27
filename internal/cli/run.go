// Copyright (c) 2026 Canonical Inc.
// Originally by Gustavo Niemeyer.
// Use of source code is governed by the MIT-style license in the LICENSE file.

package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/signal"

	"github.com/niemeyer/now/internal/api/completions"
	"github.com/niemeyer/now/internal/busybox"
	"github.com/niemeyer/now/internal/engine"
	"github.com/niemeyer/now/internal/prompt"
	"github.com/niemeyer/now/internal/sandbox"
	"github.com/niemeyer/now/internal/setup"
)

// RunOptions carries the inputs for the full cycle.
type RunOptions struct {
	// Argv is the argument list, without the program name.
	Argv []string
	// Stdin provides the request or arguments when "-" is used.
	Stdin io.Reader
	// Stdout receives the script's own output; Stderr receives the
	// script for review, prompts, and errors.
	Stdout io.Writer
	Stderr io.Writer
	// SetupPath selects the configuration file; empty means $HOME/.now.
	SetupPath string
	// TTY overrides where approval is read from; the controlling
	// terminal by default. A test seam, like ApprovalOptions.TTY.
	TTY io.Reader
}

// sampleScript writes a sample configuration to the file given as "$1",
// then prints where it landed so the user knows what to adjust. Every
// key is a commented placeholder, so an unedited sample fails loading
// with the honest missing-key error.
const sampleScript = `
# CONFIGURATION IS MISSING: This script creates a sample at $HOME/.now

cat > "$1" <<'END'
#api-url=http://128.0.0.1:11434
#api-key=
#api-model=local
#api-type=completions-v1
END
echo "Sample configuration saved, adjust as necessary: $1"
`

// Run performs the full cycle: parse the arguments, load the setup,
// generate the script, ask for approval, and run it. CTRL-C cancels
// whichever phase is in progress.
func Run(ctx context.Context, opts RunOptions) error {
	ctx, stop := signal.NotifyContext(ctx, os.Interrupt)
	defer stop()

	parsed, err := Parse(opts.Argv, opts.Stdin)
	if err != nil {
		var help *HelpRequested
		if errors.As(err, &help) {
			fmt.Fprintln(opts.Stderr, err)
			return nil
		}
		return err
	}

	var setupOpts *setup.Options
	if opts.SetupPath == "" {
		setupOpts, err = setup.Load()
	} else {
		setupOpts, err = setup.LoadFrom(opts.SetupPath)
	}
	proposeSample := false
	if errors.Is(err, fs.ErrNotExist) && !parsed.Yes && !parsed.Quiet {
		// The configuration is missing and the resulting script will be
		// reviewed. Propose the creation of a sample script and inform
		// the user of the proper path.
		proposeSample = true
		err = nil
	} else if err != nil {
		return err
	}

	// Resolve busybox before anything else: the prompt needs its
	// applets, the sandbox probe needs its path, and a missing busybox
	// must fail before the model is called.
	busyOpts, err := busybox.Probe(busybox.Options{})
	if err != nil {
		return err
	}

	// Probe the confinement before generating the script: failing to
	// confine must not waste a model call and a human review.
	grants := sandbox.Options{
		Readable: parsed.Readable,
		Writable: parsed.Writable,
		Network:  parsed.Network,
	}
	sandboxOn := parsed.Sandbox || len(parsed.Readable) > 0 || len(parsed.Writable) > 0
	if sandboxOn {
		// The allowed commands are granted readable so their binaries
		// are bound inside the sandbox.
		for _, c := range parsed.Commands {
			grants.Readable = append(grants.Readable, c.Path)
		}
		cwd, err := os.Getwd()
		if err != nil {
			return fmt.Errorf("cannot confine: %v", err)
		}
		grants.Cwd = cwd
		bwrap, err := sandbox.Probe(grants, busyOpts.Path)
		if err != nil {
			return err
		}
		grants.Bwrap = bwrap
	}

	// From here on the cycle is shared: only the script source differs.
	var script string
	var args []string
	if proposeSample {
		configPath, err := setup.DefaultPath()
		if err != nil {
			return err
		}
		// It's a known script, and will be prompted.
		sandboxOn = false
		script = sampleScript
		args = []string{configPath}
	} else {
		script, err = engine.Generate(ctx, engine.GenerateOptions{
			Request:  parsed.Request,
			Args:     parsed.Args,
			Commands: parsed.Commands,
			Busybox:  busyOpts,
			Complete: func(ctx context.Context, messages []prompt.Message) (string, error) {
				return completions.Complete(ctx, *setupOpts, messages)
			},
		})
		if err != nil {
			return err
		}
		args = parsed.Args
	}

	approved, err := Approve(ctx, ApprovalOptions{
		Script: script,
		Yes:    parsed.Yes,
		Quiet:  parsed.Quiet,
		Stderr: opts.Stderr,
		TTY:    opts.TTY,
	})
	if err != nil {
		return err
	}
	if !approved {
		return nil
	}

	// Grants: explicit -r/-w paths. Confinement turns the network off
	// unless -n opts in. The grants were assembled and probed above,
	// before generating the script.

	err = engine.Run(ctx, script, engine.RunOptions{
		Args:      args,
		Stdout:    opts.Stdout,
		Stderr:    opts.Stderr,
		Trace:     parsed.Trace,
		Commands:  parsed.Commands,
		Busybox:   busyOpts,
		SandboxOn: sandboxOn,
		Sandbox:   grants,
	})
	return err
}
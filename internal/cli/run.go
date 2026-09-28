// Copyright 2026 Canonical Inc.
// Originally by Gustavo Niemeyer.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package cli

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/signal"

	"github.com/canonical/now/internal/api/completions"
	"github.com/canonical/now/internal/busybox"
	"github.com/canonical/now/internal/engine"
	"github.com/canonical/now/internal/prompt"
	"github.com/canonical/now/internal/sandbox"
	"github.com/canonical/now/internal/setup"
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
#api-url=http://127.0.0.1:11434
#api-key=
#api-model=local
#api-type=completions-v1
END
echo "Sample configuration saved, adjust as necessary: $1"
`

// ErrAborted is returned when the user rejects the script at approval.
// It is a known outcome, not a failure: the caller prints "aborted"
// without the error prefix.
var ErrAborted = errors.New("aborted")

// Run performs the full cycle: parse the arguments, load the setup,
// generate the script, ask for approval, and run it. CTRL-C cancels
// whichever phase is in progress.
func Run(ctx context.Context, opts RunOptions) error {
	ctx, stop := signal.NotifyContext(ctx, os.Interrupt)
	defer stop()

	// Parse drains stdin to EOF when "-" is used, leaving opts.Stdin
	// at EOF for the script. The data is in "$@", not on stdin. When
	// "-" is absent, Parse never touches stdin and the script gets the
	// live stream. Passing opts.Stdin to both handles the either-or.
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
	sandboxOn := parsed.Sandbox || parsed.Network || len(parsed.Readable) > 0 || len(parsed.Writable) > 0
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

	// Buffered mode captures the script's output and shows it only when
	// the script fails, so a successful run stays quiet. The approval
	// output joins the buffer only when auto-approving (-by: the review
	// is informational); with -b alone the user must see the script to
	// approve it, so the review goes to the real stderr and only the
	// execution output is buffered. With -q nothing is produced.
	stdout, stderr := opts.Stdout, opts.Stderr
	var buffer bytes.Buffer
	if parsed.Buffered {
		stdout, stderr = &buffer, &buffer
	}
	approveOut := opts.Stderr
	if parsed.Buffered && (parsed.Yes || parsed.Quiet) {
		approveOut = &buffer
	}

	approved, err := Approve(ctx, ApprovalOptions{
		Script: script,
		Yes:    parsed.Yes,
		Quiet:  parsed.Quiet,
		Stderr: approveOut,
		TTY:    opts.TTY,
	})
	if err != nil {
		return err
	}
	if !approved {
		return ErrAborted
	}

	// Grants: explicit -r/-w paths. Confinement turns the network off
	// unless -n opts in. The grants were assembled and probed above,
	// before generating the script.

	err = engine.Run(ctx, script, engine.RunOptions{
		Args:      args,
		Stdin:     opts.Stdin,
		Stdout:    stdout,
		Stderr:    stderr,
		Trace:     parsed.Trace,
		Commands:  parsed.Commands,
		Busybox:   busyOpts,
		SandboxOn: sandboxOn,
		Sandbox:   grants,
	})
	if err != nil && parsed.Buffered {
		buffer.WriteTo(opts.Stderr)
	}
	return err
}
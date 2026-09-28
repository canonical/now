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

package engine

import (
	"context"
	"fmt"
	"io"
	"os"
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
	// Stdin is the stream the script observes on fd 0. nil gives
	// the script an empty stream (EOF on read).
	Stdin io.Reader
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

	// The script travels on stdin (sh -s) — no temp files, no
	// artifacts — wrapped in a group whose redirection attaches
	// fd 9, the user's stdin, as the script's fd 0. fd 9 is the
	// highest single-digit descriptor, so the redirection works in
	// any POSIX shell while leaving fds 3-8 free for the script's
	// own use. The brackets add safety: ash parses the whole
	// compound statement before executing any of it, so once
	// fd 0 is swapped the shell never falls back to reading user
	// data as script source. A bare "exec 0<&9" line would invite
	// exactly that, and actually breaks tests. Aliases are expanded
	// at parse time, so the prelude must run before the group is
	// parsed: put it first, then the group.
	aliases := aliasPrelude(opts.Commands)
	script = aliases + "{\n" + script + "\n} <&9 9<&-\n";

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

	// opts.Stdin must be an *os.File to ride as fd 9 (ExtraFiles
	// carries only *os.File).
	stdin, cleanup, err := stdinFile(opts.Stdin)
	if err != nil {
		return err
	}
	defer cleanup()

	// ExtraFiles numbers entries from fd 3, so six nil placeholders
	// leave fds 3-8 unopened for the script and land the user's
	// stream on fd 9.
	extras := []*os.File{nil, nil, nil, nil, nil, nil, stdin}

	var cmdPath string
	var cmdArgs []string
	if opts.SandboxOn {
		// Probing already happened in the caller, before generating
		// the script; the grants arrive ready.
		bargs, err := sandbox.Args(opts.Sandbox, busybox, shellArgs...)
		if err != nil {
			return err
		}
		cmdPath = bargs[0]
		cmdArgs = bargs[1:]
	} else {
		cmdPath = busybox
		cmdArgs = shellArgs
	}

	cmd := exec.CommandContext(ctx, cmdPath, cmdArgs...)
	cmd.Stdin = strings.NewReader(script)
	cmd.Stdout = opts.Stdout
	cmd.Stderr = opts.Stderr
	cmd.ExtraFiles = extras
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("script failed: %w", err)
	}
	return nil
}

// stdinFile turns r into the *os.File that carries the user's stdin
// as fd 9. A nil becomes an empty stream, so a script read hits
// honest EOF. A non-file reader is copied into a pipe by a goroutine
// that ends when cleanup closes the write side.
func stdinFile(r io.Reader) (f *os.File, cleanup func(), err error) {
	if f, ok := r.(*os.File); ok {
		return f, func() {}, nil
	}
	pr, pw, err := os.Pipe()
	if err != nil {
		return nil, nil, fmt.Errorf("cannot bridge stdin: %w", err)
	}
	done := make(chan struct{})
	go func() {
		defer close(done)
		if r != nil {
			_, _ = io.Copy(pw, r)
		}
		_ = pw.Close()
	}()
	cleanup = func() {
		_ = pw.Close()
		<-done
		_ = pr.Close()
	}
	return pr, cleanup, nil
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
package engine

import (
	"context"
	"fmt"
	"io"
	"os/exec"
	"strings"

	"github.com/niemeyer/now/internal/prompt"
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
	// Commands are the external commands allowed in the script, from -w.
	// Those shadowing a busybox builtin are aliased to their absolute
	// path so the script runs the real command.
	Commands []prompt.Command
}

// busyboxPath returns the busybox path for running scripts via ash. The
// prompt promises the model a busybox environment, so there is no fallback:
// if busybox is missing, running would betray that promise.
func busyboxPath() (string, error) {
	path, err := exec.LookPath("busybox")
	if err != nil {
		return "", fmt.Errorf("cannot find busybox in $PATH")
	}
	return path, nil
}

// Run executes the script with the arguments in "$@".
func Run(ctx context.Context, script string, opts RunOptions) error {
	busybox, err := busyboxPath()
	if err != nil {
		return err
	}

	var stdin strings.Builder
	stdin.WriteString(aliasPrelude(opts.Commands))
	stdin.WriteString(script)

	shellArgs := []string{"sh"}
	if opts.Trace {
		shellArgs = append(shellArgs, "-x")
	}
	shellArgs = append(shellArgs, "-s")

	cmd := exec.CommandContext(ctx, busybox, shellArgs...)
	cmd.Args = append(cmd.Args, opts.Args...)
	cmd.Stdin = strings.NewReader(stdin.String())
	cmd.Stdout = opts.Stdout
	cmd.Stderr = opts.Stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("cannot run script: %w", err)
	}
	return nil
}

// aliasPrelude returns shell lines aliasing the given commands to their
// absolute paths, so they win over the busybox builtins compiled with the
// standalone-shell preference. Commands that do not shadow a builtin
// resolve through $PATH as usual and need no alias.
func aliasPrelude(commands []prompt.Command) string {
	var b strings.Builder
	for _, c := range commands {
		if prompt.IsBusyboxBuiltin(c.Name) {
			b.WriteString("alias " + c.Name + "=" + c.Path + "\n")
		}
	}
	return b.String()
}
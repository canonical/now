package engine

import (
	"context"
	"fmt"
	"io"
	"os/exec"
	"strings"
)

// RunOptions carries the inputs for script execution.
type RunOptions struct {
	// Args are delivered to the script in "$@".
	Args []string
	// Stdout and Stderr receive the script output.
	Stdout io.Writer
	Stderr io.Writer
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

	cmd := exec.CommandContext(ctx, busybox, "sh", "-s")
	cmd.Args = append(cmd.Args, opts.Args...)
	cmd.Stdin = strings.NewReader(script)
	cmd.Stdout = opts.Stdout
	cmd.Stderr = opts.Stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("cannot run script: %w", err)
	}
	return nil
}
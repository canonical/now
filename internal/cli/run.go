package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/signal"

	"github.com/niemeyer/now/internal/api/completions"
	"github.com/niemeyer/now/internal/engine"
	"github.com/niemeyer/now/internal/prompt"
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
}

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
	if err != nil {
		return err
	}

	script, err := engine.Generate(ctx, engine.GenerateOptions{
		Request: parsed.Request,
		Args:    parsed.Args,
		With:    parsed.With,
		Complete: func(ctx context.Context, messages []prompt.Message) (string, error) {
			return completions.Complete(ctx, *setupOpts, messages)
		},
	})
	if err != nil {
		return err
	}

	approved, err := Approve(ctx, ApprovalOptions{
		Script: script,
		Yes:    parsed.Yes,
		Quiet:  parsed.Quiet,
		Stderr: opts.Stderr,
	})
	if err != nil {
		return err
	}
	if !approved {
		return nil
	}

	return engine.Run(ctx, script, engine.RunOptions{
		Args:   parsed.Args,
		Stdout: opts.Stdout,
		Stderr: opts.Stderr,
	})
}
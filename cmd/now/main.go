// Command now generates a one-shot script from a request and runs it
// after user approval, confined to the provided paths.
package main

import (
	"errors"
	"fmt"
	"os"

	"github.com/niemeyer/now/internal/cli"
)

func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, "now:", err)
		os.Exit(1)
	}
}

func run(argv []string) error {
	opts, err := cli.Parse(argv, os.Stdin)
	if err != nil {
		var help *cli.HelpRequested
		if errors.As(err, &help) {
			fmt.Fprintln(os.Stderr, err)
			return nil
		}
		return err
	}
	_ = opts
	// Phases 2-9 are implemented in later steps.
	return errors.New("not implemented yet")
}


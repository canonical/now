// Package busybox resolves the busybox that runs the generated scripts.
package busybox

import (
	"fmt"
	"os/exec"
	"strings"
)

// Options carries the resolved busybox configuration.
type Options struct {
	// Path is the resolved busybox binary.
	Path string
	// Applets are the command names the busybox build provides, from
	// its --list output.
	Applets []string
}

// Probe resolves busybox in $PATH and captures its applet list. The
// prompt promises the model a busybox environment, so a missing busybox
// is an error — better before the model is called than when the
// approved script fails to run.
//
// When Options is already filled, Probe returns it immediately, so
// probing twice is cheap.
func Probe(opts Options) (Options, error) {
	if opts.Path != "" {
		return opts, nil
	}

	path, err := exec.LookPath("busybox")
	if err != nil {
		return Options{}, fmt.Errorf("cannot find busybox in $PATH")
	}

	out, err := exec.Command(path, "--list").Output()
	if err != nil {
		return Options{}, fmt.Errorf("cannot list busybox applets: %w", err)
	}

	applets := strings.Fields(string(out))
	if len(applets) == 0 {
		return Options{}, fmt.Errorf("cannot list busybox applets: empty output")
	}
	return Options{Path: path, Applets: applets}, nil
}

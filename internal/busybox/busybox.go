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

// Package busybox resolves the busybox that runs the generated scripts.
package busybox

import (
	"fmt"
	"os/exec"
	"slices"
	"strings"
)

// Options carries the resolved busybox configuration.
type Options struct {
	// Path is the resolved busybox binary.
	Path string
	// Applets are the command names the busybox build provides, from
	// its --list output.
	Applets []string
	// Standalone reports whether the build has the shell compiled in
	// standalone mode (CONFIG_FEATURE_SH_STANDALONE), observed through
	// static-sh in the --list output. When true, ash resolves applet
	// names from its internal table before $PATH, so scripts need no
	// applet aliases.
	Standalone bool
	// Builtins are the ash shell builtins, from the shell's help
	// output. They are only probed when Standalone is false - the
	// applet aliasing that needs to exclude them is skipped entirely
	// on standalone builds.
	Builtins []string
}

// Probe resolves busybox in $PATH and captures its applet list. The
// prompt promises the model a busybox environment, so a missing busybox
// is an error - better before the model is called than when the
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

	// The [ and [[ applets are dropped from the list: they are shell
	// builtins the model knows well and needs no guidance on, so
	// listing them in the prompt is pure noise. static-sh is dropped
	// too: it is an alias for the shell itself, never a command a
	// script would intend to invoke - but not before it is captured:
	// its presence in --list is the compile-time signal that the shell
	// was built standalone (CONFIG_FEATURE_SH_STANDALONE), meaning ash
	// resolves applet names from its internal table before $PATH and
	// the engine's applet aliases are unneeded. Everything else stays
	// listed - the model is told to use only the allowed commands, so
	// removing an applet it might legitimately emit risks it avoiding
	// valid shell.
	var standalone bool
	var applets []string
	for _, name := range strings.Fields(string(out)) {
		switch name {
		case "[", "[[":
			continue
		case "static-sh":
			standalone = true
			continue
		}
		applets = append(applets, name)
	}
	if len(applets) == 0 {
		return Options{}, fmt.Errorf("cannot list busybox applets: empty output")
	}
	// Both lists are sorted here, once, for the consumers that walk
	// them in order - the engine merge-joins applets against builtins.
	slices.Sort(applets)

	// On non-standalone builds, bare applet names in scripts resolve
	// through $PATH, so the engine aliases the used ones to
	// "busybox <name>". Shell builtins must be excluded from that -
	// aliasing cd, say, would run it in a subprocess - and the list
	// comes from the installed build's own help output, not a
	// hardcoded constant that can drift. A standalone build skips the
	// probe entirely: nothing downstream needs it.
	var builtins []string
	if !standalone {
		out, err := exec.Command(path, "ash", "-c", "help").Output()
		if err != nil {
			return Options{}, fmt.Errorf("cannot list busybox builtins: %w", err)
		}
		// The help output opens with header lines ("Built-in
		// commands:", a rule); the names themselves sit on
		// tab-indented lines, so only those are taken.
		for _, line := range strings.Split(string(out), "\n") {
			if strings.HasPrefix(line, "\t") {
				builtins = append(builtins, strings.Fields(line)...)
			}
		}
		if len(builtins) == 0 {
			return Options{}, fmt.Errorf("cannot list busybox builtins: empty output")
		}
		slices.Sort(builtins)
	}

	return Options{Path: path, Applets: applets, Standalone: standalone, Builtins: builtins}, nil
}

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

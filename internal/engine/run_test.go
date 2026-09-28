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

package engine_test

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/canonical/now/internal/busybox"
	"github.com/canonical/now/internal/engine"
	"github.com/canonical/now/internal/prompt"
)

// mustBusybox probes busybox for the test.
func mustBusybox(t *testing.T) busybox.Options {
	t.Helper()
	opts, err := busybox.Probe(busybox.Options{})
	if err != nil {
		t.Fatalf("cannot probe busybox: %v", err)
	}
	return opts
}

func TestRunOutputsAndArgs(t *testing.T) {
	var out, errOut bytes.Buffer
	err := engine.Run(context.Background(), `for a in "$@"; do echo "arg: $a"; done`, engine.RunOptions{
		Args:    []string{"one", "two words"},
		Busybox: mustBusybox(t),
		Stdout:  &out,
		Stderr:  &errOut,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	assertEqual(t, "stdout", out.String(), "arg: one\narg: two words\n")
}

func TestRunLeadingDashArg(t *testing.T) {
	// An argument starting with "-" must reach "$@" verbatim, not be
	// parsed as a shell option after -s.
	var out, errOut bytes.Buffer
	err := engine.Run(context.Background(), `for a in "$@"; do echo "arg: $a"; done`, engine.RunOptions{
		Args:    []string{"-r", "foo"},
		Busybox: mustBusybox(t),
		Stdout:  &out,
		Stderr:  &errOut,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	assertEqual(t, "stdout", out.String(), "arg: -r\narg: foo\n")
}

func TestRunStopsAtFirstFailure(t *testing.T) {
	// -e aborts on the first failing command: the second command must
	// not run, and the script reports the failure.
	var out bytes.Buffer
	err := engine.Run(context.Background(), "false\necho after >&2", engine.RunOptions{
		Busybox: mustBusybox(t),
		Stdout: &out,
		Stderr: &out,
	})
	if err == nil || !strings.Contains(err.Error(), "script failed") {
		t.Fatalf("expected run error, got %v", err)
	}
	if out.String() != "" {
		t.Errorf("commands after the failure ran: %q", out.String())
	}
}

func TestRunScriptFailure(t *testing.T) {
	var out bytes.Buffer
	err := engine.Run(context.Background(), "echo before\necho oops >&2\nexit 3", engine.RunOptions{
		Busybox: mustBusybox(t),
		Stdout: &out,
		Stderr: &out,
	})
	if err == nil || !strings.Contains(err.Error(), "script failed") {
		t.Fatalf("expected run error, got %v", err)
	}
	if !strings.Contains(out.String(), "oops") {
		t.Errorf("script output missing: %q", out.String())
	}
}

func TestRunStderr(t *testing.T) {
	var out, errOut bytes.Buffer
	err := engine.Run(context.Background(), "echo err >&2", engine.RunOptions{Busybox: mustBusybox(t), Stdout: &out, Stderr: &errOut})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	assertEqual(t, "stdout", out.String(), "")
	assertEqual(t, "stderr", errOut.String(), "err\n")
}

func TestRunTrace(t *testing.T) {
	// -x prints each command to stderr as it executes.
	var out, errOut bytes.Buffer
	err := engine.Run(context.Background(), "echo hi", engine.RunOptions{
		Busybox: mustBusybox(t),
		Stdout: &out,
		Stderr: &errOut,
		Trace:  true,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	assertEqual(t, "stdout", out.String(), "hi\n")
	if !strings.Contains(errOut.String(), "+ echo hi") {
		t.Errorf("missing trace output: %q", errOut.String())
	}
}

func TestRunAliasesAppletShadowingCommand(t *testing.T) {
	// A -w command that shadows a busybox applet (echo) is aliased to
	// its absolute path, so the script runs the real command.
	dir := t.TempDir()
	path := filepath.Join(dir, "echo")
	script := "#!/bin/sh\necho REAL-ECHO \"$@\"\n"
	if err := os.WriteFile(path, []byte(script), 0o755); err != nil {
		t.Fatalf("cannot write command: %v", err)
	}

	var out bytes.Buffer
	err := engine.Run(context.Background(), "echo hello", engine.RunOptions{
		Busybox: mustBusybox(t),
		Stdout:   &out,
		Stderr:   &out,
		Commands: []prompt.Command{
			{Name: "echo", Path: path, Help: ""},
		},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	assertEqual(t, "stdout", out.String(), "REAL-ECHO hello\n")
}

func TestRunAppletWinsWithoutAlias(t *testing.T) {
	// The opposite of the alias test: without the alias, busybox runs
	// its own applet even when $PATH has another command by the same
	// name. This is the behavior the alias prelude exists to override.
	// The fake echo touches a marker file; if the marker appears, the
	// PATH command wrongly won.
	dir := t.TempDir()
	path := filepath.Join(dir, "echo")
	marker := filepath.Join(dir, "fake-echo-ran")
	script := "#!/bin/sh\ntouch " + marker + "\n"
	if err := os.WriteFile(path, []byte(script), 0o755); err != nil {
		t.Fatalf("cannot write command: %v", err)
	}

	var out bytes.Buffer
	err := engine.Run(context.Background(), "echo hello", engine.RunOptions{
		Busybox: mustBusybox(t),
		Stdout:  &out,
		Stderr:  &out,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if _, err := os.Stat(marker); err == nil {
		t.Errorf("PATH command won over the busybox applet")
	}
}

func TestRunAliasesAllCommands(t *testing.T) {
	// Every -c command is aliased to its absolute path — not just the
	// applet-shadowing ones — so the script runs exactly the binary
	// resolved at parse time, and works inside the sandbox where $PATH
	// directories are not bound.
	dir := t.TempDir()
	path := filepath.Join(dir, "jq")
	script := "#!/bin/sh\necho REAL-JQ \"$@\"\n"
	if err := os.WriteFile(path, []byte(script), 0o755); err != nil {
		t.Fatalf("cannot write command: %v", err)
	}

	var out bytes.Buffer
	err := engine.Run(context.Background(), "jq filter", engine.RunOptions{
		Busybox: mustBusybox(t),
		Stdout:  &out,
		Stderr: &out,
		Commands: []prompt.Command{
			{Name: "jq", Path: path, Help: ""},
		},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	assertEqual(t, "stdout", out.String(), "REAL-JQ filter\n")
}

func TestRunCanceled(t *testing.T) {
	// A canceled context kills the running script.
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	var out bytes.Buffer
	err := engine.Run(ctx, "sleep 5", engine.RunOptions{Busybox: mustBusybox(t), Stdout: &out, Stderr: &out})
	if err == nil || !strings.Contains(err.Error(), "script failed") {
		t.Fatalf("expected run error, got %v", err)
	}
}

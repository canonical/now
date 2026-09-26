package engine_test

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/niemeyer/now/internal/engine"
	"github.com/niemeyer/now/internal/prompt"
)

func TestRunOutputsAndArgs(t *testing.T) {
	var out, errOut bytes.Buffer
	err := engine.Run(context.Background(), `for a in "$@"; do echo "arg: $a"; done`, engine.RunOptions{
		Args:   []string{"one", "two words"},
		Stdout: &out,
		Stderr: &errOut,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	assertEqual(t, "stdout", out.String(), "arg: one\narg: two words\n")
}

func TestRunScriptFailure(t *testing.T) {
	var out bytes.Buffer
	err := engine.Run(context.Background(), "echo before\necho oops >&2\nexit 3", engine.RunOptions{
		Stdout: &out,
		Stderr: &out,
	})
	if err == nil || !strings.Contains(err.Error(), "cannot run script") {
		t.Fatalf("expected run error, got %v", err)
	}
	if !strings.Contains(out.String(), "oops") {
		t.Errorf("script output missing: %q", out.String())
	}
}

func TestRunStderr(t *testing.T) {
	var out, errOut bytes.Buffer
	err := engine.Run(context.Background(), "echo err >&2", engine.RunOptions{Stdout: &out, Stderr: &errOut})
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

func TestRunAliasesBuiltinShadowingCommand(t *testing.T) {
	// A -w command that shadows a busybox builtin (echo) is aliased to
	// its absolute path, so the script runs the real command.
	dir := t.TempDir()
	path := filepath.Join(dir, "echo")
	script := "#!/bin/sh\necho REAL-ECHO \"$@\"\n"
	if err := os.WriteFile(path, []byte(script), 0o755); err != nil {
		t.Fatalf("cannot write command: %v", err)
	}

	var out bytes.Buffer
	err := engine.Run(context.Background(), "echo hello", engine.RunOptions{
		Stdout: &out,
		Stderr: &out,
		Commands: []prompt.Command{
			{Name: "echo", Path: path, Help: ""},
		},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	assertEqual(t, "stdout", out.String(), "REAL-ECHO hello\n")
}

func TestRunBuiltinWinsWithoutAlias(t *testing.T) {
	// The opposite of the alias test: without the alias, busybox runs
	// its own builtin even when $PATH has another command by the same
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
		Stdout: &out,
		Stderr: &out,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if _, err := os.Stat(marker); err == nil {
		t.Errorf("PATH command won over the busybox builtin")
	}
}

func TestRunNoAliasWithoutShadow(t *testing.T) {
	// A -w command that does not shadow a builtin gets no alias: the
	// script text passes through unchanged.
	var out bytes.Buffer
	err := engine.Run(context.Background(), "echo marker", engine.RunOptions{
		Stdout: &out,
		Stderr: &out,
		Commands: []prompt.Command{
			{Name: "jq", Path: "/usr/bin/jq", Help: ""},
		},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	assertEqual(t, "stdout", out.String(), "marker\n")
}

func TestRunCanceled(t *testing.T) {
	// A canceled context kills the running script.
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	var out bytes.Buffer
	err := engine.Run(ctx, "sleep 5", engine.RunOptions{Stdout: &out, Stderr: &out})
	if err == nil || !strings.Contains(err.Error(), "cannot run script") {
		t.Fatalf("expected run error, got %v", err)
	}
}

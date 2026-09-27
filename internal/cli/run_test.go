// Copyright (c) 2026 Canonical Inc.
// Originally by Gustavo Niemeyer.
// Use of source code is governed by the MIT-style license in the LICENSE file.

package cli_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/niemeyer/now/internal/api/completions"
	"github.com/niemeyer/now/internal/cli"
)

// e2e starts a fake API replying with the given script, writes a setup
// file pointing at it, and runs the full cycle.
func e2e(t *testing.T, argv []string, reply string) (string, string, error) {
	t.Helper()
	f := completions.NewFakeLLM(reply)
	url, err := f.Start()
	if err != nil {
		t.Fatalf("cannot start fake: %v", err)
	}
	t.Cleanup(func() { _ = f.Stop() })

	setupPath := filepath.Join(t.TempDir(), ".now")
	content := "api-url=" + url + "\napi-model=test\n"
	if err := os.WriteFile(setupPath, []byte(content), 0o600); err != nil {
		t.Fatalf("cannot write setup: %v", err)
	}

	var stdout, stderr strings.Builder
	err = cli.Run(context.Background(), cli.RunOptions{
		Argv:      argv,
		Stdin:     strings.NewReader(""),
		Stdout:    &stdout,
		Stderr:    &stderr,
		SetupPath: setupPath,
	})
	return stdout.String(), stderr.String(), err
}

func TestRunYes(t *testing.T) {
	// -y runs the script; stdout gets only the script's output.
	stdout, stderr, err := e2e(t, []string{"-y", "do something", "one"}, "-$-SCRIPT-START-$-\necho \"arg: $1\"\n-$-SCRIPT-END-$-\n")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	assertEqual(t, "stdout", stdout, "arg: one\n")
	if !strings.Contains(stderr, "echo \"arg: $1\"") {
		t.Errorf("script not shown on stderr: %q", stderr)
	}
}

func TestRunQuiet(t *testing.T) {
	// -q runs the script without showing it.
	stdout, stderr, err := e2e(t, []string{"-q", "do something"}, "-$-SCRIPT-START-$-\necho hello\n-$-SCRIPT-END-$-\n")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	assertEqual(t, "stdout", stdout, "hello\n")
	if strings.Contains(stderr, "echo hello") {
		t.Errorf("script should be hidden: %q", stderr)
	}
}

func TestRunAborted(t *testing.T) {
	// Rejecting the generated script surfaces ErrAborted, which the
	// caller prints as "aborted" without the error prefix. The TTY is
	// injected: without it Approve would read the real /dev/tty.
	f := completions.NewFakeLLM("-$-SCRIPT-START-$-\necho hello\n-$-SCRIPT-END-$-\n")
	url, err := f.Start()
	if err != nil {
		t.Fatalf("cannot start fake: %v", err)
	}
	t.Cleanup(func() { _ = f.Stop() })

	setupPath := filepath.Join(t.TempDir(), ".now")
	content := "api-url=" + url + "\napi-model=test\n"
	if err := os.WriteFile(setupPath, []byte(content), 0o600); err != nil {
		t.Fatalf("cannot write setup: %v", err)
	}

	var stdout, stderr strings.Builder
	err = cli.Run(context.Background(), cli.RunOptions{
		Argv:      []string{"do something"},
		Stdin:     strings.NewReader(""),
		Stdout:    &stdout,
		Stderr:    &stderr,
		SetupPath: setupPath,
		TTY:       strings.NewReader("n\n"),
	})
	if !errors.Is(err, cli.ErrAborted) {
		t.Fatalf("expected ErrAborted, got %v", err)
	}
	assertEqual(t, "stdout", stdout.String(), "")
	if !strings.Contains(stderr.String(), "echo hello") {
		t.Errorf("script not shown on stderr: %q", stderr.String())
	}
}

func TestRunBufferedHidesSuccess(t *testing.T) {
	// -by buffers the script review and the ENTER separator along with
	// the script's output; a successful run produces nothing at all.
	stdout, stderr, err := e2e(t, []string{"-y", "-b", "do something"}, "-$-SCRIPT-START-$-\necho hello\necho err >&2\n-$-SCRIPT-END-$-\n")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	assertEqual(t, "stdout", stdout, "")
	assertEqual(t, "stderr", stderr, "")
}

func TestRunBufferedAloneShowsReview(t *testing.T) {
	// -b alone: the script review and the ENTER separator go to the
	// real stderr — the user cannot approve a script they cannot see —
	// and only the execution output is buffered.
	f := completions.NewFakeLLM("-$-SCRIPT-START-$-\necho hello\n-$-SCRIPT-END-$-\n")
	url, err := f.Start()
	if err != nil {
		t.Fatalf("cannot start fake: %v", err)
	}
	t.Cleanup(func() { _ = f.Stop() })

	setupPath := filepath.Join(t.TempDir(), ".now")
	if err := os.WriteFile(setupPath, []byte("api-url="+url+"\n"), 0o600); err != nil {
		t.Fatalf("cannot write setup: %v", err)
	}

	var stdout, stderr strings.Builder
	err = cli.Run(context.Background(), cli.RunOptions{
		Argv:      []string{"-b", "do something"},
		Stdin:     strings.NewReader(""),
		Stdout:    &stdout,
		Stderr:    &stderr,
		SetupPath: setupPath,
		TTY:       strings.NewReader("\n"),
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	assertEqual(t, "stdout", stdout.String(), "")
	assertEqual(t, "stderr", stderr.String(), "echo hello\n[ ENTER | CTRL-C ]\n")
}

func TestRunBufferedShowsFailure(t *testing.T) {
	// -b shows the captured output when the script fails.
	stdout, stderr, err := e2e(t, []string{"-y", "-b", "do something"}, "-$-SCRIPT-START-$-\necho hello\nexit 3\n-$-SCRIPT-END-$-\n")
	if err == nil || !strings.Contains(err.Error(), "script failed") {
		t.Fatalf("expected run error, got %v", err)
	}
	assertEqual(t, "stdout", stdout, "")
	if !strings.Contains(stderr, "hello") {
		t.Errorf("buffered output missing on failure: %q", stderr)
	}
}

func TestRunNoTerminalRejects(t *testing.T) {
	// Without -y/-q and without a controlling terminal, approval fails
	// as an error and the script does not run. Only meaningful in
	// environments without a tty.
	if _, err := os.Open("/dev/tty"); err == nil {
		t.Skip("environment has a controlling terminal")
	}
	stdout, stderr, err := e2e(t, []string{"do something"}, "-$-SCRIPT-START-$-\necho hello\n-$-SCRIPT-END-$-\n")
	if err == nil || !strings.Contains(err.Error(), "cannot ask for approval") {
		t.Fatalf("expected approval error, got %v", err)
	}
	assertEqual(t, "stdout", stdout, "")
	if !strings.Contains(stderr, "echo hello") {
		t.Errorf("script not shown on stderr: %q", stderr)
	}
}

func TestRunTrace(t *testing.T) {
	// -x threads through to the executed script: commands are traced
	// to stderr, stdout still gets only the script's output.
	stdout, stderr, err := e2e(t, []string{"-y", "-t", "do something"}, "-$-SCRIPT-START-$-\necho hello\n-$-SCRIPT-END-$-\n")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	assertEqual(t, "stdout", stdout, "hello\n")
	if !strings.Contains(stderr, "+ echo hello") {
		t.Errorf("missing trace in stderr: %q", stderr)
	}
}

func TestRunSampleConfigOffer(t *testing.T) {
	// A missing configuration with a human reviewing scripts offers the
	// sample script through the normal approval cycle; on approval the
	// file is written and the script prints the pointer to it. No model
	// call happens.
	home := t.TempDir()
	t.Setenv("HOME", home)

	var stdout, stderr strings.Builder
	err := cli.Run(context.Background(), cli.RunOptions{
		Argv:   []string{"do something"},
		Stdin:  strings.NewReader(""),
		Stdout: &stdout,
		Stderr: &stderr,
		TTY:    strings.NewReader("\n"),
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	data, err := os.ReadFile(filepath.Join(home, ".now"))
	if err != nil {
		t.Fatalf("cannot read sample: %v", err)
	}
	if !strings.Contains(string(data), "#api-url=") {
		t.Errorf("unexpected sample content: %q", data)
	}
	if !strings.Contains(stderr.String(), "adjust as necessary") {
		t.Errorf("sample script not shown for review: %q", stderr.String())
	}
	assertEqual(t, "stdout", stdout.String(), "Sample configuration saved, adjust as necessary: "+filepath.Join(home, ".now")+"\n")
}

func TestRunSampleConfigCancel(t *testing.T) {
	// Declining the sample script leaves nothing behind.
	home := t.TempDir()
	t.Setenv("HOME", home)

	var stdout, stderr strings.Builder
	err := cli.Run(context.Background(), cli.RunOptions{
		Argv:   []string{"do something"},
		Stdin:  strings.NewReader(""),
		Stdout: &stdout,
		Stderr: &stderr,
		TTY:    strings.NewReader("n\n"),
	})
	if !errors.Is(err, cli.ErrAborted) {
		t.Fatalf("expected ErrAborted, got %v", err)
	}
	if _, err := os.Stat(filepath.Join(home, ".now")); !os.IsNotExist(err) {
		t.Errorf("sample written despite cancel")
	}
}

func TestRunSampleConfigNotOfferedUnattended(t *testing.T) {
	// -y (and -q) get the plain missing-config error, not the offer:
	// the side channel only exists with a human reviewing scripts.
	home := t.TempDir()
	t.Setenv("HOME", home)

	var stdout, stderr strings.Builder
	err := cli.Run(context.Background(), cli.RunOptions{
		Argv:   []string{"-y", "do something"},
		Stdin:  strings.NewReader(""),
		Stdout: &stdout,
		Stderr: &stderr,
		TTY:    strings.NewReader(""),
	})
	if err == nil || !strings.Contains(err.Error(), "cannot open") {
		t.Fatalf("expected missing config error, got %v", err)
	}
	if strings.Contains(stderr.String(), "adjust as necessary") {
		t.Errorf("sample offered despite -y: %q", stderr.String())
	}
	if _, err2 := os.Stat(filepath.Join(home, ".now")); !os.IsNotExist(err2) {
		t.Errorf("sample written despite -y")
	}
}

func TestRunSandboxFailsBeforeAPICall(t *testing.T) {
	// Confinement problems must surface before the model is called: no
	// request is sent, no script generated, nothing to review. A broken
	// bwrap on $PATH stands in for any confinement failure.
	f := completions.NewFakeLLM("-$-SCRIPT-START-$-\necho should-not-run\n-$-SCRIPT-END-$-\n")
	url, err := f.Start()
	if err != nil {
		t.Fatalf("cannot start fake: %v", err)
	}
	t.Cleanup(func() { _ = f.Stop() })

	setupPath := filepath.Join(t.TempDir(), ".now")
	if err := os.WriteFile(setupPath, []byte("api-url="+url+"\n"), 0o600); err != nil {
		t.Fatalf("cannot write setup: %v", err)
	}

	binDir := t.TempDir()
	bwrap := filepath.Join(binDir, "bwrap")
	if err := os.WriteFile(bwrap, []byte("#!/bin/sh\nexit 1\n"), 0o755); err != nil {
		t.Fatalf("cannot write bwrap: %v", err)
	}
	t.Setenv("PATH", binDir+":"+os.Getenv("PATH"))

	var stdout, stderr strings.Builder
	err = cli.Run(context.Background(), cli.RunOptions{
		Argv:      []string{"-s", "do something"},
		Stdin:     strings.NewReader(""),
		Stdout:    &stdout,
		Stderr:    &stderr,
		SetupPath: setupPath,
	})
	if err == nil {
		t.Fatalf("expected confinement error, got success")
	}
	if !strings.Contains(err.Error(), "cannot confine") {
		t.Fatalf("expected confinement error, got %v", err)
	}
	if got := len(f.Requests()); got != 0 {
		t.Fatalf("model was called %d times, want 0", got)
	}
}

func TestRunBusyboxFailsBeforeAPICall(t *testing.T) {
	// A missing busybox must surface before the model is called: no
	// request is sent, no script generated, nothing to review. An
	// empty $PATH stands in for any environment without busybox.
	f := completions.NewFakeLLM("-$-SCRIPT-START-$-\necho should-not-run\n-$-SCRIPT-END-$-\n")
	url, err := f.Start()
	if err != nil {
		t.Fatalf("cannot start fake: %v", err)
	}
	t.Cleanup(func() { _ = f.Stop() })

	setupPath := filepath.Join(t.TempDir(), ".now")
	if err := os.WriteFile(setupPath, []byte("api-url="+url+"\n"), 0o600); err != nil {
		t.Fatalf("cannot write setup: %v", err)
	}

	t.Setenv("PATH", t.TempDir())

	var stdout, stderr strings.Builder
	err = cli.Run(context.Background(), cli.RunOptions{
		Argv:      []string{"-y", "do something"},
		Stdin:     strings.NewReader(""),
		Stdout:    &stdout,
		Stderr:    &stderr,
		SetupPath: setupPath,
	})
	if err == nil || !strings.Contains(err.Error(), "cannot find busybox") {
		t.Fatalf("expected busybox error, got %v", err)
	}
	if got := len(f.Requests()); got != 0 {
		t.Fatalf("model was called %d times, want 0", got)
	}
}

func TestRunNetworkEnforcesSandbox(t *testing.T) {
	// -n turns confinement on like -r and -w: a broken bwrap with -n
	// fails before the model is called, proving the sandbox is on.
	f := completions.NewFakeLLM("-$-SCRIPT-START-$-\necho should-not-run\n-$-SCRIPT-END-$-\n")
	url, err := f.Start()
	if err != nil {
		t.Fatalf("cannot start fake: %v", err)
	}
	t.Cleanup(func() { _ = f.Stop() })

	setupPath := filepath.Join(t.TempDir(), ".now")
	if err := os.WriteFile(setupPath, []byte("api-url="+url+"\n"), 0o600); err != nil {
		t.Fatalf("cannot write setup: %v", err)
	}

	binDir := t.TempDir()
	bwrap := filepath.Join(binDir, "bwrap")
	if err := os.WriteFile(bwrap, []byte("#!/bin/sh\nexit 1\n"), 0o755); err != nil {
		t.Fatalf("cannot write bwrap: %v", err)
	}
	t.Setenv("PATH", binDir+":"+os.Getenv("PATH"))

	var stdout, stderr strings.Builder
	err = cli.Run(context.Background(), cli.RunOptions{
		Argv:      []string{"-y", "-n", "do something"},
		Stdin:     strings.NewReader(""),
		Stdout:    &stdout,
		Stderr:    &stderr,
		SetupPath: setupPath,
	})
	if err == nil || !strings.Contains(err.Error(), "cannot confine") {
		t.Fatalf("expected confinement error, got %v", err)
	}
	if got := len(f.Requests()); got != 0 {
		t.Fatalf("model was called %d times, want 0", got)
	}
}

func TestRunErrorReply(t *testing.T) {
	// The model's ERROR reason flows through the whole cycle unchanged.
	_, _, err := e2e(t, []string{"do something"}, "-$-ERROR-START-$-\ncannot do that\n-$-ERROR-END-$-")
	if err == nil || err.Error() != "cannot do that" {
		t.Fatalf("expected model error, got %v", err)
	}
}

func TestRunStdinArgs(t *testing.T) {
	// "-" injects stdin lines as arguments; the approval still works
	// via -y since stdin is consumed.
	f := completions.NewFakeLLM("-$-SCRIPT-START-$-\nfor a in \"$@\"; do echo \"$a\"; done\n-$-SCRIPT-END-$-\n")
	url, err := f.Start()
	if err != nil {
		t.Fatalf("cannot start fake: %v", err)
	}
	t.Cleanup(func() { _ = f.Stop() })

	setupPath := filepath.Join(t.TempDir(), ".now")
	content := "api-url=" + url + "\napi-model=test\n"
	if err := os.WriteFile(setupPath, []byte(content), 0o600); err != nil {
		t.Fatalf("cannot write setup: %v", err)
	}

	var stdout, stderr strings.Builder
	err = cli.Run(context.Background(), cli.RunOptions{
		Argv:      []string{"-y", "do something", "-"},
		Stdin:     strings.NewReader("one\ntwo\n"),
		Stdout:    &stdout,
		Stderr:    &stderr,
		SetupPath: setupPath,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	assertEqual(t, "stdout", stdout.String(), "one\ntwo\n")
}

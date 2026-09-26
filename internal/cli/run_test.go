package cli_test

import (
	"context"
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
	stdout, stderr, err := e2e(t, []string{"-y", "do something", "one"}, "SCRIPT\necho \"arg: $1\"\n")
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
	stdout, stderr, err := e2e(t, []string{"-q", "do something"}, "SCRIPT\necho hello\n")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	assertEqual(t, "stdout", stdout, "hello\n")
	if strings.Contains(stderr, "echo hello") {
		t.Errorf("script should be hidden: %q", stderr)
	}
}

func TestRunNoTerminalRejects(t *testing.T) {
	// Without -y/-q and without a controlling terminal, approval fails
	// as an error and the script does not run. Only meaningful in
	// environments without a tty.
	if _, err := os.Open("/dev/tty"); err == nil {
		t.Skip("environment has a controlling terminal")
	}
	stdout, stderr, err := e2e(t, []string{"do something"}, "SCRIPT\necho hello\n")
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
	stdout, stderr, err := e2e(t, []string{"-y", "-t", "do something"}, "SCRIPT\necho hello\n")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	assertEqual(t, "stdout", stdout, "hello\n")
	if !strings.Contains(stderr, "+ echo hello") {
		t.Errorf("missing trace in stderr: %q", stderr)
	}
}

func TestRunSandboxFailsBeforeAPICall(t *testing.T) {
	// Confinement problems must surface before the model is called: no
	// request is sent, no script generated, nothing to review. A broken
	// bwrap on $PATH stands in for any confinement failure.
	f := completions.NewFakeLLM("SCRIPT\necho should-not-run\n")
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
	f := completions.NewFakeLLM("SCRIPT\necho should-not-run\n")
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

func TestRunErrorReply(t *testing.T) {
	// The model's ERROR reason flows through the whole cycle unchanged.
	_, _, err := e2e(t, []string{"do something"}, "ERROR cannot do that")
	if err == nil || err.Error() != "cannot perform the request: cannot do that" {
		t.Fatalf("expected model error, got %v", err)
	}
}

func TestRunStdinArgs(t *testing.T) {
	// "-" injects stdin lines as arguments; the approval still works
	// via -y since stdin is consumed.
	f := completions.NewFakeLLM("SCRIPT\nfor a in \"$@\"; do echo \"$a\"; done\n")
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

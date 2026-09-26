package engine_test

import (
	"bytes"
	"strings"
	"testing"

	"github.com/niemeyer/now/internal/engine"
)

func TestRunOutputsAndArgs(t *testing.T) {
	var out, errOut bytes.Buffer
	err := engine.Run(`for a in "$@"; do echo "arg: $a"; done`, engine.RunOptions{
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
	err := engine.Run("echo before\necho oops >&2\nexit 3", engine.RunOptions{
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
	err := engine.Run("echo err >&2", engine.RunOptions{Stdout: &out, Stderr: &errOut})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	assertEqual(t, "stdout", out.String(), "")
	assertEqual(t, "stderr", errOut.String(), "err\n")
}

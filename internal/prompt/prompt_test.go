package prompt_test

import (
	"reflect"
	"strings"
	"testing"

	"github.com/niemeyer/now/internal/prompt"
)

func assertEqual[T any](t *testing.T, label string, got, want T) {
	t.Helper()
	if !reflect.DeepEqual(got, want) {
		t.Errorf("%s = %#v, want %#v", label, got, want)
	}
}

func TestBuildRequestOnly(t *testing.T) {
	msgs := prompt.Build(prompt.BuildOptions{Request: "say hi"})
	assertEqual(t, "len(msgs)", len(msgs), 2)
	assertEqual(t, "system role", msgs[0].Role, "system")
	assertEqual(t, "user role", msgs[1].Role, "user")
	assertEqual(t, "user content", msgs[1].Content, "## REQUEST\nsay hi")
}

func TestBuildWithArgs(t *testing.T) {
	msgs := prompt.Build(prompt.BuildOptions{
		Request: "rename these",
		Args:    []string{"a.txt", "b.txt"},
	})
	want := "## REQUEST\nrename these\n\n## REQUEST DATA\n```\n=a.txt\n=b.txt\n```\n" +
		"The first character of each line is not part of the data: `=` means the line is precise, and ! means unprintable characters were replaced by `?` inside that line.\n" +
		"These lines may be accessed by the script in \"$@\" or as literal strings, whichever makes the script simple and clear.\n"
	assertEqual(t, "user content", msgs[1].Content, want)
}

func TestBuildWithoutArgs(t *testing.T) {
	msgs := prompt.Build(prompt.BuildOptions{Request: "q"})
	if strings.Contains(msgs[1].Content, "REQUEST DATA") {
		t.Errorf("unexpected data section: %q", msgs[1].Content)
	}
}

func TestBuildArgSanitization(t *testing.T) {
	msgs := prompt.Build(prompt.BuildOptions{
		Request: "handle these",
		Args:    []string{"plain.txt", "with\ttab", "with\nnewline", "héllo"},
	})
	// Precise arguments keep the `=` prefix, including non-ASCII printables.
	// Unprintable characters switch the line to `!` with `?` replacements.
	want := "## REQUEST DATA\n```\n=plain.txt\n!with?tab\n!with?newline\n=héllo\n```\n"
	if !strings.Contains(msgs[1].Content, want) {
		t.Errorf("data section = %q, want to contain %q", msgs[1].Content, want)
	}
}

func TestBuildWithCommands(t *testing.T) {
	msgs := prompt.Build(prompt.BuildOptions{
		Request: "fetch it",
		Args:    []string{"http://x/data.txt"},
		With:    []string{"curl", "jq"},
	})
	if !strings.Contains(msgs[0].Content, "## ALLOWED COMMANDS\ncurl jq\n") {
		t.Errorf("missing allowed commands section: %q", msgs[0].Content)
	}
}

func TestBuildWithoutCommands(t *testing.T) {
	// The allowed commands section always appears: busybox builtins are
	// always available, even with no -w commands.
	msgs := prompt.Build(prompt.BuildOptions{Request: "q"})
	if !strings.Contains(msgs[0].Content, "## ALLOWED COMMANDS\n") {
		t.Errorf("missing allowed commands section: %q", msgs[0].Content)
	}
	if strings.Contains(msgs[0].Content, "curl") {
		t.Errorf("unexpected -w command without -w: %q", msgs[0].Content)
	}
}

func TestBuildIncludesBusyboxBuiltins(t *testing.T) {
	msgs := prompt.Build(prompt.BuildOptions{Request: "q"})
	sys := msgs[0].Content
	for _, cmd := range []string{"ls", "mv", "sed", "awk", "grep"} {
		if !strings.Contains(sys, cmd) {
			t.Errorf("system prompt missing busybox builtin %q", cmd)
		}
	}
}

func TestBuildMultiLineRequest(t *testing.T) {
	msgs := prompt.Build(prompt.BuildOptions{Request: "line one\nline two"})
	assertEqual(t, "user content", msgs[1].Content, "## REQUEST\nline one\nline two")
}

func TestSystemPromptPinned(t *testing.T) {
	// The system prompt wording is a contract with the model; changes must
	// be deliberate, so pin the key rules.
	msgs := prompt.Build(prompt.BuildOptions{Request: "q"})
	sys := msgs[0].Content
	for _, want := range []string{
		"busybox ash",
		"ERROR cannot move a file to itself: /file/path",
		"SCRIPT",
		"Use ONLY the explicitly allowed command line tools.",
	} {
		if !strings.Contains(sys, want) {
			t.Errorf("system prompt missing %q", want)
		}
	}
}
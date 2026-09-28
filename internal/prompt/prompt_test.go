// Copyright (c) 2026 Canonical Inc.
// Originally by Gustavo Niemeyer.
// Use of source code is governed by the MIT-style license in the LICENSE file.

package prompt_test

import (
	"reflect"
	"strings"
	"testing"

	"github.com/canonical/now/internal/prompt"
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
	want := "## REQUEST\nrename these\n\n## REQUEST DATA\n```\na.txt\nb.txt\n```\n" +
		"These lines may be accessed by the script in \"$@\" or as literal strings, whichever makes the script simple and clear.\n" +
		"Note that any \uFFFD above replaces a non-printable character, but for the script the real string is available in \"$@\".\n"
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
	// Printable arguments, including non-ASCII, pass through; unprintable
	// characters are replaced by the replacement rune, and the note tells
	// the model the real strings are in "$@".
	want := "## REQUEST DATA\n```\nplain.txt\nwith\uFFFDtab\nwith\uFFFDnewline\nhéllo\n```\n"
	if !strings.Contains(msgs[1].Content, want) {
		t.Errorf("data section = %q, want to contain %q", msgs[1].Content, want)
	}
}

func TestBuildWithCommands(t *testing.T) {
	msgs := prompt.Build(prompt.BuildOptions{
		Request: "fetch it",
		Args:    []string{"http://x/data.txt"},
		Commands: []prompt.Command{
			{Name: "curl", Help: "curl usage:\n  curl [options] URL"},
			{Name: "jq", Help: "jq usage:\n  jq filter"},
		},
	})
	if !strings.Contains(msgs[0].Content, "## ALLOWED COMMANDS\ncurl jq\n") {
		t.Errorf("missing allowed commands section: %q", msgs[0].Content)
	}
	if !strings.Contains(msgs[0].Content, "## REFERENCES\n\n### curl --help\ncurl usage:\n  curl [options] URL") {
		t.Errorf("missing curl help: %q", msgs[0].Content)
	}
	if !strings.Contains(msgs[0].Content, "### jq --help\njq usage:\n  jq filter") {
		t.Errorf("missing jq help: %q", msgs[0].Content)
	}
}

func TestBuildReferencesSection(t *testing.T) {
	// With two commands, there is a single REFERENCES section holding
	// one subsection per command, in order.
	msgs := prompt.Build(prompt.BuildOptions{
		Request: "fetch it",
		Commands: []prompt.Command{
			{Name: "curl", Help: "curl usage:\n  curl [options] URL"},
			{Name: "jq", Help: "jq usage:\n  jq filter"},
		},
	})
	start := strings.Index(msgs[0].Content, "\n\n## REFERENCES\n\n### curl --help")
	if start < 0 {
		t.Fatalf("REFERENCES section not found: %q", msgs[0].Content)
	}
	assertEqual(t, "references", msgs[0].Content[start:], "\n\n## REFERENCES\n\n### curl --help\ncurl usage:\n  curl [options] URL"+
		"\n\n### jq --help\njq usage:\n  jq filter")
}

func TestBuildWithoutCommands(t *testing.T) {
	// The allowed commands section always appears, listing the busybox
	// applets even with no -w commands. No help sections then.
	msgs := prompt.Build(prompt.BuildOptions{Request: "q", Applets: []string{"ls"}})
	if !strings.Contains(msgs[0].Content, "## ALLOWED COMMANDS\n") {
		t.Errorf("missing allowed commands section: %q", msgs[0].Content)
	}
	if strings.Contains(msgs[0].Content, "## REFERENCES") {
		t.Errorf("unexpected help section: %q", msgs[0].Content)
	}
}

func TestBuildCommandWithoutHelp(t *testing.T) {
	// A command with no --help is still allowed, but gets no reference.
	msgs := prompt.Build(prompt.BuildOptions{
		Request: "do it",
		Commands: []prompt.Command{
			{Name: "curl", Help: "curl usage:\n  curl [options] URL"},
			{Name: "nohelputil", Help: ""},
			{Name: "jq", Help: "jq usage:\n  jq filter"},
		},
	})
	if !strings.Contains(msgs[0].Content, "## ALLOWED COMMANDS\ncurl nohelputil jq\n") {
		t.Errorf("missing allowed commands entry: %q", msgs[0].Content)
	}
	if strings.Contains(msgs[0].Content, "nohelputil --help") {
		t.Errorf("unexpected reference for help-less command: %q", msgs[0].Content)
	}
	if !strings.Contains(msgs[0].Content, "### curl --help") ||
		!strings.Contains(msgs[0].Content, "### jq --help") {
		t.Errorf("missing other references: %q", msgs[0].Content)
	}
}

func TestBuildIncludesBusyboxApplets(t *testing.T) {
	msgs := prompt.Build(prompt.BuildOptions{
		Request: "q",
		Applets: []string{"ls", "mv", "sed", "awk", "grep"},
	})
	sys := msgs[0].Content
	for _, cmd := range []string{"ls", "mv", "sed", "awk", "grep"} {
		if !strings.Contains(sys, cmd) {
			t.Errorf("system prompt missing busybox applet %q", cmd)
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
		"-$-SCRIPT-START-$-",
		"-$-SCRIPT-END-$-",
		"-$-ERROR-START-$-",
		"-$-ERROR-END-$-",
		"Use ONLY the explicitly allowed command line tools.",
	} {
		if !strings.Contains(sys, want) {
			t.Errorf("system prompt missing %q", want)
		}
	}
}
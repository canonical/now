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
	assertEqual(t, "user content", msgs[1].Content, "## REQUEST\nsay hi\n")
}

func TestBuildWithArgs(t *testing.T) {
	msg := prompt.Build(prompt.BuildOptions{
		Request: "rename these",
		Args:    []string{"a.txt", "b.txt"},
	})
	want := "## REQUEST\nrename these\n\n## \"$@\"\n```\n$1 | a.txt\n$2 | b.txt\n```\n" +
		"EVERY LINE above the LITERAL string in the respective index in \"$@\". " +
		"Your job is to INFER what the data means BASED ON THE REQUEST and generate the script to SOLVE THE REQUEST.\n" +
		"You may use \"$@\", $1, $2, etc, or the literal strings. Prefer STRING LITERALS rather than variables for SIMPLE cases.\n"
	assertEqual(t, "user content", msg[1].Content, want)
}

func TestBuildScriptOutputDropsArgAt(t *testing.T) {
	// A script to be dumped (Output true, Format "sh") never runs, so
	// "$@" is meaningless: args are plain data, with no $N | indexing
	// and no run-time notes.
	msgs := prompt.Build(prompt.BuildOptions{
		Request: "rename these",
		Args:    []string{"a.txt", "b.txt"},
		Format:  "sh",
		Output:  true,
	})
	want := "## REQUEST\nrename these\n\n## DATA\n```\na.txt\nb.txt\n```\n" +
		"EVERY LINE above is the LITERAL string in the data. " +
		"Your job is to INFER what the data means BASED ON THE REQUEST and generate the script to SOLVE THE REQUEST.\n" +
		"If you need any of this data in the script you must put it there yourself.\n"
	assertEqual(t, "user content", msgs[1].Content, want)
	if strings.Contains(msgs[1].Content, "$@") {
		t.Errorf("dumped script should not mention $@: %q", msgs[1].Content)
	}
	// The system message is still the script prompt: it will execute
	// nowhere, but the SCRIPT reply protocol and command surface apply.
	if !strings.Contains(msgs[0].Content, "---SCRIPT-START---") {
		t.Errorf("dumped script should still use the script prompt: %q", msgs[0].Content)
	}
}

func TestBuildWithoutArgs(t *testing.T) {
	msgs := prompt.Build(prompt.BuildOptions{Request: "q"})
	if strings.Contains(msgs[1].Content, "$@") {
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
	want := "## \"$@\"\n```\n$1 | plain.txt\n$2 | with\uFFFDtab\n$3 | with\uFFFDnewline\n$4 | héllo\n```\n"
	if !strings.Contains(msgs[1].Content, want) {
		t.Errorf("data section = %q, want to contain %q", msgs[1].Content, want)
	}
	if !strings.Contains(msgs[1].Content, "The \uFFFD replaces non-printable characters, so you cannot use these lines as literals.") {
		t.Errorf("missing sanitization note: %q", msgs[1].Content)
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
		"\n### jq --help\njq usage:\n  jq filter")
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
	assertEqual(t, "user content", msgs[1].Content, "## REQUEST\nline one\nline two\n")
}

func TestSystemPromptPinned(t *testing.T) {
	// The system prompt wording is a contract with the model; changes must
	// be deliberate, so pin the key rules.
	msgs := prompt.Build(prompt.BuildOptions{Request: "q"})
	sys := msgs[0].Content
	for _, want := range []string{
		"busybox ash",
		"---SCRIPT-START---",
		"---SCRIPT-END---",
		"---ERROR-START---",
		"---ERROR-END---",
		"Use ONLY the commands in the ALLOWED COMMANDS section.",
	} {
		if !strings.Contains(sys, want) {
			t.Errorf("system prompt missing %q", want)
		}
	}
}

func TestBuildFormatPrompt(t *testing.T) {
	// A non-sh format builds the output-mode prompt: OUTPUT markers,
	// the format named in the system message, and no command surface.
	msgs := prompt.Build(prompt.BuildOptions{Request: "q", Format: "json"})
	assertEqual(t, "len(msgs)", len(msgs), 2)
	for _, want := range []string{
		"---OUTPUT-START---",
		"---OUTPUT-END---",
		"---ERROR-START---",
		"---ERROR-END---",
		"\"json\"",
	} {
		if !strings.Contains(msgs[0].Content, want) {
			t.Errorf("format system prompt missing %q", want)
		}
	}
	if strings.Contains(msgs[0].Content, "## ALLOWED COMMANDS") {
		t.Errorf("format prompt should not list commands: %q", msgs[0].Content)
	}
	if strings.Contains(msgs[0].Content, "---SCRIPT-START---") {
		t.Errorf("format prompt should not mention SCRIPT: %q", msgs[0].Content)
	}
}

func TestBuildFormatShUsesScriptPrompt(t *testing.T) {
	// -f sh reuses the script prompt, so the SCRIPT markers appear.
	msgs := prompt.Build(prompt.BuildOptions{Request: "q", Format: "sh", Applets: []string{"ls"}})
	if !strings.Contains(msgs[0].Content, "---SCRIPT-START---") {
		t.Errorf("sh format should use script prompt: %q", msgs[0].Content)
	}
	if !strings.Contains(msgs[0].Content, "## ALLOWED COMMANDS") {
		t.Errorf("sh format should list commands: %q", msgs[0].Content)
	}
}

func TestBuildFormatUserMessageDropsArgNote(t *testing.T) {
	// The format user message keeps DATA but drops the script-only
	// "$@" note, since nothing runs.
	msgs := prompt.Build(prompt.BuildOptions{Request: "rename these", Args: []string{"a.txt"}, Format: "json"})
	if strings.Contains(msgs[1].Content, "$@") {
		t.Errorf("format user message should not mention $@: %q", msgs[1].Content)
	}
	if !strings.Contains(msgs[1].Content, "## DATA") {
		t.Errorf("format user message should still carry data: %q", msgs[1].Content)
	}
}
// Copyright (c) 2026 Canonical Inc.
// Originally by Gustavo Niemeyer.
// Use of source code is governed by the MIT-style license in the LICENSE file.

package engine_test

import (
	"reflect"
	"strings"
	"testing"

	"context"
	"github.com/niemeyer/now/internal/api/completions"
	"github.com/niemeyer/now/internal/engine"
	"github.com/niemeyer/now/internal/prompt"
	"github.com/niemeyer/now/internal/setup"
)

func assertEqual[T any](t *testing.T, label string, got, want T) {
	t.Helper()
	if !reflect.DeepEqual(got, want) {
		t.Errorf("%s = %#v, want %#v", label, got, want)
	}
}

func generate(t *testing.T, reply string) (string, error) {
	t.Helper()
	f := completions.NewFakeLLM(reply)
	url, err := f.Start()
	if err != nil {
		t.Fatalf("cannot start fake: %v", err)
	}
	t.Cleanup(func() { _ = f.Stop() })
	opts := setup.Options{APIURL: url, APIModel: "test"}
	return engine.Generate(context.Background(), engine.GenerateOptions{
		Request: "do something",
		Args:    []string{"a.txt"},
		Complete: func(ctx context.Context, messages []prompt.Message) (string, error) {
			return completions.Complete(ctx, opts, messages)
		},
	})
}

func TestGenerateScript(t *testing.T) {
	script, err := generate(t, "SCRIPT\necho hello\n")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	assertEqual(t, "script", script, "echo hello")
}

func TestGenerateScriptNoTrailingNewline(t *testing.T) {
	script, err := generate(t, "SCRIPT\necho hello")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	assertEqual(t, "script", script, "echo hello")
}

func TestGenerateFencedScript(t *testing.T) {
	// A fence may wrap the script body after the SCRIPT tag, or the whole
	// reply; both are stripped.
	for _, reply := range []string{
		"SCRIPT\n```\necho hi\n```\n",
		"```\nSCRIPT\necho hi\n```\n",
	} {
		script, err := generate(t, reply)
		if err != nil {
			t.Fatalf("unexpected error for %q: %v", reply, err)
		}
		assertEqual(t, "script", script, "echo hi")
	}
}

func TestGenerateFenceClosedAfterTag(t *testing.T) {
	// A fence opened right before the tag is a script fence: the payload
	// ends at its closing fence, and anything after is dropped.
	script, err := generate(t, "```\nSCRIPT\necho hi\n```\necho more\n")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	assertEqual(t, "script", script, "echo hi")
}

func TestGenerateFenceClosedBeforeTag(t *testing.T) {
	// A fence opened and closed before the SCRIPT tag is simply unfenced;
	// parsing continues as if it had just started.
	script, err := generate(t, "```\nsome chatter\n```\nSCRIPT\necho hi\n")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	assertEqual(t, "script", script, "echo hi")
}

func TestGenerateFenceInsideBodyIsContent(t *testing.T) {
	// Fences inside the body are ordinary script content; only a fence
	// opened right around the SCRIPT tag is a wrapper.
	script, err := generate(t, "SCRIPT\necho hi\n```\necho more\n```\n")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	assertEqual(t, "script", script, "echo hi\n```\necho more\n```")
}
func TestGenerateFenceLastCloseWins(t *testing.T) {
	// With a wrapper fence, the payload ends at the last fence inside
	// the script; any chatter after it is cut.
	script, err := generate(t, "SCRIPT\n```\necho one\n```\necho two\n```\nHope that helps!")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	assertEqual(t, "script", script, "echo one\n```\necho two")
}
func TestGenerateChatterAroundReply(t *testing.T) {
	// Chatter before the tag and after the closing fence is ignored.
	script, err := generate(t, "Sure, here it is:\nSCRIPT\n```\necho hi\n```\nHope that helps!")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	assertEqual(t, "script", script, "echo hi")
}

func TestGenerateChatterAfterUnfencedScript(t *testing.T) {
	// Without a fence, the script runs to the end of the reply.
	script, err := generate(t, "SCRIPT\necho hi\n")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	assertEqual(t, "script", script, "echo hi")
}

func TestGenerateTagsInsideScript(t *testing.T) {
	// SCRIPT and ERROR lines are valid script content once the body
	// started; they must not confuse the parser.
	script, err := generate(t, "SCRIPT\necho SCRIPT\nERROR: not a real error\n")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	assertEqual(t, "script", script, "echo SCRIPT\nERROR: not a real error")
}

func TestGenerateErrorReply(t *testing.T) {
	_, err := generate(t, "ERROR cannot move a file to itself: /file/path")
	if err == nil || !strings.Contains(err.Error(), "cannot move a file to itself") {
		t.Fatalf("expected model error, got %v", err)
	}
}

func TestGenerateUnexpectedReply(t *testing.T) {
	_, err := generate(t, "Sure! Here's your script:\necho hi")
	if err == nil || !strings.Contains(err.Error(), "unexpected model output") {
		t.Fatalf("expected unexpected-output error, got %v", err)
	}
}

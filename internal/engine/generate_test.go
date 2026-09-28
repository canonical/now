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
	"reflect"
	"strings"
	"testing"

	"context"
	"github.com/canonical/now/internal/api/completions"
	"github.com/canonical/now/internal/engine"
	"github.com/canonical/now/internal/prompt"
	"github.com/canonical/now/internal/setup"
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
	script, err := generate(t, "---SCRIPT-START---\necho hello\n---SCRIPT-END---\n")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	assertEqual(t, "script", script, "echo hello")
}

func TestGenerateScriptChatterOutsideTokens(t *testing.T) {
	// Chatter before the starting token and after the ending token is
	// ignored.
	script, err := generate(t, "Sure, here it is:\n---SCRIPT-START---\necho hello\n---SCRIPT-END---\nHope that helps!")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	assertEqual(t, "script", script, "echo hello")
}

func TestGenerateScriptFencesInsideAreContent(t *testing.T) {
	// Code block fences inside the tokens are ordinary script content.
	script, err := generate(t, "---SCRIPT-START---\ncat <<'EOF' >> f\nhi\nEOF\n```\necho more\n```\n---SCRIPT-END---")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	assertEqual(t, "script", script, "cat <<'EOF' >> f\nhi\nEOF\n```\necho more\n```")
}

func TestGenerateTokensInsideScriptAreContent(t *testing.T) {
	// Only the first starting token and the last ending token delimit;
	// token-like lines in between are ordinary script content.
	script, err := generate(t, "---SCRIPT-START---\necho '---SCRIPT-END---'\n---SCRIPT-END---")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	assertEqual(t, "script", script, "echo '---SCRIPT-END---'")
}

func TestGenerateErrorReply(t *testing.T) {
	_, err := generate(t, "---ERROR-START---\ncannot move a file to itself: /file/path\n---ERROR-END---")
	if err == nil || !strings.Contains(err.Error(), "cannot move a file to itself") {
		t.Fatalf("expected model error, got %v", err)
	}
}

func TestGenerateErrorReplyChatter(t *testing.T) {
	// The error content is carried verbatim, chatter outside ignored.
	_, err := generate(t, "Sorry:\n---ERROR-START---\nrequired command 'python3' is not builtin (see -c)\n---ERROR-END---\nGood luck!")
	if err == nil || !strings.Contains(err.Error(), "required command 'python3' is not builtin") {
		t.Fatalf("expected model error, got %v", err)
	}
}

func TestGenerateScriptPreferredOverError(t *testing.T) {
	// When both tokens appear, the first one in the reply wins.
	script, err := generate(t, "---SCRIPT-START---\necho hello\n---SCRIPT-END---\n---ERROR-START---\nnope\n---ERROR-END---")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	assertEqual(t, "script", script, "echo hello")
}

func TestGenerateEmptyScript(t *testing.T) {
	_, err := generate(t, "---SCRIPT-START---\n---SCRIPT-END---")
	if err == nil || !strings.Contains(err.Error(), "empty script") {
		t.Fatalf("expected empty script error, got %v", err)
	}
}

func TestGenerateMissingEndToken(t *testing.T) {
	// A starting token without its ending counterpart is an error.
	_, err := generate(t, "---SCRIPT-START---\necho hello\n")
	if err == nil || !strings.Contains(err.Error(), "unexpected model output") {
		t.Fatalf("expected unexpected-output error, got %v", err)
	}
}

func TestGenerateUnexpectedReply(t *testing.T) {
	_, err := generate(t, "Sure! Here's your script:\necho hi")
	if err == nil || !strings.Contains(err.Error(), "unexpected model output") {
		t.Fatalf("expected unexpected-output error, got %v", err)
	}
}

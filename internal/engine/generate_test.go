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

// generateFormat runs Generate with a non-sh format, exercising the
// OUTPUT reply protocol.
func generateFormat(t *testing.T, format, reply string) (string, error) {
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
		Format:  format,
		Complete: func(ctx context.Context, messages []prompt.Message) (string, error) {
			return completions.Complete(ctx, opts, messages)
		},
	})
}

// generateOutput runs Generate for a dumped script (Output true, Format
// "sh") and returns the fake so the caller can inspect the recorded
// request body.
func generateOutput(t *testing.T, reply string) (*completions.FakeLLM, string, error) {
	t.Helper()
	f := completions.NewFakeLLM(reply)
	url, err := f.Start()
	if err != nil {
		t.Fatalf("cannot start fake: %v", err)
	}
	t.Cleanup(func() { _ = f.Stop() })
	opts := setup.Options{APIURL: url, APIModel: "test"}
	content, err := engine.Generate(context.Background(), engine.GenerateOptions{
		Request: "do something",
		Args:    []string{"a.txt"},
		Format:  "sh",
		Output:  true,
		Complete: func(ctx context.Context, messages []prompt.Message) (string, error) {
			return completions.Complete(ctx, opts, messages)
		},
	})
	return f, content, err
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

func TestGenerateFormatOutput(t *testing.T) {
	content, err := generateFormat(t, "json", "---OUTPUT-START---\n{\"a\":1}\n---OUTPUT-END---\n")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	assertEqual(t, "content", content, "{\"a\":1}")
}

func TestGenerateFormatChatterOutsideTokens(t *testing.T) {
	content, err := generateFormat(t, "json", "Sure:\n---OUTPUT-START---\n{\"a\":1}\n---OUTPUT-END---\nDone!")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	assertEqual(t, "content", content, "{\"a\":1}")
}

func TestGenerateFormatEmptyOutput(t *testing.T) {
	_, err := generateFormat(t, "json", "---OUTPUT-START---\n---OUTPUT-END---")
	if err == nil || !strings.Contains(err.Error(), "empty output") {
		t.Fatalf("expected empty output error, got %v", err)
	}
}

func TestGenerateFormatMissingEndToken(t *testing.T) {
	_, err := generateFormat(t, "json", "---OUTPUT-START---\n{\"a\":1}\n")
	if err == nil || !strings.Contains(err.Error(), "unexpected model output") {
		t.Fatalf("expected unexpected-output error, got %v", err)
	}
}

func TestGenerateFormatErrorReply(t *testing.T) {
	_, err := generateFormat(t, "json", "---ERROR-START---\ncannot perform request: bad\n---ERROR-END---")
	if err == nil || !strings.Contains(err.Error(), "cannot perform request: bad") {
		t.Fatalf("expected model error, got %v", err)
	}
}

func TestGenerateFormatIgnoresScriptToken(t *testing.T) {
	// In format mode a SCRIPT token is not a valid success marker;
	// it is unexpected output.
	_, err := generateFormat(t, "json", "---SCRIPT-START---\necho hi\n---SCRIPT-END---")
	if err == nil || !strings.Contains(err.Error(), "unexpected model output") {
		t.Fatalf("expected unexpected-output error, got %v", err)
	}
}

func TestGenerateScriptIgnoresOutputToken(t *testing.T) {
	// In script mode an OUTPUT token is not a valid success marker.
	_, err := generate(t, "---OUTPUT-START---\necho hi\n---OUTPUT-END---")
	if err == nil || !strings.Contains(err.Error(), "unexpected model output") {
		t.Fatalf("expected unexpected-output error, got %v", err)
	}
}

func TestGenerateOutputDropsArgAt(t *testing.T) {
	// A dumped script (Output true) still uses the SCRIPT reply
	// protocol, but the user message presents args as plain DATA,
	// not as "$@" with $1/$2 indexing.
	f, content, err := generateOutput(t, "---SCRIPT-START---\necho hello\n---SCRIPT-END---\n")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	assertEqual(t, "content", content, "echo hello")

	req := f.LastRequest()
	if req == nil {
		t.Fatalf("no request recorded")
	}
	messages, _ := req["messages"].([]any)
	if len(messages) != 2 {
		t.Fatalf("len(messages) = %d, want 2", len(messages))
	}
	sys, _ := messages[0].(map[string]any)["content"].(string)
	user, _ := messages[1].(map[string]any)["content"].(string)
	// The system message is still the script prompt.
	if !strings.Contains(sys, "---SCRIPT-START---") {
		t.Errorf("dumped script should use the script prompt: %q", sys)
	}
	// The user message drops "$@" and uses a plain DATA block.
	if strings.Contains(user, "$@") {
		t.Errorf("dumped script user message should not mention $@: %q", user)
	}
	if !strings.Contains(user, "## DATA\n```\na.txt\n```") {
		t.Errorf("dumped script user message should carry plain data: %q", user)
	}
}

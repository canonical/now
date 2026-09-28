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

package completions_test

import (
	"bytes"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/canonical/now/internal/api/completions"
)

func assertEqual[T any](t *testing.T, label string, got, want T) {
	t.Helper()
	if !reflect.DeepEqual(got, want) {
		t.Errorf("%s = %#v, want %#v", label, got, want)
	}
}

func startFake(t *testing.T, reply string) (*completions.FakeLLM, string) {
	t.Helper()
	f := completions.NewFakeLLM(reply)
	url, err := f.Start()
	if err != nil {
		t.Fatalf("cannot start fake: %v", err)
	}
	t.Cleanup(func() { _ = f.Stop() })
	return f, url
}

func TestFakeStartAndBaseURL(t *testing.T) {
	_, url := startFake(t, "ok")
	if !strings.HasPrefix(url, "http://127.0.0.1:") || strings.HasSuffix(url, "/v1") {
		t.Errorf("unexpected base URL %q", url)
	}
}

func TestFakeChatCompletionsReply(t *testing.T) {
	_, url := startFake(t, "world")
	resp := postChatCompletions(t, url, map[string]any{
		"model":    "fakellm",
		"messages": []map[string]any{{"role": "user", "content": "hello"}},
	})
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d", resp.StatusCode)
	}
	assertContent(t, resp, "world")
}

func TestFakeRecordsRequests(t *testing.T) {
	f, url := startFake(t, "ok")
	if f.LastRequest() != nil || len(f.Requests()) != 0 {
		t.Fatalf("expected no requests before any call")
	}
	postChatCompletions(t, url, map[string]any{"messages": []map[string]any{{"content": "first"}}})
	postChatCompletions(t, url, map[string]any{"messages": []map[string]any{{"content": "second"}}})

	reqs := f.Requests()
	if len(reqs) != 2 {
		t.Fatalf("recorded %d requests, want 2", len(reqs))
	}
	first := reqs[0]["messages"].([]any)[0].(map[string]any)["content"]
	second := reqs[1]["messages"].([]any)[0].(map[string]any)["content"]
	assertEqual(t, "first content", first, "first")
	assertEqual(t, "second content", second, "second")
	assertEqual(t, "last content", f.LastRequest()["messages"].([]any)[0].(map[string]any)["content"], "second")
}

func TestFakeRequestsReturnsCopy(t *testing.T) {
	f, url := startFake(t, "ok")
	postChatCompletions(t, url, map[string]any{"messages": []map[string]any{{"content": "a"}}})
	snap := f.Requests()
	postChatCompletions(t, url, map[string]any{"messages": []map[string]any{{"content": "b"}}})
	if len(snap) != 1 {
		t.Errorf("snapshot changed: %d", len(snap))
	}
	if len(f.Requests()) != 2 {
		t.Errorf("fresh snapshot = %d, want 2", len(f.Requests()))
	}
}

func TestFakeSetReply(t *testing.T) {
	f, url := startFake(t, "first")
	assertContent(t, postChatCompletions(t, url, map[string]any{"messages": []map[string]any{{"content": "q"}}}), "first")
	f.SetReply("second")
	assertContent(t, postChatCompletions(t, url, map[string]any{"messages": []map[string]any{{"content": "q"}}}), "second")
}

func TestFakeMethodNotAllowed(t *testing.T) {
	_, url := startFake(t, "ok")
	resp, err := http.Get(url + "/v1/chat/completions")
	if err != nil {
		t.Fatalf("cannot send: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusMethodNotAllowed {
		t.Errorf("status = %d, want 405", resp.StatusCode)
	}
}

func TestFakeBadRequestBody(t *testing.T) {
	f, url := startFake(t, "ok")
	resp, err := http.Post(url+"/v1/chat/completions", "application/json", strings.NewReader("not json"))
	if err != nil {
		t.Fatalf("cannot send: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", resp.StatusCode)
	}
	if len(f.Requests()) != 0 {
		t.Errorf("malformed request should not be recorded")
	}
}

func TestFakeStopIsIdempotent(t *testing.T) {
	f, _ := startFake(t, "ok")
	if err := f.Stop(); err != nil {
		t.Fatalf("first Stop: %v", err)
	}
	if err := f.Stop(); err != nil {
		t.Fatalf("second Stop: %v", err)
	}
}

func TestFakeStopRejectsNewRequests(t *testing.T) {
	f, url := startFake(t, "ok")
	if err := f.Stop(); err != nil {
		t.Fatalf("Stop: %v", err)
	}
	client := &http.Client{Timeout: 2 * time.Second}
	_, err := client.Post(url+"/v1/chat/completions", "application/json", bytes.NewReader([]byte(`{}`)))
	if err == nil {
		t.Errorf("request after Stop should fail to connect")
	}
}

func TestFakeRequestLog(t *testing.T) {
	f, url := startFake(t, "ok")
	logPath := filepath.Join(t.TempDir(), "log")
	if err := f.EnableRequestLog(logPath, "label"); err != nil {
		t.Fatalf("cannot enable log: %v", err)
	}
	postChatCompletions(t, url, map[string]any{"messages": []map[string]any{{"content": "a"}}})
	data, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatalf("cannot read log: %v", err)
	}
	if !strings.Contains(string(data), "[label]") || !strings.Contains(string(data), "=== end ===") {
		t.Errorf("unexpected log format: %q", data)
	}
}

func postChatCompletions(t *testing.T, baseURL string, body map[string]any) *http.Response {
	t.Helper()
	raw, err := json.Marshal(body)
	if err != nil {
		t.Fatalf("cannot encode body: %v", err)
	}
	resp, err := http.Post(baseURL+"/v1/chat/completions", "application/json", bytes.NewReader(raw))
	if err != nil {
		t.Fatalf("cannot post: %v", err)
	}
	return resp
}

func assertContent(t *testing.T, resp *http.Response, want string) {
	t.Helper()
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d", resp.StatusCode)
	}
	var out map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		t.Fatalf("cannot decode response: %v", err)
	}
	choices := out["choices"].([]any)
	msg := choices[0].(map[string]any)["message"].(map[string]any)
	assertEqual(t, "content", msg["content"], any(want))
}
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
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/canonical/now/internal/api/completions"
	"github.com/canonical/now/internal/prompt"
	"github.com/canonical/now/internal/setup"
)

func clientOpts(url string) setup.Options {
	return setup.Options{APIURL: url, APIKey: "s3cr3t", APIModel: "test-model"}
}

var testMessages = []prompt.Message{
	{Role: "system", Content: "sys"},
	{Role: "user", Content: "hello"},
}

func TestCompleteSuccess(t *testing.T) {
	f, url := startFake(t, "-$-SCRIPT-START-$-\necho hi-$-SCRIPT-END-$-\n")
	got, err := completions.Complete(context.Background(), clientOpts(url), testMessages)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	assertEqual(t, "content", got, "-$-SCRIPT-START-$-\necho hi-$-SCRIPT-END-$-\n")

	// The recorded request must carry the model and the messages.
	req := f.LastRequest()
	assertEqual(t, "model", req["model"], "test-model")
	msgs := req["messages"].([]any)
	assertEqual(t, "len(messages)", len(msgs), 2)
	assertEqual(t, "system message", msgs[0].(map[string]any)["content"], "sys")
	assertEqual(t, "user message", msgs[1].(map[string]any)["content"], "hello")
}

func TestCompleteHTTPError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "boom", http.StatusInternalServerError)
	}))
	defer srv.Close()

	_, err := completions.Complete(context.Background(), setup.Options{APIURL: srv.URL, APIModel: "m"}, testMessages)
	if err == nil || !strings.Contains(err.Error(), "500") {
		t.Fatalf("expected HTTP error, got %v", err)
	}
}

func TestCompleteMalformedJSON(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("not json"))
	}))
	defer srv.Close()

	_, err := completions.Complete(context.Background(), setup.Options{APIURL: srv.URL, APIModel: "m"}, testMessages)
	if err == nil || !strings.Contains(err.Error(), "cannot parse response") {
		t.Fatalf("expected parse error, got %v", err)
	}
}

func TestCompleteNoChoices(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"choices":[]}`))
	}))
	defer srv.Close()

	_, err := completions.Complete(context.Background(), setup.Options{APIURL: srv.URL, APIModel: "m"}, testMessages)
	if err == nil || !strings.Contains(err.Error(), "no choices") {
		t.Fatalf("expected no-choices error, got %v", err)
	}
}

func TestCompleteUnreachable(t *testing.T) {
	_, err := completions.Complete(context.Background(), setup.Options{APIURL: "http://127.0.0.1:1", APIModel: "m"}, testMessages)
	if err == nil || !strings.Contains(err.Error(), "cannot reach") {
		t.Fatalf("expected reach error, got %v", err)
	}
}

func TestCompleteCanceled(t *testing.T) {
	// A canceled context aborts the request cleanly.
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := completions.Complete(ctx, clientOpts("http://127.0.0.1:1"), testMessages)
	if err == nil || !strings.Contains(err.Error(), "cannot reach") {
		t.Fatalf("expected reach error, got %v", err)
	}
}

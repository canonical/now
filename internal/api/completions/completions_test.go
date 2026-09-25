package completions_test

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/niemeyer/now/internal/api/completions"
	"github.com/niemeyer/now/internal/prompt"
)

func clientOpts(url string) completions.Options {
	return completions.Options{URL: url, Key: "s3cr3t", Model: "test-model"}
}

var testMessages = []prompt.Message{
	{Role: "system", Content: "sys"},
	{Role: "user", Content: "hello"},
}

func TestCompleteSuccess(t *testing.T) {
	f, url := startFake(t, "SCRIPT\necho hi")
	got, err := completions.Complete(clientOpts(url), testMessages)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	assertEqual(t, "content", got, "SCRIPT\necho hi")

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

	_, err := completions.Complete(completions.Options{URL: srv.URL, Model: "m"}, testMessages)
	if err == nil || !strings.Contains(err.Error(), "500") {
		t.Fatalf("expected HTTP error, got %v", err)
	}
}

func TestCompleteMalformedJSON(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("not json"))
	}))
	defer srv.Close()

	_, err := completions.Complete(completions.Options{URL: srv.URL, Model: "m"}, testMessages)
	if err == nil || !strings.Contains(err.Error(), "cannot parse response") {
		t.Fatalf("expected parse error, got %v", err)
	}
}

func TestCompleteNoChoices(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"choices":[]}`))
	}))
	defer srv.Close()

	_, err := completions.Complete(completions.Options{URL: srv.URL, Model: "m"}, testMessages)
	if err == nil || !strings.Contains(err.Error(), "no choices") {
		t.Fatalf("expected no-choices error, got %v", err)
	}
}

func TestCompleteUnreachable(t *testing.T) {
	_, err := completions.Complete(completions.Options{URL: "http://127.0.0.1:1", Model: "m"}, testMessages)
	if err == nil || !strings.Contains(err.Error(), "cannot reach") {
		t.Fatalf("expected reach error, got %v", err)
	}
}
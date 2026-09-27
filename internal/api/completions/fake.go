// Copyright (c) 2026 Canonical Inc.
// Originally by Gustavo Niemeyer.
// Use of source code is governed by the MIT-style license in the LICENSE file.

package completions

import (
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// FakeLLM is a tiny completions-compatible HTTP server that answers
// POST /v1/chat/completions with a canned response. It exists so tests can
// exercise the full client without a real model API — fast, deterministic,
// no API key, no network.
//
// The server records every request body it receives, giving tests precise
// observability of what the client sends. The reply is a fixed string,
// customizable via SetReply.
type FakeLLM struct {
	server *http.Server
	port   int
	addr   string

	mu       sync.Mutex
	reply    string
	requests []map[string]any // recorded request bodies, in arrival order

	// logPath is the file to append each received request to, and label
	// is written into each log entry header. Empty logPath disables
	// file logging.
	logPath string
	label   string
}

// NewFakeLLM creates a FakeLLM whose reply is the given string. Call Start
// to bind a listener and begin serving.
func NewFakeLLM(reply string) *FakeLLM {
	f := &FakeLLM{reply: reply}
	mux := http.NewServeMux()
	mux.HandleFunc("/v1/chat/completions", f.handleChatCompletions)
	f.server = &http.Server{
		Handler:      mux,
		ReadTimeout:  10 * time.Second,
		WriteTimeout: 10 * time.Second,
	}
	return f
}

// EnableRequestLog enables appending every received request body to the
// file at logPath, tagged with label, and truncates the log. This gives a
// persistent record of the conversation for later inspection.
func (f *FakeLLM) EnableRequestLog(logPath, label string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.logPath = logPath
	f.label = label
	return os.WriteFile(logPath, nil, 0o600)
}

// Start binds the server to a free loopback port and begins serving in a
// goroutine. Returns the base URL (ending in /v1) to point the client at.
func (f *FakeLLM) Start() (string, error) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return "", fmt.Errorf("cannot listen: %w", err)
	}
	f.port = ln.Addr().(*net.TCPAddr).Port
	f.addr = ln.Addr().String()
	go func() { _ = f.server.Serve(ln) }()
	return f.BaseURL(), nil
}

// BaseURL returns the API base URL (without the /v1 prefix, which the
// client appends internally).
func (f *FakeLLM) BaseURL() string {
	return "http://" + f.addr
}

// Port returns the TCP port the server is listening on.
func (f *FakeLLM) Port() int {
	return f.port
}

// SetReply changes the canned reply string. Safe to call between requests.
func (f *FakeLLM) SetReply(reply string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.reply = reply
}

// Requests returns a snapshot of the recorded request bodies, in arrival
// order.
func (f *FakeLLM) Requests() []map[string]any {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]map[string]any, len(f.requests))
	copy(out, f.requests)
	return out
}

// LastRequest returns the most recently recorded request body, or nil if
// none yet.
func (f *FakeLLM) LastRequest() map[string]any {
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.requests) == 0 {
		return nil
	}
	return f.requests[len(f.requests)-1]
}

// Stop shuts the server down, releasing the listener.
func (f *FakeLLM) Stop() error {
	return f.server.Close()
}

func (f *FakeLLM) handleChatCompletions(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	var body map[string]any
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		http.Error(w, "bad request body: "+err.Error(), http.StatusBadRequest)
		return
	}

	f.mu.Lock()
	f.requests = append(f.requests, body)
	reply := f.reply
	logPath := f.logPath
	label := f.label
	f.mu.Unlock()

	if logPath != "" {
		f.appendRequestLog(logPath, label, body)
	}

	resp := map[string]any{
		"id":      "chatcmpl-fake",
		"object":  "chat.completion",
		"created": time.Now().Unix(),
		"model":   "fakellm",
		"choices": []map[string]any{
			{
				"index": 0,
				"message": map[string]any{
					"role":    "assistant",
					"content": reply,
				},
				"finish_reason": "stop",
			},
		},
		"usage": map[string]any{
			"prompt_tokens":     0,
			"completion_tokens": 0,
			"total_tokens":      0,
		},
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(resp)
}

// appendRequestLog appends a single request body to the log file, preceded
// by a header with the label, a timestamp, and a separator line. Errors
// are intentionally swallowed: logging is best-effort.
func (f *FakeLLM) appendRequestLog(logPath, label string, body map[string]any) {
	bodyJSON, err := json.MarshalIndent(body, "", "  ")
	if err != nil {
		return
	}
	_ = os.MkdirAll(filepath.Dir(logPath), 0o755)
	header := fmt.Sprintf("\n=== [%s] %s request #%d ===\n", label, time.Now().UTC().Format(time.RFC3339), len(f.requests))
	entry := header + string(bodyJSON) + "\n=== end ===\n"
	fp, err := os.OpenFile(logPath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		return
	}
	defer fp.Close()
	_, _ = fp.WriteString(entry)
}
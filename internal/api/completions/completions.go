// Package completions talks to an OpenAI-compatible chat completions API.
package completions

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/niemeyer/now/internal/prompt"
)

// Options configures a client call.
type Options struct {
	// URL is the API base URL, e.g. http://127.0.0.1:11434. The /v1
	// prefix is appended internally.
	URL string
	// Key is the API key, sent as a bearer token when set.
	Key string
	// Model is the model name sent to the API.
	Model string
}

// request is the body sent to /v1/chat/completions.
type request struct {
	Model    string           `json:"model"`
	Messages []prompt.Message `json:"messages"`
}

// response is the subset of the completion response we consume.
type response struct {
	Choices []struct {
		Message struct {
			Content string `json:"content"`
		} `json:"message"`
	} `json:"choices"`
}

// Complete sends the messages to the API and returns the content of the
// first choice.
func Complete(opts Options, messages []prompt.Message) (string, error) {
	body, err := json.Marshal(request{Model: opts.Model, Messages: messages})
	if err != nil {
		return "", fmt.Errorf("cannot encode request: %w", err)
	}

	req, err := http.NewRequest(http.MethodPost, strings.TrimSuffix(opts.URL, "/")+"/v1/chat/completions", bytes.NewReader(body))
	if err != nil {
		return "", fmt.Errorf("cannot create request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	if opts.Key != "" {
		req.Header.Set("Authorization", "Bearer "+opts.Key)
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("cannot reach %s: %w", opts.URL, err)
	}
	defer resp.Body.Close()

	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("cannot read response: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("API returned %s: %s", resp.Status, strings.TrimSpace(string(data)))
	}

	var out response
	if err := json.Unmarshal(data, &out); err != nil {
		return "", fmt.Errorf("cannot parse response: %w", err)
	}
	if len(out.Choices) == 0 {
		return "", fmt.Errorf("response has no choices")
	}
	return out.Choices[0].Message.Content, nil
}
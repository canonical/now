// Package completions talks to an OpenAI-compatible chat completions API.
package completions

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/niemeyer/now/internal/prompt"
	"github.com/niemeyer/now/internal/setup"
)

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

// Complete sends the messages to the API described by opts and returns the
// content of the first choice.
func Complete(ctx context.Context, opts setup.Options, messages []prompt.Message) (string, error) {
	body, err := json.Marshal(request{Model: opts.APIModel, Messages: messages})
	if err != nil {
		return "", fmt.Errorf("cannot encode request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, strings.TrimSuffix(opts.APIURL, "/")+"/v1/chat/completions", bytes.NewReader(body))
	if err != nil {
		return "", fmt.Errorf("cannot create request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	if opts.APIKey != "" {
		req.Header.Set("Authorization", "Bearer "+opts.APIKey)
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("cannot reach %s: %w", opts.APIURL, err)
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
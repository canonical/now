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

	"github.com/canonical/now/internal/prompt"
	"github.com/canonical/now/internal/setup"
)

// request is the body sent to /v1/chat/completions: the fields
// now owns, with the passthrough settings overlaid on top by
// the custom MarshalJSON below.
type request struct {
	Model       string                     `json:"model"`
	Messages    []prompt.Message           `json:"messages"`
	Passthrough map[string]json.RawMessage `json:"-"`
}

// MarshalJSON spells the request as one object: the passthrough
// keys first, then model and messages overlaid on top - anything
// now owns wins over the passthrough.
func (r request) MarshalJSON() ([]byte, error) {
	merged := make(map[string]json.RawMessage, len(r.Passthrough)+2)
	for key, val := range r.Passthrough {
		merged[key] = val
	}
	if r.Model != "" {
		val, err := json.Marshal(r.Model)
		if err != nil {
			return nil, err
		}
		merged["model"] = val
	}
	if len(r.Messages) > 0 {
		val, err := json.Marshal(r.Messages)
		if err != nil {
			return nil, err
		}
		merged["messages"] = val
	}
	return json.Marshal(merged)
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
	body, err := json.Marshal(request{Model: opts.APIModel, Messages: messages, Passthrough: opts.Passthrough})
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

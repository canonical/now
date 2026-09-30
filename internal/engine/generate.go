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

// Package engine generates scripts from requests and runs them.
package engine

import (
	"context"
	"fmt"
	"strings"

	"github.com/canonical/now/internal/busybox"
	"github.com/canonical/now/internal/prompt"
)

// GenerateOptions carries the inputs for script generation.
type GenerateOptions struct {
	Request  string
	Args     []string
	Commands []prompt.Command
	// Busybox is the resolved busybox that will run the script, used
	// to tell the model which applets it has.
	Busybox busybox.Options
	// Format selects the output mode. Empty or "sh" generate a script
	// (the SCRIPT reply protocol). Any other value generates content
	// in that format (the OUTPUT reply protocol). See prompt.Build.
	Format string
	// Output marks a script that will be written out rather than
	// executed. It is meaningful only for the script prompt (Format
	// empty or "sh"): it drops the "$@" framing and run-time notes,
	// since the script never runs. See prompt.BuildOptions.Output.
	Output bool
	// Complete dispatches the prompt messages to the model API chosen
	// by the caller and returns its reply.
	Complete func(ctx context.Context, messages []prompt.Message) (string, error)
}

// Generate assembles the prompt for the request and returns the script
// or formatted content produced by the model.
func Generate(ctx context.Context, opts GenerateOptions) (string, error) {
	messages := prompt.Build(prompt.BuildOptions{
		Request:  opts.Request,
		Args:     opts.Args,
		Commands: opts.Commands,
		Applets:  opts.Busybox.Applets,
		Format:   opts.Format,
		Output:   opts.Output,
	})
	reply, err := opts.Complete(ctx, messages)
	if err != nil {
		return "", err
	}
	return parseReply(reply, opts.Format)
}

// parseReply extracts the script or formatted content from a model
// reply, which must be delimited by explicit tokens:
//
//	---SCRIPT-START--- ... ---SCRIPT-END--- carries the script (script mode).
//	---OUTPUT-START--- ... ---OUTPUT-END--- carries the content (format mode).
//	---ERROR-START--- ... ---ERROR-END--- carries the failure (both modes).
//
// The markers are plain literal lines, not markdown fences: smaller
// models respect a single unadorned delimiter line far more reliably
// than nested or decorated delimiters. See the busyboxPrompt const in
// internal/prompt for the model-facing side of this contract.
//
// The first starting token in the reply selects the kind; the content
// is everything between it and the last matching ending token, with
// chatter outside the tokens ignored. The format selects which
// success token is expected: empty or "sh" is script mode, any other
// value is output mode, and a success token of the wrong kind is
// treated as unexpected output so the two contracts stay distinct.
// Any other case is an error.
func parseReply(reply, format string) (string, error) {
	const (
		scriptStart = "---SCRIPT-START---"
		scriptEnd   = "---SCRIPT-END---"
		outputStart = "---OUTPUT-START---"
		outputEnd   = "---OUTPUT-END---"
		errorStart  = "---ERROR-START---"
		errorEnd    = "---ERROR-END---"
	)

	start, end := scriptStart, scriptEnd
	if format != "" && format != "sh" {
		start, end = outputStart, outputEnd
	}

	si, ei := strings.Index(reply, start), strings.Index(reply, errorStart)
	if si == -1 && ei == -1 {
		return "", errUnexpected
	}
	kind := start
	if si == -1 || (ei != -1 && ei < si) {
		kind = errorStart
		si = ei
		end = errorEnd
	}

	ej := strings.LastIndex(reply, end)
	if ej <= si+len(kind) {
		return "", errUnexpected
	}
	content := strings.TrimSpace(reply[si+len(kind) : ej])

	if kind == errorStart {
		if content == "" {
			return "", errUnexpected
		}
		return "", fmt.Errorf("%s", content)
	}
	if content == "" {
		what := "script"
		if format != "" && format != "sh" {
			what = "output"
		}
		return "", fmt.Errorf("cannot create %s: model returned an empty %s", what, what)
	}
	return content, nil
}

var errUnexpected = fmt.Errorf("cannot perform request: unexpected model output")
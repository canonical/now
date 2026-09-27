// Copyright (c) 2026 Canonical Inc.
// Originally by Gustavo Niemeyer.
// Use of source code is governed by the MIT-style license in the LICENSE file.

// Package engine generates scripts from requests and runs them.
package engine

import (
	"context"
	"fmt"
	"strings"

	"github.com/niemeyer/now/internal/busybox"
	"github.com/niemeyer/now/internal/prompt"
)

// GenerateOptions carries the inputs for script generation.
type GenerateOptions struct {
	Request  string
	Args     []string
	Commands []prompt.Command
	// Busybox is the resolved busybox that will run the script, used
	// to tell the model which applets it has.
	Busybox busybox.Options
	// Complete dispatches the prompt messages to the model API chosen
	// by the caller and returns its reply.
	Complete func(ctx context.Context, messages []prompt.Message) (string, error)
}

// Generate assembles the prompt for the request and returns the script
// produced by the model.
func Generate(ctx context.Context, opts GenerateOptions) (string, error) {
	messages := prompt.Build(prompt.BuildOptions{
		Request:  opts.Request,
		Args:     opts.Args,
		Commands: opts.Commands,
		Applets:  opts.Busybox.Applets,
	})
	reply, err := opts.Complete(ctx, messages)
	if err != nil {
		return "", err
	}
	return parseReply(reply)
}

// parseReply extracts the script or the failure reason from a model
// reply, which must be delimited by explicit tokens:
//
//	-$-SCRIPT-START-$- ... -$-SCRIPT-END-$- carries the script.
//	-$-ERROR-START-$- ... -$-ERROR-END-$- carries the failure.
//
// The first starting token in the reply selects the kind; the content
// is everything between it and the last matching ending token, with
// chatter outside the tokens ignored. Any other case is an error.
func parseReply(reply string) (string, error) {
	const (
		scriptStart = "-$-SCRIPT-START-$-"
		scriptEnd   = "-$-SCRIPT-END-$-"
		errorStart  = "-$-ERROR-START-$-"
		errorEnd    = "-$-ERROR-END-$-"
	)

	si, ei := strings.Index(reply, scriptStart), strings.Index(reply, errorStart)
	if si == -1 && ei == -1 {
		return "", errUnexpected
	}
	start, end := scriptStart, scriptEnd
	if si == -1 || (ei != -1 && ei < si) {
		start, end = errorStart, errorEnd
		si = ei
	}

	ej := strings.LastIndex(reply, end)
	if ej <= si+len(start) {
		return "", errUnexpected
	}
	content := strings.TrimSpace(reply[si+len(start) : ej])

	if start == errorStart {
		if content == "" {
			return "", errUnexpected
		}
		return "", fmt.Errorf("%s", content)
	}
	if content == "" {
		return "", fmt.Errorf("cannot create script: model returned an empty script")
	}
	return content, nil
}

var errUnexpected = fmt.Errorf("cannot create script: unexpected model output")
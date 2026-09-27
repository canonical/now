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

// parseReply extracts the script or the failure reason from a model reply.
//
// The reply must contain a SCRIPT or ERROR tag on a line of its own. Any
// chatter before the tag is ignored, and any chatter after it is either
// more content for the SCRIPT or irrelevant for ERROR. Further SCRIPT or
// ERROR tags aren't special.
// 
// The logic below attempts to be resilient to models injecting code block
// fences surrounding the script, right before, or right after the SCRIPT
// tag. This is harder than it sounds because the model may have chosen
// to use an unrelated code block before the SCRIPT started, in which case
// it must also be closed before it starts, and the SCRIPT itself may contain
// unrelated fences inside. Despite that complexity, the rules are simple:
//
// - If we find a fence being opened right before or after the SCRIPT tag,
//   we'll close the script on the last fence found.
//
// - If there is no fence being opened right around the SCRIPT tag, we'll
//   not fiddle with fences inside the script and will build it until the end.
//
// - If we find a SCRIPT tag inside a code block, but the code block has
//   content before the SCRIPT tag, we'll ignore that tag as it makes no sense.
//   
func parseReply(reply string) (string, error) {
	var (
		script     []string
		fenceOpen  bool // The ``` is currently open and needs closing. Does not open after the script is running.
		fenceDirty bool // The ``` has unrelated content before the SCRIPT tag.
		fenceClose int  // The ``` was properly opened for a script, and this is the last closing fence seen.
	)
	for _, line := range strings.Split(reply, "\n") {
		trimmed := strings.TrimSpace(line)
		switch {
		case strings.HasPrefix(trimmed, "```"):
			if fenceOpen {
				if script != nil {
					fenceClose = len(script)
					script = append(script, line)
				} else {
					fenceOpen = false
					fenceDirty = false
				}
			} else if len(script) == 0 {
				fenceOpen = true
			} else {
				script = append(script, line)
			}
		case script != nil:
			script = append(script, line)
		case script == nil && trimmed == "SCRIPT":
			if !fenceDirty {
				script = []string{}
			}
		case script == nil && strings.HasPrefix(trimmed, "ERROR "):
			reason := strings.TrimSpace(strings.TrimPrefix(trimmed, "ERROR "))
			if reason == "" {
				return "", errUnexpected
			}
			return "", fmt.Errorf("cannot perform the request: %s", reason)
		case script == nil:
			if fenceOpen && trimmed != "" {
				fenceDirty = true
			}
		}
	}
	if script == nil {
		return "", errUnexpected
	}
	if len(script) == 0 {
		return "", fmt.Errorf("cannot create script: model returned an empty script")
	}
	if fenceClose > 0 {
		script = script[:fenceClose]
	}
	return strings.TrimRight(strings.Join(script, "\n"), "\n"), nil
}

var errUnexpected = fmt.Errorf("cannot create script: unexpected model output")
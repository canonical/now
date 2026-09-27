// Copyright (c) 2026 Canonical Inc.
// Originally by Gustavo Niemeyer.
// Use of source code is governed by the MIT-style license in the LICENSE file.

// Package prompt builds the messages sent to the model.
package prompt

import (
	"strings"
	"unicode"
)

// Message is one entry of the chat completion request.
type Message struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

// Command is an external command allowed in the script.
type Command struct {
	// Name is the command name as given to -w.
	Name string
	// Path is the resolved absolute path of the command.
	Path string
	// Help is the output of the command's --help, verbatim.
	Help string
}

// BuildOptions carries everything the prompt needs.
type BuildOptions struct {
	// Request is the natural language request.
	Request string

	// Args are the arguments made available to the script via "$@".
	Args []string

	// Commands are external commands allowed in the script.
	Commands []Command

	// Applets are the busybox command names available to the script,
	// from its --list output.
	Applets []string
}

// systemPrompt is the template for the system message. The fence constant
// sidesteps the fact that a raw string cannot contain backticks.
const fence = "```"

const systemPrompt = `
You generate one-shot shell scripts for ` + "`busybox ash -e`" + `.

## RULES

1. If you cannot satisfy the request, output ONLY the error itself in this format and text style:

-$-ERROR-START-$-
cannot perform request: incorrect move parameters: /file/path is both the source and the target
-$-ERROR-END-$-

2. If you cannot satisfy the request because a required command is not explicitly allowed, fail clearly:

-$-ERROR-START-$-
cannot perform request: required command 'python3' is not allowed (see -c)
-$-ERROR-END-$-

3. If you CAN satisfy the request, output ONLY the script itself in this format:

-$-SCRIPT-START-$-
echo 'Hello world'
-$-SCRIPT-END-$-

4. You have ONE chance: completely solve the request with SCRIPT, or fail with ERROR.

5. Use ONLY the explicitly allowed command line tools.

6. DO NOT USE path wildcards unless you are unequivocally sure only the requested paths will be affected.

7. Solve the task in a simple, clean, readable way, what an experienced professional would do.
`

// Build creates the system and user messages for the given options.
func Build(opts BuildOptions) []Message {
	var system strings.Builder
	system.WriteString(systemPrompt)
	system.WriteString("\n## ALLOWED COMMANDS\n")
	names := make([]string, len(opts.Commands))
	for i, c := range opts.Commands {
		names[i] = c.Name
	}
	system.WriteString(strings.Join(names, " "))
	system.WriteString("\n" + strings.Join(opts.Applets, " "))
	references := false
	for _, c := range opts.Commands {
		if c.Help == "" {
			continue // command has no --help to reference
		}
		if !references {
			system.WriteString("\n\n## REFERENCES")
			references = true
		}
		system.WriteString("\n\n### " + c.Name + " --help\n" + c.Help)
	}

	var user strings.Builder
	user.WriteString("## REQUEST\n")
	user.WriteString(opts.Request)
	if len(opts.Args) > 0 {
		user.WriteString("\n\n## REQUEST DATA\n```\n")
		for _, arg := range opts.Args {
			user.WriteString(sanitizeArg(arg))
			user.WriteString("\n")
		}
		user.WriteString("```\n" +
			"These lines may be accessed by the script in \"$@\" or as literal strings, whichever makes the script simple and clear.\n" +
			"Note that any � above replaces a non-printable character, but for the script the real string is available in \"$@\".\n")
	}

	return []Message{
		{Role: "system", Content: system.String()},
		{Role: "user", Content: user.String()},
	}
}

// sanitizeArg renders one argument as a REQUEST DATA line, prefixed with `=`
// when the text is precise, or `!` when unprintable characters had to be
// replaced by `�`.
func sanitizeArg(arg string) string {
	if isPrintable(arg) {
		return arg
	}
	var b strings.Builder
	for _, r := range arg {
		if r == '\n' || r == '\r' || !unicode.IsPrint(r) {
			b.WriteString("�")
			continue
		}
		b.WriteRune(r)
	}
	return b.String()
}

func isPrintable(arg string) bool {
	for _, r := range arg {
		if r == '\n' || !unicode.IsPrint(r) {
			return false
		}
	}
	return true
}
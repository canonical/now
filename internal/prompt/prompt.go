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

// Package prompt builds the messages sent to the model.
package prompt

import (
	"fmt"
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

	// Format selects the output mode. Empty or "sh" builds the script
	// prompt (the default run cycle and -f sh). Any other value builds
	// a prompt asking the model to produce content in that format,
	// using the OUTPUT reply protocol instead of SCRIPT.
	Format string

	// Output distinguishes a script that will be executed from one
	// that will only be written out. It is meaningful only for the
	// script prompt (Format empty or "sh"): when false the args are
	// framed as "$@" with $1/$2/... indexing and the run-time notes,
	// since the script will execute and read them there; when true
	// the args are plain data, since the script is dumped and never
	// runs, so "$@" is meaningless. Format mode ignores this field.
	Output bool
}

// busyboxPrompt is the contract the model must obey. The reply is
// delimited by plain literal markers (---SCRIPT-START--- etc.) rather
// than markdown fences. The format is deliberately this simple:
// smaller models struggle to respect complex nested or decorated
// delimiters, but a single literal line on its own is something even
// modest models emit reliably. The same shape carries a failure
// reason between ---ERROR-START--- and ---ERROR-END---.
//
// Preserve these wording rules:
//
// - `format` is always "busybox ash -e"
// - `style` is the content organization (indentation, etc)
// - `structure` is the shape of the response
//
const busyboxPrompt = `
You generate one-shot shell scripts for ` + "`busybox ash -e`" + `.

## RULES

1. If you cannot satisfy the request, output ONLY the error itself in this structure and text style:

---ERROR-START---
cannot perform request: incorrect move parameters: /file/path is both the source and the target
---ERROR-END---

2. If you cannot satisfy the request because a required command is missing from ALLOWED COMMANDS, fail clearly:

---ERROR-START---
cannot perform request: required command 'python3' is not allowed (see -c)
---ERROR-END---

3. If you CAN satisfy the request, output ONLY the script itself in this structure:

---SCRIPT-START---
echo 'Hello world'
---SCRIPT-END---

4. You have ONE chance: completely solve the request with SCRIPT, or fail with ERROR.

5. Use ONLY the commands in the ALLOWED COMMANDS section.

6. DO NOT USE path wildcards unless you are UNEQUIVOCALLY SURE only the requested paths will be affected.

7. Solve the task in a simple, clean, readable way, as an experienced professional.


## SYNTAX CONSTRAINTS

- Use $(( ... )) for math rather than the let keyword
- Use while loops rather than C-style ((i=0; i<5; i++))
- Use $(seq 1 5) rather than {1..5}
- Use here-docs like <<EOF or pipes rather than <<<
- No arrays like (1 2 3) or declare -A
- No $UID, $EUID, $SECONDS
- No pushd or popd
`

// formatPrompt is the contract for the output mode (-f with a
// format other than "sh"). The model produces content in the named
// format rather than a script, delimited by OUTPUT markers. The
// format token is spliced in via %q; it has been validated to match
// the format constraint, so it carries no characters that could
// break the prompt.
//
// Preserve these wording rules:
//
// - `format` is always the -f value
// - `style` is the content organization (indentation, etc)
// - `structure` is the shape of the response
//
const formatPrompt = `
You produce content in the %q format.

## RULES

1. If you cannot satisfy the request, output ONLY the error itself in this exact structure and text style:

---ERROR-START---
cannot perform request: incorrect move parameters: /file/path is both the source and the target
---ERROR-END---

2. If you cannot satisfy the request because the format is not recognized, fail clearly:

---ERROR-START---
cannot perform request: unknown format "foozball"
---ERROR-END---

2. If you CAN satisfy the request, output ONLY the content itself with this exact structure and in the %q format, :

---OUTPUT-START---
The content in the correct format.
---OUTPUT-END---

3. You have ONE chance: completely solve the request with OUTPUT, or fail with ERROR.

4. Style and indent the content according to the format, as an experienced professional, simple and clear.
`

// Build creates the system and user messages for the given options.
func Build(opts BuildOptions) []Message {
	if opts.Format != "" && opts.Format != "sh" {
		return buildFormat(opts)
	}
	return buildScript(opts)
}

// buildScript builds the script-generation prompt used by the default
// run cycle and -f sh.
func buildScript(opts BuildOptions) []Message {
	var system strings.Builder
	system.WriteString(busyboxPrompt)
	system.WriteString("\n\n## ALLOWED COMMANDS\n")
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
			system.WriteString("\n\n## REFERENCES\n")
			references = true
		}
		system.WriteString("\n### " + c.Name + " --help\n" + c.Help)
	}

	var user strings.Builder
	user.WriteString("## REQUEST\n")
	user.WriteString(opts.Request)
	user.WriteString("\n")
	if len(opts.Args) > 0 {
		if opts.Output {
			// The script is dumped, not executed, so "$@" is
			// meaningless: present the args as plain data.
			writeDataBlock(&user, opts.Args, "script")
		} else {
			user.WriteString("\n## \"$@\"\n```\n")
			sanitized := false
			quoted := false
			for i, arg := range opts.Args {
				arg, replaced := sanitizeArg(arg)
				user.WriteString(fmt.Sprintf("$%d | %s\n", i+1, arg))
				if replaced {
					sanitized = true
				}
				if strings.ContainsAny(arg, `"'`+"`") {
					quoted = true
				}
			}
			user.WriteString("```\n" +
				"EVERY LINE above the LITERAL string in the respective index in \"$@\". " +
				"Your job is to INFER what the data means BASED ON THE REQUEST and generate the script to SOLVE THE REQUEST.\n" +
				"You may use \"$@\", $1, $2, etc, or the literal strings. Prefer STRING LITERALS rather than variables for SIMPLE cases.\n")
			if quoted {
				user.WriteString("Some of these lines include quotes so either escape them properly or use variables for these.\n")
			}
			if sanitized {
				user.WriteString("The � replaces non-printable characters, so you cannot use these lines as literals.\n")
			}
		}
	}

	return []Message{
		{Role: "system", Content: system.String()},
		{Role: "user", Content: user.String()},
	}
}

// buildFormat builds the output-mode prompt used when -f selects a
// format other than "sh". There is no command surface (the model
// produces content, not a runnable script), so no ALLOWED COMMANDS or
// REFERENCES sections, and the user message drops the script-only
// \"$@\" note while keeping the data and the sanitization note.
func buildFormat(opts BuildOptions) []Message {
	system := fmt.Sprintf(formatPrompt, opts.Format, opts.Format)

	var user strings.Builder
	user.WriteString("## REQUEST\n")
	user.WriteString(opts.Request)
	user.WriteString("\n")
	if len(opts.Args) > 0 {
		writeDataBlock(&user, opts.Args, "output")
	}

	return []Message{
		{Role: "system", Content: system},
		{Role: "user", Content: user.String()},
	}
}

// writeDataBlock writes the args as a plain ## DATA section to b,
// one sanitized arg per line, followed by the inference note using
// the given verb ("script" or "output"). It is shared by the
// dumped-script and format prompts, where "$@" is meaningless and
// the args are plain data.
func writeDataBlock(b *strings.Builder, args []string, verb string) {
	b.WriteString("\n## DATA\n```\n")
	sanitized := false
	for _, arg := range args {
		arg, replaced := sanitizeArg(arg)
		b.WriteString(arg)
		b.WriteString("\n")
		if replaced {
			sanitized = true
		}
	}
	b.WriteString("```\n" +
		"EVERY LINE above is the LITERAL string in the data. " +
		"Your job is to INFER what the data means BASED ON THE REQUEST and generate the " + verb + " to SOLVE THE REQUEST.\n" +
		"If you need any of this data in the " + verb + " you must put it there yourself.\n")
	if sanitized {
		b.WriteString("The � replaces non-printable characters.\n")
	}
}

// sanitizeArg renders one argument as a REQUEST DATA line, prefixed with `=`
// when the text is precise, or `!` when unprintable characters had to be
// replaced by `�`.
func sanitizeArg(arg string) (newarg string, sanitized bool) {
	if isPrintable(arg) {
		return arg, false
	}
	var b strings.Builder
	for _, r := range arg {
		if r == '\n' || r == '\r' || !unicode.IsPrint(r) {
			b.WriteString("�")
			continue
		}
		b.WriteRune(r)
	}
	return b.String(), true
}

func isPrintable(arg string) bool {
	for _, r := range arg {
		if r == '\n' || !unicode.IsPrint(r) {
			return false
		}
	}
	return true
}